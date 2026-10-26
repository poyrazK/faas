package mcpcatalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The account-wide connection health and grant retirement views name no app
// in their URL, so appaccess.Wrap never decides for them. A company app keeps
// its creator's account ID, so these views follow the inventories' rule
// themselves: once the company removes the creator, its app's issues and
// retirement records leave the views and its records cannot be recovered.
func TestAccountWideMCPViewsHideCompanyAppsAfterOffboarding(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	creator, err := store.CreateAccount(ctx, "creator@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateAccount(ctx, "owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	// Each app gets a "reader" role that needs reauthorization (a health
	// issue) and a "writer" role whose lost exchange is a retirement record.
	retirements := map[string]string{}
	newApp := func(app state.App) state.App {
		t.Helper()
		app.AccountID, app.Type, app.RAMMB, app.MaxConcurrency, app.IdleTimeoutS, app.Status = creator.ID, state.AppTypeApp, 256, 1, 30, state.AppActive
		created, err := store.CreateApp(ctx, app)
		if err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"reader", "writer"} {
			cfg := api.MCPCatalogMonitorConfig{Role: role, Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: role}}}
			if _, err := store.ConfigureMCPCatalogMonitor(ctx, creator.ID, created.ID, cfg); err != nil {
				t.Fatal(err)
			}
			if role != "writer" {
				continue
			}
			flow := api.MCPCatalogOAuthFlow{StateSHA256: strings.Repeat("a", 64), Ciphertext: []byte("VERIFIER"), ExpiresAt: time.Now().UTC().Add(time.Minute)}
			view, err := store.ChangeMCPCatalogOAuth(ctx, creator.ID, created.ID, role, cfg.Revision, "begin", "", &flow, nil)
			if err != nil {
				t.Fatal(err)
			}
			if view, err = store.ChangeMCPCatalogOAuth(ctx, creator.ID, created.ID, role, view.Config.Revision, "consume", flow.StateSHA256, nil, nil); err != nil {
				t.Fatal(err)
			}
			record, err := store.PrepareMCPOAuthRetirement(ctx, state.MCPCatalogObserverJob{AccountID: creator.ID, AppID: created.ID, Slug: created.Slug, Role: role, Snapshot: view}, nil)
			if err != nil {
				t.Fatal(err)
			}
			retirements[created.ID] = record.ID
		}
		return created
	}
	company := newApp(state.App{OrgID: org.ID, Slug: "acme-mcp"})
	own := newApp(state.App{Slug: "own-mcp"})

	serve := func(handler func(http.ResponseWriter, *http.Request, state.Account), method, target, id, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		handler(rec, req, creator)
		return rec
	}
	health := func(query string) (api.MCPConnectionHealthSnapshot, int) {
		t.Helper()
		var out api.MCPConnectionHealthSnapshot
		rec := serve(ConnectionHealthHandler(store), http.MethodGet, "/v1/mcp-connection-health"+query, "", "")
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out, rec.Code
	}
	shown := func() (healthApps, ledgerApps map[string]bool) {
		t.Helper()
		snapshot, code := health("")
		if code != http.StatusOK {
			t.Fatalf("connection health = %d", code)
		}
		healthApps, ledgerApps = map[string]bool{}, map[string]bool{}
		counted := 0
		for _, n := range snapshot.Counts {
			counted += n
		}
		if counted != snapshot.TotalIssues || len(snapshot.Items) != snapshot.TotalIssues {
			t.Fatalf("health counts %v total %d over %d items", snapshot.Counts, snapshot.TotalIssues, len(snapshot.Items))
		}
		for _, issue := range snapshot.Items {
			healthApps[issue.AppID] = true
		}
		var ledger api.MCPOAuthRetirementsResponse
		rec := serve(RetirementHandler(store), http.MethodGet, "/v1/mcp-oauth-retirements", "", "")
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &ledger) != nil {
			t.Fatalf("retirement ledger = %d %s", rec.Code, rec.Body.String())
		}
		for _, record := range ledger.Items {
			ledgerApps[record.AppID] = true
		}
		return healthApps, ledgerApps
	}

	healthApps, ledgerApps := shown()
	if !healthApps[company.ID] || !healthApps[own.ID] || !ledgerApps[company.ID] || !ledgerApps[own.ID] {
		t.Fatalf("member sees health %v and ledger %v, want both apps in each", healthApps, ledgerApps)
	}
	if _, code := health("?app=" + company.Slug); code != http.StatusOK {
		t.Fatalf("member's company health filter = %d", code)
	}

	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	healthApps, ledgerApps = shown()
	if healthApps[company.ID] || ledgerApps[company.ID] {
		t.Errorf("offboarded creator still sees the company app: health %v, ledger %v", healthApps, ledgerApps)
	}
	if !healthApps[own.ID] || !ledgerApps[own.ID] {
		t.Errorf("offboarded creator lost their own app: health %v, ledger %v", healthApps, ledgerApps)
	}
	if _, code := health("?app=" + company.Slug); code != http.StatusNotFound {
		t.Errorf("offboarded creator's company health filter = %d, want 404", code)
	}
	acknowledge := `{"action":"acknowledge_manual"}`
	if rec := serve(RetirementHandler(store), http.MethodPost, "/v1/mcp-oauth-retirements/x", retirements[company.ID], acknowledge); rec.Code != http.StatusNotFound {
		t.Errorf("offboarded creator acknowledging the company's retirement = %d, want 404", rec.Code)
	}
	ledger, err := store.ListMCPOAuthRetirements(ctx, creator.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range ledger.Items {
		if record.AppID == company.ID && record.State == "manually_acknowledged" {
			t.Error("the company's retirement was acknowledged by its removed creator")
		}
	}
	if rec := serve(RetirementHandler(store), http.MethodPost, "/v1/mcp-oauth-retirements/x", retirements[own.ID], acknowledge); rec.Code != http.StatusOK {
		t.Errorf("acknowledging the creator's own retirement = %d %s", rec.Code, rec.Body.String())
	}
}
