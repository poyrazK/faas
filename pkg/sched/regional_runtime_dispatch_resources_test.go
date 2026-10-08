package sched

// adr: 766
import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func regionalDispatchResourceFixture(t *testing.T) (*Engine, *state.MemStore, runtimequota.LaunchSource, AppSpec, Request) {
	t.Helper()
	s, app, dep, source, _ := regionalAccountingFixture(t)
	epoch := runtimequota.HostEpoch{NodeID: source.Permit.NodeID, HostEpochID: source.Permit.HostEpochID}
	vmm := &fakeVMM{statsHook: func(context.Context, string) (*StatsSnapshot, error) {
		return &StatsSnapshot{RegionalRuntimeHostEpoch: &epoch}, nil
	}}
	spec := AppSpec{AppID: app.ID, AccountID: source.Permit.Grant.Binding.RegionalAccountID, DeploymentID: dep.ID, Plan: source.Permit.Grant.Policy.Plan, MemSizeMiB: 128, VCPUCount: 1, CPUMillicores: 250, Sidecars: []fcvm.WorkloadSpec{{Name: "cache", RamMB: 64}}}
	request := Request{Instance: source.InstanceKey, AppID: app.ID, DeploymentID: dep.ID, Plan: spec.Plan, RuntimeRunID: source.Permit.RunID, NodeID: source.Permit.NodeID,
		RAMMB: 128, SidecarMBs: []int{64}, VCPU: 1, CPUMillicores: 250, CPUStartupBoostMillicores: api.DefaultAppCPUMillicores, CPUStartupBoostUntil: time.Now().Add(time.Minute), MaxConcurrency: 5,
		NodeCeilingMB: 256, VCPUBudget: 2, CPUBudgetMillicores: api.DefaultAppCPUMillicores}
	return &Engine{store: s, ledger: NewNodeLedger(), vmm: vmm}, s, source, spec, request
}

func TestRegionalDispatchRefusesReservationMismatchBeforeIssuance(t *testing.T) {
	for _, scenario := range []string{"missing", "recovered", "row-repair", "sidecar-ram", "vcpu", "cpu-peak", "plan", "purpose"} {
		t.Run(scenario, func(t *testing.T) {
			e, store, source, spec, request := regionalDispatchResourceFixture(t)
			switch scenario {
			case "row-repair":
				request.rowShadow = true
			case "sidecar-ram":
				request.SidecarMBs = nil
			case "vcpu":
				spec.VCPUCount = 2
			case "cpu-peak":
				request.CPUStartupBoostMillicores = 0
			case "plan":
				request.Plan = api.PlanHobby
			case "purpose":
				request.Kind = KindWarmPool
			}
			if scenario == "recovered" {
				if err := e.ledger.recoverResident(request); err != nil {
					t.Fatal(err)
				}
			} else if scenario != "missing" {
				if err := e.ledger.Admit(request); err != nil {
					t.Fatal(err)
				}
			}
			refused, err := e.issueRegionalRuntimeLaunch(t.Context(), source.Permit.NodeID, source.InstanceKey, source.Permit.RunID, runtimequota.CapacityServing, spec, source.Permit.Grant)
			if !errors.Is(err, runtimequota.ErrBinding) || refused.RegionalRuntime != nil {
				t.Fatal("mismatch minted an escaped placement", err)
			}
			usage, err := store.RegionalRuntimeCapacityUsage(t.Context(), spec.AppID, source.Permit.Grant.AllocationID)
			if err != nil || usage.TotalHeld != 0 || usage.IssuedHeld != 0 {
				t.Fatal("mismatch reached durable issuance", usage, err)
			}
			e.ledger.Release(source.InstanceKey)
			if e.ledger.ResidentRAM() != 0 {
				t.Fatal("known pre-issuance refusal retained fabricated physical ownership")
			}
		})
	}
}

