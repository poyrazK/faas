package logarchive

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type retentionMetrics struct {
	scanned, collected prometheus.Counter
	failures           *prometheus.CounterVec
	lastSuccess        prometheus.Gauge
}

func newRetentionMetrics(reg prometheus.Registerer) retentionMetrics {
	if reg == nil {
		return retentionMetrics{}
	}
	m := retentionMetrics{
		scanned:     prometheus.NewCounter(prometheus.CounterOpts{Name: "apid_log_archive_retention_scanned_total", Help: "Archive descriptors visited by bounded remote retention passes."}),
		collected:   prometheus.NewCounter(prometheus.CounterOpts{Name: "apid_log_archive_retention_collected_total", Help: "Expired archive descriptors successfully collected or already removed."}),
		failures:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apid_log_archive_retention_failures_total", Help: "Remote archive retention failures by operation."}, []string{"operation"}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{Name: "apid_log_archive_retention_last_success_timestamp_seconds", Help: "Time of the last successful bounded remote retention pass."}),
	}
	reg.MustRegister(m.scanned, m.collected, m.failures, m.lastSuccess)
	for _, operation := range []string{"list", "lock", "retire", "delete_object", "delete_metadata"} {
		m.failures.WithLabelValues(operation)
	}
	return m
}

func (m retentionMetrics) failure(operation string) {
	if m.failures != nil {
		m.failures.WithLabelValues(operation).Inc()
	}
}

func (m retentionMetrics) observe(result RetentionResult, success bool, now time.Time) {
	if m.scanned == nil {
		return
	}
	m.scanned.Add(float64(result.Scanned))
	m.collected.Add(float64(result.Collected))
	if success {
		m.lastSuccess.Set(float64(now.Unix()))
	}
}
