package product

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261022100735 — only a role with manage_billing reaches the
// workspace's Stripe customer; Checkout opens only after consent and returns
// to the portal on the platform's origin; completion is audited; a workspace
// not billed through Stripe reads as unavailable.
func TestProductCustomerPaymentMethod(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "card-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "carded"})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}},
		Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	if _, err := store.PutProductCatalog(ctx, account.ID, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "cus_card1", "Workspace", 100)
	if err != nil {
		t.Fatal(err)
	}
	org := uuid.NewString()
	host := &billingDetailsTestHost{customerTestHost: customerTestHost{store: store}}
	var reached []string
	var problem *api.Problem
	card := &api.ProductPaymentMethod{Provider: "stripe", Brand: "visa", Last4: "4242", ExpMonth: 4, ExpYear: 2030}
	var checkout []string
	s := &Service{Host: host, EmailOrigin: "https://gregale.example",
		PaymentMethod: func(_ context.Context, acct state.Account, customer string) (*api.ProductPaymentMethod, *api.Problem) {
			reached = append(reached, acct.ID+"/"+customer)
			return nil, problem
		},
		PaymentMethodCheckout: func(_ context.Context, acct state.Account, customer, currency, key, success, cancel string) (string, *api.Problem) {
			reached = append(reached, acct.ID+"/"+customer)
			checkout = []string{currency, key, success, cancel}
			return "https://checkout.stripe.com/c/pay/cs_test_1", problem
		},
		CompletePaymentMethod: func(_ context.Context, acct state.Account, customer, session string) (*api.ProductPaymentMethod, *api.Problem) {
			reached = append(reached, acct.ID+"/"+customer+"/"+session)
			return card, problem
		}}
	call := func(handler func(http.ResponseWriter, *http.Request, state.Account), method, body string, permissions api.ProductPermissions, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/", strings.NewReader(body))
		r.SetPathValue("slug", app.Slug)
		r = r.WithContext(authmw.WithProductCustomer(r.Context(), account, authmw.ProductCustomerIdentity{TenantID: tenant.ID, OrganizationID: org, ProductSlug: app.Slug,
			Access: api.ProductCustomerAccess{Role: "owner", Permissions: permissions}}))
		w := httptest.NewRecorder()
		handler(w, r, account)
		if w.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, body, w.Code, status, w.Body)
		}
		return w
	}
	readOnly := api.ProductPermissions{ViewWork: true, ViewBilling: true, ManagePlan: true}
	manager := api.ProductPermissions{ViewBilling: true, ManageBilling: true}
	call(s.CustomerPaymentMethod, "GET", "", readOnly, http.StatusForbidden)
	call(s.CustomerPaymentMethodCheckout, "POST", `{"consent":true}`, readOnly, http.StatusForbidden)
	call(s.CustomerPaymentMethodComplete, "POST", `{"session_id":"cs_test_123456"}`, readOnly, http.StatusForbidden)
	if len(reached) != 0 {
		t.Fatalf("a role without manage_billing reached Stripe: %v", reached)
	}

	var view api.ProductPaymentMethodView
	w := call(s.CustomerPaymentMethod, "GET", "", manager, http.StatusOK)
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || !view.Available || view.PaymentMethod != nil || reached[0] != account.ID+"/cus_card1" {
		t.Fatalf("no card = %+v %v %v", view, err, reached)
	}
	call(s.CustomerPaymentMethodCheckout, "POST", `{"consent":false}`, manager, http.StatusBadRequest)
	if len(reached) != 1 {
		t.Fatalf("checkout without consent reached Stripe: %v", reached)
	}
	w = call(s.CustomerPaymentMethodCheckout, "POST", `{"consent":true}`, manager, http.StatusOK)
	back := "https://gregale.example/v1/product-control/portal?app=carded&workspace=" + org
	if !strings.Contains(w.Body.String(), "https://checkout.stripe.com/c/pay/cs_test_1") || checkout[0] != "EUR" ||
		checkout[2] != back+"&checkout={CHECKOUT_SESSION_ID}" || checkout[3] != back+"&checkout=cancelled" {
		t.Fatalf("checkout = %s %v", w.Body, checkout)
	}
	for _, bad := range []string{`{"session_id":""}`, `{"session_id":"cs_<script>"}`, `{"session_id":"` + strings.Repeat("a", 256) + `"}`} {
		call(s.CustomerPaymentMethodComplete, "POST", bad, manager, http.StatusBadRequest)
	}
	w = call(s.CustomerPaymentMethodComplete, "POST", `{"session_id":"cs_test_123456"}`, manager, http.StatusOK)
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || view.PaymentMethod == nil || view.PaymentMethod.Last4 != "4242" || reached[len(reached)-1] != account.ID+"/cus_card1/cs_test_123456" {
		t.Fatalf("saved card = %+v %v %v", view, err, reached)
	}
	if len(host.audits) != 2 || host.audits[0]["kind"] != "product.customer.payment_method_consented" || host.audits[1]["kind"] != "product.customer.payment_method_saved" {
		t.Fatalf("audits = %v", host.audits)
	}
	if data := host.audits[1]["data"].(map[string]any); data["tenant_id"] != tenant.ID || data["organization_id"] != org || strings.Contains(strings.ToLower(jsonText(data)), "4242") {
		t.Fatalf("audit data = %v", data)
	}

	problem = api.NewProblem(http.StatusConflict, api.CodeProductPaymentMethodUnavailable, "Payment method unavailable", "not billed through Stripe")
	w = call(s.CustomerPaymentMethod, "GET", "", manager, http.StatusOK)
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || view.Available {
		t.Fatalf("unavailable = %s %v", w.Body, err)
	}
	call(s.CustomerPaymentMethodCheckout, "POST", `{"consent":true}`, manager, http.StatusConflict)
	s.EmailOrigin = ""
	call(s.CustomerPaymentMethodCheckout, "POST", `{"consent":true}`, manager, http.StatusServiceUnavailable)
}

func jsonText(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
