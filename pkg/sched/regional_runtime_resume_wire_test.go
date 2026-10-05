package sched_test

// adr: 498
import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

type explicitRegionalResumeVMM struct {
	*fakeVMM
	placement   runtimequota.Placement
	regional    int
	independent int
}

func (v *explicitRegionalResumeVMM) ResumeRegionalRuntime(_ context.Context, instance string, p runtimequota.Placement) error {
	v.regional++
	v.placement = p.Clone()
	return nil
}

func (v *explicitRegionalResumeVMM) ResumeVM(context.Context, string) error {
	v.independent++
	return nil
}

type legacyRegionalResumeVMM struct {
	*fakeVMM
	resumes int
}

func (v *legacyRegionalResumeVMM) ResumeVM(context.Context, string) error {
	v.resumes++
	return nil
}

func TestRegionalResumeWireNeverFallsBackToIndependentResume(t *testing.T) {
	policy, _ := api.RuntimeQuotaPolicyForPlan(api.PlanPro)
	g := runtimequota.Grant{IssuerID: uuid.NewString(), GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(), AllocationID: uuid.NewString(), Generation: 1, IssuedAtMicros: 1, Policy: policy, Concurrency: 2,
		Binding: runtimequota.Binding{CellID: uuid.NewString(), Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: uuid.NewString(), RegionalAppID: uuid.NewString(), RegionalAppSlug: "wire-resume", APIBase: "https://cell.example.com"}}
	p := runtimequota.CapacityPermit{Grant: g, ID: uuid.NewString(), InstanceID: uuid.NewString(), RunID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), HostEpochID: uuid.NewString(), Kind: runtimequota.CapacityWarm}
	placement := runtimequota.Placement{Permit: p, Seat: &runtimequota.ServingSeat{Permit: p, ID: uuid.NewString()}}
	vmm := &explicitRegionalResumeVMM{fakeVMM: &fakeVMM{}}
	client := newClient(t, vmm)
	if err := client.ResumeRegionalWarmInstance(t.Context(), p.InstanceID, placement); err != nil {
		t.Fatal(err)
	}
	if vmm.regional != 1 || vmm.independent != 0 || !reflect.DeepEqual(vmm.placement, placement) {
		t.Fatal("wire changed pins or used independent fallback")
	}
	if err := client.ResumeWarmInstance(t.Context(), "independent"); err != nil || vmm.independent != 1 {
		t.Fatal("independent resume changed", err)
	}
	legacy := &legacyRegionalResumeVMM{fakeVMM: &fakeVMM{}}
	if err := newClient(t, legacy).ResumeRegionalWarmInstance(t.Context(), p.InstanceID, placement); err == nil || legacy.resumes != 0 {
		t.Fatal("older vmmd silently dropped the serving seat", err)
	}
}
