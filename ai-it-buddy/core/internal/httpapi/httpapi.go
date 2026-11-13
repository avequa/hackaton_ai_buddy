// Package httpapi - HTTP API ядра (BFF для веб-интерфейса) и точки подключения MCP-серверов.
// Подтверждение заявок живет только здесь, под токеном сессии человека (аудитория ui).
package httpapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/avequa/ai-it-buddy/core/internal/authn"
	"github.com/avequa/ai-it-buddy/core/internal/domain"
	"github.com/avequa/ai-it-buddy/core/internal/mcpapi"
	"github.com/avequa/ai-it-buddy/core/internal/metrics"
)

// openapi - спецификация API для фронтенда, отдается по GET /api/openapi.yaml
//
//go:embed openapi.yaml
var openapi []byte

// Сроки жизни токенов
const (
	SessionTTL  = 12 * time.Hour
	AgentTTL    = 5 * time.Minute // агенту - только на один ход диалога
	maxBodySize = 64 << 10
)

// Config - зависимости API
type Config struct {
	Core      *domain.Core
	Issuer    *authn.Issuer
	MCP       *mcpapi.API
	Agent     Agent
	Metrics   *metrics.Metrics
	Logger    *slog.Logger
	DemoToken string // включает /api/demo/*; пустой - демо-ручки выключены
	// AllowedOrigins - откуда браузеру можно ходить в API с другого origin (dev-сервер фронтенда), например http://localhost:5173
	AllowedOrigins []string
}

// Agent - Python-агент (LangGraph): один ход диалога
type Agent interface {
	Chat(ctx context.Context, req AgentRequest) (AgentReply, error)
}

// AgentRequest - что BFF передает агенту: сообщение, тред и короткий токен только для MCP
type AgentRequest struct {
	Message  string    `json:"message"`
	ThreadID string    `json:"thread_id"`
	Token    string    `json:"token"`
	User     AgentUser `json:"user"`
}

// AgentUser - контекст пользователя для промпта (права при этом проверяет сервер)
type AgentUser struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Position string `json:"position"`
	Team     string `json:"team"`
}

// AgentReply - ответ агента
type AgentReply struct {
	Reply     string   `json:"reply"`
	ToolsUsed []string `json:"tools_used"`
}

// ctxKey - ключ пользователя в context запроса
type ctxKey struct{}

// New - HTTP-обработчик со всеми маршрутами
func New(cfg Config) (http.Handler, error) {
	a := &api{Config: cfg}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	mux.Handle("GET /metrics", cfg.Metrics.Handler())
	for _, name := range mcpapi.Servers {
		h, err := cfg.MCP.Handler(name, cfg.Issuer)
		if err != nil {
			return nil, err
		}
		mux.Handle("/mcp/"+name, h)
	}

	mux.HandleFunc("GET /api/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(openapi)
	})
	mux.HandleFunc("GET /api/personas", a.personas)
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.Handle("GET /api/me", a.auth(a.me))
	mux.Handle("GET /api/onboarding", a.auth(a.onboarding))
	mux.Handle("GET /api/access", a.auth(a.access))
	mux.Handle("GET /api/mentees", a.auth(a.mentees))
	mux.Handle("GET /api/mentees/{user}/onboarding", a.auth(a.menteeOnboarding))
	mux.Handle("POST /api/onboarding/steps/{step}/complete", a.auth(a.completeOwnStep))
	mux.Handle("GET /api/requests", a.auth(a.requests))
	mux.Handle("GET /api/requests/{id}", a.auth(a.request))
	mux.Handle("POST /api/requests/{id}/confirm", a.auth(a.confirm))
	mux.Handle("POST /api/requests/{id}/cancel", a.auth(a.cancel))
	mux.Handle("GET /api/approvals", a.auth(a.approvals))
	mux.Handle("POST /api/approvals/{id}/{decision}", a.auth(a.decide))
	mux.Handle("POST /api/mentees/{user}/steps/{step}/complete", a.auth(a.completeMenteeStep))
	mux.Handle("POST /api/chat", a.auth(a.chat))
	mux.HandleFunc("POST /api/demo/incidents", a.demoIncident)

	protection := http.NewCrossOriginProtection()
	for _, origin := range cfg.AllowedOrigins {
		if err := protection.AddTrustedOrigin(origin); err != nil {
			return nil, fmt.Errorf("allowed origin %q: %w", origin, err)
		}
	}
	return a.cors(protection.Handler(a.instrument(mux))), nil
}

