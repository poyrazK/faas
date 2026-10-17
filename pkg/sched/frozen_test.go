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
	thawReqs       []ThawRequest
	// compresses counts reclaim requests for a frozen instance; they answer
	// compressReport and compressErr.
	compresses     int
	compressReport FreezeReport
	compressErr    error
	// skipAfterRestore answers like a vmmd that predates run_after_restore.
	skipAfterRestore bool
}

func (f *frozenFakeVMM) FreezeInstance(_ context.Context, _, _ string, reclaim bool) (FreezeReport, error) {
	if reclaim {
		f.compresses++
		return f.compressReport, f.compressErr
	}
	f.freezes++
	return FreezeReport{}, f.freezeErr
}

func (f *frozenFakeVMM) ThawInstance(_ context.Context, _, _ string, req ThawRequest) (ThawResult, error) {
	f.thaws++
	f.thawReqs = append(f.thawReqs, req)
	if f.thawErr != nil {
		return ThawResult{}, f.thawErr
	}
	return ThawResult{HealthcheckVerified: req.ImageHealthcheckRequired, AfterRestoreRan: req.RunAfterRestore && !f.skipAfterRestore}, nil
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
	return newFrozenFixtureWith(t, withSnapshot, func(app *state.App) {
		if len(appType) > 0 {
			app.Type = appType[0]
		}
	})
}

// newFrozenFixtureWith is newFrozenFixture with the app shaped by shape
// before it is created.
func newFrozenFixtureWith(t *testing.T, withSnapshot bool, shape func(*state.App)) *frozenFixture {
	t.Helper()
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "frozen@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	shaped := state.App{AccountID: acct.ID, Slug: "frozen", Type: state.AppTypeFunction,
		RAMMB: 512, MaxConcurrency: 4, IdleTimeoutS: 30}
	shape(&shaped)
	app, err := store.CreateApp(ctx, shaped)
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

