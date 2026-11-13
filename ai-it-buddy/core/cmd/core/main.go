// Command core - ядро AI IT-Buddy: HTTP API для интерфейса и MCP-серверы имитаций корпоративных сервисов
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/avequa/ai-it-buddy/core/internal/authn"
	"github.com/avequa/ai-it-buddy/core/internal/domain"
	"github.com/avequa/ai-it-buddy/core/internal/httpapi"
	"github.com/avequa/ai-it-buddy/core/internal/mcpapi"
	"github.com/avequa/ai-it-buddy/core/internal/metrics"
	"github.com/avequa/ai-it-buddy/core/internal/seed"
)

// version задается при сборке: -ldflags "-X main.version=..."
var version = "dev"

// config - настройки из переменных окружения
type config struct {
	Addr          string
	DataDir       string
	JWTSecret     string
	AgentURL      string
	DemoToken     string
	ApprovalDelay time.Duration
	LogFormat     string
	Origins       []string
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(env("CORE_ADDR", ":8080")))
	}
	os.Exit(start())
}

// start - запуск с кодом выхода; отдельная функция, чтобы defer отработал до os.Exit
func start() int {
	cfg, err := loadConfig(os.Getenv)
	logger := newLogger(cfg.LogFormat)
	if err != nil {
		logger.Error("invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg, logger); err != nil {
		logger.Error("core stopped", slog.String("error", err.Error()))
		return 1
	}
	return 0
}

// env - переменная окружения или значение по умолчанию
func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// loadConfig - конфигурация из окружения
func loadConfig(getenv func(string) string) (config, error) {
	get := func(name, def string) string {
		if v := getenv(name); v != "" {
			return v
		}
		return def
	}
	cfg := config{
		Addr:      get("CORE_ADDR", ":8080"),
		DataDir:   get("DATA_DIR", "./data"),
		JWTSecret: getenv("JWT_SECRET"),
		AgentURL:  getenv("AGENT_URL"),
		DemoToken: getenv("DEMO_TOKEN"),
		LogFormat: get("LOG_FORMAT", "json"),
	}
	delay, err := time.ParseDuration(get("APPROVAL_DELAY", "20s"))
	if err != nil || delay <= 0 {
		return cfg, fmt.Errorf("APPROVAL_DELAY must be a positive duration like 20s")
	}
	cfg.ApprovalDelay = delay
	for o := range strings.SplitSeq(getenv("CORS_ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			cfg.Origins = append(cfg.Origins, o)
		}
	}
	if cfg.JWTSecret == "" {
		return cfg, errors.New("JWT_SECRET is required (at least 32 characters)")
	}
	return cfg, nil
}

// newLogger - JSON для контейнера, text для локальной отладки
func newLogger(format string) *slog.Logger {
	if format == "text" {
		return slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, nil))
}

// run - собирает ядро и слушает до отмены context
func run(ctx context.Context, cfg config, logger *slog.Logger) error {
	data, err := seed.Load(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("load seed: %w", err)
	}
	core, err := domain.New(data, domain.Options{ApprovalDelay: cfg.ApprovalDelay})
	if err != nil {
		return fmt.Errorf("init core: %w", err)
	}
	issuer, err := authn.NewIssuer(cfg.JWTSecret, nil)
	if err != nil {
		return err
	}
	m := metrics.New()
	m.Init(mcpapi.ToolNames, mcpapi.Outcomes,
		[]string{domain.KindAccess, domain.KindHelpdesk, domain.KindEquipment},
		[]string{domain.VerdictOK, domain.VerdictNeedsMentor},
		[]string{
			domain.StatusPendingMentor, domain.StatusInReview, domain.StatusApproved, domain.StatusRejected,
			domain.StatusNew, domain.StatusInProgress, domain.StatusReady, domain.StatusWaitlisted, domain.StatusCancelled,
		},
		[]string{"mcp_token", "ui_token", "ui_confirm", "ui_cancel", "ui_request_view", "ui_approval", "ui_onboarding_step", "ui_mentee_onboarding"})
	var agent httpapi.Agent
	if cfg.AgentURL != "" {
		agent = &httpapi.HTTPAgent{URL: cfg.AgentURL, Client: &http.Client{Timeout: 3 * time.Minute}}
	}
	handler, err := httpapi.New(httpapi.Config{
		Core: core, Issuer: issuer, MCP: mcpapi.New(core, m, logger, version), Agent: agent,
		Metrics: m, Logger: logger, DemoToken: cfg.DemoToken, AllowedOrigins: cfg.Origins,
	})
	if err != nil {
		return err
	}

	go simulateApprovers(ctx, core, m, logger)

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.Addr, err)
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(listener) }()
	logger.Info("core is listening", slog.String("addr", listener.Addr().String()), slog.String("version", version),
		slog.Bool("agent", agent != nil), slog.Bool("demo", cfg.DemoToken != ""), slog.Int("users", len(data.Users)),
		slog.Int("kb_docs", len(data.Docs)))

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
	}
	logger.Info("core stopped")
	return nil
}

// simulateApprovers - раз в секунду согласующие (владельцы, DevOps, склад, поддержка) двигают заявки
func simulateApprovers(ctx context.Context, core *domain.Core, m *metrics.Metrics, logger *slog.Logger) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, r := range core.Tick() {
				m.Requests.WithLabelValues(r.Kind, r.Status).Inc()
				logger.Info("request moved", slog.String("id", r.ID), slog.String("status", r.Status), slog.String("user", r.User))
			}
		}
	}
}

// healthcheck - для HEALTHCHECK в distroless-образе, где нет curl
func healthcheck(addr string) int {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/healthz", http.NoBody)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
