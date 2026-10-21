package stripeinvoice

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// adr: 20261021143000
func TestSetupCheckoutRequests(t *testing.T) {
	forms := map[string]map[string]string{}
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form := map[string]string{}
		for k := range r.PostForm {
			form[k] = r.PostForm.Get(k)
		}
		forms[r.Method+" "+r.URL.Path] = form
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/customers":
			_, _ = fmt.Fprint(w, `{"id":"cus_1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkout/sessions":
			_, _ = fmt.Fprint(w, `{"id":"cs_test_1","url":"https://checkout.stripe.com/c/pay/cs_test_1","status":"open","customer":"cus_1","setup_intent":"seti_1"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/checkout/sessions/cs_test_1":
			gotQuery = r.URL.RawQuery
			_, _ = fmt.Fprint(w, `{"id":"cs_test_1","status":"complete","customer":"cus_1","livemode":false,
				"setup_intent":{"id":"seti_1","status":"succeeded","payment_method":"pm_1"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/billing_portal/sessions":
			_, _ = fmt.Fprint(w, `{"id":"bps_1","url":"https://billing.stripe.com/p/session/bps_1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/customers/cus_1":
			_, _ = fmt.Fprint(w, `{"id":"cus_1"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"no such route"}}`)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()

	customer, err := c.CreateCustomer(ctx, "rk_test_k", "signup-1-customer", CustomerParams{Email: "ada@example.com", Name: "Ada",
		Metadata: map[string]string{"gregale_signup": "s1"}})
	if err != nil || customer != "cus_1" {
		t.Fatalf("CreateCustomer = %q, %v", customer, err)
	}
	if f := forms["POST /v1/customers"]; f["email"] != "ada@example.com" || f["name"] != "Ada" || f["metadata[gregale_signup]"] != "s1" {
		t.Fatalf("customer form = %v", f)
	}
	expires := time.Unix(1_900_000_000, 0)
	session, err := c.CreateSetupCheckout(ctx, "rk_test_k", "signup-1-checkout", SetupCheckoutParams{Customer: "cus_1",
		SuccessURL: "https://gregale.dev/ok", CancelURL: "https://gregale.dev/no", ExpiresAt: expires, ClientReferenceID: "s1",
		Metadata: map[string]string{"gregale_signup": "s1"}})
	if err != nil || session.ID != "cs_test_1" || session.URL == "" || session.Complete() {
		t.Fatalf("CreateSetupCheckout = %+v, %v", session, err)
	}
	f := forms["POST /v1/checkout/sessions"]
	if f["mode"] != "setup" || f["customer"] != "cus_1" || f["payment_method_types[0]"] != "card" || f["expires_at"] != "1900000000" ||
		f["success_url"] != "https://gregale.dev/ok" || f["client_reference_id"] != "s1" || f["setup_intent_data[metadata][gregale_signup]"] != "s1" {
		t.Fatalf("checkout form = %v", f)
	}
	read, err := c.GetCheckoutSession(ctx, "rk_test_k", "cs_test_1")
	if err != nil || !read.Complete() || read.PaymentMethod != "pm_1" || read.Customer != "cus_1" {
		t.Fatalf("GetCheckoutSession = %+v, %v", read, err)
	}
	if gotQuery != "expand[]=setup_intent" {
		t.Fatalf("session query = %q", gotQuery)
	}
	if err := c.SetDefaultPaymentMethod(ctx, "rk_test_k", "cus_1", "pm_1"); err != nil {
		t.Fatal(err)
	}
	if f := forms["POST /v1/customers/cus_1"]; f["invoice_settings[default_payment_method]"] != "pm_1" {
		t.Fatalf("default payment method form = %v", f)
	}
	portal, err := c.CreatePortalSession(ctx, "rk_test_k", "cus_1", "https://example.com/account")
	if err != nil || portal != "https://billing.stripe.com/p/session/bps_1" {
		t.Fatalf("CreatePortalSession = %q, %v", portal, err)
	}
	if f := forms["POST /v1/billing_portal/sessions"]; f["customer"] != "cus_1" || f["return_url"] != "https://example.com/account" {
		t.Fatalf("portal form = %v", f)
	}
}
