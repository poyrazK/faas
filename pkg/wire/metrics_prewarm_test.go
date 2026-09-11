package wire

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPrewarmMetricsExposeClosedLifecycleAndAdmissionTotals(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewPrewarmMetrics(reg)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	m.ObserveScheduled()
	m.ObserveFired("succeeded", 3, now.Add(time.Minute), now.Add(-time.Hour), now)
	m.ObserveFired("partial", 1, now.Add(2*time.Minute), now.Add(-time.Hour), now)
	m.ObserveFired("failed", 0, now.Add(3*time.Minute), now.Add(-time.Hour), now)
	m.ObserveExpired()
	m.ObserveCancelled()

	if got := testutil.ToFloat64(m.IntentEventsTotal.WithLabelValues("scheduled")); got != 1 {
		t.Fatalf("scheduled events = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.IntentEventsTotal.WithLabelValues("succeeded")); got != 1 {
		t.Fatalf("succeeded events = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.IntentEventsTotal.WithLabelValues("partial")); got != 1 {
		t.Fatalf("partial events = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.IntentEventsTotal.WithLabelValues("failed")); got != 1 {
		t.Fatalf("failed events = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.IntentEventsTotal.WithLabelValues("expired")); got != 1 {
		t.Fatalf("expired events = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.IntentEventsTotal.WithLabelValues("cancelled")); got != 1 {
		t.Fatalf("cancelled events = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.AdmittedInstancesTotal); got != 4 {
		t.Fatalf("admitted instances = %v, want 4", got)
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "prewarm_fire_offset_seconds" && family.GetMetric()[0].GetHistogram().GetSampleCount() != 3 {
			t.Fatalf("fire offset samples = %d, want 3", family.GetMetric()[0].GetHistogram().GetSampleCount())
		}
	}
}
