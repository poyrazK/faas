package paddle

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
)

// The Paddle-specific OpProvider remains available to existing callers. These
// adapters expose the same cached catalog through the provider-neutral billing
// interface used by apid and the CLI, so selecting Paddle or Polar does not
// change the operator's catalog/status surface.
var _ billing.CatalogProvider = (*Provider)(nil)

func (p *Provider) ListBillingCatalog(ctx context.Context) []api.BillingCatalogEntry {
	entries := p.ListCatalog(ctx)
	out := make([]api.BillingCatalogEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, api.BillingCatalogEntry{
			Plan:     string(entry.Plan),
			Kind:     api.BillingCatalogKind(entry.Kind),
			Handle:   entry.Handle,
			SyncedAt: entry.SyncedAt,
		})
	}
	return out
}

func (p *Provider) SyncBillingCatalog(ctx context.Context) ([]api.BillingCatalogEntry, error) {
	if _, err := p.SyncCatalog(ctx); err != nil {
		return nil, err
	}
	return p.ListBillingCatalog(ctx), nil
}

func (p *Provider) ResetBillingCatalog(ctx context.Context) error {
	return p.ResetCatalog(ctx)
}
