package privatenetwork

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics exposes the bounded, daemon-local signals for private-network
// convergence. App and node identifiers stay in structured logs rather than
// metric labels so a busy fleet cannot create unbounded time-series cardinality.
type Metrics struct {
	reconcileTotal   *prometheus.CounterVec
	reconcileLatency *prometheus.HistogramVec
	routeNodesTotal  *prometheus.CounterVec
	fabricNodesTotal *prometheus.CounterVec
}

// NewMetrics registers private-network convergence metrics on an existing
// daemon registry. The prefix should be the daemon metric prefix (for example
// "schedd") so the names cannot collide with another daemon's registry when
// dashboards merge scrapes.
func NewMetrics(reg prometheus.Registerer, prefix string) *Metrics {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "_")
	if prefix == "" {
		prefix = "private_network"
	} else {
		prefix += "_private_network"
	}
	m := &Metrics{
		reconcileTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_reconcile_total",
			Help: "Private-network attachment reconciliation outcomes by status.",
		}, []string{"outcome"}),
		reconcileLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    prefix + "_reconcile_duration_seconds",
			Help:    "Private-network attachment reconciliation duration in seconds.",
			Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 15, 30},
		}, []string{"outcome"}),
		routeNodesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_route_nodes_total",
			Help: "Private-network route applications by per-node outcome.",
		}, []string{"status"}),
		fabricNodesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_fabric_nodes_total",
			Help: "Gregale private-network fabric preparations by per-node outcome.",
		}, []string{"status"}),
	}
	if reg != nil {
		reg.MustRegister(m.reconcileTotal, m.reconcileLatency, m.routeNodesTotal, m.fabricNodesTotal)
	}
	for _, outcome := range []string{"ready", "pending", "error", "contended"} {
		m.reconcileTotal.WithLabelValues(outcome)
		m.reconcileLatency.WithLabelValues(outcome)
	}
	for _, status := range []string{"ready", "error"} {
		m.routeNodesTotal.WithLabelValues(status)
		m.fabricNodesTotal.WithLabelValues(status)
	}
	return m
}

// Observe records one attachment observation plus per-node fabric and route results.
func (m *Metrics) Observe(observation ReconcileObservation) {
	if m == nil {
		return
	}
	outcome := normalizeOutcome(observation.Outcome)
	m.reconcileTotal.WithLabelValues(outcome).Inc()
	if observation.Duration >= 0 {
		m.reconcileLatency.WithLabelValues(outcome).Observe(observation.Duration.Seconds())
	}
	for _, node := range observation.Nodes {
		status := node.Status
		if status != "ready" && status != "error" {
			continue
		}
		m.routeNodesTotal.WithLabelValues(status).Inc()
	}
	for _, node := range observation.FabricNodes {
		status := node.Status
		if status != "ready" && status != "error" {
			continue
		}
		m.fabricNodesTotal.WithLabelValues(status).Inc()
	}
}

// ObserveDuration is a small convenience for callers that only have a sweep
// duration and no node report. It is primarily useful to keep instrumentation
// nil-safe in tests and alternate daemon wiring.
func (m *Metrics) ObserveDuration(outcome string, duration time.Duration) {
	if m == nil {
		return
	}
	outcome = normalizeOutcome(outcome)
	m.reconcileTotal.WithLabelValues(outcome).Inc()
	if duration >= 0 {
		m.reconcileLatency.WithLabelValues(outcome).Observe(duration.Seconds())
	}
}

func normalizeOutcome(outcome string) string {
	switch outcome {
	case "ready", "pending", "error", "contended":
		return outcome
	default:
		return "error"
	}
}
