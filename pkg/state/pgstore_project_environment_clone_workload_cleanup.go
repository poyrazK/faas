package state

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CleanupProjectEnvironmentCloneWorkloadsForLease removes unborrowed original
// heads, specs and deployment pins after settings cleanup and native closure.
// Environment metadata, deployments and original ownership evidence are retained.
func (s *PgStore) CleanupProjectEnvironmentCloneWorkloadsForLease(ctx context.Context, l ProjectEnvironmentCloneLease) error {
	return s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		native, err := readCheckpointWorkloadRetirementTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if native.State != "retired" || native.Plan.EnvironmentID == "" {
			return ErrConflict
		}
		q := sqlc.New()
		config, err := q.ReadCloneConfigurationCleanup(ctx, tx, mustPgUUID(op.ID))
		if err != nil {
			return checkpointPgCompensationDependencyError(mapErr(err))
		}
		_, err = q.ReadCloneWorkloadCleanup(ctx, tx, mustPgUUID(op.ID))
		if err == nil {
			return nil
		} // Native replay authenticated the exact saved post-state.
		if !errors.Is(mapErr(err), ErrNotFound) {
			return mapErr(err)
		}
		// Block new foreign-key borrowers before inventory; removal must never
		// cascade over a reference inserted after the emptiness check.
		if _, err = q.LockCloneWorkloadCleanupSpecs(ctx, tx, mustPgUUID(native.Plan.EnvironmentID)); err != nil {
			return mapErr(err)
		}
		borrowers, err := readCloneWorkloadBorrowersTx(ctx, tx, op, native)
		if err != nil {
			return err
		}
		if borrowers.blocked() {
			return ErrConflict
		}
		foreign, err := q.CloneConfigurationCleanupHasBorrowers(ctx, tx, sqlc.CloneConfigurationCleanupHasBorrowersParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment, EnvironmentID: mustPgUUID(native.Plan.EnvironmentID)})
		if err != nil {
			return mapErr(err)
		}
		if !foreign.Valid || foreign.Bool {
			return ErrConflict
		}
		if err = q.DeleteCloneOwnedWorkloadPins(ctx, tx, sqlc.DeleteCloneOwnedWorkloadPinsParams{EnvironmentID: mustPgUUID(native.Plan.EnvironmentID), OperationID: mustPgUUID(op.ID)}); err != nil {
			return mapErr(err)
		}
		if err = q.DeleteCloneOwnedWorkloadHeads(ctx, tx, mustPgUUID(native.Plan.EnvironmentID)); err != nil {
			return mapErr(err)
		}
		if err = q.DeleteCloneOwnedWorkloadSpecs(ctx, tx, mustPgUUID(native.Plan.EnvironmentID)); err != nil {
			return mapErr(err)
		}
		raw, err := q.ReadCloneConfigurationOwnershipSnapshot(ctx, tx, sqlc.ReadCloneConfigurationOwnershipSnapshotParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment, EnvironmentID: mustPgUUID(native.Plan.EnvironmentID)})
		if err != nil {
			return mapErr(err)
		}
		manifest, err := cloneConfigurationOwnershipManifest(raw)
		if err != nil {
			return err
		}
		if err = q.InsertCloneWorkloadCleanup(ctx, tx, sqlc.InsertCloneWorkloadCleanupParams{OperationID: mustPgUUID(op.ID), ConfigurationCleanupSha256: cloneConfigurationCleanupSHA(config), Manifest: raw, ManifestSha256: cloneCheckpointSHA(manifest)}); err != nil {
			return mapErr(err)
		}
		_, err = readCheckpointWorkloadRetirementTx(ctx, tx, op)
		return err
	})
}

func cloneConfigurationCleanupSHA(row sqlc.ProjectEnvironmentCloneConfigurationCleanup) string {
	return cloneCheckpointSHA(struct {
		OperationID, OwnershipSHA256, NativePlanSHA256, NativeObservationSHA256, ManifestSHA256 string
		CleanedAt                                                                               time.Time
	}{pgUUIDString(row.OperationID), row.OwnershipSha256, row.NativePlanSha256, row.NativeObservationSha256, row.ManifestSha256, row.CleanedAt.Time.UTC()})
}

// A missing row preserves the earlier cleanup contract. A retained row must
// authenticate the earlier receipt and exact post-state; it is never a shortcut
// based on labels or missing live rows, and never authorizes native work.
func verifyCloneWorkloadCleanupTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, config sqlc.ProjectEnvironmentCloneConfigurationCleanup, prior, current map[string]string, native ProjectEnvironmentCloneCheckpointWorkloadRetirement) error {
	q := sqlc.New()
	row, err := q.ReadCloneWorkloadCleanup(ctx, tx, mustPgUUID(op.ID))
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
	if err != nil || pgUUIDString(row.OperationID) != op.ID || row.ConfigurationCleanupSha256 != cloneConfigurationCleanupSHA(config) || cloneCheckpointSHA(manifest) != row.ManifestSha256 || !row.CleanedAt.Valid || !checkpointObjectRetirementTime(row.CleanedAt.Time.UTC()) || row.CleanedAt.Time.After(time.Now()) || row.CleanedAt.Time.Before(config.CleanedAt.Time) {
		return ErrConflict
	}
	expected := make(map[string]string, len(prior))
	for key, value := range prior {
		expected[key] = value
	}
	expected["project_environment_workload_heads"], expected["project_environment_workload_specs"] = cloneCheckpointSHA([]any{}), cloneCheckpointSHA([]any{})
	if !reflect.DeepEqual(expected, manifest) {
		return ErrConflict
	}
	pins, err := q.CloneRetiredWorkloadHasPins(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return mapErr(err)
	}
	if pins {
		return ErrConflict
	}
	borrowers, err := readCloneWorkloadBorrowersTx(ctx, tx, op, native)
	if err != nil {
		return err
	}
	if borrowers.blocked() {
		return ErrConflict
	}
	return verifyCloneEnvironmentCleanupTx(ctx, tx, op, row, manifest, current, native)
}
