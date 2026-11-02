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
			Help: "Address records edgednsd created or deleted, by name and action; dry_run=true counts the changes a dry run only logged (ADR-20261102081618).",
		}, []string{"name", "action", "dry_run"}),
		probeFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "edge_dns_probe_failures_total",
			Help: "Readiness probes of a compute node's edge that failed (ADR-20261102081618).",
		}, []string{"node"}),
		nodeIn: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "edge_dns_node_in",
			Help: "1 while a compute node's address is in the managed names' records, 0 while it is withdrawn (ADR-20261102081618).",
		}, []string{"node"}),
	}
	reg.MustRegister(m.records, m.changes, m.probeFailures, m.nodeIn)
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