// cors - заголовки CORS для разрешенных origin; preflight отвечает сразу.
// В проде фронтенд и API на одном origin (через reverse proxy), и CORS не нужен.
func (a *api) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || !slices.Contains(a.AllowedOrigins, origin) {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Add("Vary", "Origin")
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// api - обработчики
type api struct {
	Config
}

// instrument - метрики и access-лог по шаблону маршрута (без id в метках)
func (a *api) instrument(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w, code: http.StatusOK}
		mux.ServeHTTP(rec, r)
		_, pattern := mux.Handler(r)
		route := pattern
		if route == "" {
			route = "unmatched"
		}
		a.Metrics.HTTPRequests.WithLabelValues(route, strconv.Itoa(rec.code)).Inc()
		a.Metrics.HTTPDuration.WithLabelValues(route).Observe(time.Since(start).Seconds())
		a.Logger.Debug("http", slog.String("route", route), slog.Int("code", rec.code), slog.Duration("duration", time.Since(start)))
	})
}

// recorder - запоминает код ответа
type recorder struct {
	http.ResponseWriter
	code int
}

func (r *recorder) WriteHeader(code int)        { r.code = code; r.ResponseWriter.WriteHeader(code) }
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// auth - проверка токена сессии (аудитория ui)
func (a *api) auth(next func(http.ResponseWriter, *http.Request, domain.User)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		claims, err := a.Issuer.Verify(token, authn.AudienceUI)
		if err != nil {
			a.Metrics.Denied.WithLabelValues("ui_token").Inc()
			writeError(w, http.StatusUnauthorized, "invalid or expired session token")
			return
		}
		u, err := a.Core.User(claims.Subject)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unknown user")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)), u)
	})
}

// personas - тестовые сотрудники для экрана входа
func (a *api) personas(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"personas": a.Core.Users()})
}

