// adr: 581
package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func (p *serviceSnapshotRestoreProvider) DeleteSnapshotRestore(ctx context.Context, d RestoreSourceDefinition, r SnapshotRestoreDeletionRequest) (SnapshotRestoreDeletionObservation, error) {
	p.deletes++
	return p.deletionResult(ctx, d, r, false), nil
}
func (p *serviceSnapshotRestoreProvider) ObserveSnapshotRestoreDeletion(ctx context.Context, d RestoreSourceDefinition, r SnapshotRestoreDeletionRequest) (SnapshotRestoreDeletionObservation, error) {
	p.deletionReads++
	return p.deletionResult(ctx, d, r, true), nil
}
func (p *serviceSnapshotRestoreProvider) deletionResult(ctx context.Context, d RestoreSourceDefinition, r SnapshotRestoreDeletionRequest, done bool) SnapshotRestoreDeletionObservation {
	p.definition, p.deletionRequest = d, r
	_, p.deadline = ctx.Deadline()
	o := SnapshotRestoreDeletionObservation{ProviderResourceID: r.Restore.ExpectedTargetResourceID, OperationIDs: []string{"delete-b", "delete-a"}, Done: done}
	switch p.fault {
	case "target":
		o.ProviderResourceID = "project/foreign"
	case "missing_target":
		o.ProviderResourceID = ""
	case "ids":
		o.OperationIDs = []string{"other"}
	case "duplicates":
		o.OperationIDs = []string{"same", "same"}
	case "missing_ids":
		o.OperationIDs = nil
	case "empty_id":
		o.OperationIDs = []string{""}
	}
	return o
}

func snapshotRestoreDeletionServiceFixture(t *testing.T) (*Service, *serviceSnapshotRestoreProvider, RestoreSourceDefinition, SnapshotRestoreDeletionRequest) {
	t.Helper()
	s, p, d, restore := snapshotRestoreServiceFixture(t)
	restore.ExpectedTargetResourceID = "project/target"
	r := SnapshotRestoreDeletionRequest{Restore: restore, SnapshotCreatedAt: restore.Snapshot.PointInTime.Add(time.Minute), TargetCreatedAt: restore.Snapshot.PointInTime.Add(2 * time.Minute)}
	return s, p, d, r
}

func TestSnapshotRestoreDeletionServiceRetainsExactOwnershipDuringDisabledRollout(t *testing.T) {
	s, p, d, r := snapshotRestoreDeletionServiceFixture(t)
	s.provisioningEnabled = func() bool { return false }
	actual, err := s.DeleteSnapshotRestore(t.Context(), d, r)
	if err != nil || actual.Done || !slices.Equal(actual.OperationIDs, []string{"delete-a", "delete-b"}) || p.deletes != 1 || !p.deadline || p.definition != d || p.deletionRequest.Restore != r.Restore {
		t.Fatalf("owned deletion: %+v %v", actual, err)
	}
	r.OperationIDs = actual.OperationIDs
	actual, err = s.ObserveSnapshotRestoreDeletion(t.Context(), d, r)
	if err != nil || !actual.Done || p.deletes != 1 || p.deletionReads != 1 || !slices.Equal(p.deletionRequest.OperationIDs, r.OperationIDs) {
		t.Fatalf("independent deletion observation: %+v %v", actual, err)
	}
	for _, fault := range []string{"target", "missing_target", "ids", "duplicates", "missing_ids", "empty_id"} {
		p.fault = fault
		if _, err := s.ObserveSnapshotRestoreDeletion(t.Context(), d, r); !errors.Is(err, ErrConflict) {
			t.Fatalf("accepted %s deletion proof: %v", fault, err)
		}
	}
}

func TestSnapshotRestoreDeletionServiceRejectsUnpinnedAuthorityBeforeIO(t *testing.T) {
	s, p, d, r := snapshotRestoreDeletionServiceFixture(t)
	for _, fault := range []string{"target", "source", "fingerprint", "snapshot_time", "target_time", "future", "precision", "duplicate_ids"} {
		definition, request := d, r
		switch fault {
		case "target":
			request.Restore.ExpectedTargetResourceID = ""
		case "source":
			request.Restore.ExpectedTargetResourceID = d.DataResourceID
		case "fingerprint":
			definition.BackendFingerprint = "changed"
		case "snapshot_time":
			request.SnapshotCreatedAt = time.Time{}
		case "target_time":
			request.TargetCreatedAt = request.SnapshotCreatedAt.Add(-time.Second)
		case "future":
			request.TargetCreatedAt = time.Now().Add(time.Hour)
		case "precision":
			request.TargetCreatedAt = request.TargetCreatedAt.Add(time.Nanosecond)
		case "duplicate_ids":
			request.OperationIDs = []string{"same", "same"}
		}
		if _, err := s.DeleteSnapshotRestore(t.Context(), definition, request); err == nil || p.deletes != 0 {
			t.Fatalf("invalid %s reached provider: %v", fault, err)
		}
	}
}
