package builderd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type warmBuilderTestVM struct {
	deleted    []WarmSnapshot
	deletedCh  chan struct{}
	deleteErr  error
	deleteCall int
}

type warmRestoreTestVM struct {
	warmBuilderTestVM
	restored     WarmSnapshot
	restoreCalls int
	restoreErr   error
	warmOut      BuildOutcome
	warmCaptured WarmSnapshot
}

func (v *warmRestoreTestVM) RestoreWarmBuilder(_ context.Context, req VMRequest, snapshot WarmSnapshot) (BuildHandle, error) {
	v.restoreCalls++
	if v.restoreErr != nil {
		return BuildHandle{}, v.restoreErr
	}
	v.restored = snapshot
	return BuildHandle{Instance: "build-" + req.BuildID, BuildID: req.BuildID, TimeoutSec: req.TimeoutSec}, nil
}

func (v *warmRestoreTestVM) Spawn(_ context.Context, req VMRequest) (BuildHandle, error) {
	return BuildHandle{Instance: "cold-" + req.BuildID, BuildID: req.BuildID, TimeoutSec: req.TimeoutSec}, nil
}

func (v *warmRestoreTestVM) WaitForWarmCompletion(context.Context, BuildHandle) (BuildOutcome, WarmSnapshot, error) {
	return v.warmOut, v.warmCaptured, nil
}

func (v *warmBuilderTestVM) Spawn(context.Context, VMRequest) (BuildHandle, error) {
	return BuildHandle{}, errors.New("unused")
}

func (v *warmBuilderTestVM) WaitForCompletion(context.Context, BuildHandle) (BuildOutcome, error) {
	return BuildOutcome{}, errors.New("unused")
}

func (v *warmBuilderTestVM) Cancel(context.Context, string) error { return nil }
func (v *warmBuilderTestVM) FirecrackerVersion(context.Context) (string, error) {
	return "firecracker-1.8.0", nil
}
func (v *warmBuilderTestVM) RestoreWarmBuilder(context.Context, VMRequest, WarmSnapshot) (BuildHandle, error) {
	return BuildHandle{}, errors.New("unused")
}
func (v *warmBuilderTestVM) WaitForWarmCompletion(context.Context, BuildHandle) (BuildOutcome, WarmSnapshot, error) {
	return BuildOutcome{}, WarmSnapshot{}, errors.New("unused")
}
func (v *warmBuilderTestVM) DeleteWarmSnapshot(_ context.Context, snapshot WarmSnapshot) error {
	v.deleteCall++
	if v.deleteErr != nil {
		return v.deleteErr
	}
	v.deleted = append(v.deleted, snapshot)
	if v.deletedCh != nil {
		select {
		case v.deletedCh <- struct{}{}:
		default:
		}
	}
	return nil
}

func testWarmSnapshot() WarmSnapshot {
	return WarmSnapshot{
		StorageKey:        "builder/build-1/mem",
		VMStateStorageKey: "builder/build-1/vmstate",
		VMStatePath:       "/var/lib/faas/snapshots/build-1.vmstate",
		FCVersion:         "firecracker-1.8.0",
	}
}

func testStorageWarmSnapshot() WarmSnapshot {
	snapshot := testWarmSnapshot()
	snapshot.VMStatePath = ""
	return snapshot
}

func TestBuilderWarmScopeKeyIsAppAndRuntimeScoped(t *testing.T) {
	base := builderWarmScopeKey("acct", "app", FrameworkNode, "node-base")
	if base == "" {
		t.Fatal("builderWarmScopeKey returned empty key")
	}
	for name, other := range map[string]string{
		"account": builderWarmScopeKey("other", "app", FrameworkNode, "node-base"),
		"app":     builderWarmScopeKey("acct", "other", FrameworkNode, "node-base"),
		"runtime": builderWarmScopeKey("acct", "app", FrameworkPython, "node-base"),
		"base":    builderWarmScopeKey("acct", "app", FrameworkNode, "other-base"),
	} {
		if other == base {
			t.Fatalf("scope key for %s matched the base scope", name)
		}
	}
}

