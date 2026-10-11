package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func checkCapturedProjectEnvironmentCloneQuota(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone, limits api.Limits) error {
	rows, err := new(sqlc.Queries).ReadProjectEnvironmentCloneValueQuota(ctx, tx, sqlc.ReadProjectEnvironmentCloneValueQuotaParams{
		AccountID: mustPgUUID(clone.AccountID), ProjectID: mustPgUUID(clone.ProjectID),
	})
	if err != nil {
		return mapErr(err)
	}
	if len(rows) != len(clone.capturedValues) {
		return ErrConflict
	}
	for _, row := range rows {
		values, ok := clone.capturedValues[row.AppID]
		if !ok {
			return ErrConflict
		}
		if err := checkCapturedCloneQuota(row.Slug, values, int(row.SecretCount), int(row.VariableCount), clone.ManagedBindingsPrepared, limits); err != nil {
			return err
		}
	}
	return nil
}

func copyCapturedProjectEnvironmentCloneValues(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) (int, int, error) {
	q := new(sqlc.Queries)
	variableCount, secretCount := 0, 0
	for appID, values := range clone.capturedValues {
		for _, value := range values.Variables {
			if err := q.InsertProjectEnvironmentCloneCapturedVariable(ctx, tx, sqlc.InsertProjectEnvironmentCloneCapturedVariableParams{
				AccountID: mustPgUUID(clone.AccountID), AppID: mustPgUUID(appID), TargetScope: clone.TargetSlug, Key: value.Key, Value: value.Value,
			}); err != nil {
				return 0, 0, mapErr(err)
			}
			variableCount++
		}
		for _, value := range values.Secrets {
			if cloneSecretManagedID(value) != "" {
				continue
			}
			ciphertext, err := cloneSecretCiphertext(value)
			if err != nil {
				return 0, 0, err
			}
			if err := q.InsertProjectEnvironmentCloneCapturedSecret(ctx, tx, sqlc.InsertProjectEnvironmentCloneCapturedSecretParams{
				AccountID: mustPgUUID(clone.AccountID), AppID: mustPgUUID(appID), TargetScope: clone.TargetSlug, Key: value.Key,
				Ciphertext: ciphertext, Kid: value.Kid, ValueHash: value.ValueHash, SecretVersion: value.SecretVersion, SecretClass: value.SecretClass,
			}); err != nil {
				return 0, 0, mapErr(err)
			}
			secretCount++
		}
	}
	return variableCount, secretCount, nil
}
