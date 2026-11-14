// Package radar - автоматическое обнаружение сбоев по access-логам через incident-radar (MCP).
// Раз в Interval ядро спрашивает радар о последних Window секундах лога каждого сервиса.
// Высокая доля 5xx несколько проверок подряд - инцидент: всем приходит уведомление, а помощник отвечает
// «это не у тебя, это сбой». Ошибки ушли - инцидент закрывается, но только тот, что открыл радар.
package radar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Stats - что радар насчитал по окну лога
type Stats struct {
	Requests  int
	ErrorRate float64 // доля 5xx, 0..1
	TopError  string  // путь, который чаще всего отвечает 5xx
}

// Analyzer - источник статистики по логам сервиса
type Analyzer interface {
	Analyze(ctx context.Context, service string, since, until time.Time) (Stats, error)
}

// MCPAnalyzer - клиент MCP-сервера incident-radar (tool analyze_logs). Сессия создается при первом вызове
// и пересоздается после ошибки: радар мог перезапуститься.
type MCPAnalyzer struct {
	URL     string
	Token   string
	Version string
	Client  *http.Client

	mu      sync.Mutex
	session *mcp.ClientSession
}

// bearer - добавляет токен радара к каждому запросу
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

func (a *MCPAnalyzer) connect(ctx context.Context) (*mcp.ClientSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != nil {
		return a.session, nil
	}
	hc := a.Client
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	if a.Token != "" {
		cp := *hc
		next := cp.Transport
		if next == nil {
			next = http.DefaultTransport
		}
		cp.Transport = bearer{token: a.Token, next: next}
		hc = &cp
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "ai-it-buddy-core", Version: a.Version}, nil)
	s, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: a.URL, HTTPClient: hc, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to incident-radar: %w", err)
	}
	a.session = s
	return s, nil
}

func (a *MCPAnalyzer) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session != nil {
		_ = a.session.Close()
		a.session = nil
	}
}

// Close - закрыть сессию
func (a *MCPAnalyzer) Close() { a.reset() }

// report - нужная часть отчета incident-radar (schema/report.schema.json)
type report struct {
	TotalRequestsCount int     `json:"totalRequestsCount"`
	ErrorRate          float64 `json:"errorRate"`
	TopErrorResources  []struct {
		Resource string `json:"resource"`
	} `json:"topErrorResources"`
}

// Analyze - analyze_logs по файлу <service>.log за окно [since, until)
func (a *MCPAnalyzer) Analyze(ctx context.Context, service string, since, until time.Time) (Stats, error) {
	s, err := a.connect(ctx)
	if err != nil {
		return Stats{}, err
	}
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: "analyze_logs", Arguments: map[string]any{
		"path":  service + ".log",
		"since": since.UTC().Format(time.RFC3339),
		"until": until.UTC().Format(time.RFC3339),
		"top":   3,
	}})
	if err != nil {
		a.reset()
		return Stats{}, fmt.Errorf("analyze_logs %s: %w", service, err)
	}
	if res.IsError {
		return Stats{}, fmt.Errorf("analyze_logs %s: %s", service, toolText(res))
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		return Stats{}, fmt.Errorf("analyze_logs %s: %w", service, err)
	}
	var rep report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return Stats{}, fmt.Errorf("analyze_logs %s: unexpected report: %w", service, err)
	}
	st := Stats{Requests: rep.TotalRequestsCount, ErrorRate: rep.ErrorRate}
	if len(rep.TopErrorResources) > 0 {
		st.TopError = rep.TopErrorResources[0].Resource
	}
	return st, nil
}

func toolText(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "; ")
}

// Incidents - что watcher делает с ядром
type Incidents interface {
	AutoOpenIncident(service, summary, message string) (id string, opened bool, err error)
	AutoResolveIncident(service string) (id string, resolved bool)
}

// Observer - метрики проверок: result = ok | high | no_traffic | error
type Observer interface {
	Checked(service, result string, errorRate float64)
	Incident(event string)
}

// Watcher - правила открытия и закрытия инцидентов. Гистерезис: открыть - OpenAfter плохих проверок подряд,
// закрыть - CloseAfter хороших подряд, порог закрытия ниже порога открытия. Так одиночный всплеск
// не будит всю компанию, а инцидент не мигает открыт-закрыт.
type Watcher struct {
	Analyzer    Analyzer
	Incidents   Incidents
	Observer    Observer
	Logger      *slog.Logger
	Services    []string
	Window      time.Duration // какой хвост лога смотреть
	OpenRate    float64       // доля 5xx, начиная с которой проверка плохая
	CloseRate   float64       // доля 5xx, ниже которой проверка хорошая
	MinRequests int           // меньше запросов в окне - мало данных, решение не принимается
	OpenAfter   int
	CloseAfter  int
	Now         func() time.Time

	bad, good map[string]int
}

