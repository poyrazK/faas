package product

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Every portal sign-in reaches the product's login service from apid's
// address, so apid limits wrong passwords per product and email itself, and
// the login service's own 429 reaches the customer as a 429 with Retry-After
// rather than a 503 (bug hunt H81).
func TestProductCustomerSignInIsLimitedPerEmail(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "signin-limit@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "limited-product"})
	if err != nil {
		t.Fatal(err)
	}
	authApp, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "limited-auth"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProductCatalog(ctx, account.ID, app.ID, api.ProductCatalogSpec{AuthApp: authApp.Slug, Currency: "EUR",
		Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}},
		Plans:    map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}); err != nil {
		t.Fatal(err)
	}
	var tokenCalls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["email"] == "busy@example.com" {
			w.Header().Set("Retry-After", "17")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer upstream.Close()
	service := &Service{Host: customerTestHost{store}, Enabled: true, AuthClient: upstream.Client(),
		AuthOrigin: func(context.Context, state.Account, string) (state.App, string, error) {
			return authApp, upstream.URL, nil
		}}
	login := func(email string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "https://portal.example/v1/product-control/customer/"+app.Slug+"/session",
			strings.NewReader(`{"email":"`+email+`","password":"wrong"}`))
		r.SetPathValue("slug", app.Slug)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://portal.example")
		w := httptest.NewRecorder()
		service.Gate(service.CustomerLogin)(w, r)
		return w
	}
	for i := range api.ProductPortalSignInFailuresPerEmail {
		if w := login("victim@example.com"); w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong password %d = %d, want 401", i, w.Code)
		}
	}
	before := tokenCalls.Load()
	w := login("VICTIM@example.com")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("past the budget = %d (Retry-After %q), want 429", w.Code, w.Header().Get("Retry-After"))
	}
	if tokenCalls.Load() != before {
		t.Fatal("a limited sign-in reached the login service")
	}
	if w := login("someone-else@example.com"); w.Code != http.StatusUnauthorized {
		t.Fatalf("another customer = %d, want 401", w.Code)
	}
	if w := login("busy@example.com"); w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "17" {
		t.Fatalf("login service 429 = %d (Retry-After %q), want 429 with its Retry-After", w.Code, w.Header().Get("Retry-After"))
	}
}
