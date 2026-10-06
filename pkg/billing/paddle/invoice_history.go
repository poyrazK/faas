package paddle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"

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
	position, err := billing.DecodeInvoiceHistoryCursor(cursor, "paddle", acct.ProviderCustomerID)
	if err != nil {
		return result, err
	}
	base := p.invoiceBaseURL
	if base == "" {
		base = "https://api.paddle.com"
	}
	r := &billing.InvoiceFactsReader{BaseURL: base, Key: p.apiKey, Provider: "paddle", Client: p.invoiceHTTPClient}
	query := url.Values{
		"customer_id": {acct.ProviderCustomerID},
		"per_page":    {fmt.Sprint(limit)},
		"status":      {"billed,paid,completed,canceled,past_due"},
		"include":     {"adjustments_totals"},
	}
	if position != "" {
		query.Set("after", position)
	}
	raw, err := r.Get(ctx, "/transactions?"+query.Encode())
	if err != nil {
		return result, err
	}
	var page struct {
		Data []json.RawMessage `json:"data"`
		Meta struct {
			Pagination struct {
				HasMore *bool `json:"has_more"`
			} `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(raw, &page); err != nil || page.Meta.Pagination.HasMore == nil || len(page.Data) > limit || (*page.Meta.Pagination.HasMore && len(page.Data) == 0) {
		return result, errors.New("paddle: invalid transaction history page")
	}
	result.Scanned = len(page.Data)
	lastID := ""
	for _, rawTransaction := range page.Data {
		data, err := billing.InvoiceFactsMap(rawTransaction)
		if err != nil {
			return result, err
		}
		id := paddleString(data, "id", "transaction_id")
		if id == "" {
			return result, errors.New("paddle: transaction history entry has no ID")
		}
		lastID = id
		customerID := paddleString(data, "customer_id")
		if customerID == "" {
			continue
		}
		if customerID != acct.ProviderCustomerID {
			return result, billing.ErrInvoiceSourceMismatch
		}
		status := strings.ToLower(paddleString(data, "status"))
		normalized, ok := paddleHistoryInvoiceStatus(status)
		if !ok {
			continue
		}
		periodStart := paddleTimeAt(data, "billing_period", "starts_at")
		periodEnd := paddleTimeAt(data, "billing_period", "ends_at")
		if periodStart.IsZero() || periodEnd.IsZero() {
			continue
		}
		currency := strings.ToLower(paddleString(data, "currency_code", "currency"))
		if currency != "eur" {
			continue
		}
		total, totalOK := billing.InvoiceMinorUnits(paddleValueAt(data, "details", "adjusted_totals", "total"))
		tax, taxOK := billing.InvoiceMinorUnits(paddleValueAt(data, "details", "adjusted_totals", "tax"))
		if !totalOK {
			total, totalOK = billing.InvoiceMinorUnits(paddleValueAt(data, "details", "totals", "total"))
		}
		if !taxOK {
			tax, taxOK = billing.InvoiceMinorUnits(paddleValueAt(data, "details", "totals", "tax"))
		}
		subtotal, subtotalOK := billing.InvoiceMinorUnits(paddleValueAt(data, "details", "adjusted_totals", "subtotal"))
		if !subtotalOK {
			subtotal, subtotalOK = billing.InvoiceMinorUnits(paddleValueAt(data, "details", "totals", "subtotal"))
		}
		if !totalOK || !taxOK || !subtotalOK {
			continue
		}
		invoiceID := paddleString(data, "invoice_id")
		if invoiceID == "" {
			invoiceID = id
		}
		paid, paidOK := paddleHistoryPaidCents(data)
		if !paidOK {
			continue
		}
		inv := state.Invoice{
			AccountID: acct.ID, Provider: "paddle", ProviderInvoiceID: invoiceID, ProviderChargeID: id,
			Number: paddleString(data, "invoice_number", "number"), Status: normalized,
			PeriodStart: periodStart, PeriodEnd: periodEnd, SubtotalCents: subtotal, TaxCents: tax,
			TotalCents: total, AmountPaidCents: paid, Plan: state.InvoicePlanUnknown, Currency: "eur",
			Details: paddleInvoiceDetails(data),
		}
		inv.PDFAvailable = inv.Number != ""
		if err := state.ValidateInvoiceHistoryInvoice(inv, acct.ID, "paddle"); err != nil {
			continue
		}
		result.Invoices = append(result.Invoices, inv)
	}
	result.HasMore = *page.Meta.Pagination.HasMore
	if result.HasMore {
		if lastID == "" || lastID == position {
			return billing.InvoiceHistoryPage{}, errors.New("paddle: transaction history cursor did not advance")
		}
		result.NextCursor = billing.EncodeInvoiceHistoryCursor("paddle", acct.ProviderCustomerID, lastID)
	}
	return result, billing.ValidateInvoiceHistoryPage(result, "paddle", limit)
}

func paddleHistoryPaidCents(data map[string]any) (int64, bool) {
	payments, ok := data["payments"].([]any)
	if !ok {
		return 0, false
	}
	paid := new(big.Int)
	for _, value := range payments {
		payment, ok := value.(map[string]any)
		if !ok {
			return 0, false
		}
		if paddleString(payment, "status") != "captured" {
			continue
		}
		amount, valid := billing.InvoiceMinorUnits(payment["amount"])
		if !valid || amount < 0 {
			return 0, false
		}
		paid.Add(paid, big.NewInt(amount))
	}
	if !paid.IsInt64() {
		return 0, false
	}
	return paid.Int64(), true
}

func paddleHistoryInvoiceStatus(status string) (string, bool) {
	switch status {
	case "billed", "ready":
		return "open", true
	case "paid", "completed":
		return "paid", true
	case "canceled", "cancelled":
		return "void", true
	case "past_due":
		return "uncollectible", true
	default:
		return "", false
	}
}
