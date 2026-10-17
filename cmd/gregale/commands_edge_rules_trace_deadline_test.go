// adr: 570
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdEdgeRulesTraceRetainsIngressDeadlineBeforeFixedResponse(t *testing.T) {
	for _, jsonFormat := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[jsonFormat], func(t *testing.T) {
			resetJSONEnv(t)
			jsonOutput = jsonFormat
			defer resetJSONEnv(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("preview mutated the API: %s", r.Method)
				}
				switch r.URL.Path {
				case "/v1/apps/demo":
					_ = json.NewEncoder(w).Encode(api.AppResponse{EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 3000, RequestBudgetMaxMS: 5000}})
				case "/v1/apps/demo/edge-rules":
					_ = json.NewEncoder(w).Encode([]api.EdgeRuleResponse{
						{ID: "ingress", Kind: "budget", Enabled: true, MatchHost: "example.com", MatchPath: "/public", Action: json.RawMessage(`{"budget":{"budget_ms":1200,"total_deadline_ms":4000}}`)},
						{ID: "rewrite", Kind: "rewrite", Enabled: true, MatchHost: "example.com", MatchPath: "*", Action: json.RawMessage(`{"rewrite":{"from":"/public","to":"/backend"}}`)},
						{ID: "fixed", Kind: "respond", Enabled: true, MatchHost: "example.com", MatchPath: "/backend", Action: json.RawMessage(`{"respond":{"status_code":200,"body":{"ok":true}}}`)},
					})
				default:
					t.Errorf("unexpected API path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			t.Setenv("FAAS_API_KEY", "")
			var stdout bytes.Buffer
			old := osStdout
			osStdout = &stdout
			defer func() { osStdout = old }()
			if code := cmdEdgeRulesTrace([]string{"--app", "demo", "--url", "https://example.com/public"}); code != 0 {
				t.Fatalf("trace exit=%d output=%s", code, stdout.String())
			}
			if jsonFormat {
				var result edgeRuleTraceResult
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				policy := result.Simulation.TotalDeadlinePolicy
				if policy == nil || policy.RuleID != "ingress" || policy.SelectionPath != "/public" || policy.DeadlineMS != 4000 || policy.EnforcementStatus != "unverified" || result.Simulation.Outcome != "fixed_response" {
					t.Fatalf("CLI lost ingress policy: %+v", result.Simulation)
				}
				return
			}
			for _, want := range []string{"ingress total deadline: 4000 ms", "selected on original path /public", "enforcement unverified", "execution overrides cannot increase it", "outcome=fixed_response final_path=/backend"} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("CLI omitted %q: %s", want, stdout.String())
				}
			}
		})
	}
}
