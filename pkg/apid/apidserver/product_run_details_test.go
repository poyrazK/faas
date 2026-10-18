package apidserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlRunDetailsHTTP(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx, store := t.Context(), state.NewMemStore()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	account, err := store.CreateAccount(ctx, "run-details@example.test", api.PlanPro)
	check(err)
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "run-details"})
	check(err)
	auth, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "run-details-auth"})
	check(err)
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:run-details"})
	check(err)
	check(store.MarkDeploymentLive(ctx, dep.ID))
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}})
	check(err)
	spec := api.ProductCatalogSpec{AuthApp: auth.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", DefinitionID: def.ID}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}, Access: &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"all": {api.ProductPermissionViewWork, api.ProductPermissionViewBilling}, "viewer": {api.ProductPermissionViewWork}, "billing": {api.ProductPermissionViewBilling}, "manager": {api.ProductPermissionManagePlan}}}}
	_, err = store.PutProductCatalog(ctx, account.ID, app.ID, spec)
	check(err)
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "customer", "Customer", 100)
	check(err)
	org := uuid.NewString()
	_, err = store.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: org})
	check(err)
	op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: account.ID, PlatformTenantID: tenant.ID, DefinitionID: def.ID, IdempotencyKey: uuid.NewString(), ProductFeature: "export", ProductControlEnabled: true, Input: json.RawMessage(`{}`)})
	check(err)
	var role atomic.Value
	role.Store("all")
	var revoked atomic.Bool
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if revoked.Load() || r.URL.Path != "/authz/v1/orgs" || r.Header.Get("Authorization") != "Bearer live.access.token" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"organizations": []map[string]string{{"id": org, "name": "Customer", "role": role.Load().(string)}}})
	}))
	defer upstream.Close()
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{})
	srv.ProductControl().AuthClient = upstream.Client()
	srv.ProductControl().AuthOrigin = func(context.Context, state.Account, string) (state.App, string, error) {
		return auth, upstream.URL, nil
	}
	handler := srv.Handler()
	base := "/v1/product-control/customer/" + app.Slug + "/operations/" + op.ID + "/details"
	asset := "/v1/product-control/portal-run.js"
	call := func(method, path, organization, credential string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "https://portal.example"+path, nil)
		r.Header.Set("X-Product-Organization", organization)
		r.Header.Set("X-Product-Role", "all")
		if credential == "customer" {
			r.AddCookie(productAccessCookie(auth.ID))
		} else if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	for _, path := range []string{base, asset} {
		for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			call(method, path, org, "customer", 404)
		}
	}
	srv.WithProductControlEnabled(true)
	call("GET", asset, "", "", 200)
	call("POST", base, org, "customer", 405)
	call("GET", base, org, "", 401)
	call("GET", base, org, "owner-bearer-is-not-customer", 401)
	call("GET", base, uuid.NewString(), "customer", 403)
	w := call("GET", base, org, "customer", 200)
	var d api.ProductRunDetails
	check(json.Unmarshal(w.Body.Bytes(), &d))
	if d.OperationID != op.ID || d.Outcome.State != "accepted" || d.Allowance.StartsAfter != 1 || d.Charge.State != "pending" || d.Charge.AmountMillicents != nil || d.Charge.UnitPriceMillicents != 25000 {
		t.Fatal(d)
	}
	for _, forbidden := range []string{"definition_id", "deployment_id", "admission_evidence", "confirmed_quote", "executions", "input\"", "waiver_reason", "review_hash"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatal("private fields leaked", forbidden)
		}
	}
	role.Store("viewer")
	w = call("GET", base, org, "customer", 200)
	if strings.Contains(w.Body.String(), "millicents") || strings.Contains(w.Body.String(), "charge") {
		t.Fatal("viewer received billing", w.Body.String())
	}
	for _, name := range []string{"billing", "manager", "unknown"} {
		role.Store(name)
		call("GET", base, org, "customer", 403)
	}
	role.Store("all")
	for _, q := range []string{"?tenant=" + tenant.ID, "?role=all", "?period=2026-09", "?after=0", "?x=1&x=2"} {
		call("GET", base+q, org, "customer", 400)
	}
	call("GET", strings.Replace(base, op.ID, uuid.NewString(), 1), org, "customer", 404)
	call("GET", strings.Replace(base, op.ID, "bad", 1), org, "customer", 400)
	revoked.Store(true)
	call("GET", base, org, "customer", 401)
	srv.WithProductControlEnabled(false)
	call("GET", base, org, "customer", 404)
}
