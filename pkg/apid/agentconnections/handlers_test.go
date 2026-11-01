// adr: 20261030133644
package agentconnections_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agentconnections"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

type fixture struct {
	t       *testing.T
	store   *state.MemStore
	acct    state.Account
	deps    agentconnections.Deps
	revoked []string
	audits  []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, store: state.NewMemStore()}
	acct, err := f.store.CreateAccount(context.Background(), "agents@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.acct = acct
	f.deps = agentconnections.Deps{
		Store: f.store,
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Prefixes: func(_ context.Context, keys []state.APIKey) map[string]string {
			out := map[string]string{}
			for _, k := range keys {
				out[k.ID] = "fp_live_" + k.ID[:4]
			}
			return out
		},
		Revoke: func(_ *http.Request, acct state.Account, id string) error {
			f.revoked = append(f.revoked, id)
			_, err := f.store.MarkAPIKeyRevoked(context.Background(), acct.ID, id)
			return err
		},
		Audit: func(_ context.Context, kind, _ string, _ map[string]any) { f.audits = append(f.audits, kind) },
	}
	return f
}

func (f *fixture) key(label string, scopes []string, expires time.Duration) state.APIKey {
	f.t.Helper()
	_, hash, err := api.GenerateAPIKey()
	if err != nil {
		f.t.Fatal(err)
	}
	at := time.Now().Add(expires)
	k, err := f.store.CreateAPIKeyWithExpiryAndProvenance(context.Background(), f.acct.ID, hash, label, scopes, &at, "", "", nil)
	if err != nil {
		f.t.Fatal(err)
	}
	return k
}

func (f *fixture) call(h func(http.ResponseWriter, *http.Request, state.Account), method, path string, key *state.APIKey, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), f.acct, key, nil))
	if i := strings.LastIndex(path, "/"); i >= 0 {
		req.SetPathValue("id", path[i+1:])
	}
	w := httptest.NewRecorder()
	h(w, req, f.acct)
	return w
}

