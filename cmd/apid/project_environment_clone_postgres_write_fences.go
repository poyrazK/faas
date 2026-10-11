package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// Persist abandonment before recovering maintenance or touching the remote
// barrier. Only a subsequent independent read of its exact terminal record
// releases the operation's source hold. Missing records and unknown replies
// retain recovery authority and cannot advance copying or full compensation.
func (s *server) abandonProjectEnvironmentClonePostgresWriteFences(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, error) {
	if lease.Operation.Status != state.CloneOperationCompensating {
		return lease, state.ErrConflict
	}
	store, ok := s.store.(state.ProjectEnvironmentClonePostgresWriteFenceStore)
	if !ok {
		return lease, errCloneCompensationUnavailable
	}
	fences, err := store.ProjectEnvironmentClonePostgresWriteFencesForLease(ctx, lease)
	if err != nil || len(fences) == 0 {
		return lease, err
	}
	plans, err := s.capturedProjectEnvironmentDatabasePlans(ctx, lease.Operation)
	if err != nil {
		return lease, err
	}
	byID := make(map[string]capturedProjectEnvironmentDatabasePlan, len(plans))
	for _, plan := range plans {
		byID[plan.source.ID] = plan
	}
	for _, fence := range fences {
		if fence.State == "released" {
			continue
		}
		plan, found := byID[fence.SourceDatabaseID]
		if !found || fence.OperationID != lease.Operation.ID || fence.SourceVersion != plan.hash ||
			fence.BackendID != plan.source.BackendID || fence.BackendFingerprint != plan.source.BackendFingerprint ||
			fence.SourceProviderResourceID != plan.source.ProviderResourceID || fence.SourceDataResourceID != plan.source.DataResourceID {
			return lease, state.ErrConflict
		}
		fence, err = store.BeginProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx, lease, plan.source.ID)
		if err != nil {
			return lease, err
		}
		var maintenance state.ProjectEnvironmentClonePostgresMaintenance
		lease, maintenance, err = s.prepareProjectEnvironmentClonePostgresMaintenance(ctx, lease, plan)
		if err != nil {
			return lease, err
		}
		stepCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
		definition, owner := clonePostgresSnapshotDefinition(plan), managedpostgres.CheckpointConnectionIdentity{
			OwnerToken: fence.OperationID, SourceResourceID: fence.SourceDataResourceID}
		_, err = s.managedPostgres.AbandonCheckpointConnections(stepCtx, definition, clonePostgresMaintenanceObservation(maintenance), owner)
		var actual managedpostgres.CheckpointConnectionTerminal
		if err == nil {
			actual, err = s.managedPostgres.ObserveCheckpointConnections(stepCtx, definition, clonePostgresMaintenanceObservation(maintenance), owner)
		}
		if err == nil {
			_, err = store.FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(stepCtx, lease, plan.source.ID,
				state.ProjectEnvironmentClonePostgresFenceAbandonment{OwnerToken: actual.OwnerToken, SourceDataResourceID: actual.SourceResourceID,
					State: actual.State, ReleasedAt: actual.ReleasedAt})
		}
		cancel()
		if err != nil {
			return lease, err
		}
	}
	return lease, nil
}
