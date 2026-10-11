package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CleanupProjectEnvironmentCloneConfigurationForLease removes copied settings
// after authenticated native closure. It retains environment metadata, workload
// specs/heads/pins and all original ownership journals. Managed-secret withdrawal
// and runtime observation retirement require their own authority first.
func (s *PgStore) CleanupProjectEnvironmentCloneConfigurationForLease(ctx context.Context, l ProjectEnvironmentCloneLease) error {
	return s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		native, err := readCheckpointWorkloadRetirementTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if native.State != "retired" || native.Plan.EnvironmentID == "" {
			return ErrConflict
		}
		ownership, err := verifyCloneConfigurationOwnershipTx(ctx, tx, op, native.Plan.EnvironmentID)
		if err != nil {
			return err
		}
		if ownership == "" {
			return ErrConflict
		}
		q := sqlc.New()
		_, err = q.ReadCloneConfigurationCleanup(ctx, tx, mustPgUUID(op.ID))
		if err == nil {
			return nil
		} // The native read authenticated the exact saved post-state.
		if !errors.Is(mapErr(err), ErrNotFound) {
			return mapErr(err)
		}
		blocked, err := q.CloneConfigurationCleanupHasBorrowers(ctx, tx, sqlc.CloneConfigurationCleanupHasBorrowersParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment, EnvironmentID: mustPgUUID(native.Plan.EnvironmentID)})
		if err != nil {
			return mapErr(err)
		}
		if !blocked.Valid || blocked.Bool {
			return ErrConflict
		}
		borrowers, err := readCloneWorkloadBorrowersTx(ctx, tx, op, native)
		if err != nil {
			return err
		}
		if borrowers.blocked() {
			return ErrConflict
		}
		if err = deleteCloneConfigurationTx(ctx, tx, op, native.Plan.EnvironmentID); err != nil {
			return err
		}
		raw, err := q.ReadCloneConfigurationOwnershipSnapshot(ctx, tx, sqlc.ReadCloneConfigurationOwnershipSnapshotParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment, EnvironmentID: mustPgUUID(native.Plan.EnvironmentID)})
		if err != nil {
			return mapErr(err)
		}
		manifest, err := cloneConfigurationOwnershipManifest(raw)
		if err != nil {
			return err
		}
		if err = q.InsertCloneConfigurationCleanup(ctx, tx, sqlc.InsertCloneConfigurationCleanupParams{OperationID: mustPgUUID(op.ID), OwnershipSha256: ownership, NativePlanSha256: cloneCheckpointSHA(native.Plan), NativeObservationSha256: native.ObservationSHA256, Manifest: raw, ManifestSha256: cloneCheckpointSHA(manifest)}); err != nil {
			return mapErr(err)
		}
		// Verify deletion, retained rows, original ownership, and native proof before
		// commit; a failed receipt write or changed post-state rolls everything back.
		_, err = readCheckpointWorkloadRetirementTx(ctx, tx, op)
		return err
	})
}

func deleteCloneConfigurationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, environmentID string) error {
	q := sqlc.New()
	deletes := []func() error{
		func() error {
			return q.DeleteCloneConfigurationAppEnvironmentSecretRefSuppressions(ctx, tx, sqlc.DeleteCloneConfigurationAppEnvironmentSecretRefSuppressionsParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment})
		},
		func() error {
			return q.DeleteCloneConfigurationAppEnvironmentSecretRefs(ctx, tx, sqlc.DeleteCloneConfigurationAppEnvironmentSecretRefsParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment})
		},
		func() error {
			return q.DeleteCloneConfigurationAppEnvs(ctx, tx, sqlc.DeleteCloneConfigurationAppEnvsParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment})
		},
		func() error {
			return q.DeleteCloneConfigurationAppSecrets(ctx, tx, sqlc.DeleteCloneConfigurationAppSecretsParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment})
		},
		func() error { return q.DeleteCloneConfigurationFeatureFlagVersions(ctx, tx, mustPgUUID(environmentID)) },
		func() error {
			return q.DeleteCloneConfigurationProjectEnvironmentConfigVersions(ctx, tx, sqlc.DeleteCloneConfigurationProjectEnvironmentConfigVersionsParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment})
		},
		func() error {
			return q.DeleteCloneConfigurationProjectEnvironmentEdgePolicies(ctx, tx, sqlc.DeleteCloneConfigurationProjectEnvironmentEdgePoliciesParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment})
		},
		func() error {
			return q.DeleteCloneConfigurationProjectEnvironmentRoutePolicies(ctx, tx, sqlc.DeleteCloneConfigurationProjectEnvironmentRoutePoliciesParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment})
		},
	}
	for _, deleteRows := range deletes {
		if err := deleteRows(); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// This is a bounded catalogue witness, not whole-stage retirement authority.
// Read the native journal statically here to avoid recursive plan verification;
// the caller still reconstructs the source, materialization and deployment plan.
func verifyCloneConfigurationCleanupTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, environmentID, ownership string, original, current map[string]string) error {
	q := sqlc.New()
	row, err := q.ReadCloneConfigurationCleanup(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return checkpointPgCompensationDependencyError(err)
	}
	manifest, err := cloneConfigurationOwnershipManifest(row.Manifest)
	if err != nil || pgUUIDString(row.OperationID) != op.ID || row.OwnershipSha256 != ownership || cloneCheckpointSHA(manifest) != row.ManifestSha256 || !row.CleanedAt.Valid || !checkpointObjectRetirementTime(row.CleanedAt.Time.UTC()) || row.CleanedAt.Time.After(time.Now()) {
		return ErrConflict
	}
	empty := cloneCheckpointSHA([]any{})
	for table, hash := range manifest {
		switch table {
		case "project_environments", "project_environment_workload_heads", "project_environment_workload_specs":
			if hash != original[table] {
				return ErrConflict
			}
		default:
			if hash != empty {
				return ErrConflict
			}
		}
	}
	nativeRow, err := q.ReadCloneCheckpointWorkloadRetirement(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return checkpointPgCompensationDependencyError(err)
	}
	native, err := checkpointWorkloadRetirementFromSQL(nativeRow)
	if err != nil || native.State != "retired" || native.Plan.EnvironmentID != environmentID || row.NativePlanSha256 != cloneCheckpointSHA(native.Plan) || row.NativeObservationSha256 != native.ObservationSHA256 || row.CleanedAt.Time.Before(native.Observation.RetiredAt) {
		return ErrConflict
	}
	return verifyCloneWorkloadCleanupTx(ctx, tx, op, row, manifest, current, native)
}
