package main

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// adr: 20261102182856
func TestBuildLogRetentionMetrics(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 2, 18, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	m := newBuildLogRetentionMetrics(reg, func() time.Time { return now })
	m.observe(5, nil)
	assertGaugeValue(t, reg, "meterd_build_log_retention_last_success_timestamp_seconds", float64(now.Unix()))
	m.observe(3, errors.New("database unavailable"))
	m.observe(0, nil)
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{
		"meterd_build_log_retention_deleted_total":  8,
		"meterd_build_log_retention_failures_total": 1,
	}
	for _, family := range families {
		if expected, ok := want[family.GetName()]; ok {
			if got := family.GetMetric()[0].GetCounter().GetValue(); got != expected {
				t.Fatalf("%s=%v, want %v", family.GetName(), got, expected)
			}
			delete(want, family.GetName())
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing metrics: %v", want)
	}
}
