package vmmdmount

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func reserveMountForTest(t *testing.T, r *Registry, mountpoint string) *MountLease {
	t.Helper()
	lease, err := r.ReserveMount(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Attach(mountpoint, MountKindParentExt4, "base/parent.ext4", "protected-source"); err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestMountLeaseReservesCapacityBeforeMounting(t *testing.T) {
	r := NewRegistry(1)
	lease, err := r.ReserveMount(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.entries) != 0 {
		t.Fatal("reservation created a physical mount record")
	}
	if _, err := r.ReserveMount(t.Context()); !errors.Is(err, ErrMountCapacity) {
		t.Fatalf("capacity not reserved: %v", err)
	}
	if _, err := r.RegisterOrEvict("legacy", MountKindParentExt4, "key", "source"); !errors.Is(err, ErrMountCapacity) {
		t.Fatalf("legacy path consumed reserved capacity: %v", err)
	}
	if err := lease.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := r.ReserveMount(t.Context())
	if err != nil {
		t.Fatalf("unused reservation leaked: %v", err)
	}
	if err := second.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestMountLeaseProtectsInFlightMaterialization(t *testing.T) {
	r := NewRegistry(1)
	var cleanups int
	r.cleanup = func(context.Context, string, MountEntry, bool) (bool, error) { cleanups++; return true, nil }
	lease := reserveMountForTest(t, r, "materializing")
	entry := r.entries["materializing"]
	entry.MountedAt = time.Now().Add(-2 * ParentMountMaxAge)
	r.entries["materializing"] = entry
	for _, action := range []string{"external unmount", "capacity eviction", "replace identity", "forget", "orphan sweep", "shutdown sweep"} {
		t.Run(action, func(t *testing.T) {
			switch action {
			case "external unmount":
				if _, err := r.Umount(t.Context(), "materializing"); !errors.Is(err, ErrMountBusy) {
					t.Fatalf("unmount: %v", err)
				}
			case "capacity eviction":
				if _, err := r.RegisterOrEvict("new", MountKindParentExt4, "other", "other"); !errors.Is(err, ErrMountCapacity) {
					t.Fatalf("eviction: %v", err)
				}
			case "replace identity":
				if _, err := r.RegisterOrEvict("materializing", MountKindParentExt4, "other", "other"); !errors.Is(err, ErrMountBusy) {
					t.Fatalf("replacement: %v", err)
				}
			case "forget":
				r.Forget("materializing")
			case "orphan sweep":
				if n := r.SweepOrphans(t.Context(), nil); n != 0 {
					t.Fatalf("swept active owner: %d", n)
				}
			case "shutdown sweep":
				if n := r.SweepAll(t.Context(), nil); n != 0 {
					t.Fatalf("swept active owner: %d", n)
				}
			}
			if actual, present := r.Lookup("materializing"); !present || actual != entry || cleanups != 0 {
				t.Fatal("active source or ownership changed during copy")
			}
		})
	}
	if err := lease.Release(t.Context()); err != nil || cleanups != 1 {
		t.Fatalf("owner did not release its exact mount: %v, cleanups=%d", err, cleanups)
	}
}

func TestMountLeaseCleanupIsExclusive(t *testing.T) {
	r := NewRegistry(1)
	started, proceed := make(chan struct{}), make(chan struct{})
	var cleanups atomic.Int32
	r.cleanup = func(context.Context, string, MountEntry, bool) (bool, error) {
		cleanups.Add(1)
		close(started)
		<-proceed
		return true, nil
	}
	lease := reserveMountForTest(t, r, "owned")
	done := make(chan error, 1)
	go func() { done <- lease.Release(t.Context()) }()
	<-started
	for _, action := range []string{"release", "unmount", "register", "handoff"} {
		var err error
		switch action {
		case "release":
			err = lease.Release(t.Context())
		case "unmount":
			_, err = r.Umount(t.Context(), "owned")
		case "register":
			_, err = r.RegisterOrEvict("owned", MountKindParentExt4, "other", "other")
		case "handoff":
			err = lease.HandOff()
		}
		if !errors.Is(err, ErrMountBusy) {
			t.Errorf("%s during cleanup: %v", action, err)
		}
	}
	r.Forget("owned")
	if _, err := r.ReserveMount(t.Context()); !errors.Is(err, ErrMountCapacity) {
		t.Errorf("capacity freed before physical cleanup: %v", err)
	}
	close(proceed)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(t.Context()); err != nil || cleanups.Load() != 1 {
		t.Fatalf("duplicate cleanup: %v, %d calls", err, cleanups.Load())
	}
	if len(r.entries)+len(r.owners)+len(r.releasing)+len(r.reservations)+len(r.retrying)+len(r.unmounted) != 0 {
		t.Fatal("released lease leaked registry ownership")
	}
}

func TestMountLeaseFailedCleanupRetainsExactOwnershipForSweep(t *testing.T) {
	r := NewRegistry(1)
	failure := errors.New("native unmount failed")
	var receipts []MountEntry
	r.cleanup = func(_ context.Context, _ string, entry MountEntry, _ bool) (bool, error) {
		receipts = append(receipts, entry)
		if len(receipts) == 1 {
			return false, failure
		}
		return true, nil
	}
	lease := reserveMountForTest(t, r, "owned")
	expected := r.entries["owned"]
	if err := lease.Release(t.Context()); !errors.Is(err, failure) {
		t.Fatalf("false cleanup receipt: %v", err)
	}
	if actual, exists := r.Lookup("owned"); !exists || actual != expected {
		t.Fatal("failed cleanup lost its exact source")
	}
	if len(r.owners) != 0 {
		t.Fatal("completed operation prevents cleanup retry")
	}
	if _, err := r.RegisterOrEvict("replacement", MountKindParentExt4, "other", "other"); !errors.Is(err, ErrMountCapacity) {
		t.Fatalf("failed cleanup ownership evicted: %v", err)
	}
	r.Forget("owned")
	if _, exists := r.Lookup("owned"); !exists {
		t.Fatal("failed cleanup forgotten")
	}
	if n := r.SweepAll(t.Context(), nil); n != 1 {
		t.Fatalf("cleanup was not retried: %d", n)
	}
	if len(receipts) != 2 || receipts[0] != expected || receipts[1] != expected {
		t.Fatal("retry changed cleanup identity")
	}
	if err := lease.Release(t.Context()); !errors.Is(err, failure) {
		t.Fatalf("sweep upgraded a failed receipt: %v", err)
	}
}

func TestRegistrySourceCleanupFailureKeepsRetryRecord(t *testing.T) {
	r := NewRegistry(1)
	source := filepath.Join(t.TempDir(), "not-removable-yet")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(source, "child")
	if err := os.WriteFile(child, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	mountpoint := t.TempDir()
	var phases []bool
	r.cleanup = func(ctx context.Context, path string, entry MountEntry, unmounted bool) (bool, error) {
		phases = append(phases, unmounted)
		return true, cleanupRegisteredFiles(ctx, path, entry)
	}
	lease, err := r.ReserveMount(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Attach(mountpoint, MountKindParentExt4, "key", source); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(t.Context()); err == nil {
		t.Fatal("source removal failure acknowledged")
	}
	if _, present := r.Lookup(mountpoint); !present {
		t.Fatal("source retry ownership lost")
	}
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Umount(t.Context(), mountpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("retry did not remove source: %v", err)
	}
	if len(phases) != 2 || phases[0] || !phases[1] {
		t.Fatal("source cleanup retry repeated the completed unmount")
	}
}

func TestMountLeaseLegacyHandoffKeepsOrphanCleanup(t *testing.T) {
	r := NewRegistry(1)
	var cleanups int
	r.cleanup = func(context.Context, string, MountEntry, bool) (bool, error) { cleanups++; return true, nil }
	lease := reserveMountForTest(t, r, "legacy")
	if err := lease.HandOff(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(t.Context()); err != nil || cleanups != 0 {
		t.Fatalf("handoff defer unmounted legacy caller: %v", err)
	}
	if n := r.SweepAll(t.Context(), nil); n != 1 || cleanups != 1 {
		t.Fatal("legacy mount lost sweep ownership")
	}
}

func TestMountLeaseRejectsInvalidTransitions(t *testing.T) {
	r := NewRegistry(2)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.ReserveMount(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled reservation: %v", err)
	}
	lease, err := r.ReserveMount(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.HandOff(); !errors.Is(err, ErrMountBusy) {
		t.Fatalf("unattached handoff: %v", err)
	}
	if err := lease.Attach("bad", MountKind(999), "key", "source"); err == nil {
		t.Fatal("unknown kind attached")
	}
	if err := lease.Attach("owned", MountKindParentExt4, "key", "source"); err != nil {
		t.Fatal(err)
	}
	if err := lease.Attach("second", MountKindParentExt4, "other", "other"); !errors.Is(err, ErrMountBusy) {
		t.Fatalf("lease attached twice: %v", err)
	}
	if err := lease.HandOff(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Attach("second", MountKindParentExt4, "other", "other"); !errors.Is(err, ErrMountBusy) {
		t.Fatalf("closed lease attached: %v", err)
	}
}

func TestMountLeaseConcurrentCapacityAndCleanup(t *testing.T) {
	r := NewRegistry(4)
	r.cleanup = func(context.Context, string, MountEntry, bool) (bool, error) { return true, nil }
	var wg sync.WaitGroup
	for worker := range 12 {
		wg.Go(func() {
			for attempt := range 40 {
				lease, err := r.ReserveMount(t.Context())
				if errors.Is(err, ErrMountCapacity) {
					continue
				}
				if err != nil {
					t.Error(err)
					return
				}
				name := fmt.Sprintf("owned-%d-%d", worker, attempt)
				if err := lease.Attach(name, MountKindParentExt4, "key", "source"); err != nil {
					t.Error(err)
					return
				}
				r.mu.Lock()
				if len(r.entries)+len(r.reservations) > r.cap {
					t.Error("physical ownership exceeded capacity")
				}
				r.mu.Unlock()
				if _, err := r.Umount(t.Context(), name); !errors.Is(err, ErrMountBusy) {
					t.Errorf("owner released externally: %v", err)
				}
				if err := lease.Release(t.Context()); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	if len(r.entries)+len(r.owners)+len(r.releasing)+len(r.reservations)+len(r.retrying)+len(r.unmounted) != 0 {
		t.Fatal("concurrent leases leaked ownership")
	}
}
