package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func clonePostgresCopyReaderRequest(reader state.ProjectEnvironmentClonePostgresCopyReader, snapshot state.ProjectEnvironmentClonePostgresSnapshot, capture state.ProjectEnvironmentClonePostgresSnapshotRestore) managedpostgres.SnapshotCopyReaderRequest {
	return managedpostgres.SnapshotCopyReaderRequest{ResourceID: reader.OwnerID, ExpectedEndpointID: reader.EndpointID, ExpectedCreatedAt: reader.EndpointCreatedAt,
		RequestedAt: reader.RequestStartedAt, SnapshotCreatedAt: snapshot.SnapshotCreatedAt, CaptureCreatedAt: capture.TargetCreatedAt,
		Capture: managedpostgres.SnapshotRestoreRequest{ResourceID: capture.TargetOwnerID, ProviderSnapshotID: snapshot.ProviderSnapshotID,
			ExpectedTargetResourceID: capture.TargetProviderResourceID, Snapshot: clonePostgresSnapshotRequest(snapshot)}}
}

// Private compute preparation grants no SQL, inventory or dataset authority.
// Public capture remains gated until the complete materialization is qualified.
func (s *server) prepareProjectEnvironmentClonePostgresCopyReaders(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, bool, error) {
	if lease.Operation.Status != state.CloneOperationCapturing {
		return lease, false, state.ErrConflict
	}
	plans, err := s.capturedProjectEnvironmentDatabasePlans(ctx, lease.Operation)
	if err != nil {
		return lease, false, err
	}
	if len(plans) == 0 {
		return lease, true, nil
	}
	if _, _, err := validateCapturedProjectEnvironmentDatabaseResources(plans, lease.Operation.Resources); err != nil {
		return lease, false, err
	}
	readers, readerOK := s.store.(state.ProjectEnvironmentClonePostgresCopyReaderStore)
	snapshots, snapshotOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotStore)
	captures, captureOK := s.store.(state.ProjectEnvironmentClonePostgresSnapshotRestoreStore)
	leases, leaseOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	if !readerOK || !snapshotOK || !captureOK || !leaseOK || s.managedPostgres == nil {
		return lease, false, managedpostgres.ErrUnavailable
	}
	available := true
	for _, plan := range plans {
		renewed, err := leases.RenewProjectEnvironmentCloneLease(ctx, lease, projectEnvironmentCloneWorkerLeaseDuration)
		if err != nil {
			return lease, false, err
		}
		lease = renewed
		readerCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
		actual, err := s.prepareProjectEnvironmentClonePostgresCopyReader(readerCtx, lease, plan, readers, snapshots, captures)
		cancel()
		if err != nil {
			return lease, false, err
		}
		available = available && actual
	}
	return lease, available, nil
}

func (s *server) prepareProjectEnvironmentClonePostgresCopyReader(ctx context.Context, lease state.ProjectEnvironmentCloneLease, plan capturedProjectEnvironmentDatabasePlan, readers state.ProjectEnvironmentClonePostgresCopyReaderStore, snapshots state.ProjectEnvironmentClonePostgresSnapshotStore, captures state.ProjectEnvironmentClonePostgresSnapshotRestoreStore) (bool, error) {
	snapshot, err := snapshots.ProjectEnvironmentClonePostgresSnapshotForLease(ctx, lease, plan.source.ID)
	if err == nil {
		err = validateClonePostgresSnapshotPlan(lease.Operation, plan, snapshot)
	}
	if err == nil && snapshot.State != "retained" {
		err = state.ErrConflict
	}
	if err != nil {
		return false, err
	}
	capture, err := captures.ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx, lease, plan.source.ID)
	if err == nil && (capture.State != "adopted" || capture.AdoptedDatabaseID != capture.TargetOwnerID || capture.AdoptedAt.IsZero()) {
		err = state.ErrConflict
	}
	if err != nil {
		return false, err
	}
	definition := clonePostgresSnapshotDefinition(plan)
	reader, err := readers.ProjectEnvironmentClonePostgresCopyReaderForLease(ctx, lease, plan.source.ID)
	if errors.Is(err, state.ErrNotFound) || err == nil && reader.State == "reserved" {
		var limit int
		limit, err = s.managedPostgres.AdmitSnapshotCopyReaderReservation(ctx, lease.Operation.AccountID, definition)
		if err == nil {
			_, _, err = readers.ReserveProjectEnvironmentClonePostgresCopyReader(ctx, lease, plan.source.ID, limit)
		}
	}
	if err != nil {
		return false, err
	}
	reader, dispatch, err := readers.ClaimProjectEnvironmentClonePostgresCopyReaderRequest(ctx, lease, plan.source.ID)
	if err != nil {
		return false, err
	}
	request := clonePostgresCopyReaderRequest(reader, snapshot, capture)
	var observation managedpostgres.SnapshotCopyReaderObservation
	if dispatch {
		observation, err = s.managedPostgres.PrepareSnapshotCopyReader(ctx, lease.Operation.AccountID, definition, request)
	} else {
		observation, err = s.managedPostgres.FindSnapshotCopyReader(ctx, definition, request)
		if errors.Is(err, managedpostgres.ErrNotFound) {
			err = managedpostgres.ErrUnavailable
		}
	}
	if err != nil {
		return false, err
	}
	reader, err = readers.RecordProjectEnvironmentClonePostgresCopyReader(ctx, lease, plan.source.ID,
		state.ProjectEnvironmentClonePostgresCopyReaderObservation{EndpointID: observation.EndpointID, CreatedAt: observation.CreatedAt, Available: observation.Available})
	return err == nil && reader.State == "observed" && reader.Available, err
}
