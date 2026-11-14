package radar_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/avequa/ai-it-buddy/core/internal/radar"
)

type fakeAnalyzer struct{ err error }

func (f *fakeAnalyzer) Analyze(context.Context, string, time.Time, time.Time) (radar.Stats, error) {
	return radar.Stats{}, f.err
}

type scripted struct {
	byService map[string]radar.Stats
	err       error
}

func (s *scripted) Analyze(_ context.Context, svc string, _, _ time.Time) (radar.Stats, error) {
	return s.byService[svc], s.err
}

type fakeIncidents struct {
	open     map[string]bool
	manual   map[string]bool
	opened   []string
	resolved []string
}

func (f *fakeIncidents) AutoOpenIncident(svc, summary, msg string) (string, bool, error) {
	if svc == "broken" {
		return "", false, errors.New("boom")
	}
	if f.open[svc] {
		return "", false, nil
	}
	f.open[svc] = true
	f.opened = append(f.opened, svc+"|"+summary+"|"+msg)
	return "INC-" + svc, true, nil
}

func (f *fakeIncidents) AutoResolveIncident(svc string) (string, bool) {
	if !f.open[svc] || f.manual[svc] {
		return "", false
	}
	delete(f.open, svc)
	f.resolved = append(f.resolved, svc)
	return "INC-" + svc, true
}

type counter struct{ checks, events map[string]int }

func (c *counter) Checked(svc, result string, _ float64) { c.checks[svc+"/"+result]++ }
func (c *counter) Incident(e string)                     { c.events[e]++ }

