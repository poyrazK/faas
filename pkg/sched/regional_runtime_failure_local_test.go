package sched

// adr: 762
import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRegionalRuntimeFailureLocalReservationOrigin(t *testing.T) {
	for _, change := range []string{"exact", "run", "unbound", "node", "deployment", "app", "host-epoch", "permit"} {
		t.Run(change, func(t *testing.T) {
			store, account, app, dep := regionalQuotaSchedulerFixture(t)
			freezeRegionalSchedulerApp(t, store, account, app)
			head, err := store.RegionalRuntimeQuotaHead(t.Context(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			p := runtimequota.CapacityPermit{Grant: head.Grant, ID: uuid.NewString(), InstanceID: uuid.NewString(), RunID: uuid.NewString(), DeploymentID: uuid.MustParse(dep.ID).String(), NodeID: uuid.NewString(), HostEpochID: uuid.NewString(), Kind: runtimequota.CapacityServing}
			source := runtimequota.LaunchSource{Permit: p, InstanceKey: p.InstanceID}
			request := Request{Instance: source.InstanceKey, AppID: app.ID, DeploymentID: dep.ID, NodeID: p.NodeID, RuntimeRunID: p.RunID, Plan: api.PlanPro, RAMMB: 128, VCPU: 1, MaxConcurrency: 1}
			switch change {
			case "run":
				request.RuntimeRunID = uuid.NewString()
			case "unbound":
				request.RuntimeRunID = ""
			case "node":
				request.NodeID = uuid.NewString()
			case "deployment":
				request.DeploymentID = uuid.NewString()
			case "app":
				request.AppID = uuid.NewString()
			}
			if change == "exact" || change == "host-epoch" || change == "permit" {
				bound := source
				if change == "host-epoch" {
					bound.Permit.HostEpochID = uuid.NewString()
				}
				if change == "permit" {
					bound.Permit.ID = uuid.NewString()
				}
				request.RegionalRuntime = &bound
			}
			ledger := NewNodeLedger()
			if err := ledger.Admit(request); err != nil {
				t.Fatal(err)
			}
			released := ledger.releaseOriginalRegionalRuntime(source)
			if released != (change == "exact") || ledger.ResidentFor(source.InstanceKey) == released {
				t.Fatal("old stop released a different reservation", released, change)
			}
		})
	}
}

func TestRegionalRuntimeFailureCallersHoldWithoutOwner(t *testing.T) {
	for _, caller := range []string{"wake", "mirror", "prime"} {
		t.Run(caller, func(t *testing.T) {
			store, account, app, dep := regionalQuotaSchedulerFixture(t)
			ins, err := store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateColdBooting), 128, uuid.NewString(), uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			freezeRegionalSchedulerApp(t, store, account, app)
			head, err := store.RegionalRuntimeQuotaHead(t.Context(), app.ID)
			if err != nil {
				t.Fatal(err)
			}
			p := runtimequota.CapacityPermit{Grant: head.Grant, ID: uuid.NewString(), InstanceID: uuid.MustParse(ins.ID).String(), RunID: uuid.MustParse(ins.WakeID).String(), DeploymentID: uuid.MustParse(ins.DeploymentID).String(), NodeID: uuid.MustParse(ins.NodeID).String(), HostEpochID: uuid.NewString(), Kind: runtimequota.CapacityServing}
			placement := runtimequota.Placement{Permit: p, Seat: &runtimequota.ServingSeat{Permit: p, ID: uuid.NewString()}}
			if err := store.IssueRegionalRuntimeLaunch(t.Context(), placement, runtimequota.LaunchSource{Permit: p, InstanceKey: ins.ID}); err != nil {
				t.Fatal(err)
			}
			vmm := &fakeVMM{}
			engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
			if err := engine.ledger.Admit(Request{Instance: ins.ID, AppID: app.ID, DeploymentID: dep.ID, NodeID: ins.NodeID, RuntimeRunID: ins.WakeID, Plan: api.PlanPro, RAMMB: 128, VCPU: 1, MaxConcurrency: 1}); err != nil {
				t.Fatal(err)
			}
			spec := AppSpec{RegionalRuntime: &placement}
			if caller == "prime" {
				engine.cleanupFailedPrime(t.Context(), ins, spec, "prime_cold_boot_failed", true)
			} else {
				mode := string(state.InstanceModeNormal)
				if caller == "mirror" {
					mode = string(state.InstanceModeMirror)
				}
				engine.cleanupFailedWake(t.Context(), bootInput{insID: ins.ID, appID: app.ID, nodeID: ins.NodeID, spec: spec}, mode, "vmm_boot_failed", true)
			}
			after, err := store.InstanceByID(t.Context(), ins.ID)
			if err != nil || after.State != ins.State || after.TerminalAt != nil || !engine.ledger.ResidentFor(ins.ID) || vmm.destroys != 0 {
				t.Fatal("unconfigured regional caller used legacy cleanup", after, err, vmm.destroys)
			}
		})
	}
}
