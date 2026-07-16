package sched

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeVMM is a sched.VMM that records calls and stands in for firecracker. It is
// shared by engine_test and loop_test (both package sched).
type fakeVMM struct {
	mu                sync.Mutex
	coldBoots         int
	restores          int
	snapshots         int
	destroys          int
	forceColdFallback bool // CreateFromSnapshot reports a cold-boot fallback (ADR-005)
	wakeErr           error
	snapErr           error
	destroyErr        error
}

func (f *fakeVMM) outcome(instance string, method vmmdpb.WakeMethod, requested vmmdpb.WakeMethod) *WakeOutcome {
	return &WakeOutcome{
		Instance: instance, LeaseUID: 20001, HostIP: "10.100.0.2",
		Netns: "fc-" + instance, VethHost: "vh1", VethPeer: "vp1",
		Method: method, RequestedMethod: requested,
	}
}

func (f *fakeVMM) CreateColdBoot(_ context.Context, instance string, _ AppSpec) (*WakeOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wakeErr != nil {
		return nil, f.wakeErr
	}
	f.coldBoots++
	return f.outcome(instance, vmmdpb.WakeMethod_WAKE_COLD_BOOT, vmmdpb.WakeMethod_WAKE_COLD_BOOT), nil
}

func (f *fakeVMM) CreateFromSnapshot(_ context.Context, instance string, _ AppSpec, _ SnapshotRef) (*WakeOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wakeErr != nil {
		return nil, f.wakeErr
	}
	f.restores++
	method := vmmdpb.WakeMethod_WAKE_RESTORE
	if f.forceColdFallback {
		method = vmmdpb.WakeMethod_WAKE_COLD_BOOT
	}
	return f.outcome(instance, method, vmmdpb.WakeMethod_WAKE_RESTORE), nil
}

func (f *fakeVMM) PauseAndSnapshot(_ context.Context, _, _, _ string) (SnapshotBytes, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snapErr != nil {
		return SnapshotBytes{}, f.snapErr
	}
	f.snapshots++
	return SnapshotBytes{MemBytes: 130 * 1024 * 1024, VMStateBytes: 4096}, nil
}

func (f *fakeVMM) Destroy(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.destroyErr != nil {
		return f.destroyErr
	}
	f.destroys++
	return nil
}

// fakeNotifier records emitted pg_notify events.
type fakeNotifier struct {
	mu     sync.Mutex
	events []notifyEvent
}

type notifyEvent struct{ channel, payload string }

func (n *fakeNotifier) Notify(_ context.Context, channel, payload string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, notifyEvent{channel, payload})
	return nil
}

func (n *fakeNotifier) count(channel string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, e := range n.events {
		if e.channel == channel {
			c++
		}
	}
	return c
}

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// seedApp builds an account + app + live deployment in a MemStore and returns
// them. A snapshot is added only when withSnapshot is set.
func seedApp(t *testing.T, store state.Store, plan api.Plan, ramMB, maxConc int) (state.Account, state.App, state.Deployment) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "u@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "app-" + string(plan), RAMMB: ramMB,
		MaxConcurrency: maxConc, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return acct, app, dep
}

func newEngine(store state.Store, vmm VMM, notif Notifier, fcVer string) *Engine {
	return NewEngine(store, NewLedger(), vmm, notif, fcVer, testLog())
}

func TestEngineWake_ColdBoot(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(store, vmm, notif, "1.10.0")

	res, err := e.Wake(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if res.Addr != "10.100.0.2:8080" {
		t.Errorf("addr = %q, want 10.100.0.2:8080", res.Addr)
	}
	if vmm.coldBoots != 1 || vmm.restores != 0 {
		t.Errorf("coldBoots=%d restores=%d, want 1/0", vmm.coldBoots, vmm.restores)
	}
	ins, err := store.RunningInstanceForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("RunningInstanceForApp: %v", err)
	}
	if ins.State != string(state.StateRunning) || ins.HostIP != "10.100.0.2" {
		t.Errorf("instance = %+v", ins)
	}
	// Resident RAM = ram + overhead (still reserved while running).
	if got := e.Ledger().ResidentRAM(); got != 512+api.PerVMOverheadMB {
		t.Errorf("resident = %d, want %d", got, 512+api.PerVMOverheadMB)
	}
}

func TestEngineWake_Idempotent(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	first, err := e.Wake(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Wake #1: %v", err)
	}
	second, err := e.Wake(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Wake #2: %v", err)
	}
	if first.InstanceID != second.InstanceID {
		t.Errorf("idempotent wake returned a new instance: %q vs %q", first.InstanceID, second.InstanceID)
	}
	if vmm.coldBoots != 1 {
		t.Errorf("coldBoots = %d, want 1 (second wake must reuse)", vmm.coldBoots)
	}
}

