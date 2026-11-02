package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// buildLogRetentionMetrics reports the build log sweep (ADR-20261102182856)
// the way logEventMaintenanceMetrics reports the log event sweep.
type buildLogRetentionMetrics struct {
	now         func() time.Time
	deleted     prometheus.Counter
	lastSuccess prometheus.Gauge
	failures    prometheus.Counter
}

func newBuildLogRetentionMetrics(reg *prometheus.Registry, now func() time.Time) *buildLogRetentionMetrics {
	if now == nil {
		now = time.Now
	}
	m := &buildLogRetentionMetrics{
		now: now,
		deleted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "meterd_build_log_retention_deleted_total",
			Help: "Build log rows (deployment_logs) removed by build log retention.",
		}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_build_log_retention_last_success_timestamp_seconds",
			Help: "Latest build log retention pass that finished its slice.",
		}),
		failures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "meterd_build_log_retention_failures_total",
			Help: "Build log retention passes that failed or hit the batch cap.",
		}),
	}
	reg.MustRegister(m.deleted, m.lastSuccess, m.failures)
	return m
}

func (m *buildLogRetentionMetrics) observe(deleted int64, err error) {
	if deleted > 0 {
		m.deleted.Add(float64(deleted))
	}
	if err != nil {
		m.failures.Inc()
		return
	}
	m.lastSuccess.Set(float64(m.now().Unix()))
}
