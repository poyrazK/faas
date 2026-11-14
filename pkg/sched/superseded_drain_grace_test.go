package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type supersededActivity map[string]sched.InstanceActivity

func (a supersededActivity) SnapshotActivity(time.Time) map[string]sched.InstanceActivity { return a }

func seedSupersededDrain(ctx context.Context, t *testing.T, plan api.Plan, maxConcurrency, instances int) (*state.MemStore, *sched.Engine, state.App, state.Deployment, []string) {
	t.Helper()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "drain-"+string(plan)+"@example.com", plan)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "drain-" + string(plan), RAMMB: 128, MaxConcurrency: maxConcurrency, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:old", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	e := sched.XNewEngine(t, mem, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	var ids []string
	for range instances {
		res, err := e.AdmitInstance(ctx, app.ID, "", "", sched.TriggerGateway)
		if err != nil {
			t.Fatalf("AdmitInstance: %v", err)
		}
		ids = append(ids, res.InstanceID)
	}
	return mem, e, app, dep, ids
}

func supersededInstanceState(ctx context.Context, t *testing.T, mem *state.MemStore, id string) string {
	t.Helper()
	ins, err := mem.InstanceByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return ins.State
}

// Deploy-path review P16: a new release going live parked the old release's
// instances at once, cutting the requests they were serving.
func TestSupersededDrainWaitsForRequestsInFlight(t *testing.T) {
	defer sched.SetSupersededDrainPoll(time.Hour)()
	ctx := context.Background()
	mem, e, _, dep, ids := seedSupersededDrain(ctx, t, api.PlanPro, 5, 2)
	busy, idle := ids[0], ids[1]
	activity := supersededActivity{busy: {Inflight: 1}}
	loop := sched.NewLoop(nil, e, nil).WithInstanceActivity(activity)

	loop.DrainSupersededDeployment(ctx, dep.ID, time.Now().Add(time.Minute))
	if got := supersededInstanceState(ctx, t, mem, idle); got != string(state.StateParked) {
		t.Fatalf("idle superseded instance = %s, want parked at once", got)
	}
	if got := supersededInstanceState(ctx, t, mem, busy); got != string(state.StateRunning) {
		t.Fatalf("busy superseded instance = %s, want running until its request finishes", got)
	}

	activity[busy] = sched.InstanceActivity{}
	loop.DrainSupersededDeployment(ctx, dep.ID, time.Now().Add(time.Minute))
	if got := supersededInstanceState(ctx, t, mem, busy); got != string(state.StateParked) {
		t.Fatalf("finished superseded instance = %s, want parked", got)
	}
}

func TestSupersededDrainDoesNotWaitPastTheGraceOrTheCap(t *testing.T) {
	defer sched.SetSupersededDrainPoll(time.Hour)()
	ctx := context.Background()
	t.Run("grace over", func(t *testing.T) {
		mem, e, _, dep, ids := seedSupersededDrain(ctx, t, api.PlanPro, 5, 1)
		loop := sched.NewLoop(nil, e, nil).WithInstanceActivity(supersededActivity{ids[0]: {Inflight: 1}})
		loop.DrainSupersededDeployment(ctx, dep.ID, time.Now().Add(-time.Second))
		if got := supersededInstanceState(ctx, t, mem, ids[0]); got == string(state.StateRunning) {
			t.Fatalf("superseded instance past its grace = %s, want drained", got)
		}
	})
	t.Run("app at its concurrency cap", func(t *testing.T) {
		// A one-instance app waiting on its old instance would refuse the
		// new release's first wake.
		mem, e, _, dep, ids := seedSupersededDrain(ctx, t, api.PlanFree, 1, 1)
		loop := sched.NewLoop(nil, e, nil).WithInstanceActivity(supersededActivity{ids[0]: {Inflight: 1}})
		loop.DrainSupersededDeployment(ctx, dep.ID, time.Now().Add(time.Minute))
		if got := supersededInstanceState(ctx, t, mem, ids[0]); got == string(state.StateRunning) {
			t.Fatalf("superseded instance of an app at its cap = %s, want drained at once", got)
		}
	})
}
