package edgedns

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics are edgedns's series (ADR-20261102081618 §3). A nil *Metrics
// records nothing.
type Metrics struct {
	records       *prometheus.GaugeVec
	changes       *prometheus.CounterVec
	probeFailures *prometheus.CounterVec
	nodeIn        *prometheus.GaugeVec
	errors        *prometheus.CounterVec
	drainRefused  *prometheus.GaugeVec
}

// NewMetrics registers the series with reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		records: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "edge_dns_records",
			Help: "Address records behind a managed app host name after the last reconcile (ADR-20261102081618).",
		}, []string{"name"}),
		changes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "edge_dns_changes_total",
			Help: "Address records edgednsd created, switched between proxied and DNS-only (action=mode, ADR-20261103163954) or deleted, by name and action; dry_run=true counts the changes a dry run only logged (ADR-20261102081618).",
		}, []string{"name", "action", "dry_run"}),
		probeFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "edge_dns_probe_failures_total",
			Help: "Readiness probes of a compute node that failed: its edge's /readyz, or, while a managed name is DNS-only, a verified TLS handshake on its public address (ADR-20261102081618 and its 2026-11-05 amendment).",
		}, []string{"node"}),
		nodeIn: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "edge_dns_node_in",
			Help: "1 while a compute node's address is in the records of any managed name, 0 while it is withdrawn from all of them (ADR-20261102081618).",
		}, []string{"node"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "edge_dns_errors_total",
			Help: "Cloudflare DNS calls edgednsd could not complete, by op (read, create, mode, delete); while they fail the records stay as they are (ADR-20261102081618).",
		}, []string{"op"}),
	}
	m.drainRefused = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "edge_dns_drain_refused_nodes",
		Help: "Drained compute nodes whose edges still pass their probes and that edgednsd keeps in the records, by mode (proxied, dns-only), because withdrawing them would leave fewer than half of the healthy nodes: the registry is writable by every compute node, so a mass drain alone does not move DNS (ADR-20261107180000).",
	}, []string{"mode"})
	for _, op := range []string{"read", "create", "mode", "delete"} {
		m.errors.WithLabelValues(op)
	}
	for _, mode := range []string{"proxied", "dns-only"} {
		m.drainRefused.WithLabelValues(mode)
	}
	reg.MustRegister(m.records, m.changes, m.probeFailures, m.nodeIn, m.errors, m.drainRefused)
	return m
}

func (m *Metrics) setRecords(name string, n int) {
	if m != nil {
		m.records.WithLabelValues(name).Set(float64(n))
	}
}

func (m *Metrics) changed(name, action string, dryRun bool) {
	if m != nil {
		m.changes.WithLabelValues(name, action, strconv.FormatBool(dryRun)).Inc()
	}
}

func (m *Metrics) errored(op string) {
	if m != nil {
		m.errors.WithLabelValues(op).Inc()
	}
}

func (m *Metrics) setDrainRefused(mode string, n int) {
	if m != nil {
		m.drainRefused.WithLabelValues(mode).Set(float64(n))
	}
}

func (m *Metrics) probeFailed(node string) {
	if m != nil {
		m.probeFailures.WithLabelValues(node).Inc()
	}
}

func (m *Metrics) setNodeIn(node string, in bool) {
	if m == nil {
		return
	}
	v := 0.0
	if in {
		v = 1
	}
	m.nodeIn.WithLabelValues(node).Set(v)
}
