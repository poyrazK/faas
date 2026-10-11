package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RecordCloneCheckpointWakeDispatch durably retains original clone ownership
// before an ordinary wake RPC. An unknown outcome cannot authorize redispatch.
func (s *PgStore) RecordCloneCheckpointWakeDispatch(ctx context.Context, instanceID, appID, deploymentID, nodeID, method string) error {
	for _, id := range []string{instanceID, appID, deploymentID, nodeID} {
		if !qualificationRecoveryUUIDValid(id) {
			return ErrInvalidArgument
		}
	}
	if method != "cold_boot" && method != "restore" {
		return ErrInvalidArgument
	}
	err := sqlc.New().RecordCloneCheckpointWakeDispatch(ctx, s.pool, sqlc.RecordCloneCheckpointWakeDispatchParams{
		InstanceID: mustPgUUID(instanceID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(deploymentID), NodeID: mustPgUUID(nodeID), Method: method,
	})
	if err != nil {
		return fmt.Errorf("record clone wake dispatch: %w", err)
	}
	return nil
}
