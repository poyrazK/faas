// adr: 20261104140058
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

// An agent key's delete or production request waits for a person: apid
// answers 428 with a review link, only a signed-in member who may decide can
// approve it, and the approval allows the identical request exactly once. A
// preview, a key that chose no approvals and an ordinary key are not gated.
func TestHandlers_AgentRiskyRequestsNeedAPersonsApproval(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	const email = "approver@example.com"
	acct, err := store.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAccount(ctx, "stranger-approvals@example.com", api.PlanPro); err != nil {
		t.Fatal(err)
	}
	for _, app := range []state.App{
		{AccountID: acct.ID, Slug: "prod-svc", Type: state.AppTypeApp},
		{AccountID: acct.ID, Slug: "keep-svc", Type: state.AppTypeApp},
		{AccountID: acct.ID, Slug: "prod-svc-pr-1", Type: state.AppTypeApp, PreviewOfSlug: "prod-svc", PreviewPrNumber: 1},
	} {
		if _, err := store.CreateApp(ctx, app); err != nil {
			t.Fatalf("CreateApp %s: %v", app.Slug, err)
		}
	}
	mint := func(label string, scopes ...string) (string, state.APIKey) {
		plaintext, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		key, err := store.CreateAPIKey(ctx, acct.ID, hash, label, scopes)
		if err != nil {
			t.Fatalf("CreateAPIKey %s: %v", label, err)
		}
		return plaintext, key
	}
	agent, _ := mint("ci-agent", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite)
	call := func(key, method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(""))
		r.Header.Set("Authorization", "Bearer "+key)
		srv.ServeHTTP(rec, r)
		return rec
	}
	pending := func(rec *httptest.ResponseRecorder) api.AgentApprovalPendingInfo {
		t.Helper()
		var problem api.Problem
		if rec.Code != http.StatusPreconditionRequired || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != api.CodeApprovalRequired || problem.AgentApproval == nil {
			t.Fatalf("response = %d %s; want 428 approval_required", rec.Code, rec.Body)
		}
		if !strings.Contains(problem.AgentApproval.ReviewURL, "/cli-auth?agent_approval="+problem.AgentApproval.ID) {
			t.Fatalf("review link = %q", problem.AgentApproval.ReviewURL)
		}
		return *problem.AgentApproval
	}

	first := pending(call(agent, http.MethodDelete, "/v1/apps/prod-svc"))
	if first.Category != api.AgentApprovalDelete {
		t.Fatalf("category = %q, want delete", first.Category)
	}
	if again := pending(call(agent, http.MethodDelete, "/v1/apps/prod-svc")); again.ID != first.ID {
		t.Fatalf("a repeated request made a new approval %s, want %s", again.ID, first.ID)
	}
	var polled api.AgentApprovalResponse
	if rec := call(agent, http.MethodGet, "/v1/agent-approvals/"+first.ID); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &polled) != nil || polled.Status != api.AgentApprovalPending || polled.Agent != "ci-agent" {
		t.Fatalf("agent polls its request = %d %s", rec.Code, rec.Body)
	}

	// No API key reaches the decision: without a dashboard session the
	// review page sends the caller to sign in.
	post := httptest.NewRequest(http.MethodPost, "/cli-auth", strings.NewReader("agent_approval="+first.ID+"&decision=approve"))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Authorization", "Bearer "+agent)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, post)
	if rec.Code == http.StatusOK {
		t.Fatalf("an agent posting the decision with its key = %d %s", rec.Code, rec.Body)
	}
	if page, cookie, _ := apidserver.RenderAgentApprovalForTestAs(t, srv, first.ID, "stranger-approvals@example.com"); cookie != "" || !strings.Contains(page.Body.String(), "unavailable") {
		t.Fatalf("another account's review page = %d, cookie %q: %s", page.Code, cookie, page.Body)
	}

	page, cookie, form := apidserver.RenderAgentApprovalForTestAs(t, srv, first.ID, email)
	if page.Code != http.StatusOK || form == "" || !strings.Contains(page.Body.String(), "DELETE /v1/apps/prod-svc") {
		t.Fatalf("review page = %d %s", page.Code, page.Body)
	}
	if denied := apidserver.DecideAgentApprovalForTest(t, srv, first.ID, cookie, form, "deny"); denied.Code != http.StatusOK || !strings.Contains(denied.Body.String(), "You denied") {
		t.Fatalf("deny = %d %s", denied.Code, denied.Body)
	}
	second := pending(call(agent, http.MethodDelete, "/v1/apps/prod-svc"))
	if second.ID == first.ID {
		t.Fatal("a denied request was reused")
	}
	_, cookie, form = apidserver.RenderAgentApprovalForTestAs(t, srv, second.ID, email)
	if approved := apidserver.DecideAgentApprovalForTest(t, srv, second.ID, cookie, form, "approve"); approved.Code != http.StatusOK || !strings.Contains(approved.Body.String(), "You approved") {
		t.Fatalf("approve = %d %s", approved.Code, approved.Body)
	}
	if rec := call(agent, http.MethodDelete, "/v1/apps/prod-svc"); rec.Code == http.StatusPreconditionRequired || rec.Code >= 500 {
		t.Fatalf("the approved request = %d %s; want it to reach the handler", rec.Code, rec.Body)
	}
	if third := pending(call(agent, http.MethodDelete, "/v1/apps/prod-svc")); third.ID == second.ID {
		t.Fatal("an approval allowed a second request")
	}

	pending(call(agent, http.MethodPost, "/v1/apps/keep-svc/restart"))
	if rec := call(agent, http.MethodPost, "/v1/apps/prod-svc-pr-1/restart"); rec.Code == http.StatusPreconditionRequired {
		t.Fatalf("restarting a preview = %d %s; previews need no approval", rec.Code, rec.Body)
	}

	free, freeKey := mint("free-agent", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite)
	if err := store.SetAPIKeyApprovalCategories(ctx, freeKey.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	if rec := call(free, http.MethodPost, "/v1/apps/keep-svc/restart"); rec.Code == http.StatusPreconditionRequired {
		t.Fatalf("a key that chose no approvals = %d %s", rec.Code, rec.Body)
	}
	admin, _ := mint("admin", api.ScopeAdmin)
	if rec := call(admin, http.MethodPost, "/v1/apps/keep-svc/restart"); rec.Code == http.StatusPreconditionRequired {
		t.Fatalf("an ordinary key = %d %s", rec.Code, rec.Body)
	}

	var listed api.AgentApprovalsResponse
	if rec := call(admin, http.MethodGet, "/v1/agent-approvals"); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &listed) != nil || len(listed.Items) == 0 {
		t.Fatalf("a person's pending list = %d %s", rec.Code, rec.Body)
	}
}

