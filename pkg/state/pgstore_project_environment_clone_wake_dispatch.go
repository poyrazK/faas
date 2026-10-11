package state

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
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

var _ ProjectEnvironmentCloneWakeRecoveryStore = (*PgStore)(nil)

func (s *PgStore) ListProjectEnvironmentCloneWakeDispatchesForRetirement(ctx context.Context, l ProjectEnvironmentCloneLease, afterInstanceID string, limit int) ([]ProjectEnvironmentCloneWakeDispatch, error) {
	if (afterInstanceID != "" && !checkpointWorkloadRetirementID(afterInstanceID)) || limit < 1 || limit > api.CheckpointWorkloadWakeRecoveryBatchMax {
		return nil, ErrInvalidArgument
	}
	var out []ProjectEnvironmentCloneWakeDispatch
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		in, err := readCheckpointWorkloadRetirementTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if in.State != "requested" && in.State != "retired" {
			return ErrConflict
		}
		rows, err := sqlc.New().ListCloneCheckpointWakeDispatchesForRetirement(ctx, tx, sqlc.ListCloneCheckpointWakeDispatchesForRetirementParams{
			OperationID: mustPgUUID(op.ID), AfterInstanceID: afterInstanceID, PageLimit: int32(limit),
		})
		if err != nil {
			return mapErr(err)
		}
		out = make([]ProjectEnvironmentCloneWakeDispatch, 0, len(rows))
		for _, row := range rows {
			out = append(out, ProjectEnvironmentCloneWakeDispatch{
				InstanceID: pgUUIDString(row.InstanceID), AppID: pgUUIDString(row.AppID), DeploymentID: pgUUIDString(row.DeploymentID), NodeID: pgUUIDString(row.NodeID), WakeID: pgUUIDString(row.WakeID),
				RequestReason: row.RequestReason, Method: row.Method, DispatchedAt: row.DispatchedAt.Time.UTC(),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
