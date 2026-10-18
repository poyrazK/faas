package apidserver_test

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardMCPOperationsOwnershipAndReadOnly(t *testing.T) {
	handler, cookie, store, _ := apidserver.NewAuthedDashboardServerFull(t)
	account, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "mcp-own", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(t.Context(), "other-mcp@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateApp(t.Context(), state.App{AccountID: other.ID, Slug: "mcp-foreign", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ConfigureMCPCatalogMonitor(t.Context(), account.ID, app.ID, api.MCPCatalogMonitorConfig{Enabled: true, Revision: "revision-1", FreshnessSeconds: 90, Baseline: json.RawMessage(`{"private-catalog-payload":"never-render"}`)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.RecordMCPCatalogObservation(t.Context(), account.ID, app.ID, api.MCPCatalogReceipt{Revision: "revision-1", State: "drift", ChangedCount: 1, Diff: json.RawMessage(`{"private-diff":"never-render"}`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		authed       bool
		status       int
	}{
		{"GET", "/dashboard/mcp", true, 200},
		{"GET", "/dashboard/mcp?app=mcp-own", true, 200},
		{"GET", "/dashboard/mcp?app=mcp-foreign", true, 404},
		{"GET", "/dashboard/mcp?app=mcp-foreign", false, 302},
		{"GET", "/dashboard/mcp?offset=-1", true, 400},
		{"GET", "/dashboard/mcp?health_offset=999999", true, 400},
		{"GET", "/dashboard/mcp?app=mcp-own&app=mcp-foreign", true, 400},
		{"GET", "/dashboard/mcp?evidence=foreign-receipt", true, 404},
		{"POST", "/dashboard/mcp", true, 405},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.authed {
				req.AddCookie(cookie)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if tc.status == 200 {
				body := rec.Body.String()
				for _, expected := range []string{"MCP operations", "Connection health", "Catalog: drift", "Policy evidence unavailable", "Investigate requests", "Application logs"} {
					if !strings.Contains(body, expected) {
						t.Errorf("missing %q", expected)
					}
				}
				for _, secret := range []string{"mcp-foreign", "private-catalog-payload", "private-diff", "never-render", "method=\"post\""} {
					if strings.Contains(body, secret) {
						t.Errorf("unexpected %q", secret)
					}
				}
				if rec.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("missing no-store")
				}
			}
		})
	}
	snapshot, err := store.GetMCPCatalogMonitor(t.Context(), account.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Config.Revision != "revision-1" || snapshot.Latest == nil || snapshot.Latest.State != "drift" {
		t.Fatal("read-only dashboard changed monitor state")
	}
}

func TestDashboardMCPOperationsApplicationPagination(t *testing.T) {
	handler, cookie, store, _ := apidserver.NewAuthedDashboardServerFull(t)
	account, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 26; i++ {
		_, err = store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: fmt.Sprintf("mcp-page-%02d", i), Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ path, has, missing string }{
		{"/dashboard/mcp", "mcp-page-00", "mcp-page-25"},
		{"/dashboard/mcp?offset=25", "mcp-page-25", "mcp-page-00"},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatal(rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, tc.has) || strings.Contains(body, tc.missing) {
			t.Fatalf("wrong page: %s", body)
		}
	}
}

