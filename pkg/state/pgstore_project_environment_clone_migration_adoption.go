package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RecordCloneCheckpointMigrationAdoption retains original source and destination
// request ownership before RPC. An unknown outcome never authorizes redispatch.
func (s *PgStore) RecordCloneCheckpointMigrationAdoption(ctx context.Context, instanceID, appID, deploymentID, destinationNodeID, memKey, stateKey, leaseToken string) error {
	for _, id := range []string{instanceID, appID, deploymentID, destinationNodeID} {
		if !checkpointWorkloadRetirementID(id) {
			return ErrInvalidArgument
		}
	}
	if memKey == "" || stateKey == "" || leaseToken == "" {
		return ErrInvalidArgument
	}
	err := sqlc.New().RecordCloneCheckpointMigrationAdoption(ctx, s.pool, sqlc.RecordCloneCheckpointMigrationAdoptionParams{
		InstanceID: mustPgUUID(instanceID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(deploymentID), DestinationNodeID: mustPgUUID(destinationNodeID), MemStorageKey: memKey, VmstateStorageKey: stateKey, LeaseToken: leaseToken,
	})
	if err != nil {
		return fmt.Errorf("record clone migration adoption: %w", err)
	}
	return nil
}
