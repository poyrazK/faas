package apidserver

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMCPCatalogMonitorHTTPAuthorization(t *testing.T) {
	e := setup(t, api.PlanPro)
	_ = mustSeedDeployment(t, e, "catalog-monitor-api")
	path := "/v1/apps/catalog-monitor-api/mcp-catalog-monitor"
	baseline, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Capabilities: []string{"tools"}, Tools: []mcphosting.Tool{}})
	raw, _ := mcphosting.MarshalContract(baseline)
	req := api.MCPCatalogMonitorConfigRequest{Enabled: true, Baseline: raw, FreshnessSeconds: 90}
	rec := e.do(t, http.MethodPut, path, req, nil)
	if rec.Code != 200 {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}
	var configured api.MCPCatalogMonitorSnapshot
	_ = json.Unmarshal(rec.Body.Bytes(), &configured)
	metricsToken, metricsHash, _ := api.GenerateAPIKey()
	_, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, metricsHash, "catalog-observer", []string{api.ScopeMetricsWrite, api.ScopeAppsRead})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + metricsToken}
	denied := e.do(t, http.MethodPut, path, req, headers)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("observer changed baseline: %d %s", denied.Code, denied.Body.String())
	}
	observed := e.do(t, http.MethodPost, path+"/observations", api.MCPCatalogObservationRequest{Revision: configured.Config.Revision, Observed: raw}, headers)
	if observed.Code != 200 {
		t.Fatalf("report: %d %s", observed.Code, observed.Body.String())
	}
	var receipt api.MCPCatalogReceipt
	_ = json.Unmarshal(observed.Body.Bytes(), &receipt)
	if receipt.State != "healthy" {
		t.Fatal("reported state not computed")
	}
	evidence := e.do(t, http.MethodGet, path+"?evidence="+receipt.ID, nil, headers)
	if evidence.Code != 200 {
		t.Fatal("read-scoped observer cannot inspect evidence")
	}
	other, err := e.store.CreateAccount(t.Context(), "other-catalog@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, otherHash, _ := api.GenerateAPIKey()
	_, err = e.store.CreateAPIKey(t.Context(), other.ID, otherHash, "other", api.ScopesAdminOnly)
	if err != nil {
		t.Fatal(err)
	}
	hidden := e.do(t, http.MethodGet, path+"?evidence="+receipt.ID, nil, map[string]string{"Authorization": "Bearer " + otherToken})
	if hidden.Code != 404 {
		t.Fatalf("cross-account evidence exposed: %d", hidden.Code)
	}
}

func TestMCPCatalogMonitorHTTPRoles(t *testing.T) {
	e := setup(t, api.PlanPro)
	_ = mustSeedDeployment(t, e, "catalog-role-api")
	path := "/v1/apps/catalog-role-api/mcp-catalog-monitor"
	baseline, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Capabilities: []string{"tools"}, Tools: []mcphosting.Tool{}})
	raw, _ := mcphosting.MarshalContract(baseline)
	revisions := map[string]string{}
	for _, role := range []string{"reader", "admin"} {
		rec := e.do(t, http.MethodPut, path, api.MCPCatalogMonitorConfigRequest{Role: role, TokenEnv: "MCP_" + role, Enabled: true, Baseline: raw, FreshnessSeconds: 90}, nil)
		if rec.Code != 200 {
			t.Fatalf("setup %s: %d %s", role, rec.Code, rec.Body)
		}
		var out api.MCPCatalogMonitorSnapshot
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		revisions[role] = out.Config.Revision
	}
	token, hash, _ := api.GenerateAPIKey()
	_, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "role-observer", []string{api.ScopeMetricsWrite, api.ScopeAppsRead})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	rec := e.do(t, http.MethodPost, path+"/observations", api.MCPCatalogObservationRequest{Role: "reader", Revision: revisions["reader"], Observed: raw}, headers)
	if rec.Code != 200 {
		t.Fatalf("report %d %s", rec.Code, rec.Body)
	}
	var receipt api.MCPCatalogReceipt
	_ = json.Unmarshal(rec.Body.Bytes(), &receipt)
	rec = e.do(t, http.MethodGet, path+"?all_roles=true", nil, headers)
	var summary api.MCPCatalogMonitorSummary
	_ = json.Unmarshal(rec.Body.Bytes(), &summary)
	if rec.Code != 200 || summary.State != "unavailable" || summary.Roles["reader"].State != "healthy" || summary.Roles["admin"].State != "unavailable" {
		t.Fatalf("summary: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, http.MethodGet, path+"?role=admin&evidence="+receipt.ID, nil, headers); rec.Code != 404 {
		t.Fatal("cross-role evidence returned")
	}
	if rec := e.do(t, http.MethodPost, path+"/observations", api.MCPCatalogObservationRequest{Role: "admin", Revision: revisions["reader"], Observed: raw}, headers); rec.Code != 409 {
		t.Fatal("cross-role revision accepted")
	}
	if rec := e.do(t, http.MethodPut, path, api.MCPCatalogMonitorConfigRequest{Role: "admin", Enabled: false, Baseline: raw, FreshnessSeconds: 90}, headers); rec.Code != 403 {
		t.Fatal("metrics credential changed role intent")
	}
	other, err := e.store.CreateAccount(t.Context(), "other-role@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, otherHash, _ := api.GenerateAPIKey()
	_, err = e.store.CreateAPIKey(t.Context(), other.ID, otherHash, "other", api.ScopesAdminOnly)
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, http.MethodGet, path+"?all_roles=true", nil, map[string]string{"Authorization": "Bearer " + otherToken}); rec.Code != 404 {
		t.Fatal("cross-account summary returned")
	}
}

