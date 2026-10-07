package paddle

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

func paddleInvoiceDetails(data map[string]any) *state.InvoiceDetails {
	d := &state.InvoiceDetails{IssuedAt: billing.InvoiceDate(paddleTimeAt(data, "billed_at"))}
	terms, _ := paddleValueAt(data, "billing_details", "payment_terms").(map[string]any)
	frequency, valid := billing.InvoiceMinorUnits(terms["frequency"])
	interval := paddleString(terms, "interval")
	if valid && frequency > 0 && (interval == "day" || interval == "week" || interval == "month" || interval == "year") {
		d.PaymentTerms = fmt.Sprintf("Net %d %s(s)", frequency, interval)
	}
	items, present := paddleValueAt(data, "details", "line_items").([]any)
	if !present {
		return d
	}
	d.Lines = &state.InvoiceLines{Complete: true, Items: make([]state.InvoiceLineItem, 0, len(items))}
	prices := paddleInvoicePrices(data)
	seen := make(map[string]bool)
	for _, value := range items {
		item, _ := value.(map[string]any)
		id := paddleString(item, "price_id")
		total, totalOK := billing.InvoiceMinorUnits(paddleValueAt(item, "totals", "total"))
		tax, taxOK := billing.InvoiceMinorUnits(paddleValueAt(item, "totals", "tax"))
		net, netOK := billing.InvoiceDifference(total, tax)
		if id == "" || seen[id] || !totalOK || !taxOK || !netOK {
			d.Lines.Complete = false
			continue
		}
		seen[id] = true
		d.Lines.Items = append(d.Lines.Items, state.InvoiceLineItem{
			ID: id, Description: paddleStringAt(item, "product", "name"),
			ChargeCategory: paddleInvoiceCategory(prices[id]), NetCents: net, TaxCents: tax,
		})
	}
	return d
}

func paddleInvoicePrices(data map[string]any) map[string]map[string]any {
	prices := make(map[string]map[string]any)
	items, _ := data["items"].([]any)
	for _, value := range items {
		item, _ := value.(map[string]any)
		price, _ := item["price"].(map[string]any)
		prices[paddleString(price, "id")] = price
	}
	return prices
}

func paddleInvoiceCategory(price map[string]any) string {
	// These descriptions are written by Gregale's own price provisioning.
	// Its overage prices are flat-rate, but represent metered consumption.
	description := paddleString(price, "description")
	if strings.HasPrefix(description, faasPlanNamePrefix) {
		for _, plan := range planProducts() {
			if description == planToProductName(plan)+"-overage" {
				return "Usage"
			}
			if description == planToProductName(plan)+"-monthly" {
				return "Purchase"
			}
		}
	}
	return "" // A billing cycle alone does not prove purchase vs consumption.
}

func paddleStringAt(data map[string]any, path ...string) string {
	value, _ := paddleValueAt(data, path...).(string)
	return value
}
