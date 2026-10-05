package managedpostgres

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func bindingFromDeliveryRow(row sqlc.FinishManagedPostgresBindingProvisionRow) Binding {
	return Binding{
		ID:                         row.ID,
		AccountID:                  row.AccountID,
		DatabaseID:                 row.DatabaseID,
		AppID:                      row.AppID,
		Scope:                      row.Scope,
		EnvironmentKey:             row.EnvironmentKey,
		Access:                     CredentialAccess(row.Access),
		ProviderIdentityID:         row.ProviderIdentityID,
		CredentialRef:              row.CredentialRef,
		CredentialGeneration:       row.CredentialGeneration,
		RotationPreviousGeneration: row.RotationPreviousGeneration,
		RotationWakeID:             row.RotationWakeID,
		RotationCleanupReady:       row.RotationCleanupReady,
		State:                      BindingState(row.State),
		LastErrorCode:              row.LastErrorCode,
		LeaseToken:                 row.LeaseToken,
		LeaseUntil:                 bindingDeliveryTime(row.LeaseUntil),
		AttemptCount:               row.AttemptCount,
		RetryAt:                    row.RetryAt.Time,
		CreatedAt:                  row.CreatedAt.Time,
		UpdatedAt:                  row.UpdatedAt.Time,
		DeletedAt:                  bindingDeliveryTimePtr(row.DeletedAt),
	}
}

func bindingDeliveryTime(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time
}
func bindingDeliveryTimePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}
