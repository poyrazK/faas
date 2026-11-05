package apidserver_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

// signupStripe is a Stripe account that creates customers and setup-mode
// Checkout Sessions and keeps each customer's default invoice card.
type signupStripe struct {
	mu        sync.Mutex
	keys      []string
	emails    []string
	defaults  map[string]string
	sessions  map[string]string
	checkouts []string
}

func (f *signupStripe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer rk_test_") {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	_ = r.ParseForm()
	customer := strings.TrimPrefix(r.URL.Path, "/v1/customers/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/invoices":
		_, _ = fmt.Fprint(w, `{"data":[],"has_more":false}`)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/customers":
		f.keys = append(f.keys, r.Header.Get("Idempotency-Key"))
		f.emails = append(f.emails, r.PostForm.Get("email"))
		_, _ = fmt.Fprint(w, `{"id":"cus_signup"}`)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
		f.checkouts = append(f.checkouts, r.PostForm.Get("mode")+" "+r.PostForm.Get("customer")+" "+r.PostForm.Get("currency"))
		scheme := "https"
		if r.PostForm.Get("customer") == "cus_plain" {
			scheme = "http"
		}
		_, _ = fmt.Fprintf(w, `{"id":"cs_test_new","url":"%s://checkout.stripe.com/c/pay/cs_test_new"}`, scheme)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/checkout/sessions/"):
		session, ok := f.sessions[strings.TrimPrefix(r.URL.Path, "/v1/checkout/sessions/")]
		if !ok || r.URL.Query().Get("expand[]") != "setup_intent" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"error":{"code":"resource_missing"}}`)
			return
		}
		_, _ = fmt.Fprint(w, session)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/customers/"):
		f.defaults[customer] = r.PostForm.Get("invoice_settings[default_payment_method]")
		_, _ = fmt.Fprintf(w, `{"id":%q}`, customer)
	case r.Method == http.MethodGet && customer == "cus_locked":
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"error":{"message":"restricted key"}}`)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/customers/"):
		if f.defaults[customer] == "" {
			_, _ = fmt.Fprintf(w, `{"id":%q,"invoice_settings":{"default_payment_method":null}}`, customer)
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"invoice_settings":{"default_payment_method":{"id":%q,"card":{"brand":"visa","last4":"4242","exp_month":4,"exp_year":2030}}}}`, customer, f.defaults[customer])
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// adr: 20261022100735 — signup creates its customer in the account's
// connected Stripe under the request's idempotency key; a card is saved only
// from a completed setup Checkout of the same customer with a succeeded
// SetupIntent; only Stripe customers have a card on file here, and a
// restricted key reads as unavailable.
func TestProductSignupStripeCustomerAndCard(t *testing.T) {
	withBillingKeys(t)
	e := apidserver.Setup(t, api.PlanPro)
	acct := *e.Acct()
	stripe := &signupStripe{defaults: map[string]string{}, sessions: map[string]string{
		"cs_test_done":    `{"id":"cs_test_done","mode":"setup","status":"complete","customer":"cus_signup","setup_intent":{"status":"succeeded","payment_method":"pm_card"}}`,
		"cs_test_open":    `{"id":"cs_test_open","mode":"setup","status":"open","customer":"cus_signup","setup_intent":{"status":"requires_payment_method","payment_method":""}}`,
		"cs_test_foreign": `{"id":"cs_test_foreign","mode":"setup","status":"complete","customer":"cus_other","setup_intent":{"status":"succeeded","payment_method":"pm_other"}}`,
		"cs_test_payment": `{"id":"cs_test_payment","mode":"payment","status":"complete","customer":"cus_signup","setup_intent":null}`,
	}}
	srv := httptest.NewServer(stripe)
	defer srv.Close()
	invoices := (*e.S()).StatementInvoices()
	invoices.Stripe = &stripeinvoice.Client{BaseURL: srv.URL, HTTP: srv.Client()}
	ctx := t.Context()

	if _, problem := invoices.SignupCustomer(ctx, acct, state.BillingProviderStripe, "k", "ada@example.com", "Ada", nil); problem == nil || problem.Code != api.CodeProductSignupUnavailable {
		t.Fatalf("signup before Stripe is connected: %+v", problem)
	}
	if _, problem := invoices.StripePaymentMethod(ctx, acct, "cus_signup"); problem == nil || problem.Code != api.CodeProductPaymentMethodUnavailable {
		t.Fatalf("card before Stripe is connected: %+v", problem)
	}
	if res := e.Do(t, http.MethodPut, "/v1/account/billing-connections/stripe", api.PutBillingConnectionRequest{SecretKey: "rk_test_51abcdWXYZ"}, nil); res.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", res.Code, res.Body)
	}
	id, problem := invoices.SignupCustomer(ctx, acct, state.BillingProviderStripe, "gregale-signup-app-req", "ada@example.com", "Ada", map[string]string{"gregale_app": "app"})
	if problem != nil || id != "cus_signup" || len(stripe.keys) != 1 || stripe.keys[0] != "gregale-signup-app-req" || stripe.emails[0] != "ada@example.com" {
		t.Fatalf("signup customer = %q %+v %v %v", id, problem, stripe.keys, stripe.emails)
	}
	if _, problem := invoices.SignupCustomer(ctx, acct, "lago", "k", "ada@example.com", "Ada", nil); problem == nil || problem.Code != api.CodeProductSignupUnavailable {
		t.Fatalf("signup with Lago: %+v", problem)
	}

	if _, problem := invoices.StripePaymentMethod(ctx, acct, "ctm_paddle"); problem == nil || problem.Code != api.CodeProductPaymentMethodUnavailable {
		t.Fatalf("card for a Paddle customer: %+v", problem)
	}
	if card, problem := invoices.StripePaymentMethod(ctx, acct, "cus_signup"); card != nil || problem != nil {
		t.Fatalf("customer without a card = %+v %+v", card, problem)
	}
	if _, problem := invoices.StripePaymentMethod(ctx, acct, "cus_locked"); problem == nil || problem.Code != api.CodeProductPaymentMethodUnavailable {
		t.Fatalf("restricted key: %+v", problem)
	}
	url, problem := invoices.StripeSetupCheckout(ctx, acct, "cus_signup", "EUR", "", "https://gregale.example/s", "https://gregale.example/c")
	if problem != nil || url != "https://checkout.stripe.com/c/pay/cs_test_new" || stripe.checkouts[0] != "setup cus_signup eur" {
		t.Fatalf("checkout = %q %+v %v", url, problem, stripe.checkouts)
	}
	if url, problem := invoices.StripeSetupCheckout(ctx, acct, "cus_plain", "EUR", "", "https://gregale.example/s", "https://gregale.example/c"); problem == nil || url != "" {
		t.Fatalf("plain-http Checkout page accepted: %q", url)
	}

	for _, session := range []string{"cs_test_open", "cs_test_foreign", "cs_test_payment"} {
		if card, problem := invoices.CompleteStripeSetup(ctx, acct, "cus_signup", session); card != nil || problem == nil || problem.Code != api.CodeProductPaymentMethodUnavailable {
			t.Fatalf("%s saved a card: %+v %+v", session, card, problem)
		}
	}
	if len(stripe.defaults) != 0 {
		t.Fatalf("an incomplete or foreign Checkout set a default card: %v", stripe.defaults)
	}
	card, problem := invoices.CompleteStripeSetup(ctx, acct, "cus_signup", "cs_test_done")
	if problem != nil || card == nil || card.Brand != "visa" || card.Last4 != "4242" || stripe.defaults["cus_signup"] != "pm_card" {
		t.Fatalf("completed Checkout = %+v %+v %v", card, problem, stripe.defaults)
	}
}
