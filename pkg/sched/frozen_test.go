// adr: 1151 — schedd freezes an idle function instance in place instead of
// parking it, books only its measured memory, thaws it for the next request
// and parks it after FrozenTTL or under memory pressure.
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type frozenFakeVMM struct {
	*fakeVMM
	freezes, thaws int
	freezeErr      error
	thawErr        error
}

func (f *frozenFakeVMM) FreezeInstance(context.Context, string, string, bool) (FreezeReport, error) {
	f.freezes++
	return FreezeReport{}, f.freezeErr
}

func (f *frozenFakeVMM) ThawInstance(context.Context, string, string, bool) (bool, error) {
	f.thaws++
	return false, f.thawErr
}

type frozenFixture struct {
	store *state.MemStore
	vmm   *frozenFakeVMM
	e     *Engine
	app   state.App
	dep   state.Deployment
	ins   state.Instance
}

// newFrozenFixture seeds a Pro function app with one RUNNING, published
// instance holding a wake reservation, and (when withSnapshot) a reusable
// init snapshot for its deployment.
func newFrozenFixture(t *testing.T, withSnapshot bool, appType ...state.AppType) *frozenFixture {
	t.Helper()
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "frozen@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	typ := state.AppTypeFunction
	if len(appType) > 0 {
		typ = appType[0]
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "frozen", Type: typ,
		RAMMB: 512, MaxConcurrency: 4, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:frozen", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if withSnapshot {
		if _, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10.0",
			StorageKey: "snap/" + dep.ID + "/captures/c1/v2/mem", MemBytes: 1 << 20}); err != nil {
			t.Fatal(err)
		}
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	ins, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateWaking), app.RAMMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	ins, err = store.PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{
		AccountID: app.AccountID, AppID: app.ID, InstanceID: ins.ID, NodeID: ins.NodeID, WakeID: ins.WakeID,
		ExpectedState: string(state.StateWaking), Fence: fence.SecretFence, ConfigFence: fence,
		Netns: "fc-" + ins.ID, HostIP: "10.100.0.2", GuestUID: 20001,
	})
	if err != nil {
		t.Fatal(err)
	}
	vmm := &frozenFakeVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd"))
	limits := api.MustLimitsFor(api.PlanPro)
	if err := e.Ledger().Admit(Request{
		Instance: ins.ID, AppID: app.ID, DeploymentID: dep.ID, Plan: api.PlanPro,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, CPUMillicores: 1000, MaxConcurrency: app.MaxConcurrency, NodeID: node.ID,
	}); err != nil {
		t.Fatal(err)
	}
	return &frozenFixture{store: store, vmm: vmm, e: e, app: app, dep: dep, ins: ins}
}

