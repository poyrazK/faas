package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
	stripeapi "github.com/stripe/stripe-go"
)

var _ billing.InvoiceHistoryReader = (*Client)(nil)

func (c *Client) FetchInvoiceHistory(ctx context.Context, acct state.Account, cursor string, limit int) (billing.InvoiceHistoryPage, error) {
	var result billing.InvoiceHistoryPage
	if acct.ID == "" || acct.ProviderCustomerID == "" || limit < 1 || limit > api.MaxInvoiceHistoryPageSize {
		return result, billing.ErrInvoiceSourceMismatch
	}
	position, err := billing.DecodeInvoiceHistoryCursor(cursor, "stripe", acct.ProviderCustomerID)
	if err != nil {
		return result, err
	}
	base := c.invoiceBaseURL
	if base == "" {
		base = "https://api.stripe.com"
	}
	r := &billing.InvoiceFactsReader{BaseURL: base, Key: c.apiKey, Provider: "stripe", Version: stripeapi.APIVersion, Client: c.invoiceHTTPClient}
	query := url.Values{"customer": {acct.ProviderCustomerID}, "limit": {strconv.Itoa(limit)}}
	if position != "" {
		query.Set("starting_after", position)
	}
	raw, err := r.Get(ctx, "/v1/invoices?"+query.Encode())
	if err != nil {
		return result, err
	}
	var page struct {
		Data    []json.RawMessage `json:"data"`
		HasMore bool              `json:"has_more"`
	}
	if err := json.Unmarshal(raw, &page); err != nil || (page.HasMore && len(page.Data) == 0) || len(page.Data) > limit {
		return result, errors.New("stripe: invalid invoice history page")
	}
	result.Scanned = len(page.Data)
	lastID := ""
	for _, rawInvoice := range page.Data {
		var source struct {
			ID, Status, Currency, Number string
			Customer                     json.RawMessage `json:"customer"`
			PaymentIntent                json.RawMessage `json:"payment_intent"`
			PeriodStart                  int64           `json:"period_start"`
			PeriodEnd                    int64           `json:"period_end"`
			Subtotal                     *int64          `json:"subtotal"`
			Total                        *int64          `json:"total"`
			AmountPaid                   *int64          `json:"amount_paid"`
			InvoicePDF                   string          `json:"invoice_pdf"`
		}
		if err := json.Unmarshal(rawInvoice, &source); err != nil || source.ID == "" {
			return result, errors.New("stripe: invalid invoice in history page")
		}
		lastID = source.ID
		customerID := stripeResourceID(source.Customer)
		if customerID == "" {
			continue
		}
		if customerID != acct.ProviderCustomerID {
			return result, billing.ErrInvoiceSourceMismatch
		}
		if source.Status == "draft" || source.Subtotal == nil || source.Total == nil || source.AmountPaid == nil || source.PeriodStart <= 0 || source.PeriodEnd <= 0 {
			continue
		}
		status, ok := stripeHistoryInvoiceStatus(source.Status)
		if !ok {
			continue
		}
		tax := stripeHistoryInvoiceTax(rawInvoice)
		if tax < 0 || source.Currency != "eur" {
			continue
		}
		inv := state.Invoice{
			AccountID: acct.ID, Provider: "stripe", ProviderInvoiceID: source.ID,
			ProviderChargeID: stripeResourceID(source.PaymentIntent), Number: source.Number, Status: status,
			PeriodStart: time.Unix(source.PeriodStart, 0).UTC(), PeriodEnd: time.Unix(source.PeriodEnd, 0).UTC(),
			SubtotalCents: *source.Subtotal, TaxCents: tax, TotalCents: *source.Total, AmountPaidCents: *source.AmountPaid,
			Plan: state.InvoicePlanUnknown, Currency: "eur", PDFAvailable: source.InvoicePDF != "",
		}
		if err := state.ValidateInvoiceHistoryInvoice(inv, acct.ID, "stripe"); err != nil {
			continue
		}
		result.Invoices = append(result.Invoices, inv)
	}
	if page.HasMore {
		if lastID == "" || lastID == position {
			return billing.InvoiceHistoryPage{}, errors.New("stripe: invoice history cursor did not advance")
		}
		result.HasMore = true
		result.NextCursor = billing.EncodeInvoiceHistoryCursor("stripe", acct.ProviderCustomerID, lastID)
	}
	return result, billing.ValidateInvoiceHistoryPage(result, "stripe", limit)
}

func stripeHistoryInvoiceStatus(status string) (string, bool) {
	switch status {
	case "open", "paid", "uncollectible", "void":
		return status, true
	default:
		return "", false
	}
}

func stripeHistoryInvoiceTax(raw json.RawMessage) int64 {
	var source struct {
		Tax             *int64 `json:"tax"`
		TotalTaxAmounts []struct {
			Amount int64 `json:"amount"`
		} `json:"total_tax_amounts"`
		TotalTaxes []struct {
			Amount int64 `json:"amount"`
		} `json:"total_taxes"`
	}
	if json.Unmarshal(raw, &source) != nil {
		return -1
	}
	if source.Tax != nil {
		return *source.Tax
	}
	list := source.TotalTaxAmounts
	if list == nil {
		list = source.TotalTaxes
	}
	if list == nil {
		return -1
	}
	var total int64
	for _, tax := range list {
		if tax.Amount < 0 || total > int64(^uint64(0)>>1)-tax.Amount {
			return -1
		}
		total += tax.Amount
	}
	return total
}
