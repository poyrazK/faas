package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ BindingRuntimeInventoryStore = (*PgStore)(nil)

func (s *PgStore) ReadBindingRuntimeInventory(ctx context.Context, accountID, appID, scope string) (BindingRuntimeInventory, error) {
	rows, err := sqlc.New().AppBindingRuntimeInventory(ctx, s.pool, sqlc.AppBindingRuntimeInventoryParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), ScopeFilter: scope,
	})
	if err != nil {
		return BindingRuntimeInventory{}, fmt.Errorf("state: read binding runtime inventory: %w", err)
	}
	if len(rows) == 0 {
		return BindingRuntimeInventory{}, ErrNotFound
	}
	result := BindingRuntimeInventory{ChangedAt: optionalHealthTime(rows[0].ChangedAt), Deployments: []BindingRuntimeDeployment{}}
	byID := make(map[string]int)
	for _, row := range rows {
		if row.DeploymentID == "" {
			continue
		}
		index, exists := byID[row.DeploymentID]
		if !exists {
			index = len(result.Deployments)
			byID[row.DeploymentID] = index
			result.Deployments = append(result.Deployments, BindingRuntimeDeployment{ID: row.DeploymentID, Scope: row.Scope, DeploymentStatus: row.DeploymentStatus})
		}
		if row.InstanceState != "" {
			addBindingRuntimeInstance(&result.Deployments[index], result.ChangedAt, Instance{State: row.InstanceState, StartedAt: row.StartedAt.Time})
		}
	}
	sortBindingRuntimeDeployments(result.Deployments)
	return result, nil
}

func (s *PgStore) ListBindingRefreshInventory(ctx context.Context, accountID, appID string, wakeIDs []string) ([]BindingRefreshInventory, error) {
	rows, err := sqlc.New().AppBindingRefreshInventory(ctx, s.pool, sqlc.AppBindingRefreshInventoryParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), WakeIds: wakeIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("state: read binding refresh inventory: %w", err)
	}
	items := make([]BindingRefreshInventory, 0, len(rows))
	for _, row := range rows {
		items = append(items, BindingRefreshInventory{WakeID: row.WakeID, Status: row.Status, Attempts: int(row.Attempts),
			FailureReason: row.FailureReason, RequestedAt: row.RequestedAt.Time, CompletedAt: optionalHealthTime(row.CompletedAt)})
	}
	return items, nil
}
