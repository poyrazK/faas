package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPAccessConvergence(t *testing.T) {
	for _, scenario := range []string{"active", "delayed", "stale", "missing", "app-only", "fleet-only", "wrong-app", "unavailable", "changed"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := accessTestConfig()
			rules := []api.EdgeRuleResponse{accessTestRule(cfg)}
			fingerprint, err := api.MCPAccessPolicyFingerprint(cfg.Auth.Resource, rules, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Fatal("unexpected mutation")
				}
				switch r.URL.Path {
				case "/v1/apps/my-mcp/policy/status":
					if r.URL.Query().Get("edge_rules_scope") != "account" {
						t.Error("missing account scope")
					}
					calls++
					status := api.RuntimePolicyStatusResponse{AppID: "app-id", EdgeRules: api.RuntimePolicyComponentStatus{Scope: "account", DesiredRevision: 42, State: "active", ServingGateways: 2, AppliedGateways: 2}}
					switch scenario {
					case "delayed":
						if calls == 1 {
							status.EdgeRules.State = "pending"
							status.EdgeRules.PendingGateways = 1
							status.EdgeRules.AppliedGateways = 1
						}
					case "stale":
						status.EdgeRules.StaleGateways = 1
					case "missing":
						status.EdgeRules.ServingGateways = 0
						status.EdgeRules.AppliedGateways = 0
					case "app-only":
						status.EdgeRules.Scope = "app"
					case "fleet-only":
						status.EdgeRules.Scope = "fleet"
					case "wrong-app":
						status.AppID = "other"
					case "unavailable":
						w.WriteHeader(503)
						_, _ = w.Write([]byte("PRIVATE_ERROR"))
						return
					}
					writeJSONTest(w, status)
				case "/v1/edge-rules":
					if scenario == "changed" {
						rules[0].Enabled = false
					}
					writeJSONTest(w, rules)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			// Successful convergence checks exercise replay and fingerprint semantics,
			// not a sub-second scheduling deadline on a busy CI host.
			deadline := 700 * time.Millisecond
			if scenario == "active" || scenario == "delayed" || scenario == "changed" {
				deadline = 5 * time.Second
			}
			ctx, cancel := context.WithTimeout(t.Context(), deadline)
			defer cancel()
			var revision int64
			check := waitMCPAccessConvergence(ctx, NewClient(server.URL, "operator"), "my-mcp", "app-id", cfg.Auth.Resource, fingerprint, &revision)
			want := "unknown"
			if scenario == "active" || scenario == "delayed" {
				want = "passed"
			}
			if scenario == "changed" {
				want = "failed"
			}
			if check.Status != want {
				t.Fatalf("%+v, want %s", check, want)
			}
			if (want == "passed" && revision != 42) || (want != "passed" && revision != 0) {
				t.Fatalf("unexpected promoted revision %d", revision)
			}
			if scenario == "delayed" && calls < 2 {
				t.Fatal("did not retry delayed gateway")
			}
		})
	}
}
