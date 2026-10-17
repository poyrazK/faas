// ADR-1605: Customer Help reuses live workspace authority and separate owner review.
package apidserver_test

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlCustomerHelpHTTP(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx, store := t.Context(), state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "help@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkAccountEmailVerified(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "help-product"})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "help-auth"})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{AuthApp: auth.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}, Access: &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"operator": {api.ProductPermissionRunWork}, "viewer": {api.ProductPermissionViewWork}, "billing": {api.ProductPermissionViewBilling}, "team": {api.ProductPermissionManageTeam}}}}
	if _, err = store.PutProductCatalog(ctx, acct.ID, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "help-customer", "Customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := store.CreatePlatformTenant(ctx, acct.ID, "help-other", "Other", 100)
	if err != nil {
		t.Fatal(err)
	}
	org, otherOrg := uuid.NewString(), uuid.NewString()
	for _, c := range []api.ProductCustomer{{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: org}, {TenantID: other.ID, Plan: "starter", AuthOrganizationID: otherOrg}} {
		if _, err = store.SetProductCustomer(ctx, acct.ID, app.ID, c); err != nil {
			t.Fatal(err)
		}
	}
	owner, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(ctx, acct.ID, hash, "owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	var role atomic.Value
	role.Store("operator")
	var revoked atomic.Bool
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if revoked.Load() || r.URL.Path != "/authz/v1/orgs" || r.Header.Get("Authorization") != "Bearer live.access.token" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"organizations": []map[string]string{{"id": org, "name": "Customer", "role": role.Load().(string)}, {"id": otherOrg, "name": "Other", "role": role.Load().(string)}}})
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
	srv.ProductControl().AuthClient = upstream.Client()
	srv.ProductControl().AuthOrigin = func(_ context.Context, a state.Account, slug string) (state.App, string, error) {
		if a.ID != acct.ID || slug != auth.Slug {
			return state.App{}, "", state.ErrNotFound
		}
		return auth, upstream.URL, nil
	}
	handler := srv.Handler()
	base, admin := "/v1/product-control/customer/"+app.Slug+"/help", "/v1/apps/"+app.Slug+"/product/help"
	call := func(method, path string, body any, credential, organization, origin string, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, e := json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest(method, "https://portal.example"+path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Product-Organization", organization)
		r.Header.Set("X-Product-Role", "owner")
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
		return w
	}
	const origin = "https://portal.example"
	request := api.ProductHelpCreate{ID: uuid.NewString(), Kind: "workspace", Message: "<script>Customer question</script>"}
	for _, denied := range []string{"", "https://foreign.example"} {
		call("POST", base, request, "customer", org, denied, 403)
	}
	for _, credential := range []string{"", owner} {
		call("POST", base, request, credential, org, origin, 401)
	}
	w := call("POST", base, request, "customer", org, origin, 200)
	var created api.ProductHelpRequest
	if err = json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.Request.ID != request.ID || created.Context.Usage != nil || created.Status != "open" || created.Version != 1 {
		t.Fatal("invalid help creation", err)
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("cacheable customer request")
	}
	for _, field := range []string{"unit_price_millicents", "amount_millicents", "auth_organization_id", "input_schema", "definition_id", "auth_app_id"} {
		if strings.Contains(w.Body.String(), field) {
			t.Fatalf("help context leaked %s", field)
		}
	}
	call("POST", base, request, "customer", org, origin, 200)
	changed := request
	changed.Message += " changed"
	call("POST", base, changed, "customer", org, origin, 409)
	for _, query := range []string{"?tenant_id=" + other.ID, "?role=owner", "?period=2026-10"} {
		call("GET", base+query, nil, "customer", org, origin, 400)
	}
	call("POST", base, map[string]any{"id": uuid.NewString(), "kind": "workspace", "message": "Question", "context": map[string]any{"reason": "forged"}}, "customer", org, origin, 400)
	call("GET", base+"/"+request.ID, nil, "customer", otherOrg, origin, 404)
	call("GET", admin, nil, "customer", org, origin, 401)
	call("GET", admin+"/"+request.ID, nil, owner, org, origin, 200)
	update := api.ProductHelpUpdate{ExpectedVersion: 1, Status: "in_progress", Response: "We are reviewing <img src=x> this request."}
	call("PUT", admin+"/"+request.ID, update, "customer", org, origin, 401)
	call("PUT", admin+"/"+request.ID, update, owner, org, origin, 200)
	call("PUT", admin+"/"+request.ID, update, owner, org, origin, 200)
	stale := update
	stale.Response = "An outdated response"
	call("PUT", admin+"/"+request.ID, stale, owner, org, origin, 409)
	feature := api.ProductHelpCreate{ID: uuid.NewString(), Kind: "feature", Feature: "export", Message: "Feature is unavailable"}
	w = call("POST", base, feature, "customer", org, origin, 200)
	var featureHelp api.ProductHelpRequest
	if err = json.Unmarshal(w.Body.Bytes(), &featureHelp); err != nil || featureHelp.Context.Reason != "temporarily_unavailable" {
		t.Fatal("missing binding context", err)
	}
	role.Store("viewer")
	call("GET", base+"/"+feature.ID, nil, "customer", org, origin, 404)
	call("POST", base, api.ProductHelpCreate{ID: uuid.NewString(), Kind: "feature", Feature: "export", Message: "Denied"}, "customer", org, origin, 403)
	w = call("GET", base, nil, "customer", org, origin, 200)
	var page api.ProductHelpPage
	if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Requests) != 1 || page.Requests[0].Version != 2 || page.Requests[0].Response != update.Response {
		t.Fatal("current role/status projection", err)
	}
	// The owner browser gets escaped text and an authenticated, request-bound
	// CSRF form. Updating help cannot mutate customer configuration or work.
	console := "/dashboard/apps/" + app.Slug + "/product-control/help"
	jar := map[string]*http.Cookie{apidserver.SessionCookie: {Name: apidserver.SessionCookie, Value: sid}}
	browser := func(method, path string, form url.Values, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, origin+path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		for _, cookie := range jar {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("owner help %s %s: %d want %d", method, path, w.Code, want)
		}
		response := w.Result()
		for _, cookie := range response.Cookies() {
			jar[cookie.Name] = cookie
		}
		_ = response.Body.Close()
		return w
	}
	browser("GET", console, nil, 200)
	w = browser("GET", console+"/"+request.ID, nil, 200)
	if strings.Contains(w.Body.String(), "<script>Customer question") || !strings.Contains(w.Body.String(), "&lt;script&gt;Customer question") || !strings.Contains(w.Body.String(), "Check current readiness") {
		t.Fatal("owner support message escaping/links failed")
	}
	token := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if len(token) != 2 {
		t.Fatal("help CSRF form missing")
	}
	browser("POST", console+"/"+request.ID, url.Values{"version": {"2"}, "status": {"resolved"}, "response": {"Answered"}}, 403)
	browser("POST", console+"/"+request.ID, url.Values{"csrf_token": {html.UnescapeString(token[1])}, "version": {"2"}, "status": {"resolved"}, "response": {"Answered"}}, 303)
	w = call("GET", base+"/"+request.ID, nil, "customer", org, origin, 200)
	if err = json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.Status != "resolved" || created.Version != 3 {
		t.Fatal("customer did not receive owner response", err)
	}
	// Notification authority follows the same live customer boundary, while
	// acknowledgement only changes read metadata shared by permitted members.
	notifications := "/v1/product-control/customer/" + app.Slug + "/notifications"
	for _, credential := range []string{"", owner} {
		call("GET", notifications, nil, credential, org, origin, 401)
	}
	for _, readOrigin := range []string{"", "null", "https://foreign.example"} {
		call("GET", notifications, nil, "customer", org, readOrigin, 200)
	}
	for _, query := range []string{"?filter=private", "?filter=all&filter=unread", "?tenant_id=" + other.ID, "?cursor=invalid", "?role=owner"} {
		call("GET", notifications+query, nil, "customer", org, origin, 400)
	}
	readPage := func(organization string) api.ProductNotificationPage {
		t.Helper()
		response := call("GET", notifications, nil, "customer", organization, origin, 200)
		if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("cacheable notification page")
		}
		var value api.ProductNotificationPage
		if e := json.Unmarshal(response.Body.Bytes(), &value); e != nil {
			t.Fatal(e)
		}
		for _, private := range []string{"Answered", "Customer question", "auth_organization_id", "unit_price_millicents", "response", "context"} {
			if strings.Contains(response.Body.String(), private) {
				t.Fatal("notification leaked", private)
			}
		}
		return value
	}
	np := readPage(org)
	if len(np.Items) != 1 || np.Items[0].Kind != "help_resolved" || np.UnreadCount != 1 || np.TenantID != tenant.ID || np.ReadStateScope != "workspace" {
		t.Fatalf("notification projection: %+v", np)
	}
	version, isRead := int64(0), true
	ack := api.ProductNotificationUpdate{ID: np.Items[0].ID, Revision: np.Items[0].Revision, ExpectedVersion: &version, Read: &isRead}
	call("POST", notifications+"/read", ack, "customer", otherOrg, origin, 404)
	if page := readPage(otherOrg); len(page.Items) != 0 || page.UnreadCount != 0 {
		t.Fatal("cross-workspace notification")
	}
	for _, body := range []any{map[string]any{"id": ack.ID, "revision": ack.Revision, "read": true}, map[string]any{"id": ack.ID, "revision": ack.Revision, "expected_version": 0}, map[string]any{"id": ack.ID, "revision": ack.Revision, "expected_version": -1, "read": true}, map[string]any{"id": ack.ID, "revision": ack.Revision, "expected_version": 0, "read": true, "tenant_id": other.ID}} {
		call("POST", notifications+"/read", body, "customer", org, origin, 400)
	}
	call("POST", notifications+"/read?filter=all", ack, "customer", org, origin, 400)
	call("POST", notifications+"/read", ack, owner, org, origin, 401)
	call("POST", notifications+"/read", ack, "customer", org, "", 403)
	call("POST", notifications+"/read", ack, "customer", org, "null", 403)
	call("POST", notifications+"/read", ack, "customer", org, "https://foreign.example", 403)
	call("POST", notifications+"/read", ack, "customer", org, origin, 200)
	call("POST", notifications+"/read", ack, "customer", org, origin, 200)
	role.Store("team")
	np = readPage(org)
	if len(np.Items) != 1 || !np.Items[0].Read || np.Items[0].ReadVersion != 1 || np.UnreadCount != 0 {
		t.Fatal("read acknowledgement was not shared")
	}
	call("PUT", admin+"/"+request.ID, api.ProductHelpUpdate{ExpectedVersion: 3, Status: "in_progress", Response: "A newer response"}, owner, org, origin, 200)
	np = readPage(org)
	if len(np.Items) != 1 || np.Items[0].Read || np.Items[0].Revision == ack.Revision || np.UnreadCount != 1 {
		t.Fatal("newer reply remained read")
	}
	call("POST", notifications+"/read", ack, "customer", org, origin, 409)
	call("PUT", admin+"/"+feature.ID, api.ProductHelpUpdate{ExpectedVersion: 1, Status: "resolved", Response: "Feature answer"}, owner, org, origin, 200)
	if page := readPage(org); len(page.Items) != 1 {
		t.Fatal("team role saw feature update")
	}
	role.Store("operator")
	np = readPage(org)
	if len(np.Items) != 2 || np.UnreadCount != 2 {
		t.Fatal("current feature permissions were not applied")
	}
	role.Store("viewer")
	for _, item := range np.Items {
		if item.HelpKind == "feature" {
			call("POST", notifications+"/read", api.ProductNotificationUpdate{ID: item.ID, Revision: item.Revision, ExpectedVersion: &version, Read: &isRead}, "customer", org, origin, 404)
		}
	}
	role.Store("unknown")
	call("GET", notifications, nil, "customer", org, origin, 403)
	call("POST", notifications+"/read", ack, "customer", org, origin, 403)
	role.Store("viewer")
	w = call("GET", "/v1/product-control/portal-notifications.js", nil, "", "", origin, 200)
	if !strings.Contains(w.Body.String(), "GregaleProductNotifications") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("embedded notification asset or privacy headers missing")
	}
	revoked.Store(true)
	call("GET", base, nil, "customer", org, origin, 401)
	call("GET", notifications, nil, "customer", org, origin, 401)
	call("POST", notifications+"/read", ack, "customer", org, origin, 401)
	revoked.Store(false)
	srv.WithProductControlEnabled(false)
	for _, method := range []string{"GET", "POST", "OPTIONS"} {
		for _, path := range []string{base, base + "/" + request.ID, notifications, notifications + "/read", admin, console, "/v1/product-control/portal-help.js", "/v1/product-control/portal-notifications.js"} {
			w = call(method, path, nil, owner, org, origin, 404)
			if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
				t.Fatal("disabled help surface was cacheable")
			}
		}
	}
}
