package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	envs := map[string]string{"JWT_SECRET": "x", "APPROVAL_DELAY": "5s", "CORE_ADDR": ":9"}
	cfg, err := loadConfig(func(k string) string { return envs[k] })
	if err != nil || cfg.ApprovalDelay != 5*time.Second || cfg.Addr != ":9" || cfg.DataDir != "./data" || cfg.LogFormat != "json" {
		t.Fatalf("cfg = %+v, err = %v", cfg, err)
	}
	envs["APPROVAL_DELAY"] = "soon"
	if _, err := loadConfig(func(k string) string { return envs[k] }); err == nil {
		t.Fatal("bad delay accepted")
	}
	if _, err := loadConfig(func(string) string { return "" }); err == nil {
		t.Fatal("missing JWT_SECRET accepted")
	}
	if newLogger("text") == nil || newLogger("json") == nil {
		t.Fatal("logger")
	}
	t.Setenv("CORE_TEST_ENV", "v")
	if env("CORE_TEST_ENV", "d") != "v" || env("CORE_TEST_MISSING", "d") != "d" {
		t.Fatal("env")
	}
}

func TestRun(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	cfg := config{
		Addr: addr, DataDir: "../../../data", JWTSecret: "test-secret-test-secret-test-secret", AgentURL: "http://127.0.0.1:1",
		ApprovalDelay: time.Second, LogFormat: "text",
	}
	go func() { done <- run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))) }()

	deadline := time.Now().Add(5 * time.Second)
	for healthcheck(addr) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("core did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/api/personas", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	time.Sleep(1100 * time.Millisecond) // один тик имитации согласующих
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if healthcheck("bad-addr") == 0 || healthcheck(addr) == 0 {
		t.Fatal("healthcheck must fail without a server")
	}
	for name, c := range map[string]config{
		"data":   {Addr: addr, DataDir: "/nope", JWTSecret: cfg.JWTSecret, ApprovalDelay: time.Second},
		"secret": {Addr: addr, DataDir: "../../../data", JWTSecret: "short", ApprovalDelay: time.Second},
		"listen": {Addr: "256.0.0.1:1", DataDir: "../../../data", JWTSecret: cfg.JWTSecret, ApprovalDelay: time.Second},
	} {
		if err := run(context.Background(), c, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
