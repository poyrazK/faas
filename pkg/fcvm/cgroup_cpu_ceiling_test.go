// adr: 20261101160231

package fcvm

import (
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// vmmd accepts a two-core ceiling on Scale a release before schedd sends one
// (ADR-20261101160231 rollout step 4).
func TestAppCPUMaxAcceptsThePlansBurstCeiling(t *testing.T) {
	period := cpuMaxPeriodUS
	got, err := appCPUMax(api.PlanScale, 2000)
	if err != nil {
		t.Fatalf("Scale 2000 m: %v", err)
	}
	if want := fmt.Sprintf("%d %d", 2*period, period); got != want {
		t.Fatalf("Scale 2000 m cpu.max = %q, want %q", got, want)
	}
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro} {
		if _, err := appCPUMax(plan, 2000); err == nil {
			t.Errorf("plan %s accepted a 2000 m ceiling above its burst ceiling", plan)
		}
	}
	if _, err := appCPUMax(api.PlanScale, 1500); err == nil {
		t.Error("Scale accepted 1500 m, which is neither a customer shape nor whole cores")
	}
}

func TestStartupCPUProfileKeepsATwoCoreCeiling(t *testing.T) {
	profile, err := resolveStartupCPUProfile(api.PlanScale, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if profile.StartupMillicores != 2000 || profile.ConfiguredMillicores != 2000 {
		t.Fatalf("Scale 2000 m profile = %+v, want 2000/2000 (the boost never lowers the ceiling)", profile)
	}
	if _, err := resolveStartupCPUProfile(api.PlanPro, 2000); err == nil {
		t.Fatal("Pro accepted a 2000 m ceiling")
	}
	profile, err = resolveStartupCPUProfile(api.PlanFree, 250)
	if err != nil || profile.StartupMillicores != 1000 || profile.ConfiguredMillicores != 250 {
		t.Fatalf("Free 250 m profile = %+v, %v; want 1000/250", profile, err)
	}
}
