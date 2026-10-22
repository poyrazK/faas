package stripeinvoice

import (
	"context"
	"net/http"
	"net/url"
)

// Card summarizes a saved card; Stripe keeps the card itself.
type Card struct {
	Brand    string `json:"brand"`
	Last4    string `json:"last4"`
	ExpMonth int    `json:"exp_month"`
	ExpYear  int    `json:"exp_year"`
}

// DefaultPaymentMethod returns the customer's default invoice card; ok is
// false without one.
func (c *Client) DefaultPaymentMethod(ctx context.Context, key, customer string) (Card, bool, error) {
	var out struct {
		InvoiceSettings struct {
			DefaultPaymentMethod *struct {
				Card *Card `json:"card"`
			} `json:"default_payment_method"`
		} `json:"invoice_settings"`
	}
	path := "/v1/customers/" + url.PathEscape(customer) + "?expand%5B%5D=invoice_settings.default_payment_method"
	if err := c.do(ctx, key, http.MethodGet, path, "", nil, &out); err != nil {
		return Card{}, false, err
	}
	pm := out.InvoiceSettings.DefaultPaymentMethod
	if pm == nil || pm.Card == nil {
		return Card{}, false, nil
	}
	return *pm.Card, true, nil
}