// Key create records the approval categories an agent key chose; they apply
// only to agent keys.
func TestHandlers_AgentKeyChoosesItsApprovals(t *testing.T) {
	env, _ := apidserver.SetupChangePlan(t, api.PlanPro, "")
	call := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/keys", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+*env.Key())
		r.Header.Set("Content-Type", "application/json")
		(*env.H()).ServeHTTP(rec, r)
		return rec
	}
	for body, want := range map[string]int{
		`{"label":"ci","scopes":["deploy:write"],"approval_categories":["delete"]}`: http.StatusBadRequest,
		`{"label":"odd","scopes":["agent"],"approval_categories":["everything"]}`:   http.StatusBadRequest,
	} {
		if rec := call(body); rec.Code != want {
			t.Fatalf("POST /v1/keys %s = %d %s, want %d", body, rec.Code, rec.Body, want)
		}
	}
	rec := call(`{"label":"agent","scopes":["agent"],"approval_categories":["billing","delete","delete"]}`)
	var created api.APIKeyResponse
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &created) != nil || strings.Join(created.ApprovalCategories, ",") != "delete,billing" {
		t.Fatalf("agent key with approvals = %d %s", rec.Code, rec.Body)
	}
	rec = call(`{"label":"alone","scopes":["agent"],"approval_categories":[]}`)
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &created) != nil || created.ApprovalCategories == nil || len(created.ApprovalCategories) != 0 {
		t.Fatalf("agent key with no approvals = %d %s", rec.Code, rec.Body)
	}
}
