package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) GetProductCustomerWorkSummary(_ context.Context, account, app, tenant string) (api.ProductCustomerWorkSummary, error) {
	if !validProductUUID(tenant) {
		return api.ProductCustomerWorkSummary{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.apps[app]; !ok || a.AccountID != account || a.Status == AppDeleted {
		return api.ProductCustomerWorkSummary{}, ErrNotFound
	}
	t, ok := m.platformTenants[tenant]
	if !ok || t.AccountID != account {
		return api.ProductCustomerWorkSummary{}, ErrNotFound
	}
	if _, ok := m.productLocked().customers[app+"/"+tenant]; !ok {
		return api.ProductCustomerWorkSummary{}, ErrNotFound
	}
	var out api.ProductCustomerWorkSummary
	for id, row := range m.productLocked().receipts {
		if row.account != account || row.app != app || row.tenant != tenant || row.resolvedState != "" || row.waivedAt != nil {
			continue
		}
		out.Unresolved++
		if op, ok := m.operationMemoryLocked().operations[id]; !ok || !operationIsActive(op) {
			out.NeedsReview++
			if out.ReviewPeriod == "" || row.receipt.Period < out.ReviewPeriod {
				out.ReviewPeriod = row.receipt.Period
			}
		}
	}
	return out, nil
}
