package sched

// adr: 636
import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

func regionalAccountingFixture(t *testing.T) (*state.MemStore, state.App, state.Deployment, runtimequota.LaunchSource, state.RegionalRuntimeResources) {
	t.Helper()
	s, app, dep, source, resources, _ := regionalAccountingResidentFixture(t, false)
	return s, app, dep, source, resources
}

func regionalAccountingResidentFixture(t *testing.T, resident bool) (*state.MemStore, state.App, state.Deployment, runtimequota.LaunchSource, state.RegionalRuntimeResources, state.Instance) {
	t.Helper()
	s, acct, app, dep := regionalQuotaSchedulerFixture(t)
	node, err := s.CreateComputeNode(t.Context(), state.ComputeNode{ID: uuid.NewString(), Name: "inventory", TargetURL: "unix:///run/faas/inventory.sock", Active: true, AdmissionCeilingMB: 256, VCPUBudget: 1, VPCPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	var ins state.Instance
	if resident {
		ins, err = s.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 128, node.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
	}
	freezeRegionalSchedulerApp(t, s, acct, app)
	head, err := s.RegionalRuntimeQuotaHead(t.Context(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := runtimequota.CapacityPermit{Grant: head.Grant, ID: uuid.NewString(), InstanceID: uuid.NewString(), RunID: uuid.NewString(), DeploymentID: uuid.MustParse(dep.ID).String(), NodeID: node.ID, HostEpochID: uuid.NewString(), Kind: runtimequota.CapacityServing}
	source := runtimequota.LaunchSource{Permit: p, InstanceKey: p.InstanceID}
	if resident {
		source.InstanceKey, source.Permit.InstanceID = ins.ID, uuid.MustParse(ins.ID).String()
	}
	return s, app, dep, source, state.RegionalRuntimeResources{RAMMB: 512, VCPU: 4, CPUMillicores: 3000}, ins
}

func issueAccountingFixture(t *testing.T, s state.Store, source runtimequota.LaunchSource, resources state.RegionalRuntimeResources) {
	t.Helper()
	p := source.Permit
	if err := s.IssueRegionalRuntimeLaunchResources(t.Context(), runtimequota.Placement{Permit: p, Seat: &runtimequota.ServingSeat{Permit: p, ID: uuid.NewString()}}, source, resources); err != nil {
		t.Fatal(err)
	}
}

func TestRegionalRuntimeInventoryStartupRetainsCollectedApp(t *testing.T) {
	s, app, _, source, resources := regionalAccountingFixture(t)
	issueAccountingFixture(t, s, source, resources)
	if err := s.DeleteApp(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, s, &fakeVMM{}, &fakeNotifier{}, "1.10.0") // No current cell signer.
	if err := e.SeedLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	l := e.ledger
	if l.ResidentRAMForNode(source.Permit.NodeID) != resources.RAMMB+api.PerVMOverheadMB || l.UsedVCPUForNode(source.Permit.NodeID) != resources.VCPU || l.UsedCPUMillicoresForNode(source.Permit.NodeID) != resources.CPUMillicores || l.Concurrency(app.ID) != 1 {
		t.Fatal("collected app lost original resources")
	}
	// Rows, mutable plans and a lower ceiling cannot make the orphan disappear.
	l.Release(source.InstanceKey)
	l.BeginSnapshot(source.InstanceKey)
	if l.ResidentRAM() != resources.RAMMB+api.PerVMOverheadMB {
		t.Fatal("generic cleanup released an issued hold")
	}
	wrong := source
	wrong.Permit.HostEpochID = uuid.NewString()
	if l.releaseOriginalRegionalRuntime(wrong) || l.releaseOriginalRegionalRuntime(runtimequota.LaunchSource{Permit: source.Permit, InstanceKey: uuid.NewString()}) {
		t.Fatal("inexact stop released hold")
	}
	if err := l.Admit(Request{Instance: uuid.NewString(), AppID: app.ID, Plan: api.PlanPro, RAMMB: 1, VCPU: 1, MaxConcurrency: 1, NodeID: source.Permit.NodeID, NodeCeilingMB: 256}); err == nil {
		t.Fatal("over-capacity restart admitted more work")
	}
	if !l.releaseOriginalRegionalRuntime(source) || l.ResidentRAM() != 0 || l.Concurrency(app.ID) != 0 {
		t.Fatal("exact original stop failed to release hold")
	}
}

func TestRegionalRuntimeInventoryKeepsReplacementAndHostEpochs(t *testing.T) {
	s, app, _, source, resources, _ := regionalAccountingResidentFixture(t, true)
	issueAccountingFixture(t, s, source, resources)
	// A distinct host incarnation can retain the same native lookup and run.
	other := source
	other.Permit.ID, other.Permit.HostEpochID = uuid.NewString(), uuid.NewString()
	issueAccountingFixture(t, s, other, resources)

	e := newEngine(t, s, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if err := e.SeedLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	if e.ledger.ResidentRAM() != 2*(resources.RAMMB+api.PerVMOverheadMB)+128+api.PerVMOverheadMB || e.ledger.Concurrency(app.ID) != 3 {
		t.Fatal("replacement row or older host epoch disappeared")
	}
	if !e.ledger.releaseOriginalRegionalRuntime(source) || !e.ledger.ResidentFor(source.InstanceKey) || e.ledger.ResidentRAM() != resources.RAMMB+128+2*api.PerVMOverheadMB {
		t.Fatal("original stop released replacement")
	}
	if e.ledger.releaseOriginalRegionalRuntime(source) {
		t.Fatal("original hold released twice")
	}
}

type interruptedRegionalInventory struct {
	state.Store
	entry state.RegionalRuntimeInventoryEntry
	mode  string
}

func (s interruptedRegionalInventory) ListRegionalRuntimeInventory(_ context.Context, cell string, after state.RegionalRuntimeRetirementCursor, _ int) ([]state.RegionalRuntimeInventoryEntry, error) {
	if cell != "" {
		return nil, errors.New("startup narrowed inventory to current signer")
	}
	if after.AppID == "" || s.mode == "repeat" {
		return []state.RegionalRuntimeInventoryEntry{s.entry}, nil
	}
	return nil, errors.New("inventory connection lost")
}

func TestRegionalRuntimeInventoryStartupBarrierRetainsPartialCharge(t *testing.T) {
	for _, variant := range []string{"read", "repeat", "unknown-resources", "unknown-source"} {
		t.Run(variant, func(t *testing.T) {
			s, app, _, source, resources := regionalAccountingFixture(t)
			now := time.Now()
			entry := state.RegionalRuntimeInventoryEntry{Receipt: state.RegionalRuntimeCapacityReceipt{Permit: source.Permit, IssuedAt: &now}, Source: &source, Resources: resources, ServingSeatsHeld: 1}
			if variant == "unknown-resources" {
				entry.Resources = state.RegionalRuntimeResources{}
			}
			if variant == "unknown-source" {
				entry.Source = nil
			}
			e := newEngine(t, interruptedRegionalInventory{Store: s, entry: entry, mode: variant}, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			if err := e.SeedLedger(t.Context()); !errors.Is(err, ErrRegionalRuntimeRecoveryUnresolved) {
				t.Fatal("incomplete startup succeeded", err)
			}
			want := resources.RAMMB + api.PerVMOverheadMB
			if variant == "unknown-resources" {
				want = 0
			}
			if e.ledger.ResidentRAM() != want {
				t.Fatal("partial known charge lost or repeated", e.ledger.ResidentRAM())
			}
			if err := e.ledger.Admit(Request{Instance: uuid.NewString(), AppID: app.ID, Plan: api.PlanPro, RAMMB: 1, VCPU: 1, MaxConcurrency: 5, NodeID: uuid.NewString()}); !errors.Is(err, ErrRegionalRuntimeRecoveryUnresolved) {
				t.Fatal("incomplete inventory allowed admission on another node", err)
			}
			if variant == "read" {
				e.ledger.releaseOriginalRegionalRuntime(source)
			}
			if err := e.ledger.Admit(Request{Instance: uuid.NewString(), AppID: app.ID, Plan: api.PlanPro, RAMMB: 1, VCPU: 1, MaxConcurrency: 5}); !errors.Is(err, ErrRegionalRuntimeRecoveryUnresolved) {
				t.Fatal("one stop cleared traversal barrier", err)
			}
		})
	}
}

func TestRegionalRuntimeDispatchReservationRequiresFullPermitForRelease(t *testing.T) {
	_, app, dep, source, _ := regionalAccountingFixture(t)
	l := NewNodeLedger()
	request := Request{Instance: source.InstanceKey, AppID: app.ID, DeploymentID: dep.ID, NodeID: source.Permit.NodeID, RuntimeRunID: source.Permit.RunID, RegionalRuntime: &source, Plan: api.PlanPro, RAMMB: 128, VCPU: 1, CPUMillicores: api.DefaultAppCPUMillicores, MaxConcurrency: 5}
	if err := l.Admit(request); err != nil {
		t.Fatal(err)
	}
	l.Release(source.InstanceKey)
	l.BeginSnapshot(source.InstanceKey)
	if !l.ResidentFor(source.InstanceKey) || l.Concurrency(app.ID) != 1 {
		t.Fatal("row bookkeeping released physical origin")
	}
	other := source
	other.Permit.HostEpochID = uuid.NewString()
	if err := l.bindOriginalRegionalRuntime(other, state.RegionalRuntimeResources{RAMMB: 128, VCPU: 1, CPUMillicores: api.DefaultAppCPUMillicores}); !errors.Is(err, runtimequota.ErrBinding) {
		t.Fatal("rebound physical origin", err)
	}
	if l.releaseOriginalRegionalRuntime(other) || !l.releaseOriginalRegionalRuntime(source) {
		t.Fatal("release ignored full permit")
	}
}

func TestRegionalRuntimeRecoveryRecordsAllExistingRowsAboveLoweredLimits(t *testing.T) {
	s := state.NewMemStore()
	_, app, dep := seedApp(t, s, api.PlanPro, 512, 1)
	node, err := s.CreateComputeNode(t.Context(), state.ComputeNode{ID: uuid.NewString(), Name: "lowered", TargetURL: "unix:///run/faas/lowered.sock", Active: true, AdmissionCeilingMB: api.RAMAdmissionCeilingMB, VCPUBudget: api.VCPUSlots, VPCPUs: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 512, node.ID, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	node.AdmissionCeilingMB, node.VCPUBudget = 128, 1
	if _, err := s.UpsertComputeNodeFromOperator(t.Context(), node); err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, s, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if err := e.SeedLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	if e.ledger.ResidentRAMForNode(node.ID) != 3*(512+api.PerVMOverheadMB) || e.ledger.Concurrency(app.ID) != 3 || e.ledger.UsedVCPUForNode(node.ID) != 3*api.MustLimitsFor(api.PlanPro).VCPU {
		t.Fatal("startup dropped over-capacity rows")
	}
	if err := e.ledger.Admit(Request{Instance: uuid.NewString(), AppID: app.ID, Plan: api.PlanPro, RAMMB: 1, VCPU: 1, MaxConcurrency: 1, NodeID: node.ID, NodeCeilingMB: 128}); err == nil {
		t.Fatal("recovery exception admitted new work")
	}
}

type waitingRegionalInventory struct {
	state.Store
	entered chan struct{}
	release chan struct{}
}

func (s waitingRegionalInventory) ListRegionalRuntimeInventory(ctx context.Context, _ string, _ state.RegionalRuntimeRetirementCursor, _ int) ([]state.RegionalRuntimeInventoryEntry, error) {
	close(s.entered)
	select {
	case <-s.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestRegionalRuntimeInventoryBlocksAdmissionDuringTraversal(t *testing.T) {
	s := waitingRegionalInventory{Store: state.NewMemStore(), entered: make(chan struct{}), release: make(chan struct{})}
	e := newEngine(t, s, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	done := make(chan error, 1)
	go func() { done <- e.SeedLedger(t.Context()) }()
	<-s.entered
	request := Request{Instance: uuid.NewString(), AppID: uuid.NewString(), Plan: api.PlanPro, RAMMB: 128, VCPU: 1, MaxConcurrency: 1}
	err := e.ledger.Admit(request)
	close(s.release)
	if seedErr := <-done; seedErr != nil {
		t.Fatal(seedErr)
	}
	if !errors.Is(err, ErrRegionalRuntimeRecoveryUnresolved) {
		t.Fatal("admission escaped before inventory completion", err)
	}
	if err := e.ledger.Admit(request); err != nil {
		t.Fatal("complete empty inventory retained traversal barrier", err)
	}
}

func TestRegionalRuntimeInventoryUnknownRetryRemainsUnresolved(t *testing.T) {
	_, app, _, source, _ := regionalAccountingFixture(t)
	now := time.Now()
	entry := state.RegionalRuntimeInventoryEntry{Receipt: state.RegionalRuntimeCapacityReceipt{Permit: source.Permit, IssuedAt: &now}, Source: &source, ServingSeatsHeld: 1}
	l := NewNodeLedger()
	for i := 0; i < 2; i++ {
		if err := l.retainRegionalInventory(entry, 128); !errors.Is(err, ErrRegionalRuntimeRecoveryUnresolved) {
			t.Fatal("retry invented missing provenance", err)
		}
	}
	if l.Concurrency(app.ID) != 1 || l.ResidentRAM() != 0 {
		t.Fatal("unknown retry invented resources or duplicated seats")
	}
	// A generic zero-resource node cleanup cannot make exact stop handling panic.
	request := Request{Instance: source.InstanceKey, AppID: app.ID, Plan: api.PlanPro, RAMMB: 128, VCPU: 1, MaxConcurrency: 5, NodeID: source.Permit.NodeID}
	if err := l.recoverResident(request); err != nil {
		t.Fatal(err)
	}
	l.Release(source.InstanceKey)
	if !l.releaseOriginalRegionalRuntime(source) || l.Concurrency(app.ID) != 0 {
		t.Fatal("unknown resource stop lost its retained seat")
	}
}
