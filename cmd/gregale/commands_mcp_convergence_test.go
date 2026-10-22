package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPConvergenceViewsDetectDegradationAndRecovery(t *testing.T) {
	for _, signal := range []string{"pending", "active", "unverified", "denied", "wrong-app"} {
		t.Run(signal, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("monitoring mutated app")
				}
				switch r.URL.Path {
				case "/v1/apps/reports/policy/status":
					if r.URL.Query().Get("edge_rules_scope") != "account" {
						t.Error("unscoped policy query")
					}
					if signal == "denied" {
						http.Error(w, "PRIVATE_ERROR_BODY", 403)
						return
					}
					appID := "app-id"
					if signal == "wrong-app" {
						appID = "other-app"
					}
					main.WriteJSONTest(w, api.RuntimePolicyStatusResponse{AppID: appID, EdgeRules: api.RuntimePolicyComponentStatus{Scope: "account"}, MCPConvergence: &api.MCPPolicyConvergenceSnapshot{State: signal, AdmissionID: "receipt-id", AdmissionHistoryPath: "/v1/deployments/target/audit"}})
				case "/v1/apps/reports/alerts":
					main.WriteJSONTest(w, []api.AlertRuleResponse{{ID: "rule-id", AppID: "app-id", Enabled: true, Metric: "mcp_policy_unconverged_gateways", State: "firing", WebhookURL: "PRIVATE_URL"}, {ID: "other", AppID: "other-app", Enabled: true, Metric: "mcp_policy_unverified", State: "firing"}})
				default:
					t.Errorf("unexpected read %s", r.URL.Path)
				}
			}))
			defer server.Close()
			result := main.CollectMCPConvergence(t.Context(), main.NewClient(server.URL, "operator"), "reports", "app-id")
			expected := signal
			if signal == "denied" || signal == "wrong-app" {
				expected = "unknown"
			}
			if result.State != expected || result.AlertsState != "available" || len(result.Alerts) != 1 {
				t.Fatalf("incorrect monitor view: %+v", result)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "PRIVATE") {
				t.Fatal("monitor view leaked configuration")
			}
		})
	}
}
