package ciapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/apid/ciapi"
	"github.com/onebox-faas/faas/pkg/ci"
)

func TestCIAutomationPullRequestPolicyScopeAndDormantCompute(t *testing.T) {
	f := newAutomationRecoveryAPI(t)
	store := ci.NewPostgresStore(f.pool)
	bindings, err := store.GitHubBindings(t.Context(), f.account)
	if err != nil || len(bindings) != 1 {
		t.Fatal("binding fixture missing")
	}
	binding := bindings[0]
	req := binding.GitHubBindingRequest
	req.PullRequestBranches = []string{"main", "release"}
	binding, err = store.PutGitHubBinding(t.Context(), f.account, binding.Name, req)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := store.ExecutionProfiles(t.Context(), f.account)
	if err != nil || len(profiles) != 1 {
		t.Fatal("profile fixture missing")
	}
	profile, err := ci.SaveExecutionProfile(t.Context(), f.handler.Config, store, f.handler.VerifyGitHub, f.account, "pull-requests",
		ci.ExecutionProfileRequest{RevisionID: binding.RevisionID, BindingID: binding.ID, Templates: profiles[0].Templates})
	if err != nil {
		t.Fatal(err)
	}
	policyReq := ci.AutomationRequest{ProfileID: profile.ID, PullRequestBranches: []string{"release", "main"}}
	var first ci.AutomationPolicy
	for i := range 2 {
		response := f.call(t, ciapi.Handler.PutAutomationPolicy, f.account, f.binding, "", "", policyReq)
		var policy ci.AutomationPolicy
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &policy) != nil || len(policy.PushBranches) != 0 || policy.PullRequestBranches[0] != "main" || policy.LatestOnly {
			t.Fatalf("PR-only policy setup: %d %s", response.Code, response.Body)
		}
		if i == 0 {
			first = policy
		} else if policy.Generation != first.Generation {
			t.Fatal("idempotent PR save changed generation")
		}
	}
	if w := f.call(t, ciapi.Handler.PutAutomationPolicy, f.other, f.binding, "", "", policyReq); w.Code != http.StatusNotFound {
		t.Fatal("foreign account saved PR policy")
	}
	invalid := policyReq
	invalid.PullRequestBranches = []string{"unbound"}
	if w := f.call(t, ciapi.Handler.PutAutomationPolicy, f.account, f.binding, "", "", invalid); w.Code == http.StatusOK {
		t.Fatal("unbound PR target became automatic authority")
	}
	var executions, runs, observations int
	if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM ci_executions),(SELECT count(*) FROM job_runs),(SELECT count(*) FROM ci_automation_pull_requests)").Scan(&executions, &runs, &observations); err != nil || executions != 0 || runs != 0 || observations != 0 {
		t.Fatal("policy setup bypassed dormant compute gates")
	}
	// Reads preserve PR identity while dispatch/worker/transport remain off.
	repo := map[string]any{"id": binding.RepositoryID, "full_name": binding.Repository}
	raw, _ := json.Marshal(map[string]any{"action": "opened", "number": 9, "repository": repo, "installation": map[string]any{"id": binding.InstallationID},
		"pull_request": map[string]any{"state": "open", "draft": false, "head": map[string]any{"sha": strings.Repeat("b", 40), "repo": repo}, "base": map[string]any{"ref": "main", "repo": repo}}})
	if err := ci.RecordVerifiedGitHubDelivery(t.Context(), f.handler.Config, store, uuid.NewString(), "pull_request", raw); err != nil {
		t.Fatal(err)
	}
	w := f.call(t, ciapi.Handler.ListAutomationEvents, f.account, f.binding, "", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"kind":"pull_request"`) || !strings.Contains(w.Body.String(), `"pr_number":9`) || strings.Contains(w.Body.String(), "lease_token") || strings.Contains(w.Body.String(), "private synthetic command") {
		t.Fatalf("PR history lost scope or exposed authority: %d %s", w.Code, w.Body)
	}
	if w := f.call(t, ciapi.Handler.ListAutomationEvents, f.other, f.binding, "", "", nil); w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"pr_number":9`) {
		t.Fatal("PR history crossed account boundary")
	}
}