func (f *frozenFixture) state(t *testing.T) state.State {
	t.Helper()
	ins, err := f.store.InstanceByID(t.Context(), f.ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	return state.State(ins.State)
}

func TestLedgerFreezeBooksMeasuredChargeOnly(t *testing.T) {
	l := NewNodeLedger()
	if err := l.Admit(Request{Instance: "i1", AppID: "a1", DeploymentID: "d1", Plan: api.PlanScale,
		RAMMB: 1024, VCPU: 2, CPUMillicores: 1000, MaxConcurrency: 4, NodeID: "n1"}); err != nil {
		t.Fatal(err)
	}
	if !l.Freeze("i1", frozenChargeMB(1024, 160)) {
		t.Fatal("Freeze = false")
	}
	if got := l.ResidentRAMForNode("n1"); got != 200 {
		t.Fatalf("resident = %d MB, want the 200 MB measured charge (160 MB + 25%%)", got)
	}
	if l.UsedVCPUForNode("n1") != 0 || l.UsedCPUMillicoresForNode("n1") != 0 || l.Concurrency("a1") != 0 {
		t.Fatalf("frozen reservation kept vCPU %d / CPU %d / concurrency %d", l.UsedVCPUForNode("n1"), l.UsedCPUMillicoresForNode("n1"), l.Concurrency("a1"))
	}
	if l.FrozenCountForNode("n1") != 1 || !l.IsFrozen("i1") || l.Freeze("i1", 100) {
		t.Fatal("frozen reservation not recorded once")
	}
	l.Release("i1")
	if l.ResidentRAMForNode("n1") != 0 || l.FrozenCountForNode("n1") != 0 {
		t.Fatal("Release left frozen RAM booked")
	}
	// An unmeasured instance books the floor plus headroom.
	if got := frozenChargeMB(1024, 0); got != api.MeasuredChargeFloorMB*(100+api.MeasuredChargeHeadroomPercent)/100 {
		t.Fatalf("unmeasured charge = %d MB", got)
	}
}

func TestLedgerSeedsFrozenReservation(t *testing.T) {
	l := NewNodeLedger()
	if err := l.Admit(Request{Instance: "i1", AppID: "a1", Plan: api.PlanScale, RAMMB: 1024, VCPU: 2,
		CPUMillicores: 1000, MaxConcurrency: 4, NodeID: "n1", Kind: KindFrozen, FrozenChargeMB: api.MeasuredChargeFloorMB}); err != nil {
		t.Fatal(err)
	}
	if l.ResidentRAMForNode("n1") != api.MeasuredChargeFloorMB || l.UsedVCPUForNode("n1") != 0 || l.Concurrency("a1") != 0 || !l.IsFrozen("i1") {
		t.Fatal("seeded frozen reservation has the wrong shape")
	}
}

func TestFreezeIdleFreezesEligibleFunction(t *testing.T) {
	f := newFrozenFixture(t, true)
	if handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); err != nil || handled {
		t.Fatalf("FreezeIdle with the tier off = %v, %v; want false", handled, err)
	}
	f.e.EnableFrozenTier()
	handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 150)
	if err != nil || !handled {
		t.Fatalf("FreezeIdle = %v, %v; want frozen", handled, err)
	}
	if f.state(t) != state.StateFrozen || f.vmm.freezes != 1 || !f.e.Ledger().IsFrozen(f.ins.ID) {
		t.Fatalf("state %s, freezes %d, ledger frozen %v", f.state(t), f.vmm.freezes, f.e.Ledger().IsFrozen(f.ins.ID))
	}
	if f.e.Ledger().Concurrency(f.app.ID) != 0 {
		t.Fatal("a frozen instance still holds concurrency")
	}
}

func TestFreezeIdleParksWithoutSnapshot(t *testing.T) {
	f := newFrozenFixture(t, false)
	f.e.EnableFrozenTier()
	if handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); err != nil || handled {
		t.Fatalf("FreezeIdle without a snapshot = %v, %v; want park", handled, err)
	}
	if f.state(t) != state.StateRunning || f.vmm.freezes != 0 {
		t.Fatalf("state %s, freezes %d", f.state(t), f.vmm.freezes)
	}
}

func TestFreezeIdleRefusesServices(t *testing.T) {
	f := newFrozenFixture(t, true, "")
	f.e.EnableFrozenTier()
	if handled, _ := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); handled || f.vmm.freezes != 0 {
		t.Fatal("a service instance was frozen")
	}
}

func TestFreezeIdleBusyGuestStaysRunning(t *testing.T) {
	f := newFrozenFixture(t, true)
	f.e.EnableFrozenTier()
	f.vmm.freezeErr = api.NewProblem(409, api.CodeInstanceBusy, "busy", "a request is in flight")
	handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 150)
	if err != nil || !handled {
		t.Fatalf("FreezeIdle on a busy guest = %v, %v; want handled (keep running)", handled, err)
	}
	if f.state(t) != state.StateRunning || f.e.Ledger().IsFrozen(f.ins.ID) || f.e.Ledger().Concurrency(f.app.ID) != 1 {
		t.Fatalf("busy guest: state %s, ledger frozen %v", f.state(t), f.e.Ledger().IsFrozen(f.ins.ID))
	}
}

