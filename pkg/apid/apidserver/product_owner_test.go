package apidserver_test

import (
	"encoding/base64"
	"encoding/json"
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
	"golang.org/x/net/html"
)

func ownerConsoleForm(t *testing.T, body, action string) url.Values {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var found url.Values
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			values := url.Values{}
			var inputs func(*html.Node)
			inputs = func(node *html.Node) {
				if node.Type == html.ElementNode && node.Data == "input" {
					name, value := "", ""
					for _, a := range node.Attr {
						if a.Key == "name" {
							name = a.Val
						}
						if a.Key == "value" {
							value = a.Val
						}
					}
					if name != "" {
						values.Set(name, value)
					}
				}
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					inputs(child)
				}
			}
			inputs(n)
			if values.Get("action") == action || (action == "confirm" && values.Get("payload") != "") {
				found = values
				return
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if found == nil {
		t.Fatalf("missing %s form: %s", action, body)
	}
	return found
}

func ownerConsoleCopy(values url.Values) url.Values {
	copy := url.Values{}
	for key, items := range values {
		copy[key] = append([]string(nil), items...)
	}
	return copy
}

func TestProductControlOwnerConsole(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "console-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "owner-console", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:console"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", DefinitionID: def.ID}, "future": {Label: "Future feature", Operation: &api.ProductOperationBinding{Name: "future", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}, "pro": {Label: "Pro", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 20, UnitPriceMillicents: 10000}}}}}
	catalog, err := store.PutProductCatalog(ctx, account.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "console-alice", "Alice <script>alert(1)</script>", 100)
	if err != nil {
		t.Fatal(err)
	}
	org := uuid.NewString()
	customer := api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", Revision: catalog.Revision, AuthOrganizationID: org}
	if _, err = store.SetProductCustomer(ctx, account.ID, app.ID, customer); err != nil {
		t.Fatal(err)
	}
	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := apidserver.MintDashboardSession(ctx, store, mgr, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, mgr, nil, 15*time.Minute, "")
	handler := srv.Handler()
	base := "/dashboard/apps/" + app.Slug + "/product-control"
	jar := map[string]*http.Cookie{apidserver.SessionCookie: {Name: apidserver.SessionCookie, Value: sid}}
	call := func(method, path string, form url.Values, auth bool, status int) *httptest.ResponseRecorder {
		t.Helper()
		body := ""
		if form != nil {
			body = form.Encode()
		}
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if auth {
			for _, cookie := range jar {
				r.AddCookie(cookie)
			}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		response := w.Result()
		defer func() { _ = response.Body.Close() }()
		for _, cookie := range response.Cookies() {
			jar[cookie.Name] = cookie
		}
		if strings.HasPrefix(path, base) && w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("owner response is cacheable")
		}
		return w
	}
	for _, route := range []struct{ method, path string }{{"GET", base}, {"GET", base + "/runs/" + uuid.NewString()}, {"POST", base + "/review"}, {"POST", base + "/confirm"}, {"GET", base + "/unknown"}, {"DELETE", base}} {
		call(route.method, route.path, nil, false, 404)
	}
	appPage := call("GET", "/dashboard/apps/"+app.Slug, nil, true, 200)
	if strings.Contains(appPage.Body.String(), "Manage Product Control") {
		t.Fatal("disabled console leaked navigation")
	}
	srv.WithProductControlEnabled(true)
	call("GET", base, nil, false, 302)
	key, keyHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, account.ID, keyHash, "console-owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	bearer := httptest.NewRequest("GET", base, nil)
	bearer.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, bearer)
	if w.Code != 302 {
		t.Fatal("bearer key authenticated the browser console")
	}
	customerReq := httptest.NewRequest("GET", base, nil)
	customerReq.AddCookie(&http.Cookie{Name: "gregale_pc_access", Value: "customer-session"})
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, customerReq)
	if w.Code != 302 {
		t.Fatal("customer cookie authenticated owner console")
	}
	path := base + "?tenant=" + tenant.ID
	page := call("GET", path, nil, true, 200)
	for _, want := range []string{"Needs attention", "Unavailable: inspect the Operation binding", "EUR 0.25000", org, "Alice &lt;script&gt;"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Fatalf("missing %q: %s", want, page.Body.String())
		}
	}
	if strings.Contains(page.Body.String(), "Alice <script>") {
		t.Fatal("customer name was not escaped")
	}
	plan := ownerConsoleForm(t, page.Body.String(), "plan")
	plan.Set("plan", "pro")
	plan.Set("suspended", "false")
	noCSRF := ownerConsoleCopy(plan)
	noCSRF.Del("csrf_token")
	call("POST", base+"/review", noCSRF, true, 403)
	preview := call("POST", base+"/review", plan, true, 200)
	for _, want := range []string{"EUR 0.25000", "EUR 0.10000", "Monthly starts already used do not reset", org} {
		if !strings.Contains(preview.Body.String(), want) {
			t.Fatalf("incomplete preview %q", want)
		}
	}
	confirm := ownerConsoleForm(t, preview.Body.String(), "confirm")
	raw, err := base64.RawURLEncoding.DecodeString(confirm.Get("payload"))
	if err != nil {
		t.Fatal(err)
	}
	tampered := ownerConsoleCopy(confirm)
	tampered.Set("payload", base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), `"plan":"pro"`, `"plan":"starter"`, 1))))
	call("POST", base+"/confirm", tampered, true, 403)
	result := call("POST", base+"/confirm", confirm, true, 200)
	if !strings.Contains(result.Body.String(), "Customer plan confirmed") {
		t.Fatal(result.Body.String())
	}
	assigned, err := store.GetProductCustomer(ctx, account.ID, app.ID, tenant.ID)
	if err != nil || assigned.Plan != "pro" || assigned.AuthOrganizationID != org {
		t.Fatalf("identity changed: %+v %v", assigned, err)
	}
	if _, err = store.SetProductCustomer(ctx, account.ID, app.ID, customer); err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/confirm", confirm, true, 200)
	assigned, err = store.GetProductCustomer(ctx, account.ID, app.ID, tenant.ID)
	if err != nil || assigned.Plan != "starter" {
		t.Fatal("old retry reapplied plan")
	}
	page = call("GET", path, nil, true, 200)
	plan = ownerConsoleForm(t, page.Body.String(), "plan")
	plan.Set("plan", "pro")
	plan.Set("suspended", "false")
	preview = call("POST", base+"/review", plan, true, 200)
	stale := ownerConsoleForm(t, preview.Body.String(), "confirm")
	changed := customer
	changed.Suspended = true
	if _, err = store.SetProductCustomer(ctx, account.ID, app.ID, changed); err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/confirm", stale, true, 409)
	if _, err = store.SetProductCustomer(ctx, account.ID, app.ID, customer); err != nil {
		t.Fatal(err)
	}
	// A pending run can be inspected and explicitly waived without creating
	// a replacement operation or claiming a verified execution outcome.
	op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: account.ID, PlatformTenantID: tenant.ID, DefinitionID: def.ID, IdempotencyKey: "console-run", ProductFeature: "export", ProductControlEnabled: true, Input: json.RawMessage(`{"private_input":"excluded-from-console"}`)})
	if err != nil {
		t.Fatal(err)
	}
	page = call("GET", path, nil, true, 200)
	waive := ownerConsoleForm(t, page.Body.String(), "waive")
	waive.Set("reason", "pilot write-off")
	preview = call("POST", base+"/review", waive, true, 200)
	waiver := ownerConsoleForm(t, preview.Body.String(), "confirm")
	call("POST", base+"/confirm", waiver, true, 200)
	call("POST", base+"/confirm", waiver, true, 200)
	runPage := call("GET", base+"/runs/"+op.ID, nil, true, 200)
	if !strings.Contains(runPage.Body.String(), "waived") || strings.Contains(runPage.Body.String(), "excluded-from-console") {
		t.Fatal("run explanation exposed input or lost charge evidence")
	}
	current := time.Now().UTC().Format("2006-01")
	form := url.Values{"action": {"finalize"}, "tenant": {tenant.ID}, "period": {current}, "csrf_token": {waive.Get("csrf_token")}}
	call("POST", base+"/review", form, true, 409)
	// Closed empty months exercise statement confirmation, retained retries,
	// external-reference review and recovery in the real owner cookie flow.
	closed := time.Date(time.Now().UTC().Year(), time.Now().UTC().Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0).Format("2006-01")
	page = call("GET", path+"&period="+closed, nil, true, 200)
	finalize := ownerConsoleForm(t, page.Body.String(), "finalize")
	preview = call("POST", base+"/review", finalize, true, 200)
	finalConfirmation := ownerConsoleForm(t, preview.Body.String(), "confirm")
	call("POST", base+"/confirm", finalConfirmation, true, 200)
	call("POST", base+"/confirm", finalConfirmation, true, 200)
	page = call("GET", path+"&period="+closed, nil, true, 200)
	handoff := ownerConsoleForm(t, page.Body.String(), "handoff")
	handoff.Set("reference", "external-invoice-001")
	preview = call("POST", base+"/review", handoff, true, 200)
	handoffConfirmation := ownerConsoleForm(t, preview.Body.String(), "confirm")
	call("POST", base+"/confirm", handoffConfirmation, true, 200)
	call("POST", base+"/confirm", handoffConfirmation, true, 200)
	page = call("GET", path+"&period="+closed, nil, true, 200)
	if !strings.Contains(page.Body.String(), "Invoice handoff recorded: <code>external-invoice-001") {
		t.Fatal("handoff recovery not visible")
	}
	call("POST", base+"/review", url.Values{"action": {"credit"}, "tenant": {tenant.ID}, "period": {current}, "operation": {op.ID}, "amount": {"1"}, "reason": {"credit attempt"}, "csrf_token": {handoff.Get("csrf_token")}}, true, 403) // old review cookie was replaced by GET
	call("GET", base+"?tenant="+uuid.NewString(), nil, true, 404)
	call("GET", base+"?tenant="+tenant.ID+"&tenant="+tenant.ID, nil, true, 400)
	// The same signed-in owner cannot inspect another account's app.
	foreign, err := store.CreateAccount(ctx, "console-foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreignSID, err := apidserver.MintDashboardSession(ctx, store, mgr, foreign.ID)
	if err != nil {
		t.Fatal(err)
	}
	jar[apidserver.SessionCookie] = &http.Cookie{Name: apidserver.SessionCookie, Value: foreignSID}
	call("GET", base, nil, true, 404)
	srv.WithProductControlEnabled(false)
	call("POST", base+"/confirm", handoffConfirmation, true, 404)
}

