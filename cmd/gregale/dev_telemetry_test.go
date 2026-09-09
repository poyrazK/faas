package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestDevPhaseTrackerRendersMappedTimings(t *testing.T) {
	tracker := newDevPhaseTracker()
	tracker.setDeploymentID("dep-123")
	tracker.sourceSync(1250_000_000, nil)
	tracker.observeStage("dependency_restore", stageStatusInProgress, 0, "")
	tracker.observeStage("dependency_restore", stageStatusCompleted, 250, "")
	tracker.observeStage("image_build", stageStatusCompleted, 1500, "")
	tracker.observeStage("snapshot_prepare", stageStatusCompleted, 2100, "")
	tracker.observeStage("readiness", stageStatusCompleted, 700, "")
	tracker.finishRouteSwitch()

	var out bytes.Buffer
	tracker.render(&out)
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
	tracker := newDevPhaseTracker()
	tracker.observeStage("image_build", stageStatusFailed, 42, "missing lockfile")

	var out bytes.Buffer
	tracker.render(&out)
	if got, want := out.String(), "build=failed (missing lockfile)"; !strings.Contains(got, want) {
		t.Fatalf("rendered failure = %q, want substring %q", got, want)
	}
}

func TestFormatDevPhaseDurationRoundsToReadableUnits(t *testing.T) {
	tests := map[int64]string{
		0:    "0ms",
		250:  "250ms",
		1250: "1.3s",
	}
	for durationMS, want := range tests {
		if got := formatDevPhaseDuration(durationMS); got != want {
			t.Errorf("formatDevPhaseDuration(%d) = %q, want %q", durationMS, got, want)
		}
	}
}

func TestDevPhaseTrackerReceiptIncludesEditToLiveSLO(t *testing.T) {
	tracker := newDevPhaseTracker()
	tracker.mu.Lock()
	tracker.startedAt = time.Now().Add(-2 * time.Second)
	tracker.mu.Unlock()
	tracker.setDeploymentID("dep-slo")
	tracker.sourceSync(100*time.Millisecond, nil)

	receipt := tracker.receipt("live")
	if receipt.SchemaVersion != 1 || receipt.Type != "developer_sync" {
		t.Fatalf("receipt identity = %#v", receipt)
	}
	if receipt.DeploymentID != "dep-slo" || receipt.Status != "live" {
		t.Fatalf("receipt deployment/status = %q/%q", receipt.DeploymentID, receipt.Status)
	}
	if receipt.SLOTargetMS != devEditToLiveTarget.Milliseconds() {
		t.Fatalf("slo target = %d, want %d", receipt.SLOTargetMS, devEditToLiveTarget.Milliseconds())
	}
	if !receipt.WithinSLO {
		t.Fatal("two-second sync should be within the fifteen-second SLO")
	}
	if len(receipt.Phases) != 1 || receipt.Phases[0].Phase != devPhaseSync {
		t.Fatalf("receipt phases = %#v, want sync phase", receipt.Phases)
	}
}