func TestEngineWake_RestoreFromSnapshot(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	// A fresh, version-matched snapshot makes wake a restore.
	if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0", Path: "/srv/fc/snap/x", MemBytes: 1,
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	vmm := &fakeVMM{}
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := e.Wake(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if res.Method != vmmdpb.WakeMethod_WAKE_RESTORE {
		t.Errorf("method = %v, want WAKE_RESTORE", res.Method)
	}
	if vmm.restores != 1 || vmm.coldBoots != 0 {
		t.Errorf("restores=%d coldBoots=%d, want 1/0", vmm.restores, vmm.coldBoots)
	}
}

func TestEngineWake_StaleFcVersionColdBoots(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	// Snapshot made by an older FC; must not be restored (ADR-005 pinning).
	if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.7.0", Path: "/srv/fc/snap/x", MemBytes: 1,
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	vmm := &fakeVMM{}
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	if _, err := e.Wake(context.Background(), app.ID); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if vmm.coldBoots != 1 || vmm.restores != 0 {
		t.Errorf("coldBoots=%d restores=%d, want 1/0 (version mismatch => cold boot)", vmm.coldBoots, vmm.restores)
	}
}

func TestEngineWake_RestoreFallbackMarksSnapshotStale(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	snap, _ := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0", Path: "/srv/fc/snap/x", MemBytes: 1,
	})
	vmm := &fakeVMM{forceColdFallback: true}
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := e.Wake(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	// vmmd fell back; the reported method is cold boot.
	if res.Method != vmmdpb.WakeMethod_WAKE_COLD_BOOT {
		t.Errorf("method = %v, want WAKE_COLD_BOOT (fallback)", res.Method)
	}
	// The bad snapshot must now be stale so the next wake doesn't retry it.
	if _, err := store.LatestSnapshot(context.Background(), dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("snapshot should be stale (no non-stale snapshot left); got err=%v", err)
	}
	_ = snap
}

func TestEngineWake_AdmissionDeniedReturnsProblem(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanFree, 128, 1)
	vmm := &fakeVMM{}
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	// Fill the ledger to the ceiling so the wake is refused for capacity.
	e.Ledger().residentRAM = api.RAMAdmissionCeilingMB

	_, err := e.Wake(context.Background(), app.ID)
	if err == nil {
		t.Fatal("expected capacity denial")
	}
	var p *api.Problem
	if !errors.As(err, &p) || p.Code != api.CodeCapacity {
		t.Fatalf("error = %v, want *api.Problem capacity", err)
	}
	if vmm.coldBoots != 0 {
		t.Errorf("no boot should happen on denial; coldBoots=%d", vmm.coldBoots)
	}
	// The instance row should have been transitioned to failed, not left waking.
	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	if len(rows) != 1 || rows[0].State != string(state.StateFailed) {
		t.Errorf("rows = %+v, want one failed row", rows)
	}
}

func TestEngineWake_BootErrorFails(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{wakeErr: errors.New("firecracker boom")}
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	if _, err := e.Wake(context.Background(), app.ID); err == nil {
		t.Fatal("expected boot error")
	}
	// Ledger must be released (no leak) and the instance marked failed.
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 (reservation released on failure)", got)
	}
	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	if len(rows) != 1 || rows[0].State != string(state.StateFailed) {
		t.Errorf("rows = %+v, want one failed row", rows)
	}
}

func TestEnginePrime_BootsSnapshotsParks(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 2)
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(store, vmm, notif, "1.10.0")

	if err := e.Prime(context.Background(), app.ID, dep.ID); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if vmm.coldBoots != 1 || vmm.snapshots != 1 {
		t.Errorf("coldBoots=%d snapshots=%d, want 1/1", vmm.coldBoots, vmm.snapshots)
	}
	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	if len(rows) != 1 || rows[0].State != string(state.StateParked) {
		t.Fatalf("rows = %+v, want one parked row", rows)
	}
	// A parked app consumes zero resident RAM (invariant §6.2-4).
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 after park", got)
	}
	// snapshot_written must be emitted so imaged records the row.
	if notif.count("snapshot_written") != 1 {
		t.Errorf("snapshot_written emitted %d times, want 1", notif.count("snapshot_written"))
	}
}

func TestEnginePark_SnapshotFailureStops(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := e.Wake(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	vmm.snapErr = errors.New("disk full")
	if err := e.Park(context.Background(), res.InstanceID); err == nil {
		t.Fatal("expected snapshot failure")
	}
	ins, _ := store.InstanceByID(context.Background(), res.InstanceID)
	if ins.State != string(state.StateStopped) {
		t.Errorf("state = %q, want stopped (snapshot failed => cold boot next)", ins.State)
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 (RAM freed even on snapshot failure)", got)
	}
}

func TestEngineEvict_Destroys(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := e.Wake(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if err := e.Evict(context.Background(), res.InstanceID); err != nil {
		t.Fatalf("Evict: %v", err)
	}
	if vmm.destroys != 1 {
		t.Errorf("destroys = %d, want 1", vmm.destroys)
	}
	ins, _ := store.InstanceByID(context.Background(), res.InstanceID)
	if ins.State != string(state.StateStopped) {
		t.Errorf("state = %q, want stopped", ins.State)
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 after evict", got)
	}
}

func TestEngineReportActivity(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	e := newEngine(store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	res, _ := e.Wake(context.Background(), app.ID)

	now := time.Now()
	applied, err := e.ReportActivity(context.Background(), []state.InstanceTouch{
		{InstanceID: res.InstanceID, LastRequest: now},
		{InstanceID: "ghost", LastRequest: now},
	})
	if err != nil {
		t.Fatalf("ReportActivity: %v", err)
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1 (ghost dropped)", applied)
	}
	ins, _ := store.InstanceByID(context.Background(), res.InstanceID)
	if !ins.LastRequestAt.Equal(now) {
		t.Errorf("last_request_at = %v, want %v", ins.LastRequestAt, now)
	}
}

func TestEngineSeedLedger(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	// A running instance survived a schedd restart.
	ins, _ := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateRunning), 512)
	_ = ins

	e := newEngine(store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if err := e.SeedLedger(context.Background()); err != nil {
		t.Fatalf("SeedLedger: %v", err)
	}
	if got := e.Ledger().ResidentRAM(); got != 512+api.PerVMOverheadMB {
		t.Errorf("resident = %d, want %d (running instance re-accounted)", got, 512+api.PerVMOverheadMB)
	}
}