func TestDashboardMCPOperationsAdmissionIsolation(t *testing.T) {
	handler, cookie, store, _ := apidserver.NewAuthedDashboardServerFull(t)
	account, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(t.Context(), "foreign-receipts@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	ownID, foreignID := "", ""
	for _, owner := range []state.Account{account, other} {
		slug := "mcp-receipt-own"
		if owner.ID == other.ID {
			slug = "mcp-receipt-foreign"
		}
		app, e := store.CreateApp(t.Context(), state.App{AccountID: owner.ID, Slug: slug, Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
		if e != nil {
			t.Fatal(e)
		}
		deployment, e := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, ImageDigest: "sha256:fixture", TrafficPercentExplicit: true})
		if e != nil {
			t.Fatal(e)
		}
		ownerUUID := uuid.MustParse(owner.ID)
		for _, outcome := range []string{"accepted", "rejected"} {
			id := uuid.NewString()
			if outcome == "rejected" {
				if owner.ID == account.ID {
					ownID = id
				} else {
					foreignID = id
				}
			}
			evidence := api.MCPAdmissionEvidence{ID: id, DeploymentID: deployment.ID, Outcome: outcome, Reason: "mcp_policy_changed", CapturedAt: time.Now().UTC(), TrafficPercent: 25}
			raw, e := json.Marshal(map[string]any{"mcp_admission": evidence})
			if e != nil {
				t.Fatal(e)
			}
			_, e = store.AppendDeploymentAudit(t.Context(), state.DeploymentAudit{AccountID: &ownerUUID, DeploymentID: uuid.MustParse(deployment.ID), Kind: state.DeployMCPAdmission, Actor: "fixture", Data: raw})
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/dashboard/mcp", 200},
		{"/dashboard/mcp?evidence=" + ownID, 200},
		{"/dashboard/mcp?evidence=" + foreignID, 404},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, rec.Code, rec.Body.String())
		}
		if tc.status == 200 {
			body := rec.Body.String()
			if !strings.Contains(body, ownID) || strings.Contains(body, foreignID) || strings.Contains(body, "mcp-receipt-foreign") {
				t.Fatal("receipt ownership or summary failed")
			}
		}
	}
}

