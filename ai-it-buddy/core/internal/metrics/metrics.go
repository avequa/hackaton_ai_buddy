// Package metrics - метрики Prometheus: технические (HTTP) и продуктовые для AgentOps-дашборда
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics - все метрики ядра в собственном registry (без глобального состояния, удобно в тестах)
type Metrics struct {
	registry *prometheus.Registry

	HTTPRequests *prometheus.CounterVec   // route, code
	HTTPDuration *prometheus.HistogramVec // route
	ToolCalls    *prometheus.CounterVec   // server, tool, outcome
	Policy       *prometheus.CounterVec   // kind, verdict: сколько заявок "не по роли"
	Denied       *prometheus.CounterVec   // surface: попытки выйти за права (чужая заявка, чужой шаг, неверный токен)
	Requests     *prometheus.CounterVec   // kind, status: переходы заявок
	Confirmed    *prometheus.CounterVec   // kind: черновики, которые человек подтвердил
	ChatTurns    *prometheus.CounterVec   // outcome: ok, error
}

// New - регистрирует метрики
func New() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	f := func(c prometheus.Collector) prometheus.Collector { reg.MustRegister(c); return c }
	return &Metrics{
		registry: reg,
		HTTPRequests: f(prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "buddy_http_requests_total", Help: "HTTP requests to the core API.",
		}, []string{"route", "code"})).(*prometheus.CounterVec),
		HTTPDuration: f(prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "buddy_http_request_duration_seconds", Help: "HTTP request duration.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		}, []string{"route"})).(*prometheus.HistogramVec),
		ToolCalls: f(prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "buddy_tool_calls_total", Help: "MCP tool calls by the agent and their outcome.",
		}, []string{"server", "tool", "outcome"})).(*prometheus.CounterVec),
		Policy: f(prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "buddy_policy_verdicts_total", Help: "Server policy verdicts for drafted requests.",
		}, []string{"kind", "verdict"})).(*prometheus.CounterVec),
		Denied: f(prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "buddy_access_denied_total", Help: "Attempts to go beyond the user's permissions.",
		}, []string{"surface"})).(*prometheus.CounterVec),
		Requests: f(prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "buddy_request_transitions_total", Help: "Request status transitions.",
		}, []string{"kind", "status"})).(*prometheus.CounterVec),
		Confirmed: f(prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "buddy_drafts_confirmed_total", Help: "Drafts confirmed by the user in the UI.",
		}, []string{"kind"})).(*prometheus.CounterVec),
		ChatTurns: f(prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "buddy_chat_turns_total", Help: "Chat turns proxied to the agent.",
		}, []string{"outcome"})).(*prometheus.CounterVec),
	}
}

// Init - заводит известные серии со значением 0. Иначе increase() не видит первое событие:
// счетчик, появившийся сразу с 1, для Prometheus не растет, и панель дашборда показывает 0 или NaN.
func (m *Metrics) Init(tools map[string][]string, outcomes, kinds, verdicts, statuses, surfaces []string) {
	for server, names := range tools {
		for _, tool := range names {
			for _, outcome := range outcomes {
				m.ToolCalls.WithLabelValues(server, tool, outcome)
			}
		}
	}
	for _, kind := range kinds {
		m.Confirmed.WithLabelValues(kind)
		for _, v := range verdicts {
			m.Policy.WithLabelValues(kind, v)
		}
		for _, st := range statuses {
			m.Requests.WithLabelValues(kind, st)
		}
	}
	for _, s := range surfaces {
		m.Denied.WithLabelValues(s)
	}
	m.ChatTurns.WithLabelValues("ok")
	m.ChatTurns.WithLabelValues("error")
}

// Handler - /metrics
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
