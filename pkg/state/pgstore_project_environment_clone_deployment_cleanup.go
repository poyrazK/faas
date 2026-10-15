package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CleanupProjectEnvironmentCloneDeploymentsForLease removes unchanged original
// artifacts only after native retirement and authenticated workload cleanup.
// Copied sidecars and the exact runtime environment identity may cascade; all
// other retained references block deletion. Original journals remain retained.
func (s *PgStore) CleanupProjectEnvironmentCloneDeploymentsForLease(ctx context.Context, l ProjectEnvironmentCloneLease) error {
	return s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		native, err := readCheckpointWorkloadRetirementTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if native.State != "retired" || native.Plan.EnvironmentID == "" {
			return ErrConflict
		}
		q := sqlc.New()
		workloads, err := q.ReadCloneWorkloadCleanup(ctx, tx, mustPgUUID(op.ID))
		if err != nil {
			return checkpointPgCompensationDependencyError(mapErr(err))
		}
		_, err = q.ReadCloneDeploymentCleanup(ctx, tx, mustPgUUID(op.ID))
		if err == nil {
			return nil
		} // Native replay authenticated the saved transition.
		if !errors.Is(mapErr(err), ErrNotFound) {
			return mapErr(err)
		}
		// Native replay locked the target artifacts FOR UPDATE. Soft references have
		// no foreign-key lock, so also exclude their writers until the receipt commits.
		if err = q.LockCloneDeploymentCleanupBorrowerWriters(ctx, tx); err != nil {
			return mapErr(err)
		}
		if err = q.LockCloneDeploymentCleanupRetirementWriters(ctx, tx); err != nil {
			return mapErr(err)
		}
		if err = requireCloneDeploymentUnborrowedTx(ctx, tx, op, native.Plan, false); err != nil {
			return err
		}
		removed, err := q.DeleteCloneOwnedDeployments(ctx, tx, sqlc.DeleteCloneOwnedDeploymentsParams{OperationID: mustPgUUID(op.ID), Environment: op.TargetEnvironment})
		if err != nil {
			return mapErr(err)
		}
		if removed != cloneRetirementDeploymentCount(native.Plan) {
			return ErrConflict
		}
		if err = q.InsertCloneDeploymentCleanup(ctx, tx, sqlc.InsertCloneDeploymentCleanupParams{OperationID: mustPgUUID(op.ID), EnvironmentID: mustPgUUID(native.Plan.EnvironmentID), WorkloadCleanupSha256: cloneWorkloadCleanupSHA(workloads), NativePlanSha256: cloneCheckpointSHA(native.Plan), NativeObservationSha256: native.ObservationSHA256, RemovedDeployments: removed}); err != nil {
			return mapErr(err)
		}
		_, err = readCheckpointWorkloadRetirementTx(ctx, tx, op)
		return err
	})
}

func cloneRetirementDeploymentCount(p ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) int64 {
	var n int64
	for _, w := range p.Workloads {
		if w.TargetDeploymentID != "" {
			n++
		}
	}
	return n
}

func cloneDeploymentBorrowerCounts(raw []byte) (map[string]int64, error) {
	var counts map[string]int64
	if json.Unmarshal(raw, &counts) != nil || counts == nil {
		return nil, ErrConflict
	}
	for _, n := range counts {
		if n < 0 {
			return nil, ErrConflict
		}
	}
	return counts, nil
}

func requireCloneDeploymentUnborrowedTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, p ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan, removed bool) error {
	q := sqlc.New()
	for _, w := range p.Workloads {
		if w.TargetDeploymentID == "" {
			continue
		}
		raw, err := q.ReadCloneDeploymentBorrowers(ctx, tx, mustPgUUID(w.TargetDeploymentID))
		if err != nil {
			return mapErr(err)
		}
		counts, err := cloneDeploymentBorrowerCounts(raw)
		if err != nil {
			return err
		}
		for name, n := range counts {
			if !removed && (name == "deployment_sidecar_layers_deployment_id_fkey" || name == "deployment_sidecar_secret_reload_signals_deployment_id_fkey" || name == "deployment_runtime_environment_owners_deployment_id_fkey") {
				continue
			}
			if n != 0 {
				return fmt.Errorf("clone deployment borrowed by %s: %w", name, ErrConflict)
			}
		}
		if !removed {
			owner, err := q.CloneDeploymentCleanupHasRuntimeOwner(ctx, tx, sqlc.CloneDeploymentCleanupHasRuntimeOwnerParams{DeploymentID: mustPgUUID(w.TargetDeploymentID), EnvironmentID: mustPgUUID(p.EnvironmentID)})
			if err != nil {
				return mapErr(err)
			}
			if !owner {
				return ErrConflict
			}
		}
		raw, err = q.ReadCloneDeploymentSoftBorrowers(ctx, tx, sqlc.ReadCloneDeploymentSoftBorrowersParams{OperationID: mustPgUUID(op.ID), DeploymentID: mustPgUUID(w.TargetDeploymentID)})
		if err != nil {
			return mapErr(err)
		}
		counts, err = cloneDeploymentBorrowerCounts(raw)
		if err != nil {
			return err
		}
		for name, n := range counts {
			if n != 0 {
				return fmt.Errorf("clone deployment retains %s: %w", name, ErrConflict)
			}
		}
	}
	return nil
}

// Authentication is based on the original native journal and exact workload
// predecessor, never an absent deployment or an operation label alone. This
// function is used only in retirement replay; publication still needs artifacts.
func verifyCloneDeploymentCleanupTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, p ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) (bool, error) {
	q := sqlc.New()
	row, err := q.ReadCloneDeploymentCleanup(ctx, tx, mustPgUUID(op.ID))
	if errors.Is(mapErr(err), ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, mapErr(err)
	}
	if err = q.LockCloneDeploymentCleanupBorrowerWriters(ctx, tx); err != nil {
		return false, mapErr(err)
	}
	orphan, err := q.CloneCheckpointWorkloadRetirementHasOrphanDeployments(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return false, mapErr(err)
	}
	if orphan {
		return false, ErrConflict
	}
	original, err := q.ReadCloneCheckpointWorkloadRetirement(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return false, checkpointPgCompensationDependencyError(mapErr(err))
	}
	native, err := checkpointWorkloadRetirementFromSQL(original)
	if err != nil || native.State != "retired" || !reflect.DeepEqual(native.Plan, p) {
		return false, ErrConflict
	}
	workloads, err := q.ReadCloneWorkloadCleanup(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return false, checkpointPgCompensationDependencyError(mapErr(err))
	}
	if pgUUIDString(row.OperationID) != op.ID || pgUUIDString(row.EnvironmentID) != p.EnvironmentID || row.WorkloadCleanupSha256 != cloneWorkloadCleanupSHA(workloads) || row.NativePlanSha256 != cloneCheckpointSHA(p) || row.NativeObservationSha256 != native.ObservationSHA256 || row.RemovedDeployments != cloneRetirementDeploymentCount(p) || !row.CleanedAt.Valid || !checkpointObjectRetirementTime(row.CleanedAt.Time.UTC()) || row.CleanedAt.Time.After(time.Now()) || row.CleanedAt.Time.Before(workloads.CleanedAt.Time) || row.CleanedAt.Time.Before(native.Observation.RetiredAt) {
		return false, ErrConflict
	}
	live, err := q.CloneDeploymentCleanupHasLiveTarget(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return false, mapErr(err)
	}
	if live {
		return false, ErrConflict
	}
	return true, requireCloneDeploymentUnborrowedTx(ctx, tx, op, p, true)
}
