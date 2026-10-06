package state

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func appSecretFromDeliveryRow(row sqlc.ListAppSecretsWithBindingAccessInScopeRow) AppSecret {
	return AppSecret{
		AccountID:                        row.AccountID,
		AppID:                            row.AppID,
		Scope:                            row.Scope,
		Key:                              row.Key,
		Ciphertext:                       row.Ciphertext,
		Kid:                              row.Kid,
		ValueHash:                        row.ValueHash,
		ManagedPostgresBindingID:         row.ManagedPostgresBindingID,
		ManagedCredentialRef:             row.ManagedCredentialRef,
		ManagedCredentialGeneration:      row.ManagedCredentialGeneration,
		ManagedObjectStorageCredentialID: row.ManagedObjectStorageCredentialID,
		SecretVersion:                    row.SecretVersion,
		DeliveryVersion:                  row.DeliveryVersion,
		DeliveredVersion:                 row.DeliveredVersion,
		DeliveryStatus:                   SecretDeliveryStatus(row.DeliveryStatus),
		LastDeliveryAttemptAt:            appSecretDeliveryTimePtr(row.LastDeliveryAttemptAt),
		LastDeliveredAt:                  appSecretDeliveryTimePtr(row.LastDeliveredAt),
		LastDeliveryErrorCode:            row.LastDeliveryErrorCode,
		LastDeliveredWakeID:              row.LastDeliveredWakeID,
		LastDeliveredInstanceID:          row.LastDeliveredInstanceID,
		LastRuntimeReloadVersion:         row.LastRuntimeReloadVersion,
		LastRuntimeReloadRevision:        row.LastRuntimeReloadRevision,
		LastRuntimeReloadProjection:      SecretReloadProjectionStatus(row.LastRuntimeReloadProjection),
		LastRuntimeReloadSignal:          SecretReloadSignalStatus(row.LastRuntimeReloadSignal),
		LastRuntimeReloadAt:              appSecretDeliveryTimePtr(row.LastRuntimeReloadAt),
		LastRuntimeReloadErrorCode:       row.LastRuntimeReloadErrorCode,
		LastRuntimeReloadInstanceID:      row.LastRuntimeReloadInstanceID,
		CreatedAt:                        row.CreatedAt.Time,
		UpdatedAt:                        row.UpdatedAt.Time,
		SecretClass:                      row.SecretClass,
		ManagedPostgresAccess:            row.ManagedPostgresAccess,
	}
}

func appSecretDeliveryTimePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}
