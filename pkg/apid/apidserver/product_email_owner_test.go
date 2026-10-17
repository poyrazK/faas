package apidserver_test

import (
	"context"
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
	"github.com/onebox-faas/faas/pkg/productemail"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
	"golang.org/x/net/html"
)

func TestProductControlOwnerEmailConsoleAndScopes(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx, store := t.Context(), state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "email-owner@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkAccountEmailVerified(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "owner-email"})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "owner-email-auth"})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "email-customer", "Email customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	org, actor := uuid.NewString(), uuid.NewString()
	_, err = store.PutProductCatalog(ctx, acct.ID, app.ID, api.ProductCatalogSpec{AuthApp: auth.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10}}}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.SetProductCustomer(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: org})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	k := productemail.Key{AccountID: acct.ID, AppID: app.ID, TenantID: tenant.ID, AuthAppID: auth.ID, OrganizationID: org, ActorID: actor}
	repo := store.ProductEmailRepository()
	sub, err := repo.Put(ctx, productemail.Subscription{Key: k, AppSlug: app.Slug, AuthSlug: auth.Slug, Help: true, Grant: []byte("encrypted-fixture"), ExpiresAt: now.Add(time.Hour)}, 0, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	scope := state.ProductHelpScope{ProductPlanScope: state.ProductPlanScope{AccountID: acct.ID, AppID: app.ID, TenantID: tenant.ID, AuthAppID: auth.ID, AuthApp: auth.Slug, OrganizationID: org, Role: "owner"}}
	h, err := store.CreateProductHelp(ctx, scope, api.ProductHelpCreate{ID: uuid.NewString(), Kind: "workspace", Message: "PRIVATE_EMAIL_QUESTION"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.UpdateProductHelp(ctx, acct.ID, app.ID, h.Request.ID, api.ProductHelpUpdate{ExpectedVersion: 1, Status: "resolved", Response: "PRIVATE_EMAIL_RESPONSE"})
	if err != nil {
		t.Fatal(err)
	}
	n, err := store.GetProductNotification(ctx, state.ProductNotificationScope(scope), "help/"+h.Request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Enqueue(ctx, sub, []api.ProductNotification{n}, now); err != nil {
		t.Fatal(err)
	}
	jobs, err := repo.Claim(ctx, now)
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	d := jobs[0]
	if err = repo.Finish(ctx, d, "failed", "provider_rejected", now, now); err != nil {
		t.Fatal(err)
	}
	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := apidserver.MintDashboardSession(ctx, store, mgr, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, mgr, nil, 15*time.Minute, "")
	p := srv.ProductControl()
	p.EmailTransport, err = productemail.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p.EmailOpen = func(string, []byte) (string, error) { return strings.Repeat("A", 43), nil }
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"protocol": 1, "actor_id": actor, "organization_id": org, "product_id": app.ID, "role": "owner", "email": "fictional@example.test", "expires_at": time.Now().UTC().Add(time.Hour)})
	}))
	defer upstream.Close()
	p.AuthClient = upstream.Client()
	p.AuthOrigin = func(context.Context, state.Account, string) (state.App, string, error) {
		return auth, upstream.URL, nil
	}
	keys := map[string]string{}
	for name, scopes := range map[string][]string{"read": {api.ScopeAppsRead}, "write": {api.ScopeDeployWrite}, "admin": api.ScopesAdminOnly, "customer": {api.ScopePlatformTenantOperationsRead}} {
		key, hash, e := api.GenerateAPIKey()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = store.CreateAPIKey(ctx, acct.ID, hash, name, scopes); e != nil {
			t.Fatal(e)
		}
		keys[name] = key
	}
	base := "/dashboard/apps/" + app.Slug + "/product-control/email"
	apiBase := "/v1/apps/" + app.Slug + "/product/email-deliveries"
	jar := map[string]*http.Cookie{apidserver.SessionCookie: {Name: apidserver.SessionCookie, Value: sid}}
	call := func(method, path, body, key string, browser bool, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "https://example.test"+path, strings.NewReader(body))
		if browser {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			for _, c := range jar {
				r.AddCookie(c)
			}
		} else {
			r.Header.Set("Content-Type", "application/json")
		}
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d want %d %s", method, path, w.Code, want, w.Body.String())
		}
		response := w.Result()
		cookies := response.Cookies()
		_ = response.Body.Close()
		for _, c := range cookies {
			jar[c.Name] = c
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable owner email route", path)
		}
		return w
	}
	for _, path := range []string{base, base + "/" + d.ID + "/retry", apiBase, apiBase + "/" + d.ID + "/retry", "/v1/apps/" + app.Slug + "/product/email-recoveries", strings.TrimSuffix(base, "/email") + "/email-recoveries"} {
		for _, method := range []string{"GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"} {
			call(method, path, "", keys["admin"], false, 404)
		}
	}
	srv.WithProductControlEnabled(true)
	call("GET", apiBase, "", "", false, 401)
	call("GET", apiBase, "", keys["customer"], false, 403)
	call("GET", apiBase, "", keys["read"], false, 200)
	recoveryAPI := "/v1/apps/" + app.Slug + "/product/email-recoveries"
	call("GET", recoveryAPI, "", "", false, 401)
	call("GET", recoveryAPI, "", keys["customer"], false, 403)
	call("GET", recoveryAPI, "", keys["read"], false, 200)
	call("GET", strings.TrimSuffix(base, "/email")+"/email-recoveries", "", "", true, 200)
	optOutAPI := "/v1/apps/" + app.Slug + "/product/email-opt-outs"
	call("GET", optOutAPI, "", "", false, 401)
	call("GET", optOutAPI, "", keys["customer"], false, 403)
	call("GET", optOutAPI, "", keys["read"], false, 200)
	call("GET", optOutAPI+"?tenant="+uuid.NewString(), "", keys["read"], false, 404)
	call("GET", strings.TrimSuffix(base, "/email")+"/email-opt-outs", "", "", true, 200)

	call("POST", apiBase+"/"+d.ID+"/retry", `{}`, keys["read"], false, 403)
	call("GET", base, "", keys["admin"], false, 302)
	page := call("GET", base+"?tenant="+tenant.ID, "", "", true, 200)
	for _, private := range []string{"PRIVATE_EMAIL_QUESTION", "PRIVATE_EMAIL_RESPONSE", "fictional@example.test", "encrypted-fixture"} {
		if strings.Contains(page.Body.String(), private) {
			t.Fatal("private email data in console", private)
		}
	}
	doc, err := html.Parse(strings.NewReader(page.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			name, value := "", ""
			for _, a := range n.Attr {
				if a.Key == "name" {
					name = a.Val
				}
				if a.Key == "value" {
					value = a.Val
				}
			}
			if name != "" {
				form.Set(name, value)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if form.Get("csrf_token") == "" {
		t.Fatal("missing email retry form", page.Body.String())
	}
	delete(form, "status")
	missing := ownerConsoleCopy(form)
	missing.Del("csrf_token")
	call("POST", base+"/"+d.ID+"/retry", missing.Encode(), "", true, 403)
	call("POST", base+"/"+d.ID+"/retry", form.Encode()+"&attempt=1", "", true, 400)
	call("POST", base+"/"+d.ID+"/retry", form.Encode(), "", true, 200)
	call("POST", base+"/"+d.ID+"/retry", form.Encode(), "", true, 200)
	body, _ := json.Marshal(api.ProductEmailRetryRequest{TenantID: tenant.ID, ExpectedAttempts: &d.Attempts})
	call("POST", apiBase+"/"+d.ID+"/retry", string(body), keys["write"], false, 200)
	// Another account's owner cannot inspect the product or recover its receipt.
	foreign, err := store.CreateAccount(ctx, "foreign-email-owner@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(ctx, foreign.ID, hash, "foreign", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	call("GET", apiBase, "", key, false, 404)
	call("GET", optOutAPI, "", key, false, 404)
	call("POST", apiBase+"/"+d.ID+"/retry", string(body), key, false, 404)
	// The email console uses the same MFA and verified-email policy as other
	// owner mutations, including before domain parsing or recipient lookup.
	pendingID := uuid.NewString()
	if _, err = store.CreateSession(ctx, pendingID, acct.ID, "192.0.2.10", "owner-email-test"); err != nil {
		t.Fatal(err)
	}
	pending, err := mgr.IssueWithSessionAndBindingHash(pendingID, acct.ID, "", true)
	if err != nil {
		t.Fatal(err)
	}
	jar[apidserver.SessionCookie] = &http.Cookie{Name: apidserver.SessionCookie, Value: pending}
	blockedOptOut := call("GET", strings.TrimSuffix(base, "/email")+"/email-opt-outs", "", "", true, 302)
	if !strings.HasPrefix(blockedOptOut.Header().Get("Location"), "/dashboard/mfa") {
		t.Fatal("pending MFA reached opt-out history")
	}
	for _, method := range []string{"GET", "POST"} {
		path := base
		if method == "POST" {
			path += "/" + d.ID + "/retry"
		}
		w := call(method, path, form.Encode(), "", true, 302)
		if !strings.HasPrefix(w.Header().Get("Location"), "/dashboard/mfa") {
			t.Fatal("pending MFA reached owner email")
		}
	}
	created, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "unverified-email-owner@example.test", Plan: api.PlanPro, RequireEmailVerification: true})
	if err != nil {
		t.Fatal(err)
	}
	unverifiedID := uuid.NewString()
	if _, err = store.CreateSession(ctx, unverifiedID, created.Account.ID, "192.0.2.10", "owner-email-test"); err != nil {
		t.Fatal(err)
	}
	unverified, err := mgr.IssueWithSessionAndBindingHash(unverifiedID, created.Account.ID, "", false)
	if err != nil {
		t.Fatal(err)
	}
	jar[apidserver.SessionCookie] = &http.Cookie{Name: apidserver.SessionCookie, Value: unverified}
	call("POST", base+"/"+d.ID+"/retry", form.Encode(), "", true, 403)
	if revoked, e := store.RevokeSession(ctx, unverifiedID, created.Account.ID); e != nil || !revoked {
		t.Fatal(revoked, e)
	}
	call("GET", base, "", "", true, 302)
}
