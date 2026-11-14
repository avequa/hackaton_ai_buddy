package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/avequa/ai-it-buddy/core/internal/domain"
	"github.com/avequa/ai-it-buddy/core/internal/metrics"
	"github.com/avequa/ai-it-buddy/core/internal/seed"
)

func cleanDB(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	conn, err := pgx.Connect(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(context.Background(), `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	return url
}

func newTestCore(t *testing.T) *domain.Core {
	t.Helper()
	sd, err := seed.Load("../../../data")
	if err != nil {
		t.Fatal(err)
	}
	c, err := domain.New(sd, domain.Options{ApprovalDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// База отвалилась: ядро продолжает работать, изменения копятся, /readyz показывает degraded
func TestPersister_SurvivesDBLoss(t *testing.T) {
	url := cleanDB(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := metrics.New()

	core := newTestCore(t)
	p, err := openStorage(ctx, url, 5*time.Second, core, m, logger)
	if err != nil {
		t.Fatal(err)
	}
	if r := p.ready(ctx); r.Status != "ok" || r.Pending != 0 || r.LastFlushAt == nil {
		t.Fatalf("ready = %+v", r)
	}
	d, err := core.DraftAccess("anna.intern", domain.AccessDetails{Service: "grafana", Project: "payments-dashboards", Level: "write", Justification: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.flush(ctx); err != nil {
		t.Fatal(err)
	}

	p.store.Close()
	if _, err = core.Confirm("anna.intern", d.Request.ID); err != nil {
		t.Fatal(err)
	}
	if err = p.flush(ctx); err == nil {
		t.Fatal("flush to closed pool succeeded")
	}
	if r := p.ready(ctx); r.Status != "degraded" || r.Pending == 0 || r.Error == "" {
		t.Fatalf("ready after loss = %+v", r)
	}

	// перезапуск: новое ядро видит черновик, но не подтверждение (оно не успело записаться)
	core2 := newTestCore(t)
	p2, err := openStorage(ctx, url, 5*time.Second, core2, m, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer p2.store.Close()
	r, err := core2.Request("anna.intern", d.Request.ID)
	if err != nil || r.Status != domain.StatusDraft {
		t.Fatalf("restored = %+v, %v", r, err)
	}
	if hits := core2.SearchKB(ctx, "впн тормозит", 1); len(hits) != 1 || hits[0].ID != "vpn-slow" {
		t.Fatalf("kb via postgres = %+v", hits)
	}

	// loop: изменение уходит в базу по тику и при остановке
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { p2.loop(loopCtx, 20*time.Millisecond); close(done) }()
	if _, err = core2.Confirm("anna.intern", d.Request.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	core2.TrackTopic("anna.intern", "VPN")
	cancel()
	<-done
	if n := core2.PendingChanges(); n != 0 {
		t.Fatalf("pending after stop = %d", n)
	}
	st, _, err := p2.store.Load(ctx)
	if err != nil || len(st.Topics) != 1 {
		t.Fatalf("topics = %+v, err = %v", st.Topics, err)
	}
}

// Полный запуск с DATABASE_URL: /readyz отвечает postgres
func TestRunWithPostgres(t *testing.T) {
	url := cleanDB(t)
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	cfg := config{
		Addr: addr, DataDir: "../../../data", JWTSecret: "test-secret-test-secret-test-secret",
		ApprovalDelay: time.Second, DigestEvery: time.Hour, DatabaseURL: url, DBWait: 5 * time.Second, FlushEvery: 50 * time.Millisecond,
	}
	go func() { done <- run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	deadline := time.Now().Add(10 * time.Second)
	for healthcheck(addr) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("core did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/readyz", http.NoBody)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || body["storage"] != "postgres" || body["status"] != "ok" {
		t.Fatalf("readyz = %d %v", resp.StatusCode, body)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	bad := cfg
	bad.DatabaseURL, bad.DBWait = "postgres://nobody@127.0.0.1:1/x?sslmode=disable&connect_timeout=1", time.Second
	if err := run(context.Background(), bad, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil || !strings.Contains(err.Error(), "postgres") {
		t.Fatalf("unreachable db: %v", err)
	}
}