// login - mock-IdP: вход под тестовой персоной без пароля (в Авито - корпоративный SSO)
func (a *api) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UserID string `json:"user_id"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	u, err := a.Core.User(in.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, "unknown persona")
		return
	}
	token, err := a.Issuer.Issue(u, authn.AudienceUI, SessionTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot issue token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": u, "expires_in": int(SessionTTL.Seconds())})
}

// me - профиль текущего пользователя
func (a *api) me(w http.ResponseWriter, _ *http.Request, u domain.User) {
	writeJSON(w, http.StatusOK, u)
}

// onboarding - путь адаптации текущего пользователя
func (a *api) onboarding(w http.ResponseWriter, _ *http.Request, u domain.User) {
	ob, err := a.Core.Onboarding(u.ID)
	a.respond(w, ob, err, "")
}

// access - выданные доступы и открытые заявки на доступ (страница "Мои доступы")
func (a *api) access(w http.ResponseWriter, _ *http.Request, u domain.User) {
	grants, open, err := a.Core.MyAccess(u.ID)
	a.respond(w, map[string]any{"grants": nonNil(grants), "open_requests": nonNil(open)}, err, "")
}

// mentees - стажеры наставника или руководителя с прогрессом
func (a *api) mentees(w http.ResponseWriter, _ *http.Request, u domain.User) {
	list, err := a.Core.Mentees(u.ID)
	a.respond(w, map[string]any{"mentees": nonNil(list)}, err, "")
}

// menteeOnboarding - путь адаптации стажера для его наставника или руководителя
func (a *api) menteeOnboarding(w http.ResponseWriter, r *http.Request, u domain.User) {
	ob, err := a.Core.OnboardingFor(u.ID, r.PathValue("user"))
	a.respond(w, ob, err, "mentee_onboarding")
}

// completeOwnStep - стажер отмечает свой шаг
func (a *api) completeOwnStep(w http.ResponseWriter, r *http.Request, u domain.User) {
	step, err := a.Core.CompleteStep(u.ID, u.ID, r.PathValue("step"))
	a.respond(w, step, err, "onboarding_step")
}

// completeMenteeStep - наставник или руководитель отмечает шаг стажера
func (a *api) completeMenteeStep(w http.ResponseWriter, r *http.Request, u domain.User) {
	step, err := a.Core.CompleteStep(u.ID, r.PathValue("user"), r.PathValue("step"))
	a.respond(w, step, err, "onboarding_step")
}

// requests - заявки пользователя (?kind=access|helpdesk|equipment)
func (a *api) requests(w http.ResponseWriter, r *http.Request, u domain.User) {
	writeJSON(w, http.StatusOK, map[string]any{"requests": nonNil(a.Core.MyRequests(u.ID, r.URL.Query().Get("kind")))})
}

// request - одна заявка с историей
func (a *api) request(w http.ResponseWriter, r *http.Request, u domain.User) {
	req, err := a.Core.Request(u.ID, r.PathValue("id"))
	a.respond(w, req, err, "request_view")
}

// confirm - человек подтверждает черновик
func (a *api) confirm(w http.ResponseWriter, r *http.Request, u domain.User) {
	req, err := a.Core.Confirm(u.ID, r.PathValue("id"))
	if err == nil {
		a.Metrics.Confirmed.WithLabelValues(req.Kind).Inc()
		a.Metrics.Requests.WithLabelValues(req.Kind, req.Status).Inc()
	}
	a.respond(w, req, err, "confirm")
}

// cancel - человек отменяет заявку
func (a *api) cancel(w http.ResponseWriter, r *http.Request, u domain.User) {
	req, err := a.Core.Cancel(u.ID, r.PathValue("id"))
	if err == nil {
		a.Metrics.Requests.WithLabelValues(req.Kind, req.Status).Inc()
	}
	a.respond(w, req, err, "cancel")
}

// approvals - очередь наставника: заявки "не по роли" его стажеров
func (a *api) approvals(w http.ResponseWriter, _ *http.Request, u domain.User) {
	writeJSON(w, http.StatusOK, map[string]any{"approvals": nonNil(a.Core.Approvals(u.ID))})
}

// decide - наставник одобряет (approve) или отклоняет (reject) заявку
func (a *api) decide(w http.ResponseWriter, r *http.Request, u domain.User) {
	decision := r.PathValue("decision")
	if decision != "approve" && decision != "reject" {
		writeError(w, http.StatusNotFound, "decision must be approve or reject")
		return
	}
	var in struct {
		Comment string `json:"comment"`
	}
	if r.ContentLength != 0 && !readJSON(w, r, &in) {
		return
	}
	req, err := a.Core.Decide(u.ID, r.PathValue("id"), decision == "approve", in.Comment)
	if err == nil {
		a.Metrics.Requests.WithLabelValues(req.Kind, req.Status).Inc()
	}
	a.respond(w, req, err, "approval")
}

// chat - один ход диалога: BFF выдает агенту короткий MCP-токен пользователя и возвращает ответ с черновиками
func (a *api) chat(w http.ResponseWriter, r *http.Request, u domain.User) {
	var in struct {
		Message  string `json:"message"`
		ThreadID string `json:"thread_id"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Message = strings.TrimSpace(in.Message)
	if in.Message == "" || len([]rune(in.Message)) > 4000 {
		writeError(w, http.StatusBadRequest, "message must be 1..4000 characters")
		return
	}
	if in.ThreadID == "" {
		in.ThreadID = "default"
	}
	if a.Agent == nil {
		writeError(w, http.StatusServiceUnavailable, "agent is not configured")
		return
	}
	token, err := a.Issuer.Issue(u, authn.AudienceMCP, AgentTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot issue agent token")
		return
	}
	reply, err := a.Agent.Chat(r.Context(), AgentRequest{
		Message: in.Message, ThreadID: u.ID + ":" + in.ThreadID, Token: token,
		User: AgentUser{ID: u.ID, Name: u.Name, Role: u.Role, Position: u.Position, Team: u.Team},
	})
	if err != nil {
		a.Metrics.ChatTurns.WithLabelValues("error").Inc()
		a.Logger.Warn("agent call failed", slog.String("user", u.ID), slog.String("error", err.Error()))
		writeError(w, http.StatusBadGateway, "Помощник сейчас недоступен, попробуй еще раз через минуту. Заявки и статусы работают.")
		return
	}
	a.Metrics.ChatTurns.WithLabelValues("ok").Inc()
	var drafts []domain.Request
	for _, req := range a.Core.MyRequests(u.ID, "") {
		if req.Status == domain.StatusDraft {
			drafts = append(drafts, req)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reply": reply.Reply, "tools_used": nonNil(reply.ToolsUsed), "drafts": nonNil(drafts), "thread_id": in.ThreadID,
	})
}

