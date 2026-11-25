package dbsched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/dbarchive"
)

type recoveryFixtureVMM struct {
	*fakeVMM
	answer string
}

func (v *recoveryFixtureVMM) CheckRecoveryArchive(_ context.Context, n Node, _ string, _ time.Time, _ int, _ bool) (dbarchive.RestoreCheck, error) {
	if n.RecoveryRegion != "eu-west-1" {
		return dbarchive.RestoreCheck{}, errors.New("recovery used a node outside the destination fleet")
	}
	return dbarchive.RestoreCheck{Outcome: v.answer, Reason: "fixture"}, nil
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
