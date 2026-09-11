package builderd

import (
	"errors"
	"testing"
	"time"
)

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
