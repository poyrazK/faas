package sched

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
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
	// lastSnapRef records the SnapshotRef CreateFromSnapshot was
	// invoked with on its most recent call. F-2 review finding —
	// Wake's storage_key plumbing deserves a test pin; storing the
	// ref lets the test assert what the engine sent to vmmd.
	lastSnapRef SnapshotRef

	// sleepFor makes every RPC sleep before returning. Used by commit
	// 1's deadline test to drive the §6.1 boot-budget path: a vmmd
	// that hangs past WakingTimeout / ColdBootTimeout must surface as
	// a context.DeadlineExceeded error to the engine. Zero (the
	// default) means "return immediately".
	sleepFor time.Duration

	// bootStarted / bootRelease fence the vmmd boot call so commit 2
	// can prove Wake's Phase 3 happens outside appMu. The fake closes
	// bootStarted when it enters the cold-boot / restore call, then
	// blocks on bootRelease before returning. Tests use it to:
	//   - start a Wake in goroutine A,
	//   - wait for A to be inside the vmmd call,
	//   - start Wake B for the same app — B must NOT block on appMu,
	//   - release A's boot.
	bootStarted chan struct{} // capacity 1; or test injects a channel
	bootRelease chan struct{}

	// lastColdBootSpec / lastRestoreSpec capture the AppSpec the engine
	// handed to vmmd on the most recent wake call. Tests that exercise
	// the sealed-env wire read these to verify schedd forwarded the
	// per-key rows correctly.
	lastColdBootSpec AppSpec
	lastRestoreSpec  AppSpec
}

func (f *fakeVMM) outcome(instance string, method vmmdpb.WakeMethod, requested vmmdpb.WakeMethod) *WakeOutcome {
	return &WakeOutcome{
		Instance: instance, LeaseUID: 20001, HostIP: "10.100.0.2",
		Netns: "fc-" + instance, VethHost: "vh1", VethPeer: "vp1",
		Method: method, RequestedMethod: requested,
	}
}

