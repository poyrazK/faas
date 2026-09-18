package reservedip

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics exposes bounded control-plane signals. Region and lease identifiers
// stay out of labels; operators can correlate them from reconciler logs.
type Metrics struct {
	sweeps    *prometheus.CounterVec
	latency   *prometheus.HistogramVec
	routes    prometheus.Gauge
	moved     prometheus.Counter
	failed    prometheus.Counter
	contended prometheus.Counter
}

func NewMetrics(reg prometheus.Registerer, prefix string) *Metrics {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "_")
	if prefix == "" {
		prefix = "reserved_ip"
	} else {
		prefix += "_reserved_ip"
	}
	m := &Metrics{
		sweeps: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_reconcile_total",
			Help: "Reserved IP route reconciliation outcomes.",
		}, []string{"outcome"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    prefix + "_reconcile_duration_seconds",
			Help:    "Reserved IP route reconciliation duration in seconds.",
			Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 15, 30},
		}, []string{"outcome"}),
		routes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_desired_routes",
			Help: "Number of routes in the latest reserved IP desired set.",
		}),
		moved: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prefix + "_route_moves_total",
			Help: "Reserved IP routes moved between compute nodes.",
		}),
		failed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prefix + "_reconcile_failures_total",
			Help: "Reserved IP route reconciliation failures.",
		}),
		contended: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prefix + "_reconcile_contention_total",
			Help: "Reserved IP route reconciliation compare-and-swap races.",
		}),
	}
	if reg != nil {
		reg.MustRegister(m.sweeps, m.latency, m.routes, m.moved, m.failed, m.contended)
	}
	for _, outcome := range []string{"ready", "error", "contended"} {
		m.sweeps.WithLabelValues(outcome)
		m.latency.WithLabelValues(outcome)
	}
	return m
}

func (m *Metrics) Observe(observation ReconcileObservation) {
	if m == nil {
		return
	}
	outcome := observation.Outcome
	switch outcome {
	case "ready", "error", "contended":
	default:
		outcome = "error"
	}
	m.sweeps.WithLabelValues(outcome).Inc()
	if observation.Duration >= 0 {
		m.latency.WithLabelValues(outcome).Observe(observation.Duration.Seconds())
	}
	m.routes.Set(float64(observation.Routes))
	if observation.Moved > 0 {
		m.moved.Add(float64(observation.Moved))
	}
	if observation.Failed > 0 {
		m.failed.Add(float64(observation.Failed))
	}
	if observation.Contended > 0 {
		m.contended.Add(float64(observation.Contended))
	}
}

// ObserveDuration keeps alternate daemon wiring nil-safe when only a sweep
// duration is available.
func (m *Metrics) ObserveDuration(outcome string, duration time.Duration) {
	m.Observe(ReconcileObservation{Outcome: outcome, Duration: duration})
}
