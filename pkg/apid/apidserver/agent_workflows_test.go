package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestAppAgentCredentialsCannotReachAnotherApp(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app := apidserver.MustSeedApp(t, e, "agent-app")
	apidserver.MustSeedApp(t, e, "other-agent-app")
	created := e.Do(t, http.MethodPost, "/v1/apps/agent-app/deploy-tokens", api.CreateDeployTokenRequest{Agent: true, Scopes: []string{api.ScopeDeployWrite, api.ScopeEnvWrite}}, nil)
	if created.Code != 201 {
		t.Fatalf("mint %d %s", created.Code, created.Body.String())
	}
	var token api.DeployTokenResponse
	if err := json.Unmarshal(created.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		var data bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&data).Encode(body)
		}
		r := httptest.NewRequest(method, path, &data)
		r.Header.Set("Authorization", "Bearer "+token.Plaintext)
		r.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		(*e.H()).ServeHTTP(out, r)
		return out
	}
	metadata := request(http.MethodGet, "/v1/account", nil)
	if metadata.Code != 200 {
		t.Fatalf("metadata %d %s", metadata.Code, metadata.Body.String())
	}
	var account api.AccountResponse
	if err := json.Unmarshal(metadata.Body.Bytes(), &account); err != nil {
		t.Fatal(err)
	}
	if account.KeyApp == nil || account.KeyApp.ID != app || account.KeyApp.Slug != "agent-app" || account.Email != "" || !slices.Contains(account.KeyScopes, api.ScopeAgent) {
		t.Fatalf("metadata scope: %+v", account)
	}
	for _, path := range []string{"/v1/apps", "/v1/keys", "/v1/billing/costs"} {
		if out := request(http.MethodGet, path, nil); out.Code != http.StatusForbidden {
			t.Errorf("global %s = %d", path, out.Code)
		}
	}
	if out := request(http.MethodGet, "/v1/apps/other-agent-app", nil); out.Code != http.StatusNotFound {
		t.Fatalf("cross-app read=%d", out.Code)
	}
	// A stage-scope write needs no approval; production configuration asks
	// like a release (agent approvals ADR amendment 2026-11-10).
	if out := request(http.MethodPut, "/v1/apps/agent-app/env/LOG_LEVEL?scope=staging", api.PutAppEnvRequest{Value: "debug"}); out.Code != 200 {
		t.Fatalf("own configuration %d %s", out.Code, out.Body.String())
	}
	if out := request(http.MethodPut, "/v1/apps/agent-app/env/LOG_LEVEL", api.PutAppEnvRequest{Value: "debug"}); out.Code != http.StatusPreconditionRequired {
		t.Fatalf("own production configuration = %d %s, want 428 approval_required", out.Code, out.Body.String())
	}
	if out := request(http.MethodPut, "/v1/apps/other-agent-app/env/LOG_LEVEL?scope=staging", api.PutAppEnvRequest{Value: "debug"}); out.Code != 404 {
		t.Fatalf("cross-app configuration=%d", out.Code)
	}
	if out := request(http.MethodPut, "/v1/apps/agent-app/secrets/TOKEN", api.PutAppSecretRequest{Value: "fake-value"}); out.Code != 403 {
		t.Fatalf("secret-write escalation=%d", out.Code)
	}
	bad := e.Do(t, http.MethodPost, "/v1/apps/agent-app/deploy-tokens", api.CreateDeployTokenRequest{Agent: true, Scopes: []string{api.ScopeAdmin}}, nil)
	if bad.Code != 400 {
		t.Fatalf("admin escalation=%d", bad.Code)
	}
}

func TestExtendedMCPBrowserCodesKeepScopeHashBinding(t *testing.T) {
	code, hash, err := apidserver.MintMCPAuthCodeForTest(api.CliAuthCodeRequest{Purpose: api.CliAuthPurposeMCP, Scopes: []string{api.ScopeDeployWrite, api.ScopeEnvWrite, api.ScopeUsageRead}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(code, "MC231-") {
		t.Fatalf("extended prefix: %s", code[:6])
	}
	normalized, ok := apidserver.NormalizeCliAuthCodeForTest(code)
	if !ok || !bytes.Equal(hash, normalized) {
		t.Fatal("extended code does not normalize to its minted hash")
	}
	changed := strings.Replace(code, "MC231-", "MC211-", 1)
	other, ok := apidserver.NormalizeCliAuthCodeForTest(changed)
	if !ok || bytes.Equal(hash, other) {
		t.Fatal("scope alteration did not change the code binding")
	}
	got := apidserver.MCPCodeScopesForTest(code)
	for _, scope := range []string{api.ScopeAppsRead, api.ScopeDeployWrite, api.ScopeEnvWrite, api.ScopeUsageRead} {
		if !slices.Contains(got, scope) {
			t.Errorf("missing %s in %v", scope, got)
		}
	}
	old := apidserver.MCPCodeScopesForTest("MCPW-" + strings.Repeat("ab", 32))
	if slices.Contains(old, api.ScopeEnvWrite) || slices.Contains(old, api.ScopeUsageRead) {
		t.Fatal("legacy consent silently gained scopes")
	}
}
