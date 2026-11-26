package apidserver_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type billingBatchLostResponseStore struct {
	MemStore *state.MemStore
	loseFor  string
}

func (s *billingBatchLostResponseStore) FinalizeProductBilling(ctx context.Context, scope state.ProductBillingScope, hash string) (api.ProductBillingDocument, error) {
	doc, err := s.MemStore.FinalizeProductBilling(ctx, scope, hash)
	if err == nil && scope.TenantID == s.loseFor {
		s.loseFor = ""
		return api.ProductBillingDocument{}, errors.New("statement response lost after commit")
	}
	return doc, err
}

func TestProductControlOwnerBillingBatch(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	fake := &billingBatchLostResponseStore{MemStore: state.NewMemStore()}
	store := statetest.NewMem(fake)
	acct, err := store.CreateAccount(ctx, "billing-batch@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountEmailVerified(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}

	spec := api.ProductCatalogSpec{Currency: "EUR",
		Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}},
		Plans: map[string]api.ProductPlan{
			"monthly": {Label: "Monthly", MonthlyFeeMillicents: 900_000, Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10}}},
			"metered": {Label: "Metered", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25_000}}},
		},
	}
	newApp := func(slug string) (state.App, api.ProductCatalog) {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp})
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := store.PutProductCatalog(ctx, acct.ID, app.ID, spec)
		if err != nil {
			t.Fatal(err)
		}
		return app, catalog
	}
	app, catalog := newApp("billing-batch")
	otherApp, otherCatalog := newApp("billing-batch-other")
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	period := month.Format("2006-01")

	workspace := func(app state.App, catalog api.ProductCatalog, name, plan string) string {
		t.Helper()
		tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, name, name, 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SetProductCustomer(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: plan}); err != nil {
			t.Fatal(err)
		}
		store.SeedProductPlanHistoryForTest(acct.ID, app.ID, tenant.ID, catalog.Revision, plan, true, month.Add(-time.Hour))
		return tenant.ID
	}
	feeOnly := workspace(app, catalog, "Fee only", "monthly")
	usage := workspace(app, catalog, "Usage billed", "metered")
	changed := workspace(app, catalog, "Changed total", "metered")
	unresolved := workspace(app, catalog, "Unresolved", "metered")
	unselected := workspace(app, catalog, "Unselected", "metered")
	outside := workspace(otherApp, otherCatalog, "Other app", "metered")
	seedRun := func(app state.App, catalog api.ProductCatalog, tenant string, status api.OperationState) {
		store.SeedProductOperationForTest(state.Operation{
			AccountID: acct.ID, AppID: app.ID, PlatformTenantID: tenant,
			OperationResponse: api.OperationResponse{ID: uuid.NewString(), State: status, UpdatedAt: month.Add(2 * time.Hour)},
			ProductControl: &api.ProductOperationReceipt{
				Revision: catalog.Revision, Feature: "export", Plan: "metered", Currency: "EUR",
				Period: period, UnitPriceMillicents: 25_000, AdmittedAt: month.Add(time.Hour),
			},
		})
	}
	for _, id := range []string{usage, changed, unselected} {
		seedRun(app, catalog, id, api.OperationSucceeded)
	}
	seedRun(app, catalog, unresolved, api.OperationAccepted)
	seedRun(otherApp, otherCatalog, outside, api.OperationSucceeded)

	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := apidserver.MintDashboardSession(ctx, store, mgr, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}, "",
		apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, mgr, nil, 15*time.Minute, "").WithProductControlEnabled(true)
	handler := srv.Handler()
	jar := map[string]*http.Cookie{apidserver.SessionCookie: {Name: apidserver.SessionCookie, Value: sid}}
	const origin = "https://gregale.dev"
	call := func(path string, form url.Values, status int) string {
		t.Helper()
		method := http.MethodGet
		if form != nil {
			method = http.MethodPost
		}
		r := httptest.NewRequest(method, origin+path, strings.NewReader(form.Encode()))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, cookie := range jar {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		result := w.Result()
		defer func() { _ = result.Body.Close() }()
		for _, cookie := range result.Cookies() {
			jar[cookie.Name] = cookie
		}
		return w.Body.String()
	}
	assertText := func(body string, values ...string) {
		t.Helper()
		for _, value := range values {
			if !strings.Contains(body, value) {
				t.Fatalf("missing %q in batch response", value)
			}
		}
	}
	base := "/dashboard/apps/" + app.Slug + "/product-control"
	reviewPath, confirmPath := base+"/billing-batch/review", base+"/billing-batch/confirm"
	monthPath := base + "?billing_month=" + period
	body := call(monthPath, nil, http.StatusOK)
	assertText(body, "Review selected statements", `name="tenant" value="`+feeOnly, `name="tenant" value="`+usage)
	if strings.Contains(body, `name="tenant" value="`+unresolved) || strings.Contains(body, outside) {
		t.Fatal("unresolved or unrelated workspace is selectable")
	}
	review := ownerConsoleForm(t, body, reviewPath)
	duplicate := ownerConsoleCopy(review)
	duplicate["tenant"] = []string{feeOnly, feeOnly}
	call(reviewPath, duplicate, http.StatusBadRequest)
	review["tenant"] = []string{feeOnly, usage, changed, unresolved, outside}
	body = call(reviewPath, review, http.StatusOK)
	assertText(body, "Finalize 3 statements", "EUR 9.00000", "EUR 0.25000", "1 unresolved runs", "No statement to finalize")
	confirm := ownerConsoleForm(t, body, confirmPath)
	changedPayload := ownerConsoleCopy(confirm)
	changedPayload.Set("payload", confirm.Get("payload")+"A")
	call(confirmPath, changedPayload, http.StatusForbidden)
	call("/dashboard/apps/"+otherApp.Slug+"/product-control/billing-batch/confirm", confirm, http.StatusForbidden)

	docs := func(app state.App, tenant string) []api.ProductBillingDocument {
		t.Helper()
		page, err := store.ListProductBillingDocuments(ctx, state.ProductBillingScope{AccountID: acct.ID, AppID: app.ID, TenantID: tenant, Period: period}, "")
		if err != nil {
			t.Fatal(err)
		}
		return page.Documents
	}
	if len(docs(app, feeOnly)) != 0 || len(docs(app, usage)) != 0 {
		t.Fatal("review or rejected confirmation finalized a statement")
	}
	seedRun(app, catalog, changed, api.OperationSucceeded)
	fake.loseFor = feeOnly
	body = call(confirmPath, confirm, http.StatusOK)
	assertText(body, "Statement finalization results", "Billing changed. Review again.", "Could not confirm the result.", "Retry batch confirmation")
	feeDocs, usageDocs := docs(app, feeOnly), docs(app, usage)
	if len(feeDocs) != 1 || feeDocs[0].AmountMillicents != 900_000 || len(usageDocs) != 1 || usageDocs[0].AmountMillicents != 25_000 {
		t.Fatalf("incorrect statements after confirmation: fee %+v, usage %+v", feeDocs, usageDocs)
	}
	retry := ownerConsoleForm(t, body, confirmPath)
	body = call(confirmPath, retry, http.StatusOK)
	assertText(body, "#document-"+feeDocs[0].ID, "#document-"+usageDocs[0].ID, "Billing changed. Review again.")
	if fee := docs(app, feeOnly); len(fee) != 1 || fee[0].ID != feeDocs[0].ID {
		t.Fatalf("retry changed the fee-only statement: %+v", fee)
	}
	if usage := docs(app, usage); len(usage) != 1 || usage[0].ID != usageDocs[0].ID {
		t.Fatalf("retry changed the usage statement: %+v", usage)
	}
	for _, tenant := range []string{changed, unresolved, unselected, outside} {
		if documents := docs(app, tenant); len(documents) != 0 {
			t.Fatalf("unexpected statement for %s: %+v", tenant, documents)
		}
	}
	if len(docs(otherApp, outside)) != 0 {
		t.Fatal("batch finalized another app's statement")
	}
	body = call(monthPath, nil, http.StatusOK)
	assertText(body, "Awaiting invoice", "#document-"+feeDocs[0].ID, "#document-"+usageDocs[0].ID, "Review outcomes")
	if strings.Contains(body, `name="tenant" value="`+feeOnly) || strings.Contains(body, `name="tenant" value="`+usage) {
		t.Fatal("finalized workspaces are still selectable")
	}
	review = ownerConsoleForm(t, body, reviewPath)
	review["tenant"] = []string{changed}
	body = call(reviewPath, review, http.StatusOK)
	assertText(body, "Finalize 1 statement", "EUR 0.50000")
	call(confirmPath, ownerConsoleForm(t, body, confirmPath), http.StatusOK)
	if documents := docs(app, changed); len(documents) != 1 || documents[0].AmountMillicents != 50_000 {
		t.Fatalf("changed workspace did not finalize at the newly reviewed amount: %+v", documents)
	}
}
