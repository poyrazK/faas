package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPAdmissionHistoryReadOnlyAndPartial(t *testing.T) {
	for _, scenario := range []string{"accepted", "rejected", "empty", "denied", "malformed", "other-deployment", "private-reason"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/v1/deployments/"+overviewWebDeployment+"/mcp-admissions" {
					t.Error("unexpected mutation or audit read")
				}
				if scenario == "denied" {
					http.Error(w, "PRIVATE_ERROR_BODY", 403)
					return
				}
				record := api.MCPAdmissionEvidence{Version: 1, ID: "00000000-0000-4000-8000-000000000003", DeploymentID: overviewWebDeployment, TrafficPercent: 100, CapturedAt: time.Now().UTC(), Outcome: "accepted", ExpectedFingerprint: strings.Repeat("a", 64), Gateways: []api.MCPAdmissionGateway{}}
				if scenario == "private-reason" {
					record.Outcome = "rejected"
					record.Reason = "PRIVATE_REJECTION_REASON"
				}
				if scenario == "rejected" {
					record.Outcome = "rejected"
					record.Reason = api.CodeMCPGatewayReplayPending
				}
				if scenario == "other-deployment" {
					record.DeploymentID = overviewHeldDeployment
				}
				if scenario == "malformed" {
					record.Version = 99
				}
				rows := []api.MCPAdmissionEvidence{record}
				outcome := r.URL.Query().Get("outcome")
				limit := 50
				if outcome != "" {
					limit = 1
				}
				if scenario == "empty" || (outcome != "" && outcome != record.Outcome) {
					rows = []api.MCPAdmissionEvidence{}
				}
				writeJSONTest(w, api.MCPAdmissionHistoryResponse{Items: rows, Limit: limit})
			}))
			defer server.Close()
			history := collectMCPAdmissionHistory(context.Background(), NewClient(server.URL, "operator"), overviewWebDeployment, 50)
			available := scenario == "accepted" || scenario == "rejected" || scenario == "empty"
			if (history.State == "available") != available {
				t.Fatalf("unexpected state: %+v", history)
			}
			count := 0
			if scenario == "accepted" || scenario == "rejected" {
				count = 1
			}
			if len(history.Entries) != count {
				t.Fatalf("wrong evidence: %+v", history)
			}
			raw, _ := json.Marshal(history)
			if strings.Contains(string(raw), "PRIVATE_") {
				t.Fatal("unrelated audit data or error leaked")
			}
		})
	}
}

func TestMCPOverviewIncludesAdmissionEvidence(t *testing.T) {
	fixture := newOverviewFixture(t, "held")
	report := collectMCPOverview(t.Context(), NewClient(fixture.server.URL, "PLATFORM_TOKEN_SENTINEL"), mcpOverviewOptions{App: "reports-web", Endpoint: "/mcp", Timeout: time.Second, Roles: []mcpReleaseRole{{Name: "default", token: "CLIENT_TOKEN_SENTINEL"}}})
	if report.Releases.AdmissionDeploymentLimit != 5 || len(report.Releases.Admissions) != 2 {
		t.Fatalf("recent release evidence missing: %+v", report.Releases)
	}
	for _, history := range report.Releases.Admissions {
		if history.State != "available" || history.WindowLimit != 50 || !history.Paginated || history.LatestAccepted.State != "empty" || history.LatestRejected.State != "empty" {
			t.Fatalf("incorrect admission window: %+v", history)
		}
	}
	if fixture.mutations.Load() != 0 || fixture.toolCalls.Load() != 0 {
		t.Fatal("admission inspection mutated application")
	}
}

func TestMCPAdmissionSummaryFindsOlderOutcome(t *testing.T) {
	accepted := api.MCPAdmissionEvidence{Version: 1, ID: "00000000-0000-4000-8000-000000000003", DeploymentID: overviewWebDeployment, TrafficPercent: 100, CapturedAt: time.Now().UTC(), Outcome: "accepted", ExpectedFingerprint: strings.Repeat("a", 64)}
	rejected := accepted
	rejected.ID = "00000000-0000-4000-8000-000000000004"
	rejected.Outcome = "rejected"
	rejected.Reason = api.CodeMCPGatewayReplayPending
	for _, scenario := range []string{"older accepted", "denied accepted", "wrong outcome", "bad cursor"} {
		t.Run(scenario, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != "GET" || r.URL.Path != "/v1/deployments/"+overviewWebDeployment+"/mcp-admissions" {
					t.Error("unexpected request")
				}
				switch r.URL.Query().Get("outcome") {
				case "accepted":
					if r.URL.Query().Get("limit") != "1" {
						t.Error("unbounded summary")
					}
					if scenario == "denied accepted" {
						http.Error(w, "PRIVATE_ERROR", 403)
						return
					}
					receipt := accepted
					if scenario == "wrong outcome" {
						receipt = rejected
					}
					writeJSONTest(w, api.MCPAdmissionHistoryResponse{Items: []api.MCPAdmissionEvidence{receipt}, Limit: 1})
				case "rejected":
					writeJSONTest(w, api.MCPAdmissionHistoryResponse{Items: []api.MCPAdmissionEvidence{rejected}, Limit: 1})
				default:
					rows := make([]api.MCPAdmissionEvidence, 50)
					for i := range rows {
						rows[i] = rejected
					}
					cursor := "42"
					if scenario == "bad cursor" {
						cursor = "PRIVATE_CURSOR"
					}
					writeJSONTest(w, api.MCPAdmissionHistoryResponse{Items: rows, Limit: 50, NextCursor: cursor})
				}
			}))
			defer server.Close()
			report := mcpCatalogStatusReport{Deployments: []mcpCatalogDeploymentReport{{DeploymentID: overviewWebDeployment}}}
			attachMCPAdmissionHistory(t.Context(), NewClient(server.URL, "operator"), &report)
			history := report.Deployments[0].Admission
			if requests != 3 || history.LatestRejected.State != "available" {
				t.Fatalf("bad summary: %+v", history)
			}
			if scenario == "older accepted" {
				if history.State != "available" || history.LatestAccepted.Receipt.ID != accepted.ID || history.NextCursor != "42" {
					t.Fatalf("older receipt lost: %+v", history)
				}
				if history.LatestAccepted.ReceiptPath != "/v1/mcp/admissions/"+accepted.ID {
					t.Fatal("wrong receipt link")
				}
			} else if history.State != "unknown" {
				t.Fatal("failed read reported available")
			}
			if scenario == "denied accepted" || scenario == "wrong outcome" {
				if history.LatestAccepted.State != "unavailable" || history.LatestAccepted.Receipt != nil {
					t.Fatal("invalid receipt exposed")
				}
			}
			raw, _ := json.Marshal(history)
			if strings.Contains(string(raw), "PRIVATE_") {
				t.Fatal("private response leaked")
			}
			oldOut, oldJSON := osStdout, jsonOutput
			var out bytes.Buffer
			osStdout = &out
			jsonOutput = false
			defer func() { osStdout = oldOut; jsonOutput = oldJSON }()
			_ = writeMCPAdmissionHistory(*history)
			if !strings.Contains(out.String(), "Latest rejected: available") || !strings.Contains(out.String(), "gregale mcp access history --evidence "+rejected.ID) {
				t.Fatal("missing receipt inspection command")
			}
		})
	}
}
