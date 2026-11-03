// adr: 20261103131742 — a plan that ended or shrank does not keep the higher
// plan's deployed apps or RAM: the apps outside it park and do not wake.
package sched_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// A Pro account's subscription ends with three apps, one of them at 1 GB.
// On Free only the oldest app wakes; the second parks and is refused as past
// the deployed-app limit, the 1 GB app as above the plan's RAM. Upgrading
// again wakes them at once.
func TestAppsOutsideThePlanParkAndDoNotWake(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "plan-fit-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().Add(-time.Hour)
	newApp := func(slug string, ram int) state.App {
		t.Helper()
		created = created.Add(time.Minute)
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, RAMMB: ram, MaxConcurrency: 1, IdleTimeoutS: 60, CreatedAt: created})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive}); err != nil {
			t.Fatal(err)
		}
		return app
	}
	first, second, big := newApp("first", 128), newApp("second", 128), newApp("big", 1024)
	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	if woke, err := e.Wake(ctx, second.ID, "", "", sched.TriggerGateway); err != nil || woke.InstanceID == "" {
		t.Fatalf("Wake on Pro = (%+v, %v), want a running instance", woke, err)
	}

	if err := store.UpdateAccountPlan(ctx, acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	if acted, err := e.ParkApp(ctx, second.ID); err != nil || acted != 1 {
		t.Fatalf("ParkApp past the Free app limit = (%d, %v), want its instance parked", acted, err)
	}
	for _, tc := range []struct {
		app  state.App
		code string
	}{{second, api.CodePlanLimitApps}, {big, api.CodePlanLimitRAM}} {
		_, err := e.Wake(ctx, tc.app.ID, "", "", sched.TriggerGateway)
		var problem *api.Problem
		if !errors.Is(err, sched.ErrPermanentWake) || !errors.As(err, &problem) || problem.Code != tc.code {
			t.Fatalf("Wake %s on Free = %v, want a permanent %s refusal", tc.app.Slug, err, tc.code)
		}
	}
	if woke, err := e.Wake(ctx, first.ID, "", "", sched.TriggerGateway); err != nil || woke.InstanceID == "" {
		t.Fatalf("Wake of the oldest app on Free = (%+v, %v), want a running instance", woke, err)
	}
	if acted, err := e.ParkApp(ctx, first.ID); err != nil || acted != 0 {
		t.Fatalf("ParkApp of the oldest app = (%d, %v), want it left running", acted, err)
	}

	if err := store.UpdateAccountPlan(ctx, acct.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if woke, err := e.Wake(ctx, big.ID, "", "", sched.TriggerGateway); err != nil || woke.InstanceID == "" {
		t.Fatalf("Wake after upgrading again = (%+v, %v), want a running instance", woke, err)
	}
}
