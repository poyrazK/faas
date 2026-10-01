package udpd

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics uses only fixed label sets. Peer addresses, ports, app/account IDs
// and arbitrary error strings never become Prometheus labels.
type Metrics struct {
	registry        *prometheus.Registry
	active          prometheus.Gauge
	listeners       prometheus.Gauge
	started         prometheus.Counter
	completed       *prometheus.CounterVec
	duration        *prometheus.HistogramVec
	drops           *prometheus.CounterVec
	datagrams       *prometheus.CounterVec
	bytes           *prometheus.CounterVec
	reconcileErrors prometheus.Counter
}

func NewMetrics(registry *prometheus.Registry, prefix string) *Metrics {
	if registry == nil {
		registry = prometheus.NewRegistry()
	}
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "_")
	if prefix == "" {
		prefix = "udpd"
	}
	m := &Metrics{registry: registry,
		active:          prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_udp_active_peers", Help: "UDP peers including pending admission."}),
		listeners:       prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_udp_listeners", Help: "Currently serving public UDP sockets."}),
		started:         prometheus.NewCounter(prometheus.CounterOpts{Name: prefix + "_udp_peers_started_total", Help: "UDP peers allocated after source and resource checks."}),
		completed:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_udp_peers_completed_total", Help: "UDP peer completions by terminal outcome."}, []string{"outcome"}),
		duration:        prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: prefix + "_udp_peer_duration_seconds", Help: "UDP peer lifetime including admission."}, []string{"outcome"}),
		drops:           prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_udp_datagrams_dropped_total", Help: "UDP datagrams dropped at the public socket boundary."}, []string{"reason"}),
		datagrams:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_udp_datagrams_total", Help: "Inbound datagrams queued and outbound datagrams successfully sent."}, []string{"direction"}),
		bytes:           prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_udp_payload_bytes_total", Help: "Payload bytes queued inbound or successfully sent outbound."}, []string{"direction"}),
		reconcileErrors: prometheus.NewCounter(prometheus.CounterOpts{Name: prefix + "_udp_reconciliation_errors_total", Help: "Failures to read, validate or bind the desired UDP listener set."}),
	}
	registry.MustRegister(m.active, m.listeners, m.started, m.completed, m.duration, m.drops, m.datagrams, m.bytes, m.reconcileErrors)
	for _, reason := range []string{"source_denied", "truncated", "inbound_rate", "outbound_rate", "peer_limit", "queue_full", "stale_reply", "write_error", "other"} {
		m.drops.WithLabelValues(reason)
	}
	for _, outcome := range []string{"success", "canceled", "admission_error", "forward_error", "resource_exhausted", "idle_timeout", "other"} {
		m.completed.WithLabelValues(outcome)
		m.duration.WithLabelValues(outcome)
	}
	for _, direction := range []string{"client_to_guest", "guest_to_client"} {
		m.datagrams.WithLabelValues(direction)
		m.bytes.WithLabelValues(direction)
	}
	return m
}
func (m *Metrics) Registry() *prometheus.Registry {
	if m == nil {
		return nil
	}
	return m.registry
}
func (m *Metrics) beginPeer() time.Time {
	if m != nil {
		m.started.Inc()
		m.active.Inc()
	}
	return time.Now()
}
func (m *Metrics) endPeer(start time.Time, outcome string) {
	if m == nil {
		return
	}
	switch outcome {
	case "success", "canceled", "admission_error", "forward_error", "resource_exhausted", "idle_timeout":
	default:
		outcome = "other"
	}
	m.active.Dec()
	m.completed.WithLabelValues(outcome).Inc()
	m.duration.WithLabelValues(outcome).Observe(time.Since(start).Seconds())
}
func (m *Metrics) drop(reason string) {
	if m == nil {
		return
	}
	switch reason {
	case "source_denied", "truncated", "inbound_rate", "outbound_rate", "peer_limit", "queue_full", "stale_reply", "write_error":
	default:
		reason = "other"
	}
	m.drops.WithLabelValues(reason).Inc()
}
func (m *Metrics) packet(size int, outbound bool) {
	if m == nil {
		return
	}
	direction := "client_to_guest"
	if outbound {
		direction = "guest_to_client"
	}
	m.datagrams.WithLabelValues(direction).Inc()
	m.bytes.WithLabelValues(direction).Add(float64(size))
}
func (m *Metrics) listener(delta float64) {
	if m != nil {
		m.listeners.Add(delta)
	}
}
