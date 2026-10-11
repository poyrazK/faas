package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func clonePostgresSnapshotDefinition(plan capturedProjectEnvironmentDatabasePlan) managedpostgres.RestoreSourceDefinition {
	source := plan.source
	return managedpostgres.RestoreSourceDefinition{Spec: source.Spec, BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint,
		ProviderResourceID: source.ProviderResourceID, DataResourceID: source.DataResourceID}
}

func clonePostgresSnapshotRequest(receipt state.ProjectEnvironmentClonePostgresSnapshot) managedpostgres.SnapshotCaptureRequest {
	return managedpostgres.SnapshotCaptureRequest{ResourceID: state.ProjectEnvironmentClonePostgresSnapshotOwner(receipt.OperationID, receipt.SourceDatabaseID),
		SourceResourceID: receipt.SourceDataResourceID, PointInTime: receipt.CapturePoint, IdempotencyKey: "checkpoint-" + receipt.OperationID}
}

func validateClonePostgresSnapshotPlan(op state.ProjectEnvironmentCloneOperation, plan capturedProjectEnvironmentDatabasePlan, receipt state.ProjectEnvironmentClonePostgresSnapshot) error {
	source := plan.source
	if receipt.OperationID != op.ID || receipt.SourceDatabaseID != source.ID || receipt.SourceVersion != plan.hash ||
		receipt.BackendID != source.BackendID || receipt.BackendFingerprint != source.BackendFingerprint ||
		receipt.SourceProviderResourceID != source.ProviderResourceID || receipt.SourceDataResourceID != source.DataResourceID {
		return state.ErrConflict
	}
	return nil
}

// This phase requires the capture owner's already frozen common point. It does
// not choose a point, advance the operation, or release source writers. Durable
// receipts survive provider acknowledgement loss and worker takeover.
func (s *server) captureProjectEnvironmentClonePostgresSnapshots(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, error) {
	snapshots, ok := s.store.(state.ProjectEnvironmentClonePostgresSnapshotStore)
	leases, leasesOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	if !ok || !leasesOK || s.managedPostgres == nil {
		return lease, managedpostgres.ErrUnavailable
	}
	if lease.Operation.Status != state.CloneOperationCapturing {
		return lease, state.ErrConflict
	}
	plans, err := s.capturedProjectEnvironmentDatabasePlans(ctx, lease.Operation)
	if err != nil {
		return lease, err
	}
	_, point, err := validateCapturedProjectEnvironmentDatabaseResources(plans, lease.Operation.Resources)
	if err != nil {
		return lease, err
	}
	for _, plan := range plans {
		renewed, err := leases.RenewProjectEnvironmentCloneLease(ctx, lease, projectEnvironmentCloneWorkerLeaseDuration)
		if err != nil {
			return lease, err
		}
		lease = renewed
		captureCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
		receipt, err := snapshots.ReserveProjectEnvironmentClonePostgresSnapshot(captureCtx, lease, plan.source.ID)
		if err == nil {
			err = validateClonePostgresSnapshotPlan(lease.Operation, plan, receipt)
		}
		if err == nil && !receipt.CapturePoint.Equal(point) {
			err = state.ErrConflict
		}
		if err != nil {
			cancel()
			return lease, err
		}
		receipt, dispatch, err := snapshots.ClaimProjectEnvironmentClonePostgresSnapshotRequest(captureCtx, lease, plan.source.ID)
		if err != nil {
			cancel()
			return lease, err
		}
		definition, request := clonePostgresSnapshotDefinition(plan), clonePostgresSnapshotRequest(receipt)
		var actual managedpostgres.DatabaseSnapshot
		if dispatch {
			actual, err = s.managedPostgres.CaptureSnapshot(captureCtx, lease.Operation.AccountID, definition, request)
		} else {
			actual, err = s.managedPostgres.FindSnapshot(captureCtx, definition, request)
			if errors.Is(err, managedpostgres.ErrNotFound) {
				err = managedpostgres.ErrUnavailable
			}
			if err == nil && receipt.State == "requested" {
				actual, err = s.managedPostgres.RetainSnapshot(captureCtx, definition, request, actual.ProviderSnapshotID)
			}
		}
		if err == nil {
			_, err = snapshots.RecordProjectEnvironmentClonePostgresSnapshot(captureCtx, lease, plan.source.ID,
				state.ProjectEnvironmentClonePostgresSnapshotObservation{ProviderSnapshotID: actual.ProviderSnapshotID,
					SourceDataResourceID: actual.SourceResourceID, CapturePoint: actual.PointInTime, CreatedAt: actual.CreatedAt, ExpiresAt: actual.ExpiresAt})
		}
		cancel()
		if err != nil {
			return lease, err
		}
	}
	return lease, nil
}

