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
	// Ready - состояние зависимостей для /readyz; nil - ядро работает только в памяти
	Ready func(ctx context.Context) Readiness
	// AllowedOrigins - откуда браузеру можно ходить в API с другого origin (dev-сервер фронтенда), например http://localhost:5173
	AllowedOrigins []string
}

// Readiness - ответ /readyz. Ядро отвечает и при недоступной базе (работает из памяти и дописывает позже),
// поэтому degraded - это 200, а не 503: снимать ядро с трафика незачем, нужно чинить базу.
type Readiness struct {
	Status      string     `json:"status"`  // ok | degraded
	Storage     string     `json:"storage"` // postgres | memory
	Pending     int        `json:"pending_changes"`
	LastFlushAt *time.Time `json:"last_flush_at,omitempty"`
	Error       string     `json:"error,omitempty"`
}

func (a *api) readyz(w http.ResponseWriter, r *http.Request) {
	st := Readiness{Status: "ok", Storage: "memory"}
	if a.Ready != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		st = a.Ready(ctx)
	}
	writeJSON(w, http.StatusOK, st)
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

type ctxKey struct{}

// New - HTTP-обработчик со всеми маршрутами
func New(cfg Config) (http.Handler, error) {
	a := &api{Config: cfg}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	mux.HandleFunc("GET /readyz", a.readyz)
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
	mux.Handle("GET /api/notifications", a.auth(a.notifications))
	mux.Handle("POST /api/notifications/read", a.auth(a.readNotifications))
	mux.Handle("GET /api/incidents", a.auth(a.incidents))
	mux.Handle("POST /api/incidents", a.auth(a.openIncident))
	mux.Handle("POST /api/incidents/{id}/resolve", a.auth(a.resolveIncident))
	mux.Handle("POST /api/onboarding/starter-pack", a.auth(a.starterPack))
	mux.Handle("POST /api/requests/confirm-batch", a.auth(a.confirmBatch))
	mux.Handle("GET /api/mentees/{user}/digest", a.auth(a.digest))
	mux.Handle("GET /api/security/events", a.auth(a.securityEvents))
	mux.HandleFunc("POST /api/demo/digests", a.demoDigests)
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

type api struct {
	Config
}

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

type recorder struct {
	http.ResponseWriter
	code int
}

func (r *recorder) WriteHeader(code int)        { r.code = code; r.ResponseWriter.WriteHeader(code) }
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

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
	a.Core.OfferStarterPack(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": u, "expires_in": int(SessionTTL.Seconds())})
}

func (a *api) me(w http.ResponseWriter, _ *http.Request, u domain.User) {
	writeJSON(w, http.StatusOK, u)
}

func (a *api) onboarding(w http.ResponseWriter, _ *http.Request, u domain.User) {
	ob, err := a.Core.Onboarding(u.ID)
	a.respond(w, u, ob, err, "")
}

func (a *api) access(w http.ResponseWriter, _ *http.Request, u domain.User) {
	grants, open, err := a.Core.MyAccess(u.ID)
	a.respond(w, u, map[string]any{"grants": nonNil(grants), "open_requests": nonNil(open)}, err, "")
}

func (a *api) mentees(w http.ResponseWriter, _ *http.Request, u domain.User) {
	list, err := a.Core.Mentees(u.ID)
	a.respond(w, u, map[string]any{"mentees": nonNil(list)}, err, "")
}

func (a *api) menteeOnboarding(w http.ResponseWriter, r *http.Request, u domain.User) {
	ob, err := a.Core.OnboardingFor(u.ID, r.PathValue("user"))
	a.respond(w, u, ob, err, "mentee_onboarding")
}

func (a *api) completeOwnStep(w http.ResponseWriter, r *http.Request, u domain.User) {
	step, err := a.Core.CompleteStep(u.ID, u.ID, r.PathValue("step"))
	a.respond(w, u, step, err, "onboarding_step")
}

func (a *api) completeMenteeStep(w http.ResponseWriter, r *http.Request, u domain.User) {
	step, err := a.Core.CompleteStep(u.ID, r.PathValue("user"), r.PathValue("step"))
	a.respond(w, u, step, err, "onboarding_step")
}

func (a *api) requests(w http.ResponseWriter, r *http.Request, u domain.User) {
	writeJSON(w, http.StatusOK, map[string]any{"requests": nonNil(a.Core.MyRequests(u.ID, r.URL.Query().Get("kind")))})
}

func (a *api) request(w http.ResponseWriter, r *http.Request, u domain.User) {
	req, err := a.Core.Request(u.ID, r.PathValue("id"))
	a.respond(w, u, req, err, "request_view")
}

func (a *api) confirm(w http.ResponseWriter, r *http.Request, u domain.User) {
	req, err := a.Core.Confirm(u.ID, r.PathValue("id"))
	if err == nil {
		a.Metrics.Confirmed.WithLabelValues(req.Kind).Inc()
		a.Metrics.Requests.WithLabelValues(req.Kind, req.Status).Inc()
	}
	a.respond(w, u, req, err, "confirm")
}

func (a *api) cancel(w http.ResponseWriter, r *http.Request, u domain.User) {
	req, err := a.Core.Cancel(u.ID, r.PathValue("id"))
	if err == nil {
		a.Metrics.Requests.WithLabelValues(req.Kind, req.Status).Inc()
	}
	a.respond(w, u, req, err, "cancel")
}

func (a *api) approvals(w http.ResponseWriter, _ *http.Request, u domain.User) {
	writeJSON(w, http.StatusOK, map[string]any{"approvals": nonNil(a.Core.Approvals(u.ID))})
}

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
	a.respond(w, u, req, err, "approval")
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
	if found := a.Core.ScreenChat(u.ID, in.Message); len(found) > 0 {
		a.Metrics.Security.WithLabelValues(domain.SecChat).Inc()
		a.Logger.Warn("security event", slog.String("user", u.ID), slog.String("kind", domain.SecChat),
			slog.String("detail", strings.Join(found, "; ")))
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
	if !a.demoAllowed(r) {
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
	h := a.Core.SetIncident(in.Service, in.Title, in.Active)
	if in.Active {
		a.Metrics.Incidents.WithLabelValues("opened").Inc()
	} else {
		a.Metrics.Incidents.WithLabelValues("resolved").Inc()
	}
	writeJSON(w, http.StatusOK, h)
}

func (a *api) demoDigests(w http.ResponseWriter, r *http.Request) {
	if !a.demoAllowed(r) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"sent": a.Core.SendDigests()})
}

