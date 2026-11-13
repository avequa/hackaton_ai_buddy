package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/avequa/ai-it-buddy/core/internal/authn"
	"github.com/avequa/ai-it-buddy/core/internal/domain"
	"github.com/avequa/ai-it-buddy/core/internal/mcpapi"
	"github.com/avequa/ai-it-buddy/core/internal/metrics"
	"github.com/avequa/ai-it-buddy/core/internal/seed"
)

// fakeAgent - вместо Python-агента: проверяет MCP-токен и создает черновик, как сделал бы агент через инструмент
type fakeAgent struct {
	t      *testing.T
	core   *domain.Core
	issuer *authn.Issuer
	fail   bool
	last   AgentRequest
}

func (f *fakeAgent) Chat(_ context.Context, req AgentRequest) (AgentReply, error) {
	f.last = req
	if f.fail {
		return AgentReply{}, errors.New("agent down")
	}
	claims, err := f.issuer.Verify(req.Token, authn.AudienceMCP)
	if err != nil {
		f.t.Errorf("agent got a token that is not an MCP token: %v", err)
	}
	if _, uiErr := f.issuer.Verify(req.Token, authn.AudienceUI); uiErr == nil {
		f.t.Error("agent token must not work as a UI session token")
	}
	_, err = f.core.DraftAccess(claims.Subject, domain.AccessDetails{Service: "grafana", Project: "payments-dashboards", Level: "write", Justification: req.Message})
	if err != nil {
		f.t.Error(err)
	}
	return AgentReply{Reply: "Подготовила заявку", ToolsUsed: []string{"list_services", "draft_access_request"}}, nil
}

// env - тестовый сервер API
type env struct {
	srv   *httptest.Server
	core  *domain.Core
	agent *fakeAgent
}

