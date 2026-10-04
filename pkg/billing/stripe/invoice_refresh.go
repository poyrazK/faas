package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
	stripeapi "github.com/stripe/stripe-go"
)

var _ billing.InvoiceDetailsReader = (*Client)(nil)

func (c *Client) FetchInvoiceDetails(ctx context.Context, acct state.Account, inv state.Invoice) (*state.InvoiceDetails, error) {
	if inv.Provider != "stripe" || inv.AccountID != acct.ID || acct.ProviderCustomerID == "" {
		return nil, billing.ErrInvoiceSourceMismatch
	}
	base := c.invoiceBaseURL
	if base == "" {
		base = "https://api.stripe.com"
	}
	r := &billing.InvoiceFactsReader{BaseURL: base, Key: c.apiKey, Provider: "stripe", Version: stripeapi.APIVersion, Client: c.invoiceHTTPClient}
	path := "/v1/invoices/" + url.PathEscape(inv.ProviderInvoiceID)
	raw, err := r.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	var source struct {
		ID, Currency string
		Customer     json.RawMessage
		Total        *int64
	}
	var facts stripeInvoiceFacts
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &facts); err != nil {
		return nil, err
	}
	envelope, err := json.Marshal(struct {
		Data struct {
			Object json.RawMessage `json:"object"`
		} `json:"data"`
	}{Data: struct {
		Object json.RawMessage `json:"object"`
	}{Object: raw}})
	if err != nil {
		return nil, err
	}
	if source.Total == nil {
		return nil, billing.ErrInvoiceSourceMismatch
	}
	if err := billing.ValidateInvoiceSource(acct, inv, stripeResourceID(source.Customer), source.ID, source.Currency, *source.Total, InvoiceTaxCentsFromWebhook(envelope, -1)); err != nil {
		return nil, err
	}
	// Always use the dedicated line endpoint. An invoice response includes
	// only the first handful of lines, even when its preview looks complete.
	lines, err := readStripeInvoiceLines(ctx, r, path+"/lines")
	if err != nil {
		return nil, err
	}
	if err := hydrateStripeInvoicePrices(ctx, r, lines); err != nil {
		return nil, err
	}
	facts.Lines = lines
	details := invoiceDetailsFromFacts(facts)
	if err := billing.ValidateRefreshedDetails(details); err != nil {
		return nil, err
	}
	if !details.Lines.Complete {
		return nil, errors.New("stripe: invalid invoice line snapshot")
	}
	return details, nil
}

func readStripeInvoiceLines(ctx context.Context, r *billing.InvoiceFactsReader, path string) (*stripeInvoiceLines, error) {
	done := false
	result := &stripeInvoiceLines{HasMore: &done}
	seen := make(map[string]bool)
	cursor := ""
	for {
		q := url.Values{"limit": []string{strconv.Itoa(api.StripeInvoicePageSize)}}
		if cursor != "" {
			q.Set("starting_after", cursor)
		}
		raw, err := r.Get(ctx, path+"?"+q.Encode())
		if err != nil {
			return nil, err
		}
		var page stripeInvoiceLines
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, err
		}
		if page.HasMore == nil || (len(page.Data) == 0 && *page.HasMore) {
			return nil, errors.New("stripe: invalid invoice line pagination")
		}
		if len(result.Data)+len(page.Data) > api.MaxInvoiceLineItems {
			return nil, &billing.InvoiceRefreshLimitError{Kind: "line items", Limit: api.MaxInvoiceLineItems, Observed: len(result.Data) + len(page.Data)}
		}
		for _, line := range page.Data {
			if line.ID == "" || seen[line.ID] {
				return nil, errors.New("stripe: missing or repeated invoice line ID")
			}
			seen[line.ID] = true
			result.Data = append(result.Data, line)
			cursor = line.ID
		}
		if !*page.HasMore {
			return result, nil
		}
	}
}

func hydrateStripeInvoicePrices(ctx context.Context, r *billing.InvoiceFactsReader, lines *stripeInvoiceLines) error {
	cache := make(map[string]json.RawMessage)
	for n := range lines.Data {
		line := &lines.Data[n]
		raw, kind := &line.Price, "prices"
		if len(*raw) == 0 || string(*raw) == "null" {
			raw = &line.Pricing.PriceDetails.Price
		}
		if len(*raw) == 0 || string(*raw) == "null" {
			raw, kind = &line.Plan, "plans"
		}
		var id string
		if json.Unmarshal(*raw, &id) != nil || id == "" {
			continue
		} // already expanded or unavailable
		key := kind + ":" + id
		expanded, ok := cache[key]
		if !ok {
			var err error
			expanded, err = r.Get(ctx, "/v1/"+kind+"/"+url.PathEscape(id))
			if err != nil {
				return err
			}
			var price struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(expanded, &price); err != nil {
				return err
			}
			if price.ID != id {
				return fmt.Errorf("stripe: price identity: %w", billing.ErrInvoiceSourceMismatch)
			}
			cache[key] = expanded
		}
		*raw = expanded
	}
	return nil
}

func stripeResourceID(raw json.RawMessage) string {
	var id string
	if json.Unmarshal(raw, &id) == nil {
		return id
	}
	var resource struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &resource) != nil {
		return ""
	}
	return resource.ID
}
