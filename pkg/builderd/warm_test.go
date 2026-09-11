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

func TestWarmLifecycleInvalidatesOnFailure(t *testing.T) {
	base := time.Unix(100, 0)
	lifecycle := NewWarmLifecycle(time.Minute)
	if _, err := lifecycle.Start(base, "firecracker-1.8.0"); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Complete(base, testWarmSnapshot()); err != nil {
		t.Fatal(err)
	}

	lifecycle.Invalidate()
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
