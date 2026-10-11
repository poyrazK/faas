package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func routeHealthCorrelationFixtures(t *testing.T) (api.RouteHealthReport, map[string]api.RouteHealthInvestigation) {
	t.Helper()
	checkoutOpts := api.RouteHealthInvestigationOptions{Method: "POST", Path: "/checkout", Signal: "latency"}
	usersOpts := api.RouteHealthInvestigationOptions{Method: "GET", Path: "/users/{id}", Signal: "latency"}
	checkout := cliLatencyInvestigation(t, checkoutOpts)
	users := cliLatencyInvestigation(t, usersOpts)
	health := checkout.Report
	health.Routes = []api.RouteHealthFinding{checkout.Finding, users.Finding}
	routehealth.Evaluate(&health, health.ObservationAnchor, "")
	for _, investigation := range []*api.RouteHealthInvestigation{&checkout, &users} {
		investigation.Report = health
		for _, finding := range health.Routes {
			if finding.Method == investigation.Selection.Method && finding.Path == investigation.Selection.Path {
				investigation.Finding = finding
				investigation.Status, investigation.Reason = routehealth.InvestigationSignal(finding, 0, "latency")
			}
		}
	}
	return health, map[string]api.RouteHealthInvestigation{
		"POST /checkout":  checkout,
		"GET /users/{id}": users,
	}
}

func TestRouteHealthCorrelationCLI(t *testing.T) {
	for _, scenario := range []string{"json", "human", "unshared", "partial", "missing_spans", "no_latency_routes", "out", "invalid_limit"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			health, investigations := routeHealthCorrelationFixtures(t)
			if scenario == "no_latency_routes" {
				for i := range health.Routes {
					health.Routes[i].CheckLatency = false
					health.Routes[i].MaxP95MS = 0
				}
				routehealth.Evaluate(&health, health.ObservationAnchor, "")
			}
			if scenario == "unshared" {
				for i := range investigations["GET /users/{id}"].Windows {
					investigations["GET /users/{id}"].Windows[i].Diagnostics.Dependencies[0].Kind = "redis"
				}
			}
			if scenario == "missing_spans" {
				investigation := investigations["GET /users/{id}"]
				investigation.Windows[0].Diagnostics.Candidate.SpanRows--
				investigation.Windows[0].Diagnostics.Candidate.MissingSpanRows++
				investigations["GET /users/{id}"] = investigation
			}
			var mu sync.Mutex
			calls := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls[r.URL.Path+"?"+r.URL.RawQuery]++
				mu.Unlock()
				if r.URL.Path == "/v1/apps/demo/route-health/deployments/"+healthCandidateID {
					writeJSONTest(w, health)
					return
				}
				if r.URL.Path == "/v1/apps/demo/route-health/deployments/"+healthCandidateID+"/investigation" {
					key := r.URL.Query().Get("method") + " " + r.URL.Query().Get("path")
					if scenario == "partial" && key == "GET /users/{id}" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					if investigation, ok := investigations[key]; ok {
						writeJSONTest(w, investigation)
						return
					}
				}
				t.Errorf("unexpected route correlation request: %s?%s", r.URL.Path, r.URL.RawQuery)
				http.NotFound(w, r)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var output bytes.Buffer
			old := osStdout
			osStdout = &output
			t.Cleanup(func() { osStdout = old })
			args := []string{"routes", "health", "correlate", "demo", "--deployment", healthCandidateID}
			if scenario != "human" {
				args = append(args, "--json")
			}
			if scenario == "out" {
				args = append(args, "--out", filepath.Join(t.TempDir(), "correlation.json"))
			}
			if scenario == "invalid_limit" {
				args = append(args, "--limit", "21")
			}
			wantExit := 0
			if scenario == "invalid_limit" {
				wantExit = 1
			}
			if code := run(args); code != wantExit {
				t.Fatalf("exit %d want %d: %s", code, wantExit, output.String())
			}
			mu.Lock()
			requestCount := 0
			for _, count := range calls {
				requestCount += count
			}
			mu.Unlock()
			wantCalls := 3
			if scenario == "no_latency_routes" {
				wantCalls = 1
			}
			if scenario == "invalid_limit" {
				wantCalls = 0
			}
			if requestCount != wantCalls {
				t.Fatalf("made %d API calls, want %d: %#v", requestCount, wantCalls, calls)
			}
			if scenario == "human" {
				for _, expected := range []string{"Route dependency correlation: shared_dependency_slowdown_observed", "managed_binding/postgres: shared across 2 routes", "gregale debug requests inspect demo", "not proof of root cause"} {
					if !strings.Contains(output.String(), expected) {
						t.Fatalf("human output missing %q: %s", expected, output.String())
					}
				}
				return
			}
			if scenario == "invalid_limit" {
				return
			}
			body := output.Bytes()
			if scenario == "out" {
				var err error
				body, err = os.ReadFile(args[len(args)-1])
				if err != nil {
					t.Fatal(err)
				}
			}
			var got routeHealthCorrelationReport
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "json", "human", "out":
				if got.Status != "shared_dependency_slowdown_observed" || len(got.Groups) != 1 || got.Groups[0].Type != "managed_binding" || got.Groups[0].Kind != "postgres" || got.Groups[0].AffectedRoutes != 2 || len(got.Groups[0].Routes) != 2 {
					t.Fatalf("shared group not correlated: %+v", got)
				}
				if len(got.Groups[0].Routes[0].Windows) != api.RouteHealthWindows || got.Groups[0].Routes[0].Windows[0].P95DeltaMS <= 0 || got.Groups[0].Routes[0].Windows[1].P95DeltaMS <= 0 {
					t.Fatal("the shared group did not retain both positive window deltas")
				}
			case "unshared":
				if got.Status != "no_shared_dependency_slowdown_observed" || len(got.Groups) != 0 {
					t.Fatalf("unshared groups were combined: %+v", got)
				}
			case "partial":
				if got.Status != "incomplete" || got.RoutesAnalyzed != 1 || got.RoutesUnavailable != 1 || len(got.UnavailableRoutes) != 1 || got.UnavailableRoutes[0].Path != "/users/{id}" {
					t.Fatalf("partial route evidence was not disclosed: %+v", got)
				}
			case "missing_spans":
				if got.Status != "incomplete" || got.EvidenceIncompleteRoutes != 1 || len(got.Groups) != 1 || len(got.RouteCoverage) != 2 || got.RouteCoverage[1].CandidateMissingSpanRows != 1 {
					t.Fatalf("incomplete span coverage was not disclosed: %+v", got)
				}
			case "no_latency_routes":
				if got.Status != "no_latency_routes" || got.RoutesConsidered != 0 || len(got.Groups) != 0 {
					t.Fatalf("no-latency case is unclear: %+v", got)
				}
			}
		})
	}
}
