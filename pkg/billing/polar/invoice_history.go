package polar

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

var _ billing.InvoiceHistoryReader = (*Provider)(nil)

func (p *Provider) FetchInvoiceHistory(ctx context.Context, acct state.Account, cursor string, limit int) (billing.InvoiceHistoryPage, error) {
	var result billing.InvoiceHistoryPage
	if acct.ID == "" || acct.ProviderCustomerID == "" || limit < 1 || limit > api.MaxInvoiceHistoryPageSize {
		return result, billing.ErrInvoiceSourceMismatch
	}
	position, err := billing.DecodeInvoiceHistoryCursor(cursor, "polar", acct.ProviderCustomerID)
	if err != nil {
		return result, err
	}
	pageNumber := 1
	if position != "" {
		pageNumber, err = strconv.Atoi(position)
		if err != nil || pageNumber < 2 || pageNumber > 100000 {
			return result, billing.ErrInvoiceHistoryCursor
		}
	}
	r := &billing.InvoiceFactsReader{BaseURL: p.baseURL, Key: p.apiKey, Provider: "polar", Client: p.client}
	query := url.Values{"customer_id": {acct.ProviderCustomerID}, "limit": {strconv.Itoa(limit)}, "page": {strconv.Itoa(pageNumber)}, "sorting": {"created_at"}}
	raw, err := r.Get(ctx, "/v1/orders?"+query.Encode())
	if err != nil {
		return result, err
	}
	data, err := billing.InvoiceFactsMap(raw)
	if err != nil {
		return result, err
	}
	items, ok := data["items"].([]any)
	pagination, _ := data["pagination"].(map[string]any)
	maxPage, maxOK := billing.InvoiceMinorUnits(pagination["max_page"])
	if !ok || !maxOK || len(items) > limit || (len(items) == 0 && int64(pageNumber) < maxPage) {
		return result, errors.New("polar: invalid order history page")
	}
	result.Scanned = len(items)
	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			return billing.InvoiceHistoryPage{}, errors.New("polar: invalid order in history page")
		}
		id := firstString(item, "id")
		if id == "" {
			return billing.InvoiceHistoryPage{}, errors.New("polar: order history entry has no ID")
		}
		customerID := firstString(item, "customer_id")
		if customerID == "" {
			continue
		}
		if customerID != acct.ProviderCustomerID {
			return billing.InvoiceHistoryPage{}, billing.ErrInvoiceSourceMismatch
		}
		status, ok := polarHistoryInvoiceStatus(firstString(item, "status"))
		if !ok {
			continue
		}
		periodStart, periodEnd := polarHistoryBillingPeriod(item)
		if periodStart.IsZero() || periodEnd.IsZero() {
			continue
		}
		currency := strings.ToLower(firstString(item, "currency", "price_currency"))
		if currency != "eur" {
			continue
		}
		total, totalOK := billing.InvoiceMinorUnits(item["total_amount"])
		tax, taxOK := billing.InvoiceMinorUnits(item["tax_amount"])
		subtotal, subtotalOK := billing.InvoiceMinorUnits(item["subtotal_amount"])
		if !subtotalOK {
			subtotal, subtotalOK = billing.InvoiceMinorUnits(item["net_amount"])
		}
		if !totalOK || !taxOK || !subtotalOK {
			continue
		}
		paid := int64(0)
		if status == "paid" {
			paid, _ = billing.InvoiceMinorUnits(item["amount_paid"])
			if paid == 0 {
				paid = total
			}
		}
		inv := state.Invoice{
			AccountID: acct.ID, Provider: "polar", ProviderInvoiceID: id, ProviderChargeID: id,
			Number: firstString(item, "invoice_number", "number"), Status: status,
			PeriodStart: periodStart, PeriodEnd: periodEnd, SubtotalCents: subtotal, TaxCents: tax,
			TotalCents: total, AmountPaidCents: paid, Plan: state.InvoicePlanUnknown, Currency: "eur",
			PDFAvailable: boolValue(item["is_invoice_generated"]) || firstString(item, "invoice_pdf", "invoice_url", "pdf_url") != "",
			Details:      polarInvoiceDetails(item),
		}
		if err := state.ValidateInvoiceHistoryInvoice(inv, acct.ID, "polar"); err != nil {
			continue
		}
		result.Invoices = append(result.Invoices, inv)
	}
	result.HasMore = int64(pageNumber) < maxPage
	if result.HasMore {
		result.NextCursor = billing.EncodeInvoiceHistoryCursor("polar", acct.ProviderCustomerID, fmt.Sprint(pageNumber+1))
	}
	return result, billing.ValidateInvoiceHistoryPage(result, "polar", limit)
}

func polarHistoryBillingPeriod(order map[string]any) (time.Time, time.Time) {
	start := firstTime(order, "period_start", "billing_period_start")
	end := firstTime(order, "period_end", "billing_period_end")
	if start.IsZero() {
		start = nestedTime(order, "billing_period", "starts_at")
	}
	if end.IsZero() {
		end = nestedTime(order, "billing_period", "ends_at")
	}
	if !start.IsZero() && !end.IsZero() {
		return start, end
	}
	items, ok := order["items"].([]any)
	if !ok || len(items) == 0 {
		return time.Time{}, time.Time{}
	}
	var earliest, latest time.Time
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return time.Time{}, time.Time{}
		}
		lineStart, lineEnd := timeValue(item["start_timestamp"]), timeValue(item["end_timestamp"])
		if lineStart.IsZero() || lineEnd.IsZero() {
			return time.Time{}, time.Time{}
		}
		if earliest.IsZero() || lineStart.Before(earliest) {
			earliest = lineStart
		}
		if latest.IsZero() || lineEnd.After(latest) {
			latest = lineEnd
		}
	}
	return earliest, latest
}

func polarHistoryInvoiceStatus(status string) (string, bool) {
	switch strings.ToLower(status) {
	case "paid":
		return "paid", true
	case "void", "canceled", "cancelled":
		return "void", true
	case "pending":
		return "open", true
	default:
		return "", false
	}
}