// Recover cleanup from intent, including a snapshot whose creation succeeded
// before its ID reached Gregale. Discovery does not create a replacement copy.
// Other resource compensation still gates the final compensated state.
func (s *server) cleanupProjectEnvironmentClonePostgresSnapshots(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, bool, error) {
	plans, err := s.capturedProjectEnvironmentDatabasePlans(ctx, lease.Operation)
	if err != nil {
		return lease, false, err
	}
	if len(plans) == 0 {
		return lease, true, nil
	}
	snapshots, ok := s.store.(state.ProjectEnvironmentClonePostgresSnapshotStore)
	leases, leasesOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	if !ok || !leasesOK || s.managedPostgres == nil {
		return lease, false, managedpostgres.ErrUnavailable
	}
	if lease.Operation.Status != state.CloneOperationCompensating {
		return lease, false, state.ErrConflict
	}
	complete := true
	for _, plan := range plans {
		renewed, err := leases.RenewProjectEnvironmentCloneLease(ctx, lease, projectEnvironmentCloneWorkerLeaseDuration)
		if err != nil {
			return lease, false, err
		}
		lease = renewed
		cleanupCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
		receipt, err := snapshots.ProjectEnvironmentClonePostgresSnapshotForLease(cleanupCtx, lease, plan.source.ID)
		if errors.Is(err, state.ErrNotFound) {
			cancel()
			continue
		}
		if err == nil {
			err = validateClonePostgresSnapshotPlan(lease.Operation, plan, receipt)
		}
		if err != nil {
			cancel()
			return lease, false, err
		}
		if receipt.State == "deleted" {
			cancel()
			continue
		}
		receipt, err = snapshots.BeginProjectEnvironmentClonePostgresSnapshotCleanup(cleanupCtx, lease, plan.source.ID)
		if err != nil {
			cancel()
			return lease, false, err
		}
		accepted, err := s.managedPostgres.SnapshotCreationAcknowledgement(cleanupCtx, clonePostgresSnapshotDefinition(plan), clonePostgresSnapshotRequest(receipt))
		if err != nil {
			cancel()
			return lease, false, err
		}
		// The independent creation journal survives missing correctness metadata.
		// It does not replace the verified snapshot receipt used for publication.
		if accepted != nil {
			result, cleanupErr := s.managedPostgres.DeleteSnapshot(cleanupCtx, clonePostgresSnapshotDefinition(plan), clonePostgresSnapshotRequest(receipt), receipt.ProviderSnapshotID)
			if cleanupErr == nil && result.Done {
				_, cleanupErr = snapshots.FinishProjectEnvironmentClonePostgresSnapshotCleanup(cleanupCtx, lease, plan.source.ID)
			} else if cleanupErr == nil {
				complete = false
			}
			cancel()
			if cleanupErr != nil {
				return lease, false, cleanupErr
			}
			continue
		}
		// Persist discovered identity before deletion. If deletion or its
		// checkpoint loses acknowledgement, the next worker observes that ID.
		if receipt.ProviderSnapshotID == "" && !receipt.RequestStartedAt.IsZero() {
			actual, findErr := s.managedPostgres.FindSnapshot(cleanupCtx, clonePostgresSnapshotDefinition(plan), clonePostgresSnapshotRequest(receipt))
			if errors.Is(findErr, managedpostgres.ErrNotFound) {
				findErr = managedpostgres.ErrUnavailable
			}
			if findErr != nil {
				cancel()
				return lease, false, findErr
			}
			receipt, err = snapshots.RecordProjectEnvironmentClonePostgresSnapshot(cleanupCtx, lease, plan.source.ID,
				state.ProjectEnvironmentClonePostgresSnapshotObservation{ProviderSnapshotID: actual.ProviderSnapshotID,
					SourceDataResourceID: actual.SourceResourceID, CapturePoint: actual.PointInTime, CreatedAt: actual.CreatedAt, ExpiresAt: actual.ExpiresAt})
			if err != nil {
				cancel()
				return lease, false, err
			}
		}
		result := managedpostgres.DeleteResult{Done: true}
		if !receipt.RequestStartedAt.IsZero() {
			result, err = s.managedPostgres.DeleteSnapshot(cleanupCtx, clonePostgresSnapshotDefinition(plan), clonePostgresSnapshotRequest(receipt), receipt.ProviderSnapshotID)
		}
		if err == nil && result.Done {
			_, err = snapshots.FinishProjectEnvironmentClonePostgresSnapshotCleanup(cleanupCtx, lease, plan.source.ID)
		} else if err == nil {
			complete = false
		}
		cancel()
		if err != nil {
			return lease, false, err
		}
	}
	return lease, complete, nil
}
