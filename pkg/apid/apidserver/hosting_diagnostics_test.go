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

func TestHostingDiagnosticsFlagsRoutesAndCIReadAuthority(t *testing.T) {
	// adr: 202610171059 — a same-site read-only report adds no deployment or account authority.
	for _, value := range []string{"", "false", "true", "1"} {
		m := apidserver.NewRuntimeConfigManager(func(key string) string {
			if key == "FAAS_FRONTEND_HOSTING_DIAGNOSTICS_ENABLED" {
				return value
			}
			return ""
		})
		if m.Bool("frontend_hosting_diagnostics_enabled", false) != (value == "true" || value == "1") {
			t.Fatal(value)
		}
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "true")
	env := apidserver.Setup(t, api.PlanHobby)
	(*env.S()).WithAdminAllowlist(env.Acct().Email)
	values := map[string]string{"FAAS_FRONTEND_HOSTING_ENABLED": "true", "FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED": "true", "FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED": "true", "FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED": "true", "FAAS_FRONTEND_HOSTING_DOMAIN": "hosting.example", "FAAS_FRONTEND_HOSTING_R2_ENDPOINT": "https://example.r2.cloudflarestorage.com", "FAAS_FRONTEND_HOSTING_R2_BUCKET": "hosting", "FAAS_FRONTEND_HOSTING_R2_ACCESS_KEY": "test-key", "FAAS_FRONTEND_HOSTING_R2_SECRET_KEY": "test-secret", "FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID": strings.Repeat("a", 32), "FAAS_FRONTEND_HOSTING_KV_NAMESPACE_ID": strings.Repeat("b", 32), "FAAS_FRONTEND_HOSTING_CF_TOKEN": "test-token"}
	getenv := func(key string) string { return values[key] }
	if err := (*env.S()).ConfigureHostingForTest(getenv); err != nil {
		t.Fatal(err)
	}
	manager := apidserver.NewRuntimeConfigManager(getenv)
	(*env.S()).WithRuntimeConfigManager(manager)
	if _, err := (*env.Store()).CreateProject(t.Context(), state.Project{AccountID: env.Acct().ID, Slug: "diagnostics"}); err != nil {
		t.Fatal(err)
	}
	created := env.Do(t, "POST", "/v1/internal/hosting/sites", api.CreateHostingSiteRequest{Project: "diagnostics", Environment: "production", Name: "web"}, nil)
	var site api.HostingSite
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &site) != nil {
		t.Fatal(created.Code, created.Body)
	}
	root := "/v1/internal/hosting/sites/" + site.ID
	if got := env.Do(t, "GET", root+"/diagnostics", nil, nil); got.Code != 503 {
		t.Fatal("default off", got.Code, got.Body)
	}
	if err := manager.Apply("frontend_hosting_diagnostics_enabled", []byte("true")); err != nil {
		t.Fatal(err)
	}
	caps := env.Do(t, "GET", "/v1/internal/hosting/capabilities", nil, nil)
	var capabilities api.HostingCapabilities
	if caps.Code != 200 || json.Unmarshal(caps.Body.Bytes(), &capabilities) != nil || !capabilities.DiagnosticsEnabled {
		t.Fatal(caps.Code, caps.Body)
	}
	for _, ops := range [][]string{{"stage"}, {"preview"}, {"stage", "preview"}} {
		minted := env.Do(t, "POST", root+"/automation-credentials", api.CreateHostingAutomationCredentialRequest{Label: "diagnostics", Operations: ops}, nil)
		var token api.HostingAutomationCredentialResponse
		if minted.Code != 201 || json.Unmarshal(minted.Body.Bytes(), &token) != nil {
			t.Fatal(minted.Code, minted.Body)
		}
		headers := map[string]string{"Authorization": "Bearer " + token.Token}
		got := env.Do(t, "GET", root+"/diagnostics?run_id="+uuid.NewString(), nil, headers)
		if len(ops) == 1 {
			if got.Code != 403 {
				t.Fatal("partial CI scope gained aggregate reads", got.Code)
			}
			continue
		}
		var report api.HostingDiagnostics
		if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &report) != nil || report.SiteID != site.ID || report.RetryAdvice != "inspect_before_retry" || report.ServingHealth != "not_checked" || got.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(got.Code, got.Body)
		}
		for _, route := range []struct{ method, path string }{{"GET", "/v1/internal/hosting/sites/" + uuid.NewString() + "/diagnostics"}, {"POST", root + "/diagnostics"}, {"GET", "/v1/account"}} {
			got = env.Do(t, route.method, route.path, nil, headers)
			if got.Code != 403 && got.Code != 405 {
				t.Fatal(route, got.Code, got.Body)
			}
		}
		if err := manager.Apply("frontend_hosting_diagnostics_enabled", []byte("false")); err != nil {
			t.Fatal(err)
		}
		if got = env.Do(t, "GET", root+"/diagnostics", nil, headers); got.Code != 503 {
			t.Fatal("hot disable", got.Code, got.Body)
		}
	}
}
