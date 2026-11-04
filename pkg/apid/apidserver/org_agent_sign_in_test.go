// adr: 20261101225122
package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func mintScopedMCPCode(t *testing.T, srv http.Handler, scopes ...string) string {
	t.Helper()
	body, _ := json.Marshal(api.CliAuthCodeRequest{Purpose: api.CliAuthPurposeMCP, Scopes: scopes})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/cli-auth/code", bytes.NewReader(body)))
	var code api.CliAuthCodeResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &code) != nil {
		t.Fatalf("mint = %d %s", rec.Code, rec.Body)
	}
	return code.Code
}

// A hosted MCP sign-in approved for an organization gets that
// organization's key, holding only what its agent policy allows, and
// /v1/account names the organization; a policy that denies agents refuses
// the organization at approval.
func TestHandlers_HostedSignInActsInTheChosenOrganization(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	const email = "org-agent@example.com"
	acct, err := store.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, acct.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetOrgAgentPolicy(ctx, state.OrgAgentPolicy{OrgID: org.ID, AgentsAllowed: true, AllowedScopes: []string{api.ScopeDeployWrite}}); err != nil {
		t.Fatal(err)
	}

	code := mintScopedMCPCode(t, srv, api.ScopeDeployWrite, api.ScopeSecretsRequest)
	cookie, csrf := apidserver.RenderCliAuthForTestAs(t, srv, code, email)
	if claim := apidserver.PostCliAuthWithOrgForTest(t, srv, code, email, cookie, csrf, org.ID); claim.Code != http.StatusFound {
		t.Fatalf("approve for the organization = %d %s", claim.Code, claim.Body)
	}
	exchangeBody, _ := json.Marshal(api.CliAuthExchangeRequest{Code: code})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/cli-auth/exchange", bytes.NewReader(exchangeBody)))
	var exchange api.CliAuthExchangeResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &exchange) != nil {
		t.Fatalf("exchange = %d %s", rec.Code, rec.Body)
	}
	key, err := store.APIKeyByHash(ctx, api.HashToken([]byte(exchange.Plaintext)))
	if err != nil {
		t.Fatal(err)
	}
	if key.OrgID != org.ID || key.Label != api.MCPLoginKeyLabel || slices.Contains(key.Scopes, api.ScopeSecretsRequest) || !slices.Contains(key.Scopes, api.ScopeDeployWrite) {
		t.Fatalf("org sign-in key = org %q label %q scopes %v; want the organization's mcp-login key without secrets:request", key.OrgID, key.Label, key.Scopes)
	}
	whoami := httptest.NewRequest(http.MethodGet, "/v1/account", nil)
	whoami.Header.Set("Authorization", "Bearer "+exchange.Plaintext)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, whoami)
	var account api.AccountResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &account) != nil || account.KeyOrg == nil || account.KeyOrg.Slug != "acme" {
		t.Fatalf("GET /v1/account = %d %s; want key_org acme", rec.Code, rec.Body)
	}

	if _, err := store.SetOrgAgentPolicy(ctx, state.OrgAgentPolicy{OrgID: org.ID, AllowedScopes: []string{}}); err != nil {
		t.Fatal(err)
	}
	refused := mintScopedMCPCode(t, srv)
	cookie, csrf = apidserver.RenderCliAuthForTestAs(t, srv, refused, email)
	claim := apidserver.PostCliAuthWithOrgForTest(t, srv, refused, email, cookie, csrf, org.ID)
	if claim.Code != http.StatusOK || !strings.Contains(claim.Body.String(), "agent policy") {
		t.Fatalf("approve under a policy denying agents = %d %s", claim.Code, claim.Body)
	}
}

// An organization agent key expires with the agent lifetime, and the
// organization's agent policy decides which capabilities one may hold.
func TestHandlers_OrgAgentKeysFollowTheOrganizationsPolicy(t *testing.T) {
	env, _ := apidserver.SetupChangePlan(t, api.PlanPro, "")
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+(*env.Key()))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Active-Org", "agent-team")
		(*env.H()).ServeHTTP(rec, r)
		return rec
	}
	if rec := call(http.MethodPost, "/v1/orgs", `{"slug":"agent-team","name":"Agent Team"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create org = %d %s", rec.Code, rec.Body)
	}
	rec := call(http.MethodPost, "/v1/orgs/agent-team/keys", `{"label":"ci-agent","scopes":["agent","deploy:write"]}`)
	var key api.APIKeyResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &key) != nil {
		t.Fatalf("org agent key = %d %s", rec.Code, rec.Body)
	}
	expires, err := time.Parse(time.RFC3339, key.ExpiresAt)
	if delta := time.Until(expires); err != nil || delta < api.AgentKeyLifetime-time.Hour || delta > api.AgentKeyLifetime+time.Hour {
		t.Fatalf("org agent key expires in %v, want %v", delta, api.AgentKeyLifetime)
	}

	store := *env.Store()
	org, err := store.OrgBySlug(t.Context(), "agent-team")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetOrgAgentPolicy(t.Context(), state.OrgAgentPolicy{OrgID: org.ID, AgentsAllowed: true, AllowedScopes: []string{}}); err != nil {
		t.Fatal(err)
	}
	if rec := call(http.MethodPost, "/v1/orgs/agent-team/keys", `{"label":"ci-agent-2","scopes":["agent","deploy:write"]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("agent key beyond the policy = %d %s, want 403", rec.Code, rec.Body)
	}
	if rec := call(http.MethodPost, "/v1/orgs/agent-team/keys", `{"label":"ci-reader","scopes":["agent","apps:read"]}`); rec.Code != http.StatusCreated {
		t.Fatalf("agent key within the policy = %d %s, want 201", rec.Code, rec.Body)
	}
}