// The list holds hosted MCP sign-ins and agent keys that still work, named
// by the client that signed in; other keys stay on the API keys page.
func TestListShowsUsableAgentKeysWithTheirClients(t *testing.T) {
	f := newFixture(t)
	hosted := f.key(api.MCPLoginKeyLabel, []string{api.ScopeAppsRead}, time.Hour)
	agent := f.key("ci-agent", []string{api.ScopeAgent, api.ScopeAppsRead}, 90*24*time.Hour)
	f.key("deploy", []string{api.ScopeAdmin}, time.Hour)
	f.key(api.MCPLoginKeyLabel, []string{api.ScopeAdmin}, time.Hour)
	f.key("old-agent", []string{api.ScopeAgent}, -time.Minute)
	gone := f.key(api.MCPLoginKeyLabel, []string{api.ScopeAppsRead}, time.Hour)
	if _, err := f.store.MarkAPIKeyRevoked(context.Background(), f.acct.ID, gone.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetAPIKeyAgentClient(context.Background(), f.acct.ID, hosted.ID, state.AgentClient{Name: "Claude", Site: "claude.ai"}); err != nil {
		t.Fatal(err)
	}

	w := f.call(agentconnections.List(f.deps), http.MethodGet, "/v1/agent-connections", nil, "")
	var got api.AgentConnectionsResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	kinds := map[string]api.AgentConnectionResponse{}
	for _, item := range got.Items {
		kinds[item.ID] = item
	}
	if len(got.Items) != 2 || kinds[hosted.ID].Kind != api.AgentConnectionHostedMCP || kinds[agent.ID].Kind != api.AgentConnectionAgentKey {
		t.Fatalf("items = %+v, want the hosted sign-in and the agent key", got.Items)
	}
	if c := kinds[hosted.ID]; c.ClientName != "Claude" || c.ClientSite != "claude.ai" || c.Prefix == "" || c.ExpiresAt == "" {
		t.Fatalf("hosted item = %+v", c)
	}
	if c := kinds[agent.ID]; c.ClientName != "" || c.Label != "ci-agent" {
		t.Fatalf("agent key item = %+v", c)
	}
}

func TestRevokeTakesOnlyAgentConnections(t *testing.T) {
	f := newFixture(t)
	hosted := f.key(api.MCPLoginKeyLabel, []string{api.ScopeAppsRead}, time.Hour)
	admin := f.key("deploy", []string{api.ScopeAdmin}, time.Hour)
	for _, id := range []string{admin.ID, "not-a-key"} {
		if w := f.call(agentconnections.Revoke(f.deps), http.MethodDelete, "/v1/agent-connections/"+id, nil, ""); w.Code != http.StatusNotFound {
			t.Fatalf("revoke %s = %d %s, want 404", id, w.Code, w.Body)
		}
	}
	for range 2 {
		if w := f.call(agentconnections.Revoke(f.deps), http.MethodDelete, "/v1/agent-connections/"+hosted.ID, nil, ""); w.Code != http.StatusNoContent {
			t.Fatalf("revoke = %d %s, want 204 every time", w.Code, w.Body)
		}
	}
	if len(f.revoked) != 2 || f.revoked[0] != hosted.ID {
		t.Fatalf("revoked %v, want the hosted key", f.revoked)
	}
}

func TestRevokeAllCutsOffEveryAgent(t *testing.T) {
	f := newFixture(t)
	hosted := f.key(api.MCPLoginKeyLabel, []string{api.ScopeAppsRead}, time.Hour)
	agent := f.key("ci-agent", []string{api.ScopeAgent}, time.Hour)
	admin := f.key("deploy", []string{api.ScopeAdmin}, time.Hour)

	w := f.call(agentconnections.RevokeAll(f.deps), http.MethodPost, "/v1/agent-connections/revoke_all", nil, "")
	var got api.AgentConnectionsRevokeAllResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Revoked != 2 {
		t.Fatalf("revoke all = %d %s, want 2 revoked", w.Code, w.Body)
	}
	slices.Sort(f.revoked)
	want := []string{hosted.ID, agent.ID}
	slices.Sort(want)
	if !slices.Equal(f.revoked, want) || slices.Contains(f.revoked, admin.ID) || !slices.Equal(f.audits, []string{"agent_connections.revoke_all"}) {
		t.Fatalf("revoked %v audits %v", f.revoked, f.audits)
	}
	if w := f.call(agentconnections.RevokeAll(f.deps), http.MethodPost, "/v1/agent-connections/revoke_all", nil, ""); !strings.Contains(w.Body.String(), `"revoked":0`) || len(f.audits) != 1 {
		t.Fatalf("second revoke all = %s audits %v", w.Body, f.audits)
	}
}

func TestHostedKeysDisconnectOnlyThemselves(t *testing.T) {
	f := newFixture(t)
	hosted := f.key(api.MCPLoginKeyLabel, []string{api.ScopeAppsRead}, time.Hour)
	agent := f.key("ci-agent", []string{api.ScopeAgent}, time.Hour)
	disconnect := agentconnections.Disconnect(f.deps)
	if w := f.call(disconnect, http.MethodDelete, "/v1/agent-connections/self", &agent, ""); w.Code != http.StatusForbidden {
		t.Fatalf("agent key disconnect = %d, want 403", w.Code)
	}
	if w := f.call(disconnect, http.MethodDelete, "/v1/agent-connections/self", &hosted, ""); w.Code != http.StatusNoContent || !slices.Equal(f.revoked, []string{hosted.ID}) {
		t.Fatalf("hosted disconnect = %d, revoked %v", w.Code, f.revoked)
	}
}

// Only the hosted MCP key itself names its client, once, with a printable
// name and a host name.
func TestHostedKeysNameTheirClientOnce(t *testing.T) {
	f := newFixture(t)
	hosted := f.key(api.MCPLoginKeyLabel, []string{api.ScopeAppsRead}, time.Hour)
	agent := f.key("ci-agent", []string{api.ScopeAgent}, time.Hour)
	name := agentconnections.Name(f.deps)
	if w := f.call(name, http.MethodPut, "/v1/agent-connections/self", &agent, `{"client_name":"Spoof"}`); w.Code != http.StatusForbidden {
		t.Fatalf("agent key naming = %d, want 403", w.Code)
	}
	for _, body := range []string{`{"client_name":""}`, `{"client_name":"Bad\u001b[31m"}`, `{"client_name":"` + strings.Repeat("x", 81) + `"}`, `{"client_name":"Claude","client_site":"https://claude.ai"}`, `{"client_name":"Claude","extra":1}`} {
		if w := f.call(name, http.MethodPut, "/v1/agent-connections/self", &hosted, body); w.Code != http.StatusBadRequest {
			t.Fatalf("naming with %s = %d, want 400", body, w.Code)
		}
	}
	for _, body := range []string{`{"client_name":"Claude","client_site":"claude.ai"}`, `{"client_name":"Renamed"}`} {
		if w := f.call(name, http.MethodPut, "/v1/agent-connections/self", &hosted, body); w.Code != http.StatusNoContent {
			t.Fatalf("naming with %s = %d %s, want 204", body, w.Code, w.Body)
		}
	}
	clients, err := f.store.APIKeyAgentClients(context.Background(), f.acct.ID)
	if err != nil || clients[hosted.ID] != (state.AgentClient{Name: "Claude", Site: "claude.ai"}) {
		t.Fatalf("clients = %+v, %v; want the first name kept", clients, err)
	}
}
