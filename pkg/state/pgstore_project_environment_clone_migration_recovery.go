package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneMigrationRecoveryStore = (*PgStore)(nil)

func (s *PgStore) ListProjectEnvironmentCloneMigrationHoldingsForRetirement(ctx context.Context, l ProjectEnvironmentCloneLease, afterInstanceID string, limit int) ([]ProjectEnvironmentCloneMigrationHolding, error) {
	if (afterInstanceID != "" && !checkpointWorkloadRetirementID(afterInstanceID)) || limit < 1 || limit > api.CheckpointWorkloadMigrationRecoveryBatchMax {
		return nil, ErrInvalidArgument
	}
	var out []ProjectEnvironmentCloneMigrationHolding
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		in, err := readCheckpointWorkloadRetirementTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if in.State != "requested" && in.State != "retired" {
			return ErrConflict
		}
		rows, err := sqlc.New().ListCloneCheckpointMigrationHoldingsForRetirement(ctx, tx, sqlc.ListCloneCheckpointMigrationHoldingsForRetirementParams{
			OperationID: mustPgUUID(op.ID), AfterInstanceID: afterInstanceID, PageLimit: int32(limit),
		})
		if err != nil {
			return mapErr(err)
		}
		out = make([]ProjectEnvironmentCloneMigrationHolding, 0, len(rows))
		for _, row := range rows {
			out = append(out, ProjectEnvironmentCloneMigrationHolding{
				Lease: MigrationLease{InstanceID: pgUUIDString(row.InstanceID), LeaseToken: row.LeaseToken, SourceNodeID: row.SourceNodeID, CreatedAt: row.CreatedAt.Time.UTC(), LeaseExpiresAt: row.LeaseExpiresAt.Time.UTC(), MemStorageKey: row.MemStorageKey, VMStateStorageKey: row.VmstateStorageKey, Pending: row.Pending},
				AppID: pgUUIDString(row.AppID), DeploymentID: pgUUIDString(row.DeploymentID), WakeID: pgUUIDString(row.WakeID), SchedulingNodeID: pgUUIDString(row.SchedulingNodeID),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
