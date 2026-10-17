package state

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CleanupProjectEnvironmentCloneEnvironmentForLease removes only the original
// unborrowed environment after settings/workload cleanup and native closure.
// Retained deployments and operational references require separate retirement;
// frozen captures, original ownership and all cleanup journals remain retained.
func (s *PgStore) CleanupProjectEnvironmentCloneEnvironmentForLease(ctx context.Context, l ProjectEnvironmentCloneLease) error {
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
		_, err = q.ReadCloneEnvironmentCleanup(ctx, tx, mustPgUUID(op.ID))
		if err == nil {
			return nil
		} // Native replay authenticated the saved post-state.
		if !errors.Is(mapErr(err), ErrNotFound) {
			return mapErr(err)
		}
		// Scope-only borrowers have no environment FK. Hold their writer locks
		// through deletion and receipt verification; the lease bounds the wait.
		if err = q.LockCloneEnvironmentCleanupScopeBorrowers(ctx, tx); err != nil {
			return mapErr(err)
		}
		if err = q.LockCloneBindingEnvironmentCleanupBorrowers(ctx, tx); err != nil {
			return mapErr(err)
		}

		// Native materialization replay locked the original environment FOR UPDATE.
		// Its foreign-key borrowers cannot race this check and disappear by cascade.
		if err = requireCloneEnvironmentUnborrowedTx(ctx, tx, op, native.Plan.EnvironmentID); err != nil {
			return err
		}
		removed, err := q.DeleteCloneOwnedEnvironment(ctx, tx, sqlc.DeleteCloneOwnedEnvironmentParams{ID: mustPgUUID(native.Plan.EnvironmentID), AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), Slug: op.TargetEnvironment})
		if err != nil {
			return mapErr(err)
		}
		if removed != 1 {
			return ErrConflict
		}
		raw, err := q.ReadCloneConfigurationOwnershipSnapshot(ctx, tx, sqlc.ReadCloneConfigurationOwnershipSnapshotParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment, EnvironmentID: mustPgUUID(native.Plan.EnvironmentID)})
		if err != nil {
			return mapErr(err)
		}
		manifest, err := cloneConfigurationOwnershipManifest(raw)
		if err != nil {
			return err
		}
		if err = q.InsertCloneEnvironmentCleanup(ctx, tx, sqlc.InsertCloneEnvironmentCleanupParams{OperationID: mustPgUUID(op.ID), EnvironmentID: mustPgUUID(native.Plan.EnvironmentID), WorkloadCleanupSha256: cloneWorkloadCleanupSHA(workloads), Manifest: raw, ManifestSha256: cloneCheckpointSHA(manifest)}); err != nil {
			return mapErr(err)
		}
		_, err = readCheckpointWorkloadRetirementTx(ctx, tx, op)
		return err
	})
}

func requireCloneEnvironmentUnborrowedTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, environmentID string) error {
	q := sqlc.New()
	row, err := q.ReadCloneEnvironmentMetadataBorrowers(ctx, tx, sqlc.ReadCloneEnvironmentMetadataBorrowersParams{OperationID: mustPgUUID(op.ID), ProjectID: mustPgUUID(op.ProjectID), EnvironmentID: mustPgUUID(environmentID), Environment: op.TargetEnvironment})
	if err != nil {
		return mapErr(err)
	}
	if row.OwnedDeployments != 0 || row.ForeignDeployments != 0 {
		return fmt.Errorf("clone environment retains deployments: %w", ErrConflict)
	}
	for table, count := range cloneEnvironmentBorrowerCounts(row) {
		if count != 0 {
			return fmt.Errorf("clone environment borrowed by %s: %w", table, ErrConflict)
		}
	}
	blocked, err := q.CloneEnvironmentCleanupHasScopeBorrowers(ctx, tx, sqlc.CloneEnvironmentCleanupHasScopeBorrowersParams{OperationID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), EnvironmentID: mustPgUUID(environmentID), Environment: op.TargetEnvironment})
	if err != nil {
		return mapErr(err)
	}
	if blocked {
		return fmt.Errorf("clone environment retains scoped references: %w", ErrConflict)
	}
	return nil
}

func verifyCloneEnvironmentCleanupTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, workloads sqlc.ProjectEnvironmentCloneWorkloadCleanup, prior, current map[string]string, native ProjectEnvironmentCloneCheckpointWorkloadRetirement) error {
	q := sqlc.New()
	row, err := q.ReadCloneEnvironmentCleanup(ctx, tx, mustPgUUID(op.ID))
	if errors.Is(mapErr(err), ErrNotFound) {
		if !reflect.DeepEqual(prior, current) {
			return ErrConflict
		}
		return nil
	}
	if err != nil {
		return mapErr(err)
	}
	manifest, err := cloneConfigurationOwnershipManifest(row.Manifest)
	if err != nil || pgUUIDString(row.OperationID) != op.ID || pgUUIDString(row.EnvironmentID) != native.Plan.EnvironmentID || row.WorkloadCleanupSha256 != cloneWorkloadCleanupSHA(workloads) || cloneCheckpointSHA(manifest) != row.ManifestSha256 || !reflect.DeepEqual(manifest, current) || !row.CleanedAt.Valid || !checkpointObjectRetirementTime(row.CleanedAt.Time.UTC()) || row.CleanedAt.Time.After(time.Now()) || row.CleanedAt.Time.Before(workloads.CleanedAt.Time) {
		return ErrConflict
	}
	expected := make(map[string]string, len(prior))
	for key, value := range prior {
		expected[key] = value
	}
	expected["project_environments"] = cloneCheckpointSHA([]any{})
	if !reflect.DeepEqual(expected, manifest) {
		return ErrConflict
	}
	replacement, err := q.CloneEnvironmentCleanupHasReplacement(ctx, tx, sqlc.CloneEnvironmentCleanupHasReplacementParams{EnvironmentID: mustPgUUID(native.Plan.EnvironmentID), ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment})
	if err != nil {
		return mapErr(err)
	}
	if replacement {
		return ErrConflict
	}
	return requireCloneEnvironmentUnborrowedTx(ctx, tx, op, native.Plan.EnvironmentID)
}
