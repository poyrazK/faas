package main

import (
	"time"

	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/prometheus/client_golang/prometheus"
)

type logEventMaintenanceMetrics struct {
	now                  func() time.Time
	partitionLastSuccess prometheus.Gauge
	coveredMonths        prometheus.Gauge
	currentCovered       prometheus.Gauge
	defaultRows          prometheus.Gauge
	defaultLatest        prometheus.Gauge
	partitionFailures    prometheus.Counter
	retentionDeleted     prometheus.Counter
	retentionLastSuccess prometheus.Gauge
	retentionFailures    prometheus.Counter
}

func newLogEventMaintenanceMetrics(reg *prometheus.Registry, now func() time.Time) *logEventMaintenanceMetrics {
	if now == nil {
		now = time.Now
	}
	m := &logEventMaintenanceMetrics{
		now: now,
		partitionLastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_log_events_partition_last_success_timestamp_seconds",
			Help: "Latest successful log event partition reconciliation.",
		}),
		coveredMonths: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_log_events_partition_covered_months",
			Help: "Attached log event partitions across the current and next two months.",
		}),
		currentCovered: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_log_events_partition_current_covered",
			Help: "Whether the current UTC month has an explicit log event partition.",
		}),
		defaultRows: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_log_events_default_rows",
			Help: "Rows in the catch-all log event partition.",
		}),
		defaultLatest: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_log_events_default_latest_occurred_timestamp_seconds",
			Help: "Latest occurrence timestamp in the catch-all log event partition, or zero when empty.",
		}),
		partitionFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "meterd_log_events_partition_reconcile_failures_total",
			Help: "Failed log event partition reconciliation passes.",
		}),
		retentionDeleted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "meterd_log_events_retention_deleted_total",
			Help: "Log event rows removed by plan-aware retention.",
		}),
		retentionLastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_log_events_retention_last_success_timestamp_seconds",
			Help: "Latest successful log event retention and partition drop pass.",
		}),
		retentionFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "meterd_log_events_retention_failures_total",
			Help: "Failed log event retention or partition drop passes.",
		}),
	}
	reg.MustRegister(m.partitionLastSuccess, m.coveredMonths, m.currentCovered,
		m.defaultRows, m.defaultLatest, m.partitionFailures, m.retentionDeleted,
		m.retentionLastSuccess, m.retentionFailures)
	return m
}

func (m *logEventMaintenanceMetrics) observePartition(coverage meter.LogEventPartitionCoverage, err error) {
	if err != nil {
		m.partitionFailures.Inc()
		return
	}
	m.partitionLastSuccess.Set(float64(m.now().Unix()))
	m.coveredMonths.Set(float64(coverage.CoveredMonths))
	if coverage.CurrentMonthCovered {
		m.currentCovered.Set(1)
	} else {
		m.currentCovered.Set(0)
	}
	m.defaultRows.Set(float64(coverage.DefaultRows))
	latest := float64(0)
	if !coverage.DefaultLatest.IsZero() && coverage.DefaultLatest.Unix() > 0 {
		latest = float64(coverage.DefaultLatest.Unix())
	}
	m.defaultLatest.Set(latest)
}

func (m *logEventMaintenanceMetrics) observeRetention(deleted int64, err error) {
	if deleted > 0 {
		m.retentionDeleted.Add(float64(deleted))
	}
	if err != nil {
		m.retentionFailures.Inc()
		return
	}
	m.retentionLastSuccess.Set(float64(m.now().Unix()))
}
