package apidserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlBillingBoundary(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "billing-http@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	owner, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, account.ID, hash, "owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "billing-http"})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	if _, err := store.PutProductCatalog(ctx, account.ID, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	alice, _, err := store.CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	bob, _, err := store.CreatePlatformTenant(ctx, account.ID, "bob", "Bob", 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: alice.ID, Plan: "starter"}); err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{})
	handler := srv.Handler()
	call := func(method, path, key string, body any, status int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		return w
	}
	base := "/v1/apps/" + app.Slug + "/product/billing"
	docID := uuid.NewString()
	for _, route := range []struct{ method, path string }{{"GET", base + "/review"}, {"GET", base + "/receipts"}, {"GET", base + "/documents"}, {"POST", base + "/statements/finalize"}, {"POST", base + "/receipts/" + uuid.NewString() + "/waive"}, {"POST", base + "/credits"}, {"POST", base + "/documents/" + docID + "/handoff"}, {"GET", base + "/documents/" + docID + "/handoff"}, {"GET", "/v1/platform-tenant-self/product/billing?app=" + app.Slug}, {"GET", "/v1/product-control/customer/" + app.Slug + "/billing"}} {
		w := call(route.method, route.path, "", nil, http.StatusNotFound)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("billing route cacheable while disabled")
		}
	}
	srv.WithProductControlEnabled(true)
	call("GET", base+"/review", "", nil, http.StatusUnauthorized)
	now := time.Now().UTC()
	period := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0).Format("2006-01")
	query := "?tenant_id=" + alice.ID + "&period=" + period
	var review api.ProductBillingReview
	w := call("GET", base+"/review"+query, owner, nil, http.StatusOK)
	if err := json.Unmarshal(w.Body.Bytes(), &review); err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/statements/finalize", owner, api.ProductFinalizeRequest{TenantID: alice.ID, Period: period, ExpectedHash: strings.Repeat("0", 64)}, http.StatusConflict)
	w = call("POST", base+"/statements/finalize", owner, api.ProductFinalizeRequest{TenantID: alice.ID, Period: period, ExpectedHash: review.ReviewHash}, http.StatusOK)
	var doc api.ProductBillingDocument
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil || doc.Snapshot == nil || doc.Snapshot.Starts != 0 {
		t.Fatalf("finalized snapshot: %s %v", w.Body.String(), err)
	}
	call("POST", base+"/statements/finalize", owner, api.ProductFinalizeRequest{TenantID: alice.ID, Period: period, ExpectedHash: review.ReviewHash}, http.StatusOK)
	handoff := base + "/documents/" + doc.ID + "/handoff?period=" + period
	call("POST", handoff, owner, api.ProductHandoffRequest{TenantID: alice.ID, ExternalInvoiceID: "internal-invoice-1"}, http.StatusOK)
	call("POST", handoff, owner, api.ProductHandoffRequest{TenantID: alice.ID, ExternalInvoiceID: "internal-invoice-2"}, http.StatusConflict)
	call("GET", handoff+"&tenant_id="+alice.ID, owner, nil, http.StatusOK)
	call("GET", handoff+"&tenant_id="+bob.ID, owner, nil, http.StatusNotFound)
	call("POST", base+"/credits?period="+period, owner, api.ProductCreditRequest{ID: uuid.NewString(), TenantID: alice.ID, OperationID: uuid.NewString(), AmountMillicents: 0, Reason: "invalid"}, http.StatusBadRequest)
	var access api.CreatePlatformTenantAccessTokenResponse
	for _, tenant := range []state.PlatformTenant{alice, bob} {
		w := call("POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", owner, api.CreatePlatformTenantAccessTokenRequest{Name: "billing", Scopes: []string{api.ScopePlatformTenantOperationsRead}}, http.StatusCreated)
		if err := json.Unmarshal(w.Body.Bytes(), &access); err != nil {
			t.Fatal(err)
		}
		call("GET", base+"/review"+query, access.Token, nil, http.StatusForbidden)
		call("POST", base+"/statements/finalize", access.Token, api.ProductFinalizeRequest{TenantID: alice.ID, Period: period, ExpectedHash: review.ReviewHash}, http.StatusForbidden)
		status := http.StatusOK
		if tenant.ID == bob.ID {
			status = http.StatusNotFound
		}
		w = call("GET", "/v1/platform-tenant-self/product/billing?app="+app.Slug+"&period="+period+"&tenant_id="+bob.ID, access.Token, nil, status)
		if status == http.StatusOK {
			var billing api.ProductCustomerBilling
			if err := json.Unmarshal(w.Body.Bytes(), &billing); err != nil || billing.Review.TenantID != alice.ID || len(billing.Documents.Documents) != 1 || billing.Documents.Documents[0].ID != doc.ID {
				t.Fatalf("customer statement: %s %v", w.Body.String(), err)
			}
		}
	}
	srv.WithProductControlEnabled(false)
	call("GET", base+"/documents"+query, owner, nil, http.StatusNotFound)
	srv.WithProductControlEnabled(true)
	call("GET", base+"/documents"+query, owner, nil, http.StatusOK)
}
