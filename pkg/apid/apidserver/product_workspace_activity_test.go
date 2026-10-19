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

func TestProductControlWorkspaceActivityHTTP(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx, store := t.Context(), state.NewMemStore()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	account, err := store.CreateAccount(ctx, "workspace-activity@example.test", api.PlanPro)
	check(err)
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "workspace-activity"})
	check(err)
	auth, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "workspace-activity-auth"})
	check(err)
	spec := api.ProductCatalogSpec{AuthApp: auth.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10}}}, "pro": {Label: "Pro", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 20}}}}, Access: &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"viewer": {api.ProductPermissionViewWork}, "manager": {api.ProductPermissionManagePlan}}}, SelfService: &api.ProductPlanPolicy{Transitions: map[string][]string{"starter": {"pro"}}}}
	_, err = store.PutProductCatalog(ctx, account.ID, app.ID, spec)
	check(err)
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "customer", "Customer", 100)
	check(err)
	org := uuid.NewString()
	_, err = store.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: org})
	check(err)
	scope := state.ProductPlanScope{AccountID: account.ID, AppID: app.ID, TenantID: tenant.ID, AuthApp: auth.Slug, AuthAppID: auth.ID, OrganizationID: org, Role: "manager"}
	help, err := store.CreateProductHelp(ctx, state.ProductHelpScope{ProductPlanScope: scope}, api.ProductHelpCreate{ID: uuid.NewString(), Kind: "workspace", Message: "PRIVATE_QUESTION"})
	check(err)
	_, err = store.UpdateProductHelp(ctx, account.ID, app.ID, help.Request.ID, api.ProductHelpUpdate{ExpectedVersion: 1, Status: "resolved", Response: "PRIVATE_RESPONSE"})
	check(err)
	preview, err := store.PreviewProductPlanChange(ctx, scope, api.ProductPlanSelection{ID: uuid.NewString(), Plan: "pro"})
	check(err)
	_, err = store.ApplyProductPlanChange(ctx, scope, api.ProductPlanChangeConfirmation{Request: preview.Request, ExpectedHash: preview.Hash})
	check(err)
	var role atomic.Value
	role.Store("viewer")
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
	base := "/v1/product-control/customer/" + app.Slug + "/activity"
	asset := "/v1/product-control/portal-activity.js"
	call := func(method, path, organization, credential string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "https://portal.example"+path, nil)
		r.Header.Set("X-Product-Organization", organization)
		r.Header.Set("X-Product-Role", "manager")
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
	call("GET", base, org, "owner.credentials.are.not.customer.credentials", 401)
	call("GET", base, uuid.NewString(), "customer", 403)
	read := func(filter string, want int) {
		t.Helper()
		w := call("GET", base+"?filter="+filter, org, "customer", 200)
		var p api.ProductWorkspaceActivityPage
		check(json.Unmarshal(w.Body.Bytes(), &p))
		if p.AccountID != account.ID || p.AppID != app.ID || p.TenantID != tenant.ID || len(p.Events) != want {
			t.Fatal("scoped activity", p)
		}
		for _, private := range []string{"PRIVATE_", "expected_hash", "auth_organization_id", "actor_role", "/dashboard/", "amount_millicents"} {
			if strings.Contains(w.Body.String(), private) {
				t.Fatal("unsafe projection", private)
			}
		}
	}
	read("all", 1)
	read("plans", 0)
	role.Store("manager")
	read("all", 2)
	read("plans", 1)
	for _, q := range []string{"?tenant=" + tenant.ID, "?role=manager", "?filter=all&filter=plans", "?filter=unsupported", "?cursor=bad"} {
		call("GET", base+q, org, "customer", 400)
	}
	role.Store("unknown")
	call("GET", base, org, "customer", 403)
	role.Store("viewer")
	revoked.Store(true)
	call("GET", base, org, "customer", 401)
	srv.WithProductControlEnabled(false)
	call("GET", base, org, "customer", 404)
}