func TestDashboardMCPRecoveryFormGuards(t *testing.T) {
	handler, cookie, _, _ := apidserver.NewAuthedDashboardServerFull(t)
	req := httptest.NewRequest("GET", "/dashboard/mcp", nil)
	req.AddCookie(cookie)
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, req)
	var csrf *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == "dashboard_mcp_recovery_csrf" {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("missing recovery CSRF cookie")
	}
	for _, tc := range []struct {
		name, action, confirmed string
		token, auth             bool
		status                  int
	}{
		{"unauthenticated", "retry", "", true, false, 302},
		{"missing csrf", "retry", "", false, true, 400},
		{"manual confirmation required", "acknowledge_manual", "", true, true, 400},
		{"unknown action", "begin", "", true, true, 400},
		{"unknown retirement", "retry", "", true, true, 404},
		{"confirmed unknown retirement", "acknowledge_manual", "yes", true, true, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{"action": {tc.action}}
			if tc.token {
				form.Set("csrf_token", csrf.Value)
			}
			if tc.confirmed != "" {
				form.Set("provider_confirmed", tc.confirmed)
			}
			r := httptest.NewRequest("POST", "/dashboard/mcp/retirements/"+uuid.NewString(), strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.auth {
				r.AddCookie(cookie)
				r.AddCookie(csrf)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, r)
			if rec.Code != tc.status {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDashboardMCPRecoveryManualResolutionAndOwnership(t *testing.T) {
	handler, cookie, store, _ := apidserver.NewAuthedDashboardServerFull(t)
	account, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	create := func(owner state.Account, slug string) string {
		app, e := store.CreateApp(t.Context(), state.App{AccountID: owner.ID, Slug: slug, Type: state.AppTypeApp, Status: state.AppActive})
		if e != nil {
			t.Fatal(e)
		}
		cfg := api.MCPCatalogMonitorConfig{Role: "reader", Enabled: true, Revision: uuid.NewString(), FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, Endpoint: "/mcp", IntervalSeconds: 30, TimeoutSeconds: 1, OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: "reader", Scopes: []string{"read"}}}}
		_, e = store.ConfigureMCPCatalogMonitor(t.Context(), owner.ID, app.ID, cfg)
		if e != nil {
			t.Fatal(e)
		}
		hash := strings.Repeat("a", 64)
		view, e := store.ChangeMCPCatalogOAuth(t.Context(), owner.ID, app.ID, "reader", cfg.Revision, "begin", "", &api.MCPCatalogOAuthFlow{StateSHA256: hash, Ciphertext: []byte("private-flow"), ExpiresAt: time.Now().Add(time.Minute)}, nil)
		if e != nil {
			t.Fatal(e)
		}
		view, e = store.ChangeMCPCatalogOAuth(t.Context(), owner.ID, app.ID, "reader", view.Config.Revision, "consume", hash, nil, nil)
		if e != nil {
			t.Fatal(e)
		}
		record, e := store.PrepareMCPOAuthRetirement(t.Context(), state.MCPCatalogObserverJob{AccountID: owner.ID, AppID: app.ID, Role: "reader", Snapshot: view}, nil)
		if e != nil {
			t.Fatal(e)
		}
		return record.ID
	}
	owned := create(account, "recover-own")
	foreignAccount, err := store.CreateAccount(t.Context(), "recover-foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign := create(foreignAccount, "recover-foreign")
	get := httptest.NewRequest("GET", "/dashboard/mcp", nil)
	get.AddCookie(cookie)
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, get)
	var csrf *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == "dashboard_mcp_recovery_csrf" {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("missing csrf")
	}
	if strings.Contains(page.Body.String(), foreign) || strings.Contains(page.Body.String(), "private-flow") {
		t.Fatal("foreign or private retirement exposed")
	}
	for _, tc := range []struct {
		id, action string
		status     int
	}{{foreign, "acknowledge_manual", 404}, {owned, "retry", 400}, {owned, "acknowledge_manual", 303}, {owned, "acknowledge_manual", 409}} {
		form := url.Values{"csrf_token": {csrf.Value}, "action": {tc.action}, "provider_confirmed": {"yes"}}
		req := httptest.NewRequest("POST", "/dashboard/mcp/retirements/"+tc.id, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		req.AddCookie(csrf)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("%s %s status %d: %s", tc.id, tc.action, rec.Code, rec.Body.String())
		}
	}
	records, err := store.ListMCPOAuthRetirements(t.Context(), account.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records.Items) != 1 || records.Items[0].State != "manually_acknowledged" {
		t.Fatal("resolution not recorded", records)
	}
}

func TestDashboardMCPReadinessEnvironmentSelection(t *testing.T) {
	handler, cookie, store, _ := apidserver.NewAuthedDashboardServerFull(t)
	account, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "scoped-dashboard", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	policies := state.MCPReadinessStore(store)
	policy, err := policies.SetMCPReadinessPolicy(t.Context(), account.ID, app.ID, api.SetMCPReadinessPolicyRequest{Scope: "staging", Enabled: true, Endpoint: "/mcp", MaxAgeSeconds: 300, Reason: "reviewed staging <script>policy</script>", Roles: []api.MCPReadinessRole{{Name: "reader", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: strings.Repeat("a", 64)}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		scope    string
		status   int
		contains string
		excludes string
	}{
		{"staging", 200, policy.Revision, "Not configured"},
		{"default", 200, "Disabled", policy.Revision},
		{"preview", 200, "Not configured", policy.Revision},
		{"__all__", 400, "", ""},
	} {
		req := httptest.NewRequest("GET", "/dashboard/mcp?app=scoped-dashboard&scope="+tc.scope, nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("scope %s: %d %s", tc.scope, rec.Code, rec.Body.String())
		}
		if tc.status == 200 {
			_, readiness, ok := strings.Cut(rec.Body.String(), "<h2>Deployment readiness</h2>")
			if !ok {
				t.Fatal("missing readiness section")
			}
			readiness, _, _ = strings.Cut(readiness, "</section>")
			if tc.scope == "staging" && (!strings.Contains(readiness, "reviewed staging &lt;script&gt;policy&lt;/script&gt;") || strings.Contains(readiness, "<script>policy</script>")) {
				t.Fatal("audit reason not HTML escaped", readiness)
			}
			if !strings.Contains(readiness, tc.contains) || strings.Contains(readiness, tc.excludes) {
				t.Fatalf("wrong environment %s: %s", tc.scope, readiness)
			}
		}
	}
}