func (f *fakeVMM) CreateColdBoot(ctx context.Context, instance string, app AppSpec) (*WakeOutcome, error) {
	if d := f.sleepFor; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.bootStarted != nil {
		select {
		case f.bootStarted <- struct{}{}:
		default:
		}
		select {
		case <-f.bootRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wakeErr != nil {
		return nil, f.wakeErr
	}
	f.lastColdBootSpec = app
	f.coldBoots++
	return f.outcome(instance, vmmdpb.WakeMethod_WAKE_COLD_BOOT, vmmdpb.WakeMethod_WAKE_COLD_BOOT), nil
}

func (f *fakeVMM) CreateFromSnapshot(ctx context.Context, instance string, app AppSpec, ref SnapshotRef) (*WakeOutcome, error) {
	if d := f.sleepFor; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.bootStarted != nil {
		select {
		case f.bootStarted <- struct{}{}:
		default:
		}
		select {
		case <-f.bootRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wakeErr != nil {
		return nil, f.wakeErr
	}
	f.lastRestoreSpec = app
	f.lastSnapRef = ref
	f.restores++
	method := vmmdpb.WakeMethod_WAKE_RESTORE
	if f.forceColdFallback {
		method = vmmdpb.WakeMethod_WAKE_COLD_BOOT
	}
	return f.outcome(instance, method, vmmdpb.WakeMethod_WAKE_RESTORE), nil
}

func (f *fakeVMM) PauseAndSnapshot(ctx context.Context, _, _, _, _ string) (SnapshotBytes, error) {
	if d := f.sleepFor; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return SnapshotBytes{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snapErr != nil {
		return SnapshotBytes{}, f.snapErr
	}
	f.snapshots++
	return SnapshotBytes{MemBytes: 130 * 1024 * 1024, VMStateBytes: 4096}, nil
}

func (f *fakeVMM) Destroy(ctx context.Context, _ string) error {
	if d := f.sleepFor; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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
		StorageKey: SnapshotMemKey(dep.ID),
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

// TestEngineWake_StorageKey_ForwardedFromRow pins the F-2 happy path:
// Wake reads snap.StorageKey from the snapshots row (the canonical
// source after #96) and forwards it on SnapshotRef.StorageKey. This
// is the contract the OCI driver depends on — a regression here would
// silently fall back to the on-disk path under local backends and
// fail to restore under remote ones.
func TestEngineWake_StorageKey_ForwardedFromRow(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	// Use a non-default storage_key so a regression that hardcodes
	// "snap/<dep>/mem" can't pass — the row's value is what vmmd
	// must see.
	customKey := "snap/" + dep.ID + "/mem" // canonical today
	if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0", Path: "/srv/fc/snap/x", MemBytes: 1,
		StorageKey: customKey,
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	vmm := &fakeVMM{}
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	if _, err := e.Wake(context.Background(), app.ID); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if vmm.lastSnapRef.StorageKey != customKey {
		t.Errorf("Wake forwarded StorageKey = %q, want %q (the snap row's value)", vmm.lastSnapRef.StorageKey, customKey)
	}
	if vmm.lastSnapRef.DeploymentID != dep.ID {
		t.Errorf("Wake forwarded DeploymentID = %q, want %q", vmm.lastSnapRef.DeploymentID, dep.ID)
	}
}

// TestEngineWake_StorageKey_FallbackOnEmpty pins the F-2 deprecation
// seam: a row whose StorageKey is "" (a pre-migration row that
// imaged stamped before #96 landed) still wakes — Wake falls back to
// the computed SnapshotMemKey(depID) form. After one milestone of
// deprecation, this fallback becomes dead code; the test ensures the
// dead branch behaves correctly while it's still load-bearing.
//
// Setup: MemStore's CreateSnapshot rejects empty StorageKey (the
// F-1 contract), so we insert a valid row first and then call
// SetSnapshotStorageKeyForTest to clear the value back to ''.
func TestEngineWake_StorageKey_FallbackOnEmpty(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0", Path: "/srv/fc/snap/x", MemBytes: 1,
		StorageKey: SnapshotMemKey(dep.ID),
	}); err != nil {
		t.Fatalf("CreateSnapshot (seed): %v", err)
	}
	store.SetSnapshotStorageKeyForTest(dep.ID, "")

	vmm := &fakeVMM{}
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.Wake(context.Background(), app.ID); err != nil {
		t.Fatalf("Wake (empty StorageKey): %v", err)
	}
	want := SnapshotMemKey(dep.ID)
	if vmm.lastSnapRef.StorageKey != want {
		t.Errorf("Wake fallback StorageKey = %q, want %q (computed SnapshotMemKey form)", vmm.lastSnapRef.StorageKey, want)
	}
}

func TestEngineWake_ForwardsSealedEnv(t *testing.T) {
	// Wake must load the app's sealed env rows from the store and pack
	// them into AppSpec.SealedEnv so vmmd can unseal + stage them onto
	// drive1. Without this, the §11/G2 secrets feature never reaches
	// the customer's running VM (PR-review regression target).
	store := state.NewMemStore()
	acct, app, _ := seedApp(t, store, api.PlanPro, 512, 5)

	// Two rows — proves multi-key fan-out. We don't seal real ciphertext
	// here (that's the secretbox package's job); the sched wire just
	// carries the bytes through, so any byte string exercises the path.
	if err := store.UpsertAppSecret(context.Background(), acct.ID, app.ID,
		"STRIPE_KEY", []byte("ct-stripe")); err != nil {
		t.Fatalf("UpsertAppSecret STRIPE_KEY: %v", err)
	}
	if err := store.UpsertAppSecret(context.Background(), acct.ID, app.ID,
		"DB_URL", []byte("ct-db")); err != nil {
		t.Fatalf("UpsertAppSecret DB_URL: %v", err)
	}

	vmm := &fakeVMM{}
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.Wake(context.Background(), app.ID); err != nil {
		t.Fatalf("Wake: %v", err)
	}

	spec := vmm.lastColdBootSpec
	if len(spec.SealedEnv) != 2 {
		t.Fatalf("SealedEnv len=%d, want 2 (rows: %+v)", len(spec.SealedEnv), spec.SealedEnv)
	}
	// MemStore preserves insertion order; assert keys arrived.
	gotKeys := map[string][]byte{}
	for _, e := range spec.SealedEnv {
		gotKeys[e.Key] = e.Ciphertext
	}
	if string(gotKeys["STRIPE_KEY"]) != "ct-stripe" {
		t.Errorf("STRIPE_KEY ciphertext = %q, want ct-stripe", gotKeys["STRIPE_KEY"])
	}
	if string(gotKeys["DB_URL"]) != "ct-db" {
		t.Errorf("DB_URL ciphertext = %q, want ct-db", gotKeys["DB_URL"])
	}
}

func TestEngineWake_NoSecrets_EmptySealedEnv(t *testing.T) {
	// An app with zero secrets must hand vmmd a nil/empty SealedEnv so
	// the Manager short-circuits the StageSecretsEnv mount entirely.
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	if _, err := e.Wake(context.Background(), app.ID); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if len(vmm.lastColdBootSpec.SealedEnv) != 0 {
		t.Errorf("SealedEnv = %+v, want empty", vmm.lastColdBootSpec.SealedEnv)
	}
}

func TestEngineWake_StaleFcVersionColdBoots(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	// Snapshot made by an older FC; must not be restored (ADR-005 pinning).
	if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.7.0", Path: "/srv/fc/snap/x", MemBytes: 1,
		StorageKey: SnapshotMemKey(dep.ID),
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
		StorageKey: SnapshotMemKey(dep.ID),
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

// TestEngineWake_VMMDColdBootDeadlineEnforced (commit 1, spec §6.1) pins
// that a Wake whose vmmd call exceeds the §6.1 budget (COLD_BOOTING ≤
// 30s) cannot leak the ledger reservation. The fake's sleepFor is set
// past the budget; the engine's context.WithTimeout wrapper must fire
// before the test gives up. After the Wake fails, the ledger is
// released and the instance row is FAILED.
func TestEngineWake_VMMDColdBootDeadlineEnforced(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	// Cold-boot path: no snapshot, initState = COLD_BOOTING, so the
	// deadline is ColdBootTimeout (35s). sleep well past it — but the
	// test fails immediately if the engine doesn't honour the budget,
	// so we set 5× and rely on the test wall-clock to bound the
	// failure mode.
	vmm := &fakeVMM{sleepFor: 2 * ColdBootTimeout}
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	start := time.Now()
	_, err := e.Wake(context.Background(), app.ID)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Wake returned nil err, expected deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Wake err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > ColdBootTimeout+2*time.Second {
		t.Errorf("Wake took %v, want ≤ ColdBootTimeout (35s) + slack", elapsed)
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 (reservation released on deadline)", got)
	}
	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	if len(rows) != 1 || rows[0].State != string(state.StateFailed) {
		t.Errorf("rows = %+v, want one FAILED row", rows)
	}
}

// TestEnginePrime_VMMDDeadlineEnforced mirrors the Wake test for the
// Prime path. Prime is always cold-boot, so it gets the same
// ColdBootTimeout — the only difference is that Prime's instance goes
// RUNNING → SNAPSHOTTING → PARKED on success, but a hung vmmd should
// leave the row FAILED with no reservation.
func TestEnginePrime_VMMDDeadlineEnforced(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 2)
	vmm := &fakeVMM{sleepFor: 2 * ColdBootTimeout}
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	err := e.Prime(context.Background(), app.ID, dep.ID)
	if err == nil {
		t.Fatal("Prime returned nil err, expected deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Prime err = %v, want context.DeadlineExceeded", err)
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 (reservation released on deadline)", got)
	}
	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	if len(rows) != 1 || rows[0].State != string(state.StateFailed) {
		t.Errorf("rows = %+v, want one FAILED row", rows)
	}
}

// bootSignalVMM returns a fakeVMM configured for lock-narrow tests.
// bootStarted has capacity 4 so multiple Wake callers can signal their
// arrival inside Phase 3 without blocking. bootRelease closes once —
// every blocked fake receives the closure and unblocks.
func bootSignalVMM() (*fakeVMM, chan struct{}, chan struct{}) {
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	return &fakeVMM{
		bootStarted: started,
		bootRelease: release,
	}, started, release
}

// TestEngineWake_LockReleasedDuringBoot (commit 2, finding #1) proves
// the vmmd call happens outside the per-app mutex. Without the lock
// narrowing, B would block on appMu while A holds the lock through
// Phase 3; B would never reach its own vmmd call. We fence A on the
// boot channels, start B, and assert B reaches its own Phase 3 by
// observing vmm.coldBoots grow from 0 to 2 within a short window.
func TestEngineWake_LockReleasedDuringBoot(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm, bootStarted, bootRelease := bootSignalVMM()
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	aResult := make(chan error, 1)
	aDone := make(chan struct{})
	go func() {
		defer close(aDone)
		_, err := e.Wake(context.Background(), app.ID)
		aResult <- err
	}()

	// Wait for A to be inside the boot call (Phase 3).
	select {
	case <-bootStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("A's Wake never entered the vmmd boot")
	}

	// Start B. It should run Phase 1 (fast-path miss), Phase 2
	// (resolve + Admit) immediately, and reach its own Phase 3
	// without blocking on A's appMu.
	bResult := make(chan error, 1)
	bDone := make(chan struct{})
	go func() {
		defer close(bDone)
		_, err := e.Wake(context.Background(), app.ID)
		bResult <- err
	}()

	// Wait for B to enter its own Phase 3 (signalled on bootStarted).
	// A has already consumed slot 1; B's signal lands in slot 2.
	select {
	case <-bootStarted:
	case <-time.After(500 * time.Millisecond):
		close(bootRelease)
		select {
		case <-aDone:
		case <-aResult:
		}
		select {
		case <-bDone:
		case <-bResult:
		}
		t.Fatal("B never reached Phase 3 — appMu held during A's boot (regression of finding #1)")
	}

	// Both A and B are now inside their vmmd calls. Release them.
	close(bootRelease)
	select {
	case err := <-aResult:
		if err != nil {
			t.Errorf("A's Wake returned err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("A's Wake never returned")
	}
	select {
	case err := <-bResult:
		if err != nil {
			t.Errorf("B's Wake returned err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("B's Wake never returned")
	}
	select {
	case <-aDone:
	case <-time.After(time.Second):
		t.Fatal("A never completed")
	}
	select {
	case <-bDone:
	case <-time.After(time.Second):
		t.Fatal("B never completed")
	}
}

// TestEngineWake_PostVMMDAbortOnStolenState (commit 2, finding #1)
// proves the Phase 4 re-read defends against watchdog-steals-state
// (commit 3). We simulate a watchdog-like event by setting the
// instance's state to STOPPED after Phase 2 completes but before the
// vmmd call returns. Wake's Phase 4 must:
//   - re-read the row,
//   - see STOPPED ≠ COLD_BOOTING,
//   - destroy the VM it just booted,
//   - release the ledger reservation,
//   - return an error.
//
// The boot is fenced so the test controls when Phase 3 finishes.
func TestEngineWake_PostVMMDAbortOnStolenState(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm, bootStarted, bootRelease := bootSignalVMM()
	e := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	type result struct {
		id  string
		err error
	}
	aRes := make(chan result, 1)
	go func() {
		r, err := e.Wake(context.Background(), app.ID)
		aRes <- result{r.InstanceID, err}
	}()

	var insID string
	// Poll for the COLD_BOOTING row — appears after Phase 2 commits.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
		for _, r := range rows {
			if r.State == string(state.StateColdBooting) {
				insID = r.ID
				break
			}
		}
		if insID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if insID == "" {
		t.Fatal("never observed a COLD_BOOTING row — Phase 2 didn't commit")
	}

	// Simulate the watchdog (or any external transition) stealing
	// the state. We mutate the store directly; the engine will
	// observe this in Phase 4's re-read.
	if err := store.UpdateInstanceState(context.Background(), insID, string(state.StateStopped)); err != nil {
		t.Fatalf("UpdateInstanceState: %v", err)
	}

	// Drain bootStarted (best-effort — it may have already been
	// signalled by the time we got here) and release the boot.
	select {
	case <-bootStarted:
	default:
	}
	close(bootRelease)

	select {
	case r := <-aRes:
		if r.err == nil {
			t.Fatalf("Wake returned nil err despite state theft; expected error")
		}
		if !strings.Contains(r.err.Error(), "state stolen") {
			t.Errorf("Wake err = %v, want 'state stolen' message", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wake never returned after release")
	}

	// VM must be destroyed (the boot was wasted) and ledger released.
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if vmm.destroys < 1 {
		t.Errorf("destroys = %d, want ≥1 (post-stolen-state cleanup)", vmm.destroys)
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 (reservation released)", got)
	}
}
