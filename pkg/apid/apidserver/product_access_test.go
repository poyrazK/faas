// ADR-1708: customer permissions use live Auth roles and the latest product policy.
package apidserver_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestProductControlCustomerPermissions(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "product-permissions@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkAccountEmailVerified(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "permissions-product"})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "permissions-auth"})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:permissions"})
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
	spec := api.ProductCatalogSpec{AuthApp: auth.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: def.Spec.Name, Scope: dep.Scope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	initial, err := store.PutProductCatalog(ctx, acct.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "permissions-customer", "Permissions customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	org := uuid.NewString()
	if _, err = store.SetProductCustomer(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: org}); err != nil {
		t.Fatal(err)
	}
	owner, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(ctx, acct.ID, hash, "permissions-owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	var role, mode atomic.Value
	role.Store("member")
	mode.Store("normal")
	var authReads atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/authz/v1/orgs" {
			t.Errorf("unexpected Auth call %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		authReads.Add(1)
		if r.Header.Get("Authorization") != "Bearer live.access.token" {
			w.WriteHeader(401)
			return
		}
		rows := []map[string]string{{"id": org, "name": "Customer workspace", "role": role.Load().(string)}}
		switch mode.Load().(string) {
		case "revoked":
			w.WriteHeader(401)
			return
		case "outage":
			w.WriteHeader(503)
			return
		case "removed":
			rows = []map[string]string{}
		case "missing role":
			delete(rows[0], "role")
		case "duplicate":
			rows = append(rows, map[string]string{"id": org, "name": "Duplicate", "role": "owner"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"organizations": rows})
	}))
	defer upstream.Close()
	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := apidserver.MintDashboardSession(ctx, store, mgr, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, mgr, nil, 15*time.Minute, "").WithProductControlEnabled(true)
	srv.Operation().AdmissionEnabled = true
	srv.ProductControl().AuthClient = upstream.Client()
	srv.ProductControl().AuthOrigin = func(_ context.Context, account state.Account, slug string) (state.App, string, error) {
		if account.ID != acct.ID || slug != auth.Slug {
			return state.App{}, "", state.ErrNotFound
		}
		return auth, upstream.URL, nil
	}
	handler := srv.Handler()
	base := "/v1/product-control/customer/" + app.Slug
	admin := "/v1/apps/" + app.Slug + "/product/access"
	call := func(method, path string, body any, credential string, status int) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		r := httptest.NewRequest(method, "https://portal.example"+path, strings.NewReader(string(raw)))
		r.Header.Set("Origin", "https://portal.example")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Product-Organization", org)
		r.Header.Set("Idempotency-Key", "permissions-request")
		// Caller headers never override the role returned by live Auth.
		r.Header.Set("X-Product-Role", "owner")
		if credential == "customer" {
			// The cookie name is bound to the owned Auth-app identity.
			r.AddCookie(productAccessCookie(auth.ID))
		} else {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s role=%s mode=%s: %d want %d: %s", method, path, role.Load(), mode.Load(), w.Code, status, w.Body.String())
		}
		return w
	}
	view := func() api.ProductPortal {
		t.Helper()
		w := call("GET", base+"/product", nil, "customer", 200)
		var v api.ProductPortal
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	v := view()
	if v.Access == nil || v.Access.Role != "member" || !v.Access.Permissions.ViewWork || v.Access.Permissions.RunWork || v.Usage != nil || len(v.Quotes) != 0 || len(v.Bindings) != 0 {
		t.Fatalf("default member exposure: %+v", v)
	}
	for _, route := range []struct{ method, path string }{{"POST", "/start"}, {"POST", "/operations/" + uuid.NewString() + "/cancel"}, {"GET", "/billing"}} {
		call(route.method, base+route.path, map[string]any{}, "customer", 403)
	}
	policy := productcontrol.DefaultAccess()
	policy.Roles["operator"] = []api.ProductPermission{api.ProductPermissionRunWork}
	policy.Roles["billing"] = []api.ProductPermission{api.ProductPermissionViewBilling}
	w := call("POST", admin+"/preview", policy, owner, 200)
	var preview api.ProductAccessChangePreview
	if err = json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ method, path string }{{"GET", admin}, {"POST", admin + "/preview"}, {"PUT", admin}} {
		call(route.method, route.path, nil, "", 401)
		call(route.method, route.path, nil, "customer", 401)
	}
	badPolicy := api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"member": {"*"}}}
	call("POST", admin+"/preview", badPolicy, owner, 400)
	badChange := preview.Change
	badChange.ExpectedRevision = strings.Repeat("0", 64)
	call("PUT", admin, badChange, owner, 400)
	for _, origin := range []string{"", "https://foreign.example", "https://portal.example"} {
		raw, marshalErr := json.Marshal(policy)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		r := httptest.NewRequest("POST", "https://portal.example"+admin+"/preview", strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: sid})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		want := 403
		if origin == "https://portal.example" {
			want = 200
		}
		if response.Code != want {
			t.Fatalf("owner cookie API origin %q: %d want %d: %s", origin, response.Code, want, response.Body.String())
		}
	}
	tokenResponse := call("POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", api.CreatePlatformTenantAccessTokenRequest{Name: "product-service", Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}}, owner, 201)
	var serviceToken api.CreatePlatformTenantAccessTokenResponse
	if err = json.Unmarshal(tokenResponse.Body.Bytes(), &serviceToken); err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ method, path string }{{"GET", admin}, {"POST", admin + "/preview"}, {"PUT", admin}} {
		call(route.method, route.path, nil, serviceToken.Token, 403)
	}
	current, err := store.GetProductCatalog(ctx, acct.ID, app.ID, "")
	if err != nil || current.Revision != initial.Revision {
		t.Fatal("access preview mutated catalog", err)
	}
	call("PUT", admin, preview.Change, owner, 200)
	call("PUT", admin, preview.Change, owner, 200)
	role.Store("operator")
	v = view()
	if v.Access.PolicyRevision != preview.After.Revision || v.Customer.Revision != initial.Revision || !v.Access.Permissions.RunWork || !v.Access.Permissions.ViewWork || v.Usage != nil || v.Allowances["export"] != 10 || len(v.Quotes["export"]) != 64 || v.Catalog.Spec.Access != nil {
		t.Fatalf("operator view/pinned terms: %+v", v)
	}
	accepted := call("POST", base+"/start", api.ProductStartRequest{Feature: "export", Input: json.RawMessage(`{}`), ExpectedQuote: v.Quotes["export"]}, "customer", 202)
	var receipt api.OperationAcceptedResponse
	if err = json.Unmarshal(accepted.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	call("GET", base+"/operations/"+receipt.ID, nil, "customer", 200)
	call("POST", base+"/operations/"+receipt.ID+"/cancel", api.OperationCancellationRequest{ExpectedGeneration: 1}, "customer", 200)
	call("GET", base+"/billing", nil, "customer", 403)
	role.Store("member")
	v = view()
	if len(v.Operations) != 1 || v.Usage != nil {
		t.Fatalf("viewer work/billing projection: %+v", v)
	}
	call("GET", base+"/operations/"+receipt.ID, nil, "customer", 200)
	call("GET", base+"/operations/"+receipt.ID+"/events", nil, "customer", 200)
	call("POST", base+"/submissions/lookup", map[string]string{"feature": "export", "idempotency_key": "permissions-request"}, "customer", 200)
	call("POST", base+"/operations/"+receipt.ID+"/cancel", api.OperationCancellationRequest{ExpectedGeneration: 1}, "customer", 403)
	role.Store("billing")
	v = view()
	if v.Usage == nil || v.Access.Permissions.ViewWork || len(v.Operations) != 0 || len(v.Catalog.Spec.Features) != 0 || len(v.Bindings) != 0 || len(v.Quotes) != 0 {
		t.Fatalf("billing-only work exposure: %+v", v)
	}
	call("GET", base+"/billing?tenant_id="+uuid.NewString(), nil, "customer", 200)
	for _, suffix := range []string{"", "/events", "/artifacts/" + uuid.NewString()} {
		call("GET", base+"/operations/"+receipt.ID+suffix, nil, "customer", 403)
	}
	call("POST", base+"/submissions/lookup", map[string]string{"feature": "export", "idempotency_key": "permissions-request"}, "customer", 403)
	call("POST", base+"/start", api.ProductStartRequest{Feature: "export", Input: json.RawMessage(`{}`)}, "customer", 403)
	role.Store("unknown")
	workspaces := call("GET", base+"/workspaces", nil, "customer", 200)
	if strings.Contains(workspaces.Body.String(), tenant.ID) {
		t.Fatal("ungranted role discovered customer workspace")
	}
	call("GET", base+"/product", nil, "customer", 403)
	role.Store("operator")
	for _, tc := range []struct {
		mode   string
		status int
	}{{"revoked", 401}, {"outage", 503}, {"removed", 403}, {"missing role", 503}, {"duplicate", 503}} {
		mode.Store(tc.mode)
		call("GET", base+"/product", nil, "customer", tc.status)
	}
	mode.Store("normal")
	// Latest policy revocation applies even while the customer pins old prices.
	current, err = store.GetProductCatalog(ctx, acct.ID, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	current.Spec.Access.Roles["operator"] = []api.ProductPermission{api.ProductPermissionViewWork}
	if _, err = store.PutProductCatalog(ctx, acct.ID, app.ID, current.Spec); err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/start", api.ProductStartRequest{Feature: "export", Input: json.RawMessage(`{}`)}, "customer", 403)
	call("PUT", admin, preview.Change, owner, 409)
	v = view()
	if v.Access.Permissions.RunWork || v.Customer.Revision != initial.Revision {
		t.Fatal("old retry restored permission or changed customer terms")
	}
	usage, err := store.GetProductUsage(ctx, acct.ID, app.ID, tenant.ID, productcontrol.Period(time.Now()))
	if err != nil || len(usage.Lines) != 1 || usage.Lines[0].Starts != 1 {
		t.Fatalf("denied actions consumed quota: %+v %v", usage, err)
	}
	if authReads.Load() < 15 {
		t.Fatal("customer authorization was cached")
	}

	// Owner dashboard edits require both Origin and signed review/confirmation.
	console := "/dashboard/apps/" + app.Slug + "/product-control/access"
	jar := map[string]*http.Cookie{apidserver.SessionCookie: {Name: apidserver.SessionCookie, Value: sid}}
	browser := func(method, path string, form url.Values, origin string, status int) *httptest.ResponseRecorder {
		t.Helper()
		body := ""
		if form != nil {
			body = form.Encode()
		}
		r := httptest.NewRequest(method, "https://portal.example"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		for _, cookie := range jar {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("owner %s: %d want %d: %s", path, w.Code, status, w.Body.String())
		}
		for _, cookie := range w.Result().Cookies() {
			jar[cookie.Name] = cookie
		}
		return w
	}
	page := browser("GET", console, nil, "", 200)
	if !strings.Contains(page.Body.String(), "Customer permissions") || !strings.Contains(page.Body.String(), "billing") {
		t.Fatal("owner permission editor missing roles")
	}
	form := onboardingForm(t, page.Body.String(), "/access/review")
	form.Set("role", "member")
	form.Set("action", "set")
	form.Set("view_work", "true")
	form.Set("run_work", "true")
	form.Del("view_billing")
	form.Del("manage_plan")
	form.Del("manage_team")
	bad := ownerConsoleCopy(form)
	bad.Del("csrf_token")
	browser("POST", console+"/review", bad, "https://portal.example", 403)
	browser("POST", console+"/review", form, "https://foreign.example", 403)
	reviewPage := browser("POST", console+"/review", form, "https://portal.example", 200)
	confirm := onboardingForm(t, reviewPage.Body.String(), "/access/confirm")
	tampered := ownerConsoleCopy(confirm)
	tampered.Set("payload", confirm.Get("payload")+"A")
	browser("POST", console+"/confirm", tampered, "https://portal.example", 400)
	browser("POST", console+"/confirm", confirm, "https://portal.example", 200)
	browser("POST", console+"/confirm", confirm, "https://portal.example", 200)
	role.Store("member")
	if !view().Access.Permissions.RunWork {
		t.Fatal("reviewed owner permission not applied")
	}
	srv.WithProductControlEnabled(false)
	call("GET", admin, nil, owner, 404)
	call("POST", admin+"/preview", policy, owner, 404)
	call("PUT", admin, preview.Change, owner, 404)
	browser("GET", console, nil, "", 404)
	call("GET", base+"/product", nil, "customer", 404)
}

func productAccessCookie(authAppID string) *http.Cookie {
	digest := sha256.Sum256([]byte(authAppID))
	return &http.Cookie{Name: fmt.Sprintf("__Secure-gregale-pc-%x-access", digest[:8]), Value: "live.access.token"}
}
