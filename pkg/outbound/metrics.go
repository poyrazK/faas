package outbound

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics is the bounded observability surface for outboundd. Operator IDs
// are configuration-owned; customer-created integrations share a fixed label
// so customer growth cannot create unbounded metric cardinality.
type Metrics struct {
	admissions             *prometheus.CounterVec
	rejections             *prometheus.CounterVec
	inFlight               *prometheus.GaugeVec
	upstreamRequests       *prometheus.CounterVec
	upstreamLatency        *prometheus.HistogramVec
	cacheRequests          *prometheus.CounterVec
	circuitEvents          *prometheus.CounterVec
	retryBudgetEvents      *prometheus.CounterVec
	providerCooldownEvents *prometheus.CounterVec
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
		cacheRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_response_cache_requests_total",
			Help: "Outbound response-cache lookups by integration and outcome (hit or miss).",
		}, []string{"integration_id", "outcome"}),
		circuitEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_circuit_breaker_events_total",
			Help: "Outbound circuit-breaker checks and outcomes by integration and bounded event.",
		}, []string{"integration_id", "event"}),
		retryBudgetEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_retry_budget_events_total",
			Help: "Shared outbound retry-budget checks by integration and bounded event.",
		}, []string{"integration_id", "event"}),
		providerCooldownEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbound_provider_cooldown_events_total",
			Help: "Shared provider-directed outbound cooldown checks by integration and bounded event.",
		}, []string{"integration_id", "event"}),
	}
	collectors := []prometheus.Collector{
		m.admissions,
		m.rejections,
		m.inFlight,
		m.upstreamRequests,
		m.upstreamLatency,
		m.cacheRequests,
		m.circuitEvents,
		m.retryBudgetEvents,
		m.providerCooldownEvents,
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

func (m *Metrics) ObserveCache(integrationID, outcome string) {
	if m == nil || m.cacheRequests == nil {
		return
	}
	m.cacheRequests.WithLabelValues(integrationID, outcome).Inc()
}

func (m *Metrics) ObserveCircuit(integrationID, event string) {
	if m == nil || m.circuitEvents == nil {
		return
	}
	m.circuitEvents.WithLabelValues(integrationID, event).Inc()
}

func (m *Metrics) ObserveRetryBudget(integrationID, event string) {
	if m == nil || m.retryBudgetEvents == nil {
		return
	}
	m.retryBudgetEvents.WithLabelValues(integrationID, event).Inc()
}

func (m *Metrics) ObserveProviderCooldown(integrationID, event string) {
	if m == nil || m.providerCooldownEvents == nil {
		return
	}
	m.providerCooldownEvents.WithLabelValues(integrationID, event).Inc()
}