// addPublishedInstance adds a second RUNNING, published instance of the
// fixture's deployment.
func (f *frozenFixture) addPublishedInstance(t *testing.T) state.Instance {
	t.Helper()
	ctx := t.Context()
	ins, err := f.store.CreateInstanceWithMode(ctx, f.app.ID, f.dep.ID, string(state.StateWaking), f.app.RAMMB, f.ins.NodeID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.store.RuntimeAppValuesForDeployment(ctx, f.app.AccountID, f.app.ID, f.dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	ins, err = f.store.PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{
		AccountID: f.app.AccountID, AppID: f.app.ID, InstanceID: ins.ID, NodeID: ins.NodeID, WakeID: ins.WakeID,
		ExpectedState: string(state.StateWaking), Fence: fence.SecretFence, ConfigFence: fence,
		Netns: "fc-" + ins.ID, HostIP: "10.100.0.3", GuestUID: 20002,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ins
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

// A thaw runs the app's after_restore callback when the app declares one,
// and a vmmd that does not confirm it ran is treated as a failed thaw: the
// VM parks and the request wakes from the snapshot, whose restore runs it.
func TestThawRunsAfterRestoreWhenDeclared(t *testing.T) {
	for _, tc := range []struct {
		name             string
		declared         bool
		skipAfterRestore bool
		wantThawed       bool
	}{
		{name: "no hook", wantThawed: true},
		{name: "hook declared", declared: true, wantThawed: true},
		{name: "vmmd predates run_after_restore", declared: true, skipAfterRestore: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFrozenFixtureWith(t, true, func(app *state.App) {
				if tc.declared {
					app.Manifest.AfterRestore = &api.AfterRestoreHook{Path: "/after-restore"}
				}
			})
			f.e.EnableFrozenTier()
			f.vmm.skipAfterRestore = tc.skipAfterRestore
			if handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); err != nil || !handled {
				t.Fatalf("FreezeIdle: %v, %v", handled, err)
			}
			result, err := f.e.Wake(t.Context(), f.app.ID, "", "", "")
			if err != nil {
				t.Fatalf("Wake: %v", err)
			}
			if len(f.vmm.thawReqs) != 1 || f.vmm.thawReqs[0].RunAfterRestore != tc.declared {
				t.Fatalf("thaw requests %+v, want one with RunAfterRestore=%v", f.vmm.thawReqs, tc.declared)
			}
			if thawed := result.InstanceID == f.ins.ID; thawed != tc.wantThawed {
				t.Fatalf("woke %s (frozen %s, now %s); want thawed=%v", result.InstanceID, f.ins.ID, f.state(t), tc.wantThawed)
			}
			if !tc.wantThawed && f.state(t) != state.StateParked {
				t.Fatalf("unconfirmed after_restore left the frozen row %s, want parked", f.state(t))
			}
		})
	}
}

// The pause an app declares and the apps switch decide which tier may
// pause it: quiet pause only for between_requests, the frozen tier for
// anything but never, and apps other than functions only with the switch.
func TestPauseTierEligibility(t *testing.T) {
	normal := string(state.InstanceModeNormal)
	for _, tc := range []struct {
		name       string
		app        state.App
		mode, net  string
		appsSwitch bool
		wantQuiet  bool
		wantFrozen bool
	}{
		{name: "function default", app: state.App{Type: state.AppTypeFunction}, mode: normal, wantQuiet: true, wantFrozen: true},
		{name: "function never", app: state.App{Type: state.AppTypeFunction, Manifest: state.AppManifest{Pause: api.PauseNever}}, mode: normal},
		{name: "function when_idle", app: state.App{Type: state.AppTypeFunction, Manifest: state.AppManifest{Pause: api.PauseWhenIdle}}, mode: normal, wantFrozen: true},
		{name: "app default, switch off", app: state.App{Type: state.AppTypeApp}, mode: normal},
		{name: "app default, switch on", app: state.App{Type: state.AppTypeApp}, mode: normal, appsSwitch: true, wantFrozen: true},
		{name: "app between_requests", app: state.App{Type: state.AppTypeApp, Manifest: state.AppManifest{Pause: api.PauseBetweenRequests}}, mode: normal, appsSwitch: true, wantQuiet: true, wantFrozen: true},
		{name: "app never", app: state.App{Type: state.AppTypeApp, Manifest: state.AppManifest{Pause: api.PauseNever}}, mode: normal, appsSwitch: true},
		{name: "grpc app", app: state.App{Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassGRPC}, mode: normal, appsSwitch: true, wantFrozen: true},
		{name: "worker class", app: state.App{Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker}, mode: normal, appsSwitch: true},
		{name: "service mode", app: state.App{Type: state.AppTypeApp}, mode: string(state.InstanceModeService), appsSwitch: true},
		{name: "mirror", app: state.App{Type: state.AppTypeFunction}, mode: string(state.InstanceModeMirror)},
		{name: "private network", app: state.App{Type: state.AppTypeFunction}, mode: normal, net: "net-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var e Engine
			if tc.appsSwitch {
				e.EnableFrozenTierApps()
			}
			if got := quietPauseAfterMS(tc.app, tc.mode, tc.net) > 0; got != tc.wantQuiet {
				t.Errorf("quiet pause = %v, want %v", got, tc.wantQuiet)
			}
			if got := e.frozenTierAllows(tc.app, tc.mode, tc.net); got != tc.wantFrozen {
				t.Errorf("frozen tier = %v, want %v", got, tc.wantFrozen)
			}
		})
	}
}

func TestFreezeIdleFreezesAppsWithTheAppsSwitch(t *testing.T) {
	f := newFrozenFixture(t, true, state.AppTypeApp)
	f.e.EnableFrozenTier()
	if handled, _ := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); handled || f.vmm.freezes != 0 {
		t.Fatal("an app froze without the apps switch")
	}
	f.e.EnableFrozenTierApps()
	if handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 150); err != nil || !handled || f.state(t) != state.StateFrozen {
		t.Fatalf("FreezeIdle on an app with the switch = %v, %v, state %s; want frozen", handled, err, f.state(t))
	}
}

// A session pinned to a frozen VM thaws that VM even when another frozen VM
// of the deployment would come first.
func TestFrozenCandidatesPreferTheSessionInstance(t *testing.T) {
	f := newFrozenFixture(t, true)
	other := f.addPublishedInstance(t)
	for _, id := range []string{f.ins.ID, other.ID} {
		if err := updateInstanceStateCAS(t.Context(), f.store, id, string(state.StateRunning), string(state.StateFrozen)); err != nil {
			t.Fatal(err)
		}
	}
	plain, err := f.e.frozenCandidates(t.Context(), f.app, f.dep)
	if err != nil || len(plain) != 2 {
		t.Fatalf("candidates = %v, %v", plain, err)
	}
	last := plain[1].ID
	hinted, err := f.e.frozenCandidates(WithPreferredInstance(t.Context(), last), f.app, f.dep)
	if err != nil || len(hinted) != 2 || hinted[0].ID != last || hinted[1].ID != plain[0].ID {
		t.Fatalf("hinted candidates = %v, %v; want %s first", hinted, err, last)
	}
	if got, _ := f.e.frozenCandidates(WithPreferredInstance(t.Context(), "elsewhere"), f.app, f.dep); got[0].ID != plain[0].ID {
		t.Fatal("a hint naming no candidate reordered them")
	}
}

