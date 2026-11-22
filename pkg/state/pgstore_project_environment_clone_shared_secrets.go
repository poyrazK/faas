// adr: 590
package state

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// readCloneSharedSecretsDB reads an environment's shared secrets and every
// project app's references and suppressions in it (ADR-20261104120000
// amendment 2026-11-21).
func readCloneSharedSecretsDB(ctx context.Context, db sqlc.DBTX, accountID, projectID, environment string) (projectCloneSharedSecrets, error) {
	q := new(sqlc.Queries)
	out := projectCloneSharedSecrets{}
	secrets, err := q.ReadProjectEnvironmentCloneSharedSecrets(ctx, db, sqlc.ReadProjectEnvironmentCloneSharedSecretsParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), EnvironmentSlug: environment})
	if err != nil {
		return out, mapErr(err)
	}
	for _, s := range secrets {
		out.Secrets = append(out.Secrets, projectCloneSharedSecret{Key: s.Key, Ciphertext: s.Ciphertext, Kid: s.Kid, ValueHash: s.ValueHash, SecretVersion: s.SecretVersion})
	}
	refs, err := q.ReadProjectEnvironmentCloneSecretRefs(ctx, db, sqlc.ReadProjectEnvironmentCloneSecretRefsParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), EnvironmentSlug: environment})
	if err != nil {
		return out, mapErr(err)
	}
	for _, r := range refs {
		out.Refs = append(out.Refs, projectCloneSecretRef{AppID: uuidString(r.AppID), Key: r.Key, SecretName: r.SecretName})
	}
	suppressions, err := q.ReadProjectEnvironmentCloneSecretSuppressions(ctx, db, sqlc.ReadProjectEnvironmentCloneSecretSuppressionsParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), EnvironmentSlug: environment})
	if err != nil {
		return out, mapErr(err)
	}
	for _, s := range suppressions {
		out.Suppressions = append(out.Suppressions, projectCloneSecretSuppression{AppID: uuidString(s.AppID), Key: s.Key})
	}
	return normalizeCloneSharedSecrets(out)
}

// materializeCloneSharedSecretsTx writes captured shared secrets, references
// and suppressions into the target environment. The envelopes are copied as
// sealed; triggers advance the environment's shared secret generation.
func materializeCloneSharedSecretsTx(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone, captured projectCloneSharedSecrets) (secrets, refs int, err error) {
	q := new(sqlc.Queries)
	account, project := mustPgUUID(clone.AccountID), mustPgUUID(clone.ProjectID)
	for _, s := range captured.Secrets {
		if err := q.InsertProjectEnvironmentCloneSharedSecret(ctx, tx, sqlc.InsertProjectEnvironmentCloneSharedSecretParams{AccountID: account, ProjectID: project,
			EnvironmentSlug: clone.TargetSlug, Key: s.Key, Ciphertext: s.Ciphertext, Kid: s.Kid, ValueHash: s.ValueHash, SecretVersion: s.SecretVersion}); err != nil {
			return 0, 0, mapErr(err)
		}
	}
	for _, r := range captured.Refs {
		n, err := q.InsertProjectEnvironmentCloneSecretRef(ctx, tx, sqlc.InsertProjectEnvironmentCloneSecretRefParams{AccountID: account, ProjectID: project,
			EnvironmentSlug: clone.TargetSlug, AppID: mustPgUUID(r.AppID), Key: r.Key, SecretName: r.SecretName})
		if err != nil {
			return 0, 0, mapErr(err)
		}
		refs += int(n)
	}
	for _, s := range captured.Suppressions {
		if _, err := q.InsertProjectEnvironmentCloneSecretSuppression(ctx, tx, sqlc.InsertProjectEnvironmentCloneSecretSuppressionParams{AccountID: account, ProjectID: project,
			EnvironmentSlug: clone.TargetSlug, AppID: mustPgUUID(s.AppID), Key: s.Key}); err != nil {
			return 0, 0, mapErr(err)
		}
	}
	return len(captured.Secrets), refs, nil
}

// verifyCloneSharedSecretsPublicationTx proves the stage holds exactly the
// captured shared secrets.
func verifyCloneSharedSecretsPublicationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil || len(records) == 0 {
		return err
	}
	captured, err := capturedCloneSharedSecrets(records)
	if err != nil || captured == nil {
		return err
	}
	target, err := readCloneSharedSecretsDB(ctx, tx, op.AccountID, op.ProjectID, op.TargetEnvironment)
	if err != nil {
		return err
	}
	return validateCloneSharedSecretsProof(captured, target)
}
