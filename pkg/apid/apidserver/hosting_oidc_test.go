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

func TestHostingOIDCRuntimeAndTrustRoutes(t *testing.T) {
	// adr: 202610171028 — flags, real auth wrappers and revocation apply to already minted sessions.
	for _, value := range []string{"", "false", "true", "1"} {
		manager := apidserver.NewRuntimeConfigManager(func(key string) string {
			if key == "FAAS_FRONTEND_HOSTING_OIDC_ENABLED" {
				return value
			}
			return ""
		})
		if manager.Bool("frontend_hosting_oidc_enabled", false) != (value == "true" || value == "1") {
			t.Fatal(value)
		}
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "true")
	env := apidserver.Setup(t, api.PlanHobby)
	(*env.S()).WithAdminAllowlist(env.Acct().Email)
	values := map[string]string{"FAAS_FRONTEND_HOSTING_ENABLED": "true", "FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED": "true", "FAAS_FRONTEND_HOSTING_DURABLE_ROUTING_ENABLED": "true", "FAAS_FRONTEND_HOSTING_OIDC_ENABLED": "true", "FAAS_FRONTEND_HOSTING_DOMAIN": "hosting.example", "FAAS_FRONTEND_HOSTING_R2_ENDPOINT": "https://example.r2.cloudflarestorage.com", "FAAS_FRONTEND_HOSTING_R2_BUCKET": "hosting", "FAAS_FRONTEND_HOSTING_R2_ACCESS_KEY": "test-key", "FAAS_FRONTEND_HOSTING_R2_SECRET_KEY": "test-secret", "FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID": strings.Repeat("a", 32), "FAAS_FRONTEND_HOSTING_KV_NAMESPACE_ID": strings.Repeat("b", 32), "FAAS_FRONTEND_HOSTING_CF_TOKEN": "test-token"}
	getenv := func(key string) string { return values[key] }
	if err := (*env.S()).ConfigureHostingForTest(getenv); err != nil {
		t.Fatal(err)
	}
	manager := apidserver.NewRuntimeConfigManager(getenv)
	(*env.S()).WithRuntimeConfigManager(manager)
	if _, err := (*env.Store()).CreateProject(t.Context(), state.Project{AccountID: env.Acct().ID, Slug: "oidc"}); err != nil {
		t.Fatal(err)
	}
	created := env.Do(t, "POST", "/v1/internal/hosting/sites", api.CreateHostingSiteRequest{Project: "oidc", Environment: "production", Name: "web"}, nil)
	var site api.HostingSite
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &site) != nil {
		t.Fatal(created.Code, created.Body)
	}
	root := "/v1/internal/hosting/sites/" + site.ID
	rules := api.HostingOIDCTrustRules{RepositoryOwnerID: "123", RepositoryID: "456", Repository: "org/site", Ref: "refs/heads/main", WorkflowRef: "org/site/.github/workflows/preview.yml@refs/heads/main", EventName: "push"}
	response := env.Do(t, "POST", root+"/oidc-trusts", rules, map[string]string{"Idempotency-Key": "never-store-identity"})
	var trust api.HostingOIDCTrust
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &trust) != nil || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(response.Code, response.Body)
	}
	list := env.Do(t, "GET", root+"/oidc-trusts", nil, nil)
	if list.Code != 200 || !strings.Contains(list.Body.String(), trust.ID) {
		t.Fatal(list.Code, list.Body)
	}
	caps := env.Do(t, "GET", "/v1/internal/hosting/capabilities", nil, nil)
	var capabilities api.HostingCapabilities
	if caps.Code != 200 || json.Unmarshal(caps.Body.Bytes(), &capabilities) != nil || !capabilities.OIDCEnabled {
		t.Fatal(caps.Code, caps.Body)
	}
	token, hash, err := api.GenerateHostingAutomationToken()
	if err != nil {
		t.Fatal(err)
	}
	store := *env.Store()
	now := time.Now().UTC()
	if _, err = store.CreateHostingOIDCCredential(t.Context(), trust, api.HostingAutomationCredential{ID: uuid.NewString(), AccountID: env.Acct().ID, SiteID: site.ID, OIDCTrustID: trust.ID, Label: "GitHub OIDC", Operations: []string{"stage", "preview"}, Hash: hash, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	for _, path := range []string{root, root + "/status", "/v1/internal/hosting/capabilities"} {
		if got := env.Do(t, "GET", path, nil, headers); got.Code != 200 {
			t.Fatal(path, got.Code, got.Body)
		}
	}
	for _, route := range []struct{ method, path string }{{"GET", root + "/oidc-trusts"}, {"POST", root + "/oidc-trusts"}, {"DELETE", root + "/oidc-trusts/" + trust.ID}, {"POST", root + "/promotions"}, {"GET", "/v1/account"}} {
		got := env.Do(t, route.method, route.path, rules, headers)
		if got.Code != 403 {
			t.Fatal("authority escaped", route, got.Code, got.Body)
		}
	}
	if err = manager.Apply("frontend_hosting_oidc_enabled", []byte("false")); err != nil {
		t.Fatal(err)
	}
	if got := env.Do(t, "GET", root, nil, headers); got.Code != 503 {
		t.Fatal("live session survived kill switch", got.Code, got.Body)
	}
	if got := env.Do(t, "POST", "/v1/internal/hosting/oidc/exchange", api.HostingOIDCExchangeRequest{}, nil); got.Code != 503 {
		t.Fatal("exchange default-off", got.Code, got.Body)
	}
	if err = manager.Apply("frontend_hosting_oidc_enabled", []byte("true")); err != nil {
		t.Fatal(err)
	}
	if got := env.Do(t, "POST", "/v1/internal/hosting/oidc/exchange", api.HostingOIDCExchangeRequest{}, nil); got.Code != 401 {
		t.Fatal("anonymous exchange validation", got.Code, got.Body)
	}
	if got := env.Do(t, "DELETE", root+"/oidc-trusts/"+trust.ID, nil, nil); got.Code != 204 {
		t.Fatal(got.Code, got.Body)
	}
	if got := env.Do(t, "GET", root, nil, headers); got.Code != 401 {
		t.Fatal("revoked trust bearer accepted", got.Code, got.Body)
	}
}
