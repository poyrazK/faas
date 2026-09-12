package outbound

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics is the bounded observability surface for outboundd. Integration IDs
// are configuration-owned values, so the label cardinality is bounded by the
// number of configured integrations rather than by request paths or provider
// response bodies.
type Metrics struct {
	admissions       *prometheus.CounterVec
	rejections       *prometheus.CounterVec
	inFlight         *prometheus.GaugeVec
	upstreamRequests *prometheus.CounterVec
	upstreamLatency  *prometheus.HistogramVec
}

// NewMetrics registers the outbound gateway metric families against reg.
// Callers normally pass the daemon's private OpsMetrics registry so the
// metrics are exposed on the existing operator-only /metrics listener.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	if reg == nil {
		return nil, errors.New("outbound: nil prometheus registerer")
	}
	m := &Metrics{
		admissions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_admissions_total",
			Help: "Outbound gateway admission attempts by integration and outcome (granted, rejected, or error).",
		}, []string{"integration_id", "outcome"}),
		rejections: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_rejections_total",
			Help: "Outbound gateway requests rejected by integration and bounded reason.",
		}, []string{"integration_id", "reason"}),
		inFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "outbound_in_flight",
			Help: "Outbound requests currently admitted by this gateway process, by integration.",
		}, []string{"integration_id"}),
		upstreamRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_upstream_requests_total",
			Help: "Outbound provider request outcomes by integration and HTTP status class.",
		}, []string{"integration_id", "outcome"}),
		upstreamLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "outbound_upstream_latency_seconds",
			Help:    "Outbound provider request latency by integration.",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
		}, []string{"integration_id"}),
	}
	collectors := []prometheus.Collector{
		m.admissions,
		m.rejections,
		m.inFlight,
		m.upstreamRequests,
		m.upstreamLatency,
	}
	for _, collector := range collectors {
		if err := reg.Register(collector); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Metrics) ObserveAdmission(integrationID, outcome string) {
	if m == nil || m.admissions == nil {
		return
	}
	m.admissions.WithLabelValues(integrationID, outcome).Inc()
}

func (m *Metrics) ObserveRejection(integrationID, reason string) {
	if m == nil || m.rejections == nil {
		return
	}
	m.rejections.WithLabelValues(integrationID, reason).Inc()
}

func (m *Metrics) IncInFlight(integrationID string) {
	if m == nil || m.inFlight == nil {
		return
	}
	m.inFlight.WithLabelValues(integrationID).Inc()
}

func (m *Metrics) DecInFlight(integrationID string) {
	if m == nil || m.inFlight == nil {
		return
	}
	m.inFlight.WithLabelValues(integrationID).Dec()
}

func (m *Metrics) ObserveUpstream(integrationID string, status int, latency time.Duration) {
	if m == nil {
		return
	}
	outcome := "other"
	switch {
	case status >= 200 && status < 300:
		outcome = "2xx"
	case status >= 300 && status < 400:
		outcome = "3xx"
	case status >= 400 && status < 500:
		outcome = "4xx"
	case status >= 500 && status < 600:
		outcome = "5xx"
	}
	m.upstreamRequests.WithLabelValues(integrationID, outcome).Inc()
	if latency >= 0 {
		m.upstreamLatency.WithLabelValues(integrationID).Observe(latency.Seconds())
	}
}

func (m *Metrics) ObserveUpstreamError(integrationID string, latency time.Duration) {
	if m == nil {
		return
	}
	m.upstreamRequests.WithLabelValues(integrationID, "error").Inc()
	if latency >= 0 {
		m.upstreamLatency.WithLabelValues(integrationID).Observe(latency.Seconds())
	}
}
