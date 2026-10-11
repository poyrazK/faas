// spend_cap_test.go pins ADR-968: the account spend cap stops always-on
// capacity, not only request wakes.
package sched

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func switchableSpendCap(reached *atomic.Bool) OverageChecker {
	return newMockChecker(func(context.Context, string) (OverageStatus, int64, int64, error) {
		if reached.Load() {
			return OverageReached, 1_500, 1_000, nil
		}
		return OverageOK, 0, 0, nil
	})
}

func TestAdmitInstanceForDeployment_SpendCapRefusesFloor(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	vmm := &fakeVMM{}
	var reached atomic.Bool
	reached.Store(true)
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOverageChecker(switchableSpendCap(&reached))

	res, err := e.AdmitInstanceForDeployment(context.Background(), app.ID, dep.ID, "", TriggerFloorDep)
	if err != nil {
		t.Fatalf("AdmitInstanceForDeployment: %v", err)
	}
	if !res.AtCapacity || res.InstanceID != "" {
		t.Fatalf("floor admission at the spend cap = %+v, want a typed AtCapacity refusal", res)
	}
	vmm.mu.Lock()
	boots := vmm.coldBoots + vmm.restores
	vmm.mu.Unlock()
	if boots != 0 || e.Ledger().Concurrency(app.ID) != 0 {
		t.Fatalf("boots=%d concurrency=%d, want no capacity started past the cap", boots, e.Ledger().Concurrency(app.ID))
	}

	reached.Store(false)
	res, err = e.AdmitInstanceForDeployment(context.Background(), app.ID, dep.ID, "", TriggerFloorDep)
	if err != nil || res.InstanceID == "" {
		t.Fatalf("floor admission after the cap lifts = (%+v, %v), want a started instance", res, err)
	}
}

func TestAdmitInstanceForDeployment_SpendCapExemptsDeploymentSmoke(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 128, 5)
	candidate, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:candidate", Status: state.DeploySnapshotting,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(candidate): %v", err)
	}
	var reached atomic.Bool
	reached.Store(true)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOverageChecker(switchableSpendCap(&reached))

	res, err := e.AdmitInstanceForDeployment(context.Background(), app.ID, candidate.ID, "", TriggerDeploymentSmoke)
	if err != nil || res.AtCapacity || res.InstanceID == "" {
		t.Fatalf("deployment smoke at the spend cap = (%+v, %v), want the release verified", res, err)
	}
}

func TestAppMustPark_SpendCap(t *testing.T) {
	store := state.NewMemStore()
	acct, app, _ := seedApp(t, store, api.PlanPro, 128, 5)
	ctx := context.Background()
	if newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").appMustPark(ctx, app, acct) {
		t.Fatal("appMustPark without an overage checker = true, want false")
	}
	var reached atomic.Bool
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOverageChecker(switchableSpendCap(&reached))
	if e.appMustPark(ctx, app, acct) {
		t.Fatal("appMustPark under the cap = true, want false")
	}
	reached.Store(true)
	if !e.appMustPark(ctx, app, acct) {
		t.Fatal("appMustPark at the cap = false, want true")
	}
}

func TestParkApp_ParksRunningInstanceAtSpendCap(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 128, 5)
	ctx := context.Background()
	var reached atomic.Bool
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOverageChecker(switchableSpendCap(&reached))
	woke, err := e.Wake(ctx, app.ID, "", "", TriggerGateway)
	if err != nil || woke.InstanceID == "" {
		t.Fatalf("Wake = (%+v, %v), want a running instance", woke, err)
	}
	if acted, err := e.ParkApp(ctx, app.ID); err != nil || acted != 0 {
		t.Fatalf("ParkApp under the cap = (%d, %v), want a no-op", acted, err)
	}

	reached.Store(true)
	acted, err := e.ParkApp(ctx, app.ID)
	if err != nil || acted != 1 {
		t.Fatalf("ParkApp at the cap = (%d, %v), want the running instance parked", acted, err)
	}
	ins, err := store.InstanceByID(ctx, woke.InstanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if state.State(ins.State).CountsForRAM() {
		t.Fatalf("instance state after ParkApp = %q, want a non-resident state", ins.State)
	}
}
