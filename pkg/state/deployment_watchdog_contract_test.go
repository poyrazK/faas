// adr: 117. The deployment watchdog lists and fails only stuck deployments.
package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemDeploymentWatchdog(t *testing.T) {
	testDeploymentWatchdog(t, state.NewMemStore())
}

// Every deployment must reach a terminal state (ADR-20261024140000). A build
// queued past its bound, or an in-flight deployment with no build in
// progress and no stage progress, is listed; a running build, or anything
// within its bounds, is not. Failing re-checks under the row lock, so a
// deployment that moved on in between is left alone.
func testDeploymentWatchdog(t *testing.T, store state.ContractTestStore) {
	t.Helper()
	ctx := t.Context()
	s, ok := any(store).(state.DeploymentWatchdogStore)
	if !ok {
		t.Fatalf("%T has no deployment watchdog", store)
	}
	acct, err := store.CreateAccount(ctx, "watchdog-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "watchdog-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	deploy := func(scope string) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Scope: scope})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	queued := deploy("default")
	if _, err := store.CreateBuild(ctx, queued.ID, state.DeploymentKindTarball, 10, ""); err != nil {
		t.Fatal(err)
	}
	stalled := deploy("staging")
	if err := store.UpdateDeploymentStatus(ctx, stalled.ID, state.DeployImaging, ""); err != nil {
		t.Fatal(err)
	}
	running := deploy("testing")
	build, err := store.CreateBuild(ctx, running.ID, state.DeploymentKindTarball, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimQueuedBuild(ctx, build.ID); err != nil {
		t.Fatal(err)
	}

	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	if got, err := s.ListStuckDeployments(ctx, past, past, 10); err != nil || len(got) != 0 {
		t.Fatalf("within bounds listed %+v (err=%v)", got, err)
	}
	got, err := s.ListStuckDeployments(ctx, future, future, 10)
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]state.StuckDeploymentReason{}
	for _, d := range got {
		reasons[d.ID] = d.Reason
	}
	if len(reasons) != 2 || reasons[queued.ID] != state.StuckDeploymentQueued || reasons[stalled.ID] != state.StuckDeploymentStalled {
		t.Fatalf("stuck = %+v, want the queued build and the stalled imaging deployment only", got)
	}

	var stalledRow state.StuckDeployment
	for _, d := range got {
		if d.ID == stalled.ID {
			stalledRow = d
		}
	}
	// The deployment finishes on its own between the listing and the failure.
	if _, err := store.SetDeploymentFailed(ctx, stalled.ID, "build_failed", "its own failure"); err != nil {
		t.Fatal(err)
	}
	if moved, ok, err := s.FailStuckDeployment(ctx, stalledRow, future, future, api.CodeDeploymentStalled, "stalled"); err != nil || ok {
		t.Fatalf("watchdog overwrote a deployment that had moved on: %+v ok=%v err=%v", moved, ok, err)
	}
	if kept, err := store.DeploymentByID(ctx, stalled.ID); err != nil || kept.ErrorCode != "build_failed" {
		t.Fatalf("moved-on deployment = code %q (err=%v), want its own failure kept", kept.ErrorCode, err)
	}

	var queuedRow state.StuckDeployment
	for _, d := range got {
		if d.ID == queued.ID {
			queuedRow = d
		}
	}
	failed, ok, err := s.FailStuckDeployment(ctx, queuedRow, future, past, api.CodeDeploymentQueueTimeout, "queued too long")
	if err != nil || !ok || failed.Status != state.DeployFailed || failed.ErrorCode != api.CodeDeploymentQueueTimeout {
		t.Fatalf("fail queued = %+v ok=%v err=%v", failed, ok, err)
	}
	queuedBuild, err := store.BuildByDeployment(ctx, queued.ID)
	if err != nil || queuedBuild.Status != state.BuildCancelled {
		t.Fatalf("queued build = %+v (err=%v), want cancelled", queuedBuild, err)
	}
	if again, err := s.ListStuckDeployments(ctx, future, future, 10); err != nil || len(again) != 0 {
		t.Fatalf("after failing, stuck = %+v (err=%v); the running build stays with builderd", again, err)
	}
}
