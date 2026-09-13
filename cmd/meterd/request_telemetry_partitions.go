package main

import (
	"time"

	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/prometheus/client_golang/prometheus"
)

type requestTelemetryPartitionMetrics struct {
	now            func() time.Time
	lastSuccess    prometheus.Gauge
	coveredMonths  prometheus.Gauge
	currentCovered prometheus.Gauge
	defaultRows    prometheus.Gauge
	defaultLatest  prometheus.Gauge
	failures       prometheus.Counter
}

func newRequestTelemetryPartitionMetrics(reg *prometheus.Registry, now func() time.Time) *requestTelemetryPartitionMetrics {
	if now == nil {
		now = time.Now
	}
	m := &requestTelemetryPartitionMetrics{
		now: now,
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_request_telemetry_partition_last_success_timestamp_seconds",
			Help: "Unix timestamp of the latest successful request telemetry partition reconciliation.",
		}),
		coveredMonths: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_request_telemetry_partition_covered_months",
			Help: "Number of attached explicit request telemetry partitions across the current and next two months.",
		}),
		currentCovered: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_request_telemetry_partition_current_covered",
			Help: "Whether an attached explicit request telemetry partition covers the current month.",
		}),
		defaultRows: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_request_telemetry_default_rows",
			Help: "Rows currently retained in the request telemetry default partition.",
		}),
		defaultLatest: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_request_telemetry_default_latest_received_timestamp_seconds",
			Help: "Latest received_at timestamp in the request telemetry default partition, or zero when empty.",
		}),
		failures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "meterd_request_telemetry_partition_reconcile_failures_total",
			Help: "Failed request telemetry partition reconciliation passes.",
		}),
	}
	reg.MustRegister(m.lastSuccess, m.coveredMonths, m.currentCovered, m.defaultRows, m.defaultLatest, m.failures)
	return m
}

func (m *requestTelemetryPartitionMetrics) observe(coverage meter.RequestTelemetryPartitionCoverage, err error) {
	if err != nil {
		m.failures.Inc()
		return
	}
	m.lastSuccess.Set(float64(m.now().Unix()))
	m.coveredMonths.Set(float64(coverage.CoveredMonths))
	if coverage.CurrentMonthCovered {
		m.currentCovered.Set(1)
	} else {
		m.currentCovered.Set(0)
	}
	m.defaultRows.Set(float64(coverage.DefaultRows))
	latest := float64(0)
	if !coverage.DefaultLatestReceived.IsZero() && coverage.DefaultLatestReceived.Unix() > 0 {
		latest = float64(coverage.DefaultLatestReceived.Unix())
	}
	m.defaultLatest.Set(latest)
}