// demoIncident - включить или снять инцидент сервиса для демонстрации (вместо мониторинга)
func (a *api) demoIncident(w http.ResponseWriter, r *http.Request) {
	if a.DemoToken == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Demo-Token")), []byte(a.DemoToken)) != 1 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var in struct {
		Service string `json:"service"`
		Title   string `json:"title"`
		Active  bool   `json:"active"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Service == "" {
		writeError(w, http.StatusBadRequest, "service is required")
		return
	}
	writeJSON(w, http.StatusOK, a.Core.SetIncident(in.Service, in.Title, in.Active))
}

// respond - ответ по результату операции домена; запрещенные действия попадают в метрику обхода прав
func (a *api) respond(w http.ResponseWriter, v any, err error, surface string) {
	if err == nil {
		writeJSON(w, http.StatusOK, v)
		return
	}
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, domain.ErrForbidden):
		code = http.StatusForbidden
	case errors.Is(err, domain.ErrInvalid):
		code = http.StatusBadRequest
	case errors.Is(err, domain.ErrConflict):
		code = http.StatusConflict
	}
	if surface != "" && (code == http.StatusNotFound || code == http.StatusForbidden) {
		a.Metrics.Denied.WithLabelValues("ui_" + surface).Inc()
	}
	writeError(w, code, err.Error())
}

// nonNil - пустой список в JSON как [], а не null
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// readJSON - тело запроса с ограничением размера; false - ответ с ошибкой уже отправлен
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodySize))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// writeJSON - JSON-ответ
func writeJSON(w http.ResponseWriter, code int, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		http.Error(w, `{"error":"encode response"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(buf.Bytes())
}

// writeError - ошибка в едином формате
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// HTTPAgent - клиент Python-агента
type HTTPAgent struct {
	URL    string
	Client *http.Client
}

// Chat - POST {URL}/chat
func (h *HTTPAgent) Chat(ctx context.Context, req AgentRequest) (AgentReply, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return AgentReply{}, fmt.Errorf("encode agent request: %w", err)
	}
	// URL агента задает администратор в AGENT_URL, пользовательский ввод в адрес не попадает
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.URL, "/")+"/chat", bytes.NewReader(body)) //nolint:gosec // G704: см. выше
	if err != nil {
		return AgentReply{}, fmt.Errorf("build agent request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := h.Client.Do(httpReq) //nolint:gosec // G704: адрес агента из конфигурации
	if err != nil {
		return AgentReply{}, fmt.Errorf("call agent: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return AgentReply{}, fmt.Errorf("read agent reply: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return AgentReply{}, fmt.Errorf("agent returned %d: %s", resp.StatusCode, bytes.TrimSpace(data))
	}
	var reply AgentReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return AgentReply{}, fmt.Errorf("decode agent reply: %w", err)
	}
	return reply, nil
}
