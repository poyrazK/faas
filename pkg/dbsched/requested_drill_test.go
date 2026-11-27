package dbsched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbarchive"
)

type requestedDrillVMM struct {
	*fakeVMM
	archiveGap bool
	failDrop   bool
}

func (f *requestedDrillVMM) CheckArchiveRestore(_ context.Context, _ Node, _ string, _ time.Time, _ int) (dbarchive.RestoreCheck, error) {
	if f.archiveGap {
		return dbarchive.RestoreCheck{Outcome: dbarchive.RestoreWALGap}, nil
	}
	return dbarchive.RestoreCheck{Outcome: dbarchive.RestoreCovered}, nil
}

func (f *requestedDrillVMM) DropArchive(ctx context.Context, node Node, id string) error {
	if f.failDrop {
		f.failDrop = false
		return errors.New("archive temporarily unavailable")
	}
	return f.fakeVMM.DropArchive(ctx, node, id)
}

func TestRequestedRestoreDrillReplaysAndCleansUp(t *testing.T) {
	ctx := t.Context()
	store := storeFactories(t)["pg"](t).(*PgStore)
	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 20 << 30, Lifecycle: NodeActive})
	vmm := &requestedDrillVMM{fakeVMM: newFakeVMM()}
	vmm.archive = true
	s := New(store, vmm, Config{}, nil)
	t.Cleanup(s.restoreWG.Wait)
	source, err := s.Provision(ctx, ProvisionRequest{
		DatabaseID: uuid.NewString(), Major: 17, VcpuCount: 1, MemMiB: 384,
		VolumeBytes: 1 << 30, RestoreWindowSeconds: 86400,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, gap := range []bool{false, true} {
		vmm.restoreFail, vmm.archiveGap = gap, gap
		key := uuid.NewString()
		d, err := s.RequestRestoreDrill(ctx, source.DatabaseID, key)
		if err != nil || d.Result != DrillQueued || !d.TargetAt.IsZero() {
			t.Fatalf("queue: %+v, %v", d, err)
		}
		state, err := store.Get(ctx, source.DatabaseID)
		if err != nil || state.State != StateParked {
			t.Fatalf("queuing woke the source: %+v, %v", state, err)
		}
		retry, err := s.RequestRestoreDrill(ctx, source.DatabaseID, key)
		if err != nil || retry.ID != d.ID {
			t.Fatalf("queue retry: %+v, %v", retry, err)
		}
		if _, err := s.RequestRestoreDrill(ctx, source.DatabaseID, uuid.NewString()); !errors.Is(err, ErrConflict) {
			t.Fatalf("second active drill: %v", err)
		}
		if _, err := s.GetRestoreDrill(ctx, uuid.NewString(), d.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("read drill through another source: %v", err)
		}

		if err := s.advanceRequestedDrills(ctx, store); err != nil {
			t.Fatal(err)
		}
		s.restoreWG.Wait()
		// Model a lost acknowledgement after placing the temporary database.
		// A new sweep must use the same restore and marker.
		if _, err := store.pool.Exec(ctx, `UPDATE database_restore_drills SET result='queued' WHERE drill_id=$1`,
			mustUUID(d.ID)); err != nil {
			t.Fatal(err)
		}
		if err := s.advanceRequestedDrills(ctx, store); err != nil {
			t.Fatal(err)
		}
		running, err := s.GetRestoreDrill(ctx, source.DatabaseID, d.ID)
		if err != nil || running.Result != DrillRunning || running.TargetAt.IsZero() {
			t.Fatalf("running: %+v, %v", running, err)
		}

		vmm.failDrop = true
		if err := s.advanceRequestedDrills(ctx, store); err == nil {
			t.Fatal("expected retryable archive cleanup failure")
		}
		if _, err := store.Get(ctx, d.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("temporary VM survived cleanup attempt: %v", err)
		}
		if err := s.advanceRequestedDrills(ctx, store); err != nil {
			t.Fatal(err)
		}
		want, code := DrillOK, ""
		if gap {
			want, code = DrillFailed, "missing_wal"
		}
		retry, err = s.RequestRestoreDrill(ctx, source.DatabaseID, key)
		if err != nil || retry.ID != d.ID || retry.Result != want || retry.ErrorCode != code || retry.FinishedAt.IsZero() {
			t.Fatalf("terminal retry: %+v, %v, want %s/%s", retry, err, want, code)
		}
		if _, err := store.Get(ctx, d.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("terminal retry recreated a temporary VM: %v", err)
		}
		if err := s.Park(ctx, source.DatabaseID); err != nil {
			t.Fatal(err)
		}
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if len(vmm.restoreBoots) != 2 || countCalls(vmm.calls, "drop_archive") != 2 {
		t.Fatalf("expected one boot and archive removal per request, got %d boots, %v", len(vmm.restoreBoots), vmm.calls)
	}
}
