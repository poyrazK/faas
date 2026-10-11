package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// Called before native deletion and again before catalogue retirement. Known
// pending reader deletion may recover through independently qualified native
// deletion. An unknown creation must keep its capture available for discovery.
func (s *server) cleanupProjectEnvironmentClonePostgresCopyReader(ctx context.Context, lease state.ProjectEnvironmentCloneLease, plan capturedProjectEnvironmentDatabasePlan) (bool, error) {
	if lease.Operation.Status != state.CloneOperationCompensating {
		return false, state.ErrConflict
	}
	readers, ok := s.store.(state.ProjectEnvironmentClonePostgresCopyReaderStore)
	if !ok {
		return false, managedpostgres.ErrUnavailable
	}
	reader, err := readers.ProjectEnvironmentClonePostgresCopyReaderForLease(ctx, lease, plan.source.ID)
	if errors.Is(err, state.ErrNotFound) {
		return true, nil
	}
	if err != nil || reader.State == "retired" {
		return err == nil, err
	}
	reader, err = readers.BeginProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, lease, plan.source.ID)
	if err != nil {
		return false, err
	}
	if reader.RequestStartedAt.IsZero() {
		_, err = readers.FinishProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, lease, plan.source.ID, state.ProjectEnvironmentClonePostgresCopyReaderDeletion{Done: true})
		return err == nil, err
	}
	snapshots, snapshotOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotStore)
	captures, captureOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotRestoreStore)
	if !snapshotOK || !captureOK || s.managedPostgres == nil {
		return false, managedpostgres.ErrUnavailable
	}
	snapshot, err := snapshots.ProjectEnvironmentClonePostgresSnapshotForLease(ctx, lease, plan.source.ID)
	if err == nil {
		err = validateClonePostgresSnapshotPlan(lease.Operation, plan, snapshot)
	}
	if err != nil {
		return false, err
	}
	capture, err := captures.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, lease, plan.source.ID)
	if err != nil {
		return false, err
	}
	definition := clonePostgresSnapshotDefinition(plan)
	if reader.EndpointID == "" {
		observation, findErr := s.managedPostgres.FindSnapshotCopyReader(ctx, definition, clonePostgresCopyReaderRequest(reader, snapshot, capture))
		if errors.Is(findErr, managedpostgres.ErrNotFound) {
			findErr = managedpostgres.ErrUnavailable
		}
		if findErr != nil {
			return false, findErr
		}
		_, err = readers.RecordProjectEnvironmentClonePostgresCopyReader(ctx, lease, plan.source.ID,
			state.ProjectEnvironmentClonePostgresCopyReaderObservation{EndpointID: observation.EndpointID, CreatedAt: observation.CreatedAt})
		if err != nil {
			return false, err
		}
	}
	reader, dispatch, err := readers.ClaimProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, lease, plan.source.ID)
	if err != nil {
		return false, err
	}
	request, err := clonePostgresCopyReaderDeletionRequest(reader, snapshot, capture)
	if err != nil {
		return false, err
	}
	var observation managedpostgres.SnapshotCopyReaderDeletionObservation
	if dispatch {
		observation, err = s.managedPostgres.DeleteSnapshotCopyReader(ctx, definition, request)
		if err == nil {
			reader, err = readers.RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(ctx, lease, plan.source.ID, clonePostgresCopyReaderDeletionProof(observation))
		}
		if err != nil {
			return false, clonePostgresCopyReaderPendingDeletion(err)
		}
		request, err = clonePostgresCopyReaderDeletionRequest(reader, snapshot, capture)
	}
	if err == nil {
		observation, err = s.managedPostgres.ObserveSnapshotCopyReaderDeletion(ctx, definition, request)
	}
	if err != nil {
		return false, clonePostgresCopyReaderPendingDeletion(err)
	}
	proof := clonePostgresCopyReaderDeletionProof(observation)
	if len(proof.OperationIDs)+len(proof.CaptureOperationIDs) > 0 {
		_, err = readers.RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(ctx, lease, plan.source.ID, proof)
	}
	if err == nil && observation.Done {
		_, err = readers.FinishProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, lease, plan.source.ID, proof)
	}
	return err == nil && observation.Done, err
}

func clonePostgresCopyReaderPendingDeletion(err error) error {
	if errors.Is(err, managedpostgres.ErrUnavailable) {
		// The exact reader and first DELETE dispatch remain retained. Native
		// deletion can supply independent proof after a lost endpoint reply.
		return nil
	}
	return err
}

func clonePostgresCopyReaderDeletionProof(o managedpostgres.SnapshotCopyReaderDeletionObservation) state.ProjectEnvironmentClonePostgresCopyReaderDeletion {
	return state.ProjectEnvironmentClonePostgresCopyReaderDeletion{EndpointID: o.EndpointID, CreatedAt: o.CreatedAt,
		OperationIDs: o.OperationIDs, CaptureOperationIDs: o.CaptureOperationIDs, Done: o.Done}
}

func clonePostgresCopyReaderDeletionRequest(reader state.ProjectEnvironmentClonePostgresCopyReader, snapshot state.ProjectEnvironmentClonePostgresSnapshot, capture state.ProjectEnvironmentClonePostgresSnapshotRestore) (managedpostgres.SnapshotCopyReaderDeletionRequest, error) {
	r := managedpostgres.SnapshotCopyReaderDeletionRequest{Reader: clonePostgresCopyReaderRequest(reader, snapshot, capture), RequestedAt: reader.CleanupDispatchedAt}
	var err error
	r.OperationIDs, err = reader.DeletionOperationIDs()
	if err == nil {
		r.CaptureOperationIDs, err = reader.CaptureDeletionOperationIDs()
	}
	if err == nil && len(r.OperationIDs)+len(r.CaptureOperationIDs) == 0 && (capture.State == "deleting" || capture.State == "deleted") {
		r.CaptureOperationIDs, err = capture.DeletionOperationIDs()
	}
	return r, err
}
