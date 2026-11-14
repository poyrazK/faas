// adr: 20261107220000
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// An agent key's write requests are recorded once the handler answers, with
// the approval a request went through on; a request the approval gate
// answers is not. The person who connected the agent and the agent itself
// read the log; another account and another agent cannot.
func TestHandlers_AgentActivityLog(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	const email = "activity-owner@example.com"
	acct, err := store.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := store.CreateAccount(ctx, "activity-stranger@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "doomed-svc", Type: state.AppTypeApp}); err != nil {
		t.Fatal(err)
	}
	mint := func(account, label string, scopes ...string) (string, state.APIKey) {
		t.Helper()
		plaintext, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		key, err := store.CreateAPIKey(ctx, account, hash, label, scopes)
		if err != nil {
			t.Fatalf("CreateAPIKey %s: %v", label, err)
		}
		return plaintext, key
	}
	agent, agentKey := mint(acct.ID, "activity-agent", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite)
	otherAgent, _ := mint(acct.ID, "other-agent", api.ScopeAgent, api.ScopeAppsRead)
	owner, _ := mint(acct.ID, "owner-admin", api.ScopeAdmin)
	outsider, _ := mint(stranger.ID, "outsider-admin", api.ScopeAdmin)
	call := func(key, method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(""))
		r.Header.Set("Authorization", "Bearer "+key)
		srv.ServeHTTP(rec, r)
		return rec
	}
	read := func(key, path string) (int, api.AgentActivityResponse) {
		t.Helper()
		rec := call(key, http.MethodGet, path)
		var out api.AgentActivityResponse
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode %s: %v %s", path, err, rec.Body)
			}
		}
		return rec.Code, out
	}

	// Gated: answered 428 by the approval gate, so not recorded.
	rec := call(agent, http.MethodDelete, "/v1/apps/doomed-svc")
	var problem api.Problem
	if rec.Code != http.StatusPreconditionRequired || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.AgentApproval == nil {
		t.Fatalf("delete before approval = %d %s; want 428", rec.Code, rec.Body)
	}
	approvalID := problem.AgentApproval.ID
	_, cookie, form := apidserver.RenderAgentApprovalForTestAs(t, srv, approvalID, email)
	if approved := apidserver.DecideAgentApprovalForTest(t, srv, approvalID, cookie, form, "approve"); approved.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", approved.Code, approved.Body)
	}
	deleted := call(agent, http.MethodDelete, "/v1/apps/doomed-svc")
	if deleted.Code >= 300 {
		t.Fatalf("approved delete = %d %s", deleted.Code, deleted.Body)
	}
	// Reads are not recorded.
	call(agent, http.MethodGet, "/v1/apps")

	// Recording is off the request path; wait for the row.
	var self api.AgentActivityResponse
	deadline := time.Now().Add(5 * time.Second)
	for {
		var code int
		code, self = read(agent, "/v1/agent-connections/self/activity")
		if code != http.StatusOK {
			t.Fatalf("agent reads its own activity = %d", code)
		}
		if len(self.Activity) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(self.Activity) != 1 {
		t.Fatalf("activity = %+v; want only the approved delete", self.Activity)
	}
	if got := self.Activity[0]; got.Method != http.MethodDelete || got.Path != "/v1/apps/doomed-svc" || got.Status != deleted.Code || got.ApprovalID != approvalID {
		t.Fatalf("row = %+v; want DELETE /v1/apps/doomed-svc %d via %s", got, deleted.Code, approvalID)
	}

	if code, out := read(owner, "/v1/agent-connections/"+agentKey.ID+"/activity"); code != http.StatusOK || len(out.Activity) != 1 {
		t.Fatalf("connector reads the agent's activity = %d %+v", code, out)
	}
	if code, _ := read(outsider, "/v1/agent-connections/"+agentKey.ID+"/activity"); code != http.StatusNotFound {
		t.Fatalf("another account reads the agent's activity = %d; want 404", code)
	}
	if code, _ := read(otherAgent, "/v1/agent-connections/"+agentKey.ID+"/activity"); code != http.StatusNotFound {
		t.Fatalf("another agent reads the agent's activity = %d; want 404", code)
	}
	if code, _ := read(owner, "/v1/agent-connections/self/activity"); code != http.StatusForbidden {
		t.Fatalf("a person's key reads self activity = %d; want 403", code)
	}
	if code, _ := read(owner, "/v1/agent-connections/"+agentKey.ID+"/activity?limit=0"); code != http.StatusBadRequest {
		t.Fatalf("limit=0 = %d; want 400", code)
	}
}
