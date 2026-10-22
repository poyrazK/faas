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

// adr: 20261018212918 — over HTTP, an owner's overdue policy holds a customer
// whose exported statement is overdue: the portal explains the hold, a new
// start is a 402 that consumes no start, and clearing the policy lets the same
// never-admitted work start.
func TestProductControlOverdueHold(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "product-overdue@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "product-overdue", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:overdue"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}}); err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	handler := srv.Handler()
	do := func(method, path, bearer string, body any, idempotency string) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+bearer)
		if idempotency != "" {
			r.Header.Set("Idempotency-Key", idempotency)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	check := func(w *httptest.ResponseRecorder, status int) *httptest.ResponseRecorder {
		t.Helper()
		if w.Code != status {
			t.Fatalf("got %d want %d: %s", w.Code, status, w.Body.String())
		}
		return w
	}
	policyPath := "/v1/apps/" + app.Slug + "/product/billing/overdue-policy"
	holdsPath := "/v1/apps/" + app.Slug + "/product/billing/overdue-holds"
	check(do("GET", policyPath, key, nil, ""), http.StatusNotFound)
	srv.WithProductControlEnabled(true)
	srv.Operation().AdmissionEnabled = true
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: dep.Scope}}},
		Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	check(do("PUT", "/v1/apps/"+app.Slug+"/product", key, spec, ""), http.StatusOK)
	alice, _, err := store.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	var access api.CreatePlatformTenantAccessTokenResponse
	if err := json.Unmarshal(check(do("POST", "/v1/account/platform-tenants/"+alice.ID+"/access-tokens", key, api.CreatePlatformTenantAccessTokenRequest{Name: "portal",
		Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}}, ""), http.StatusCreated).Body.Bytes(), &access); err != nil {
		t.Fatal(err)
	}
	aliceKey := access.Token
	check(do("PUT", "/v1/apps/"+app.Slug+"/product/customers/"+alice.ID, key, map[string]any{"plan": "starter"}, ""), http.StatusOK)

	doc := api.ProductBillingDocument{ID: uuid.NewString(), Kind: "statement", TenantID: alice.ID, Period: "2026-08", Currency: "EUR", AmountMillicents: 25000}
	store.SeedProductBillingDocumentForTest(acct.ID, app.ID, doc)
	export, _, err := store.BeginStatementInvoiceExport(ctx, state.StatementInvoiceExport{AccountID: acct.ID, SourceKind: state.StatementSourceProductControl,
		StatementID: doc.ID, Provider: state.BillingProviderStripe, Customer: "cus_alice", Currency: "EUR", AmountMinor: 25})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatementInvoiceExportInvoice(ctx, acct.ID, export.ID, "in_alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteStatementInvoiceExport(ctx, acct.ID, export.ID); err != nil {
		t.Fatal(err)
	}
	due := time.Now().UTC().AddDate(0, 0, -20).Truncate(time.Second)
	hosted := "https://invoice.stripe.com/i/acct_1/in_alice"
	if _, err := store.RecordStatementInvoicePayment(ctx, export.ID, state.StatementInvoicePayment{Status: state.InvoicePaymentOpen, AmountRemainingMinor: 25,
		DueAt: &due, HostedInvoiceURL: hosted}, time.Now().UTC(), nil); err != nil {
		t.Fatal(err)
	}

	check(do("PUT", policyPath, aliceKey, map[string]int{"block_overdue_after_days": 14}, ""), http.StatusForbidden)
	check(do("PUT", policyPath, key, map[string]int{"block_overdue_after_days": 400}, ""), http.StatusUnprocessableEntity)
	check(do("PUT", policyPath, key, map[string]int{"block_overdue_after_days": 14}, ""), http.StatusOK)
	if err := srv.ProductControl().SyncOverdueOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var holds api.ProductOverdueHolds
	if err := json.Unmarshal(check(do("GET", holdsPath, key, nil, ""), http.StatusOK).Body.Bytes(), &holds); err != nil || len(holds.Holds) != 1 ||
		holds.Holds[0].TenantID != alice.ID || holds.Holds[0].HostedInvoiceURL != hosted {
		t.Fatalf("holds = %+v, %v", holds, err)
	}
	portal := func() api.ProductPortal {
		t.Helper()
		var view api.ProductPortal
		if err := json.Unmarshal(check(do("GET", "/v1/platform-tenant-self/product?app="+app.Slug, aliceKey, nil, ""), http.StatusOK).Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		return view
	}
	view := portal()
	if view.OverdueHold == nil || !view.OverdueHold.DueAt.Equal(due) || view.OverdueHold.HostedInvoiceURL != hosted || view.Customer.Suspended {
		t.Fatalf("portal hold = %+v", view.OverdueHold)
	}
	if view.Guidance == nil || !view.Guidance.InvoiceOverdue || len(view.Guidance.Features) != 1 || view.Guidance.Features[0].State != "invoice_overdue" {
		t.Fatalf("portal guidance = %+v", view.Guidance)
	}
	start := api.ProductStartRequest{App: app.Slug, Feature: "export", Input: json.RawMessage(`{}`), ExpectedQuote: view.Quotes["export"]}
	refused := check(do("POST", "/v1/platform-tenant-self/product/start", aliceKey, start, "held-work"), http.StatusPaymentRequired)
	var problem api.Problem
	if err := json.Unmarshal(refused.Body.Bytes(), &problem); err != nil || problem.Code != api.CodeProductInvoiceOverdue ||
		!strings.Contains(problem.Detail, due.Format(time.DateOnly)) || !strings.Contains(problem.Detail, hosted) {
		t.Fatalf("refusal = %+v, %v", problem, err)
	}
	if view := portal(); view.Usage == nil || len(view.Usage.Lines) != 0 {
		t.Fatalf("refused start was consumed: %+v", view.Usage)
	}

	check(do("DELETE", policyPath, key, nil, ""), http.StatusNoContent)
	if view := portal(); view.OverdueHold != nil || view.Guidance == nil || view.Guidance.InvoiceOverdue {
		t.Fatalf("hold survived clearing the policy: %+v", view.OverdueHold)
	}
	check(do("POST", "/v1/platform-tenant-self/product/start", aliceKey, start, "held-work"), http.StatusAccepted)
}