func (a *api) demoAllowed(r *http.Request) bool {
	return a.DemoToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Demo-Token")), []byte(a.DemoToken)) == 1
}

// notifications - лента уведомлений для окна чата: ?after=<последний полученный id>, опрашивать раз в 3-5 с
func (a *api) notifications(w http.ResponseWriter, r *http.Request, u domain.User) {
	var after int64
	if v := r.URL.Query().Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "after must be a non-negative integer")
			return
		}
		after = n
	}
	list := nonNil(a.Core.Notifications(u.ID, after))
	unread := 0
	for _, n := range a.Core.Notifications(u.ID, 0) {
		if !n.Read {
			unread++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": list, "unread": unread})
}

func (a *api) readNotifications(w http.ResponseWriter, r *http.Request, u domain.User) {
	var in struct {
		UpTo int64 `json:"up_to"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"marked": a.Core.MarkRead(u.ID, in.UpTo)})
}

func (a *api) incidents(w http.ResponseWriter, _ *http.Request, _ domain.User) {
	writeJSON(w, http.StatusOK, map[string]any{"incidents": nonNil(a.Core.Incidents())})
}

func (a *api) openIncident(w http.ResponseWriter, r *http.Request, u domain.User) {
	var in struct {
		Service string `json:"service"`
		Title   string `json:"title"`
		Message string `json:"message"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	inc, err := a.Core.OpenIncident(u.ID, in.Service, in.Title, in.Message)
	if err == nil {
		a.Metrics.Incidents.WithLabelValues("opened").Inc()
	}
	a.respond(w, u, inc, err, "incident")
}

func (a *api) resolveIncident(w http.ResponseWriter, r *http.Request, u domain.User) {
	inc, err := a.Core.ResolveIncident(u.ID, r.PathValue("id"))
	if err == nil {
		a.Metrics.Incidents.WithLabelValues("resolved").Inc()
	}
	a.respond(w, u, inc, err, "incident")
}

func (a *api) starterPack(w http.ResponseWriter, _ *http.Request, u domain.User) {
	items, err := a.Core.StarterPack(u.ID)
	var drafts []string
	for _, it := range items {
		if it.Outcome == "draft_created" {
			drafts = append(drafts, it.Request.ID)
		}
	}
	a.respond(w, u, map[string]any{"items": nonNil(items), "draft_ids": nonNil(drafts)}, err, "")
}

func (a *api) confirmBatch(w http.ResponseWriter, r *http.Request, u domain.User) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.IDs) == 0 || len(in.IDs) > 20 {
		writeError(w, http.StatusBadRequest, "ids: 1..20 request ids")
		return
	}
	results := a.Core.ConfirmMany(u.ID, in.IDs)
	for _, res := range results {
		switch {
		case res.Request != nil:
			a.Metrics.Confirmed.WithLabelValues(res.Request.Kind).Inc()
			a.Metrics.Requests.WithLabelValues(res.Request.Kind, res.Request.Status).Inc()
		case errors.Is(res.Err(), domain.ErrForeign):
			a.security(u.ID, domain.SecForeign, surfaceText["confirm"])
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (a *api) digest(w http.ResponseWriter, r *http.Request, u domain.User) {
	d, err := a.Core.Digest(u.ID, r.PathValue("user"))
	a.respond(w, u, d, err, "digest")
}

func (a *api) securityEvents(w http.ResponseWriter, _ *http.Request, u domain.User) {
	writeJSON(w, http.StatusOK, map[string]any{"events": nonNil(a.Core.SecurityEvents(u.ID))})
}

// respond - ответ по результату операции домена. Попытки открыть чужое или действовать не по роли
// попадают в метрику и в события безопасности (наставник и руководитель получают уведомление).
func (a *api) respond(w http.ResponseWriter, u domain.User, v any, err error, surface string) {
	if err == nil {
		writeJSON(w, http.StatusOK, v)
		return
	}
	switch {
	case errors.Is(err, domain.ErrForeign):
		a.security(u.ID, domain.SecForeign, surfaceText[surface])
		writeError(w, http.StatusNotFound, "not found")
		return
	case errors.Is(err, domain.ErrForbidden) && surface != "":
		a.security(u.ID, domain.SecForbidden, surfaceText[surface]+": "+err.Error())
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
	if surface != "" && code == http.StatusForbidden {
		a.Metrics.Denied.WithLabelValues("ui_" + surface).Inc()
	}
	writeError(w, code, err.Error())
}

var surfaceText = map[string]string{
	"request_view": "открыть чужую заявку", "confirm": "подтвердить чужую заявку", "cancel": "отменить чужую заявку",
	"approval": "решить чужую заявку на согласовании", "onboarding_step": "отметить шаг адаптации не по роли",
	"mentee_onboarding": "посмотреть план чужого стажера", "digest": "посмотреть сводку чужого стажера",
	"incident": "объявить или закрыть инцидент без роли SRE",
}

func (a *api) security(userID, kind, detail string) {
	ev, alerted := a.Core.RecordSecurity(userID, kind, detail)
	a.Metrics.Security.WithLabelValues(kind).Inc()
	if kind == domain.SecForeign {
		a.Metrics.Denied.WithLabelValues("ui_foreign").Inc()
	}
	a.Logger.Warn("security event", slog.String("user", userID), slog.String("kind", kind),
		slog.String("detail", ev.Detail), slog.Bool("mentor_notified", alerted))
}

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
