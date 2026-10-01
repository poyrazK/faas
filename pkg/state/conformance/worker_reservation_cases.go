package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testWorkerAccountCapacity(t *testing.T, fx *Fixture) {
	app, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID,
		Slug: "worker-neighbor", RAMMB: 128, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: app.ID,
		Scope: "staging", Kind: state.DeploymentKindImage, ImageDigest: "sha256:worker-neighbor"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(app state.App, dep state.Deployment, mode state.InstanceMode, status state.State) (state.Instance, error) {
		return fx.Store.CreateInstanceWithMode(fx.Ctx, app.ID, dep.ID, string(status),
			128, fx.Node.ID, uuid.NewString(), string(mode))
	}
	if _, err := create(fx.App, fx.Deployment, state.InstanceModeNormal, state.StateRunning); err != nil {
		t.Fatal(err)
	}
	var workers []state.Instance
	for i := range api.MustLimitsFor(fx.Account.Plan).WorkerReplicasMax {
		selectedApp, selectedDep := fx.App, fx.Deployment
		if i%2 == 1 {
			selectedApp, selectedDep = app, dep
		}
		ins, err := create(selectedApp, selectedDep, state.InstanceModeWorker, state.StateColdBooting)
		if err != nil {
			t.Fatal(err)
		}
		workers = append(workers, ins)
	}
	refuse := func() {
		t.Helper()
		if _, err := create(app, dep, state.InstanceModeWorker, state.StateWaking); !errors.Is(err, state.ErrAccountWorkerCapacity) {
			t.Fatalf("account worker over-admission = %v", err)
		}
	}
	refuse()
	// Migration and draining keep the same resident's account slot. Another
	// application must not steal it while ownership or teardown is pending.
	for _, status := range []state.State{state.StateMigrating, state.StateDraining} {
		if err := fx.Store.UpdateInstanceState(fx.Ctx, workers[0].ID, string(status)); err != nil {
			t.Fatal(err)
		}
		refuse()
	}
	if err := fx.Store.UpdateInstanceStateToTerminal(fx.Ctx, workers[0].ID, string(state.StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := create(app, dep, state.InstanceModeWorker, state.StateRunning); err != nil {
		t.Fatalf("terminated worker did not release account capacity: %v", err)
	}
	refuse()
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby} {
		account, err := fx.Store.CreateAccount(fx.Ctx, string(plan)+"-worker@example.test", plan)
		if err != nil {
			t.Fatal(err)
		}
		other, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: account.ID, Slug: string(plan) + "-other-worker", RAMMB: 128})
		if err != nil {
			t.Fatal(err)
		}
		otherDep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: other.ID,
			Kind: state.DeploymentKindImage, ImageDigest: "sha256:other-worker"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = create(other, otherDep, state.InstanceModeWorker, state.StateRunning)
		if plan == api.PlanFree && !errors.Is(err, state.ErrAccountWorkerCapacity) || plan != api.PlanFree && err != nil {
			t.Fatalf("independent %s worker admission = %v", plan, err)
		}
	}
}

func testWorkerAdmissionIdentity(t *testing.T, fx *Fixture) {
	create := func(mode state.InstanceMode, status state.State) state.Instance {
		t.Helper()
		row, err := fx.Store.CreateInstanceWithMode(fx.Ctx, fx.App.ID, fx.Deployment.ID,
			string(status), 128, fx.Node.ID, uuid.NewString(), string(mode))
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	ordinary := create(state.InstanceModeNormal, state.StateRunning)
	worker := create(state.InstanceModeWorker, state.StateColdBooting)
	parked := create(state.InstanceModeWorker, state.StateParked)
	if err := fx.Store.SetInstanceMode(fx.Ctx, ordinary.ID, state.InstanceModeWorker); err == nil {
		t.Fatal("ordinary instance bypassed worker admission by changing mode")
	}
	if err := fx.Store.SetInstanceMode(fx.Ctx, worker.ID, state.InstanceModeNormal); err == nil {
		t.Fatal("resident worker released account ownership by changing mode")
	}
	if err := fx.Store.SetInstanceMode(fx.Ctx, worker.ID, state.InstanceModeWorker); err != nil {
		t.Fatalf("idempotent worker mode changed identity: %v", err)
	}
	if err := fx.Store.SetInstanceMode(fx.Ctx, ordinary.ID, state.InstanceModeMirror); err != nil {
		t.Fatalf("ordinary mirror classification failed: %v", err)
	}
	inputs := state.RuntimeConfigInputs{Scope: "default", Boundary: time.Now().UTC()}
	for name, mutate := range map[string]func() error{
		"state": func() error { return fx.Store.UpdateInstanceState(fx.Ctx, parked.ID, string(state.StateRunning)) },
		"conditional": func() error {
			return fx.Store.UpdateInstanceStateIf(fx.Ctx, parked.ID, string(state.StateParked), string(state.StateWaking))
		},
		"timestamp": func() error {
			return fx.Store.UpdateInstanceStateWithTimestamp(fx.Ctx, parked.ID, string(state.StateSnapshotting), time.Now())
		},
		"terminal helper": func() error {
			return fx.Store.UpdateInstanceStateToTerminal(fx.Ctx, parked.ID, string(state.StateRunning), time.Now())
		},
		"runtime publication": func() error {
			_, err := fx.Store.PublishInstanceRuntime(fx.Ctx, parked.ID, string(state.StateParked), "new-netns", "10.0.0.1", 20000)
			return err
		},
		"receipt publication": func() error {
			_, err := fx.Store.(state.RuntimeConfigReceiptPublisher).PublishInstanceRuntimeWithConfig(fx.Ctx, parked.ID,
				string(state.StateParked), "new-netns", "10.0.0.1", 20000, parked.WakeID, inputs)
			return err
		},
	} {
		if err := mutate(); err == nil {
			t.Fatalf("%s resurrected an unreserved worker", name)
		}
		after, err := fx.Store.InstanceByID(fx.Ctx, parked.ID)
		if err != nil || after.State != string(state.StateParked) || after.Mode != string(state.InstanceModeWorker) || after.Netns != "" {
			t.Fatalf("%s rejection changed worker: state=%s mode=%s netns=%s err=%v", name, after.State, after.Mode, after.Netns, err)
		}
	}
	if _, exists, err := fx.Store.(state.RuntimeConfigReceiptStore).InstanceRuntimeConfigReceipt(fx.Ctx, parked.ID); err != nil || exists {
		t.Fatalf("rejected resurrection published an input receipt: exists=%v err=%v", exists, err)
	}
	if _, err := fx.Store.PublishInstanceRuntime(fx.Ctx, worker.ID, string(state.StateColdBooting), "worker-netns", "10.0.0.2", 20001); err != nil {
		t.Fatalf("reserved worker could not become ready: %v", err)
	}
	if err := fx.Store.UpdateInstanceStateToTerminal(fx.Ctx, worker.ID, string(state.StateStopped), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.UpdateInstanceState(fx.Ctx, worker.ID, string(state.StateColdBooting)); err == nil {
		t.Fatal("terminal worker reused a released reservation")
	}
	if _, err := fx.Store.CreateInstanceWithMode(fx.Ctx, fx.App.ID, fx.Deployment.ID, "RUNNING",
		128, fx.Node.ID, uuid.NewString(), string(state.InstanceModeWorker)); err == nil {
		t.Fatal("noncanonical worker state bypassed reservation accounting")
	}
}
