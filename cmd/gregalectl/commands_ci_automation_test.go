// ADR-1656: automation configuration requires an explicit profile and branches.
package main

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/ci"
)

func TestCIAutomationRequestsRequireExactScope(t *testing.T) {
	binding := uuid.NewString()
	req := ci.AutomationRequest{ProfileID: uuid.NewString(), PushBranches: []string{"release", "main"}}
	method, path, body, err := ciAutomationRequest("put", binding, req)
	if err != nil || method != http.MethodPut || path != "/v1/internal/ci/github/automation/"+binding || body.(ci.AutomationRequest).PushBranches[0] != "main" {
		t.Fatal("automation policy lost explicit immutable scope")
	}
	for _, branches := range [][]string{nil, {"*"}, {"main", "main"}} {
		req.PushBranches = branches
		if _, _, _, err := ciAutomationRequest("put", binding, req); err == nil {
			t.Fatal("automation accepted implicit or duplicate branches")
		}
	}
	if _, _, _, err := ciAutomationRequest("events", "../outside", req); err == nil {
		t.Fatal("unsafe automation lookup accepted")
	}
	if method, path, _, err := ciAutomationRequest("events", binding, req); err != nil || method != http.MethodGet || path != "/v1/internal/ci/github/automation/"+binding+"/events" {
		t.Fatal("automation history request malformed")
	}
	if method, _, _, err := ciAutomationRequest("disable", binding, req); err != nil || method != http.MethodDelete {
		t.Fatal("automation disable request malformed")
	}
	req.PushBranches, req.LatestOnly = []string{"main"}, true
	_, _, body, err = ciAutomationRequest("put", binding, req)
	if err != nil || !body.(ci.AutomationRequest).LatestOnly {
		t.Fatal("explicit latest-only policy was lost")
	}
	req.PushBranches, req.PullRequestBranches, req.LatestOnly = nil, []string{"release", "main"}, false
	_, _, body, err = ciAutomationRequest("put", binding, req)
	if err != nil || body.(ci.AutomationRequest).PullRequestBranches[0] != "main" || len(body.(ci.AutomationRequest).PushBranches) != 0 || body.(ci.AutomationRequest).LatestOnly {
		t.Fatal("PR-only automation lost its explicit base filters or changed push policy")
	}
	req.IncludePaths, req.ExcludePaths = []string{"src/**", "go.mod"}, []string{"src/docs/**"}
	_, _, body, err = ciAutomationRequest("put", binding, req)
	if err != nil || body.(ci.AutomationRequest).IncludePaths[0] != "go.mod" || body.(ci.AutomationRequest).ExcludePaths[0] != "src/docs/**" {
		t.Fatal("CLI dropped normalized path filters")
	}
	req.IncludePaths = []string{"../outside/**"}
	if _, _, _, err := ciAutomationRequest("put", binding, req); err == nil {
		t.Fatal("CLI accepted unsafe path pattern")
	}
}
