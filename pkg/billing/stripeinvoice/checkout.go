package stripeinvoice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Consumer sign-up (ADR-20261021143000): a customer of the key holder's
// API is created in the key holder's Stripe account and saves a card with
// a setup-mode Checkout Session, which the usage invoices later charge.

// CustomerParams describe a new Stripe customer.
type CustomerParams struct {
	Email    string
	Name     string
	Metadata map[string]string
}

// CreateCustomer creates a customer. idempotencyKey makes a retry within
// Stripe's 24-hour window return the same customer.
func (c *Client) CreateCustomer(ctx context.Context, key, idempotencyKey string, p CustomerParams) (string, error) {
	form := url.Values{}
	form.Set("email", p.Email)
	form.Set("name", p.Name)
	for k, v := range p.Metadata {
		form.Set("metadata["+k+"]", v)
	}
	var out struct {
		ID string `json:"id"`
	}
	return out.ID, c.do(ctx, key, http.MethodPost, "/v1/customers", idempotencyKey, form, &out)
}

// SetupCheckoutParams describe a setup-mode Checkout Session that saves a
// card for Customer without charging it.
type SetupCheckoutParams struct {
	Customer          string
	SuccessURL        string
	CancelURL         string
	ExpiresAt         time.Time
	ClientReferenceID string
	Metadata          map[string]string
}

// CheckoutSession is the part of a Checkout Session sign-up reads.
// SetupIntentStatus and PaymentMethod come from the expanded SetupIntent.
type CheckoutSession struct {
	ID                string
	URL               string
	Status            string
	Customer          string
	Livemode          bool
	SetupIntentStatus string
	PaymentMethod     string
}

// Complete reports whether the customer finished the checkout and Stripe
// saved the card.
func (s CheckoutSession) Complete() bool {
	return s.Status == "complete" && s.SetupIntentStatus == "succeeded" && s.PaymentMethod != ""
}

// checkoutSessionJSON's setup_intent is an ID unless the request expanded
// it into an object.
type checkoutSessionJSON struct {
	ID          string          `json:"id"`
	URL         string          `json:"url"`
	Status      string          `json:"status"`
	Customer    string          `json:"customer"`
	Livemode    bool            `json:"livemode"`
	SetupIntent json.RawMessage `json:"setup_intent"`
}

func (j checkoutSessionJSON) session() CheckoutSession {
	out := CheckoutSession{ID: j.ID, URL: j.URL, Status: j.Status, Customer: j.Customer, Livemode: j.Livemode}
	var intent struct {
		Status        string `json:"status"`
		PaymentMethod string `json:"payment_method"`
	}
	if json.Unmarshal(j.SetupIntent, &intent) == nil {
		out.SetupIntentStatus, out.PaymentMethod = intent.Status, intent.PaymentMethod
	}
	return out
}

// CreateSetupCheckout creates a setup-mode Checkout Session for cards.
func (c *Client) CreateSetupCheckout(ctx context.Context, key, idempotencyKey string, p SetupCheckoutParams) (CheckoutSession, error) {
	form := url.Values{}
	form.Set("mode", "setup")
	form.Set("customer", p.Customer)
	form.Set("payment_method_types[0]", "card")
	form.Set("success_url", p.SuccessURL)
	form.Set("cancel_url", p.CancelURL)
	form.Set("expires_at", strconv.FormatInt(p.ExpiresAt.Unix(), 10))
	if p.ClientReferenceID != "" {
		form.Set("client_reference_id", p.ClientReferenceID)
	}
	for k, v := range p.Metadata {
		form.Set("metadata["+k+"]", v)
		form.Set("setup_intent_data[metadata]["+k+"]", v)
	}
	var out checkoutSessionJSON
	if err := c.do(ctx, key, http.MethodPost, "/v1/checkout/sessions", idempotencyKey, form, &out); err != nil {
		return CheckoutSession{}, err
	}
	return out.session(), nil
}

// GetCheckoutSession reads a Checkout Session with its SetupIntent.
func (c *Client) GetCheckoutSession(ctx context.Context, key, sessionID string) (CheckoutSession, error) {
	var out checkoutSessionJSON
	if err := c.do(ctx, key, http.MethodGet, "/v1/checkout/sessions/"+url.PathEscape(sessionID)+"?expand[]=setup_intent", "", nil, &out); err != nil {
		return CheckoutSession{}, err
	}
	return out.session(), nil
}

// SetDefaultPaymentMethod makes a saved payment method the one the
// customer's invoices charge.
func (c *Client) SetDefaultPaymentMethod(ctx context.Context, key, customer, paymentMethod string) error {
	form := url.Values{}
	form.Set("invoice_settings[default_payment_method]", paymentMethod)
	return c.do(ctx, key, http.MethodPost, "/v1/customers/"+url.PathEscape(customer), "", form, nil)
}
