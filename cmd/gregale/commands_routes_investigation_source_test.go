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

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeimpact"
)

func investigationSourceFixture(method, path string) routeimpact.Report {
	before := routeimpact.Route{Method: method, Path: path, Handler: "checkout", HandlerSymbol: "handlers.checkout", Source: routeimpact.Location{File: "handlers.py", Line: 20}, Registration: routeimpact.Location{File: "handlers.py", Line: 19}, ContextFiles: []string{"handlers.py"}, DependencyFiles: []string{}, DependencySymbols: []string{}, FallbackFiles: []string{}}
	after := before
	after.Source.Line = 23
	evidence := routeimpact.Evidence{File: "validation.py", Change: "modified", Revision: "candidate", Kind: "function_reference", Symbol: "validation.check", Line: 9, Via: []string{}, ViaSymbols: []routeimpact.SymbolLocation{{Name: "handlers.checkout", File: "handlers.py", Line: 23}, {Name: "validation.check", File: "validation.py", Line: 9}}}
	return routeimpact.Report{
		Version: 2, Framework: "fastapi", Repository: "github.com/team/service", SourceRoot: ".", Status: "complete", Scope: "scope_must_not_be_exported",
		Base:         routeimpact.Snapshot{Revision: strings.Repeat("a", 40), SourceSHA256: strings.Repeat("1", 64), PythonFiles: 2},
		Candidate:    routeimpact.Snapshot{Revision: strings.Repeat("b", 40), SourceSHA256: strings.Repeat("2", 64), PythonFiles: 2},
		ChangedFiles: []routeimpact.FileChange{{File: "validation.py", Change: "modified"}}, ChangedSymbols: []routeimpact.SymbolChange{},
		Summary: routeimpact.Summary{SourceChanged: 1}, Issues: []routeimpact.Issue{},
		Routes: []routeimpact.Result{{Method: method, Path: path, Change: "source_changed", Precision: "function", Before: &before, After: &after, Evidence: []routeimpact.Evidence{evidence}, Uncertainties: []routeimpact.Issue{}}},
	}
}

func investigationSourceHealth(opts api.RouteHealthInvestigationOptions) api.RouteHealthInvestigation {
	investigation := cliInvestigation(opts)
	investigation.Report.CandidateCommitSHA = strings.Repeat("b", 40)
	investigation.Report.StableCommitSHA = strings.Repeat("a", 40)
	return investigation
}

