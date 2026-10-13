package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RecordCloneCheckpointMigrationResume retains source restart uncertainty before RPC.
func (s *PgStore) RecordCloneCheckpointMigrationResume(ctx context.Context, instanceID, sourceNodeID, leaseToken string) error {
	if !checkpointWorkloadRetirementID(instanceID) || sourceNodeID == "" || leaseToken == "" {
		return ErrInvalidArgument
	}
	if err := sqlc.New().RecordCloneCheckpointMigrationResume(ctx, s.pool, sqlc.RecordCloneCheckpointMigrationResumeParams{InstanceID: mustPgUUID(instanceID), SourceNodeID: sourceNodeID, LeaseToken: leaseToken}); err != nil {
		return fmt.Errorf("record clone migration resume: %w", err)
	}
	return nil
}
