// adr: 20261030133644
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Connected agents are listed and revoked through the keys they use: the
// hosted MCP server names its key's client, an agent key cannot revoke other
// agents, and revoking one or all of them stops their keys.
func TestHandlers_ConnectedAgentsAreListedAndRevokedThroughTheirKeys(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	store, acct := *e.Store(), *e.Acct()
	bearer := func(key string) map[string]string { return map[string]string{"Authorization": "Bearer " + key} }
	hostedKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	hosted, err := store.CreateAPIKeyWithExpiryAndProvenance(context.Background(), acct.ID, hash, api.MCPLoginKeyLabel, []string{api.ScopeAppsRead}, &expires, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodPut, "/v1/agent-connections/self", api.AgentClientRequest{ClientName: "Claude", ClientSite: "claude.ai"}, bearer(hostedKey)); rec.Code != http.StatusNoContent {
		t.Fatalf("name client: code=%d body=%s", rec.Code, rec.Body)
	}
	rec := e.Do(t, http.MethodPost, "/v1/keys", api.CreateKeyRequest{Label: "ci-agent", Scopes: []string{api.ScopeAgent}}, nil)
	var agent api.APIKeyResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &agent) != nil {
		t.Fatalf("POST /v1/keys: code=%d body=%s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodDelete, "/v1/agent-connections/"+hosted.ID, nil, bearer(agent.Plaintext)); rec.Code != http.StatusForbidden {
		t.Fatalf("agent key revoking another agent: code=%d, want 403", rec.Code)
	}

	rec = e.Do(t, http.MethodGet, "/v1/agent-connections", nil, nil)
	var list api.AgentConnectionsResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Items) != 2 {
		t.Fatalf("GET /v1/agent-connections: code=%d body=%s", rec.Code, rec.Body)
	}
	for _, item := range list.Items {
		if item.ID == hosted.ID && (item.Kind != api.AgentConnectionHostedMCP || item.ClientName != "Claude" || item.ClientSite != "claude.ai") {
			t.Fatalf("hosted connection = %+v", item)
		}
	}

	if rec := e.Do(t, http.MethodDelete, "/v1/agent-connections/"+hosted.ID, nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: code=%d body=%s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/account", nil, bearer(hostedKey)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked hosted key: code=%d, want 401", rec.Code)
	}
	rec = e.Do(t, http.MethodPost, "/v1/agent-connections/revoke_all", nil, nil)
	var all api.AgentConnectionsRevokeAllResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &all) != nil || all.Revoked != 1 {
		t.Fatalf("revoke all: code=%d body=%s, want 1 revoked", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/account", nil, bearer(agent.Plaintext)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked agent key: code=%d, want 401", rec.Code)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/account", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("owner key after revoking agents: code=%d, want 200", rec.Code)
	}
}