func TestMCPCatalogMonitorHTTPManagedAuthorization(t *testing.T) {
	e := setup(t, api.PlanPro)
	_ = mustSeedDeployment(t, e, "managed-monitor-api")
	path := "/v1/apps/managed-monitor-api/mcp-catalog-monitor"
	baseline, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Capabilities: []string{"tools"}, Tools: []mcphosting.Tool{}})
	raw, _ := mcphosting.MarshalContract(baseline)
	req := api.MCPCatalogMonitorConfigRequest{Role: "reader", Enabled: true, Baseline: raw, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, SecretRef: "secret:READER_TOKEN"}}
	deployToken, deployHash, _ := api.GenerateAPIKey()
	_, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, deployHash, "deploy-without-secret-read", []string{api.ScopeDeployWrite, api.ScopeAppsRead})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + deployToken}
	if rec := e.do(t, http.MethodPut, path, req, headers); rec.Code != 403 {
		t.Fatalf("secret binding without permission: %d %s", rec.Code, rec.Body)
	}
	rec := e.do(t, http.MethodPut, path, req, nil)
	if rec.Code != 200 {
		t.Fatalf("managed setup: %d %s", rec.Code, rec.Body)
	}
	var out api.MCPCatalogMonitorSnapshot
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.ObserverStatus == nil || out.ObserverStatus.State != "scheduled" || out.Config.Observer.IntervalSeconds != 30 {
		t.Fatal("managed defaults/status missing")
	}
	metricsToken, metricsHash, _ := api.GenerateAPIKey()
	_, err = e.store.CreateAPIKey(t.Context(), e.acct.ID, metricsHash, "manual-observer", []string{api.ScopeMetricsWrite, api.ScopeAppsRead})
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, http.MethodPost, path+"/observations", api.MCPCatalogObservationRequest{Role: "reader", Revision: out.Config.Revision, Observed: raw}, map[string]string{"Authorization": "Bearer " + metricsToken}); rec.Code != 400 {
		t.Fatal("manual telemetry spoofed managed observation")
	}
	req.Enabled = false
	req.Observer = out.Config.Observer
	req.Observer.Paused = true
	if rec := e.do(t, http.MethodPut, path, req, headers); rec.Code != 200 {
		t.Fatalf("pause unnecessarily required secret access: %d %s", rec.Code, rec.Body)
	}
	req.Enabled = true
	req.Observer.Paused = false
	if rec := e.do(t, http.MethodPut, path, req, headers); rec.Code != 403 {
		t.Fatal("resume consumed a secret without read scope")
	}
}

func TestMCPCatalogMonitorHTTPOAuthAuthorization(t *testing.T) {
	e := setup(t, api.PlanPro)
	_ = mustSeedDeployment(t, e, "oauth-monitor-api")
	path := "/v1/apps/oauth-monitor-api/mcp-catalog-monitor"
	baseline, _ := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Capabilities: []string{"tools"}, Tools: []mcphosting.Tool{}})
	raw, _ := mcphosting.MarshalContract(baseline)
	req := api.MCPCatalogMonitorConfigRequest{Role: "reader", Enabled: true, Baseline: raw, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, OAuth: &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "reader", RefreshTokenSecretRef: "secret:REFRESH"}}}
	token, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "oauth-without-secret-read", []string{api.ScopeDeployWrite, api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	if rec := e.do(t, http.MethodPut, path, req, headers); rec.Code != 403 {
		t.Fatalf("OAuth binding without read: %d", rec.Code)
	}
	if rec := e.do(t, http.MethodPut, path, req, nil); rec.Code != 200 {
		t.Fatalf("OAuth setup: %d %s", rec.Code, rec.Body)
	}
	req.Enabled = false
	req.Observer.Paused = true
	if rec := e.do(t, http.MethodPut, path, req, headers); rec.Code != 200 {
		t.Fatalf("OAuth pause: %d", rec.Code)
	}
}

