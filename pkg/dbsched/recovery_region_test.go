package dbsched

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/dbarchive"
)

type recoveryFixtureVMM struct {
	*fakeVMM
	answer           string
	archiveTimeline  uint32
	checkedTimelines []uint32
	busyOnCheck      int
	unconfiguredNode string
}

func (v *recoveryFixtureVMM) CheckRecoveryArchive(_ context.Context, n Node, _ string, _ time.Time, _ int, _ bool, timeline uint32) (dbarchive.RestoreCheck, error) {
	if n.RecoveryRegion != "eu-west-1" {
		return dbarchive.RestoreCheck{}, errors.New("recovery used a node outside the destination fleet")
	}
	v.checkedTimelines = append(v.checkedTimelines, timeline)
	if n.ID == v.unconfiguredNode {
		return dbarchive.RestoreCheck{Outcome: dbarchive.RestoreUnverified, Reason: "recovery_archive_unconfigured"}, nil
	}
	if len(v.checkedTimelines) == v.busyOnCheck {
		return dbarchive.RestoreCheck{Outcome: dbarchive.RestoreUnverified, Reason: "archive_busy"}, nil
	}
	if timeline == 0 {
		timeline = max(1, v.archiveTimeline)
	}
	if v.archiveTimeline != 0 {
		v.archiveTimeline++
	}
	return dbarchive.RestoreCheck{Outcome: v.answer, Reason: "fixture", Timeline: timeline}, nil
}

func TestRegionalRestoreReservesAnArchiveVerifiedNodeWithCapacity(t *testing.T) {
	ctx := t.Context()
	store := NewMemStore()
	vmm := &recoveryFixtureVMM{fakeVMM: newFakeVMM(), answer: dbarchive.RestoreCovered, unconfiguredNode: "spacious"}
	vmm.archive = true
	seedNodes(t, store,
		Node{ID: "full", TargetURL: "unix:///full", Lifecycle: NodeActive, RecoveryRegion: "eu-west-1",
			AdmissionCeilingMB: 4096, VolumeCapacityBytes: 512 << 20},
		Node{ID: "spacious", TargetURL: "unix:///spacious", Lifecycle: NodeActive, RecoveryRegion: "eu-west-1",
			AdmissionCeilingMB: 4096, VolumeCapacityBytes: 30 << 30},
		Node{ID: "verified", TargetURL: "unix:///verified", Lifecycle: NodeActive, RecoveryRegion: "eu-west-1",
			AdmissionCeilingMB: 4096, VolumeCapacityBytes: 10 << 30})
	s := New(store, vmm, Config{SharedArchive: true}, nil)
	t.Cleanup(s.restoreWG.Wait)
	req := restoreRequest(VM{DatabaseID: uuid.NewString(), Major: 17, VolumeBytes: 1 << 30}, time.Now().Add(-time.Minute))
	req.RecoveryRegion = "eu-west-1"
	reserved, err := s.Restore(ctx, req)
	if err != nil || reserved.NodeID != "verified" || reserved.RestoreTimeline == 0 {
		t.Fatalf("restore placement: %+v, %v", reserved, err)
	}
	s.restoreWG.Wait()
	if got, err := store.Get(ctx, req.DatabaseID); err != nil || got.NodeID != "verified" || got.State != StateParked {
		t.Fatalf("restore outcome: %+v, %v", got, err)
	}
}

func TestRegionalRestoreDoesNotContactTheSource(t *testing.T) {
	ctx := t.Context()
	store := NewMemStore()
	vmm := &recoveryFixtureVMM{fakeVMM: newFakeVMM(), answer: dbarchive.RestoreWALGap}
	vmm.archive = true
	seedNodes(t, store,
		Node{ID: "home", TargetURL: "unix:///missing", Lifecycle: NodeUnavailable, AdmissionCeilingMB: 4096, VolumeCapacityBytes: 40 << 30},
		Node{ID: "other", TargetURL: "unix:///other", Lifecycle: NodeActive, RecoveryRegion: "us-east-1", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 30 << 30},
		Node{ID: "recovery", TargetURL: "unix:///recovery", Lifecycle: NodeActive, RecoveryRegion: "eu-west-1", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 10 << 30})
	s := New(store, vmm, Config{SharedArchive: true}, nil)
	// A source-region scheduler catalog is not required for recovery.
	source := VM{DatabaseID: uuid.NewString(), Major: 17, VolumeBytes: 1 << 30}
	req := restoreRequest(source, time.Now().Add(-time.Minute))
	req.RecoveryRegion = "eu-west-1"
	if _, err := s.Restore(ctx, req); !errors.Is(err, ErrConflict) {
		t.Fatalf("restore with missing WAL: %v", err)
	}
	if _, err := store.Get(ctx, req.DatabaseID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("uncovered restore reserved a VM: %v", err)
	}
	vmm.answer = dbarchive.RestoreCovered
	if vm, err := s.Restore(ctx, req); err != nil || vm.NodeID != "recovery" || !vm.RestoreFromRecovery {
		t.Fatalf("regional restore placement: %+v, %v", vm, err)
	}
	s.restoreWG.Wait()
	got, err := store.Get(ctx, req.DatabaseID)
	if err != nil || got.State != StateParked || got.LastBackupAt.IsZero() {
		t.Fatalf("regional restore settled to %+v, %v", got, err)
	}
	if len(vmm.restoreBoots) != 1 || !vmm.restoreBoots[0].Equal(req.Target.Truncate(time.Microsecond)) ||
		len(vmm.untilEnd) != 1 || vmm.untilEnd[0] || len(vmm.flushes) != 0 {
		t.Fatalf("restore boots=%v until-end=%v source-flushes=%v", vmm.restoreBoots, vmm.untilEnd, vmm.flushes)
	}
	if vm, err := s.Restore(ctx, req); err != nil || vm.DatabaseID != got.DatabaseID || vm.State != StateParked {
		t.Fatalf("retry = %+v, %v", vm, err)
	}
	if _, err := s.Move(ctx, got.DatabaseID, "other"); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("move escaped the recovery fleet: %v", err)
	}
}

