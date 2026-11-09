// adr: 20261109172421

package sched_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// An app on cpu_size_apps gets its CPU from its instance size, whatever its
// plan: one vCPU per GB, a weight of one per 16 MB, no quota at the default
// ceiling, and a booking of its RAM's baseline. It takes precedence over
// guest_vcpu_from_cpu_apps; every other app keeps its plan.
func TestCPUSizeAppsFollowInstanceSize(t *testing.T) {
	engine, err := sched.NewEngine(context.Background(), state.NewMemStore(), sched.NewNodeLedger(), nil, nil, "", nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	const id = "0b6f8c1e-2f6a-4c55-9d38-3c1f0c5e7a11"
	other := state.App{ID: "7e0d4a52-91b3-4f7e-8a3c-5d2e9b1f6c44", RAMMB: 2048}
	scale := api.MustLimitsFor(api.PlanScale)
	if err := engine.SetGuestVCPUFromCPUApps([]string{"*"}); err != nil {
		t.Fatal(err)
	}
	if err := engine.SetCPUSizeApps([]string{" " + id + " ", ""}); err != nil {
		t.Fatalf("SetCPUSizeApps: %v", err)
	}
	for _, tc := range []struct {
		plan                api.Plan
		ramMB, chosen       int
		vcpus, cpu, booking int
		weight              int32
	}{
		{api.PlanFree, 128, 0, 1, 1000, 125, 8},
		{api.PlanHobby, 512, 0, 1, 1000, 500, 32},
		{api.PlanScale, 512, 0, 1, 1000, 500, 32}, // the same size gets the same CPU on every plan
		{api.PlanPro, 1024, 0, 1, 1000, 1000, 64},
		{api.PlanScale, 2048, 0, 2, 1000, 2000, 128},
		{api.PlanScale, 2048, 250, 2, 250, 250, 128}, // a chosen cap stays and bounds the booking
	} {
		app := state.App{ID: id, RAMMB: tc.ramMB, CPUMillicores: tc.chosen}
		limits := api.MustLimitsFor(tc.plan)
		if got := engine.GuestVCPUs(app, limits); got != tc.vcpus {
			t.Errorf("%s %d MB: vCPUs = %d, want %d", tc.plan, tc.ramMB, got, tc.vcpus)
		}
		if got := engine.AppCPUMillicores(app, limits); got != tc.cpu {
			t.Errorf("%s %d MB: CPU = %d, want %d", tc.plan, tc.ramMB, got, tc.cpu)
		}
		if got, stamp := engine.AppCPUBookingMillicores(app, limits), engine.AppCPUBookingStamp(app, limits); got != tc.booking || stamp != tc.booking {
			t.Errorf("%s %d MB: books %d m, stamps %d; want %d", tc.plan, tc.ramMB, got, stamp, tc.booking)
		}
		if got := engine.AppCPUWeight(app); got != tc.weight || !engine.CPUBySize(app.ID) {
			t.Errorf("%s %d MB: weight = %d, by size %v; want %d and true", tc.plan, tc.ramMB, got, engine.CPUBySize(app.ID), tc.weight)
		}
		if got, err := engine.PolicyAppCPUMillicores(context.Background(), app.ID, 500); err != nil || got != 500 {
			t.Errorf("%s: a live policy of 500 m pushes %d (err %v), want it unchanged", tc.plan, got, err)
		}
	}
	// Not listed: guest_vcpu_from_cpu_apps and the plan as before.
	if got := engine.GuestVCPUs(other, scale); got != 2 {
		t.Errorf("an app off cpu_size_apps boots %d vCPUs, want guest_vcpu_from_cpu_apps' 2", got)
	}
	if engine.AppCPUWeight(other) != 0 || engine.CPUBySize(other.ID) {
		t.Error("an app off cpu_size_apps carries a size weight or no quota")
	}
	if err := engine.SetCPUSizeApps([]string{"not-an-app"}); err == nil {
		t.Fatal("SetCPUSizeApps accepted an entry that is neither an app ID nor *")
	}
	if err := engine.SetCPUSizeApps([]string{"*"}); err != nil || !engine.CPUBySize(other.ID) {
		t.Fatalf("* did not list every app (err %v)", err)
	}
	if err := engine.SetCPUSizeApps(nil); err != nil || engine.CPUBySize(other.ID) {
		t.Fatalf("an empty list kept apps on size (err %v)", err)
	}
}

// The weight and the uncapped flag reach vmmd's wake request.
func TestCPUSizeFieldsReachVMMD(t *testing.T) {
	var got fcvm.WakeRequest
	client := newClient(t, &fakeVMM{wakeFn: func(_ context.Context, req fcvm.WakeRequest) (*fcvm.Instance, error) {
		got = req
		return &fcvm.Instance{}, nil
	}})
	app := sched.AppSpec{BaseKey: "base.ext4", LayerKey: "app.ext4", VCPUCount: 1, MemSizeMiB: 512, CPUMillicores: 1000,
		CPUWeight: 32, CPUUncapped: true, AppID: "app-size", Plan: api.PlanHobby}
	if _, err := client.CreateColdBoot(t.Context(), "size-instance", app); err != nil {
		t.Fatalf("CreateColdBoot: %v", err)
	}
	if got.CPUWeight != 32 || !got.CPUUncapped {
		t.Fatalf("wake request weight %d, uncapped %v; want 32 and true", got.CPUWeight, got.CPUUncapped)
	}
}

// setSizeApps sets cpu_size_apps as schedd does: a revision, then the engine
// applies it.
func (f *refreshFixture) setSizeApps(t *testing.T, list string) {
	t.Helper()
	value, _ := json.Marshal(list)
	if _, err := f.store.UpsertRuntimeConfig(f.ctx, state.RuntimeConfigUpdate{Key: runtimeconfig.KeyCPUSizeApps, DesiredValue: value}); err != nil {
		t.Fatalf("UpsertRuntimeConfig: %v", err)
	}
	if err := f.engine.SetCPUSizeApps([]string{list}); err != nil {
		t.Fatal(err)
	}
}

// Listing apps on cpu_size_apps changes a Pro app's vCPUs from 2 to 1: a
// snapshot from before the change is stale without asking vmmd, one from
// after is asked about as usual.
func TestSnapshotRefreshPassFindsSnapshotsBeforeCPUSize(t *testing.T) {
	f := newRefreshFixture(t)
	f.shadow(t)
	_, before := f.app(t, f.nodeA.ID, time.Hour, state.Snapshot{CreatedAt: time.Now().UTC().Add(-2 * time.Hour)})
	f.vmm.verdicts[before] = sched.SnapshotBackingCurrent
	f.setSizeApps(t, "*")
	_, after := f.app(t, f.nodeA.ID, 2*time.Hour, state.Snapshot{CreatedAt: time.Now().UTC()})
	f.vmm.verdicts[after] = sched.SnapshotBackingCurrent

	pass, err := f.refresher.Pass(f.ctx, f.nodeA.ID, "r1")
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if pass.Outcomes[sched.SnapshotRefreshWouldRefresh] != 1 || pass.Outcomes[sched.SnapshotRefreshCurrent] != 1 {
		t.Fatalf("pass outcomes = %v, want one would_refresh and one current", pass.Outcomes)
	}
	if len(f.vmm.asked) != 1 || f.vmm.asked[0] != f.nodeA.ID+"|"+after {
		t.Fatalf("vmmd asked %v, want only the snapshot captured after the change", f.vmm.asked)
	}
}
