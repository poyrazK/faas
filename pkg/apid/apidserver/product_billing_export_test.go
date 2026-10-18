package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/paddleinvoice"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// productStatementFake serves finalized Product Control documents for a
// closed month, which a MemStore cannot backdate admissions into; the
// export itself, its connection and its export rows are the MemStore's.
type productStatementFake struct {
	MemStore *state.MemStore
	mu       sync.Mutex
	docs     map[string]api.ProductBillingDocument
	handoffs map[string]api.ProductInvoiceHandoff
}

func (f *productStatementFake) GetProductBillingDocument(_ context.Context, scope state.ProductBillingScope, id string) (api.ProductBillingDocument, error) {
	if _, err := uuid.Parse(id); err != nil {
		return api.ProductBillingDocument{}, state.ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	doc, ok := f.docs[id]
	if !ok || doc.TenantID != scope.TenantID || doc.Period != scope.Period {
		return api.ProductBillingDocument{}, state.ErrNotFound
	}
	return doc, nil
}

func (f *productStatementFake) GetProductBillingHandoff(ctx context.Context, scope state.ProductBillingScope, id string) (api.ProductInvoiceHandoff, error) {
	if _, err := f.GetProductBillingDocument(ctx, scope, id); err != nil {
		return api.ProductInvoiceHandoff{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	handoff, ok := f.handoffs[id]
	if !ok {
		return handoff, state.ErrNotFound
	}
	return handoff, nil
}

func (f *productStatementFake) HandoffProductBilling(ctx context.Context, scope state.ProductBillingScope, id, external string) (api.ProductInvoiceHandoff, error) {
	doc, err := f.GetProductBillingDocument(ctx, scope, id)
	if err != nil {
		return api.ProductInvoiceHandoff{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.handoffs[id]; ok {
		if existing.ExternalInvoiceID != external {
			return existing, state.ErrConflict
		}
		return existing, nil
	}
	handoff := api.ProductInvoiceHandoff{DocumentID: id, ExternalInvoiceID: external, Currency: doc.Currency, AmountMillicents: doc.AmountMillicents, CreatedAt: time.Now().UTC()}
	f.handoffs[id] = handoff
	return handoff, nil
}

func (f *productStatementFake) StatementHandoffBlocked(ctx context.Context, accountID, kind, id string) (bool, error) {
	if kind != state.StatementSourceProductControl {
		return f.MemStore.StatementHandoffBlocked(ctx, accountID, kind, id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if doc, ok := f.docs[id]; !ok || doc.Kind != "statement" {
		return false, state.ErrNotFound
	}
	return false, nil
}

// adr: 20261018212918 — a finalized Product Control statement exports to
// the owner's own provider: the tenant's external reference is the default
// customer, the provider invoice becomes the statement's handoff, a retry
// resumes it, and credit notes or statements handed off by hand never export.
func TestProductControlStatementExport(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	mem := state.NewMemStore()
	account, err := mem.CreateAccount(ctx, "export-http@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	owner, hash, _ := api.GenerateAPIKey()
	if _, err := mem.CreateAPIKey(ctx, account.ID, hash, "owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "export-http"})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}},
		Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	if _, err := mem.PutProductCatalog(ctx, account.ID, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	alice, _, err := mem.CreatePlatformTenant(ctx, account.ID, "ctm_01alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	bob, _, err := mem.CreatePlatformTenant(ctx, account.ID, "bob", "Bob", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []state.PlatformTenant{alice, bob} {
		if _, err := mem.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter"}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	period := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0).Format("2006-01")
	statement := func(tenant string) api.ProductBillingDocument {
		return api.ProductBillingDocument{ID: uuid.NewString(), Kind: "statement", TenantID: tenant, Period: period, Currency: "EUR", AmountMillicents: 50000,
			Snapshot: &api.ProductBillingReview{TenantID: tenant, Period: period, Currency: "EUR", Closed: true, Starts: 3, AmountMillicents: 50000,
				Lines: []api.ProductBillingLine{{Feature: "export", Plan: "starter", UnitPriceMillicents: 25000, Starts: 3, Succeeded: 2, Failed: 1, AmountMillicents: 50000}}}}
	}
	paid, manual, bobs := statement(alice.ID), statement(alice.ID), statement(bob.ID)
	credit := api.ProductBillingDocument{ID: uuid.NewString(), Kind: "credit_note", TenantID: alice.ID, Period: period, Currency: "EUR", StatementID: paid.ID, AmountMillicents: -5000}
	fake := &productStatementFake{MemStore: mem, docs: map[string]api.ProductBillingDocument{}, handoffs: map[string]api.ProductInvoiceHandoff{}}
	for _, doc := range []api.ProductBillingDocument{paid, manual, bobs, credit} {
		fake.docs[doc.ID] = doc
	}

	srv := apidserver.NewServer(statetest.NewMem(fake), slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{})
	paddle := &fakePaddle{status: map[string]string{}}
	paddleSrv := httptest.NewServer(paddle)
	defer paddleSrv.Close()
	srv.StatementInvoices().Paddle = &paddleinvoice.Client{BaseURL: paddleSrv.URL, HTTP: paddleSrv.Client()}
	handler := srv.Handler()
	call := func(method, path string, body any, status int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+owner)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		return w
	}
	invoice := func(doc api.ProductBillingDocument) string {
		return "/v1/apps/" + app.Slug + "/product/billing/documents/" + doc.ID + "/invoice?period=" + period
	}
	exportReq := api.ProductInvoiceExportRequest{TenantID: alice.ID, Provider: "paddle"}

	if w := call("POST", invoice(paid), exportReq, http.StatusNotFound); w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("export route served while Product Control is disabled")
	}
	srv.WithProductControlEnabled(true)
	call("POST", invoice(paid), exportReq, http.StatusConflict) // connect Paddle first
	call("PUT", "/v1/account/billing-connections/paddle", api.PutBillingConnectionRequest{SecretKey: "pdl_sdbx_apikey_01abcdWXYZ"}, http.StatusOK)

	var export api.StatementInvoiceExportResponse
	if err := json.Unmarshal(call("POST", invoice(paid), exportReq, http.StatusOK).Body.Bytes(), &export); err != nil {
		t.Fatal(err)
	}
	if export.InvoiceID != "txn_1" || export.Customer != "ctm_01alice" || export.SourceKind != state.StatementSourceProductControl ||
		export.AmountMinor != 50 || export.Currency != "EUR" {
		t.Fatalf("export = %+v", export)
	}
	txn := paddle.created[0]
	item := txn["items"].([]any)[0].(map[string]any)["price"].(map[string]any)
	if txn["customer_id"] != "ctm_01alice" || item["unit_price"].(map[string]any)["amount"] != "50" ||
		!strings.Contains(item["description"].(string), "2 successful export runs on plan starter at 0.25 EUR each") ||
		txn["custom_data"].(map[string]any)["gregale_source"] != state.StatementSourceProductControl {
		t.Fatalf("paddle transaction = %v", txn)
	}
	if fake.handoffs[paid.ID].ExternalInvoiceID != "txn_1" {
		t.Fatalf("handoff = %+v", fake.handoffs[paid.ID])
	}
	if replay := call("POST", invoice(paid), exportReq, http.StatusOK); !strings.Contains(replay.Body.String(), `"invoice_id":"txn_1"`) || len(paddle.created) != 1 {
		t.Fatalf("replay = %s, transactions = %d", replay.Body, len(paddle.created))
	}
	read := call("GET", invoice(paid)+"&tenant_id="+alice.ID, nil, http.StatusOK)
	if !strings.Contains(read.Body.String(), `"invoice_id":"txn_1"`) || !strings.Contains(read.Body.String(), `"payment_status":"draft"`) {
		t.Fatalf("export read = %s", read.Body)
	}
	call("GET", invoice(paid)+"&tenant_id="+bob.ID, nil, http.StatusNotFound)
	call("GET", invoice(manual)+"&tenant_id="+alice.ID, nil, http.StatusNotFound) // not exported

	call("POST", invoice(credit), exportReq, http.StatusUnprocessableEntity)
	call("POST", "/v1/apps/"+app.Slug+"/product/billing/documents/"+manual.ID+"/handoff?period="+period,
		api.ProductHandoffRequest{TenantID: alice.ID, ExternalInvoiceID: "internal-invoice-7"}, http.StatusOK)
	call("POST", invoice(manual), exportReq, http.StatusConflict)
	call("POST", invoice(paid), api.ProductInvoiceExportRequest{TenantID: bob.ID, Provider: "paddle"}, http.StatusNotFound)
	call("POST", invoice(paid), api.ProductInvoiceExportRequest{TenantID: alice.ID, Provider: "chargebee"}, http.StatusUnprocessableEntity)

	bobReq := api.ProductInvoiceExportRequest{TenantID: bob.ID, Provider: "paddle"}
	if w := call("POST", invoice(bobs), bobReq, http.StatusUnprocessableEntity); !strings.Contains(w.Body.String(), "Paddle customer") {
		t.Fatalf("export without a Paddle customer: %s", w.Body)
	}
	bobReq.Customer = "ctm_01bob"
	if w := call("POST", invoice(bobs), bobReq, http.StatusOK); !strings.Contains(w.Body.String(), `"customer":"ctm_01bob"`) || len(paddle.created) != 2 {
		t.Fatalf("export with an explicit customer: %s", w.Body)
	}
}
