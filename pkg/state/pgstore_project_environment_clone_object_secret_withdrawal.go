package state

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type cloneObjectWithdrawalOwnership struct {
	EnvironmentID, SHA256, PostgresSHA256, PriorManifestSHA256 string
}

// Called before removing envelopes, under the configuration clock and original
// worker/App locks. Legacy materializations never gain a backfilled witness.
func prepareCloneObjectSecretWithdrawalTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (cloneObjectWithdrawalOwnership, error) {
	var out cloneObjectWithdrawalOwnership
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
	_, err = q.ReadCloneObjectSecretWithdrawal(ctx, tx, mustPgUUID(op.ID))
	if !errors.Is(mapErr(err), ErrNotFound) {
		if err != nil {
			return out, mapErr(err)
		}
		return out, ErrConflict
	}
	publication, err := readCheckpointPgPublicationTx(ctx, tx, op)
	if err == nil && publication.State != "withdrawn" {
		return out, ErrConflict
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return out, err
	}
	out.PostgresSHA256, err = cloneObjectWithdrawalPostgresSHA(ctx, tx, op)
	if err != nil {
		return out, err
	}
	raw, err := q.ReadCloneConfigurationOwnershipSnapshot(ctx, tx, sqlc.ReadCloneConfigurationOwnershipSnapshotParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment, EnvironmentID: row.EnvironmentID})
	if err != nil {
		return out, mapErr(err)
	}
	manifest, err := cloneConfigurationOwnershipManifest(raw)
	if err != nil {
		return out, err
	}
	out.PriorManifestSHA256 = cloneCheckpointSHA(manifest)
	return out, nil
}

func saveCloneObjectSecretWithdrawalTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, owner cloneObjectWithdrawalOwnership, publicationSHA string) error {
	if owner.EnvironmentID == "" {
		return nil
	}
	q := sqlc.New()
	publication, err := q.ReadCloneCheckpointObjectPublication(ctx, tx, mustPgUUID(op.ID))
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
	if err = q.InsertCloneObjectSecretWithdrawal(ctx, tx, sqlc.InsertCloneObjectSecretWithdrawalParams{OperationID: mustPgUUID(op.ID), OwnershipSha256: owner.SHA256, PostgresWithdrawalSha256: owner.PostgresSHA256, PriorManifestSha256: owner.PriorManifestSHA256, PublicationPlanSha256: publicationSHA, PublicationWithdrawnAt: publication.WithdrawnAt, Manifest: raw, ManifestSha256: cloneCheckpointSHA(manifest)}); err != nil {
		return mapErr(err)
	}
	_, err = verifyCloneConfigurationOwnershipTx(ctx, tx, op, owner.EnvironmentID)
	return err
}

// Authenticate the saved transition independently of current copied settings;
// subsequent cleanup has its own exact post-state. No native IO occurs here.
func cloneObjectSecretWithdrawalManifestTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, ownership string, ownedAt time.Time, original map[string]string) (map[string]string, error) {
	q := sqlc.New()
	row, err := q.ReadCloneObjectSecretWithdrawal(ctx, tx, mustPgUUID(op.ID))
	if errors.Is(mapErr(err), ErrNotFound) {
		publication, publicationErr := q.ReadCloneCheckpointObjectPublication(ctx, tx, mustPgUUID(op.ID))
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
	postgresSHA, err := cloneObjectWithdrawalPostgresSHA(ctx, tx, op)
	if err != nil {
		return nil, err
	}
	if row.PostgresWithdrawalSha256 != postgresSHA || row.PriorManifestSha256 != cloneCheckpointSHA(original) {
		return nil, ErrConflict
	}
	publication, err := readCheckpointObjectPublicationTx(ctx, tx, op)
	if err != nil {
		return nil, checkpointPgCompensationDependencyError(err)
	}
	if publication.State != "withdrawn" || publication.Plan.SHA256 != row.PublicationPlanSha256 {
		return nil, ErrConflict
	}
	hasSecrets := false
	for _, bucket := range publication.Plan.Buckets {
		for _, preparation := range bucket.Preparations {
			hasSecrets = hasSecrets || len(preparation.Secrets) != 0
		}
	}
	if reflect.DeepEqual(original, manifest) == hasSecrets {
		return nil, ErrConflict
	}
	stored, err := q.ReadCloneCheckpointObjectPublication(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return nil, mapErr(err)
	}
	if !stored.WithdrawnAt.Valid || !stored.WithdrawnAt.Time.Equal(row.PublicationWithdrawnAt.Time) {
		return nil, ErrConflict
	}
	if err = checkpointObjectPublicationRowsTx(ctx, tx, publication); err != nil {
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

// Pin the entire predecessor receipt, including its publication and recording
// times. Absence is meaningful for object-only stages and never backfilled.
func cloneObjectWithdrawalPostgresSHA(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (string, error) {
	row, err := sqlc.New().ReadClonePostgresSecretWithdrawal(ctx, tx, mustPgUUID(op.ID))
	if errors.Is(mapErr(err), ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", mapErr(err)
	}
	return cloneCheckpointSHA(struct {
		OwnershipSHA256, PublicationSHA256, ManifestSHA256 string
		PublicationWithdrawnAt, RecordedAt                 time.Time
	}{row.OwnershipSha256, row.PublicationPlanSha256, row.ManifestSha256, row.PublicationWithdrawnAt.Time.UTC(), row.RecordedAt.Time.UTC()}), nil
}
