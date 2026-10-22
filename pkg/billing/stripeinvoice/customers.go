package stripeinvoice

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// CreateSignupCustomer creates a customer in the account's Stripe for a
// self-serve signup (ADR-20261022100735). idempotencyKey makes a retry within
// Stripe's 24-hour window return the same customer.
func (c *Client) CreateSignupCustomer(ctx context.Context, key, idempotencyKey, email, name string, metadata map[string]string) (string, error) {
	form := url.Values{}
	form.Set("email", email)
	form.Set("name", name)
	for k, v := range metadata {
		form.Set("metadata["+k+"]", v)
	}
	var out struct {
		ID string `json:"id"`
	}
	return out.ID, c.do(ctx, key, http.MethodPost, "/v1/customers", idempotencyKey, form, &out)
}

// SignupCheckoutSession is a Stripe-hosted Checkout Session for a self-serve
// signup.
type SignupCheckoutSession struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Mode     string `json:"mode"`
	Status   string `json:"status"`
	Customer string `json:"customer"`
	// SetupIntent is expanded by GetSignupCheckoutSession.
	SetupIntent *struct {
		Status        string `json:"status"`
		PaymentMethod string `json:"payment_method"`
	} `json:"setup_intent"`
}

// CreateSignupSetupCheckout opens a Stripe-hosted Checkout Session in setup
// mode that saves a payment method to customer without charging it
// (ADR-20261022100735). successURL may contain {CHECKOUT_SESSION_ID}.
func (c *Client) CreateSignupSetupCheckout(ctx context.Context, key, idempotencyKey, customer, currency, successURL, cancelURL string) (SignupCheckoutSession, error) {
	form := url.Values{}
	form.Set("mode", "setup")
	form.Set("customer", customer)
	form.Set("currency", strings.ToLower(currency))
	form.Set("success_url", successURL)
	form.Set("cancel_url", cancelURL)
	var out SignupCheckoutSession
	return out, c.do(ctx, key, http.MethodPost, "/v1/checkout/sessions", idempotencyKey, form, &out)
}

// GetSignupCheckoutSession reads a signup Checkout Session with its
// SetupIntent.
func (c *Client) GetSignupCheckoutSession(ctx context.Context, key, id string) (SignupCheckoutSession, error) {
	var out SignupCheckoutSession
	return out, c.do(ctx, key, http.MethodGet, "/v1/checkout/sessions/"+url.PathEscape(id)+"?expand%5B%5D=setup_intent", "", nil, &out)
}

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
