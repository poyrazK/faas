package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) GetProductRunDetails(ctx context.Context, s ProductRunDetailsScope, id string) (api.ProductRunDetails, error) {
	if !validProductUUID(id) {
		return api.ProductRunDetails{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.ProductRunDetails{}, err
	}
	v, err := m.helpViewLocked(ProductHelpScope(s))
	if err != nil {
		return api.ProductRunDetails{}, err
	}
	if !v.permissions.ViewWork {
		return api.ProductRunDetails{}, ErrProductAccess
	}
	r, ok := m.productLocked().receipts[id]
	if !ok || r.account != s.AccountID || r.app != s.AppID || r.tenant != s.TenantID {
		return api.ProductRunDetails{}, ErrNotFound
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	live, result := "", false
	if op, ok := m.operationMemoryLocked().operations[id]; ok && op.AccountID == s.AccountID && op.AppID == s.AppID && op.PlatformTenantID == s.TenantID && operationRetained(op, now) {
		live, result = string(op.State), len(op.Result) > 0 && string(op.Result) != "null" || len(op.Artifacts) > 0
	}
	d := productRunDetails(s, id, r.receipt, r.resolvedState, r.resolvedAt, live, result, r.waivedAt != nil, v.permissions, now)
	if c := d.Charge; c != nil {
		for _, row := range m.productLocked().documents {
			doc := row.document
			if row.account != s.AccountID || row.app != s.AppID || doc.TenantID != s.TenantID || doc.Period != r.receipt.Period {
				continue
			}
			if doc.Kind == "statement" {
				c.Statement = &api.ProductRunStatement{ID: doc.ID, CreatedAt: doc.CreatedAt.UTC()}
			}
			if doc.Kind == "credit_note" && doc.OperationID == id {
				if doc.AmountMillicents >= 0 || doc.AmountMillicents < -c.UnitPriceMillicents || c.CreditMillicents < -c.UnitPriceMillicents-doc.AmountMillicents {
					return api.ProductRunDetails{}, ErrConflict
				}
				c.CreditMillicents += doc.AmountMillicents
				c.CreditCount++
			}
		}
	}
	if err = productRunFinalCharge(&d); err != nil {
		return api.ProductRunDetails{}, err
	}
	return d, ctx.Err()
}
