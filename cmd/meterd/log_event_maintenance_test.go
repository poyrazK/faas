package main

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/prometheus/client_golang/prometheus"
)

func TestLogEventMaintenanceMetrics(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 22, 14, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	m := newLogEventMaintenanceMetrics(reg, func() time.Time { return now })
	m.observePartition(meter.LogEventPartitionCoverage{
		CoveredMonths: 3, CurrentMonthCovered: true,
		DefaultRows: 2, DefaultLatest: now.Add(-time.Minute),
	}, nil)
	m.observeRetention(5, nil)
	assertGaugeValue(t, reg, "meterd_log_events_partition_covered_months", 3)
	assertGaugeValue(t, reg, "meterd_log_events_partition_current_covered", 1)
	assertGaugeValue(t, reg, "meterd_log_events_default_rows", 2)
	assertGaugeValue(t, reg, "meterd_log_events_default_latest_occurred_timestamp_seconds", float64(now.Add(-time.Minute).Unix()))
	assertGaugeValue(t, reg, "meterd_log_events_partition_last_success_timestamp_seconds", float64(now.Unix()))
	assertGaugeValue(t, reg, "meterd_log_events_retention_last_success_timestamp_seconds", float64(now.Unix()))
	m.observePartition(meter.LogEventPartitionCoverage{}, errors.New("database unavailable"))
	m.observeRetention(3, errors.New("database unavailable"))
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{
		"meterd_log_events_partition_reconcile_failures_total": 1,
		"meterd_log_events_retention_deleted_total":            8,
		"meterd_log_events_retention_failures_total":           1,
	}
	for _, family := range families {
		if expected, ok := want[family.GetName()]; ok {
			got := family.GetMetric()[0].GetCounter().GetValue()
			if got != expected {
				t.Fatalf("%s=%v, want %v", family.GetName(), got, expected)
			}
			delete(want, family.GetName())
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing metrics: %v", want)
	}
}
