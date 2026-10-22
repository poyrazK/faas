package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectEnvironmentCloneManagedSecretBorrowers contains counts only. Credential
// references overlap provider-specific counts; no sealed values or references
// leave the transaction. Runtime observations include unmanaged secrets too.
type ProjectEnvironmentCloneManagedSecretBorrowers struct {
	OperationID, EnvironmentID, OwnershipSHA256 string
	NativePlanSHA256, NativeObservationSHA256   string
	PostgresSecrets, ObjectStorageSecrets       int64
	CredentialReferences, RuntimeObservations   int64
}

// ProjectEnvironmentCloneManagedSecretBorrowersForLease authenticates original
// configuration ownership and native workload closure before inventorying rows
// requiring separate withdrawal authority. It grants no deletion authority.
func (s *PgStore) ProjectEnvironmentCloneManagedSecretBorrowersForLease(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneManagedSecretBorrowers, error) {
	var out ProjectEnvironmentCloneManagedSecretBorrowers
	err := s.checkpointSourceCompensationTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
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
		row, err := sqlc.New().ReadCloneManagedSecretConfigurationBorrowers(ctx, tx, sqlc.ReadCloneManagedSecretConfigurationBorrowersParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment})
		if err != nil {
			return mapErr(err)
		}
		out = ProjectEnvironmentCloneManagedSecretBorrowers{OperationID: op.ID, EnvironmentID: native.Plan.EnvironmentID, OwnershipSHA256: ownership, NativePlanSHA256: cloneCheckpointSHA(native.Plan), NativeObservationSHA256: native.ObservationSHA256, PostgresSecrets: row.PostgresSecrets, ObjectStorageSecrets: row.ObjectStorageSecrets, CredentialReferences: row.CredentialReferences, RuntimeObservations: row.RuntimeObservations}
		return nil
	})
	if err != nil {
		return ProjectEnvironmentCloneManagedSecretBorrowers{}, err
	}
	return out, nil
}