func TestRegionalRestoreRechecksReservedTimeline(t *testing.T) {
	ctx := t.Context()
	store := NewMemStore()
	vmm := &recoveryFixtureVMM{fakeVMM: newFakeVMM(), answer: dbarchive.RestoreCovered, archiveTimeline: 2}
	vmm.archive = true
	seedNodes(t, store, Node{ID: "recovery", TargetURL: "unix:///recovery", Lifecycle: NodeActive,
		RecoveryRegion: "eu-west-1", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 10 << 30})
	s := New(store, vmm, Config{SharedArchive: true}, nil)
	source := VM{DatabaseID: uuid.NewString(), Major: 17, VolumeBytes: 1 << 30}
	req := restoreRequest(source, time.Now().Add(-time.Minute))
	req.RecoveryRegion = "eu-west-1"
	reserved, err := s.Restore(ctx, req)
	if err != nil || reserved.RestoreTimeline != 2 {
		t.Fatalf("reserve timeline: %+v, %v", reserved, err)
	}
	s.restoreWG.Wait()
	after, err := store.Get(ctx, req.DatabaseID)
	if err != nil || after.State != StateParked || !slices.Equal(vmm.checkedTimelines, []uint32{0, 2}) {
		t.Fatalf("replay on reserved timeline: %+v, %v, checks %v", after, err, vmm.checkedTimelines)
	}
}

func TestRegionalRestoreRetriesBusyArchive(t *testing.T) {
	ctx := t.Context()
	store := NewMemStore()
	vmm := &recoveryFixtureVMM{fakeVMM: newFakeVMM(), answer: dbarchive.RestoreCovered, archiveTimeline: 2, busyOnCheck: 2}
	vmm.archive = true
	seedNodes(t, store, Node{ID: "recovery", TargetURL: "unix:///recovery", Lifecycle: NodeActive,
		RecoveryRegion: "eu-west-1", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 10 << 30})
	s := New(store, vmm, Config{SharedArchive: true}, nil)
	req := restoreRequest(VM{DatabaseID: uuid.NewString(), Major: 17, VolumeBytes: 1 << 30}, time.Now().Add(-time.Minute))
	req.RecoveryRegion = "eu-west-1"
	if _, err := s.Restore(ctx, req); err != nil {
		t.Fatal(err)
	}
	s.restoreWG.Wait()
	after, err := store.Get(ctx, req.DatabaseID)
	if err != nil || after.State != StateParked || after.RestoreTimeline != 2 ||
		!slices.Equal(vmm.checkedTimelines, []uint32{0, 2, 2}) {
		t.Fatalf("restore after a busy archive: %+v, %v, checks %v", after, err, vmm.checkedTimelines)
	}
}

func TestRegionalRestoreResumesWithoutEnteringLegacyQueue(t *testing.T) {
	store := storeFactories(t)["pg"](t).(*PgStore)
	ctx := t.Context()
	seedNodes(t, store, Node{ID: "recovery", TargetURL: "unix:///recovery", Lifecycle: NodeActive,
		RecoveryRegion: "eu-west-1", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 10 << 30})
	placed, err := store.Place(ctx, VM{
		DatabaseID: uuid.NewString(), RecoveryRegion: "eu-west-1", RestoreFromRecovery: true,
		Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30,
		RestoreSource: uuid.NewString(), RestoreTarget: time.Now().Add(-time.Minute), RestoreTimeline: 2,
	})
	if err != nil || placed.State != stateRegionalRestore {
		t.Fatalf("reserve regional restore: %+v, %v", placed, err)
	}
	// The previous release's claim predicate must never match this row.
	tag, err := store.pool.Exec(ctx, `UPDATE database_vms SET restore_lease_until = now() + interval '1 minute'
		WHERE database_id = $1 AND state = 'provisioning' AND restore_source IS NOT NULL
		AND (restore_lease_until IS NULL OR restore_lease_until < now())`, mustUUID(placed.DatabaseID))
	if err != nil || tag.RowsAffected() != 0 {
		t.Fatalf("previous scheduler claimed regional restore: %v, %v", tag, err)
	}
	vmm := &recoveryFixtureVMM{fakeVMM: newFakeVMM(), answer: dbarchive.RestoreCovered}
	vmm.archive = true
	s := New(store, vmm, Config{SharedArchive: true}, nil)
	if err := s.ResumeRestores(ctx); err != nil {
		t.Fatal(err)
	}
	s.restoreWG.Wait()
	after, err := store.Get(ctx, placed.DatabaseID)
	if err != nil || after.State != StateParked || !slices.Equal(vmm.checkedTimelines, []uint32{2}) {
		t.Fatalf("resumed regional restore: %+v, %v, checks %v", after, err, vmm.checkedTimelines)
	}
}