func TestFreezeIdleFailureFallsBackToPark(t *testing.T) {
	f := newFrozenFixture(t, true)
	f.e.EnableFrozenTier()
	f.vmm.freezeErr = errors.New("vmmd unavailable")
	if handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); err != nil || handled {
		t.Fatalf("FreezeIdle failure = %v, %v; want park", handled, err)
	}
	if f.state(t) != state.StateRunning {
		t.Fatalf("state after failed freeze = %s, want running", f.state(t))
	}
}

func TestWakeThawsFrozenInstance(t *testing.T) {
	f := newFrozenFixture(t, true)
	f.e.EnableFrozenTier()
	if handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); err != nil || !handled {
		t.Fatalf("FreezeIdle: %v, %v", handled, err)
	}
	result, err := f.e.Wake(t.Context(), f.app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if result.InstanceID != f.ins.ID || f.vmm.thaws != 1 || f.vmm.coldBoots != 0 || f.vmm.restores != 0 {
		t.Fatalf("result %+v thaws %d cold %d restore %d", result, f.vmm.thaws, f.vmm.coldBoots, f.vmm.restores)
	}
	if f.state(t) != state.StateRunning || f.e.Ledger().IsFrozen(f.ins.ID) || f.e.Ledger().Concurrency(f.app.ID) != 1 {
		t.Fatalf("after thaw: state %s, ledger frozen %v, concurrency %d", f.state(t), f.e.Ledger().IsFrozen(f.ins.ID), f.e.Ledger().Concurrency(f.app.ID))
	}
}

func TestFailedThawParksAndWakesFromSnapshot(t *testing.T) {
	f := newFrozenFixture(t, true)
	f.e.EnableFrozenTier()
	if handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); err != nil || !handled {
		t.Fatalf("FreezeIdle: %v, %v", handled, err)
	}
	f.vmm.thawErr = errors.New("instance is not live")
	result, err := f.e.Wake(t.Context(), f.app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if result.InstanceID == f.ins.ID || f.state(t) != state.StateParked {
		t.Fatalf("failed thaw: result %s, frozen row %s; want a new instance and a parked row", result.InstanceID, f.state(t))
	}
	if f.e.Ledger().ResidentFor(f.ins.ID) {
		t.Fatal("parked frozen instance kept a reservation")
	}
}

func TestParkFrozenAndEvict(t *testing.T) {
	f := newFrozenFixture(t, true)
	f.e.EnableFrozenTier()
	if handled, _ := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); !handled {
		t.Fatal("not frozen")
	}
	if err := f.e.Evict(t.Context(), f.ins.ID); err != nil {
		t.Fatalf("Evict: %v", err)
	}
	if f.state(t) != state.StateParked || f.e.Ledger().ResidentFor(f.ins.ID) {
		t.Fatalf("evicted frozen instance: state %s; want parked with no reservation", f.state(t))
	}
}

func TestFrozenExpired(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	idle := 30 * time.Second
	ttl := time.Duration(api.FrozenTTLSeconds) * time.Second
	in := InstanceInfo{State: state.StateFrozen, Started: now.Add(-time.Hour), LastRequest: now.Add(-idle - ttl + time.Second)}
	if frozenExpired(now, in, idle) {
		t.Fatal("expired before FrozenTTL")
	}
	in.LastRequest = now.Add(-idle - ttl - time.Second)
	if !frozenExpired(now, in, idle) {
		t.Fatal("not expired after FrozenTTL")
	}
}

