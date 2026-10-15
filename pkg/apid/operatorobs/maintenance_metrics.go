package operatorobs

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// MaintenanceMetrics has only closed operation/outcome labels. Counters and
// timestamps describe this process's acknowledged attempts, not global totals.
// Durable last-success receipts and exact backlog remain available in the API.
type MaintenanceMetrics struct {
	attempts          *prometheus.CounterVec
	duration          *prometheus.HistogramVec
	lastAttempt       *prometheus.GaugeVec
	lastSuccess       *prometheus.GaugeVec
	collectionEnabled prometheus.Gauge
}

func NewMaintenanceMetrics(reg prometheus.Registerer) *MaintenanceMetrics {
	m := &MaintenanceMetrics{
		attempts:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apid_operator_alert_maintenance_attempts_total", Help: "Process-local acknowledged alert maintenance attempts by operation and outcome."}, []string{"operation", "outcome"}),
		duration:          prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "apid_operator_alert_maintenance_duration_seconds", Help: "Process-local alert maintenance attempt duration, including failures.", Buckets: prometheus.DefBuckets}, []string{"operation"}),
		lastAttempt:       prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "apid_operator_alert_maintenance_last_attempt_timestamp_seconds", Help: "Producer-written start timestamp of the last completed process-local attempt. Zero until an attempt completes."}, []string{"operation"}),
		lastSuccess:       prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "apid_operator_alert_maintenance_last_success_timestamp_seconds", Help: "Producer-written start timestamp of the last successful process-local attempt. Zero after restart until success; frozen on failure."}, []string{"operation"}),
		collectionEnabled: prometheus.NewGauge(prometheus.GaugeOpts{Name: "apid_operator_alert_collection_enabled", Help: "Whether this process has a configured Prometheus alert collector. Retention is independent."}),
	}
	reg.MustRegister(m.attempts, m.duration, m.lastAttempt, m.lastSuccess, m.collectionEnabled)
	for _, operation := range []string{"collection", "retention"} {
		for _, outcome := range []string{"success", "failure"} {
			m.attempts.WithLabelValues(operation, outcome)
		}
		m.duration.WithLabelValues(operation)
		m.lastAttempt.WithLabelValues(operation)
		m.lastSuccess.WithLabelValues(operation)
	}
	return m
}

func (m *MaintenanceMetrics) setCollectionEnabled(enabled bool) {
	if m == nil {
		return
	}
	value := 0.0
	if enabled {
		value = 1
	}
	m.collectionEnabled.Set(value)
}

func (m *MaintenanceMetrics) record(operation string, started time.Time, err error) {
	if m == nil {
		return
	}
	outcome := "success"
	if err != nil {
		outcome = "failure"
	}
	m.attempts.WithLabelValues(operation, outcome).Inc()
	m.duration.WithLabelValues(operation).Observe(time.Since(started).Seconds())
	at := float64(started.UnixNano()) / float64(time.Second)
	m.lastAttempt.WithLabelValues(operation).Set(at)
	if err == nil {
		m.lastSuccess.WithLabelValues(operation).Set(at)
	}
}
