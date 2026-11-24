package stripeinvoice

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// creditPackAPIVersion supports explicitly disabling Adaptive Pricing.
const creditPackAPIVersion = "2024-11-20.acacia"

// CreditPackCheckoutParams describes a one-off card payment for prepaid credit.
type CreditPackCheckoutParams struct {
	PurchaseID, Customer, Label, Currency, ReturnURL string
	AmountMinor                                      int64
	ExpiresAt                                        time.Time
}

func (c *Client) CreateCreditPackCheckout(ctx context.Context, key string, p CreditPackCheckoutParams) (CheckoutSession, error) {
	form := url.Values{
		"mode":                                   {"payment"},
		"customer":                               {p.Customer},
		"payment_method_types[0]":                {"card"},
		"client_reference_id":                    {p.PurchaseID},
		"success_url":                            {p.ReturnURL},
		"cancel_url":                             {p.ReturnURL},
		"expires_at":                             {strconv.FormatInt(p.ExpiresAt.Unix(), 10)},
		"automatic_tax[enabled]":                 {"false"},
		"adaptive_pricing[enabled]":              {"false"},
		"line_items[0][quantity]":                {"1"},
		"line_items[0][price_data][currency]":    {strings.ToLower(p.Currency)},
		"line_items[0][price_data][unit_amount]": {strconv.FormatInt(p.AmountMinor, 10)},
		"line_items[0][price_data][product_data][name]": {p.Label},
	}
	var session CheckoutSession
	return session, c.doVersion(ctx, key, creditPackAPIVersion, http.MethodPost, "/v1/checkout/sessions", "gregale-credit-pack-"+p.PurchaseID, form, &session)
}

// GetCreditPackCheckout retrieves the payment session without expanding setup data.
func (c *Client) GetCreditPackCheckout(ctx context.Context, key, id string) (CheckoutSession, error) {
	var session CheckoutSession
	return session, c.doVersion(ctx, key, creditPackAPIVersion, http.MethodGet, "/v1/checkout/sessions/"+url.PathEscape(id), "", nil, &session)
}
