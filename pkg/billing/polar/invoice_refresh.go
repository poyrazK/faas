package polar

import (
	"context"
	"net/url"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

var _ billing.InvoiceDetailsReader = (*Provider)(nil)

func (p *Provider) FetchInvoiceDetails(ctx context.Context, acct state.Account, inv state.Invoice) (*state.InvoiceDetails, error) {
	if inv.Provider != "polar" || inv.AccountID != acct.ID || acct.ProviderCustomerID == "" {
		return nil, billing.ErrInvoiceSourceMismatch
	}
	r := &billing.InvoiceFactsReader{BaseURL: p.baseURL, Key: p.apiKey, Provider: "polar", Client: p.client}
	raw, err := r.Get(ctx, "/v1/orders/"+url.PathEscape(inv.ProviderInvoiceID))
	if err != nil {
		return nil, err
	}
	data, err := billing.InvoiceFactsMap(raw)
	if err != nil {
		return nil, err
	}
	total, totalOK := billing.InvoiceMinorUnits(data["total_amount"])
	tax, taxOK := billing.InvoiceMinorUnits(data["tax_amount"])
	if !totalOK || !taxOK {
		return nil, billing.ErrInvoiceSourceMismatch
	}
	if err := billing.ValidateInvoiceSource(acct, inv, firstString(data, "customer_id"), firstString(data, "id"), firstString(data, "currency"), total, tax); err != nil {
		return nil, err
	}
	items, _ := data["items"].([]any)
	if err := billing.ValidateInvoiceLineCount(len(items)); err != nil {
		return nil, err
	}
	details := polarInvoiceDetails(data)
	if err := billing.ValidateRefreshedDetails(details); err != nil {
		return nil, err
	}
	return details, nil
}
