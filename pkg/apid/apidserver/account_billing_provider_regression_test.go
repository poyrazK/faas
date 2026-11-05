package apidserver_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBillingPatchSyncUsesOnlyActiveProviderCustomer(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			for _, tc := range []struct {
				name   string
				legacy string
				active bool
			}{
				{"old provider cache", "cus_old_stripe", true},
				{"empty cache", "", true},
				{"no active binding", "cus_old_stripe", false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					acct, err := store.CreateAccount(t.Context(), uuid.NewString()+"@example.com", api.PlanPro)
					if err != nil {
						t.Fatal(err)
					}
					if tc.legacy != "" {
						if err := store.UpdateAccountProviderCustomerID(t.Context(), acct.ID, tc.legacy+"_"+acct.ID); err != nil {
							t.Fatal(err)
						}
					}
					customer := uuid.NewString()
					if tc.active {
						if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: acct.ID, Provider: "polar", CustomerID: customer}); err != nil {
							t.Fatal(err)
						}
					}
					var mu sync.Mutex
					var paths []string
					endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						paths = append(paths, r.URL.Path)
						mu.Unlock()
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(`{}`))
					}))
					defer endpoint.Close()
					provider, err := polar.NewProvider(polar.Config{APIKey: "offline-test-key", WebhookSecret: orgSubjectWebhookSecret, BaseURL: endpoint.URL}, nil)
					if err != nil {
						t.Fatal(err)
					}
					srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.com", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, nil, nil, 15*24, "")
					srv.WithBillingProvider(provider)
					rr := httptest.NewRecorder()
					srv.UpdateAccountBillingInfo(rr, httptest.NewRequest(http.MethodPatch, "/v1/account/billing", strings.NewReader(`{"business_name":"Updated company"}`)), acct)
					if rr.Code != http.StatusOK {
						t.Fatal(rr.Code, rr.Body.String())
					}
					mu.Lock()
					defer mu.Unlock()
					if tc.active && (len(paths) != 1 || paths[0] != "/v1/customers/"+customer) || !tc.active && len(paths) != 0 {
						t.Fatalf("wrong provider customer selected: %v", paths)
					}
				})
			}
		})
	}
}

func TestPGOrganizationCheckoutAndEmailChange(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanFree)
	store := *e.Store()
	org, err := store.(state.SharedOrgCreationStore).CreateSharedOrg(t.Context(), e.Acct().ID, "org-checkout", "Checkout")
	if err != nil {
		t.Fatal(err)
	}
	customer := uuid.NewString()
	var mu sync.Mutex
	providerEmail := ""
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/customers/external/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/customers":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": customer})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/checkouts":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "checkout-test", "url": "https://checkout.example/test"})
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/customers/"+customer:
			var body struct{ Email string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			mu.Lock()
			providerEmail = body.Email
			mu.Unlock()
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer endpoint.Close()
	provider, err := polar.NewProvider(polar.Config{APIKey: "offline-test-key", WebhookSecret: orgSubjectWebhookSecret, BaseURL: endpoint.URL, ProProductID: "product-pro"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	(*e.S()).WithBillingProvider(provider)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		e.AddAdminSession(t, req)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Active-Org", org.Slug)
		req.Header.Set("Idempotency-Key", uuid.NewString())
		rr := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rr, req)
		return rr
	}
	rr := request(http.MethodPatch, "/v1/orgs/"+org.Slug+"/plan", `{"plan":"pro"}`)
	var problem api.Problem
	if json.Unmarshal(rr.Body.Bytes(), &problem) != nil || rr.Code != http.StatusPaymentRequired || problem.CheckoutURL != "https://checkout.example/test" {
		t.Fatalf("organization checkout: %d %s", rr.Code, rr.Body.String())
	}
	identity, err := store.BillingIdentity(t.Context(), org.ID, "polar")
	if err != nil || identity.CustomerID != customer {
		t.Fatalf("qualified organization binding: %+v, %v", identity, err)
	}
	rr = request(http.MethodPut, "/v1/orgs/"+org.Slug+"/billing/email", `{"billing_email":"finance@example.com"}`)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Code, rr.Body.String())
	}
	subject, err := state.BillingResourceAccount(t.Context(), store, org.ID)
	mu.Lock()
	defer mu.Unlock()
	if err != nil || subject.Email != "finance@example.com" || providerEmail != subject.Email {
		t.Fatalf("organization email differs: local=%q provider=%q err=%v", subject.Email, providerEmail, err)
	}
}
