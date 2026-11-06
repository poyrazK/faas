// adr: 20261101160231

package sched_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGuestVCPUsFollowCPUQuotaOnlyForListedApps(t *testing.T) {
	engine, err := sched.NewEngine(context.Background(), state.NewMemStore(), sched.NewNodeLedger(), nil, nil, "", nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	listed := state.App{ID: "0b6f8c1e-2f6a-4c55-9d38-3c1f0c5e7a11", CPUMillicores: 500}
	other := state.App{ID: "7e0d4a52-91b3-4f7e-8a3c-5d2e9b1f6c44"}
	pro := api.MustLimitsFor(api.PlanPro)
	scale := api.MustLimitsFor(api.PlanScale)

	if got := engine.GuestVCPUs(listed, pro); got != pro.VCPU {
		t.Fatalf("no list: listed app boots %d vCPUs, want the plan's %d", got, pro.VCPU)
	}
	if err := engine.SetGuestVCPUFromCPUApps([]string{" " + strings.ToUpper(listed.ID) + " ", ""}); err != nil {
		t.Fatalf("SetGuestVCPUFromCPUApps: %v", err)
	}
	if got := engine.GuestVCPUs(listed, pro); got != 1 {
		t.Fatalf("listed app at 500 millicores boots %d vCPUs, want 1", got)
	}
	if got := engine.GuestVCPUs(other, pro); got != pro.VCPU {
		t.Fatalf("unlisted app boots %d vCPUs, want the plan's %d", got, pro.VCPU)
	}
	if err := engine.SetGuestVCPUFromCPUApps([]string{"*"}); err != nil {
		t.Fatalf("SetGuestVCPUFromCPUApps(*): %v", err)
	}
	if got := engine.GuestVCPUs(other, pro); got != 1 {
		t.Fatalf("* with the default 1,000 millicores on Pro boots %d vCPUs, want 1", got)
	}
	if err := engine.SetGuestVCPUFromCPUApps([]string{"not-an-app"}); err == nil {
		t.Fatal("SetGuestVCPUFromCPUApps accepted an entry that is neither an app ID nor *")
	}
	if err := engine.SetGuestVCPUFromCPUApps(nil); err != nil {
		t.Fatalf("SetGuestVCPUFromCPUApps(nil): %v", err)
	}
	if got := engine.GuestVCPUs(listed, scale); got != scale.VCPU {
		t.Fatalf("cleared list: listed app boots %d vCPUs, want the plan's %d", got, scale.VCPU)
	}
}

// A listed app on the default gets its plan's burst ceiling: one core on
// Free, Hobby and Pro, two on Scale. A smaller choice stays (decision 1).
func TestListedAppsGetThePlansBurstCeiling(t *testing.T) {
	engine, err := sched.NewEngine(context.Background(), state.NewMemStore(), sched.NewNodeLedger(), nil, nil, "", nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	app := state.App{ID: "0b6f8c1e-2f6a-4c55-9d38-3c1f0c5e7a11", CPUMillicores: api.DefaultAppCPUMillicores}
	small := state.App{ID: app.ID, CPUMillicores: 250}
	scale := api.MustLimitsFor(api.PlanScale)

	if got := engine.AppCPUMillicores(app, scale); got != api.DefaultAppCPUMillicores {
		t.Fatalf("unlisted Scale app CPU = %d, want its configured %d", got, api.DefaultAppCPUMillicores)
	}
	if err := engine.SetGuestVCPUFromCPUApps([]string{app.ID}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		plan       api.Plan
		app        state.App
		cpu, vcpus int
	}{
		{api.PlanFree, app, 1000, 1}, {api.PlanHobby, app, 1000, 1}, {api.PlanPro, app, 1000, 1},
		{api.PlanScale, app, 2000, 2}, {api.PlanScale, small, 250, 1},
	} {
		limits := api.MustLimitsFor(tc.plan)
		if got := engine.AppCPUMillicores(tc.app, limits); got != tc.cpu {
			t.Errorf("%s app at %d m: CPU = %d, want %d", tc.plan, tc.app.CPUMillicores, got, tc.cpu)
		}
		if got := engine.GuestVCPUs(tc.app, limits); got != tc.vcpus {
			t.Errorf("%s app at %d m: vCPUs = %d, want %d", tc.plan, tc.app.CPUMillicores, got, tc.vcpus)
		}
		if !api.ValidAppCPUCeilingMillicores(tc.plan, engine.AppCPUMillicores(tc.app, limits)) {
			t.Errorf("%s: schedd sends a CPU value vmmd refuses", tc.plan)
		}
	}
}

// A listed app books its plan's baseline, 1,000 m per GB of plan RAM, or its
// smaller choice; an unlisted app books and stamps as before (step 5b).
func TestListedAppsBookThePlansBaseline(t *testing.T) {
	engine, err := sched.NewEngine(context.Background(), state.NewMemStore(), sched.NewNodeLedger(), nil, nil, "", nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	app := state.App{ID: "0b6f8c1e-2f6a-4c55-9d38-3c1f0c5e7a11", CPUMillicores: api.DefaultAppCPUMillicores}
	small := state.App{ID: app.ID, CPUMillicores: 250}
	free := api.MustLimitsFor(api.PlanFree)
	if got, stamp := engine.AppCPUBookingMillicores(app, free), engine.AppCPUBookingStamp(app, free); got != 1000 || stamp != 0 {
		t.Fatalf("unlisted Free app books %d m, stamps %d; want 1000 and no stamp", got, stamp)
	}
	if err := engine.SetGuestVCPUFromCPUApps([]string{app.ID}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		plan    api.Plan
		app     state.App
		booking int
	}{
		{api.PlanFree, app, 250}, {api.PlanHobby, app, 500}, {api.PlanPro, app, 1000},
		{api.PlanScale, app, 2000}, {api.PlanScale, small, 250}, {api.PlanHobby, small, 250},
	} {
		limits := api.MustLimitsFor(tc.plan)
		if got := engine.AppCPUBookingMillicores(tc.app, limits); got != tc.booking {
			t.Errorf("%s app at %d m books %d m, want %d", tc.plan, tc.app.CPUMillicores, got, tc.booking)
		}
		if got := engine.AppCPUBookingStamp(tc.app, limits); got != tc.booking {
			t.Errorf("%s app at %d m stamps %d m, want %d", tc.plan, tc.app.CPUMillicores, got, tc.booking)
		}
		if got := engine.AppCPUBookingMillicores(tc.app, limits); got > engine.AppCPUMillicores(tc.app, limits) {
			t.Errorf("%s: booking %d m exceeds the ceiling", tc.plan, got)
		}
	}
}

// adr: 20261101215228 — the runtime-config shape moves a listed app's ceiling,
// vCPUs and booking; an unlisted app is unaffected.
func TestCPUShapeMovesListedApps(t *testing.T) {
	engine, err := sched.NewEngine(context.Background(), state.NewMemStore(), sched.NewNodeLedger(), nil, nil, "", nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	app := state.App{ID: "0b6f8c1e-2f6a-4c55-9d38-3c1f0c5e7a11", CPUMillicores: api.DefaultAppCPUMillicores}
	other := state.App{ID: "7e0d4a52-91b3-4f7e-8a3c-5d2e9b1f6c44", CPUMillicores: api.DefaultAppCPUMillicores}
	if err := engine.SetGuestVCPUFromCPUApps([]string{app.ID}); err != nil {
		t.Fatal(err)
	}
	engine.SetCPUShape(api.CPUShape{BaselineMillicoresPerGB: 500, CeilingFloorMillicores: 500})
	free, scale := api.MustLimitsFor(api.PlanFree), api.MustLimitsFor(api.PlanScale)
	for _, tc := range []struct {
		name                string
		app                 state.App
		limits              api.Limits
		cpu, vcpus, booking int
	}{
		{"listed Free", app, free, 500, 1, 125},
		{"listed Scale", app, scale, 1000, 1, 1000},
		{"unlisted Scale", other, scale, 1000, scale.VCPU, 1000},
	} {
		cpu, vcpus, booking := engine.AppCPUMillicores(tc.app, tc.limits), engine.GuestVCPUs(tc.app, tc.limits), engine.AppCPUBookingMillicores(tc.app, tc.limits)
		if cpu != tc.cpu || vcpus != tc.vcpus || booking != tc.booking {
			t.Errorf("%s: CPU/vCPUs/booking = %d/%d/%d, want %d/%d/%d", tc.name, cpu, vcpus, booking, tc.cpu, tc.vcpus, tc.booking)
		}
	}
	engine.SetCPUShape(api.CPUShape{})
	if got := engine.AppCPUMillicores(app, scale); got != 2000 {
		t.Errorf("compiled shape again: listed Scale CPU = %d, want 2000", got)
	}
}
