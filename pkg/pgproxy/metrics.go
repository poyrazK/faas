package pgproxy

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics are pgproxyd's session metrics. A nil *Metrics records nothing.
type Metrics struct {
	sessions *prometheus.CounterVec
	active   prometheus.Gauge
	wake     prometheus.Histogram
	cancels  *prometheus.CounterVec
}

// NewMetrics registers the proxy metrics on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		sessions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pgproxyd_sessions_total",
			Help: "Client sessions by result: ok, tls (no TLS session), error, or the SQLSTATE sent to the client.",
		}, []string{"result"}),
		active: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "pgproxyd_active_sessions",
			Help: "Client sessions currently relayed to a database.",
		}),
		wake: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "pgproxyd_wake_seconds",
			Help:    "Time from an authenticated client to a running database (schedd WakeDatabase).",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60},
		}),
		cancels: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pgproxyd_cancel_requests_total",
			Help: "Cancel requests by result: ok (sent to the backend), forwarded (to the instance that issued the key), unknown (no such key) or failed.",
		}, []string{"result"}),
	}
	reg.MustRegister(m.sessions, m.active, m.wake, m.cancels)
	return m
}

func (m *Metrics) result(result string) {
	if m != nil {
		m.sessions.WithLabelValues(result).Inc()
	}
}

func (m *Metrics) sessionStarted() {
	if m != nil {
		m.active.Inc()
	}
}

func (m *Metrics) sessionEnded() {
	if m != nil {
		m.active.Dec()
	}
}

func (m *Metrics) woke(d time.Duration) {
	if m != nil {
		m.wake.Observe(d.Seconds())
	}
}

func (m *Metrics) cancelled(result string) {
	if m != nil {
		m.cancels.WithLabelValues(result).Inc()
	}
}
