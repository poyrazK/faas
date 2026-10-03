package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func cliLatencyReport(scenario string) api.RouteHealthReport {
	r := cliHealthReport("healthy")
	f := &r.Routes[0]
	f.CheckLatency, f.MaxP95MS = true, 300
	for i := range f.Windows {
		candidate, stable := 150.0, 120.0
		if scenario == "regressed" || scenario == "mixed_signals" && i == 0 {
			candidate = 500
		}
		if scenario == "mixed_signals" && i == 1 {
			candidate = 120
			f.Windows[i].Candidate.ServerErrors = 10
		}
		if scenario == "zero" {
			candidate, stable = 0, 0
		}
		f.Windows[i].Candidate.P95LatencyMS, f.Windows[i].Stable.P95LatencyMS = &candidate, &stable
		if scenario == "unknown" {
			f.Windows[i].Candidate.Requests = 99
		}
		if scenario == "unavailable" {
			f.Windows[i].Candidate.P95LatencyMS = nil
		}
	}
	anchor := time.Now().Add(-time.Hour)
	routehealth.Evaluate(&r, &anchor, "")
	return r
}

func TestRouteLatencyCLIReportsAndEvidenceValidation(t *testing.T) {
	for _, scenario := range []string{"healthy", "human", "zero", "regressed", "unknown", "unavailable", "mixed_signals", "false_healthy", "missing_p95", "missing_signal", "wrong_minimum", "wrong_delta", "wrong_factor", "contradictory_error"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			report := cliLatencyReport(scenario)
			window := &report.Routes[0].Windows[0]
			switch scenario {
			case "false_healthy":
				*window.Candidate.P95LatencyMS = 500
			case "missing_p95":
				window.Candidate.P95LatencyMS = nil
			case "missing_signal":
				window.LatencyStatus = ""
			case "wrong_minimum":
				report.MinimumLatencyRequests = 20
			case "wrong_delta":
				*window.LatencyDeltaMS = 999
			case "wrong_factor":
				*window.LatencyFactor = 9
			case "contradictory_error":
				report = cliHealthReport("regressed")
				report.Status, report.Routes[0].Status, report.Routes[0].ErrorStatus = "healthy", "healthy", "healthy"
				for i := range report.Routes[0].Windows {
					report.Routes[0].Windows[i].ErrorStatus = "healthy"
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/v1/apps/demo/route-health/deployments/"+healthCandidateID {
					t.Error("latency report binding")
				}
				writeJSONTest(w, report)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			old := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = old })
			args := []string{"routes", "health", "report", "demo", "--deployment", healthCandidateID, "--fail-on-unhealthy"}
			if scenario != "human" {
				args = append(args, "--json")
			}
			want := 1
			if scenario == "healthy" || scenario == "human" || scenario == "zero" {
				want = 0
			}
			if code := run(args); code != want {
				t.Fatalf("exit %d want %d: %s", code, want, out.String())
			}
			switch scenario {
			case "healthy", "zero", "regressed", "unknown", "unavailable", "mixed_signals":
				var got api.RouteHealthReport
				if json.Unmarshal(out.Bytes(), &got) != nil || got.DeploymentID != healthCandidateID || got.Status != report.Status || len(got.Routes) != 1 || got.Routes[0].LatencyStatus != report.Routes[0].LatencyStatus {
					t.Fatalf("valid latency evidence was rejected: %s", out.String())
				}
			case "human":
			default:
				if out.Len() != 0 {
					t.Fatal("invalid evidence was emitted as a report")
				}
			}
			if scenario == "human" {
				for _, evidence := range []string{"p95 candidate 150.0ms, stable 120.0ms", "delta +30.0ms", "1.25x", "p95 budget 300ms", "relative slowdown check enabled", "minimum 100"} {
					if !strings.Contains(out.String(), evidence) {
						t.Fatalf("missing %q: %s", evidence, out.String())
					}
				}
			}
		})
	}
}

func TestRouteLatencyCLIConfiguration(t *testing.T) {
	for _, scenario := range []string{"set", "wrong_budget", "wrong_relative_flag", "negative_budget", "oversized_budget"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			route := api.RouteHealthRoute{Method: "POST", Path: "/checkout", CheckLatency: true, MaxP95MS: 300}
			if scenario == "negative_budget" {
				route.MaxP95MS = -1
			}
			if scenario == "oversized_budget" {
				route.MaxP95MS = api.RouteHealthMaxP95BudgetMS + 1
			}
			body, err := json.Marshal([]api.RouteHealthRoute{route})
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "critical-routes.json")
			if err := os.WriteFile(file, body, 0600); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req api.SetRouteHealthGateRequest
				if r.Method != "PUT" || json.NewDecoder(r.Body).Decode(&req) != nil || len(req.Routes) != 1 || req.Routes[0] != route {
					t.Error("latency intent not preserved")
				}
				response := route
				if scenario == "wrong_budget" {
					response.MaxP95MS = 0
				}
				if scenario == "wrong_relative_flag" {
					response.CheckLatency = false
				}
				writeJSONTest(w, api.RouteHealthGate{AppID: "11111111-1111-4111-8111-111111111111", Mode: "enforce", Revision: 1, UpdatedAt: &now, Routes: []api.RouteHealthRoute{response}})
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			want := 1
			if scenario == "set" {
				want = 0
			}
			if code := run([]string{"routes", "health", "set", "demo", "--routes", file, "--mode", "enforce", "--expected-revision", "0", "--json"}); code != want {
				t.Fatalf("exit %d want %d", code, want)
			}
			if (scenario == "negative_budget" || scenario == "oversized_budget") && calls != 0 {
				t.Fatal("invalid latency intent reached API")
			}
		})
	}
}
