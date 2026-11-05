package databaseswitch_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestDatabaseSwitchAmbiguousPromotionRestoresSource(t *testing.T) {
	h := newHarness(t)
	h.databases.target.State = "ready"
	h.runUntil(state.DatabaseSwitchDraining, 10)
	h.completeDrain()
	h.runUntil(state.DatabaseSwitchSwitching, 3)
	lostReply := true
	h.worker.PromoteRelease = func(ctx context.Context, sw state.DatabaseSwitch, id string) error {
		if err := h.releases.MarkDeploymentLive(ctx, id); err != nil {
			return err
		}
		if id == sw.TargetDeploymentID && lostReply {
			lostReply = false
			return errors.New("promotion committed but connection closed before commit acknowledgement")
		}
		return nil
	}
	h.runUntil(state.DatabaseSwitchFailed, 5)
	if live := h.releases.live["default"]; live != h.sw.FromDeploymentID {
		t.Fatalf("switch failed and cutover=%s fence=%v, but target code still serves: live=%s source=%s target=%s checkpoint=%v", h.cutovers.cutover.State, h.cutovers.fenced, live, h.sw.FromDeploymentID, h.sw.TargetDeploymentID, h.sw.ReleaseSwitchedAt)
	}
}

func TestDatabaseSwitchUnfencedAbortKeepsAnIndependentRelease(t *testing.T) {
	h := newHarness(t)
	h.databases.target.State = "ready"
	h.runUntil(state.DatabaseSwitchStaging, 3)
	if err := h.releases.MarkDeploymentLive(t.Context(), h.sw.TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.RequestDatabaseSwitchCancel(t.Context(), h.sw.AccountID, h.sw.AppID, h.sw.ID, h.clock); err != nil {
		t.Fatal(err)
	}
	h.runUntil(state.DatabaseSwitchCancelled, 4)
	if h.releases.live["default"] != h.sw.TargetDeploymentID {
		t.Fatal("abort before fencing replaced an independent release")
	}
}