func newEnv(t *testing.T, withAgent bool) *env {
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := metrics.New()
	e := &env{core: core, agent: &fakeAgent{t: t, core: core, issuer: issuer}}
	cfg := Config{Core: core, Issuer: issuer, MCP: mcpapi.New(core, m, logger, "test"), Metrics: m, Logger: logger, DemoToken: "demo"}
	if withAgent {
		cfg.Agent = e.agent
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.srv = httptest.NewServer(h)
	t.Cleanup(e.srv.Close)
	return e
}

// do - запрос к API, ответ как map
func (e *env) do(t *testing.T, method, path, token, body string, hdr ...string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

// login - токен сессии персоны
func (e *env) login(t *testing.T, user string) string {
	t.Helper()
	code, out := e.do(t, "POST", "/api/auth/login", "", `{"user_id":"`+user+`"}`)
	if code != http.StatusOK {
		t.Fatalf("login %s: %d %v", user, code, out)
	}
	return out["token"].(string)
}

func TestFlow_ChatDraftConfirmMentor(t *testing.T) {
	e := newEnv(t, true)
	anna, igor := e.login(t, "anna.intern"), e.login(t, "igor.mentor")

	code, out := e.do(t, "POST", "/api/chat", anna, `{"message":"нужно поправить алерт в дашборде платежей"}`)
	drafts := out["drafts"].([]any)
	if code != http.StatusOK || out["reply"] == "" || len(drafts) != 1 || out["thread_id"] != "default" {
		t.Fatalf("chat: %d %v", code, out)
	}
	if e.agent.last.ThreadID != "anna.intern:default" || e.agent.last.User.Team != "payments" {
		t.Fatalf("agent request = %+v", e.agent.last)
	}
	id := drafts[0].(map[string]any)["id"].(string)

	if got, _ := e.do(t, "POST", "/api/requests/"+id+"/confirm", e.login(t, "pavel.intern"), ""); got != http.StatusNotFound {
		t.Fatalf("other user confirmed: %d", code)
	}
	code, out = e.do(t, "POST", "/api/requests/"+id+"/confirm", anna, "")
	if code != http.StatusOK || out["status"] != "in_review" {
		t.Fatalf("confirm: %d %v", code, out)
	}
	if got, _ := e.do(t, "POST", "/api/requests/"+id+"/confirm", anna, ""); got != http.StatusConflict {
		t.Fatalf("double confirm: %d", code)
	}
	if got, body := e.do(t, "GET", "/api/requests/"+id, igor, ""); got != http.StatusOK || len(body["history"].([]any)) != 2 {
		t.Fatalf("mentor view: %d %v", code, out)
	}
	if got, body := e.do(t, "GET", "/api/requests?kind=access", anna, ""); got != http.StatusOK || len(body["requests"].([]any)) != 1 {
		t.Fatalf("list: %d %v", code, out)
	}

	// заявка не по роли: стажер -> наставник
	d, err := e.core.DraftAccess("anna.intern", domain.AccessDetails{Service: "k8s-prod", Project: "prod-cluster", Level: "read", Justification: "поды"})
	if err != nil {
		t.Fatal(err)
	}
	if got, body := e.do(t, "POST", "/api/requests/"+d.Request.ID+"/confirm", anna, ""); got != http.StatusOK || body["status"] != "pending_mentor" {
		t.Fatalf("not by role: %d %v", code, out)
	}
	code, out = e.do(t, "GET", "/api/approvals", igor, "")
	if code != http.StatusOK || len(out["approvals"].([]any)) != 1 {
		t.Fatalf("approvals: %d %v", code, out)
	}
	if got, _ := e.do(t, "POST", "/api/approvals/"+d.Request.ID+"/maybe", igor, ""); got != http.StatusNotFound {
		t.Fatalf("bad decision: %d", code)
	}
	if got, _ := e.do(t, "POST", "/api/approvals/"+d.Request.ID+"/reject", anna, ""); got != http.StatusNotFound {
		t.Fatalf("intern decided own approval: %d", code)
	}
	code, out = e.do(t, "POST", "/api/approvals/"+d.Request.ID+"/reject", igor, `{"comment":"логи смотри в Grafana"}`)
	if code != http.StatusOK || out["status"] != "rejected" || out["resolution"] != "логи смотри в Grafana" {
		t.Fatalf("reject: %d %v", code, out)
	}

	// отмена
	d, _ = e.core.DraftAccess("anna.intern", domain.AccessDetails{Service: "jira", Project: "PAY", Level: "write", Justification: "задачи"})
	if code, out := e.do(t, "POST", "/api/requests/"+d.Request.ID+"/cancel", anna, ""); code != http.StatusOK || out["status"] != "cancelled" {
		t.Fatalf("cancel: %d %v", code, out)
	}
}

func TestOnboardingAndProfile(t *testing.T) {
	e := newEnv(t, false)
	anna, igor := e.login(t, "anna.intern"), e.login(t, "igor.mentor")
	if code, out := e.do(t, "GET", "/api/me", anna, ""); code != http.StatusOK || out["id"] != "anna.intern" {
		t.Fatalf("me: %d %v", code, out)
	}
	if code, _ := e.do(t, "POST", "/api/onboarding/steps/meet-team/complete", anna, ""); code != http.StatusOK {
		t.Fatalf("self step: %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/onboarding/steps/setup-env/complete", anna, ""); code != http.StatusForbidden {
		t.Fatalf("intern completed mentor step: %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/mentees/anna.intern/steps/setup-env/complete", igor, ""); code != http.StatusOK {
		t.Fatalf("mentor step: %d", code)
	}
	code, out := e.do(t, "GET", "/api/onboarding", anna, "")
	if code != http.StatusOK || out["done"].(float64) != 3 {
		t.Fatalf("onboarding: %d %v", code, out)
	}
	if code, out := e.do(t, "GET", "/api/personas", "", ""); code != http.StatusOK || len(out["personas"].([]any)) != 5 {
		t.Fatalf("personas: %d", code)
	}
}

func TestErrorsAndSecurity(t *testing.T) {
	e := newEnv(t, false)
	anna := e.login(t, "anna.intern")
	cases := []struct {
		method, path, token, body string
		code                      int
	}{
		{"GET", "/api/me", "", "", http.StatusUnauthorized},
		{"GET", "/api/me", "garbage", "", http.StatusUnauthorized},
		{"POST", "/api/auth/login", "", `{"user_id":"ghost"}`, http.StatusNotFound},
		{"POST", "/api/auth/login", "", `{"user":"x"}`, http.StatusBadRequest},
		{"POST", "/api/chat", anna, `{"message":"привет"}`, http.StatusServiceUnavailable},
		{"POST", "/api/chat", anna, `{"message":"   "}`, http.StatusBadRequest},
		{"GET", "/api/requests/ACC-9999", anna, "", http.StatusNotFound},
		{"POST", "/api/demo/incidents", "", `{"service":"vpn","active":true}`, http.StatusNotFound},
		{"GET", "/healthz", "", "", http.StatusOK},
		{"GET", "/metrics", "", "", http.StatusOK},
	}
	for _, c := range cases {
		if code, out := e.do(t, c.method, c.path, c.token, c.body); code != c.code {
			t.Errorf("%s %s: %d, want %d (%v)", c.method, c.path, code, c.code, out)
		}
	}
	code, out := e.do(t, "POST", "/api/demo/incidents", "", `{"service":"vpn","title":"VPN лежит","active":true}`, "X-Demo-Token", "demo")
	if code != http.StatusOK || out["status"] != "incident" {
		t.Fatalf("demo incident: %d %v", code, out)
	}
	if code, _ := e.do(t, "POST", "/api/demo/incidents", "", `{"active":true}`, "X-Demo-Token", "demo"); code != http.StatusBadRequest {
		t.Fatalf("demo without service: %d", code)
	}

	// MCP-токен агента не открывает API интерфейса
	u, _ := e.core.User("anna.intern")
	iss, _ := authn.NewIssuer("test-secret-test-secret-test-secret", nil)
	mcpToken, _ := iss.Issue(u, authn.AudienceMCP, time.Minute)
	if code, _ := e.do(t, "GET", "/api/me", mcpToken, ""); code != http.StatusUnauthorized {
		t.Fatalf("mcp token opened UI API: %d", code)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, e.srv.URL+"/metrics", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !bytes.Contains(body, []byte("buddy_access_denied_total")) || !bytes.Contains(body, []byte(`route="GET /api/me"`)) {
		t.Fatal("metrics are missing")
	}
}

func TestChat_AgentDown(t *testing.T) {
	e := newEnv(t, true)
	e.agent.fail = true
	code, out := e.do(t, "POST", "/api/chat", e.login(t, "anna.intern"), `{"message":"привет","thread_id":"t1"}`)
	if code != http.StatusBadGateway || !strings.Contains(out["error"].(string), "недоступен") {
		t.Fatalf("agent down: %d %v", code, out)
	}
}

func TestHTTPAgent(t *testing.T) {
	var got AgentRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		switch got.Message {
		case "500":
			http.Error(w, "boom", http.StatusInternalServerError)
		case "bad":
			_, _ = io.WriteString(w, "not json")
		default:
			_, _ = io.WriteString(w, `{"reply":"ок","tools_used":["search_kb"]}`)
		}
	}))
	defer srv.Close()
	a := &HTTPAgent{URL: srv.URL + "/", Client: srv.Client()}
	reply, err := a.Chat(context.Background(), AgentRequest{Message: "hi", ThreadID: "u:t"})
	if err != nil || reply.Reply != "ок" || got.ThreadID != "u:t" {
		t.Fatalf("reply = %+v, err = %v", reply, err)
	}
	for _, msg := range []string{"500", "bad"} {
		if _, err := a.Chat(context.Background(), AgentRequest{Message: msg}); err == nil {
			t.Errorf("%s: no error", msg)
		}
	}
	srv.Close()
	if _, err := a.Chat(context.Background(), AgentRequest{Message: "x"}); err == nil {
		t.Fatal("closed server: no error")
	}
}

func TestFrontendEndpoints(t *testing.T) {
	e := newEnv(t, false)
	anna, igor := e.login(t, "anna.intern"), e.login(t, "igor.mentor")
	if code, out := e.do(t, "GET", "/api/access", anna, ""); code != http.StatusOK || len(out["grants"].([]any)) != 2 {
		t.Fatalf("access: %d %v", code, out)
	}
	if code, out := e.do(t, "GET", "/api/mentees", igor, ""); code != http.StatusOK || len(out["mentees"].([]any)) != 1 {
		t.Fatalf("mentees: %d %v", code, out)
	}
	if code, out := e.do(t, "GET", "/api/mentees", anna, ""); code != http.StatusOK || len(out["mentees"].([]any)) != 0 {
		t.Fatalf("intern mentees: %d %v", code, out)
	}
	if code, out := e.do(t, "GET", "/api/mentees/anna.intern/onboarding", igor, ""); code != http.StatusOK || out["user"] != "anna.intern" {
		t.Fatalf("mentee onboarding: %d %v", code, out)
	}
	if code, _ := e.do(t, "GET", "/api/mentees/pavel.intern/onboarding", igor, ""); code != http.StatusNotFound {
		t.Fatalf("foreign mentee: %d", code)
	}
}

func TestOpenAPICoversRoutes(t *testing.T) {
	spec := string(openapi)
	for _, path := range []string{
		"/api/personas:", "/api/auth/login:", "/api/me:", "/api/chat:", "/api/requests:", "/api/requests/{id}:",
		"/api/requests/{id}/confirm:", "/api/requests/{id}/cancel:", "/api/access:", "/api/onboarding:",
		"/api/onboarding/steps/{step}/complete:", "/api/mentees:", "/api/mentees/{user}/onboarding:",
		"/api/mentees/{user}/steps/{step}/complete:", "/api/approvals:", "/api/approvals/{id}/{decision}:",
		"/api/demo/incidents:", "/api/openapi.yaml:", "/healthz:",
	} {
		if !strings.Contains(spec, "\n  "+path) {
			t.Errorf("openapi.yaml does not describe %s", strings.TrimSuffix(path, ":"))
		}
	}
	e := newEnv(t, false)
	resp, err := http.DefaultClient.Do(mustReq(t, "GET", e.srv.URL+"/api/openapi.yaml", "", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/yaml") {
		t.Fatalf("openapi: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestCORS(t *testing.T) {
	e := newEnv(t, false)
	h, err := New(Config{
		Core: e.core, Issuer: mustIssuer(t), MCP: mcpapi.New(e.core, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)), "t"),
		Metrics: metrics.New(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), AllowedOrigins: []string{"http://localhost:5173"},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	pre := mustReq(t, "OPTIONS", srv.URL+"/api/auth/login", "", map[string]string{"Origin": "http://localhost:5173", "Access-Control-Request-Method": "POST"})
	resp, err := http.DefaultClient.Do(pre)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" ||
		!strings.Contains(resp.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("preflight: %d %v", resp.StatusCode, resp.Header)
	}

	login := mustReq(t, "POST", srv.URL+"/api/auth/login", `{"user_id":"anna.intern"}`,
		map[string]string{"Origin": "http://localhost:5173", "Sec-Fetch-Site": "same-site", "Content-Type": "application/json"})
	resp, err = http.DefaultClient.Do(login)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("cors login: %d", resp.StatusCode)
	}

	evil := mustReq(t, "POST", srv.URL+"/api/auth/login", `{"user_id":"anna.intern"}`,
		map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"})
	resp, err = http.DefaultClient.Do(evil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("evil origin: %d", resp.StatusCode)
	}

	if _, err := New(Config{
		Core: e.core, Issuer: mustIssuer(t), MCP: mcpapi.New(e.core, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)), "t"),
		Metrics: metrics.New(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), AllowedOrigins: []string{"not a url"},
	}); err == nil {
		t.Fatal("bad origin accepted")
	}
}

func mustIssuer(t *testing.T) *authn.Issuer {
	t.Helper()
	iss, err := authn.NewIssuer("test-secret-test-secret-test-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	return iss
}

func mustReq(t *testing.T, method, url, body string, hdr map[string]string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return req
}