// Defaults - значения для демо: инцидент через ~20 секунд после начала сбоя, закрытие через ~30 секунд после конца
func (w *Watcher) Defaults() *Watcher {
	if w.Window <= 0 {
		w.Window = 30 * time.Second
	}
	if w.OpenRate <= 0 {
		w.OpenRate = 0.2
	}
	if w.CloseRate <= 0 {
		w.CloseRate = 0.05
	}
	if w.MinRequests <= 0 {
		w.MinRequests = 20
	}
	if w.OpenAfter <= 0 {
		w.OpenAfter = 2
	}
	if w.CloseAfter <= 0 {
		w.CloseAfter = 3
	}
	if w.Now == nil {
		w.Now = time.Now
	}
	if w.Logger == nil {
		w.Logger = slog.New(slog.DiscardHandler)
	}
	return w
}

// Check - одна проверка всех сервисов
func (w *Watcher) Check(ctx context.Context) error {
	if w.bad == nil {
		w.bad, w.good = map[string]int{}, map[string]int{}
	}
	now := w.Now()
	var errs []error
	for _, svc := range w.Services {
		st, err := w.Analyzer.Analyze(ctx, svc, now.Add(-w.Window), now)
		if err != nil {
			w.observe(svc, "error", 0)
			errs = append(errs, err)
			continue // радар недоступен - ничего не открываем и не закрываем
		}
		switch {
		case st.Requests < w.MinRequests:
			w.observe(svc, "no_traffic", st.ErrorRate)
		case st.ErrorRate >= w.OpenRate:
			w.observe(svc, "high", st.ErrorRate)
			w.good[svc] = 0
			w.bad[svc]++
			if w.bad[svc] >= w.OpenAfter {
				w.open(svc, st)
			}
		case st.ErrorRate < w.CloseRate:
			w.observe(svc, "ok", st.ErrorRate)
			w.bad[svc] = 0
			w.good[svc]++
			if w.good[svc] >= w.CloseAfter {
				if id, ok := w.Incidents.AutoResolveIncident(svc); ok {
					w.event("resolved")
					w.Logger.Info("incident resolved by radar", slog.String("id", id), slog.String("service", svc))
				}
			}
		default: // между порогами: состояние не меняется
			w.observe(svc, "ok", st.ErrorRate)
			w.bad[svc], w.good[svc] = 0, 0
		}
	}
	return errors.Join(errs...)
}

func (w *Watcher) open(svc string, st Stats) {
	summary := fmt.Sprintf("%.0f%% запросов отвечают ошибкой 5xx", st.ErrorRate*100) // название сервиса добавит уведомление
	msg := "Обнаружено автоматически по логам NGINX. Заявку в поддержку заводить не нужно, сообщим, когда починят."
	if st.TopError != "" {
		msg = "Чаще всего падает " + st.TopError + ". " + msg
	}
	id, opened, err := w.Incidents.AutoOpenIncident(svc, summary, msg)
	if err != nil {
		w.Logger.Error("radar could not open incident", slog.String("service", svc), slog.String("error", err.Error()))
		return
	}
	if opened {
		w.event("opened")
		w.Logger.Warn("incident opened by radar", slog.String("id", id), slog.String("service", svc),
			slog.Float64("error_rate", st.ErrorRate), slog.Int("requests", st.Requests))
	}
}

func (w *Watcher) observe(svc, result string, rate float64) {
	if w.Observer != nil {
		w.Observer.Checked(svc, result, rate)
	}
}

func (w *Watcher) event(e string) {
	if w.Observer != nil {
		w.Observer.Incident(e)
	}
}

// Run - проверки раз в interval до отмены context. Ошибки радара логируются один раз при появлении.
func (w *Watcher) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			opCtx, cancel := context.WithTimeout(ctx, interval)
			err := w.Check(opCtx)
			cancel()
			switch {
			case err != nil && !failing:
				w.Logger.Error("incident-radar is unavailable, auto incidents paused", slog.String("error", err.Error()))
			case err == nil && failing:
				w.Logger.Info("incident-radar is back")
			}
			failing = err != nil
		}
	}
}
