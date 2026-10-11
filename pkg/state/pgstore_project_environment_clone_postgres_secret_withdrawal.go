package state

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type clonePostgresWithdrawalOwnership struct {
	EnvironmentID, SHA256 string
}

// Called before removing envelopes, under the configuration clock and original
// worker/App locks. Legacy materializations never gain a backfilled witness.
func prepareClonePostgresSecretWithdrawalTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (clonePostgresWithdrawalOwnership, error) {
	var out clonePostgresWithdrawalOwnership
	q := sqlc.New()
	borrowers, err := q.ReadCloneManagedSecretConfigurationBorrowers(ctx, tx, sqlc.ReadCloneManagedSecretConfigurationBorrowersParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment})
	if err != nil {
		return out, mapErr(err)
	}
	if borrowers.RuntimeObservations != 0 {
		return out, ErrConflict
	}
	row, err := q.ReadCloneConfigurationOwnership(ctx, tx, mustPgUUID(op.ID))
	if errors.Is(mapErr(err), ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return out, mapErr(err)
	}
	out.EnvironmentID = pgUUIDString(row.EnvironmentID)
	env, err := replayCloneMaterializationTx(ctx, tx, op)
	if err != nil {
		return out, err
	}
	if env.ID != out.EnvironmentID {
		return out, ErrConflict
	}
	out.SHA256, err = verifyCloneConfigurationOwnershipTx(ctx, tx, op, out.EnvironmentID)
	if err != nil {
		return out, err
	}
	blocked, err := q.CloneManagedSecretWithdrawalHasExternalOwners(ctx, tx, row.EnvironmentID)
	if err != nil {
		return out, mapErr(err)
	}
	if !blocked.Valid || blocked.Bool {
		return out, ErrConflict
	}
	_, err = q.ReadClonePostgresSecretWithdrawal(ctx, tx, mustPgUUID(op.ID))
	if !errors.Is(mapErr(err), ErrNotFound) {
		if err != nil {
			return out, mapErr(err)
		}
		return out, ErrConflict
	}
	return out, nil
}

func saveClonePostgresSecretWithdrawalTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, owner clonePostgresWithdrawalOwnership, publicationSHA string) error {
	if owner.EnvironmentID == "" {
		return nil
	}
	q := sqlc.New()
	publication, err := q.ReadCloneCheckpointPgPublication(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return mapErr(err)
	}
	if publication.State != "withdrawn" || publication.PlanSha256 != publicationSHA || !publication.WithdrawnAt.Valid {
		return ErrConflict
	}
	raw, err := q.ReadCloneConfigurationOwnershipSnapshot(ctx, tx, sqlc.ReadCloneConfigurationOwnershipSnapshotParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment, EnvironmentID: mustPgUUID(owner.EnvironmentID)})
	if err != nil {
		return mapErr(err)
	}
	manifest, err := cloneConfigurationOwnershipManifest(raw)
	if err != nil {
		return err
	}
	if err = q.InsertClonePostgresSecretWithdrawal(ctx, tx, sqlc.InsertClonePostgresSecretWithdrawalParams{OperationID: mustPgUUID(op.ID), OwnershipSha256: owner.SHA256, PublicationPlanSha256: publicationSHA, PublicationWithdrawnAt: publication.WithdrawnAt, Manifest: raw, ManifestSha256: cloneCheckpointSHA(manifest)}); err != nil {
		return mapErr(err)
	}
	_, err = verifyCloneConfigurationOwnershipTx(ctx, tx, op, owner.EnvironmentID)
	return err
}

// Authenticate the saved transition independently of current copied settings;
// subsequent cleanup has its own exact post-state. No native IO occurs here.
func clonePostgresSecretWithdrawalManifestTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, ownership string, ownedAt time.Time, original map[string]string) (map[string]string, error) {
	q := sqlc.New()
	row, err := q.ReadClonePostgresSecretWithdrawal(ctx, tx, mustPgUUID(op.ID))
	if errors.Is(mapErr(err), ErrNotFound) {
		publication, publicationErr := q.ReadCloneCheckpointPgPublication(ctx, tx, mustPgUUID(op.ID))
		if publicationErr == nil && publication.State == "withdrawn" {
			return nil, ErrConflict
		}
		if publicationErr != nil && !errors.Is(mapErr(publicationErr), ErrNotFound) {
			return nil, mapErr(publicationErr)
		}
		return original, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	manifest, err := cloneConfigurationOwnershipManifest(row.Manifest)
	if err != nil || pgUUIDString(row.OperationID) != op.ID || row.OwnershipSha256 != ownership || cloneCheckpointSHA(manifest) != row.ManifestSha256 || !row.RecordedAt.Valid || !checkpointObjectRetirementTime(row.RecordedAt.Time.UTC()) || row.RecordedAt.Time.After(time.Now()) || !row.PublicationWithdrawnAt.Valid || !checkpointObjectRetirementTime(row.PublicationWithdrawnAt.Time.UTC()) || row.RecordedAt.Time.Before(row.PublicationWithdrawnAt.Time) || row.PublicationWithdrawnAt.Time.Before(ownedAt) {
		return nil, ErrConflict
	}
	for table, hash := range manifest {
		if table != "app_secrets" && original[table] != hash {
			return nil, ErrConflict
		}
	}
	publication, err := readCheckpointPgPublicationTx(ctx, tx, op)
	if err != nil {
		return nil, checkpointPgCompensationDependencyError(err)
	}
	if publication.State != "withdrawn" || publication.PlanSHA256 != row.PublicationPlanSha256 {
		return nil, ErrConflict
	}
	changed := !reflect.DeepEqual(original, manifest)
	if changed != (len(publication.Plan.Secrets) != 0) {
		return nil, ErrConflict
	}
	stored, err := q.ReadCloneCheckpointPgPublication(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return nil, mapErr(err)
	}
	if !stored.WithdrawnAt.Valid || !stored.WithdrawnAt.Time.Equal(row.PublicationWithdrawnAt.Time) {
		return nil, ErrConflict
	}
	if err = checkpointPgPublicationRowsTx(ctx, tx, op, publication); err != nil {
		return nil, err
	}
	cleanup, err := q.ReadCloneConfigurationCleanup(ctx, tx, mustPgUUID(op.ID))
	if err == nil && (!cleanup.CleanedAt.Valid || cleanup.CleanedAt.Time.Before(row.RecordedAt.Time)) {
		return nil, ErrConflict
	}
	if err != nil && !errors.Is(mapErr(err), ErrNotFound) {
		return nil, mapErr(err)
	}
	return manifest, nil
}
