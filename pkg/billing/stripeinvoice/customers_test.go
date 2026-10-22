package stripeinvoice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// adr: 20261022100735 — a signup creates its Stripe customer under an
// idempotency key; a setup-mode Checkout saves a card without charging it;
// completion reads the session with its SetupIntent and makes the card the
// customer's invoice default; only the card's brand, last digits and expiry
// are read back.
func TestStripeSignupCustomerAndCardOnFile(t *testing.T) {
	var requests []string
	var forms []map[string]string
	var idempotency []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		idempotency = append(idempotency, r.Header.Get("Idempotency-Key"))
		_ = r.ParseForm()
		form := map[string]string{}
		for k := range r.PostForm {
			form[k] = r.PostForm.Get(k)
		}
		forms = append(forms, form)
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/customers":
			_, _ = fmt.Fprint(w, `{"id":"cus_new"}`)
		case "POST /v1/checkout/sessions":
			_, _ = fmt.Fprint(w, `{"id":"cs_test_1","url":"https://checkout.stripe.com/c/pay/cs_test_1","mode":"setup","status":"open","customer":"cus_new"}`)
		case "GET /v1/checkout/sessions/cs_test_1":
			_, _ = fmt.Fprint(w, `{"id":"cs_test_1","mode":"setup","status":"complete","customer":"cus_new","setup_intent":{"id":"seti_1","status":"succeeded","payment_method":"pm_1"}}`)
		case "POST /v1/customers/cus_new":
			_, _ = fmt.Fprint(w, `{"id":"cus_new"}`)
		case "GET /v1/customers/cus_new":
			_, _ = fmt.Fprint(w, `{"id":"cus_new","invoice_settings":{"default_payment_method":{"id":"pm_1","card":{"brand":"visa","last4":"4242","exp_month":4,"exp_year":2030,"fingerprint":"never-read"}}}}`)
		case "GET /v1/customers/cus_bare":
			_, _ = fmt.Fprint(w, `{"id":"cus_bare","invoice_settings":{"default_payment_method":null}}`)
		case "GET /v1/customers/cus_locked":
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"restricted key"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"error":{"type":"invalid_request_error","code":"resource_missing"}}`)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()
	const key = "rk_test_abcd"

	id, err := c.CreateCustomer(ctx, key, "gregale-signup-app-req", "ada@example.com", "Ada's workspace", map[string]string{"gregale_app": "exports"})
	if err != nil || id != "cus_new" || idempotency[0] != "gregale-signup-app-req" ||
		forms[0]["email"] != "ada@example.com" || forms[0]["name"] != "Ada's workspace" || forms[0]["metadata[gregale_app]"] != "exports" {
		t.Fatalf("CreateCustomer = %q %v %v %v", id, err, idempotency, forms[0])
	}
	session, err := c.CreateSetupCheckout(ctx, key, "", SetupCheckoutParams{Customer: "cus_new", Currency: "EUR",
		SuccessURL: "https://gregale.example/p?checkout={CHECKOUT_SESSION_ID}", CancelURL: "https://gregale.example/p?checkout=cancelled"})
	f := forms[1]
	if err != nil || session.URL != "https://checkout.stripe.com/c/pay/cs_test_1" || f["mode"] != "setup" || f["customer"] != "cus_new" || f["currency"] != "eur" ||
		f["payment_method_types[0]"] != "" || f["expires_at"] != "" ||
		f["success_url"] != "https://gregale.example/p?checkout={CHECKOUT_SESSION_ID}" || f["cancel_url"] != "https://gregale.example/p?checkout=cancelled" || idempotency[1] != "" {
		t.Fatalf("CreateSetupCheckout = %+v %v %v", session, err, f)
	}
	session, err = c.GetCheckoutSession(ctx, key, "cs_test_1")
	if err != nil || requests[2] != "GET /v1/checkout/sessions/cs_test_1?expand%5B%5D=setup_intent" || session.SetupIntent == nil ||
		session.SetupIntent.Status != "succeeded" || session.SetupIntent.PaymentMethod != "pm_1" || session.Customer != "cus_new" {
		t.Fatalf("GetCheckoutSession = %+v %v %v", session, err, requests)
	}
	if err := c.SetDefaultPaymentMethod(ctx, key, "cus_new", "pm_1"); err != nil || forms[3]["invoice_settings[default_payment_method]"] != "pm_1" {
		t.Fatalf("SetDefaultPaymentMethod = %v %v", err, forms[3])
	}
	card, ok, err := c.DefaultPaymentMethod(ctx, key, "cus_new")
	if err != nil || !ok || card != (Card{Brand: "visa", Last4: "4242", ExpMonth: 4, ExpYear: 2030}) ||
		requests[4] != "GET /v1/customers/cus_new?expand%5B%5D=invoice_settings.default_payment_method" {
		t.Fatalf("DefaultPaymentMethod = %+v %v %v %v", card, ok, err, requests)
	}
	if _, ok, err := c.DefaultPaymentMethod(ctx, key, "cus_bare"); ok || err != nil {
		t.Fatalf("customer without a card = %v %v", ok, err)
	}
	if _, _, err := c.DefaultPaymentMethod(ctx, key, "cus_locked"); !errors.Is(err, ErrKeyRejected) {
		t.Fatalf("restricted key = %v", err)
	}
}
