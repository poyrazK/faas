// ADR-1604: guidance is a live, customer-scoped, read-only allowance projection.
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
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/productcontrol"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlCustomerGuidanceHTTP(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "guidance@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkAccountEmailVerified(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "guidance-product"})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "guidance-auth"})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:guidance"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{AuthApp: auth.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: dep.Scope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}, "pro": {Label: "Pro", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 100, UnitPriceMillicents: 10000}}}}, Access: &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"runner": {api.ProductPermissionRunWork}, "viewer": {api.ProductPermissionViewWork}, "billing": {api.ProductPermissionViewBilling}, "manager": {api.ProductPermissionRunWork, api.ProductPermissionManagePlan}}}, SelfService: &api.ProductPlanPolicy{Transitions: map[string][]string{"starter": {"pro"}}}}
	initial, err := store.PutProductCatalog(ctx, acct.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "guidance-customer", "Customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := store.CreatePlatformTenant(ctx, acct.ID, "guidance-other", "Other", 100)
	if err != nil {
		t.Fatal(err)
	}
	org, otherOrg := uuid.NewString(), uuid.NewString()
	for _, c := range []api.ProductCustomer{{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: org}, {TenantID: other.ID, Plan: "starter", AuthOrganizationID: otherOrg}} {
		if _, err = store.SetProductCustomer(ctx, acct.ID, app.ID, c); err != nil {
			t.Fatal(err)
		}
	}
	admit := func() {
		t.Helper()
		if _, _, e := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: acct.ID, PlatformTenantID: tenant.ID, DefinitionID: def.ID, IdempotencyKey: uuid.NewString(), Input: json.RawMessage(`{}`), ProductFeature: "export", ProductControlEnabled: true}); e != nil {
			t.Fatal(e)
		}
	}
	for range 8 {
		admit()
	}
	owner, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(ctx, acct.ID, hash, "owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	var role atomic.Value
	role.Store("runner")
	var revoked atomic.Bool
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if revoked.Load() || r.URL.Path != "/authz/v1/orgs" || r.Header.Get("Authorization") != "Bearer live.access.token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"organizations": []map[string]string{{"id": org, "name": "Customer", "role": role.Load().(string)}, {"id": otherOrg, "name": "Other", "role": role.Load().(string)}}})
	}))
	defer upstream.Close()
	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, mgr, nil, 15*time.Minute, "").WithProductControlEnabled(true)
	srv.ProductControl().AuthClient = upstream.Client()
	srv.ProductControl().AuthOrigin = func(_ context.Context, a state.Account, slug string) (state.App, string, error) {
		if a.ID != acct.ID || slug != auth.Slug {
			return state.App{}, "", state.ErrNotFound
		}
		return auth, upstream.URL, nil
	}
	handler := srv.Handler()
	base := "/v1/product-control/customer/" + app.Slug
	call := func(method, path, organization, credential string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "https://portal.example"+path, nil)
		r.Header.Set("Origin", "https://portal.example")
		r.Header.Set("X-Product-Organization", organization)
		r.Header.Set("X-Product-Role", "manager") // Caller claims never grant access.
		if credential == "customer" {
			r.AddCookie(productAccessCookie(auth.ID))
		} else if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("cacheable guidance surface: %s", path)
		}
		return w
	}
	read := func(organization string) api.ProductUsageGuidance {
		t.Helper()
		w := call("GET", base+"/guidance", organization, "customer", 200)
		for _, private := range []string{"unit_price_millicents", "amount_millicents", "definition_id", "input_schema", "operations", "auth_organization_id"} {
			if strings.Contains(w.Body.String(), private) {
				t.Fatalf("guidance leaked %s", private)
			}
		}
		var g api.ProductUsageGuidance
		if e := json.Unmarshal(w.Body.Bytes(), &g); e != nil {
			t.Fatal(e)
		}
		return g
	}
	g := read(org)
	if g.TenantID != tenant.ID || g.Revision != initial.Revision || len(g.Features) != 1 || g.Features[0].State != "near_limit" || g.Features[0].Starts != 8 || g.Features[0].Remaining != 2 || g.PlanAction != "ask_workspace_admin" {
		t.Fatalf("runner projection: %+v", g)
	}
	if g.Period != productcontrol.Period(time.Now()) || g.ResetsAt != time.Date(g.ObservedAt.Year(), g.ObservedAt.Month()+1, 1, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("reset boundary: %+v", g)
	}
	if otherG := read(otherOrg); otherG.TenantID != other.ID || otherG.Features[0].Starts != 0 {
		t.Fatalf("cross-workspace usage: %+v", otherG)
	}
	for _, pair := range []struct{ role, state string }{{"billing", "permission_required"}, {"viewer", ""}, {"manager", "near_limit"}} {
		role.Store(pair.role)
		g = read(org)
		if pair.role == "viewer" {
			if len(g.Features) != 0 {
				t.Fatal("viewer received counts")
			}
		} else if g.Features[0].State != pair.state || (pair.role == "manager" && g.PlanAction != "review_plans") {
			t.Fatalf("%s projection: %+v", pair.role, g)
		}
	}
	role.Store("runner")
	for _, query := range []string{"?tenant_id=" + other.ID, "?period=2026-01", "?role=manager", "?cursor=x", "?period=x&period=y"} {
		call("GET", base+"/guidance"+query, org, "customer", 400)
	}
	call("GET", base+"/guidance", uuid.NewString(), "customer", 403)
	for _, credential := range []string{"", owner} {
		call("GET", base+"/guidance", org, credential, 401)
	}
	// Accepted starts survive a new commercial revision and remain observable
	// without executing the underlying work or creating any new admission.
	spec.Plans["starter"].Features["export"] = api.ProductFeatureGrant{MonthlyStarts: 10, UnitPriceMillicents: 50000}
	head, err := store.PutProductCatalog(ctx, acct.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	customer, err := store.SetProductCustomer(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: org})
	if err != nil {
		t.Fatal(err)
	}
	admit()
	g = read(org)
	if g.Revision != head.Revision || g.PolicyRevision != head.Revision || g.Features[0].Starts != 9 || g.Features[0].Remaining != 1 {
		t.Fatalf("revision reset usage: %+v", g)
	}
	admit()
	if g = read(org); g.Features[0].State != "allowance_exhausted" || g.Features[0].Starts != 10 {
		t.Fatalf("full allowance: %+v", g)
	}
	customer.Suspended = true
	if _, err = store.SetProductCustomer(ctx, acct.ID, app.ID, customer); err != nil {
		t.Fatal(err)
	}
	if g = read(org); g.Features[0].State != "access_paused" || g.PlanAction != "contact_product_team" {
		t.Fatalf("paused allowance: %+v", g)
	}
	revoked.Store(true)
	call("GET", base+"/guidance", org, "customer", 401)
	revoked.Store(false)
	role.Store("unrecognized")
	call("GET", base+"/guidance", org, "customer", 403)
	call("GET", "/v1/product-control/portal-guidance.js", "", "", 200)
	srv.WithProductControlEnabled(false)
	for _, method := range []string{"GET", "POST", "OPTIONS"} {
		for _, path := range []string{base + "/guidance", "/v1/product-control/portal-guidance.js", "/v1/platform-tenant-self/product"} {
			call(method, path, org, "customer", 404)
		}
	}
}
