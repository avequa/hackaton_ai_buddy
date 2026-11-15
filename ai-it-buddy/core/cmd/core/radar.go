package main

import (
	"github.com/avequa/ai-it-buddy/core/internal/domain"
	"github.com/avequa/ai-it-buddy/core/internal/metrics"
)

type radarIncidents struct{ core *domain.Core }

func (r radarIncidents) AutoOpenIncident(service, summary, message string) (string, bool, error) {
	inc, opened, err := r.core.AutoOpenIncident(service, summary, message)
	return inc.ID, opened, err
}

func (r radarIncidents) AutoResolveIncident(service string) (string, bool) {
	inc, ok := r.core.AutoResolveIncident(service)
	return inc.ID, ok
}

type radarObserver struct{ m *metrics.Metrics }

func (o radarObserver) Checked(service, result string, errorRate float64) {
	o.m.RadarChecks.WithLabelValues(service, result).Inc()
	if result != "error" {
		o.m.RadarErrorRate.WithLabelValues(service).Set(errorRate)
	}
}

func (o radarObserver) Incident(event string) { o.m.Incidents.WithLabelValues(event).Inc() }
