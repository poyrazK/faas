package stripeinvoice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Stripe-hosted Checkout and the customer portal, shared by Product Control
// self-serve signup (ADR-20261022100735) and API consumer sign-up and
// self-service (ADR-20261021143000, ADR-20261021183000). A setup-mode
// Checkout Session saves a card without charging it; the invoices exports
// create later charge it.

// SetupIntent is the part of a Checkout Session's SetupIntent signup reads.
type SetupIntent struct {
	Status        string `json:"status"`
	PaymentMethod string `json:"payment_method"`
}

// CheckoutSession is a Stripe-hosted Checkout Session. SetupIntent is set
// only when the response expanded it, as GetCheckoutSession does; Stripe
// otherwise sends its ID.
type CheckoutSession struct {
	ID          string
	URL         string
	Mode        string
	Status      string
	Customer    string
	Livemode    bool
	SetupIntent *SetupIntent
}

// UnmarshalJSON reads a session whose setup_intent is an ID or the expanded
// object.
func (s *CheckoutSession) UnmarshalJSON(raw []byte) error {
	var wire struct {
		ID          string          `json:"id"`
		URL         string          `json:"url"`
		Mode        string          `json:"mode"`
		Status      string          `json:"status"`
		Customer    string          `json:"customer"`
		Livemode    bool            `json:"livemode"`
		SetupIntent json.RawMessage `json:"setup_intent"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	*s = CheckoutSession{ID: wire.ID, URL: wire.URL, Mode: wire.Mode, Status: wire.Status, Customer: wire.Customer, Livemode: wire.Livemode}
	if len(wire.SetupIntent) > 0 && wire.SetupIntent[0] == '{' {
		var intent SetupIntent
		if err := json.Unmarshal(wire.SetupIntent, &intent); err != nil {
			return err
		}
		s.SetupIntent = &intent
	}
	return nil
}

// Complete reports whether the customer finished the checkout and Stripe
// saved the card.
func (s CheckoutSession) Complete() bool {
	return s.Status == "complete" && s.SetupIntent != nil && s.SetupIntent.Status == "succeeded" && s.SetupIntent.PaymentMethod != ""
}

// SetupCheckoutParams describe a setup-mode Checkout Session for Customer.
// A Currency offers the payment methods Stripe enables for it; without one
// the session saves a card. SuccessURL may contain {CHECKOUT_SESSION_ID}.
// A zero ExpiresAt keeps Stripe's default of 24 hours.
type SetupCheckoutParams struct {
	Customer          string
	Currency          string
	SuccessURL        string
	CancelURL         string
	ExpiresAt         time.Time
	ClientReferenceID string
	Metadata          map[string]string
}

// CreateSetupCheckout opens a setup-mode Checkout Session that saves a
// payment method to the customer without charging it.
func (c *Client) CreateSetupCheckout(ctx context.Context, key, idempotencyKey string, p SetupCheckoutParams) (CheckoutSession, error) {
	form := url.Values{}
	form.Set("mode", "setup")
	form.Set("customer", p.Customer)
	if p.Currency != "" {
		form.Set("currency", strings.ToLower(p.Currency))
	} else {
		form.Set("payment_method_types[0]", "card")
	}
	form.Set("success_url", p.SuccessURL)
	form.Set("cancel_url", p.CancelURL)
	if !p.ExpiresAt.IsZero() {
		form.Set("expires_at", strconv.FormatInt(p.ExpiresAt.Unix(), 10))
	}
	if p.ClientReferenceID != "" {
		form.Set("client_reference_id", p.ClientReferenceID)
	}
	for k, v := range p.Metadata {
		form.Set("metadata["+k+"]", v)
		form.Set("setup_intent_data[metadata]["+k+"]", v)
	}
	var out CheckoutSession
	return out, c.do(ctx, key, http.MethodPost, "/v1/checkout/sessions", idempotencyKey, form, &out)
}

// GetCheckoutSession reads a Checkout Session with its SetupIntent.
func (c *Client) GetCheckoutSession(ctx context.Context, key, id string) (CheckoutSession, error) {
	var out CheckoutSession
	return out, c.do(ctx, key, http.MethodGet, "/v1/checkout/sessions/"+url.PathEscape(id)+"?expand%5B%5D=setup_intent", "", nil, &out)
}

// CreatePortalSession returns a short-lived Stripe customer portal URL
// where the customer updates their card and sees and pays invoices
// (ADR-20261021183000). An empty returnURL uses the portal's default; live
// mode needs the portal saved once in the Stripe dashboard.
func (c *Client) CreatePortalSession(ctx context.Context, key, customer, returnURL string) (string, error) {
	form := url.Values{}
	form.Set("customer", customer)
	if returnURL != "" {
		form.Set("return_url", returnURL)
	}
	var out struct {
		URL string `json:"url"`
	}
	return out.URL, c.do(ctx, key, http.MethodPost, "/v1/billing_portal/sessions", "", form, &out)
}
