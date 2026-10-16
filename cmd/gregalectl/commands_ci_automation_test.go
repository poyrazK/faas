// ADR-1341: automation configuration requires an explicit profile and branches.
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
}
