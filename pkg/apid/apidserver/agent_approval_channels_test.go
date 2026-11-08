// adr: 20261107140000
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A person chooses which of their notification channels also get their
// agents' approval requests; an agent key cannot change them.
func TestHandlers_AgentApprovalChannels(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "channels-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateNotificationChannel(ctx, state.NotificationChannel{AccountID: acct.ID, Name: "ops", Kind: "email", Email: "ops@example.com"}, 10); err != nil {
		t.Fatal(err)
	}
	mint := func(label string, scopes ...string) string {
		plaintext, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateAPIKey(ctx, acct.ID, hash, label, scopes); err != nil {
			t.Fatal(err)
		}
		return plaintext
	}
	agent, admin := mint("agent", api.ScopeAgent, api.ScopeAppsRead), mint("admin", api.ScopeAdmin)
	call := func(key, method, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/v1/agent-approvals/channels", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(rec, r)
		return rec
	}
	channels := func(rec *httptest.ResponseRecorder) []string {
		t.Helper()
		var out api.AgentApprovalChannelsResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("response = %d %s", rec.Code, rec.Body)
		}
		return out.Channels
	}

	if got := channels(call(admin, http.MethodGet, "")); len(got) != 0 {
		t.Fatalf("channels before any were chosen = %v", got)
	}
	if rec := call(agent, http.MethodPut, `{"channels":[]}`); rec.Code != http.StatusForbidden {
		t.Fatalf("an agent key clearing its approval channels = %d %s; want 403", rec.Code, rec.Body)
	}
	if rec := call(admin, http.MethodPut, `{"channels":["nope"]}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "nope") {
		t.Fatalf("an unknown channel = %d %s; want 400 naming it", rec.Code, rec.Body)
	}
	if got := channels(call(admin, http.MethodPut, `{"channels":["ops"]}`)); len(got) != 1 || got[0] != "ops" {
		t.Fatalf("set channels = %v; want [ops]", got)
	}
	if got := channels(call(agent, http.MethodGet, "")); len(got) != 1 || got[0] != "ops" {
		t.Fatalf("an agent reading the channels = %v; want [ops]", got)
	}
	if got := channels(call(admin, http.MethodPut, `{"channels":[]}`)); len(got) != 0 {
		t.Fatalf("cleared channels = %v", got)
	}
}