func TestLedgerShrinkFrozenCharge(t *testing.T) {
	l := NewNodeLedger()
	if err := l.Admit(Request{Instance: "i1", AppID: "a1", Plan: api.PlanScale, RAMMB: 1024, VCPU: 2,
		CPUMillicores: 1000, MaxConcurrency: 4, NodeID: "n1"}); err != nil {
		t.Fatal(err)
	}
	if l.ShrinkFrozenCharge("i1", 100) {
		t.Fatal("a running reservation was rebooked as frozen")
	}
	l.Freeze("i1", 500)
	if !l.ShrinkFrozenCharge("i1", 120) || l.ResidentRAMForNode("n1") != 120 {
		t.Fatalf("after compression resident = %d MB, want 120", l.ResidentRAMForNode("n1"))
	}
	if !l.ShrinkFrozenCharge("i1", 300) || l.ResidentRAMForNode("n1") != 120 {
		t.Fatal("a compression grew the frozen charge")
	}
	if !l.ShrinkFrozenCharge("i1", 1) || l.ResidentRAMForNode("n1") != api.MeasuredChargeFloorMB {
		t.Fatalf("frozen charge fell below the floor: %d MB", l.ResidentRAMForNode("n1"))
	}
}

// Memory pressure compresses a frozen VM into memory-only swap before it
// parks it (ADR-906 decision 6): a VM that compression shrinks by half stays
// frozen and books the smaller footprint; anything else parks as before.
func TestEvictUnderPressureCompressesFrozenInstances(t *testing.T) {
	for _, tc := range []struct {
		name      string
		report    FreezeReport
		err       error
		wantState state.State
	}{
		{name: "compressed", report: FreezeReport{ResidentBytesBefore: 400 << 20, ResidentBytesAfter: 80 << 20}, wantState: state.StateFrozen},
		{name: "barely shrinks", report: FreezeReport{ResidentBytesBefore: 400 << 20, ResidentBytesAfter: 380 << 20}, wantState: state.StateParked},
		{name: "no memory-only swap", err: api.NewProblem(409, api.CodeFrozenCompressUnavailable, "Compression unavailable", "no zswap"), wantState: state.StateParked},
		{name: "vmmd error", err: errors.New("vmmd unavailable"), wantState: state.StateParked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFrozenFixture(t, true)
			f.e.EnableFrozenTier()
			if handled, err := f.e.FreezeIdle(t.Context(), f.ins.ID, 400); err != nil || !handled {
				t.Fatalf("FreezeIdle: %v, %v", handled, err)
			}
			booked := f.e.Ledger().ResidentRAMForNode(f.ins.NodeID)
			f.vmm.compressReport, f.vmm.compressErr = tc.report, tc.err
			if err := f.e.EvictUnderPressure(t.Context(), f.ins.ID); err != nil {
				t.Fatal(err)
			}
			if f.vmm.compresses != 1 || f.state(t) != tc.wantState {
				t.Fatalf("compresses %d, state %s; want one compression and %s", f.vmm.compresses, f.state(t), tc.wantState)
			}
			if tc.wantState == state.StateFrozen {
				if now := f.e.Ledger().ResidentRAMForNode(f.ins.NodeID); now >= booked || !f.e.Ledger().IsFrozen(f.ins.ID) {
					t.Fatalf("booked %d MB after compression, %d before; want the smaller footprint", now, booked)
				}
			}
		})
	}
}

func TestEvictUnderPressureEvictsRunningInstances(t *testing.T) {
	f := newFrozenFixture(t, true)
	if err := f.e.EvictUnderPressure(t.Context(), f.ins.ID); err != nil {
		t.Fatal(err)
	}
	if f.vmm.compresses != 0 || f.state(t) != state.StateStopped {
		t.Fatalf("compresses %d, state %s; want an ordinary eviction", f.vmm.compresses, f.state(t))
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
