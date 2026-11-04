// adr: 20261029085359
package mcpmanagement

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func readAgentKeyResponse(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func agentKeyFixture(t *testing.T, keys map[string][]string) *remoteFixture {
	t.Helper()
	cli := newProvisionCLI()
	f := newRemoteFixtureWithOptions(t, true, HTTPOptions{Onboarding: "browser", AgentKeys: true, CLIRunner: func(string) LocalCLI { return cli.cli }})
	f.mu.Lock()
	f.keyScopes = map[string][]string{}
	for key, scopes := range keys {
		f.accounts[key] = "alice"
		f.keyScopes[key] = scopes
	}
	f.mu.Unlock()
	return f
}

// An agent key opens the server with the tools its scopes allow, across the
// account, and never the local-directory tools (ADR-20261029085359).
func TestAgentKeysOpenTheServerByTheirScopes(t *testing.T) {
	f := agentKeyFixture(t, map[string][]string{
		"fp_test_agent_writer": {api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite, api.ScopeSecretsRequest, api.ScopeStorageManage},
		"fp_test_agent_reader": {api.ScopeAgent, api.ScopeAppsRead},
	})
	writer, ctx := f.connect(tokenResponse{AccessToken: "fp_test_agent_writer"}, "2026-07-28")
	tools := listedTools(t, ctx, writer)
	for _, want := range []string{"gregale_list_apps", "gregale_request_secret", "gregale_plan_bucket", "gregale_plan_domain"} {
		if !tools[want] {
			t.Errorf("writer key lacks %s", want)
		}
	}
	for _, unwanted := range []string{"gregale_plan_postgres", "gregale_plan_local_deploy", "gregale_plan_env_change"} {
		if tools[unwanted] {
			t.Errorf("writer key lists %s", unwanted)
		}
	}
	reader, ctx := f.connect(tokenResponse{AccessToken: "fp_test_agent_reader"}, "2026-07-28")
	tools = listedTools(t, ctx, reader)
	if !tools["gregale_list_apps"] || tools["gregale_request_secret"] || tools["gregale_plan_domain"] {
		t.Fatalf("read-only agent key tools = %v", tools)
	}
}

// Only agent keys open the server: a deploy key, an admin key and an agent key
// that is also admin are refused with a pointer to agent keys; an unknown key
// gets the OAuth challenge.
func TestOnlyAgentKeysOpenTheServer(t *testing.T) {
	f := agentKeyFixture(t, map[string][]string{
		"fp_test_ci_deploy":    {api.ScopeAppsRead, api.ScopeDeployWrite},
		"fp_test_admin":        {api.ScopeAdmin},
		"fp_test_agent_admin":  {api.ScopeAgent, api.ScopeAdmin, api.ScopeAppsRead},
		"fp_test_agent_noread": {api.ScopeAgent, api.ScopeDeployWrite},
	})
	for _, key := range []string{"fp_test_ci_deploy", "fp_test_admin", "fp_test_agent_admin", "fp_test_agent_noread"} {
		response := f.request(http.MethodPost, "/mcp", strings.NewReader(`{}`), "application/json", key)
		page := readAgentKeyResponse(t, response)
		if response.StatusCode != http.StatusForbidden || !strings.Contains(page, "--scopes agent") || !strings.Contains(response.Header.Get("WWW-Authenticate"), "insufficient_scope") {
			t.Errorf("%s = %d %s", key, response.StatusCode, page)
		}
	}
	unknown := f.request(http.MethodPost, "/mcp", strings.NewReader(`{}`), "application/json", "fp_test_never_issued")
	_ = unknown.Body.Close()
	if unknown.StatusCode != http.StatusUnauthorized || !strings.Contains(unknown.Header.Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatalf("unknown key = %d", unknown.StatusCode)
	}
}

// A bridge that does not take agent keys keeps refusing every API key.
func TestAgentKeysNeedTheBridgeOption(t *testing.T) {
	f := newRemoteFixture(t, false)
	f.mu.Lock()
	f.keyScopes = map[string][]string{"fp_test_alice_remote_key": {api.ScopeAgent, api.ScopeAppsRead}}
	f.mu.Unlock()
	response := f.request(http.MethodPost, "/mcp", strings.NewReader(`{}`), "application/json", "fp_test_alice_remote_key")
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("agent key without AgentKeys = %d, want 401", response.StatusCode)
	}
}

// A key connection keeps nothing durable: unused, it is dropped like an idle
// grant's server, and the next request builds it again.
func TestAgentKeyConnectionsAreDroppedWhenIdle(t *testing.T) {
	f := agentKeyFixture(t, map[string][]string{"fp_test_agent_reader": {api.ScopeAgent, api.ScopeAppsRead}})
	service, err := NewHTTPService(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler(service.Handler())
	cs, ctx := f.connect(tokenResponse{AccessToken: "fp_test_agent_reader"}, "2026-07-28")
	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	r := service.remote
	r.mu.Lock()
	if len(r.keyConns) != 1 {
		r.mu.Unlock()
		t.Fatalf("key connections = %d, want 1", len(r.keyConns))
	}
	for _, c := range r.keyConns {
		if c.inflight != 0 || c.accountID != "alice" || strings.Contains(c.id, "fp_test") {
			t.Fatalf("connection %+v", c)
		}
		c.lastUsed = time.Now().Add(-idleServerTTL - time.Second)
	}
	r.pruneLocked()
	dropped := len(r.keyConns) == 0
	r.mu.Unlock()
	if !dropped {
		t.Fatal("an idle key connection was kept")
	}
	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatalf("a dropped key connection was not rebuilt: %v", err)
	}
}