func TestProductControlOwnerSessionGuards(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	created, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "unverified-console@example.com", Plan: api.PlanPro, RequireEmailVerification: true})
	if err != nil {
		t.Fatal(err)
	}
	account := created.Account
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "owner-session-guards"})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "customer", "Customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	if _, err := store.PutProductCatalog(ctx, account.ID, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	if _, err := store.CreateSession(ctx, sessionID, account.ID, "192.0.2.10", "owner-console-test"); err != nil {
		t.Fatal(err)
	}
	value, err := mgr.IssueWithSessionAndBindingHash(sessionID, account.ID, "", false)
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, mgr, nil, 15*time.Minute, "").WithProductControlEnabled(true)
	handler := srv.Handler()
	base := "/dashboard/apps/" + app.Slug + "/product-control"
	call := func(method, path, body, cookie string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: cookie})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		return w
	}
	page := call("GET", base+"?tenant="+tenant.ID, "", value, 200)
	form := ownerConsoleForm(t, page.Body.String(), "plan")
	form.Set("suspended", "false")
	form.Set("plan", "starter")
	call("POST", base+"/review", form.Encode(), value, 403) // verified-email gate precedes domain writes
	if err := store.MarkAccountEmailVerified(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/review", form.Encode()+"&tenant="+tenant.ID, value, 400)
	call("POST", base+"/review", form.Encode()+"&reason="+strings.Repeat("x", api.ProductControlOwnerFormMaxBytes), value, 400)
	pendingID := uuid.NewString()
	if _, err := store.CreateSession(ctx, pendingID, account.ID, "192.0.2.10", "owner-console-test"); err != nil {
		t.Fatal(err)
	}
	pending, err := mgr.IssueWithSessionAndBindingHash(pendingID, account.ID, "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "POST"} {
		path := base
		if method == "POST" {
			path += "/review"
		}
		w := call(method, path, form.Encode(), pending, 302)
		if !strings.HasPrefix(w.Header().Get("Location"), "/dashboard/mfa") {
			t.Fatal("pending MFA reached owner console")
		}
	}
	if revoked, err := store.RevokeSession(ctx, sessionID, account.ID); err != nil || !revoked {
		t.Fatalf("revoke session: %v %v", revoked, err)
	}
	call("GET", base, "", value, 302)
}
