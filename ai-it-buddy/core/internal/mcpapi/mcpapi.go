// Package mcpapi - MCP-серверы имитаций корпоративных сервисов: Access, Helpdesk, Inventory и Onboarding.
// Пользователь берется из проверенного JWT (аудитория mcp), а не из аргументов модели:
// агент не может действовать от чужого имени, даже если его об этом попросят в тексте.
package mcpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/avequa/ai-it-buddy/core/internal/authn"
	"github.com/avequa/ai-it-buddy/core/internal/domain"
	"github.com/avequa/ai-it-buddy/core/internal/metrics"
)

// Servers - имена MCP-серверов; каждый доступен по пути /mcp/<имя>
var Servers = []string{"access", "helpdesk", "inventory", "onboarding"}

// ToolNames - инструменты каждого сервера (для предварительной инициализации метрик)
var ToolNames = map[string][]string{
	"access":     {"list_services", "list_my_access", "draft_access_request", "list_access_requests"},
	"helpdesk":   {"check_service_health", "search_kb", "draft_ticket", "list_tickets"},
	"inventory":  {"list_equipment", "draft_equipment_request", "list_equipment_requests"},
	"onboarding": {"get_onboarding"},
}

// Outcomes - исходы вызовов инструментов
var Outcomes = []string{
	"ok", "found", "not_found_in_kb", "incident", "draft_created", "already_granted", "already_requested",
	"known_incident", "out_of_stock", "invalid", "forbidden", "not_found", "conflict", "error",
}

// API - общие зависимости обработчиков
type API struct {
	core    *domain.Core
	metrics *metrics.Metrics
	logger  *slog.Logger
	version string
}

// New - API поверх ядра
func New(core *domain.Core, m *metrics.Metrics, logger *slog.Logger, version string) *API {
	return &API{core: core, metrics: m, logger: logger, version: version}
}

// Handler - streamable HTTP для сервера name под проверкой Bearer JWT с аудиторией mcp
func (a *API) Handler(name string, issuer *authn.Issuer) (http.Handler, error) {
	server, err := a.Server(name)
	if err != nil {
		return nil, err
	}
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, Logger: a.logger})
	verify := func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		claims, err := issuer.Verify(token, authn.AudienceMCP)
		if err != nil {
			a.metrics.Denied.WithLabelValues("mcp_token").Inc()
			return nil, fmt.Errorf("%w: %w", auth.ErrInvalidToken, err)
		}
		return &auth.TokenInfo{UserID: claims.Subject, Expiration: claims.ExpiresAt.Time}, nil
	}
	return auth.RequireBearerToken(verify, nil)(h), nil
}

// Server - MCP-сервер по имени
func (a *API) Server(name string) (*mcp.Server, error) {
	s := mcp.NewServer(&mcp.Implementation{Name: "buddy-" + name, Version: a.version}, &mcp.ServerOptions{
		Instructions: instructions[name], Logger: a.logger,
	})
	switch name {
	case "access":
		a.addAccess(s)
	case "helpdesk":
		a.addHelpdesk(s)
	case "inventory":
		a.addInventory(s)
	case "onboarding":
		a.addOnboarding(s)
	default:
		return nil, fmt.Errorf("unknown MCP server %q", name)
	}
	return s, nil
}

// instructions - как пользоваться сервером (уходит агенту при подключении)
var instructions = map[string]string{
	"access": "Access Service. To request access for a task: call list_services, map the task to services, projects " +
		"and the minimal level (read < write < admin), check list_my_access, then draft_access_request for each missing one. " +
		"Drafts are NOT submitted: the user confirms them in the UI.",
	"helpdesk": "Helpdesk. Before troubleshooting call check_service_health: if there is an incident, tell the user it is " +
		"not on their side. Otherwise use search_kb and walk the user through steps one at a time. Draft a ticket only " +
		"if the steps did not help; include what was tried.",
	"inventory": "Inventory. list_equipment shows stock and the user's norm. draft_equipment_request never submits: " +
		"the user confirms in the UI. If out of stock, offer the alternatives or the waitlist.",
	"onboarding": "Onboarding path of the intern: weeks, steps, status and the next step.",
}

