package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPAdmissionExplainHistoricalAndCurrent(t *testing.T) {
	captured := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	expected, observed, behind := int64(42), int64(43), int64(41)
	old, future, fresh := captured.Add(-11*time.Second), captured.Add(time.Second), captured.Add(-api.MCPPolicyGatewayFreshness)
	id := "00000000-0000-4000-8000-000000000003"
	for _, scenario := range []string{"historical", "current", "denied receipt", "invalid reason", "wrong receipt", "missing gateways", "no revision guard", "wrong app", "current unavailable", "old current", "accepted"} {
		t.Run(scenario, func(t *testing.T) {
			requests := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				if r.Method != "GET" {
					t.Error("explanation mutated state")
				}
				switch r.URL.Path {
				case "/v1/mcp/admissions/" + id:
					if scenario == "denied receipt" {
						http.Error(w, "PRIVATE_ERROR", 403)
						return
					}
					receipt := api.MCPAdmissionEvidence{Version: 1, ID: id, DeploymentID: main.OverviewWebDeployment, TrafficPercent: 100, CapturedAt: captured, Outcome: "rejected", Reason: api.CodeMCPGatewayReplayPending, ExpectedFingerprint: strings.Repeat("a", 64), ObservedFingerprint: strings.Repeat("b", 64), ExpectedRevision: &expected, ObservedRevision: &observed, Gateways: []api.MCPAdmissionGateway{
						{NodeName: "old", Revision: &behind, ObservedAt: &old},
						{NodeName: "future", Revision: &observed, ObservedAt: &future},
						{NodeName: "missing"},
						{NodeName: "boundary", Revision: &observed, ObservedAt: &fresh},
					}}
					if scenario == "accepted" {
						receipt.Outcome = "accepted"
						receipt.Reason = ""
						receipt.ObservedFingerprint = receipt.ExpectedFingerprint
						receipt.ObservedRevision = &expected
						receipt.Gateways = []api.MCPAdmissionGateway{{NodeName: "boundary", Revision: &expected, ObservedAt: &fresh}}
					}
					if scenario == "invalid reason" {
						receipt.Reason = "PRIVATE_REASON"
					}
					if scenario == "wrong receipt" {
						receipt.ID = main.OverviewHeldDeployment
					}
					if scenario == "missing gateways" {
						receipt.Gateways = nil
					}
					if scenario == "no revision guard" {
						receipt.ExpectedRevision = nil
						receipt.ObservedRevision = nil
						receipt.Gateways = nil
						receipt.Reason = api.CodeMCPAccessPolicyChanged
					}
					main.WriteJSONTest(w, receipt)
				case "/v1/deployments/" + main.OverviewWebDeployment:
					main.WriteJSONTest(w, api.DeploymentResponse{ID: main.OverviewWebDeployment, AppID: "app-id"})
				case "/v1/apps/reports":
					appID := "app-id"
					if scenario == "wrong app" {
						appID = "different-app"
					}
					main.WriteJSONTest(w, api.AppResponse{ID: appID, Slug: "reports"})
				case "/v1/apps/reports/policy/status":
					if r.URL.Query().Get("edge_rules_scope") != "account" {
						t.Error("incorrect convergence scope")
					}
					if scenario == "current unavailable" {
						http.Error(w, "PRIVATE_CURRENT_ERROR", 500)
						return
					}
					at := captured.Add(24 * time.Hour)
					if scenario == "old current" {
						at = captured.Add(-time.Second)
					}
					main.WriteJSONTest(w, api.RuntimePolicyStatusResponse{AppID: "app-id", EdgeRules: api.RuntimePolicyComponentStatus{Scope: "account"}, MCPConvergence: &api.MCPPolicyConvergenceSnapshot{State: "active", CapturedAt: at, DesiredRevision: observed, ServingGateways: 4}})
				case "/v1/apps/reports/alerts":
					main.WriteJSONTest(w, []api.AlertRuleResponse{})
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			slug := ""
			if scenario == "current" || scenario == "wrong app" || scenario == "current unavailable" || scenario == "old current" {
				slug = "reports"
			}
			report := main.CollectMCPAdmissionExplanation(t.Context(), main.NewClient(server.URL, "operator"), id, slug)
			unavailable := scenario == "denied receipt" || scenario == "invalid reason" || scenario == "wrong receipt"
			if (report.State == "unavailable") != unavailable {
				t.Fatalf("unexpected state: %+v", report)
			}
			if unavailable {
				if report.Receipt != nil || len(requests) != 1 {
					t.Fatal("invalid receipt exposed or followed")
				}
			} else {
				if report.Receipt.CapturedAt != captured || (scenario != "accepted" && report.Fingerprint != "changed") {
					t.Fatal("historical decision altered")
				}
				if scenario == "missing gateways" {
					if report.GatewayEvidence != "no_serving_gateways" {
						t.Fatal("no fleet not identified")
					}
				} else if scenario == "no revision guard" {
					if report.GatewayEvidence != "not_requested" || report.AccountRevision != "not_requested" {
						t.Fatal("fabricated gateway proof")
					}
				} else if scenario == "accepted" {
					if report.Fingerprint != "matched" || report.AccountRevision != "matched" || len(report.Gateways) != 1 || report.Gateways[0].Freshness != "fresh" {
						t.Fatal("accepted record misdiagnosed")
					}
				} else {
					if report.AccountRevision != "changed" || len(report.Gateways) != 4 {
						t.Fatalf("bad historical analysis: %+v", report)
					}
					if report.Gateways[0].Freshness != "stale" || report.Gateways[0].Replay != "behind" || report.Gateways[1].Freshness != "future_dated" || report.Gateways[2].Freshness != "missing" || report.Gateways[2].Replay != "missing" || report.Gateways[3].Freshness != "fresh" {
						t.Fatal("decision-time gateway diagnostics incorrect")
					}
				}
			}
			if scenario == "current" && (report.CurrentState != "available" || report.Current.State != "active" || report.Gateways[0].Freshness != "stale") {
				t.Fatal("live status rewrote historical evidence")
			}
			if scenario == "wrong app" {
				if report.CurrentState != "unavailable" || len(requests) != 3 {
					t.Fatal("unrelated app queried")
				}
			}
			if scenario == "accepted" && !strings.Contains(report.Explanation, "committed") {
				t.Fatal("accepted receipt misrepresented")
			}
			if scenario == "current unavailable" || scenario == "old current" {
				if report.CurrentState != "unavailable" || report.State != "available" {
					t.Fatal("current failure lost history")
				}
				if report.Current != nil && report.Current.Snapshot != nil {
					t.Fatal("invalid live snapshot displayed")
				}
			}
			raw, _ := json.Marshal(report)
			if strings.Contains(string(raw), "PRIVATE_") {
				t.Fatal("private upstream data leaked")
			}
			oldOut, oldJSON := (*main.OsStdout), (*main.JsonOutput)
			var out bytes.Buffer
			(*main.OsStdout) = &out
			(*main.JsonOutput) = false
			defer func() { (*main.OsStdout) = oldOut; (*main.JsonOutput) = oldJSON }()
			code := main.WriteMCPAdmissionExplanation(report)
			if (code != 0) != (unavailable || report.CurrentState == "unavailable") {
				t.Fatalf("unexpected exit: %d", code)
			}
			if !strings.Contains(out.String(), "Current observation (separate from this receipt)") {
				t.Fatal("missing temporal separation")
			}
		})
	}
}
func TestMCPAdmissionExplainCommandValidation(t *testing.T) {
	for _, args := range [][]string{{"explain"}, {"explain", "--evidence", "bad"}, {"explain", "--evidence", main.OverviewWebDeployment, "--timeout", "0s"}, {"explain", "--evidence", main.OverviewWebDeployment, "--app", "bad slug"}} {
		if main.CmdMCPAccess(args) == 0 {
			t.Fatal("invalid explanation accepted")
		}
	}
}
