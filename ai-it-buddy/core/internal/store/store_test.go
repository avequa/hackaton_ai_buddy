package store_test

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/avequa/ai-it-buddy/core/internal/domain"
	"github.com/avequa/ai-it-buddy/core/internal/seed"
	"github.com/avequa/ai-it-buddy/core/internal/store"
)

// Интеграционные тесты: нужен пустой Postgres, например
// TEST_DATABASE_URL=postgres://postgres@127.0.0.1:5433/buddy_test?sslmode=disable
// База очищается перед каждым тестом.
func openClean(t *testing.T) *store.Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newCore(t *testing.T, clk *clock) *domain.Core {
	t.Helper()
	sd, err := seed.Load("../../../data")
	if err != nil {
		t.Fatal(err)
	}
	c, err := domain.New(sd, domain.Options{ApprovalDelay: 10 * time.Second, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func flush(t *testing.T, s *store.Store, c *domain.Core) {
	t.Helper()
	ch, commit := c.TakeChanges()
	err := s.Save(context.Background(), ch)
	commit(err == nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := c.PendingChanges(); n != 0 {
		t.Fatalf("pending after flush = %d", n)
	}
}

// Ядро работает, состояние пишется пачками; после «перезапуска» новое ядро видит то же самое
func TestSaveLoadRoundTrip(t *testing.T) {
	s := openClean(t)
	ctx := context.Background()
	clk := &clock{t: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)}

	if _, initialized, err := s.Load(ctx); err != nil || initialized {
		t.Fatalf("fresh db: initialized=%v err=%v", initialized, err)
	}

	a := newCore(t, clk)
	a.MarkAllDirty()
	flush(t, s, a)

	acc := must[domain.AccessDraft](t)(a.DraftAccess("anna.intern", domain.AccessDetails{Service: "grafana", Project: "payments-dashboards", Level: "write", Justification: "поправить алерт"}))
	must[domain.Request](t)(a.Confirm("anna.intern", acc.Request.ID))
	eq := must[domain.EquipmentDraft](t)(a.DraftEquipment("anna.intern", domain.EquipmentDetails{Action: "issue", Item: "lg-27-qhd", Reason: "второй монитор"}))
	if eq.Request == nil {
		t.Fatalf("equipment draft = %+v", eq)
	}
	must[domain.Request](t)(a.Confirm("anna.intern", eq.Request.ID))
	flush(t, s, a) // промежуточная запись: заявки в работе, склад зарезервирован

	clk.t = clk.t.Add(11 * time.Second)
	a.Tick()
	must[domain.StepView](t)(a.CompleteStep("anna.intern", "anna.intern", "meet-team"))
	inc := must[domain.Incident](t)(a.OpenIncident("sergey.sre", "vpn", "Недоступен шлюз VPN", "Работайте через резервный шлюз"))
	a.RecordSecurity("pavel.intern", domain.SecForeign, "REQ-1")
	a.TrackTopic("anna.intern", "VPN")
	a.TrackTopic("anna.intern", "VPN")
	a.TrackKBMiss("anna.intern")
	a.OfferStarterPack("pavel.intern")
	notes := a.Notifications("anna.intern", 0)
	if len(notes) == 0 {
		t.Fatal("no notifications for anna")
	}
	a.MarkRead("anna.intern", notes[len(notes)-1].ID)
	must[domain.Incident](t)(a.ResolveIncident("sergey.sre", inc.ID))
	flush(t, s, a)

	st, initialized, err := s.Load(ctx)
	if err != nil || !initialized {
		t.Fatalf("load: initialized=%v err=%v", initialized, err)
	}
	b := newCore(t, clk)
	b.Restore(st)

	for _, u := range []string{"anna.intern", "pavel.intern", "igor.mentor", "sergey.sre"} {
		if x, y := a.MyRequests(u, ""), b.MyRequests(u, ""); !equalJSON(t, x, y) {
			t.Fatalf("%s requests differ:\n%+v\n%+v", u, x, y)
		}
		if x, y := a.Notifications(u, 0), b.Notifications(u, 0); !equalJSON(t, x, y) {
			t.Fatalf("%s notifications differ:\n%+v\n%+v", u, x, y)
		}
		xg, _, _ := a.MyAccess(u)
		yg, _, _ := b.MyAccess(u)
		if !equalJSON(t, xg, yg) {
			t.Fatalf("%s grants differ:\n%+v\n%+v", u, xg, yg)
		}
	}
	for _, view := range []func(*domain.Core) any{
		func(c *domain.Core) any { i, h, _ := c.Equipment("anna.intern", ""); return []any{i, h} },
		func(c *domain.Core) any { o, _ := c.Onboarding("anna.intern"); return o },
		func(c *domain.Core) any { return c.Incidents() },
		func(c *domain.Core) any { return c.SecurityEvents("igor.mentor") },
		func(c *domain.Core) any { d, _ := c.Digest("igor.mentor", "anna.intern"); return d },
	} {
		if x, y := view(a), view(b); !equalJSON(t, x, y) {
			t.Fatalf("views differ:\n%+v\n%+v", x, y)
		}
	}
	if b.OfferStarterPack("pavel.intern") {
		t.Fatal("starter pack offered twice after restart")
	}

	// после восстановления номера продолжаются, а не начинаются заново
	next := must[domain.TicketDraft](t)(b.DraftTicket("pavel.intern", domain.TicketDetails{Service: "jira", Summary: "не открывается доска", StepsTried: []string{"перелогин"}}))
	if next.Request == nil || slices.ContainsFunc(st.Requests, func(r domain.Request) bool { return r.ID == next.Request.ID }) {
		t.Fatalf("id reused: %+v", next)
	}
	flush(t, s, b)
	st2, _, err := s.Load(ctx)
	if err != nil || len(st2.Requests) != len(st.Requests)+1 || len(st2.Issued) != len(st.Issued) {
		t.Fatalf("second load: %d requests, %d issued, err=%v", len(st2.Requests), len(st2.Issued), err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := openClean(t)
	s.Close()
	again, err := store.Open(context.Background(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
}

func TestOpenTimesOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if _, err := store.Open(ctx, "postgres://nobody@127.0.0.1:1/x?sslmode=disable&connect_timeout=1"); err == nil {
		t.Fatal("expected error for unreachable db")
	}
	if _, err := store.Open(ctx, "::not a url"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestSearchKB(t *testing.T) {
	s := openClean(t)
	ctx := context.Background()
	c := newCore(t, &clock{t: time.Now()})
	if err := s.SyncDocs(ctx, c.Docs()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"не подключается VPN, ошибка timeout": "vpn-not-connecting",
		"впн очень медленно работает":         "vpn-slow",
		"как оформить отгул":                  "day-off",
		"хочу второй монитор":                 "equipment-how-to",
		"подключиться к впн":                  "vpn-not-connecting",
	}
	for q, want := range cases {
		hits, err := s.SearchKB(ctx, q, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) == 0 || hits[0].ID != want {
			t.Errorf("%q: want %s first, got %+v", q, want, ids(hits))
		}
	}
	for _, q := range []string{"", "?? и в на", "квантовая криптография"} {
		if hits, err := s.SearchKB(ctx, q, 3); err != nil || len(hits) != 0 {
			t.Errorf("%q: hits=%v err=%v", q, ids(hits), err)
		}
	}

	// документ убрали из data/kb - он пропадает и из поиска
	docs := slices.DeleteFunc(c.Docs(), func(d domain.Doc) bool { return d.ID == "day-off" })
	if err := s.SyncDocs(ctx, docs); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.SearchKB(ctx, "как оформить отгул", 3); slices.Contains(ids(hits), "day-off") {
		t.Fatal("stale doc still found")
	}

	// ядро ищет через Postgres; если база отвалилась - в памяти, с отметкой об ошибке
	var kbErrs int
	sd, _ := seed.Load("../../../data")
	core, _ := domain.New(sd, domain.Options{OnKBError: func(error) { kbErrs++ }})
	core.SetKB(s)
	if hits := core.SearchKB(ctx, "впн тормозит", 2); len(hits) == 0 || hits[0].ID != "vpn-slow" {
		t.Fatalf("core via pg: %v", ids(hits))
	}
	s.Close()
	if hits := core.SearchKB(ctx, "не подключается VPN", 2); len(hits) == 0 || kbErrs != 1 {
		t.Fatalf("fallback: hits=%v errs=%d", ids(hits), kbErrs)
	}
}

func ids(hits []domain.DocHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.ID)
	}
	return out
}
