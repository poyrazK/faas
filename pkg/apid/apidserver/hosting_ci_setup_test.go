package apidserver_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHostingCISetupFlagsRoutesAndCICredentialDenial(t *testing.T) {
	// adr: 202610171119 — only operators generate configuration; CI credentials retain their original narrow authority.
	for _, value := range []string{"", "false", "true", "1"} {
		m := apidserver.NewRuntimeConfigManager(func(key string) string {
			if key == "FAAS_FRONTEND_HOSTING_CI_SETUP_ENABLED" {
				return value
			}
			return ""
		})
		if m.Bool("frontend_hosting_ci_setup_enabled", false) != (value == "true" || value == "1") {
			t.Fatal(value)
		}
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "true")
	env := apidserver.Setup(t, api.PlanHobby)
	(*env.S()).WithAdminAllowlist(env.Acct().Email)
	values := map[string]string{"FAAS_FRONTEND_HOSTING_ENABLED": "true", "FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED": "true", "FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED": "true", "FAAS_FRONTEND_HOSTING_DOMAIN": "hosting.example", "FAAS_FRONTEND_HOSTING_R2_ENDPOINT": "https://example.r2.cloudflarestorage.com", "FAAS_FRONTEND_HOSTING_R2_BUCKET": "hosting", "FAAS_FRONTEND_HOSTING_R2_ACCESS_KEY": "test-key", "FAAS_FRONTEND_HOSTING_R2_SECRET_KEY": "test-secret", "FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID": strings.Repeat("a", 32), "FAAS_FRONTEND_HOSTING_KV_NAMESPACE_ID": strings.Repeat("b", 32), "FAAS_FRONTEND_HOSTING_CF_TOKEN": "test-token"}
	getenv := func(key string) string { return values[key] }
	if err := (*env.S()).ConfigureHostingForTest(getenv); err != nil {
		t.Fatal(err)
	}
	manager := apidserver.NewRuntimeConfigManager(getenv)
	(*env.S()).WithRuntimeConfigManager(manager)
	if _, err := (*env.Store()).CreateProject(t.Context(), state.Project{AccountID: env.Acct().ID, Slug: "ci-setup"}); err != nil {
		t.Fatal(err)
	}
	created := env.Do(t, "POST", "/v1/internal/hosting/sites", api.CreateHostingSiteRequest{Project: "ci-setup", Environment: "production", Name: "web"}, nil)
	var site api.HostingSite
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &site) != nil {
		t.Fatal(created.Code, created.Body)
	}
	root := "/v1/internal/hosting/sites/" + site.ID
	req := api.HostingCISetupRequest{TrustID: uuid.NewString()}
	if got := env.Do(t, "POST", root+"/ci-setup", req, nil); got.Code != 503 {
		t.Fatal(got.Code, got.Body)
	}
	if err := manager.Apply("frontend_hosting_ci_setup_enabled", []byte("true")); err != nil {
		t.Fatal(err)
	}
	caps := env.Do(t, "GET", "/v1/internal/hosting/capabilities", nil, nil)
	var capabilities api.HostingCapabilities
	if caps.Code != 200 || json.Unmarshal(caps.Body.Bytes(), &capabilities) != nil || !capabilities.CISetupEnabled {
		t.Fatal(caps.Code, caps.Body)
	}
	tr, err := (*env.Store()).CreateHostingOIDCTrust(t.Context(), api.HostingOIDCTrust{ID: req.TrustID, AccountID: env.Acct().ID, SiteID: site.ID, CreatedAt: time.Now().UTC(), HostingOIDCTrustRules: api.HostingOIDCTrustRules{RepositoryOwnerID: "123", RepositoryID: "456", Repository: "org/site", Ref: "refs/heads/main", WorkflowRef: "org/site/.github/workflows/preview.yml@refs/heads/main", EventName: "push"}})
	if err != nil {
		t.Fatal(err)
	}
	req.TrustID = tr.ID
	// The route remains useful for configuration blockers with deployment flags off.
	got := env.Do(t, "POST", root+"/ci-setup", req, nil)
	var report api.HostingCISetup
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &report) != nil || report.ConfigurationValid || report.WorkflowYAML != "" || got.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(got.Code, got.Body)
	}
	for _, ops := range [][]string{{"stage"}, {"preview"}, {"stage", "preview"}} {
		minted := env.Do(t, "POST", root+"/automation-credentials", api.CreateHostingAutomationCredentialRequest{Label: "setup", Operations: ops}, nil)
		var credential api.HostingAutomationCredentialResponse
		if minted.Code != 201 || json.Unmarshal(minted.Body.Bytes(), &credential) != nil {
			t.Fatal(minted.Code, minted.Body)
		}
		if got := env.Do(t, "POST", root+"/ci-setup", req, map[string]string{"Authorization": "Bearer " + credential.Token}); got.Code != 403 {
			t.Fatal("CI gained setup authority", got.Code, got.Body)
		}
	}
	if err := manager.Apply("frontend_hosting_ci_setup_enabled", []byte("false")); err != nil {
		t.Fatal(err)
	}
	if got := env.Do(t, "POST", root+"/ci-setup", req, nil); got.Code != 503 {
		t.Fatal("hot disable", got.Code, got.Body)
	}
}
