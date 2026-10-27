package sched_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Container qualification 2026-10-27: after `POST /v1/apps/{slug}/park` the
// next request woke the app through AdmitInstance, which left it
// evicted_cold. The reaper's parked-app sweep then destroyed the waking VM
// and the request answered 503. Request admission must un-park the app first.
func TestTrafficAdmissionUnparksAnExplicitlyParkedApp(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanPro, 256, 4)
	parked := state.AppEvictedCold
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatal(err)
	}
	vmm := &sched.FakeVMM{}
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	sweeps, swept := 0, 0
	*vmm.ColdBootHook() = func() {
		// The reaper sweeps parked apps while the wake's vmmd call runs.
		acted, err := engine.ParkApp(ctx, app.ID)
		if err != nil {
			t.Errorf("parked-app sweep: %v", err)
		}
		sweeps, swept = sweeps+1, swept+acted
	}

	res, err := engine.AdmitTrafficInstance(ctx, app.ID, "", "", sched.TriggerGateway)
	if err != nil || res.AtCapacity || res.InstanceID == "" {
		t.Fatalf("admission = %+v, %v; want a woken instance", res, err)
	}
	if sweeps != 1 || swept != 0 || *vmm.Destroys() != 0 {
		t.Fatalf("sweeps=%d acted=%d destroys=%d; the sweep must leave the waking VM alone", sweeps, swept, *vmm.Destroys())
	}
	after, err := store.AppByID(ctx, app.ID)
	if err != nil || after.Status != state.AppActive {
		t.Fatalf("app status = %q, %v; want active", after.Status, err)
	}
	instance, err := store.InstanceByID(ctx, res.InstanceID)
	if err != nil || instance.State != string(state.StateRunning) {
		t.Fatalf("instance = %+v, %v; want running", instance, err)
	}
}

// Admission for an app that is already active keeps the plain AdmitInstance
// path, including its scale-out of a second instance.
func TestTrafficAdmissionOfAnActiveAppAddsCapacity(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanPro, 256, 4)
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	first, err := engine.AdmitTrafficInstance(ctx, app.ID, "", "", sched.TriggerGateway)
	if err != nil || first.InstanceID == "" {
		t.Fatalf("first admission = %+v, %v", first, err)
	}
	second, err := engine.AdmitTrafficInstance(ctx, app.ID, "", "", sched.TriggerGateway)
	if err != nil || second.InstanceID == "" || second.InstanceID == first.InstanceID {
		t.Fatalf("second admission = %+v, %v; want a new instance beside %s", second, err, first.InstanceID)
	}
}
