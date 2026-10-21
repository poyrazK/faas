package main_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDevPhaseTrackerRendersMappedTimings(t *testing.T) {
	tracker := main.NewDevPhaseTracker()
	tracker.SetDeploymentID("dep-123")
	tracker.SourceSync(1250_000_000, nil)
	tracker.ObserveStage("dependency_restore", main.StageStatusInProgress, 0, "")
	tracker.ObserveStage("dependency_restore", main.StageStatusCompleted, 250, "")
	tracker.ObserveStage("image_build", main.StageStatusCompleted, 1500, "")
	tracker.ObserveStage("snapshot_prepare", main.StageStatusCompleted, 2100, "")
	tracker.ObserveStage("readiness", main.StageStatusCompleted, 700, "")
	tracker.FinishRouteSwitch()

	var out bytes.Buffer
	tracker.Render(&out)
	got := out.String()
	for _, want := range []string{
		"dev phases (dep-123):",
		"sync=1.3s",
		"cache=250ms",
		"build=1.5s",
		"boot=2.1s",
		"ready=700ms",
		"route=0ms",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered phases = %q, missing %q", got, want)
		}
	}
}

func TestDevPhaseTrackerPreservesFailureReason(t *testing.T) {
	tracker := main.NewDevPhaseTracker()
	tracker.ObserveStage("image_build", main.StageStatusFailed, 42, "missing lockfile")

	var out bytes.Buffer
	tracker.Render(&out)
	if got, want := out.String(), "build=failed (missing lockfile)"; !strings.Contains(got, want) {
		t.Fatalf("rendered failure = %q, want substring %q", got, want)
	}
}

func TestDevPhaseTrackerReconcilesFinalStageState(t *testing.T) {
	tracker := main.NewDevPhaseTracker()
	tracker.ObserveStage("image_build", main.StageStatusInProgress, 0, "")
	tracker.ObserveStageState(json.RawMessage(`{
		"current":"",
		"history":[{"name":"image_build","status":"completed","duration_ms":42000}]
	}`))

	receipt := tracker.Receipt("live")
	if len(receipt.Phases) != 1 {
		t.Fatalf("phases = %#v, want one build phase", receipt.Phases)
	}
	if got := receipt.Phases[0]; got.Phase != main.DevPhaseBuild || got.Status != main.StageStatusCompleted || got.DurationMS != 42000 {
		t.Fatalf("build phase = %#v, want completed 42000ms", got)
	}
}

func TestFormatDevPhaseDurationRoundsToReadableUnits(t *testing.T) {
	tests := map[int64]string{
		0:    "0ms",
		250:  "250ms",
		1250: "1.3s",
	}
	for durationMS, want := range tests {
		if got := main.FormatDevPhaseDuration(durationMS); got != want {
			t.Errorf("formatDevPhaseDuration(%d) = %q, want %q", durationMS, got, want)
		}
	}
}

func TestDevPhaseTrackerReceiptCarriesDevPatchPreview(t *testing.T) {
	tracker := main.NewDevPhaseTracker()
	if receipt := tracker.Receipt("live"); receipt.DevPatch != nil {
		t.Fatalf("receipt without a server preview = %+v, want no dev_patch", receipt.DevPatch)
	}
	preview := &api.DevPatchPreview{Eligible: true, ChangedPaths: 2, PatchBytes: 90}
	tracker.SetDevPatch(preview)
	preview.ChangedPaths = 99 // the tracker keeps its own copy
	body, err := json.Marshal(tracker.Receipt("live"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"dev_patch":{"eligible":true,"changed_paths":2,"patch_bytes":90}`) {
		t.Fatalf("receipt = %s, want the recorded dev_patch preview", body)
	}
}

func TestDevPhaseTrackerReceiptIncludesEditToLiveSLO(t *testing.T) {
	tracker := main.NewDevPhaseTracker()
	(*tracker.Mu()).Lock()
	(*tracker.StartedAt()) = time.Now().Add(-2 * time.Second)
	(*tracker.Mu()).Unlock()
	tracker.SetDeploymentID("dep-slo")
	tracker.SourceSync(100*time.Millisecond, nil)

	receipt := tracker.Receipt("live")
	if receipt.SchemaVersion != 1 || receipt.Type != "developer_sync" {
		t.Fatalf("receipt identity = %#v", receipt)
	}
	if receipt.DeploymentID != "dep-slo" || receipt.Status != "live" {
		t.Fatalf("receipt deployment/status = %q/%q", receipt.DeploymentID, receipt.Status)
	}
	if receipt.SLOTargetMS != main.DevEditToLiveTarget.Milliseconds() {
		t.Fatalf("slo target = %d, want %d", receipt.SLOTargetMS, main.DevEditToLiveTarget.Milliseconds())
	}
	if !receipt.WithinSLO {
		t.Fatal("two-second sync should be within the fifteen-second SLO")
	}
	if len(receipt.Phases) != 1 || receipt.Phases[0].Phase != main.DevPhaseSync {
		t.Fatalf("receipt phases = %#v, want sync phase", receipt.Phases)
	}
}
