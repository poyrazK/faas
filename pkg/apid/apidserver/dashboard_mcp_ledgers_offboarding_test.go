package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The MCP operations page's connection-health and grant-retirement panels are
// account-wide. A company app's issues and retirement records keep its
// creator's account ID, so the panels leave them out once the company removes
// the creator, as the page's app list already does.
func TestDashboardMCPLedgersHideCompanyAppsAfterOffboarding(t *testing.T) {
	env := newDashboardFailedEventsTestEnv(t)
	ctx := t.Context()
	owner, err := env.store.CreateAccount(ctx, "acme-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := env.store.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := env.store.AddOrgMember(ctx, org.ID, env.account.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	creator := env.account.ID
	for _, app := range []state.App{{OrgID: org.ID, Slug: "acme-mcp"}, {Slug: "own-mcp"}} {
		app.AccountID, app.Type, app.Runtime, app.RAMMB, app.Status = creator, state.AppTypeApp, "node22", 128, state.AppActive
		created, err := env.store.CreateApp(ctx, app)
		if err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"reader", "writer"} {
			cfg := api.MCPCatalogMonitorConfig{Role: role, Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: role}}}
			if _, err := env.store.ConfigureMCPCatalogMonitor(ctx, creator, created.ID, cfg); err != nil {
				t.Fatal(err)
			}
			if role == "reader" {
				continue
			}
			flow := api.MCPCatalogOAuthFlow{StateSHA256: strings.Repeat("a", 64), Ciphertext: []byte("VERIFIER"), ExpiresAt: time.Now().UTC().Add(time.Minute)}
			view, err := env.store.ChangeMCPCatalogOAuth(ctx, creator, created.ID, role, cfg.Revision, "begin", "", &flow, nil)
			if err != nil {
				t.Fatal(err)
			}
			if view, err = env.store.ChangeMCPCatalogOAuth(ctx, creator, created.ID, role, view.Config.Revision, "consume", flow.StateSHA256, nil, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := env.store.PrepareMCPOAuthRetirement(ctx, state.MCPCatalogObserverJob{AccountID: creator, AppID: created.ID, Slug: created.Slug, Role: role, Snapshot: view}, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	page := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/dashboard/mcp", nil)
		req.AddCookie(env.session)
		env.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("MCP operations page = %d", rec.Code)
		}
		return rec.Body.String()
	}
	// The company slug appears in the app list, a health issue and a
	// retirement row; after offboarding it must appear in none of them,
	// while every mention of the creator's own app stays.
	before := page()
	if !strings.Contains(before, "acme-mcp") {
		t.Fatal("member's MCP page does not show the company app")
	}
	if err := env.store.RemoveOrgMember(ctx, org.ID, creator); err != nil {
		t.Fatal(err)
	}
	after := page()
	if n := strings.Count(after, "acme-mcp"); n != 0 {
		t.Errorf("offboarded creator's MCP page still shows the company app %d times", n)
	}
	if strings.Count(after, "own-mcp") != strings.Count(before, "own-mcp") {
		t.Errorf("own app mentions went from %d to %d", strings.Count(before, "own-mcp"), strings.Count(after, "own-mcp"))
	}
}
