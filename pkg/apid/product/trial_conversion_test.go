package product

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261022180921 — reading or saving the workspace's card records
// whether one is on file; the resolution step runs only while Product Control
// is enabled and audits each ended trial as converted or fallen back; a start
// awaiting a card is refused with 402 and a self-service change with 409.
func TestProductTrialConversion(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "trial-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "converting"})
	if err != nil {
		t.Fatal(err)
	}
	grant := map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}},
		Plans: map[string]api.ProductPlan{"free": {Label: "Free", Features: grant},
			"pro": {Label: "Pro", TrialDays: 14, RequiresPaymentMethod: true, FallbackPlan: "free", Features: grant}}}
	if _, err := store.PutProductCatalog(ctx, account.ID, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	assigned := time.Now()
	customer := func(ref string) (string, string) {
		t.Helper()
		tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, ref, ref, 100)
		if err != nil {
			t.Fatal(err)
		}
		org := uuid.NewString()
		if _, err := store.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "pro", AuthOrganizationID: org}); err != nil {
			t.Fatal(err)
		}
		// The trial ends a minute after the assignment.
		store.SeedProductTrialForTest(app.ID, tenant.ID, assigned.AddDate(0, 0, -14), assigned.Add(time.Minute))
		return tenant.ID, org
	}
	carded, cardedOrg := customer("cus_carded")
	lapsed, _ := customer("cus_lapsed")
	host := &overdueTestHost{customerTestHost: customerTestHost{store: store}}
	card := &api.ProductPaymentMethod{Provider: "stripe", Brand: "visa", Last4: "4242", ExpMonth: 4, ExpYear: 2030}
	onFile := card
	s := &Service{Host: host, EmailOrigin: "https://gregale.example",
		PaymentMethod: func(context.Context, state.Account, string) (*api.ProductPaymentMethod, *api.Problem) {
			return onFile, nil
		},
		PaymentMethodCheckout: func(context.Context, state.Account, string, string, string, string, string) (string, *api.Problem) {
			return "https://checkout.stripe.com/c/pay/cs_test_1", nil
		},
		CompletePaymentMethod: func(context.Context, state.Account, string, string) (*api.ProductPaymentMethod, *api.Problem) {
			return card, nil
		}}
	call := func(handler func(http.ResponseWriter, *http.Request, state.Account), method, body string) {
		t.Helper()
		r := httptest.NewRequest(method, "/", strings.NewReader(body))
		r.SetPathValue("slug", app.Slug)
		r = r.WithContext(authmw.WithProductCustomer(r.Context(), account, authmw.ProductCustomerIdentity{TenantID: carded, OrganizationID: cardedOrg, ProductSlug: app.Slug,
			Access: api.ProductCustomerAccess{Role: "owner", Permissions: api.ProductPermissions{ManageBilling: true}}}))
		w := httptest.NewRecorder()
		handler(w, r, account)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: %d: %s", method, body, w.Code, w.Body)
		}
	}
	recorded := func() *time.Time {
		t.Helper()
		c, err := store.GetProductCustomer(ctx, account.ID, app.ID, carded)
		if err != nil {
			t.Fatal(err)
		}
		return c.PaymentMethodAt
	}
	// Reading the card records it, and clears it once Stripe has none.
	call(s.CustomerPaymentMethod, "GET", "")
	if recorded() == nil {
		t.Fatal("a read card was not recorded")
	}
	onFile = nil
	call(s.CustomerPaymentMethod, "GET", "")
	if recorded() != nil {
		t.Fatal("a removed card stayed recorded")
	}
	// Completing Checkout records it.
	call(s.CustomerPaymentMethodComplete, "POST", `{"session_id":"cs_test_123456"}`)
	if recorded() == nil {
		t.Fatal("a saved card was not recorded")
	}

	if events := host.take(); !slices.Equal(events, []string{"product.customer.payment_method_saved " + carded}) {
		t.Fatalf("card audits = %v", events)
	}

	// Resolution waits for Product Control and the trial's end.
	s.Now = func() time.Time { return assigned.Add(2 * time.Minute) }
	if err := s.ResolveTrialsOnce(ctx); err != nil || len(host.take()) != 0 {
		t.Fatalf("resolution while disabled: %v", err)
	}
	s.Enabled = true
	if err := s.ResolveTrialsOnce(ctx); err != nil {
		t.Fatal(err)
	}
	events := host.take()
	slices.Sort(events)
	want := []string{"product.customer.trial_converted " + carded, "product.customer.trial_fell_back " + lapsed}
	slices.Sort(want)
	if !slices.Equal(events, want) {
		t.Fatalf("resolution audits = %v, want %v", events, want)
	}
	if c, err := store.GetProductCustomer(ctx, account.ID, app.ID, lapsed); err != nil || c.Plan != "free" || c.TrialResolvedAt == nil {
		t.Fatalf("lapsed customer = %+v %v", c, err)
	}
	if err := s.ResolveTrialsOnce(ctx); err != nil || len(host.take()) != 0 {
		t.Fatalf("second resolution: %v", err)
	}

	// The refusals name the code: 402 for a start, 409 for a plan change.
	for _, tc := range []struct {
		write  func(http.ResponseWriter)
		status int
	}{
		{func(w http.ResponseWriter) { operation.WriteOperationError(w, state.ErrProductPaymentMethodRequired) }, http.StatusPaymentRequired},
		{func(w http.ResponseWriter) { planRespond(w, nil, state.ErrProductPaymentMethodRequired) }, http.StatusConflict},
	} {
		w := httptest.NewRecorder()
		tc.write(w)
		var problem api.Problem
		if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil || w.Code != tc.status || problem.Code != api.CodeProductPaymentMethodRequired {
			t.Fatalf("refusal = %d %s %v", w.Code, w.Body, err)
		}
	}
}