func TestWatcher_Hysteresis(t *testing.T) {
	an := &scripted{byService: map[string]radar.Stats{}}
	inc := &fakeIncidents{open: map[string]bool{}, manual: map[string]bool{}}
	obs := &counter{checks: map[string]int{}, events: map[string]int{}}
	w := (&radar.Watcher{Analyzer: an, Incidents: inc, Observer: obs, Services: []string{"vpn", "gitlab"}}).Defaults()
	ctx := context.Background()
	set := func(svc string, req int, rate float64) {
		an.byService[svc] = radar.Stats{Requests: req, ErrorRate: rate, TopError: "/api/v1/connect"}
	}
	check := func() {
		t.Helper()
		if err := w.Check(ctx); err != nil {
			t.Fatal(err)
		}
	}

	set("vpn", 200, 0.01)
	set("gitlab", 5, 0.9) // мало трафика - не повод
	check()
	set("vpn", 200, 0.6)
	check()
	if len(inc.opened) != 0 {
		t.Fatal("opened after a single bad check")
	}
	set("vpn", 200, 0.1) // между порогами - счетчики сброшены
	check()
	set("vpn", 200, 0.6)
	check()
	if len(inc.opened) != 0 {
		t.Fatal("bad checks were not consecutive")
	}
	check()
	if len(inc.opened) != 1 || !strings.Contains(inc.opened[0], "60%") || !strings.Contains(inc.opened[0], "/api/v1/connect") {
		t.Fatalf("opened = %v", inc.opened)
	}
	check() // уже открыт - второй раз не открывается
	if len(inc.opened) != 1 {
		t.Fatal("opened twice")
	}

	set("vpn", 200, 0.02)
	check()
	check()
	if len(inc.resolved) != 0 {
		t.Fatal("resolved too early")
	}
	check()
	if len(inc.resolved) != 1 {
		t.Fatal("not resolved after three good checks")
	}
	if obs.events["opened"] != 1 || obs.events["resolved"] != 1 || obs.checks["gitlab/no_traffic"] == 0 || obs.checks["vpn/high"] == 0 {
		t.Fatalf("observer = %+v", obs)
	}

	// объявленный человеком сбой радар не закрывает
	inc.open["gitlab"], inc.manual["gitlab"] = true, true
	set("gitlab", 100, 0)
	for range 4 {
		check()
	}
	if !inc.open["gitlab"] {
		t.Fatal("radar closed a manual incident")
	}

	// ошибка ядра при открытии не роняет проверку
	w2 := (&radar.Watcher{Analyzer: &scripted{byService: map[string]radar.Stats{"broken": {Requests: 100, ErrorRate: 1}}}, Incidents: inc, Services: []string{"broken"}, OpenAfter: 1}).Defaults()
	if err := w2.Check(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWatcher_AnalyzerDown(t *testing.T) {
	inc := &fakeIncidents{open: map[string]bool{"vpn": true}, manual: map[string]bool{}}
	obs := &counter{checks: map[string]int{}, events: map[string]int{}}
	w := (&radar.Watcher{Analyzer: &fakeAnalyzer{err: errors.New("down")}, Incidents: inc, Observer: obs, Services: []string{"vpn"}}).Defaults()
	for range 5 {
		if err := w.Check(context.Background()); err == nil {
			t.Fatal("error expected")
		}
	}
	if !inc.open["vpn"] || obs.checks["vpn/error"] != 5 {
		t.Fatal("incident must stay as is while radar is down")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	w.Run(ctx, 10*time.Millisecond) // логирует и выходит по отмене
}

// fakeRadar - MCP-сервер с тем же контрактом analyze_logs, что у incident-radar
func fakeRadar(t *testing.T, token string) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var calls []map[string]any
	server := mcp.NewServer(&mcp.Implementation{Name: "incident-radar", Version: "test"}, nil)
	type in struct {
		Path  string `json:"path"`
		Since string `json:"since,omitempty"`
		Until string `json:"until,omitempty"`
		Top   int    `json:"top,omitempty"`
	}
	type errRes struct {
		Resource string `json:"resource"`
		Count    int    `json:"count"`
	}
	type out struct {
		TotalRequestsCount int      `json:"totalRequestsCount"`
		ErrorRate          float64  `json:"errorRate"`
		TopErrorResources  []errRes `json:"topErrorResources"`
	}
	mcp.AddTool(server, &mcp.Tool{Name: "analyze_logs"}, func(_ context.Context, _ *mcp.CallToolRequest, a in) (*mcp.CallToolResult, out, error) {
		calls = append(calls, map[string]any{"path": a.Path, "since": a.Since, "until": a.Until})
		if a.Path == "missing.log" {
			return nil, out{}, errors.New("no such file")
		}
		return nil, out{TotalRequestsCount: 120, ErrorRate: 0.42, TopErrorResources: []errRes{{"/api/v1/connect", 40}}}, nil
	})
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestMCPAnalyzer(t *testing.T) {
	srv, calls := fakeRadar(t, "secret")
	a := &radar.MCPAnalyzer{URL: srv.URL, Token: "secret", Version: "test"}
	defer a.Close()
	ctx := context.Background()
	until := time.Date(2026, 11, 14, 10, 0, 30, 0, time.FixedZone("MSK", 3*3600))
	st, err := a.Analyze(ctx, "vpn", until.Add(-30*time.Second), until)
	if err != nil {
		t.Fatal(err)
	}
	if st.Requests != 120 || st.ErrorRate != 0.42 || st.TopError != "/api/v1/connect" {
		t.Fatalf("stats = %+v", st)
	}
	if c := (*calls)[0]; c["path"] != "vpn.log" || c["since"] != "2026-11-14T07:00:00Z" || c["until"] != "2026-11-14T07:00:30Z" {
		t.Fatalf("call = %v", c)
	}
	if _, err := a.Analyze(ctx, "missing", until, until); err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("tool error = %v", err)
	}
	// после ошибки сессии - переподключение
	if _, err := a.Analyze(ctx, "vpn", until, until); err != nil {
		t.Fatal(err)
	}

	bad := &radar.MCPAnalyzer{URL: srv.URL, Token: "wrong"}
	if _, err := bad.Analyze(ctx, "vpn", until, until); err == nil {
		t.Fatal("wrong token accepted")
	}
	down := &radar.MCPAnalyzer{URL: "http://127.0.0.1:1/mcp"}
	if _, err := down.Analyze(ctx, "vpn", until, until); err == nil {
		t.Fatal("unreachable radar")
	}
}
