package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

func (m *MemStore) ListProductWorkHistory(ctx context.Context, s ProductWorkHistoryScope, f api.ProductWorkHistoryFilter, raw string) (api.ProductWorkHistoryPage, error) {
	f, err := productcontrol.NormalizeWorkHistoryFilter(f)
	if err != nil {
		return api.ProductWorkHistoryPage{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return api.ProductWorkHistoryPage{}, err
	}
	v, err := m.helpViewLocked(ProductHelpScope(s))
	if err != nil {
		return api.ProductWorkHistoryPage{}, err
	}
	c, err := workHistoryCursor(s, v.permissions, f, raw)
	if err != nil {
		return api.ProductWorkHistoryPage{}, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	entries := make([]api.ProductWorkHistoryEntry, 0, api.ProductControlWorkHistoryPageSize+1)
	for id, row := range m.productLocked().receipts {
		if err = ctx.Err(); err != nil {
			return api.ProductWorkHistoryPage{}, err
		}
		if row.account != s.AccountID || row.app != s.AppID || row.tenant != s.TenantID || f.OperationID != "" && id != f.OperationID {
			continue
		}
		live, result := "", false
		if op, ok := m.operationMemoryLocked().operations[id]; ok && op.AccountID == s.AccountID && op.AppID == s.AppID && op.PlatformTenantID == s.TenantID && operationRetained(op, now) {
			live, result = string(op.State), len(op.Result) > 0 && string(op.Result) != "null" || len(op.Artifacts) > 0
		}
		r := row.receipt
		e := api.ProductWorkHistoryEntry{OperationID: id, Feature: r.Feature, Plan: r.Plan, Period: r.Period, AdmittedAt: r.AdmittedAt.UTC().Truncate(time.Microsecond), Outcome: productRunOutcome(row.resolvedState, row.resolvedAt, live, result)}
		entries = collectWorkHistory(entries, e, f, c)
	}
	return workHistoryPage(s, v.permissions, now, f, c, entries)
}
