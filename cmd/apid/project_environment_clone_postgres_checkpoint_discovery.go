package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/checkpointselection"
	"github.com/onebox-faas/faas/pkg/state"
)

// The source hold and ready maintenance receipt precede this private read.
// Retained original selection wins recovery, so retries never replace it with
// today's catalogue. The complete capture coordinator remains gated until all
// database/background writers and the common object/configuration point qualify.
func (s *server) discoverProjectEnvironmentClonePostgresCheckpointSelection(ctx context.Context, l state.ProjectEnvironmentCloneLease, plan capturedProjectEnvironmentDatabasePlan) (checkpointselection.Selection, error) {
	store, ok := s.store.(state.ProjectEnvironmentClonePostgresCheckpointSelectionStore)
	if !ok {
		return checkpointselection.Selection{}, managedpostgres.ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	scope, err := store.ProjectEnvironmentClonePostgresCheckpointSelectionScopeForLease(ctx, l, plan.source.ID)
	if err != nil {
		return checkpointselection.Selection{}, err
	}
	if err := validateCloneCheckpointSelectionPlan(scope, plan, l); err != nil {
		return checkpointselection.Selection{}, err
	}
	return s.projectEnvironmentClonePostgresCheckpointSelection(ctx, l, plan.source.ID,
		func(ctx context.Context, scope checkpointselection.Scope) (managedpostgres.CheckpointConnectionRequest, error) {
			if s.managedPostgres == nil {
				return managedpostgres.CheckpointConnectionRequest{}, managedpostgres.ErrUnavailable
			}
			if err := validateCloneCheckpointSelectionPlan(scope, plan, l); err != nil {
				return managedpostgres.CheckpointConnectionRequest{}, err
			}
			return s.managedPostgres.DiscoverCheckpointConnections(ctx, clonePostgresSnapshotDefinition(plan),
				managedpostgres.CheckpointMaintenance{OwnerToken: scope.MaintenanceID, SourceResourceID: scope.SourceDataResourceID,
					State: "ready", OwnerOID: scope.MaintenanceOwnerOID, DatabaseOID: scope.MaintenanceDatabaseOID},
				managedpostgres.CheckpointConnectionIdentity{OwnerToken: scope.OperationID, SourceResourceID: scope.SourceDataResourceID})
		})
}
