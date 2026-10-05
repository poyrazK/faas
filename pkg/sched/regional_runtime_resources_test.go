package sched

// adr: 637
import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRegionalRuntimeResourcesMatchNativeGuestAndCPUProfile(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale} {
		for _, disabled := range []bool{false, true} {
			for _, configured := range []int{0, 250, api.DefaultAppCPUMillicores} {
				spec := AppSpec{Plan: plan, MemSizeMiB: 128, VCPUCount: 2, CPUMillicores: int32(configured), DisableStartupCPUBoost: disabled, Sidecars: []fcvm.WorkloadSpec{{RamMB: 64}, {RamMB: 32}}}
				resources, valid := regionalRuntimeLaunchResources(spec)
				ceiling := int(int64(plan.CPUQuotaUS()) * 1000 / int64(plan.CPUPeriodUS()))
				cpu := configured
				if cpu == 0 {
					cpu = ceiling
				}
				if !disabled {
					cpu = max(cpu, min(api.DefaultAppCPUMillicores, ceiling))
				}
				if !valid || resources != (state.RegionalRuntimeResources{RAMMB: 224, VCPU: 2, CPUMillicores: cpu}) {
					t.Fatalf("plan=%s disabled=%t configured=%d footprint=%+v valid=%t", plan, disabled, configured, resources, valid)
				}
			}
		}
	}
}

func TestRegionalRuntimeResourcesRefuseInvalidLaunchShape(t *testing.T) {
	for _, scenario := range []string{"main-ram", "vcpu", "cpu", "above-plan", "invalid-plan", "overflow-ram"} {
		t.Run(scenario, func(t *testing.T) {
			spec := AppSpec{Plan: api.PlanPro, MemSizeMiB: 128, VCPUCount: 2, CPUMillicores: 250, Sidecars: []fcvm.WorkloadSpec{{RamMB: 64}}}
			switch scenario {
			case "main-ram":
				spec.MemSizeMiB = 0
			case "vcpu":
				spec.VCPUCount = 0
			case "cpu":
				spec.CPUMillicores = -1
			case "above-plan":
				spec.CPUMillicores = int32(int64(spec.Plan.CPUQuotaUS())*1000/int64(spec.Plan.CPUPeriodUS())) + api.DefaultAppCPUMillicores
			case "invalid-plan":
				spec.Plan = "invalid"
			case "overflow-ram":
				spec.Sidecars[0].RamMB = api.RegionalRuntimeResourceValueMax
			}
			if resources, valid := regionalRuntimeLaunchResources(spec); valid {
				t.Fatal("invalid actual shape became a retained claim", resources)
			}
		})
	}
}
