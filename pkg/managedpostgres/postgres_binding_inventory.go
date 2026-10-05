package managedpostgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ AppBindingInventoryStore = (*PostgresStore)(nil)

func (s *PostgresStore) ListBindingsForApp(ctx context.Context, accountID, appID, scope string) ([]AppBindingInventory, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return nil, err
	}
	app, err := postgresUUID(appID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().AppManagedPostgresBindingInventory(ctx, s.pool, sqlc.AppManagedPostgresBindingInventoryParams{
		AccountID: account, AppID: app, ScopeFilter: scope,
	})
	if err != nil {
		return nil, fmt.Errorf("managedpostgres: list app bindings: %w", err)
	}
	items := make([]AppBindingInventory, 0, len(rows))
	for _, row := range rows {
		items = append(items, AppBindingInventory{
			BindingID:    uuid.UUID(row.BindingID.Bytes).String(),
			DatabaseName: row.DatabaseName, Scope: row.Scope, EnvironmentKey: row.EnvironmentKey,
			Access: row.Access, State: row.State, CredentialGeneration: row.CredentialGeneration,
			RotationPending: row.RotationPending,
			RotationWakeID:  row.RotationWakeID,
		})
	}
	return items, nil
}
