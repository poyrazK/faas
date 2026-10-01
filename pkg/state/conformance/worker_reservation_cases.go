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