func TestPrepareWarmBuilderDiscardsForeignScope(t *testing.T) {
	vm := &warmBuilderTestVM{}
	ops := wire.NewOpsMetrics("builderd")
	b := New(nil, nil, vm, nil, nil, nil, Config{}, nil).WithOpsMetrics(ops)
	now := time.Unix(100, 0)
	if _, err := b.warm.Start(now, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	snapshot := testStorageWarmSnapshot()
	snapshot.ScopeKey = "foreign-scope"
	if err := b.warm.Complete(now, snapshot); err != nil {
		t.Fatal(err)
	}

	gotVM, result, gotSnapshot, started := b.prepareWarmBuilder(context.Background(), SlotDecision{Label: "guaranteed"}, VMRequest{WarmScopeKey: "current-scope"})
	if gotVM != vm || result != WarmRestoreMiss || gotSnapshot != (WarmSnapshot{}) || !started {
		t.Fatalf("prepareWarmBuilder = (%v, %q, %+v, %v), want warm miss with empty snapshot", gotVM, result, gotSnapshot, started)
	}
	if len(vm.deleted) != 1 || vm.deleted[0].ScopeKey != "foreign-scope" {
		t.Fatalf("deleted snapshots = %+v, want the foreign scope", vm.deleted)
	}
	body := scrapeMetrics(t, ops)
	if !strings.Contains(body, `builderd_warm_restore_total{result="miss"} 1`) {
		t.Fatalf("foreign scope metric missing miss=1:\n%s", body)
	}
	if strings.Contains(body, `builderd_warm_restore_total{result="hit"} 1`) {
		t.Fatal("foreign scope was counted as a warm restore hit")
	}
}

func TestProcessOneCleansConsumedWarmSnapshotAfterRestore(t *testing.T) {
	store := state.NewMemStore()
	source := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, source, []string{"package.json", "index.js"})
	buildID, _, appID := seedDeployment(t, store, source)
	app, err := store.AppByID(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}

	layerPath := filepath.Join(t.TempDir(), "produced.ext4")
	if err := os.WriteFile(layerPath, []byte("produced layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	vm := &warmRestoreTestVM{warmOut: BuildOutcome{OCIImage: layerPath, ExitCode: 0}}
	ops := wire.NewOpsMetrics("builderd")
	b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithOpsMetrics(ops)

	now := time.Now()
	if _, _, err := b.StartWarmBuilder(now, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	old := testStorageWarmSnapshot()
	old.ScopeKey = builderWarmScopeKey(app.AccountID, app.ID, FrameworkNode, imaged.BaseRefMinimal)
	old.CreatedAt = now
	old.LastUsedAt = now
	if err := b.CompleteWarmBuilder(now, old); err != nil {
		t.Fatal(err)
	}

	if _, err := b.ProcessOne(context.Background(), buildID); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if vm.restoreCalls != 1 || vm.restored != old {
		t.Fatalf("restore calls/snapshot = %d/%+v, want 1/%+v", vm.restoreCalls, vm.restored, old)
	}
	if len(vm.deleted) != 1 {
		t.Fatalf("deleted snapshots = %+v, want one consumed snapshot", vm.deleted)
	}
	if vm.deleted[0] != func() WarmSnapshot {
		consumed := old
		consumed.LayerPath = ""
		return consumed
	}() {
		t.Fatalf("deleted snapshot = %+v, want old storage objects without retained drive", vm.deleted[0])
	}
	if got := b.WarmState(); got != WarmCold {
		t.Fatalf("WarmState() = %q, want %q after no replacement capture", got, WarmCold)
	}
	body := scrapeMetrics(t, ops)
	if !strings.Contains(body, `builderd_warm_restore_total{result="hit"} 1`) {
		t.Fatalf("successful restore metric missing hit=1:\n%s", body)
	}
	if strings.Contains(body, `builderd_warm_restore_total{result="miss"} 1`) {
		t.Fatal("successful warm restore was also counted as a miss")
	}
}

func TestProcessOneCountsWarmRestoreFallbackAsMiss(t *testing.T) {
	store := state.NewMemStore()
	source := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, source, []string{"package.json", "index.js"})
	buildID, _, appID := seedDeployment(t, store, source)
	app, err := store.AppByID(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}

	layerPath := filepath.Join(t.TempDir(), "produced.ext4")
	if err := os.WriteFile(layerPath, []byte("produced layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	vm := &warmRestoreTestVM{
		restoreErr: errors.New("restore unavailable"),
		warmOut:    BuildOutcome{OCIImage: layerPath, ExitCode: 0},
	}
	ops := wire.NewOpsMetrics("builderd")
	b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithOpsMetrics(ops)

	now := time.Now()
	if _, _, err := b.StartWarmBuilder(now, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	old := testStorageWarmSnapshot()
	old.ScopeKey = builderWarmScopeKey(app.AccountID, app.ID, FrameworkNode, imaged.BaseRefMinimal)
	old.CreatedAt = now
	old.LastUsedAt = now
	if err := b.CompleteWarmBuilder(now, old); err != nil {
		t.Fatal(err)
	}

	if _, err := b.ProcessOne(context.Background(), buildID); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	body := scrapeMetrics(t, ops)
	if !strings.Contains(body, `builderd_warm_restore_total{result="miss"} 1`) {
		t.Fatalf("restore fallback metric missing miss=1:\n%s", body)
	}
	if strings.Contains(body, `builderd_warm_restore_total{result="hit"} 1`) {
		t.Fatal("failed warm restore was counted as a hit")
	}
}

func TestWarmLifecycleColdStartIsMiss(t *testing.T) {
	lifecycle := NewWarmLifecycle(time.Minute)

	result, err := lifecycle.Start(time.Unix(100, 0), "firecracker-1.8.0")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result != WarmRestoreMiss {
		t.Fatalf("Start() result = %q, want %q", result, WarmRestoreMiss)
	}
	if got := lifecycle.State(); got != WarmRunning {
		t.Fatalf("State() = %q, want %q", got, WarmRunning)
	}
}

func TestWarmLifecycleRestoreHitConsumesSnapshot(t *testing.T) {
	base := time.Unix(100, 0)
	lifecycle := NewWarmLifecycle(time.Minute)
	if _, err := lifecycle.Start(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Complete(base, testWarmSnapshot()); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	result, err := lifecycle.Start(base.Add(30*time.Second), "firecracker-1.8.0")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result != WarmRestoreHit {
		t.Fatalf("Start() result = %q, want %q", result, WarmRestoreHit)
	}
	if _, ok := lifecycle.Snapshot(); ok {
		t.Fatal("Snapshot() returned a retained snapshot after restore")
	}
}

func TestWarmLifecycleStartWithSnapshotReturnsConsumedSnapshot(t *testing.T) {
	base := time.Unix(100, 0)
	lifecycle := NewWarmLifecycle(time.Minute)
	if _, err := lifecycle.Start(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	want := testStorageWarmSnapshot()
	if err := lifecycle.Complete(base, want); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	want.CreatedAt = base
	want.LastUsedAt = base

	result, snapshot, err := lifecycle.StartWithSnapshot(base.Add(10*time.Second), "firecracker-1.8.0")
	if err != nil {
		t.Fatalf("StartWithSnapshot() error = %v", err)
	}
	if result != WarmRestoreHit {
		t.Fatalf("StartWithSnapshot() result = %q, want %q", result, WarmRestoreHit)
	}
	if snapshot != want {
		t.Fatalf("StartWithSnapshot() snapshot = %+v, want %+v", snapshot, want)
	}
}

func TestWarmLifecycleStorageBackedSnapshotIsValid(t *testing.T) {
	base := time.Unix(100, 0)
	lifecycle := NewWarmLifecycle(time.Minute)
	if _, err := lifecycle.Start(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Complete(base, testStorageWarmSnapshot()); err != nil {
		t.Fatalf("Complete() rejected storage-backed snapshot: %v", err)
	}
}

func TestWarmLifecycleExpiryIsMissAndReturnsCold(t *testing.T) {
	base := time.Unix(100, 0)
	lifecycle := NewWarmLifecycle(time.Minute)
	if _, err := lifecycle.Start(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Complete(base, testWarmSnapshot()); err != nil {
		t.Fatal(err)
	}

	result, err := lifecycle.Start(base.Add(time.Minute), "firecracker-1.8.0")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result != WarmRestoreMiss {
		t.Fatalf("Start() result = %q, want %q", result, WarmRestoreMiss)
	}
	if got := lifecycle.State(); got != WarmRunning {
		t.Fatalf("State() = %q, want %q", got, WarmRunning)
	}
}

func TestWarmLifecycleStaleVersionIsReportedAndCleared(t *testing.T) {
	base := time.Unix(100, 0)
	lifecycle := NewWarmLifecycle(time.Minute)
	if _, err := lifecycle.Start(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Complete(base, testWarmSnapshot()); err != nil {
		t.Fatal(err)
	}

	result, err := lifecycle.Start(base.Add(10*time.Second), "firecracker-1.9.0")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result != WarmRestoreStale {
		t.Fatalf("Start() result = %q, want %q", result, WarmRestoreStale)
	}
	if _, ok := lifecycle.Snapshot(); ok {
		t.Fatal("Snapshot() returned a stale snapshot")
	}
}

func TestWarmLifecycleExpireEvictsPausedSnapshot(t *testing.T) {
	base := time.Unix(100, 0)
	lifecycle := NewWarmLifecycle(time.Minute)
	if _, err := lifecycle.Start(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Complete(base, testWarmSnapshot()); err != nil {
		t.Fatal(err)
	}

	if !lifecycle.Expire(base.Add(time.Minute)) {
		t.Fatal("Expire() = false, want true")
	}
	if got := lifecycle.State(); got != WarmCold {
		t.Fatalf("State() = %q, want %q", got, WarmCold)
	}
	if _, ok := lifecycle.Snapshot(); ok {
		t.Fatal("Snapshot() returned an evicted snapshot")
	}
}

func TestWarmLifecycleExpireSnapshotReturnsEvictedMetadata(t *testing.T) {
	base := time.Unix(100, 0)
	lifecycle := NewWarmLifecycle(time.Minute)
	if _, err := lifecycle.Start(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	want := testStorageWarmSnapshot()
	if err := lifecycle.Complete(base, want); err != nil {
		t.Fatal(err)
	}
	want.CreatedAt = base
	want.LastUsedAt = base

	got, expired := lifecycle.ExpireSnapshot(base.Add(time.Minute))
	if !expired {
		t.Fatal("ExpireSnapshot() = false, want true")
	}
	if got != want {
		t.Fatalf("ExpireSnapshot() metadata = %+v, want %+v", got, want)
	}
}

func TestWarmLifecycleInvalidatesOnFailure(t *testing.T) {
	base := time.Unix(100, 0)
	lifecycle := NewWarmLifecycle(time.Minute)
	if _, err := lifecycle.Start(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Complete(base, testWarmSnapshot()); err != nil {
		t.Fatal(err)
	}

	want := testWarmSnapshot()
	want.CreatedAt = base
	want.LastUsedAt = base
	got, retained := lifecycle.InvalidateSnapshot()
	if !retained || got != want {
		t.Fatalf("InvalidateSnapshot() = (%+v, %v), want (%+v, true)", got, retained, want)
	}
	if got := lifecycle.State(); got != WarmCold {
		t.Fatalf("State() = %q, want %q", got, WarmCold)
	}
	if _, ok := lifecycle.Snapshot(); ok {
		t.Fatal("Snapshot() returned an invalidated snapshot")
	}
}

func TestWarmLifecycleRejectsConcurrentStart(t *testing.T) {
	lifecycle := NewWarmLifecycle(time.Minute)
	base := time.Unix(100, 0)
	if _, err := lifecycle.Start(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}

	if _, err := lifecycle.Start(base, "firecracker-1.8.0"); !errors.Is(err, ErrWarmAlreadyRunning) {
		t.Fatalf("second Start() error = %v, want %v", err, ErrWarmAlreadyRunning)
	}
}

func TestNewWarmLifecycleDefaultsIdleWindow(t *testing.T) {
	lifecycle := NewWarmLifecycle(0)
	base := time.Unix(100, 0)
	if _, err := lifecycle.Start(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Complete(base, testWarmSnapshot()); err != nil {
		t.Fatal(err)
	}

	result, err := lifecycle.Start(base.Add(DefaultWarmIdle-time.Nanosecond), "firecracker-1.8.0")
	if err != nil {
		t.Fatal(err)
	}
	if result != WarmRestoreHit {
		t.Fatalf("Start() result = %q, want %q", result, WarmRestoreHit)
	}
}

func TestBuilderdWarmLifecycleUsesConfiguredIdleWindow(t *testing.T) {
	base := time.Unix(100, 0)
	b := New(nil, nil, nil, NewCache(t.TempDir()), nil, nil, Config{WarmIdle: time.Second}, nil)

	result, _, err := b.StartWarmBuilder(base, "firecracker-1.8.0")
	if err != nil || result != WarmRestoreMiss {
		t.Fatalf("cold StartWarmBuilder() = (%q, %v), want (miss, nil)", result, err)
	}
	if err := b.CompleteWarmBuilder(base, testStorageWarmSnapshot()); err != nil {
		t.Fatalf("CompleteWarmBuilder() error = %v", err)
	}
	if _, ok := b.ExpireWarmBuilder(base.Add(time.Second)); !ok {
		t.Fatal("ExpireWarmBuilder() = false, want true at configured idle boundary")
	}
	if got := b.WarmState(); got != WarmCold {
		t.Fatalf("WarmState() = %q, want %q", got, WarmCold)
	}
}

func TestSweepExpiredWarmBuilderCleansBackingStore(t *testing.T) {
	base := time.Unix(100, 0)
	vm := &warmBuilderTestVM{}
	b := New(nil, nil, vm, NewCache(t.TempDir()), nil, nil, Config{WarmIdle: time.Minute}, nil)

	if _, _, err := b.StartWarmBuilder(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	want := testStorageWarmSnapshot()
	want.CreatedAt = base
	want.LastUsedAt = base
	if err := b.CompleteWarmBuilder(base, want); err != nil {
		t.Fatal(err)
	}

	expired, err := b.SweepExpiredWarmBuilder(context.Background(), base.Add(time.Minute))
	if err != nil {
		t.Fatalf("SweepExpiredWarmBuilder() error = %v", err)
	}
	if !expired {
		t.Fatal("SweepExpiredWarmBuilder() = false, want true")
	}
	if len(vm.deleted) != 1 || vm.deleted[0] != want {
		t.Fatalf("deleted snapshots = %+v, want %+v", vm.deleted, []WarmSnapshot{want})
	}
	if got := b.WarmState(); got != WarmCold {
		t.Fatalf("WarmState() = %q, want %q", got, WarmCold)
	}
}

func TestSweepExpiredWarmBuilderRetriesFailedCleanup(t *testing.T) {
	base := time.Unix(100, 0)
	vm := &warmBuilderTestVM{deleteErr: errors.New("temporary vmmd failure")}
	b := New(nil, nil, vm, NewCache(t.TempDir()), nil, nil, Config{WarmIdle: time.Minute}, nil)

	if _, _, err := b.StartWarmBuilder(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	want := testStorageWarmSnapshot()
	want.CreatedAt = base
	want.LastUsedAt = base
	if err := b.CompleteWarmBuilder(base, want); err != nil {
		t.Fatal(err)
	}

	expired, err := b.SweepExpiredWarmBuilder(context.Background(), base.Add(time.Minute))
	if !expired {
		t.Fatal("SweepExpiredWarmBuilder() = false, want true")
	}
	if err == nil {
		t.Fatal("SweepExpiredWarmBuilder() error = nil, want the cleanup failure")
	}
	if vm.deleteCall != 1 || len(vm.deleted) != 0 {
		t.Fatalf("cleanup calls = (%d, %+v), want one failed call", vm.deleteCall, vm.deleted)
	}

	vm.deleteErr = nil
	expired, err = b.SweepExpiredWarmBuilder(context.Background(), base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("retrying SweepExpiredWarmBuilder() error = %v", err)
	}
	if expired {
		t.Fatal("retrying SweepExpiredWarmBuilder() = true, want false")
	}
	if vm.deleteCall != 2 || len(vm.deleted) != 1 || vm.deleted[0] != want {
		t.Fatalf("retried cleanup = (%d, %+v), want one successful retry for %+v", vm.deleteCall, vm.deleted, want)
	}
	b.warmCleanupMu.Lock()
	pending := len(b.warmCleanupPending)
	b.warmCleanupMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending warm cleanups = %d, want 0", pending)
	}
}

func TestDrainRetriesPendingWarmSnapshotCleanup(t *testing.T) {
	base := time.Unix(100, 0)
	vm := &warmBuilderTestVM{deleteErr: errors.New("temporary vmmd failure")}
	b := New(nil, nil, vm, NewCache(t.TempDir()), nil, nil, Config{}, nil)

	if _, _, err := b.StartWarmBuilder(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	if err := b.CompleteWarmBuilder(base, testStorageWarmSnapshot()); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SweepExpiredWarmBuilder(context.Background(), base.Add(6*time.Minute)); err == nil {
		t.Fatal("SweepExpiredWarmBuilder() error = nil, want the cleanup failure")
	}

	vm.deleteErr = nil
	if err := b.Drain(context.Background()); err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	if vm.deleteCall != 2 || len(vm.deleted) != 1 {
		t.Fatalf("Drain cleanup calls = (%d, %+v), want one failed and one successful call", vm.deleteCall, vm.deleted)
	}
}

func TestSweepDefersPendingCleanupWhileWarmSlotIsRetained(t *testing.T) {
	base := time.Unix(100, 0)
	vm := &warmBuilderTestVM{deleteErr: errors.New("temporary vmmd failure")}
	b := New(nil, nil, vm, NewCache(t.TempDir()), nil, nil, Config{WarmIdle: time.Minute}, nil)

	if _, _, err := b.StartWarmBuilder(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	snapshot := testStorageWarmSnapshot()
	snapshot.CreatedAt = base
	snapshot.LastUsedAt = base
	if err := b.CompleteWarmBuilder(base, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SweepExpiredWarmBuilder(context.Background(), base.Add(time.Minute)); err == nil {
		t.Fatal("SweepExpiredWarmBuilder() error = nil, want the cleanup failure")
	}

	// A later capture can reuse the same build-derived storage key. It must
	// remain untouched while the newer snapshot is eligible for restore.
	if _, _, err := b.StartWarmBuilder(base.Add(2*time.Minute), "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	if err := b.CompleteWarmBuilder(base.Add(2*time.Minute), snapshot); err != nil {
		t.Fatal(err)
	}
	if expired, err := b.SweepExpiredWarmBuilder(context.Background(), base.Add(2*time.Minute+30*time.Second)); expired || err != nil {
		t.Fatalf("SweepExpiredWarmBuilder() = (%v, %v), want retained snapshot without retry", expired, err)
	}
	if vm.deleteCall != 1 {
		t.Fatalf("cleanup calls while retained = %d, want 1", vm.deleteCall)
	}
}

func TestDrainCleansRetainedWarmSnapshot(t *testing.T) {
	base := time.Unix(100, 0)
	vm := &warmBuilderTestVM{}
	b := New(nil, nil, vm, NewCache(t.TempDir()), nil, nil, Config{}, nil)

	if _, _, err := b.StartWarmBuilder(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	want := testStorageWarmSnapshot()
	want.CreatedAt = base
	want.LastUsedAt = base
	if err := b.CompleteWarmBuilder(base, want); err != nil {
		t.Fatal(err)
	}
	if err := b.Drain(context.Background()); err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	if len(vm.deleted) != 1 || vm.deleted[0] != want {
		t.Fatalf("deleted snapshots = %+v, want %+v", vm.deleted, []WarmSnapshot{want})
	}
	if got := b.WarmState(); got != WarmCold {
		t.Fatalf("WarmState() = %q, want %q", got, WarmCold)
	}
}

func TestWarmBuilderSweepLoopExpiresSnapshot(t *testing.T) {
	vm := &warmBuilderTestVM{deletedCh: make(chan struct{}, 1)}
	b := New(nil, nil, vm, NewCache(t.TempDir()), nil, nil, Config{WarmIdle: time.Millisecond}, nil)
	now := time.Now()
	if _, _, err := b.StartWarmBuilder(now, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	if err := b.CompleteWarmBuilder(now, testStorageWarmSnapshot()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		WarmBuilderSweepLoop(ctx, b, time.Millisecond, nil)
		close(done)
	}()
	select {
	case <-vm.deletedCh:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("WarmBuilderSweepLoop did not expire the snapshot")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WarmBuilderSweepLoop did not stop after cancellation")
	}
}

func TestWarmLifecycleRejectsInvalidSnapshotAndReturnsCold(t *testing.T) {
	lifecycle := NewWarmLifecycle(time.Minute)
	base := time.Unix(100, 0)
	if _, err := lifecycle.Start(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}

	if err := lifecycle.Complete(base, WarmSnapshot{FCVersion: "firecracker-1.8.0"}); !errors.Is(err, ErrInvalidWarmSnapshot) {
		t.Fatalf("Complete() error = %v, want %v", err, ErrInvalidWarmSnapshot)
	}
	if got := lifecycle.State(); got != WarmCold {
		t.Fatalf("State() = %q, want %q", got, WarmCold)
	}
}
