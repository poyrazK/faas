// adr: 20261110182236
package apidserver_test

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// An agent's Gregale-Agent-Reason is sanitised and shown on its approval
// request (API and review page) and on its activity rows. The approval keeps
// the reason of the request that opened it; a repeat with another reason
// does not replace it, and the reason never changes what is approved.
func TestHandlers_AgentRequestReason(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	const email = "reason-owner@example.com"
	acct, err := store.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"reason-svc", "admin-svc"} {
		if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp}); err != nil {
			t.Fatal(err)
		}
	}
	mint := func(label string, scopes ...string) string {
		t.Helper()
		plaintext, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateAPIKey(ctx, acct.ID, hash, label, scopes); err != nil {
			t.Fatalf("CreateAPIKey %s: %v", label, err)
		}
		return plaintext
	}
	agent := mint("reason-agent", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite)
	call := func(key, method, path, reason string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(""))
		r.Header.Set("Authorization", "Bearer "+key)
		if reason != "" {
			r.Header.Set(api.AgentReasonHeader, reason)
		}
		srv.ServeHTTP(rec, r)
		return rec
	}

	const want = `Remove the duplicate <shop> service`
	rec := call(agent, http.MethodDelete, "/v1/apps/reason-svc", "  Remove the duplicate\r\n<shop>\x00 service ")
	var problem api.Problem
	if rec.Code != http.StatusPreconditionRequired || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.AgentApproval == nil {
		t.Fatalf("delete = %d %s; want 428", rec.Code, rec.Body)
	}
	id := problem.AgentApproval.ID
	// Repeating with another reason reuses the request and keeps its reason.
	if again := call(agent, http.MethodDelete, "/v1/apps/reason-svc", "something else"); again.Code != http.StatusPreconditionRequired {
		t.Fatalf("repeat = %d %s; want 428", again.Code, again.Body)
	}
	var got api.AgentApprovalResponse
	if rec := call(agent, http.MethodGet, "/v1/agent-approvals/"+id, ""); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("get approval = %d %s", rec.Code, rec.Body)
	}
	if got.Reason != want {
		t.Fatalf("approval reason = %q; want %q", got.Reason, want)
	}

	page, cookie, form := apidserver.RenderAgentApprovalForTestAs(t, srv, id, email)
	body := page.Body.String()
	if !strings.Contains(body, html.EscapeString(want)) || strings.Contains(body, "<shop>") {
		t.Fatalf("review page does not show the escaped reason:\n%s", body)
	}
	if approved := apidserver.DecideAgentApprovalForTest(t, srv, id, cookie, form, "approve"); approved.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", approved.Code, approved.Body)
	}
	if deleted := call(agent, http.MethodDelete, "/v1/apps/reason-svc", "approved, deleting now"); deleted.Code >= 300 {
		t.Fatalf("approved delete = %d %s", deleted.Code, deleted.Body)
	}

	var activity api.AgentActivityResponse
	deadline := time.Now().Add(5 * time.Second)
	for len(activity.Activity) == 0 && time.Now().Before(deadline) {
		rec := call(agent, http.MethodGet, "/v1/agent-connections/self/activity", "")
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &activity) != nil {
			t.Fatalf("activity = %d %s", rec.Code, rec.Body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(activity.Activity) != 1 || activity.Activity[0].Reason != "approved, deleting now" || activity.Activity[0].ApprovalID != id {
		t.Fatalf("activity = %+v; want the delete with its own reason", activity.Activity)
	}

	// A person's key is not an agent: its header is ignored, so nothing
	// stores it (and its writes are not in any agent's activity log).
	admin := mint("reason-admin", api.ScopeAdmin)
	if rec := call(admin, http.MethodDelete, "/v1/apps/admin-svc", "ignored"); rec.Code >= 300 {
		t.Fatalf("admin delete = %d %s", rec.Code, rec.Body)
	}
}