// userID - пользователь из проверенного токена
func userID(req *mcp.CallToolRequest) (string, error) {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil || req.Extra.TokenInfo.UserID == "" {
		return "", fmt.Errorf("no authenticated user: %w", domain.ErrForbidden)
	}
	return req.Extra.TokenInfo.UserID, nil
}

// call - общая обертка: пользователь из токена, метрика, лог, понятная модели ошибка
func call[In, Out any](a *API, server, tool string, fn func(user string, in In) (Out, string, error)) mcp.ToolHandlerFor[In, Out] {
	return func(_ context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		start := time.Now()
		var zero Out
		user, err := userID(req)
		if err != nil {
			a.metrics.Denied.WithLabelValues("mcp_no_user").Inc()
			return nil, zero, err
		}
		out, outcome, err := fn(user, in)
		if err != nil {
			outcome = errorOutcome(err)
			if outcome == "forbidden" || outcome == "not_found" {
				a.metrics.Denied.WithLabelValues("mcp_" + tool).Inc()
			}
		}
		a.metrics.ToolCalls.WithLabelValues(server, tool, outcome).Inc()
		a.logger.Info("tool call", slog.String("server", server), slog.String("tool", tool), slog.String("user", user),
			slog.String("outcome", outcome), slog.Duration("duration", time.Since(start)))
		if err != nil {
			return nil, zero, err
		}
		return nil, out, nil
	}
}

// errorOutcome - метка метрики по виду ошибки домена
func errorOutcome(err error) string {
	switch {
	case errors.Is(err, domain.ErrInvalid):
		return "invalid"
	case errors.Is(err, domain.ErrForbidden):
		return "forbidden"
	case errors.Is(err, domain.ErrNotFound):
		return "not_found"
	case errors.Is(err, domain.ErrConflict):
		return "conflict"
	default:
		return "error"
	}
}

// readOnly - аннотация для инструментов, которые ничего не меняют
var readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)}

// draftOnly - аннотация для инструментов, которые создают только черновик
var draftOnly = &mcp.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(false)}

// RequestsInput - аргументы list_my_requests
type RequestsInput struct{}

// RequestsOutput - заявки пользователя
type RequestsOutput struct {
	Requests []RequestBrief `json:"requests"`
}

// RequestBrief - заявка без истории: модели хватит статуса и причины, токены экономим
type RequestBrief struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Title      string         `json:"title"`
	Status     string         `json:"status"`
	Approver   string         `json:"approver,omitempty"`
	Resolution string         `json:"resolution,omitempty"`
	Policy     *domain.Policy `json:"policy,omitempty"`
}

// brief - заявка в кратком виде
func brief(r domain.Request) RequestBrief {
	b := RequestBrief{ID: r.ID, Kind: r.Kind, Title: r.Title, Status: r.Status, Approver: r.ApproverName, Resolution: r.Resolution}
	if r.Policy.Verdict != domain.VerdictOK {
		p := r.Policy
		b.Policy = &p
	}
	return b
}

// listToolNames - у каждого сервера свое имя: модели нужны уникальные имена инструментов
var listToolNames = map[string]string{
	domain.KindAccess: "list_access_requests", domain.KindHelpdesk: "list_tickets", domain.KindEquipment: "list_equipment_requests",
}

// addListRequests - список заявок пользователя по виду заявок сервера
func (a *API) addListRequests(s *mcp.Server, server, kind string) {
	name := listToolNames[kind]
	mcp.AddTool(s, &mcp.Tool{
		Name:        name,
		Description: fmt.Sprintf("Lists the current user's %s requests with status and rejection reason, newest first.", kind),
		Annotations: readOnly,
	}, call(a, server, name, func(user string, _ RequestsInput) (RequestsOutput, string, error) {
		out := RequestsOutput{Requests: []RequestBrief{}}
		for _, r := range a.core.MyRequests(user, kind) {
			out.Requests = append(out.Requests, brief(r))
		}
		return out, "ok", nil
	}))
}
