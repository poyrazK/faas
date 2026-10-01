package polar

import (
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

func polarInvoiceDetails(data map[string]any) *state.InvoiceDetails {
	d := new(state.InvoiceDetails)
	// Order created_at is not an invoice issue date, and billing_name is
	// the buyer, not the merchant of record. Neither fills issuer/date gaps.
	items, present := data["items"].([]any)
	if !present {
		return d
	}
	d.Lines = &state.InvoiceLines{Complete: true, Items: make([]state.InvoiceLineItem, 0, len(items))}
	product, _ := data["product"].(map[string]any)
	prices, _ := product["prices"].([]any)
	categories := make(map[string]string)
	for _, value := range prices {
		price, _ := value.(map[string]any)
		categories[firstString(price, "id")] = polarInvoiceCategory(firstString(price, "amount_type"))
	}
	seen := make(map[string]bool)
	for _, value := range items {
		item, _ := value.(map[string]any)
		id := firstString(item, "id")
		net, netOK := billing.InvoiceMinorUnits(item["amount"])
		tax, taxOK := billing.InvoiceMinorUnits(item["tax_amount"])
		if id == "" || seen[id] || !netOK || !taxOK {
			d.Lines.Complete = false
			continue
		}
		seen[id] = true
		d.Lines.Items = append(d.Lines.Items, state.InvoiceLineItem{
			ID: id, Description: firstString(item, "label"),
			ChargeCategory: categories[firstString(item, "product_price_id")], NetCents: net, TaxCents: tax,
		})
	}
	return d
}

func polarInvoiceCategory(amountType string) string {
	switch amountType {
	case "metered_unit":
		return "Usage"
	case "fixed":
		return "Purchase"
	default:
		return ""
	}
}
