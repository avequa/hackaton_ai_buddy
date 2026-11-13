package mcpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/avequa/ai-it-buddy/core/internal/authn"
	"github.com/avequa/ai-it-buddy/core/internal/domain"
	"github.com/avequa/ai-it-buddy/core/internal/metrics"
	"github.com/avequa/ai-it-buddy/core/internal/seed"
)

// env - ядро, издатель токенов и HTTP-сервер со всеми MCP-серверами
type env struct {
	core   *domain.Core
	issuer *authn.Issuer
	srv    *httptest.Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	s, err := seed.Load("../../../data")
	if err != nil {
		t.Fatal(err)
	}
	core, err := domain.New(s, domain.Options{ApprovalDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := authn.NewIssuer("test-secret-test-secret-test-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	api := New(core, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)), "test")
	mux := http.NewServeMux()
	for _, name := range Servers {
		h, err := api.Handler(name, issuer)
		if err != nil {
			t.Fatal(err)
		}
		mux.Handle("/mcp/"+name, h)
	}
	if _, err := api.Handler("nope", issuer); err == nil {
		t.Fatal("unknown server accepted")
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &env{core: core, issuer: issuer, srv: srv}
}

// bearer - добавляет токен к каждому запросу клиента
type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// session - MCP-клиент пользователя к серверу name
func (e *env) session(t *testing.T, user, audience, name string) (*mcp.ClientSession, error) {
	t.Helper()
	u, err := e.core.User(user)
	if err != nil {
		t.Fatal(err)
	}
	token, err := e.issuer.Issue(u, audience, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: e.srv.URL + "/mcp/" + name, HTTPClient: &http.Client{Transport: bearer{token}}}
	s, err := client.Connect(context.Background(), transport, nil)
	if err == nil {
		t.Cleanup(func() { _ = s.Close() })
	}
	return s, err
}

// callTool - вызывает инструмент и возвращает structured content как map
func callTool(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (map[string]any, bool, string) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			text = tc.Text
		}
	}
	out := map[string]any{}
	if res.StructuredContent != nil {
		data, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(data, &out)
	}
	return out, res.IsError, text
}

func TestTools_List(t *testing.T) {
	e := newEnv(t)
	want := map[string][]string{
		"access":     {"draft_access_request", "list_access_requests", "list_my_access", "list_services"},
		"helpdesk":   {"check_service_health", "draft_ticket", "list_tickets", "search_kb"},
		"inventory":  {"draft_equipment_request", "list_equipment", "list_equipment_requests"},
		"onboarding": {"get_onboarding"},
	}
	for name, tools := range want {
		s, err := e.session(t, "anna.intern", authn.AudienceMCP, name)
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, tool := range res.Tools {
			got = append(got, tool.Name)
		}
		if strings.Join(got, ",") != strings.Join(tools, ",") {
			t.Errorf("%s tools = %v, want %v", name, got, tools)
		}
	}
}

func TestAuth_RejectsUIAndMissingTokens(t *testing.T) {
	e := newEnv(t)
	if _, err := e.session(t, "anna.intern", authn.AudienceUI, "access"); err == nil {
		t.Fatal("ui token accepted by MCP")
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, e.srv.URL+"/mcp/access", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
}

func TestAccessTools(t *testing.T) {
	e := newEnv(t)
	s, err := e.session(t, "anna.intern", authn.AudienceMCP, "access")
	if err != nil {
		t.Fatal(err)
	}
	out, isErr, _ := callTool(t, s, "list_services", nil)
	if isErr || len(out["services"].([]any)) != 6 {
		t.Fatalf("list_services = %v", out)
	}
	out, isErr, _ = callTool(t, s, "draft_access_request", map[string]any{
		"service": "grafana", "project": "payments-dashboards", "level": "write", "justification": "поправить алерт",
	})
	req, _ := out["request"].(map[string]any)
	if isErr || out["outcome"] != "draft_created" || req["status"] != "draft" || out["next_step"] == "" {
		t.Fatalf("draft = %v", out)
	}
	out, _, _ = callTool(t, s, "draft_access_request", map[string]any{
		"service": "k8s-prod", "project": "prod-cluster", "level": "read", "justification": "посмотреть поды",
	})
	if p := out["request"].(map[string]any)["policy"].(map[string]any); p["verdict"] != "needs_mentor" {
		t.Fatalf("policy = %v", p)
	}
	out, _, _ = callTool(t, s, "draft_access_request", map[string]any{
		"service": "vpn", "project": "corp", "level": "read", "justification": "x",
	})
	if out["outcome"] != "already_granted" {
		t.Fatalf("vpn = %v", out)
	}
	_, isErr, text := callTool(t, s, "draft_access_request", map[string]any{
		"service": "nope", "project": "x", "level": "read", "justification": "x",
	})
	if !isErr || !strings.Contains(text, "available") {
		t.Fatalf("unknown service: %v %s", isErr, text)
	}
	out, _, _ = callTool(t, s, "list_my_access", nil)
	if len(out["open_requests"].([]any)) != 2 || len(out["grants"].([]any)) != 2 {
		t.Fatalf("my access = %v", out)
	}
	out, _, _ = callTool(t, s, "list_access_requests", nil)
	if len(out["requests"].([]any)) != 2 {
		t.Fatalf("requests = %v", out)
	}
}

func TestHelpdeskInventoryOnboardingTools(t *testing.T) {
	e := newEnv(t)
	hd, err := e.session(t, "anna.intern", authn.AudienceMCP, "helpdesk")
	if err != nil {
		t.Fatal(err)
	}
	e.core.SetIncident("vpn", "VPN недоступен", true)
	out, _, _ := callTool(t, hd, "check_service_health", map[string]any{"service": "vpn"})
	if out["status"] != "incident" {
		t.Fatalf("health = %v", out)
	}
	out, _, _ = callTool(t, hd, "draft_ticket", map[string]any{"service": "vpn", "summary": "не подключается"})
	if out["outcome"] != "known_incident" {
		t.Fatalf("ticket during incident = %v", out)
	}
	e.core.SetIncident("vpn", "", false)
	out, _, _ = callTool(t, hd, "draft_ticket", map[string]any{"service": "vpn", "summary": "не подключается", "steps_tried": []string{"перезапуск"}})
	if out["outcome"] != "draft_created" {
		t.Fatalf("ticket = %v", out)
	}
	_, isErr, _ := callTool(t, hd, "draft_ticket", map[string]any{"service": "vpn", "summary": " "})
	if !isErr {
		t.Fatal("empty summary accepted")
	}
	out, _, _ = callTool(t, hd, "search_kb", map[string]any{"query": "vpn timeout"})
	if docs := out["docs"].([]any); len(docs) == 0 {
		t.Fatalf("kb = %v", out)
	}
	out, _, _ = callTool(t, hd, "search_kb", map[string]any{"query": "квантовая криптография"})
	if out["note"] == nil {
		t.Fatalf("kb miss = %v", out)
	}
	out, _, _ = callTool(t, hd, "list_tickets", nil)
	if len(out["requests"].([]any)) != 1 {
		t.Fatalf("tickets = %v", out)
	}

	inv, err := e.session(t, "anna.intern", authn.AudienceMCP, "inventory")
	if err != nil {
		t.Fatal(err)
	}
	out, _, _ = callTool(t, inv, "list_equipment", map[string]any{"category": "monitor"})
	if len(out["items"].([]any)) != 3 {
		t.Fatalf("equipment = %v", out)
	}
	out, _, _ = callTool(t, inv, "draft_equipment_request", map[string]any{"action": "issue", "item": "dell-27-4k", "reason": "второй монитор"})
	if out["outcome"] != "out_of_stock" || len(out["alternatives"].([]any)) != 2 {
		t.Fatalf("out of stock = %v", out)
	}
	out, _, _ = callTool(t, inv, "draft_equipment_request", map[string]any{"action": "issue", "item": "lg-27-qhd", "reason": "второй монитор"})
	if out["outcome"] != "draft_created" {
		t.Fatalf("monitor = %v", out)
	}
	_, isErr, _ = callTool(t, inv, "draft_equipment_request", map[string]any{"action": "steal", "item": "lg-27-qhd", "reason": "x"})
	if !isErr {
		t.Fatal("unknown action accepted")
	}
	out, _, _ = callTool(t, inv, "list_equipment_requests", nil)
	if len(out["requests"].([]any)) != 1 {
		t.Fatalf("equipment requests = %v", out)
	}

	ob, err := e.session(t, "anna.intern", authn.AudienceMCP, "onboarding")
	if err != nil {
		t.Fatal(err)
	}
	out, _, _ = callTool(t, ob, "get_onboarding", nil)
	if out["total"].(float64) != 11 || out["next"] == nil {
		t.Fatalf("onboarding = %v", out)
	}
}

func TestToolNamesMatchServers(t *testing.T) {
	e := newEnv(t)
	for name, want := range ToolNames {
		s, err := e.session(t, "anna.intern", authn.AudienceMCP, name)
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Tools) != len(want) {
			t.Errorf("%s: ToolNames is out of date: %d tools registered, %d listed", name, len(res.Tools), len(want))
		}
	}
}

func TestUserID_NoToken(t *testing.T) {
	if _, err := userID(nil); err == nil {
		t.Fatal("nil request accepted")
	}
	if _, err := userID(&mcp.CallToolRequest{}); err == nil {
		t.Fatal("request without token accepted")
	}
	if errorOutcome(io.EOF) != "error" || errorOutcome(domain.ErrConflict) != "conflict" {
		t.Fatal("error outcomes")
	}
}
