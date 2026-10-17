package apidserver_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHostingAutomationRuntimeOptIn(t *testing.T) {
	// adr: 202610170925 — automation requires an independent, hot-revocable opt-in.
	for _, value := range []string{"", "false", "true", "1"} {
		manager := apidserver.NewRuntimeConfigManager(func(key string) string {
			if key == "FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED" {
				return value
			}
			return ""
		})
		if manager.Bool("frontend_hosting_automation_enabled", false) != (value == "true" || value == "1") {
			t.Fatal(value)
		}
		if err := manager.Apply("frontend_hosting_automation_enabled", []byte("false")); err != nil {
			t.Fatal(err)
		}
		if manager.Bool("frontend_hosting_automation_enabled", true) {
			t.Fatal("hot disable ignored")
		}
	}
}

func TestHostingAutomationBearerCannotEscapeSiteOrOperations(t *testing.T) {
	// adr: 202610170925 — exercise the real route, MFA, scope and operator wrappers.
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "true")
	env := apidserver.Setup(t, api.PlanHobby)
	(*env.S()).WithAdminAllowlist(env.Acct().Email)
	values := map[string]string{
		"FAAS_FRONTEND_HOSTING_RETENTION_ENABLED": "true", "FAAS_FRONTEND_HOSTING_ENABLED": "true", "FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED": "true", "FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED": "true", "FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED": "true",
		"FAAS_FRONTEND_HOSTING_DOMAIN": "hosting.example", "FAAS_FRONTEND_HOSTING_R2_ENDPOINT": "https://example.r2.cloudflarestorage.com", "FAAS_FRONTEND_HOSTING_R2_BUCKET": "hosting", "FAAS_FRONTEND_HOSTING_R2_ACCESS_KEY": "test-key", "FAAS_FRONTEND_HOSTING_R2_SECRET_KEY": "test-secret",
		"FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID": strings.Repeat("a", 32), "FAAS_FRONTEND_HOSTING_KV_NAMESPACE_ID": strings.Repeat("b", 32), "FAAS_FRONTEND_HOSTING_CF_TOKEN": "test-token",
	}
	getenv := func(key string) string { return values[key] }
	if err := (*env.S()).ConfigureHostingForTest(getenv); err != nil {
		t.Fatal(err)
	}
	manager := apidserver.NewRuntimeConfigManager(getenv)
	(*env.S()).WithRuntimeConfigManager(manager)
	if _, err := (*env.Store()).CreateProject(t.Context(), state.Project{AccountID: env.Acct().ID, Slug: "automation"}); err != nil {
		t.Fatal(err)
	}
	created := env.Do(t, "POST", "/v1/internal/hosting/sites", api.CreateHostingSiteRequest{Project: "automation", Environment: "production", Name: "web"}, nil)
	var site api.HostingSite
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &site) != nil {
		t.Fatal(created.Code, created.Body)
	}
	root := "/v1/internal/hosting/sites/" + site.ID
	plan := env.Do(t, "GET", root+"/cleanup", nil, nil)
	if plan.Code != 200 {
		t.Fatal(plan.Code, plan.Body)
	}
	policy := env.Do(t, "PUT", root+"/retention", api.SetHostingRetentionRequest{Enabled: true, HostingRetentionRules: api.HostingRetentionRules{PendingAgeSeconds: 3600, StagedAgeSeconds: 7200}}, nil)
	if policy.Code != 200 {
		t.Fatal(policy.Code, policy.Body)
	}
	if err := manager.Apply("frontend_hosting_retention_enabled", []byte("false")); err != nil {
		t.Fatal(err)
	}
	plan = env.Do(t, "GET", root+"/cleanup", nil, nil)
	if plan.Code != 503 {
		t.Fatal("retention hot-disable", plan.Code, plan.Body)
	}
	if err := manager.Apply("frontend_hosting_retention_enabled", []byte("true")); err != nil {
		t.Fatal(err)
	}
	minted := env.Do(t, "POST", root+"/automation-credentials", api.CreateHostingAutomationCredentialRequest{Label: "CI"}, nil)
	var credential api.HostingAutomationCredentialResponse
	if minted.Code != 201 || json.Unmarshal(minted.Body.Bytes(), &credential) != nil {
		t.Fatal(minted.Code, minted.Body)
	}
	headers := map[string]string{"Authorization": "Bearer " + credential.Token, "Idempotency-Key": "same-run"}
	caps := env.Do(t, "GET", "/v1/internal/hosting/capabilities", nil, headers)
	var capabilities api.HostingCapabilities
	if caps.Code != 200 || json.Unmarshal(caps.Body.Bytes(), &capabilities) != nil || capabilities.AutomationSiteID != site.ID || len(capabilities.AutomationOperations) != 2 {
		t.Fatal(caps.Code, caps.Body)
	}
	for _, route := range []struct{ method, path string }{
		{"POST", root + "/promotions"}, {"DELETE", root}, {"PUT", root + "/suspension"},
		{"GET", root + "/automation-credentials"}, {"POST", root + "/automation-credentials"}, {"DELETE", root + "/automation-credentials/" + credential.Credential.ID},
		{"GET", "/v1/apps"}, {"GET", "/v1/internal/hosting/sites"}, {"GET", "/v1/internal/hosting/sites/" + uuid.NewString()},
		{"GET", root + "/cleanup"}, {"PUT", root + "/retention"}, {"GET", root + "/history"}, {"GET", root + "/domains"},
	} {
		r := env.Do(t, route.method, route.path, map[string]any{}, headers)
		if r.Code != 403 {
			t.Fatalf("%s %s: %d %s", route.method, route.path, r.Code, r.Body)
		}
	}
	path := root + "/ci/" + uuid.NewString()
	request := api.HostingCIRunRequest{Manifest: api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 1, SHA256: strings.Repeat("a", 64), ContentType: "text/html"}}}, PreviewTTLSeconds: 3600}
	first := env.Do(t, "PUT", path, request, headers)
	var run api.HostingCIRun
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &run) != nil {
		t.Fatal(first.Code, first.Body)
	}
	retry := env.Do(t, "PUT", path, request, headers)
	if retry.Code != 200 || retry.Body.String() != first.Body.String() {
		t.Fatal("unstable run", retry.Code, retry.Body)
	}
	if err := manager.Apply("frontend_hosting_automation_enabled", []byte("false")); err != nil {
		t.Fatal(err)
	}
	off := env.Do(t, "PUT", path, request, headers)
	if off.Code != 503 {
		t.Fatal("disabled replay", off.Code, off.Body)
	}
	if err := manager.Apply("frontend_hosting_automation_enabled", []byte("true")); err != nil {
		t.Fatal(err)
	}
	revoked := env.Do(t, "DELETE", root+"/automation-credentials/"+credential.Credential.ID, nil, nil)
	if revoked.Code != 204 {
		t.Fatal(revoked.Code, revoked.Body)
	}
	denied := env.Do(t, "GET", path, nil, headers)
	if denied.Code != 401 {
		t.Fatal("revoked token accepted", denied.Code, denied.Body)
	}
}
