package conformance

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// testRevertedReleaseIsNotARollbackTarget pins ADR-625 amendment 2026-11-10:
// a release an automatic guard reverted is never the implicit target of a
// plain rollback. It served most recently, so a plain `rollback` after the
// revert put the failing release back with its guard already spent.
func testRevertedReleaseIsNotARollbackTarget(t *testing.T, fx *Fixture) {
	t.Helper()
	live := func(digest string) state.Deployment {
		t.Helper()
		dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
			AppID: fx.App.ID, Kind: state.DeploymentKindImage, ImageDigest: digest, Status: state.DeployPending,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.Store.MarkDeploymentLive(fx.Ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		return dep
	}
	good := live("sha256:reverted-target-good")
	failing := live("sha256:reverted-target-failing")
	if err := fx.Store.MarkDeploymentSuperseded(fx.Ctx, good.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.MarkAutoRollback(fx.Ctx, failing.ID, string(state.AutoRollbackReasonThresholdExceeded), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	current := live("sha256:reverted-target-current")
	if err := fx.Store.MarkDeploymentSuperseded(fx.Ctx, failing.ID); err != nil {
		t.Fatal(err)
	}

	target, err := fx.Store.LatestSupersededDeployment(fx.Ctx, fx.App.ID, current.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if target.ID == failing.ID {
		t.Fatalf("plain rollback target is the reverted release %s", failing.ID)
	}
	if target.ID != good.ID {
		t.Fatalf("plain rollback target = %s, want the last good release %s", target.ID, good.ID)
	}
}
