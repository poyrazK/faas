package paddle

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

var _ billing.InvoiceDetailsReader = (*Provider)(nil)

func (p *Provider) FetchInvoiceDetails(ctx context.Context, acct state.Account, inv state.Invoice) (*state.InvoiceDetails, error) {
	if inv.Provider != "paddle" || inv.AccountID != acct.ID || acct.ProviderCustomerID == "" {
		return nil, billing.ErrInvoiceSourceMismatch
	}
	id := inv.ProviderChargeID
	if id == "" && strings.HasPrefix(inv.ProviderInvoiceID, "txn_") {
		id = inv.ProviderInvoiceID
	}
	if id == "" {
		return nil, billing.ErrInvoiceSourceMismatch
	}
	base := p.invoiceBaseURL
	if base == "" {
		base = "https://api.paddle.com"
	}
	r := &billing.InvoiceFactsReader{BaseURL: base, Key: p.apiKey, Provider: "paddle", Client: p.invoiceHTTPClient}
	raw, err := r.Get(ctx, "/transactions/"+url.PathEscape(id))
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	data, err := billing.InvoiceFactsMap(envelope.Data)
	if err != nil {
		return nil, err
	}
	if paddleString(data, "id") != id {
		return nil, billing.ErrInvoiceSourceMismatch
	}
	document := paddleString(data, "invoice_id")
	if document == "" {
		document = id
	}
	total, totalOK := billing.InvoiceMinorUnits(paddleValueAt(data, "details", "adjusted_totals", "total"))
	tax, taxOK := billing.InvoiceMinorUnits(paddleValueAt(data, "details", "adjusted_totals", "tax"))
	if !totalOK {
		total, totalOK = billing.InvoiceMinorUnits(paddleValueAt(data, "details", "totals", "total"))
	}
	if !taxOK {
		tax, taxOK = billing.InvoiceMinorUnits(paddleValueAt(data, "details", "totals", "tax"))
	}
	if !totalOK || !taxOK {
		return nil, billing.ErrInvoiceSourceMismatch
	}
	if err := billing.ValidateInvoiceSource(acct, inv, paddleString(data, "customer_id"), document, paddleString(data, "currency_code"), total, tax); err != nil {
		return nil, err
	}
	items, _ := paddleValueAt(data, "details", "line_items").([]any)
	if err := billing.ValidateInvoiceLineCount(len(items)); err != nil {
		return nil, err
	}
	details := paddleInvoiceDetails(data)
	if err := billing.ValidateRefreshedDetails(details); err != nil {
		return nil, err
	}
	return details, nil
}
