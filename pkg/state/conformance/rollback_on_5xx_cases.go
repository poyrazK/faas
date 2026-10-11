package conformance

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// testRollbackOn5xxCandidates pins the ADR-625 worker's input. production-us
// hunt #4 found `deploy --rollback-on-5xx` stored the opt-in and nothing ever
// read it: a release answering 100% 500s stayed live. A live, complete release
// that opted in is a candidate until it auto-rolls back; one that did not opt
// in never is.
func testRollbackOn5xxCandidates(t *testing.T, fx *Fixture) {
	t.Helper()
	listed := func() map[string]state.RollbackOn5xxCandidate {
		t.Helper()
		rows, err := fx.Store.(state.RollbackOn5xxStore).ListRollbackOn5xxCandidates(fx.Ctx, 2*time.Minute, 100)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]state.RollbackOn5xxCandidate{}
		for _, row := range rows {
			if row.AppID == fx.App.ID {
				out[row.DeploymentID] = row
			}
		}
		return out
	}
	live := func(optIn bool, digest string) state.Deployment {
		t.Helper()
		dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
			AppID: fx.App.ID, Kind: state.DeploymentKindImage, ImageDigest: digest,
			Status: state.DeployPending, RollbackOn5xx: optIn,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := fx.Store.MarkDeploymentLive(fx.Ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		return dep
	}

	guarded := live(true, "sha256:rollback-on-5xx-guarded")
	got := listed()
	if len(got) != 1 || got[guarded.ID].DeploymentID != guarded.ID || got[guarded.ID].WindowEndsAt != nil {
		t.Fatalf("candidates = %+v, want only %s with no window opened yet", got, guarded.ID)
	}

	before := time.Now().Add(-time.Second)
	if _, err := fx.Store.StampFirstWake(fx.Ctx, guarded.ID, 5); err != nil {
		t.Fatal(err)
	}
	got = listed()
	window := got[guarded.ID].WindowEndsAt
	if len(got) != 1 || window == nil || window.Before(before.Add(5*time.Minute)) || got[guarded.ID].FirstWakeAt == nil {
		t.Fatalf("after StampFirstWake candidates = %+v, want %s with a 5 minute window", got, guarded.ID)
	}

	if _, err := fx.Store.MarkAutoRollback(fx.Ctx, guarded.ID, string(state.AutoRollbackReasonThresholdExceeded), time.Now()); err != nil {
		t.Fatal(err)
	}
	if got = listed(); len(got) != 0 {
		t.Fatalf("after MarkAutoRollback candidates = %+v, want none", got)
	}

	live(false, "sha256:rollback-on-5xx-unguarded")
	if got = listed(); len(got) != 0 {
		t.Fatalf("a release without rollback_on_5xx listed: %+v", got)
	}
}
