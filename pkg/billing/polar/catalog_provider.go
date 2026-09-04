package polar

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
)

var _ billing.CatalogProvider = (*Provider)(nil)

// ListBillingCatalog returns the dashboard-owned Polar product IDs that
// passed the last catalog preflight. Polar does not expose Paddle's local
// monthly/overage handle cache, so product rows are the truthful operator
// projection; the product endpoint validation remains the source of the
// fixed-price, metered-price, and meter-wiring checks.
func (p *Provider) ListBillingCatalog(context.Context) []api.BillingCatalogEntry {
	if p == nil {
		return []api.BillingCatalogEntry{}
	}
	p.catalogMu.RLock()
	syncedAt := p.lastSyncAt
	p.catalogMu.RUnlock()
	out := make([]api.BillingCatalogEntry, 0, 3)
	for _, plan := range []api.Plan{api.PlanHobby, api.PlanPro, api.PlanScale} {
		if productID := p.products[plan]; productID != "" {
			out = append(out, api.BillingCatalogEntry{
				Plan:     string(plan),
				Kind:     api.BillingCatalogKindProduct,
				Handle:   productID,
				SyncedAt: syncedAt,
			})
		}
	}
	return out
}

func (p *Provider) SyncBillingCatalog(ctx context.Context) ([]api.BillingCatalogEntry, error) {
	if err := p.EnsurePlanProducts(ctx); err != nil {
		return nil, err
	}
	return p.ListBillingCatalog(ctx), nil
}

// Polar products are managed in the Polar dashboard. A local reset cannot
// safely delete or unlink them, so callers receive a truthful unsupported
// result and can use the dashboard before running sync again.
func (p *Provider) ResetBillingCatalog(context.Context) error {
	return billing.ErrNotImplemented
}
