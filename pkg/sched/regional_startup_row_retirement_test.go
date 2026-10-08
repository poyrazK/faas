package sched

// adr: 660
// adr: 661
import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// This isolates the accounting guard after synthetic accepted absence. The wire
// tests separately exercise the authenticated proof and actual recovery caller.
func TestStoppedRegionalStartupRowRequiresCompleteRecoveryAndExactOrigin(t *testing.T) {
	for _, scenario := range []string{"exact", "unseeded", "seeding", "failed-recovery", "ordinary", "bound-other-source", "row-run", "row-spelling", "local-run", "local-app", "local-deployment", "local-node", "other-host-epoch", "task-source"} {
		t.Run(scenario, func(t *testing.T) {
			e, store, original, v := startupRowBindingFixture(t)
			v.err = errors.New("native observation unavailable")
			if scenario == "other-host-epoch" {
				other := original
				other.Permit.ID, other.Permit.HostEpochID = uuid.NewString(), uuid.NewString()
				issueAccountingFixture(t, store, other, state.RegionalRuntimeResources{RAMMB: 512, VCPU: 4, CPUMillicores: 3000})
			}
			if err := e.SeedLedger(t.Context()); err != nil {
				t.Fatal(err)
			}
			if scenario == "failed-recovery" {
				if err := e.SeedLedger(t.Context()); !errors.Is(err, ErrRegionalRuntimeRecoveryUnresolved) {
					t.Fatal("duplicate reconstruction did not fail recovery", err)
				}
			}
			if !e.ledger.releaseOriginalRegionalRuntime(original) {
				t.Fatal("synthetic original stop failed to release its physical hold")
			}
			ins, err := store.InstanceByID(t.Context(), original.InstanceKey)
			if err != nil {
				t.Fatal(err)
			}
			source := original
			r := e.ledger.entries[original.InstanceKey]
			switch scenario {
			case "unseeded":
				l := NewNodeLedger()
				if err := l.recoverResident(Request{Instance: ins.ID, AppID: ins.AppID, DeploymentID: ins.DeploymentID, NodeID: ins.NodeID, RuntimeRunID: ins.WakeID, Plan: r.plan, RAMMB: r.admissionMB - api.PerVMOverheadMB, VCPU: r.vcpu, CPUMillicores: r.cpuMillicores}); err != nil {
					t.Fatal(err)
				}
				e.ledger = l
			case "seeding":
				if err := e.ledger.beginRegionalRecovery(); err != nil {
					t.Fatal(err)
				}
			case "ordinary":
				r.rowShadow = false
			case "bound-other-source":
				other := original
				other.Permit.ID = uuid.NewString()
				r.regionalSource = &other
			case "row-run":
				ins.WakeID = uuid.NewString()
			case "row-spelling":
				ins.ID = source.Permit.InstanceID
				if ins.ID == source.InstanceKey {
					ins.ID = strings.ReplaceAll(ins.ID, "-", "")
				}
			case "local-run":
				r.runtimeRunID = uuid.NewString()
			case "local-app":
				r.appID = uuid.NewString()
			case "local-deployment":
				r.deploymentID = uuid.NewString()
			case "local-node":
				r.nodeID = uuid.NewString()
			case "task-source":
				source.TaskID, source.Permit.Kind = uuid.NewString(), runtimequota.CapacityAppTask
				source.Permit.InstanceID, err = runtimequota.AppTaskInstanceID(source.TaskID, source.Permit.RunID)
				if err != nil {
					t.Fatal(err)
				}
				source.InstanceKey, ins.ID = source.Permit.InstanceID, source.Permit.InstanceID
				if err := source.ValidateTask(); err != nil {
					t.Fatal("task fixture is invalid", err)
				}
				delete(e.ledger.entries, original.InstanceKey)
				e.ledger.entries[source.InstanceKey] = r
			}
			counts := func() [5]int {
				l, p := e.ledger, original.Permit
				return [5]int{l.ResidentRAM(), l.UsedVCPUForNode(p.NodeID), l.UsedCPUMillicoresForNode(p.NodeID), l.Concurrency(p.Grant.Binding.RegionalAppID), l.ConcurrencyForDeployment(p.Grant.Binding.RegionalAppID, p.DeploymentID)}
			}
			before := counts()
			released := e.ledger.releaseStoppedRegionalStartupRow(source, ins)
			if scenario == "exact" {
				if !released || counts() != [5]int{} || e.ledger.ResidentFor(source.InstanceKey) {
					t.Fatal("accepted exact stop left startup resource charges", counts())
				}
			} else if released || counts() != before || !e.ledger.ResidentFor(source.InstanceKey) {
				t.Fatal("another origin or incomplete recovery released startup charges", before, counts())
			}
		})
	}
}
