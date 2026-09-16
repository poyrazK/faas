package main

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/prometheus/client_golang/prometheus"
)

func TestRequestTelemetryPartitionMetricsExposeCoverageAndFailures(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 13, 14, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	m := newRequestTelemetryPartitionMetrics(reg, func() time.Time { return now })
	m.observe(meter.RequestTelemetryPartitionCoverage{
		CoveredMonths:         3,
		CurrentMonthCovered:   true,
		DefaultRows:           4,
		DefaultLatestReceived: now.Add(-time.Minute),
	}, nil)
	assertGaugeValue(t, reg, "meterd_request_telemetry_partition_last_success_timestamp_seconds", float64(now.Unix()))
	assertGaugeValue(t, reg, "meterd_request_telemetry_partition_covered_months", 3)
	assertGaugeValue(t, reg, "meterd_request_telemetry_partition_current_covered", 1)
	assertGaugeValue(t, reg, "meterd_request_telemetry_default_rows", 4)
	assertGaugeValue(t, reg, "meterd_request_telemetry_default_latest_received_timestamp_seconds", float64(now.Add(-time.Minute).Unix()))

	m.observe(meter.RequestTelemetryPartitionCoverage{}, errors.New("postgres unavailable"))
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "meterd_request_telemetry_partition_reconcile_failures_total" {
			if got := family.GetMetric()[0].GetCounter().GetValue(); got != 1 {
				t.Fatalf("reconcile failures = %v, want 1", got)
			}
			return
		}
	}
	t.Fatal("reconcile failure counter not found")
}
