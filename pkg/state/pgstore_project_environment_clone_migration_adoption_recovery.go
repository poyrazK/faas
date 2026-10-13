package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneMigrationAdoptionRecoveryStore = (*PgStore)(nil)

func (s *PgStore) ListProjectEnvironmentCloneMigrationAdoptionsForRetirement(ctx context.Context, l ProjectEnvironmentCloneLease, afterDispatchID string, limit int) ([]ProjectEnvironmentCloneMigrationAdoption, error) {
	if (afterDispatchID != "" && !checkpointWorkloadRetirementID(afterDispatchID)) || limit < 1 || limit > api.CheckpointWorkloadMigrationAdoptionRecoveryBatchMax {
		return nil, ErrInvalidArgument
	}
	var out []ProjectEnvironmentCloneMigrationAdoption
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		in, err := readCheckpointWorkloadRetirementTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if in.State != "requested" && in.State != "retired" {
			return ErrConflict
		}
		rows, err := sqlc.New().ListCloneCheckpointMigrationAdoptionsForRetirement(ctx, tx, sqlc.ListCloneCheckpointMigrationAdoptionsForRetirementParams{OperationID: mustPgUUID(op.ID), AfterDispatchID: afterDispatchID, PageLimit: int32(limit)})
		if err != nil {
			return mapErr(err)
		}
		out = make([]ProjectEnvironmentCloneMigrationAdoption, 0, len(rows))
		for _, row := range rows {
			out = append(out, ProjectEnvironmentCloneMigrationAdoption{DispatchID: pgUUIDString(row.DispatchID), AppID: pgUUIDString(row.AppID), DeploymentID: pgUUIDString(row.DeploymentID), WakeID: pgUUIDString(row.WakeID), RequestReason: row.RequestReason, DestinationNodeID: pgUUIDString(row.DestinationNodeID), DispatchedAt: row.DispatchedAt.Time.UTC(), SourceLease: MigrationLease{InstanceID: pgUUIDString(row.InstanceID), LeaseToken: row.LeaseToken, SourceNodeID: row.SourceNodeID, CreatedAt: row.LeaseCreatedAt.Time.UTC(), LeaseExpiresAt: row.LeaseExpiresAt.Time.UTC(), MemStorageKey: row.MemStorageKey, VMStateStorageKey: row.VmstateStorageKey}})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
