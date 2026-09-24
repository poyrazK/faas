package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectsEnvironmentPoliciesSetUsesScopedEndpoint(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"ownership":"environment","rules":[]}`, http.StatusOK)
	oldIn, oldOut := osStdin, osStdout
	osStdin = strings.NewReader(`{"rules":[]}`)
	var output bytes.Buffer
	osStdout = &output
	t.Cleanup(func() { osStdin, osStdout = oldIn, oldOut })
	if code := cmdProjectsEnvironmentPolicies([]string{"set", "shop", "staging", "shop-api", "--stdin", "--yes"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if f.sawMethod != http.MethodPut || f.sawPath != "/v1/projects/shop/environments/staging/workloads/shop-api/policies" {
		t.Fatalf("request=%s %s", f.sawMethod, f.sawPath)
	}
	if !strings.Contains(output.String(), "0 headers/CORS rules") {
		t.Fatalf("output=%q", output.String())
	}
}

func TestEnvironmentPolicyDiffSummaryDoesNotExposeHeaderValue(t *testing.T) {
	policy := api.ProjectEnvironmentEdgePolicyResponse{
		Ownership: "environment",
		Rules: []api.ProjectEnvironmentEdgeRuleResponse{{
			Kind: "headers", MatchPath: "/api/*", Enabled: true,
			Action: json.RawMessage(`{"response_headers":[{"name":"Authorization","value":"private-value","action":"set"}]}`),
		}},
	}
	summary := edgePolicySummary(policy)
	if !strings.Contains(summary, "headers:/api/*") || !strings.Contains(summary, "sha256:") || strings.Contains(summary, "private-value") {
		t.Fatalf("unsafe or incomplete summary: %q", summary)
	}
}
