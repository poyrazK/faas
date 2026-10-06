package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) ListPlatformTenantOperations(_ context.Context, account, tenant string, opts api.OperationListOptions) (api.OperationListResponse, error) {
	return m.listOperationHistory(account, tenant, opts, false)
}

func (m *MemStore) ListAccountOperations(_ context.Context, account string, opts api.OperationListOptions) (api.OperationListResponse, error) {
	return m.listOperationHistory(account, opts.TenantID, opts, true)
}

func (m *MemStore) listOperationHistory(account, tenant string, opts api.OperationListOptions, operator bool) (api.OperationListResponse, error) {
	opts, cursor, err := prepareOperationHistoryQuery(account, tenant, opts, operator)
	if err != nil {
		return api.OperationListResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []api.OperationSummary{}
	now := time.Now().UTC()
	for _, op := range m.operationMemoryLocked().operations {
		if !sameOperationHistoryIdentity(op.AccountID, account) || (tenant != "" && !sameOperationHistoryIdentity(op.PlatformTenantID, tenant)) || !sameOperationHistoryIdentity(op.AppID, opts.AppID) || op.Scope != opts.Scope || !operationRetained(op, now) || (opts.Name != "" && op.Name != opts.Name) || (opts.State != "" && op.State != opts.State) {
			continue
		}
		row := operationHistorySummary(m.operationDeliveryLocked(op))
		if operator {
			row.PlatformTenantID = op.PlatformTenantID
		}
		if cursor.ID != "" && (row.CreatedAt.After(cursor.CreatedAt) || (row.CreatedAt.Equal(cursor.CreatedAt) && row.ID >= cursor.ID)) {
			continue
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	if len(rows) > opts.Limit+1 {
		rows = rows[:opts.Limit+1]
	}
	return operationHistoryPage(rows, opts.Limit, cursor), nil
}