func TestMCPCatalogMonitorHTTPGuidedOAuthPermission(t *testing.T) {
	e := setup(t, api.PlanPro)
	_ = mustSeedDeployment(t, e, "guided-monitor-api")
	path := "/v1/apps/guided-monitor-api/mcp-catalog-monitor/oauth"
	token, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "guided-no-secret-read", []string{api.ScopeDeployWrite, api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	req := api.MCPCatalogOAuthRequest{Action: "begin", Role: "reader", Revision: uuid.NewString(), RedirectURI: "http://127.0.0.1:34567/callback"}
	rec := e.do(t, http.MethodPost, path, req, map[string]string{"Authorization": "Bearer " + token})
	if rec.Code != 403 {
		t.Fatalf("guided OAuth without secret permission: %d %s", rec.Code, rec.Body)
	}
}

func TestMCPCatalogMonitorHTTPOAuthLifecyclePrivacy(t *testing.T) {
	e := setup(t, api.PlanPro)
	_ = mustSeedDeployment(t, e, "oauth-lifecycle-api")
	app, err := e.store.AppBySlug(t.Context(), "oauth-lifecycle-api")
	if err != nil {
		t.Fatal(err)
	}
	monitors := e.store
	flows := e.store
	cfg := api.MCPCatalogMonitorConfig{Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", RevocationEndpoint: "https://provider.example/revoke", ClientID: "reader"}}}
	if _, err = monitors.ConfigureMCPCatalogMonitor(t.Context(), e.acct.ID, app.ID, cfg); err != nil {
		t.Fatal(err)
	}
	flow := api.MCPCatalogOAuthFlow{StateSHA256: strings.Repeat("a", 64), Ciphertext: []byte("PRIVATE_VERIFIER"), ExpiresAt: time.Now().Add(time.Minute)}
	view, err := flows.ChangeMCPCatalogOAuth(t.Context(), e.acct.ID, app.ID, "reader", cfg.Revision, "begin", "", &flow, nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err = flows.ChangeMCPCatalogOAuth(t.Context(), e.acct.ID, app.ID, "reader", view.Config.Revision, "consume", flow.StateSHA256, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	expiry := at.Add(time.Hour)
	oauth := api.MCPCatalogObserverOAuthStatus{State: "connected", ExpiresAt: &expiry, LastRenewalAt: &at, SourceSHA256: strings.Repeat("b", 64), Ciphertext: []byte("PRIVATE_GRANT")}
	view, err = flows.ChangeMCPCatalogOAuth(t.Context(), e.acct.ID, app.ID, "reader", view.Config.Revision, "finish", flow.StateSHA256, nil, &oauth)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/oauth-lifecycle-api/mcp-catalog-monitor"
	rec := e.do(t, http.MethodPost, path+"/oauth", api.MCPCatalogOAuthRequest{Action: "disconnect", Role: "reader", Revision: view.Config.Revision}, nil)
	if rec.Code != 200 {
		t.Fatalf("disconnect: %d %s", rec.Code, rec.Body)
	}
	var out api.MCPCatalogOAuthResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.State != "disconnected" || out.ProviderRevocation != "pending" {
		t.Fatal("missing retirement status", out)
	}
	for _, query := range []string{"?role=reader", "?all_roles=true"} {
		read := e.do(t, http.MethodGet, path+query, nil, nil)
		if read.Code != 200 {
			t.Fatalf("read: %d %s", read.Code, read.Body)
		}
		for _, private := range []string{"private_", "PRIVATE_GRANT", "PRIVATE_VERIFIER", "source_sha256"} {
			if strings.Contains(read.Body.String(), private) {
				t.Fatal("public route exposed private queue state", private)
			}
		}
		if !strings.Contains(read.Body.String(), "pending") {
			t.Fatal("queue status omitted")
		}
	}
}

func TestMCPCatalogMonitorHTTPDurableRetirementRecovery(t *testing.T) {
	e := setup(t, api.PlanPro)
	_ = mustSeedDeployment(t, e, "retirement-history-api")
	app, err := e.store.AppBySlug(t.Context(), "retirement-history-api")
	if err != nil {
		t.Fatal(err)
	}
	cfg := api.MCPCatalogMonitorConfig{Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: "reader"}}}
	if _, err = e.store.ConfigureMCPCatalogMonitor(t.Context(), e.acct.ID, app.ID, cfg); err != nil {
		t.Fatal(err)
	}
	flow := api.MCPCatalogOAuthFlow{StateSHA256: strings.Repeat("a", 64), Ciphertext: []byte("PRIVATE_VERIFIER"), ExpiresAt: time.Now().Add(time.Minute)}
	view, err := e.store.ChangeMCPCatalogOAuth(t.Context(), e.acct.ID, app.ID, "reader", cfg.Revision, "begin", "", &flow, nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err = e.store.ChangeMCPCatalogOAuth(t.Context(), e.acct.ID, app.ID, "reader", view.Config.Revision, "consume", flow.StateSHA256, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	expiry := at.Add(time.Hour)
	grant := api.MCPCatalogObserverOAuthStatus{State: "connected", ExpiresAt: &expiry, LastRenewalAt: &at, SourceSHA256: strings.Repeat("b", 64), Ciphertext: []byte("PRIVATE_GRANT")}
	if _, err = e.store.ChangeMCPCatalogOAuth(t.Context(), e.acct.ID, app.ID, "reader", view.Config.Revision, "finish", flow.StateSHA256, nil, &grant); err != nil {
		t.Fatal(err)
	}
	if err = e.store.DeleteApp(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodGet, "/v1/mcp-oauth-retirements", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("history: %d %s", rec.Code, rec.Body)
	}
	var history api.MCPOAuthRetirementsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &history)
	if len(history.Items) != 1 || history.Items[0].State != "manual_required" {
		t.Fatal("deleted app history missing")
	}
	if strings.Contains(rec.Body.String(), "private_") || strings.Contains(rec.Body.String(), "PRIVATE") {
		t.Fatal("history exposed retired grant")
	}
	token, hash, _ := api.GenerateAPIKey()
	if _, err = e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read-only", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	denied := e.do(t, http.MethodGet, "/v1/mcp-oauth-retirements", nil, map[string]string{"Authorization": "Bearer " + token})
	if denied.Code != 403 {
		t.Fatal("non-admin read retirement history")
	}
	recovered := e.do(t, http.MethodPost, "/v1/mcp-oauth-retirements/"+history.Items[0].ID, api.MCPOAuthRetirementAction{Action: "acknowledge_manual"}, nil)
	if recovered.Code != 200 {
		t.Fatalf("recovery: %d %s", recovered.Code, recovered.Body)
	}
	var out api.MCPOAuthRetirement
	_ = json.Unmarshal(recovered.Body.Bytes(), &out)
	if out.State != "manually_acknowledged" || strings.Contains(recovered.Body.String(), "private_") {
		t.Fatal("invalid public recovery response")
	}
}

func TestMCPCatalogMonitorHTTPRenewalRecoveryPrivacy(t *testing.T) {
	e := setup(t, api.PlanPro)
	_ = mustSeedDeployment(t, e, "oauth-renewal-api")
	app, err := e.store.AppBySlug(t.Context(), "oauth-renewal-api")
	if err != nil {
		t.Fatal(err)
	}
	cfg := api.MCPCatalogMonitorConfig{Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "reader", RefreshTokenSecretRef: "secret:REFRESH"}}}
	if _, err = e.store.ConfigureMCPCatalogMonitor(t.Context(), e.acct.ID, app.ID, cfg); err != nil {
		t.Fatal(err)
	}
	due, err := e.store.ListDueMCPCatalogObservers(t.Context(), 4)
	if err != nil || len(due) != 1 {
		t.Fatal("missing observer", err)
	}
	job, err := e.store.ClaimMCPCatalogObserver(t.Context(), due[0])
	if err != nil {
		t.Fatal(err)
	}
	journal, _, err := e.store.BeginMCPCatalogOAuthRenewal(t.Context(), job, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	expiry := at.Add(time.Hour)
	result := api.MCPCatalogObserverOAuthStatus{State: "connected", SourceSHA256: journal.SourceSHA256, Ciphertext: []byte("PRIVATE_RENEWAL_RESULT"), LastRenewalAt: &at, ExpiresAt: &expiry}
	if err = e.store.RecordMCPCatalogOAuthRenewal(t.Context(), job, journal.ID, "saved", &result); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"?role=reader", "?all_roles=true"} {
		out := e.do(t, http.MethodGet, "/v1/apps/oauth-renewal-api/mcp-catalog-monitor"+query, nil, nil)
		if out.Code != 200 {
			t.Fatal("monitor read failed", out.Code)
		}
		if !strings.Contains(out.Body.String(), `"oauth_renewal"`) || !strings.Contains(out.Body.String(), `"saved"`) {
			t.Fatal("renewal status omitted")
		}
		for _, value := range []string{"private_", "PRIVATE_RENEWAL_RESULT", journal.ID, journal.SourceSHA256} {
			if strings.Contains(out.Body.String(), value) {
				t.Fatal("renewal private metadata leaked")
			}
		}
	}
}

func TestMCPCatalogMonitorHTTPCredentialChangePrivacy(t *testing.T) {
	e := setup(t, api.PlanPro)
	_ = mustSeedDeployment(t, e, "oauth-credential-api")
	app, err := e.store.AppBySlug(t.Context(), "oauth-credential-api")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.store.UpsertAppSecret(t.Context(), e.acct.ID, app.ID, "REFRESH", []byte("PRIVATE_SOURCE")); err != nil {
		t.Fatal(err)
	}
	cfg := api.MCPCatalogMonitorConfig{Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", RevocationEndpoint: "https://provider.example/revoke", ClientID: "reader", RefreshTokenSecretRef: "secret:REFRESH"}}}
	if _, err = e.store.ConfigureMCPCatalogMonitor(t.Context(), e.acct.ID, app.ID, cfg); err != nil {
		t.Fatal(err)
	}
	due, err := e.store.ListDueMCPCatalogObservers(t.Context(), 4)
	if err != nil || len(due) != 1 {
		t.Fatal("missing job", err)
	}
	job, err := e.store.ClaimMCPCatalogObserver(t.Context(), due[0])
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	expiry := at.Add(time.Hour)
	grant := api.MCPCatalogObserverOAuthStatus{State: "connected", SourceSHA256: strings.Repeat("a", 64), Ciphertext: []byte("PRIVATE_GRANT"), LastRenewalAt: &at, ExpiresAt: &expiry}
	if err = e.store.SaveMCPCatalogObserverOAuth(t.Context(), job, grant); err != nil {
		t.Fatal(err)
	}
	if err = e.store.UpsertAppSecret(t.Context(), e.acct.ID, app.ID, "REFRESH", []byte("PRIVATE_REPLACEMENT")); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"?role=reader", "?all_roles=true"} {
		out := e.do(t, http.MethodGet, "/v1/apps/oauth-credential-api/mcp-catalog-monitor"+query, nil, nil)
		if out.Code != 200 || !strings.Contains(out.Body.String(), `"credential_state":"credential_changed"`) {
			t.Fatal("credential recovery status unavailable", out.Code)
		}
		for _, value := range []string{"private_", "PRIVATE", "source_sha256", job.LeaseID} {
			if strings.Contains(out.Body.String(), value) {
				t.Fatal("credential retirement exposed private data")
			}
		}
	}
}

func TestMCPCatalogMonitorHTTPConnectionHealthFleet(t *testing.T) {
	e := setup(t, api.PlanPro)
	_ = mustSeedDeployment(t, e, "connection-health-api")
	app, err := e.store.AppBySlug(t.Context(), "connection-health-api")
	if err != nil {
		t.Fatal(err)
	}
	cfg := api.MCPCatalogMonitorConfig{Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 30, TimeoutSeconds: 1, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: "reader"}}}
	if _, err = e.store.ConfigureMCPCatalogMonitor(t.Context(), e.acct.ID, app.ID, cfg); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/mcp-connection-health", "/v1/mcp-connection-health?app=connection-health-api"} {
		out := e.do(t, http.MethodGet, path, nil, nil)
		if out.Code != 200 {
			t.Fatal("health route failed", out.Code)
		}
		var health api.MCPConnectionHealthSnapshot
		if json.Unmarshal(out.Body.Bytes(), &health) != nil || !health.Complete || health.Counts["reauthorization_required"] != 1 || len(health.Items) != 1 || health.Items[0].RecoveryPath == "" {
			t.Fatal("fleet omitted actionable connection")
		}
		for _, private := range []string{"private_", "ciphertext", "source_sha256", "fingerprints", "lease_id"} {
			if strings.Contains(out.Body.String(), private) {
				t.Fatal("health route exposed private metadata")
			}
		}
	}
	readKey, hash, _ := api.GenerateAPIKey()
	if _, err = e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "health-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	out := e.do(t, http.MethodGet, "/v1/mcp-connection-health", nil, map[string]string{"Authorization": "Bearer " + readKey})
	if out.Code != 403 {
		t.Fatal("app key could read account fleet", out.Code)
	}
	out = e.do(t, http.MethodGet, "/v1/mcp-connection-health?offset=-1", nil, nil)
	if out.Code != 400 {
		t.Fatal("negative offset accepted")
	}
}
