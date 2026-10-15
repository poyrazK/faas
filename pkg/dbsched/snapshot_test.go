// adr: 1027
package dbsched

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const snapName = "0123456789abcdef0123456789abcdef"

// A snapshot holds the source's archive at its point once the archive has
// reached it; capturing again returns it unchanged, another point under
// the name conflicts, and deleting it is idempotent.
func TestCaptureSnapshotHoldsAndFlushes(t *testing.T) {
	s, _, vmm, source := archivedSource(t)
	ctx := context.Background()
	point := time.Now().Add(-time.Millisecond)
	snap, err := s.CaptureSnapshot(ctx, source.DatabaseID, snapName, point)
	want := point.UTC().Truncate(time.Microsecond)
	if err != nil || !snap.PointInTime.Equal(want) || snap.CreatedAt.Before(want) || snap.DatabaseID != source.DatabaseID {
		t.Fatalf("capture = %+v, %v", snap, err)
	}
	if after, ok := vmm.flushes[source.DatabaseID]; !ok || !after.Equal(want) {
		t.Fatalf("flush = %v %v; want the archive past the point", after, ok)
	}
	if !strings.Contains(strings.Join(vmm.onNode, " "), "hold:"+snapName+"@db-a") {
		t.Fatalf("hold not placed on the source's node: %v", vmm.onNode)
	}
	again, err := s.CaptureSnapshot(ctx, source.DatabaseID, snapName, point)
	if err != nil || again != snap {
		t.Fatalf("again = %+v, %v; want %+v", again, err, snap)
	}
	if _, err := s.CaptureSnapshot(ctx, source.DatabaseID, snapName, point.Add(-time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("another point = %v, want ErrConflict", err)
	}
	if got, err := s.GetSnapshot(ctx, source.DatabaseID, snapName); err != nil || got != snap {
		t.Fatalf("get = %+v, %v", got, err)
	}
	for range 2 {
		if err := s.DeleteSnapshot(ctx, source.DatabaseID, snapName); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.GetSnapshot(ctx, source.DatabaseID, snapName); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete = %v", err)
	}
	if err := s.DeleteSnapshot(ctx, uuid.NewString(), snapName); err != nil {
		t.Fatalf("delete of an unknown database's snapshot = %v", err)
	}
}

// A retry after the point has aged past the margin still returns the
// placed hold; a new snapshot there is refused.
func TestCaptureSnapshotRetryOutlivesTheMargin(t *testing.T) {
	s, _, _, source := archivedSource(t)
	ctx := context.Background()
	point := time.Now().Add(-time.Millisecond)
	snap, err := s.CaptureSnapshot(ctx, source.DatabaseID, snapName, point)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return point.Add(minArchiveRetention) }
	if again, err := s.CaptureSnapshot(ctx, source.DatabaseID, snapName, point); err != nil || again != snap {
		t.Fatalf("retry = %+v, %v", again, err)
	}
	if _, err := s.CaptureSnapshot(ctx, source.DatabaseID, strings.Repeat("1", 32), point); !errors.Is(err, ErrInvalid) {
		t.Fatalf("new snapshot at the end of retention = %v, want ErrInvalid", err)
	}
}

func TestCaptureSnapshotRejects(t *testing.T) {
	s, _, _, source := archivedSource(t)
	ctx := context.Background()
	now := time.Now()
	for name, c := range map[string]struct {
		id, snap string
		point    time.Time
	}{
		"future":          {source.DatabaseID, snapName, now.Add(time.Hour)},
		"before database": {source.DatabaseID, snapName, source.CreatedAt.Add(-time.Second)},
		"no point":        {source.DatabaseID, snapName, time.Time{}},
		"bad name":        {source.DatabaseID, "../x", now},
		"bad id":          {"nope", snapName, now},
	} {
		if _, err := s.CaptureSnapshot(ctx, c.id, c.snap, c.point); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: capture = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := s.CaptureSnapshot(ctx, uuid.NewString(), snapName, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown database = %v, want ErrNotFound", err)
	}
	s2, _, _ := newTestScheduler(t, 4096)
	unarchived := provision(t, s2)
	if _, err := s2.CaptureSnapshot(ctx, unarchived.DatabaseID, snapName, time.Now()); !errors.Is(err, ErrInvalid) {
		t.Errorf("database without backups = %v, want ErrInvalid", err)
	}
}

// A restore from a snapshot recovers to its point after the source's
// restore window has passed it; without the snapshot it is refused, and a
// snapshot of another point conflicts.
func TestRestoreFromASnapshotOutsideTheWindow(t *testing.T) {
	s, store, vmm, _ := archivedSource(t)
	ctx := context.Background()
	source, err := s.Provision(ctx, ProvisionRequest{DatabaseID: uuid.NewString(), Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	point := time.Now().Add(-time.Millisecond).UTC().Truncate(time.Microsecond)
	if _, err := s.CaptureSnapshot(ctx, source.DatabaseID, snapName, point); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return point.Add(10 * time.Minute) }
	req := restoreRequest(source, point)
	if _, err := s.Restore(ctx, req); !errors.Is(err, ErrInvalid) {
		t.Fatalf("restore outside the window without the snapshot = %v, want ErrInvalid", err)
	}
	other := req
	other.Snapshot, other.Target = snapName, point.Add(-time.Second)
	if _, err := s.Restore(ctx, other); !errors.Is(err, ErrConflict) {
		t.Fatalf("snapshot of another point = %v, want ErrConflict", err)
	}
	missing := req
	missing.Snapshot = strings.Repeat("2", 32)
	if _, err := s.Restore(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing snapshot = %v, want ErrNotFound", err)
	}
	req.Snapshot = snapName
	vm, err := s.Restore(ctx, req)
	if err != nil {
		t.Fatalf("restore from the snapshot = %v", err)
	}
	s.restoreWG.Wait()
	got, _ := store.Get(ctx, vm.DatabaseID)
	if got.State != StateParked || got.RestoreSource != source.DatabaseID || !got.RestoreTarget.Equal(point) {
		t.Fatalf("restored = %+v", got)
	}
	if n := len(vmm.restoreBoots); n == 0 || !vmm.restoreBoots[n-1].Equal(point) {
		t.Fatalf("restore boots = %v", vmm.restoreBoots)
	}
	// Once the snapshot is gone, the restored database still answers.
	if err := s.DeleteSnapshot(ctx, source.DatabaseID, snapName); err != nil {
		t.Fatal(err)
	}
	if again, err := s.Restore(ctx, req); err != nil || again.DatabaseID != vm.DatabaseID {
		t.Fatalf("repeat after the snapshot is gone = %+v, %v", again, err)
	}
}

// With a shared archive, holds go through another node when the source's
// does not answer; without one, only the source's node reaches them.
func TestSnapshotHoldsReachTheArchive(t *testing.T) {
	ctx := context.Background()
	for _, shared := range []bool{false, true} {
		s, _, vmm, source := twoNodeScheduler(t, shared)
		vmm.down["db-a"] = true
		_, err := s.GetSnapshot(ctx, source.DatabaseID, snapName)
		if shared && !errors.Is(err, ErrNotFound) || !shared && (err == nil || errors.Is(err, ErrNotFound)) {
			t.Errorf("shared=%v: get with the source's node down = %v", shared, err)
		}
		err = s.DeleteSnapshot(ctx, source.DatabaseID, snapName)
		if shared != (err == nil) {
			t.Errorf("shared=%v: delete with the source's node down = %v", shared, err)
		}
	}
}
