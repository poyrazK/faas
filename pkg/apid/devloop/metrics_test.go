package devloop

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// adr: 740 — the dev-loop metrics carry only bounded labels: CLI-reported
// phases are validated upstream, patch reasons outside the known set
// collapse to "other", and watch is "true" or "false" (ADR-1170).
func TestDevLoopMetricsObserveSyncAndPatchPlans(t *testing.T) {
	m := NewMetrics(prometheus.NewRegistry(), "apid")
	m.ObserveSync(api.RecordDevSyncRequest{Status: "live", EditToLiveMS: 4200, WithinSLO: true, Phases: []api.DevSyncPhase{
		{Phase: "sync", Status: "completed", DurationMS: 300},
		{Phase: "build", Status: "completed", DurationMS: 3100},
		{Phase: "route", Status: "completed"},
	}}, true)
	if got := testutil.ToFloat64(m.syncs.WithLabelValues("live", "true", "true")); got != 1 {
		t.Fatalf("live syncs within SLO = %v, want 1", got)
	}
	if got := testutil.CollectAndCount(m.editToLive); got != 1 {
		t.Fatalf("edit-to-live series = %d, want 1", got)
	}
	if got := testutil.CollectAndCount(m.phases); got != 2 {
		t.Fatalf("phase series = %d, want 2 (zero-duration phases are skipped)", got)
	}
	m.ObservePatchPlan(PatchIneligible, api.DevPatchReasonBuildCommand, false)
	m.ObservePatchPlan(PatchIneligible, "unexpected_reason", false)
	m.ObservePatchPlan(PatchPublished, "", true)
	for labels, want := range map[[3]string]float64{
		{PatchIneligible, api.DevPatchReasonBuildCommand, "false"}: 1,
		{PatchIneligible, "other", "false"}:                        1,
		{PatchPublished, "", "true"}:                               1,
	} {
		if got := testutil.ToFloat64(m.patchPlans.WithLabelValues(labels[0], labels[1], labels[2])); got != want {
			t.Fatalf("patch plans %v = %v, want %v", labels, got, want)
		}
	}
	// Re-registration on the same registry reuses the collectors.
	if again := NewMetrics(m.registry, "apid"); again.syncs != m.syncs {
		t.Fatal("re-registration created a second sync counter")
	}
	m.ObserveBuildDeferral(BuildDeferred)
	if got := testutil.ToFloat64(m.deferrals.WithLabelValues(BuildDeferred)); got != 1 {
		t.Fatalf("deferred builds = %v, want 1", got)
	}
	var nilMetrics *Metrics
	nilMetrics.ObserveBuildDeferral(BuildStarted)
	nilMetrics.ObserveSync(api.RecordDevSyncRequest{Status: "live"}, false)
	nilMetrics.ObservePatchPlan(PatchPublished, "", false)
}
