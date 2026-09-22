// Package tcpmetrics contains bounded instrumentation for raw TCP ingress.
package tcpmetrics

import (
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	outcomeSuccess  = "success"
	outcomeError    = "error"
	outcomeCanceled = "canceled"
	outcomeRejected = "rejected"
)

// Metrics is the Prometheus bundle for one raw-TCP ingress daemon.
//
// Account labels are populated from durable listener ownership rather than
// client input. This makes the active-session view useful without exposing
// arbitrary wire data as metric labels.
type Metrics struct {
	registry *prometheus.Registry

	accepted       prometheus.Counter
	rejected       *prometheus.CounterVec
	completed      *prometheus.CounterVec
	active         prometheus.Gauge
	activeAccounts *prometheus.GaugeVec
	duration       *prometheus.HistogramVec
	bytes          *prometheus.CounterVec
	idleTimeouts   prometheus.Counter
}

// New creates and registers a TCP metrics bundle. A nil registry creates an
// isolated registry suitable for callers that do not share a daemon registry.
func New(registry *prometheus.Registry, prefix string) *Metrics {
	if registry == nil {
		registry = prometheus.NewRegistry()
	}
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "_")
	if prefix == "" {
		prefix = "tcpd"
	}
	m := &Metrics{
		registry: registry,
		accepted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prefix + "_tcp_sessions_accepted_total",
			Help: "Number of raw TCP sessions accepted by the public ingress.",
		}),
		rejected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_tcp_sessions_rejected_total",
			Help: "Number of raw TCP sessions rejected before forwarding, labelled by reason.",
		}, []string{"reason"}),
		completed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_tcp_sessions_completed_total",
			Help: "Number of raw TCP sessions that completed after acceptance, labelled by outcome.",
		}, []string{"outcome"}),
		active: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_tcp_active_sessions",
			Help: "Number of raw TCP sessions currently being handled.",
		}),
		activeAccounts: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_tcp_active_sessions_by_account",
			Help: "Number of raw TCP sessions currently being handled, labelled by owning account.",
		}, []string{"account_id"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: prefix + "_tcp_session_duration_seconds",
			Help: "Duration of raw TCP sessions in seconds, labelled by terminal outcome.",
			Buckets: []float64{
				0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300, 900,
			},
		}, []string{"outcome"}),
		bytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_tcp_bytes_total",
			Help: "Number of raw TCP payload bytes forwarded, labelled by direction.",
		}, []string{"direction"}),
		idleTimeouts: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prefix + "_tcp_idle_timeouts_total",
			Help: "Number of raw TCP sessions terminated by the idle timeout.",
		}),
	}
	registry.MustRegister(
		m.accepted,
		m.rejected,
		m.completed,
		m.active,
		m.activeAccounts,
		m.duration,
		m.bytes,
		m.idleTimeouts,
	)
	for _, reason := range []string{"global_limit", "route_missing", "route_error", "invalid_route", "account_limit", "target_error"} {
		m.rejected.WithLabelValues(reason)
	}
	for _, outcome := range []string{outcomeSuccess, outcomeError, outcomeCanceled, outcomeRejected} {
		m.completed.WithLabelValues(outcome)
		m.duration.WithLabelValues(outcome)
	}
	for _, direction := range []string{"client_to_guest", "guest_to_client"} {
		m.bytes.WithLabelValues(direction)
	}
	return m
}

// Registry returns the registry populated by New.
func (m *Metrics) Registry() *prometheus.Registry {
	if m == nil {
		return nil
	}
	return m.registry
}

// Begin records an accepted session and returns its lifecycle handle.
func (m *Metrics) Begin() *Session {
	if m == nil {
		return nil
	}
	m.accepted.Inc()
	m.active.Inc()
	return &Session{metrics: m, started: time.Now()}
}

// AddBytes records forwarded payload bytes in one direction.
func (m *Metrics) AddBytes(direction string, n int64) {
	if m == nil || n <= 0 {
		return
	}
	m.bytes.WithLabelValues(direction).Add(float64(n))
}

// ObserveIdleTimeout records a session that ended because it was idle.
func (m *Metrics) ObserveIdleTimeout() {
	if m != nil {
		m.idleTimeouts.Inc()
	}
}

// Session tracks one accepted connection until it reaches a terminal state.
type Session struct {
	metrics  *Metrics
	started  time.Time
	account  string
	bound    bool
	finishMu sync.Once
}

// Bind associates the session with the route owner for account-level gauges.
func (s *Session) Bind(accountID string) {
	if s == nil || s.metrics == nil || s.bound {
		return
	}
	s.account = label(accountID)
	s.bound = true
	s.metrics.activeAccounts.WithLabelValues(s.account).Inc()
}

// Reject records a session rejected before forwarding began.
func (s *Session) Reject(reason string) {
	if s == nil || s.metrics == nil {
		return
	}
	s.finishMu.Do(func() {
		s.release()
		s.metrics.rejected.WithLabelValues(label(reason)).Inc()
		s.metrics.completed.WithLabelValues(outcomeRejected).Inc()
		s.metrics.duration.WithLabelValues(outcomeRejected).Observe(time.Since(s.started).Seconds())
	})
}

// Finish records a forwarded session's terminal outcome. Unknown outcomes are
// normalized to error so the metric label set remains closed.
func (s *Session) Finish(outcome string) {
	if s == nil || s.metrics == nil {
		return
	}
	if outcome != outcomeSuccess && outcome != outcomeError && outcome != outcomeCanceled {
		outcome = outcomeError
	}
	s.finishMu.Do(func() {
		s.release()
		s.metrics.completed.WithLabelValues(outcome).Inc()
		s.metrics.duration.WithLabelValues(outcome).Observe(time.Since(s.started).Seconds())
	})
}

func (s *Session) release() {
	s.metrics.active.Dec()
	if s.bound {
		s.metrics.activeAccounts.WithLabelValues(s.account).Dec()
	}
}

func label(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}
