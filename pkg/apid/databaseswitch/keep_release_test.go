// adr: 20261023181931 — amendment 3: a restore switch moves the app to the copy and never changes its release.
package databaseswitch_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestKeepReleaseSwitchMovesTheBindingsWithoutTouchingTheRelease(t *testing.T) {
	h := newHarnessFor(t, true)
	live := h.sw.FromDeploymentID
	if !h.sw.KeepsRelease() || h.sw.TargetDeploymentID != live {
		t.Fatalf("switch does not keep the release: %+v", h.sw)
	}
	h.tick()
	h.databases.target.State = managedpostgres.StateReady
	h.runUntil(state.DatabaseSwitchDraining, 12)
	h.completeDrain()
	sw := h.runUntil(state.DatabaseSwitchCompleted, 6)
	if sw.ActivatedAt == nil || sw.ReleaseSwitchedAt == nil || !h.cutovers.resumed || h.snapshots == 0 {
		t.Fatalf("switch did not activate and resume: %+v", sw)
	}
	if h.releases.live["default"] != live || len(h.releases.marked) != 0 {
		t.Fatalf("release changed: live %s marked %v", h.releases.live["default"], h.releases.marked)
	}
	if !h.databases.withinLimit {
		t.Fatal("the switch's restore may take the restore slot")
	}
}

func TestKeepReleaseSwitchAbortLeavesTheReleaseAlone(t *testing.T) {
	h := newHarnessFor(t, true)
	h.databases.target.State = managedpostgres.StateReady
	h.runUntil(state.DatabaseSwitchDraining, 12)
	h.completeDrain()
	h.cutovers.activateErr = &managedpostgres.ActivationBlockedError{Code: managedpostgres.ActivationBindingsChanged}
	sw := h.runUntil(state.DatabaseSwitchFailed, 8)
	if sw.FailureCode != "activation_failed" || sw.ActivatedAt != nil {
		t.Fatalf("got %+v", sw)
	}
	if h.releases.live["default"] != sw.FromDeploymentID || len(h.releases.marked) != 0 {
		t.Fatalf("abort touched the release: live %s marked %v", h.releases.live["default"], h.releases.marked)
	}
	if !slices.Contains(h.cutovers.calls, "cancel") || h.cutovers.fenced {
		t.Fatalf("cutover not cancelled: %v", h.cutovers.calls)
	}
}

func TestKeepReleaseSwitchFailsWhenAnotherReleaseStartedServing(t *testing.T) {
	h := newHarnessFor(t, true)
	h.databases.target.State = managedpostgres.StateReady
	h.runUntil(state.DatabaseSwitchDraining, 12)
	h.completeDrain()
	other := state.Deployment{ID: uuid.NewString(), AppID: h.sw.AppID, Scope: "default", Status: state.DeployLive, TrafficPercent: 100, Revision: 43}
	h.releases.deployments[other.ID] = other
	h.releases.live["default"] = other.ID
	sw := h.runUntil(state.DatabaseSwitchFailed, 6)
	if sw.FailureCode != "release_changed" || len(h.releases.marked) != 0 || slices.Contains(h.cutovers.calls, "activate") {
		t.Fatalf("got %+v marked %v calls %v", sw, h.releases.marked, h.cutovers.calls)
	}
}
