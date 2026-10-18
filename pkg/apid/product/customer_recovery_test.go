package product

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type customerRecoveryFixture struct {
	service Service
	store   *state.MemStore
	account state.Account
	app     state.App
	authApp state.App
	spec    api.ProductCatalogSpec
	lookups int
}

func newCustomerRecoveryFixture(t *testing.T) *customerRecoveryFixture {
	t.Helper()
	f := &customerRecoveryFixture{store: state.NewMemStore()}
	var err error
	f.account, err = f.store.CreateAccount(t.Context(), "recovery-owner@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.app, err = f.store.CreateApp(t.Context(), state.App{AccountID: f.account.ID, Slug: "recovery-product"})
	if err != nil {
		t.Fatal(err)
	}
	f.authApp, err = f.store.CreateApp(t.Context(), state.App{AccountID: f.account.ID, Slug: "recovery-auth"})
	if err != nil {
		t.Fatal(err)
	}
	f.spec = api.ProductCatalogSpec{AuthApp: f.authApp.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10}}}}}
	if _, err := f.store.PutProductCatalog(t.Context(), f.account.ID, f.app.ID, f.spec); err != nil {
		t.Fatal(err)
	}
	f.service = Service{Host: customerTestHost{f.store}, Enabled: true, AuthOrigin: func(ctx context.Context, account state.Account, slug string) (state.App, string, error) {
		f.lookups++
		app, err := f.store.AppBySlug(ctx, slug)
		if err != nil || app.AccountID != account.ID {
			return state.App{}, "", state.ErrNotFound
		}
		return app, "https://" + app.Slug + ".example.test", nil
	}}
	return f
}

func (f *customerRecoveryFixture) request(query string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "https://portal.example/v1/product-control/customer/"+f.app.Slug+"/account-recovery"+query, nil)
	r.SetPathValue("slug", f.app.Slug)
	r.Host = "untrusted-request-host.example"
	r.Header.Set("Authorization", "Bearer OWNER_TOKEN_MUST_NOT_LEAVE")
	r.AddCookie(&http.Cookie{Name: "customer", Value: "CUSTOMER_COOKIE_MUST_NOT_LEAVE"})
	w := httptest.NewRecorder()
	f.service.Gate(f.service.CustomerRecovery)(w, r)
	return w
}

func TestProductCustomerRecoveryUsesLatestOwnedRealmWithoutCredentials(t *testing.T) {
	f := newCustomerRecoveryFixture(t)
	w := f.request("")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "https://recovery-auth.example.test/ui/forgot-password#" {
		t.Fatalf("recovery: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" || len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "MUST_NOT_LEAVE") {
		t.Fatal("recovery changed or exposed session state")
	}
	other, err := f.store.CreateApp(t.Context(), state.App{AccountID: f.account.ID, Slug: "replacement-auth"})
	if err != nil {
		t.Fatal(err)
	}
	f.spec.AuthApp = other.Slug
	if _, err := f.store.PutProductCatalog(t.Context(), f.account.ID, f.app.ID, f.spec); err != nil {
		t.Fatal(err)
	}
	w = f.request("")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "https://replacement-auth.example.test/ui/forgot-password#" || f.lookups != 2 {
		t.Fatal("recovery reused the previous realm")
	}
}

func TestProductCustomerRecoveryRejectsCallerSelectedTargetsAndDisabledGate(t *testing.T) {
	for _, query := range []string{"?redirect_to=https://evil.example", "?email=private@example.test", "?token=private", "?app=other", "?origin=https://evil.example"} {
		t.Run(query, func(t *testing.T) {
			f := newCustomerRecoveryFixture(t)
			w := f.request(query)
			if w.Code != http.StatusBadRequest || w.Header().Get("Location") != "" || f.lookups != 0 || strings.Contains(w.Body.String(), "private") {
				t.Fatal("caller selected recovery scope or target")
			}
		})
	}
	f := newCustomerRecoveryFixture(t)
	f.service.Enabled = false
	if w := f.request(""); w.Code != http.StatusNotFound || f.lookups != 0 || w.Header().Get("Location") != "" {
		t.Fatal("disabled recovery reached the realm")
	}
}

func TestProductCustomerRecoveryRejectsInvalidRealmOrigins(t *testing.T) {
	for _, origin := range []string{"", "http://auth.example.test", "//auth.example.test", "https://user:password@auth.example.test", "https://auth.example.test/untrusted", "https://auth.example.test?redirect_to=evil", "https://auth.example.test?", "https://auth.example.test#token", "https://auth.example.test\r\nLocation: https://evil.example"} {
		t.Run(origin, func(t *testing.T) {
			f := newCustomerRecoveryFixture(t)
			f.service.AuthOrigin = func(context.Context, state.Account, string) (state.App, string, error) { return f.authApp, origin, nil }
			w := f.request("")
			if w.Code != http.StatusForbidden || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "evil.example") {
				t.Fatal("untrusted recovery origin accepted or reflected")
			}
		})
	}
}

func TestProductCustomerRecoveryRequiresAnAvailableOwnedAuthApp(t *testing.T) {
	for _, name := range []string{"no auth", "unavailable resolver", "binding rejected", "foreign account", "wrong auth app", "empty app identity"} {
		t.Run(name, func(t *testing.T) {
			f := newCustomerRecoveryFixture(t)
			if name == "no auth" {
				f.spec.AuthApp = ""
				if _, err := f.store.PutProductCatalog(t.Context(), f.account.ID, f.app.ID, f.spec); err != nil {
					t.Fatal(err)
				}
			} else if name == "unavailable resolver" {
				f.service.AuthOrigin = nil
			} else {
				f.service.AuthOrigin = func(context.Context, state.Account, string) (state.App, string, error) {
					auth := f.authApp
					switch name {
					case "binding rejected":
						return state.App{}, "", errors.New("PRIVATE_BINDING_DIAGNOSTIC")
					case "foreign account":
						auth.AccountID = "other-account"
					case "wrong auth app":
						auth.Slug = "different-auth"
					case "empty app identity":
						auth.ID = ""
					}
					return auth, "https://auth.example.test", nil
				}
			}
			w := f.request("")
			if w.Code < 400 || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), "PRIVATE_BINDING_DIAGNOSTIC") {
				t.Fatal("unavailable or foreign Auth app supplied a recovery target")
			}
		})
	}
}
