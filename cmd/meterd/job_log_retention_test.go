package main

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/prometheus/client_golang/prometheus"
)

// adr: 20261102183152
func TestJobLogRetentionMetrics(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 2, 18, 0, 0, 0, time.UTC)
	reg := prometheus.NewRegistry()
	m := newJobLogRetentionMetrics(reg, func() time.Time { return now })
	m.observe(meter.JobLogsCleared{Runs: 2, Tasks: 5, Attempts: 6}, nil)
	assertGaugeValue(t, reg, "meterd_job_logs_retention_last_success_timestamp_seconds", float64(now.Unix()))
	m.observe(meter.JobLogsCleared{Runs: 1, Tasks: 1}, errors.New("database unavailable"))
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	cleared := map[string]float64{}
	var failures float64
	for _, family := range families {
		switch family.GetName() {
		case "meterd_job_logs_retention_cleared_total":
			for _, metric := range family.GetMetric() {
				cleared[metric.GetLabel()[0].GetValue()] = metric.GetCounter().GetValue()
			}
		case "meterd_job_logs_retention_failures_total":
			failures = family.GetMetric()[0].GetCounter().GetValue()
		}
	}
	want := map[string]float64{"job_runs": 3, "job_tasks": 6, "job_task_attempts": 6}
	for table, n := range want {
		if cleared[table] != n {
			t.Fatalf("cleared %s = %v, want %v (all: %v)", table, cleared[table], n, cleared)
		}
	}
	if failures != 1 {
		t.Fatalf("failures = %v, want 1", failures)
	}
}
