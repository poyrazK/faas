package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneMigrationResumeRecoveryStore = (*PgStore)(nil)

func (s *PgStore) ListProjectEnvironmentCloneMigrationResumesForRetirement(ctx context.Context, l ProjectEnvironmentCloneLease, afterDispatchID string, limit int) ([]ProjectEnvironmentCloneMigrationResume, error) {
	if (afterDispatchID != "" && !checkpointWorkloadRetirementID(afterDispatchID)) || limit < 1 || limit > api.CheckpointWorkloadMigrationResumeRecoveryBatchMax {
		return nil, ErrInvalidArgument
	}
	var out []ProjectEnvironmentCloneMigrationResume
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		in, err := readCheckpointWorkloadRetirementTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if in.State != "requested" && in.State != "retired" {
			return ErrConflict
		}
		rows, err := sqlc.New().ListCloneCheckpointMigrationResumesForRetirement(ctx, tx, sqlc.ListCloneCheckpointMigrationResumesForRetirementParams{OperationID: mustPgUUID(op.ID), AfterDispatchID: afterDispatchID, PageLimit: int32(limit)})
		if err != nil {
			return mapErr(err)
		}
		out = make([]ProjectEnvironmentCloneMigrationResume, 0, len(rows))
		for _, row := range rows {
			out = append(out, ProjectEnvironmentCloneMigrationResume{DispatchID: pgUUIDString(row.DispatchID), AppID: pgUUIDString(row.AppID), DeploymentID: pgUUIDString(row.DeploymentID), WakeID: pgUUIDString(row.WakeID), RequestReason: row.RequestReason, DispatchedAt: row.DispatchedAt.Time.UTC(), SourceLease: ProjectEnvironmentCloneMigrationResumeSource{InstanceID: pgUUIDString(row.InstanceID), LeaseToken: row.LeaseToken, SourceNodeID: row.SourceNodeID, CreatedAt: row.LeaseCreatedAt.Time.UTC(), LeaseExpiresAt: row.LeaseExpiresAt.Time.UTC()}})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