func TestEvictionCandidatesTakeFrozenFirst(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	old := now.Add(-time.Hour)
	got := evictionCandidates(now, []InstanceInfo{
		{Instance: "running-old", State: state.StateRunning, Started: old, LastRequest: old},
		{Instance: "frozen-new", State: state.StateFrozen, Started: old, LastRequest: now.Add(-time.Minute)},
		{Instance: "frozen-old", State: state.StateFrozen, Started: old, LastRequest: old},
	})
	if len(got) != 3 || got[0].Instance != "frozen-old" || got[1].Instance != "frozen-new" || got[2].Instance != "running-old" {
		t.Fatalf("eviction order = %v, want frozen (LRU) before running", got)
	}
}

// Node recovery parks a frozen VM: it cannot be live-migrated (vmmd refuses
// to snapshot it) and its deployment keeps the snapshot.
func TestRecoveryParksFrozenInstances(t *testing.T) {
	a := NewArbiter(nil, nil)
	frozen := state.RecoveryInstance{State: string(state.StateFrozen), Kind: "wake"}
	for _, lifecycle := range []state.NodeLifecycle{state.NodeLifecycleDraining, state.NodeLifecycleForceDraining,
		state.NodeLifecycleRecovering, state.NodeLifecycleUnavailable} {
		if got := a.Decide(state.ComputeNode{Lifecycle: lifecycle}, frozen); got != DecisionRecreate {
			t.Fatalf("Decide(%s, frozen) = %v, want recreate (which parks)", lifecycle, got)
		}
	}
	if got := a.Decide(state.ComputeNode{Lifecycle: state.NodeLifecycleActive}, frozen); got != DecisionNone {
		t.Fatalf("Decide(active, frozen) = %v, want none", got)
	}
	f := newFrozenFixture(t, true)
	f.e.EnableFrozenTier()
	if handled, _ := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); !handled {
		t.Fatal("not frozen")
	}
	if err := f.e.RecreateInstance(t.Context(), f.ins.ID); err != nil {
		t.Fatalf("RecreateInstance: %v", err)
	}
	if f.state(t) != state.StateParked || f.e.Ledger().ResidentFor(f.ins.ID) {
		t.Fatalf("recovered frozen instance: state %s; want parked with no reservation", f.state(t))
	}
}

// A frozen VM on an unreachable node is parked without a thaw attempt or a
// destroy that would wait out its RPC timeout; vmmd's reaper removes the VM.
func TestFrozenOnUnreachableNodeIsParkedWithoutWaiting(t *testing.T) {
	f := newFrozenFixture(t, true)
	f.e.EnableFrozenTier()
	if handled, _ := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); !handled {
		t.Fatal("not frozen")
	}
	node, err := f.store.ComputeNodeByID(t.Context(), f.ins.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.NodeSetLifecycle(t.Context(), node.ID, node.Lifecycle, state.NodeLifecycleUnavailable); err != nil {
		t.Fatalf("NodeSetLifecycle: %v", err)
	}
	destroys := f.vmm.destroys
	_, _ = f.e.Wake(t.Context(), f.app.ID, "", "", "")
	if f.vmm.thaws != 0 || f.vmm.destroys != destroys {
		t.Fatalf("thaws %d, destroys %d→%d; want no RPC to an unreachable node", f.vmm.thaws, destroys, f.vmm.destroys)
	}
	if f.state(t) != state.StateParked {
		t.Fatalf("frozen row on an unreachable node: state %s, want parked", f.state(t))
	}
}

func TestThawSkippedWithoutFrozenReservation(t *testing.T) {
	l := NewNodeLedger()
	if l.FrozenCountForApp("a1") != 0 {
		t.Fatal("empty ledger reports frozen reservations")
	}
	if err := l.Admit(Request{Instance: "i1", AppID: "a1", Plan: api.PlanScale, RAMMB: 512, VCPU: 1,
		MaxConcurrency: 4, NodeID: "n1", Kind: KindFrozen, FrozenChargeMB: 100}); err != nil {
		t.Fatal(err)
	}
	if l.FrozenCountForApp("a1") != 1 || l.FrozenCountForApp("a2") != 0 {
		t.Fatal("FrozenCountForApp does not count per app")
	}
}
