package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// adr: 740 — the dev-loop metrics carry only bounded labels: CLI-reported
// phases are validated upstream, and patch reasons outside the known set
// collapse to "other".
func TestDevLoopMetricsObserveSyncAndPatchPlans(t *testing.T) {
	m := newDevLoopMetrics(prometheus.NewRegistry(), "apid")
	m.observeSync(api.RecordDevSyncRequest{Status: "live", EditToLiveMS: 4200, WithinSLO: true, Phases: []api.DevSyncPhase{
		{Phase: "sync", Status: "completed", DurationMS: 300},
		{Phase: "build", Status: "completed", DurationMS: 3100},
		{Phase: "route", Status: "completed"},
	}})
	if got := testutil.ToFloat64(m.syncs.WithLabelValues("live", "true")); got != 1 {
		t.Fatalf("live syncs within SLO = %v, want 1", got)
	}
	if got := testutil.CollectAndCount(m.editToLive); got != 1 {
		t.Fatalf("edit-to-live series = %d, want 1", got)
	}
	if got := testutil.CollectAndCount(m.phases); got != 2 {
		t.Fatalf("phase series = %d, want 2 (zero-duration phases are skipped)", got)
	}
	m.observePatchPlan(devPatchOutcomeIneligible, api.DevPatchReasonBuildCommand)
	m.observePatchPlan(devPatchOutcomeIneligible, "unexpected_reason")
	m.observePatchPlan(devPatchOutcomePublished, "")
	for labels, want := range map[[2]string]float64{
		{devPatchOutcomeIneligible, api.DevPatchReasonBuildCommand}: 1,
		{devPatchOutcomeIneligible, "other"}:                        1,
		{devPatchOutcomePublished, ""}:                              1,
	} {
		if got := testutil.ToFloat64(m.patchPlans.WithLabelValues(labels[0], labels[1])); got != want {
			t.Fatalf("patch plans %v = %v, want %v", labels, got, want)
		}
	}
	// Re-registration on the same registry reuses the collectors.
	if again := newDevLoopMetrics(m.registry, "apid"); again.syncs != m.syncs {
		t.Fatal("re-registration created a second sync counter")
	}
	var nilMetrics *devLoopMetrics
	nilMetrics.observeSync(api.RecordDevSyncRequest{Status: "live"})
	nilMetrics.observePatchPlan(devPatchOutcomePublished, "")
}
