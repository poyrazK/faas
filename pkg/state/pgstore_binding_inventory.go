package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	_ ObjectStorageBindingInventoryStore = (*PgStore)(nil)
	_ QueueBindingConsumerInventoryStore = (*PgStore)(nil)
)

func (s *PgStore) ListObjectStorageBindingsForApp(ctx context.Context, accountID, appID, scope string) ([]ObjectStorageBindingInventory, error) {
	rows, err := sqlc.New().AppObjectStorageBindingInventory(ctx, s.pool, sqlc.AppObjectStorageBindingInventoryParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), ScopeFilter: scope,
	})
	if err != nil {
		return nil, fmt.Errorf("state: list app object-storage bindings: %w", err)
	}
	items := make([]ObjectStorageBindingInventory, 0, len(rows))
	for _, row := range rows {
		items = append(items, ObjectStorageBindingInventory{
			RotationRevisionID: row.RotationRevisionID,
			BindingID:          uuidString(row.BindingID),
			BucketName:         row.BucketName, Scope: row.Scope.String, Prefix: row.Prefix.String,
			Permission: row.Permission, State: row.State, RotationPending: row.RotationPending, RotationWakeID: row.RotationWakeID,
		})
	}
	return items, nil
}

func (s *PgStore) ListQueueBindingConsumersForApp(ctx context.Context, accountID, appID string) ([]QueueBindingConsumerInventory, error) {
	rows, err := sqlc.New().AppQueueBindingConsumerInventory(ctx, s.pool, sqlc.AppQueueBindingConsumerInventoryParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list app queue consumers: %w", err)
	}
	items := make([]QueueBindingConsumerInventory, 0, len(rows))
	for _, row := range rows {
		var enabled *bool
		if row.ConsumerID != "" {
			value := row.ConsumerEnabled
			enabled = &value
		}
		items = append(items, QueueBindingConsumerInventory{
			BindingID: uuidString(row.BindingID), ConsumerEnabled: enabled,
			LastPollAt: optionalHealthTime(row.LastPollAt), LastSuccessAt: optionalHealthTime(row.LastSuccessAt),
			LastErrorAt: optionalHealthTime(row.LastErrorAt),
		})
	}
	return items, nil
}
