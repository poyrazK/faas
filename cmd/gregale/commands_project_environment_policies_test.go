package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectsEnvironmentPoliciesSetUsesScopedEndpoint(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"ownership":"environment","rules":[]}`, http.StatusOK)
	oldIn, oldOut := (*main.OsStdin), (*main.OsStdout)
	(*main.OsStdin) = strings.NewReader(`{"rules":[]}`)
	var output bytes.Buffer
	(*main.OsStdout) = &output
	t.Cleanup(func() { (*main.OsStdin), (*main.OsStdout) = oldIn, oldOut })
	if code := main.CmdProjectsEnvironmentPolicies([]string{"set", "shop", "staging", "shop-api", "--stdin", "--yes"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if (*f.SawMethod()) != http.MethodPut || (*f.SawPath()) != "/v1/projects/shop/environments/staging/workloads/shop-api/policies" {
		t.Fatalf("request=%s %s", (*f.SawMethod()), (*f.SawPath()))
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
	summary := main.EdgePolicySummary(policy)
	if !strings.Contains(summary, "headers:/api/*") || !strings.Contains(summary, "sha256:") || strings.Contains(summary, "private-value") {
		t.Fatalf("unsafe or incomplete summary: %q", summary)
	}
}
