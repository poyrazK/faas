package apidserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/paddleinvoice"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// adr: 20261020120000 — with Paddle collection send, a customer's billing
// details entered through Gregale land on the Paddle customer, and exports
// are billed with that address and business; before an address exists the
// export fails and keeps the draft, which a retry then bills (ADR-20261019100333).
// Details are refused for a customer that Paddle does not bill.
func TestPaddleExportsBilledFromCustomerBillingDetails(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	withBillingKeys(t)
	ctx := t.Context()
	mem := state.NewMemStore()
	account, err := mem.CreateAccount(ctx, "billing-details@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	owner, hash, _ := api.GenerateAPIKey()
	if _, err := mem.CreateAPIKey(ctx, account.ID, hash, "owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "billing-details"})
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
	if _, err := mem.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: alice.ID, Plan: "starter"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	period := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0).Format("2006-01")
	statement := func() api.ProductBillingDocument {
		return api.ProductBillingDocument{ID: uuid.NewString(), Kind: "statement", TenantID: alice.ID, Period: period, Currency: "EUR", AmountMillicents: 50000,
			Snapshot: &api.ProductBillingReview{TenantID: alice.ID, Period: period, Currency: "EUR", Closed: true, Starts: 2, AmountMillicents: 50000,
				Lines: []api.ProductBillingLine{{Feature: "export", Plan: "starter", UnitPriceMillicents: 25000, Starts: 2, Succeeded: 2, AmountMillicents: 50000}}}}
	}
	first, second := statement(), statement()
	fake := &productStatementFake{MemStore: mem, docs: map[string]api.ProductBillingDocument{first.ID: first, second.ID: second}, handoffs: map[string]api.ProductInvoiceHandoff{}}

	srv := apidserver.NewServer(statetest.NewMem(fake), slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{})
	paddle := &fakePaddle{status: map[string]string{}}
	paddleSrv := httptest.NewServer(paddle)
	defer paddleSrv.Close()
	srv.StatementInvoices().Paddle = &paddleinvoice.Client{BaseURL: paddleSrv.URL, HTTP: paddleSrv.Client()}
	srv.WithProductControlEnabled(true)
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
	details := srv.StatementInvoices()
	request := api.PutProductBillingDetailsRequest{Address: api.ProductBillingAddress{CountryCode: "US", PostalCode: "94107", Region: "CA", City: "San Francisco",
		FirstLine: "1 Market St"}, Business: &api.ProductBillingBusiness{Name: "Alice Inc", TaxIdentifier: "US123"}}
	if _, _, problem := details.PutPaddleBillingDetails(ctx, account, "ctm_01alice", request); problem == nil || problem.Code != api.CodeProductBillingDetailsUnavailable {
		t.Fatalf("billing details before Paddle is connected: %+v", problem)
	}

	call("PUT", "/v1/account/billing-connections/paddle", api.PutBillingConnectionRequest{SecretKey: "pdl_sdbx_apikey_01abcdWXYZ", Collection: state.CollectionSend, DaysUntilDue: 14}, http.StatusOK)
	invoice := func(doc api.ProductBillingDocument) string {
		return "/v1/apps/" + app.Slug + "/product/billing/documents/" + doc.ID + "/invoice?period=" + period
	}
	exportReq := api.ProductInvoiceExportRequest{TenantID: alice.ID, Provider: state.BillingProviderPaddle}
	// ADR-20261019100333: without an address the export stops and keeps the draft.
	if w := call("POST", invoice(first), exportReq, http.StatusUnprocessableEntity); !bytes.Contains(w.Body.Bytes(), []byte("no address")) ||
		len(paddle.created) != 1 || len(paddle.billed) != 0 {
		t.Fatalf("export without an address: %s, created %d, billed %v", w.Body, len(paddle.created), paddle.billed)
	}

	if _, _, problem := details.PutPaddleBillingDetails(ctx, account, "cus_stripe", request); problem == nil || problem.Code != api.CodeProductBillingDetailsUnavailable {
		t.Fatalf("billing details for a customer Paddle does not bill: %+v", problem)
	}
	bad := request
	bad.Address.CountryCode = "usa"
	if _, _, problem := details.PutPaddleBillingDetails(ctx, account, "ctm_01alice", bad); problem == nil || problem.Status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid country accepted: %+v", problem)
	}
	saved, changed, problem := details.PutPaddleBillingDetails(ctx, account, "ctm_01alice", request)
	if problem != nil || len(changed) != 2 || saved.Provider != "paddle" || saved.Address == nil || saved.Address.PostalCode != "94107" ||
		saved.Business == nil || saved.Business.TaxIdentifier != "US123" {
		t.Fatalf("saved = %+v %v %+v", saved, changed, problem)
	}
	writes := paddle.writes
	if _, changed, problem := details.PutPaddleBillingDetails(ctx, account, "ctm_01alice", request); problem != nil || len(changed) != 0 || paddle.writes != writes {
		t.Fatalf("unchanged details were written again: %v %+v", changed, problem)
	}
	moved := request
	moved.Address.City, moved.Business = "Oakland", nil
	if read, changed, problem := details.PutPaddleBillingDetails(ctx, account, "ctm_01alice", moved); problem != nil || len(changed) != 1 || changed[0] != "address" ||
		read.Address.City != "Oakland" || read.Business == nil || len(paddle.entities["ctm_01alice/addresses"]) != 1 {
		t.Fatalf("moved = %+v %v %+v %v", read, changed, problem, paddle.entities)
	}

	call("POST", invoice(first), exportReq, http.StatusOK)
	if len(paddle.created) != 1 || paddle.billed["txn_1"] != "add_1" || paddle.billedBusiness["txn_1"] != "biz_2" {
		t.Fatalf("retried export billed %v / %v after %d transactions", paddle.billed, paddle.billedBusiness, len(paddle.created))
	}
	call("POST", invoice(second), exportReq, http.StatusOK)
	terms := paddle.created[1]["billing_details"].(map[string]any)["payment_terms"].(map[string]any)
	if paddle.billed["txn_2"] != "add_1" || paddle.billedBusiness["txn_2"] != "biz_2" || terms["frequency"] != float64(14) {
		t.Fatalf("second export = %v / %v, terms %v", paddle.billed, paddle.billedBusiness, terms)
	}
}