func TestRouteInvestigationSourceImpactCorrelation(t *testing.T) {
	for _, scenario := range []string{"matched", "parameter_names", "repository_mismatch", "health_revision_mismatch", "route_not_reported", "ambiguous_route", "evidence_truncated", "analysis_incomplete", "deployment_unavailable", "app_identity_mismatch", "human", "out"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			opts := api.RouteHealthInvestigationOptions{Method: "POST", Path: "/checkout", StatusCode: 403}
			source := investigationSourceFixture("POST", "/checkout")
			if scenario == "parameter_names" {
				opts.Path = "/orders/{id}"
				source = investigationSourceFixture("POST", "/orders/{order_id}")
			}
			if scenario == "route_not_reported" {
				source = investigationSourceFixture("POST", "/elsewhere")
			}
			if scenario == "ambiguous_route" {
				opts.Path = "/orders/{id}"
				source = investigationSourceFixture("POST", "/orders/{first}")
				second := investigationSourceFixture("POST", "/orders/{second}")
				source.Routes = append(source.Routes, second.Routes[0])
				source.Summary.SourceChanged++
			}
			if scenario == "evidence_truncated" {
				first := source.Routes[0].Evidence[0]
				for i := 1; i < 10; i++ {
					source.Routes[0].Evidence = append(source.Routes[0].Evidence, first)
				}
			}
			if scenario == "analysis_incomplete" {
				issue := routeimpact.Issue{Code: "unresolved_dependency", Message: "a static dependency could not be resolved"}
				source.Status = "incomplete"
				source.Issues = []routeimpact.Issue{issue}
				source.Routes[0].Uncertainties = []routeimpact.Issue{issue}
			}
			if scenario == "repository_mismatch" {
				source.Repository = "github.com/other/service"
			}
			if scenario == "health_revision_mismatch" {
				source.Candidate.Revision = strings.Repeat("c", 40)
			}
			investigation := investigationSourceHealth(opts)
			impactBytes, err := json.Marshal(source)
			if err != nil {
				t.Fatal(err)
			}
			impactPath := filepath.Join(t.TempDir(), "impact.json")
			if err := os.WriteFile(impactPath, impactBytes, 0600); err != nil {
				t.Fatal(err)
			}
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				switch r.URL.Path {
				case "/v1/apps/demo/route-health/deployments/" + healthCandidateID + "/investigation":
					writeJSONTest(w, investigation)
				case "/v1/deployments/" + healthCandidateID:
					commit := strings.Repeat("b", 40)
					appID := investigation.Report.AppID
					if scenario == "health_revision_mismatch" {
						commit = strings.Repeat("c", 40)
					}
					if scenario == "app_identity_mismatch" {
						appID = "99999999-9999-4999-8999-999999999999"
					}
					writeJSONTest(w, api.DeploymentResponse{ID: healthCandidateID, AppID: appID, CommitSHA: commit, SourceURL: "https://github.com/team/service", SourceRoot: "."})
				case "/v1/deployments/" + investigation.Report.StableDeploymentID:
					if scenario == "deployment_unavailable" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					writeJSONTest(w, api.DeploymentResponse{ID: investigation.Report.StableDeploymentID, AppID: investigation.Report.AppID, CommitSHA: strings.Repeat("a", 40), SourceURL: "https://github.com/team/service", SourceRoot: "."})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			old := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = old })
			args := []string{"routes", "health", "investigate", "demo", "--deployment", healthCandidateID, "--route", opts.Method + " " + opts.Path, "--status", "403", "--source-impact", impactPath}
			if scenario != "human" {
				args = append(args, "--json")
			}
			if scenario == "out" {
				args = append(args, "--out", filepath.Join(t.TempDir(), "investigation.json"))
			}
			if code := run(args); code != 0 {
				t.Fatalf("exit %d: %s", code, out.String())
			}
			if !strings.Contains(strings.Join(calls, "\n"), "/v1/deployments/") || len(calls) != 3 {
				t.Fatalf("deployment binding did not read both deployment details: %v", calls)
			}
			if scenario == "human" {
				if !strings.Contains(out.String(), "Source correlation: matched") || !strings.Contains(out.String(), "reference chain: handlers.checkout (handlers.py:23) → validation.check (validation.py:9)") || !strings.Contains(out.String(), "do not prove code execution or regression cause") {
					t.Fatalf("human source correlation is incomplete: %s", out.String())
				}
				return
			}
			jsonBody := out.Bytes()
			if scenario == "out" {
				jsonBody, err = os.ReadFile(args[len(args)-1])
				if err != nil {
					t.Fatal(err)
				}
			}
			var got routeInvestigationWithSource
			if err := json.Unmarshal(jsonBody, &got); err != nil {
				t.Fatal(err)
			}
			if got.Status != investigation.Status || got.SourceCorrelation == nil {
				t.Fatal("source output changed the investigation or omitted the correlation")
			}
			wantStatus, wantReason := "matched", ""
			switch scenario {
			case "parameter_names":
				if got.SourceCorrelation.Route == nil || got.SourceCorrelation.Route.Mapping != "parameter_names" {
					t.Fatal("whole-segment route parameters were not mapped")
				}
			case "repository_mismatch":
				wantStatus, wantReason = "unavailable", "base_repository_mismatch"
			case "health_revision_mismatch":
				wantStatus, wantReason = "unavailable", "candidate_route_health_revision_mismatch"
			case "route_not_reported":
				wantStatus, wantReason = "route_not_reported", "route_not_found_in_report"
			case "ambiguous_route":
				wantStatus, wantReason = "unavailable", "ambiguous_route_mapping"
			case "deployment_unavailable":
				wantStatus, wantReason = "unavailable", "base_deployment_detail_unavailable"
			case "app_identity_mismatch":
				wantStatus, wantReason = "unavailable", "candidate_app_identity_mismatch"
			}
			if got.SourceCorrelation.Status != wantStatus || got.SourceCorrelation.Reason != wantReason {
				t.Fatalf("correlation = %s/%s, want %s/%s", got.SourceCorrelation.Status, got.SourceCorrelation.Reason, wantStatus, wantReason)
			}
			if wantStatus != "matched" && got.SourceCorrelation.Route != nil {
				t.Fatal("unbound or missing route unexpectedly exposed source evidence")
			}
			if wantStatus == "matched" && (got.SourceCorrelation.Route == nil || got.SourceCorrelation.Route.AfterLocation == nil || got.SourceCorrelation.Route.AfterLocation.Line != 23) {
				t.Fatal("matched source route lost handler location")
			}
			if scenario == "evidence_truncated" && (len(got.SourceCorrelation.Route.Evidence) != routeInvestigationSourceEvidenceLimit || !got.SourceCorrelation.Route.EvidenceTruncated) {
				t.Fatal("source evidence was not bounded")
			}
			if scenario != "evidence_truncated" && wantStatus == "matched" && len(got.SourceCorrelation.Route.Evidence) != 1 {
				t.Fatal("matched source route lost its evidence")
			}
			if scenario == "analysis_incomplete" && (got.SourceCorrelation.AnalysisStatus != "incomplete" || got.SourceCorrelation.Route.UncertaintyCount != 1) {
				t.Fatal("incomplete analysis uncertainty was hidden")
			}
			if strings.Contains(string(jsonBody), "scope_must_not_be_exported") {
				t.Fatal("unrelated source report scope was exported")
			}
		})
	}
}

func TestRouteInvestigationRejectsInvalidSourceBeforeAPI(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	impactPath := filepath.Join(t.TempDir(), "invalid-impact.json")
	if err := os.WriteFile(impactPath, []byte(`{"version":99}`), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	if code := run([]string{"routes", "health", "investigate", "demo", "--deployment", healthCandidateID, "--route", "POST /checkout", "--source-impact", impactPath}); code != 1 {
		t.Fatalf("exit %d, want invalid source report", code)
	}
	if calls != 0 {
		t.Fatalf("invalid local report made %d API calls", calls)
	}
}
