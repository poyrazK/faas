package sched

// adr: 788
import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func startupRowBindingFixture(t *testing.T) (*Engine, *state.MemStore, runtimequota.LaunchSource, *inventoryReadingVMM) {
	t.Helper()
	s, _, _, source, resources, ins := regionalAccountingResidentFixture(t, true)
	source.Permit.RunID = ins.WakeID
	issueAccountingFixture(t, s, source, resources)
	_, sample := nativeReconciliationFixture(t)
	sample.Host = runtimequota.HostEpoch{NodeID: source.Permit.NodeID, HostEpochID: source.Permit.HostEpochID}
	owner := &sample.Inventory.Owners[0]
	owner.Instance, owner.Lease.Instance, owner.Lease.Plan = ins.ID, ins.ID, source.Permit.Grant.Policy.Plan
	owner.Placement, owner.OriginalSource, owner.OriginalResources = &runtimequota.Placement{Permit: source.Permit}, &source, &resources
	owner.Cgroup.Path = fcvm.ParentCgroupFor(owner.Lease.Plan) + "/" + ins.ID
	sample.Inventory.Cgroups[0].Path = owner.Cgroup.Path
	v := &inventoryReadingVMM{fakeVMM: &fakeVMM{}, sample: sample}
	return newEngine(t, s, v, &fakeNotifier{}, "1.10.0"), s, source, v
}

func TestRegionalStartupRowBindingRetainsBothChargesUntilExactStop(t *testing.T) {
	e, _, source, _ := startupRowBindingFixture(t)
	if err := e.SeedLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	r := e.ledger.entries[source.InstanceKey]
	if r == nil || !r.rowShadow || r.regionalSource == nil || *r.regionalSource != source || r.regionalResources.RAMMB != 512 || r.admissionMB != 128+api.PerVMOverheadMB {
		t.Fatal("original source replaced row accounting or acquired dispatch authority")
	}
	want := 512 + 128 + 2*api.PerVMOverheadMB
	if e.ledger.ResidentRAM() != want || e.ledger.Concurrency(r.appID) != 2 {
		t.Fatal("advisory binding released or merged charges")
	}
	e.ledger.Release(source.InstanceKey)
	e.ledger.BeginSnapshot(source.InstanceKey)
	wrong := source
	wrong.Permit.HostEpochID = uuid.NewString()
	if e.ledger.releaseOriginalRegionalRuntime(wrong) || e.ledger.ResidentRAM() != want || e.ledger.Concurrency(r.appID) != 2 {
		t.Fatal("generic cleanup or another host epoch released startup ownership")
	}
	if !e.ledger.releaseOriginalRegionalRuntime(source) || e.ledger.ResidentRAM() != 0 || e.ledger.Concurrency(r.appID) != 0 || e.ledger.ResidentFor(source.InstanceKey) {
		t.Fatal("accepted exact-source stop left the original row shadow charged")
	}
}

func TestRegionalStartupRowBindingPreservesAmbiguousAndUnavailableOrigins(t *testing.T) {
	for _, scenario := range []string{"native-source", "native-missing-source", "native-unresolved", "unrelated-helper", "unclaimed-task", "unresolved-other-owner", "native-unavailable", "other-host-epoch", "replacement-run", "lookup-spelling"} {
		t.Run(scenario, func(t *testing.T) {
			e, s, source, v := startupRowBindingFixture(t)
			want := 512 + 128 + 2*api.PerVMOverheadMB
			switch scenario {
			case "native-source":
				changed := *v.sample.Inventory.Owners[0].OriginalSource
				changed.Permit.RunID = uuid.NewString()
				v.sample.Inventory.Owners[0].OriginalSource = &changed
			case "native-missing-source":
				v.sample.Inventory.Owners[0].OriginalSource = nil
			case "native-unresolved":
				v.sample.Inventory.Unresolved = []string{"historical helper family is unresolved"}
			case "unclaimed-task":
				v.sample.Inventory.ManagedUIDTasks = append(v.sample.Inventory.ManagedUIDTasks, fcvm.NativeRuntimeTask{PID: 999, UID: 20001, StartTime: 100})
			case "unrelated-helper":
				v.sample.Inventory.HelperTasks = []fcvm.NativeRuntimeHelperTask{{Instance: uuid.NewString(), OwnerGeneration: uuid.NewString(), PID: 999, StartTime: 100}}
			case "unresolved-other-owner":
				v.sample.Inventory.Owners = append(v.sample.Inventory.Owners, fcvm.NativeRuntimeObservation{Unresolved: "historical owner"})
			case "native-unavailable":
				v.err = errors.New("native observation unavailable")
			case "other-host-epoch":
				other := source
				other.Permit.ID, other.Permit.HostEpochID = uuid.NewString(), uuid.NewString()
				issueAccountingFixture(t, s, other, state.RegionalRuntimeResources{RAMMB: 512, VCPU: 4, CPUMillicores: 3000})
				want += 512 + api.PerVMOverheadMB
			case "replacement-run", "lookup-spelling":
				// Read-side row substitutions never change the retained native source.
				wrapped := startupBindingRowSubstitution{Store: s, source: source, scenario: scenario}
				e.store = wrapped
			}
			if err := e.SeedLedger(t.Context()); err != nil {
				t.Fatal(err)
			}
			r := e.ledger.entries[source.InstanceKey]
			if r == nil || r.regionalSource != nil || e.ledger.ResidentRAM() != want {
				t.Fatal("uncertain observation rebound or changed conservative charges", e.ledger.ResidentRAM(), want)
			}
		})
	}
}

type startupBindingRowSubstitution struct {
	state.Store
	source   runtimequota.LaunchSource
	scenario string
}

func (s startupBindingRowSubstitution) InstanceByID(ctx context.Context, id string) (state.Instance, error) {
	ins, err := s.Store.InstanceByID(ctx, id)
	if id == s.source.InstanceKey {
		if s.scenario == "replacement-run" {
			ins.WakeID = uuid.NewString()
		} else {
			ins.ID = s.source.Permit.InstanceID
			if ins.ID == s.source.InstanceKey {
				ins.ID = strings.ReplaceAll(ins.ID, "-", "")
			}
		}
	}
	return ins, err
}

func TestRegionalStartupRowBindingCannotAuthorizeDispatch(t *testing.T) {
	e, _, source, _ := startupRowBindingFixture(t)
	if err := e.SeedLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	resources := state.RegionalRuntimeResources{RAMMB: 512, VCPU: 4, CPUMillicores: 3000}
	if err := e.ledger.bindOriginalRegionalRuntime(source, resources); !errors.Is(err, runtimequota.ErrBinding) {
		t.Fatal("startup shadow acquired a new physical dispatch", err)
	}
}

type cancellingStartupRowRead struct {
	state.Store
	cancel context.CancelFunc
}

func (s cancellingStartupRowRead) InstanceByID(ctx context.Context, id string) (state.Instance, error) {
	ins, err := s.Store.InstanceByID(ctx, id)
	s.cancel()
	return ins, err
}

func TestRegionalStartupRowBindingCancellationRetainsUnboundCharges(t *testing.T) {
	e, s, source, _ := startupRowBindingFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	e.store = cancellingStartupRowRead{Store: s, cancel: cancel}
	if err := e.SeedLedger(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled row read completed startup", err)
	}
	if e.ledger.entries[source.InstanceKey].regionalSource != nil || e.ledger.ResidentRAM() != 512+128+2*api.PerVMOverheadMB {
		t.Fatal("cancelled observation rebound or released original charges")
	}
}
