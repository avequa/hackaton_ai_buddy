package metrics_test

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avequa/ai-it-buddy/core/internal/metrics"
)

// Известные серии есть с нулем сразу после старта: иначе increase() не видит первое событие
func TestInitExposesZeroSeries(t *testing.T) {
	m := metrics.New()
	m.Init(map[string][]string{"access": {"draft_access_request"}}, []string{"ok"}, []string{"access"},
		[]string{"ok", "needs_mentor"}, []string{"approved"}, []string{"ui_foreign"})
	m.InitRadar([]string{"vpn"})
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{
		`buddy_tool_calls_total{outcome="ok",server="access",tool="draft_access_request"} 0`,
		`buddy_security_events_total{kind="chat_attack"} 0`,
		`buddy_store_flushes_total{result="error"} 0`,
		`buddy_radar_checks_total{result="error",service="vpn"} 0`,
		`buddy_radar_error_rate{service="vpn"} 0`,
		`buddy_incidents_total{event="opened"} 0`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %s", want)
		}
	}
}