func TestRegionalDispatchRetainsExactResourcesForEveryInitialPurpose(t *testing.T) {
	for _, scenario := range []struct {
		purpose string
		kind    Kind
	}{
		{runtimequota.CapacityServing, KindWake},
		{runtimequota.CapacityWarm, KindWarmPool},
		{runtimequota.CapacityPrime, KindSnapshotPrime},
		{runtimequota.CapacityAppTask, KindAppTask},
	} {
		t.Run(scenario.purpose, func(t *testing.T) {
			e, store, source, spec, request := regionalDispatchResourceFixture(t)
			request.Kind = scenario.kind
			taskID := ""
			if scenario.purpose == runtimequota.CapacityAppTask {
				taskID = uuid.NewString()
				source.InstanceKey, _ = runtimequota.AppTaskInstanceID(taskID, source.Permit.RunID)
				request.Instance = source.InstanceKey
			}
			spec.PrimeOnly = scenario.kind == KindSnapshotPrime
			if err := e.ledger.Admit(request); err != nil {
				t.Fatal(err)
			}
			issued, err := e.issueRegionalRuntimeLaunchSource(t.Context(), source.Permit.NodeID, source.InstanceKey, source.Permit.RunID, scenario.purpose, taskID, spec, source.Permit.Grant)
			if err != nil || issued.RegionalRuntime == nil {
				t.Fatal("matching resources refused", err)
			}
			r := e.ledger.entries[source.InstanceKey]
			resources := state.RegionalRuntimeResources{RAMMB: 192, VCPU: 1, CPUMillicores: api.DefaultAppCPUMillicores}
			retained, err := e.store.RegionalRuntimeLaunchSource(t.Context(), spec.AppID, issued.RegionalRuntime.Permit.ID)
			if err != nil || r.regionalSource == nil || *r.regionalSource != retained.Source || r.regionalResources != resources || retained.Resources != resources {
				t.Fatal("reservation and issuance disagree", retained, err)
			}
			if _, err := e.issueRegionalRuntimeLaunchSource(t.Context(), source.Permit.NodeID, source.InstanceKey, source.Permit.RunID, scenario.purpose, taskID, issued, source.Permit.Grant); err != nil {
				t.Fatal("exact retry refused", err)
			}
			changed := issued
			changed.Sidecars = []fcvm.WorkloadSpec{{Name: "cache", RamMB: 65}}
			if refused, err := e.issueRegionalRuntimeLaunchSource(t.Context(), source.Permit.NodeID, source.InstanceKey, source.Permit.RunID, scenario.purpose, taskID, changed, source.Permit.Grant); !errors.Is(err, runtimequota.ErrBinding) || refused.RegionalRuntime == nil || refused.RegionalRuntime.Permit != issued.RegionalRuntime.Permit {
				t.Fatal("changed retry lost original cleanup identity", err)
			}
			usage, err := store.RegionalRuntimeCapacityUsage(t.Context(), spec.AppID, source.Permit.Grant.AllocationID)
			if err != nil || usage.TotalHeld != 1 || usage.IssuedHeld != 1 || e.ledger.ResidentRAM() != 200 {
				t.Fatal("retry changed physical charges", usage, err)
			}
		})
	}
}

func TestRegionalDispatchCPUDeadlineCannotReleasePhysicalPeak(t *testing.T) {
	e, _, source, spec, request := regionalDispatchResourceFixture(t)
	if err := e.ledger.Admit(request); err != nil {
		t.Fatal(err)
	}
	issued, err := e.issueRegionalRuntimeLaunch(t.Context(), source.Permit.NodeID, source.InstanceKey, source.Permit.RunID, runtimequota.CapacityServing, spec, source.Permit.Grant)
	if err != nil {
		t.Fatal(err)
	}
	if !e.ledger.SetCPUStartupBoostUntil(source.InstanceKey, time.Now().Add(-time.Second)) || e.ledger.UsedCPUMillicoresForNode(source.Permit.NodeID) != api.DefaultAppCPUMillicores {
		t.Fatal("deadline cleared original physical CPU peak")
	}
	other := Request{Instance: uuid.NewString(), AppID: spec.AppID, Plan: spec.Plan, RAMMB: 16, VCPU: 1, CPUMillicores: 250, MaxConcurrency: 5, NodeID: source.Permit.NodeID, NodeCeilingMB: 256, VCPUBudget: 2, CPUBudgetMillicores: api.DefaultAppCPUMillicores}
	if err := e.ledger.Admit(other); err == nil {
		t.Fatal("new work consumed an unreconciled CPU peak")
	}
	retained := runtimequota.LaunchSource{Permit: issued.RegionalRuntime.Permit, InstanceKey: source.InstanceKey}
	if !e.ledger.releaseOriginalRegionalRuntime(retained) || e.ledger.UsedCPUMillicoresForNode(source.Permit.NodeID) != 0 {
		t.Fatal("accepted exact-source release retained the physical peak")
	}
	if err := e.ledger.Admit(other); err != nil {
		t.Fatal("released physical peak remained unavailable", err)
	}
}
