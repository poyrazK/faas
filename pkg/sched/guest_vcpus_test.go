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
