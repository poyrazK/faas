package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeimpact"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

func cliProductionIncident(t *testing.T) api.RouteMonitorIncident {
	t.Helper()
	body, err := os.ReadFile("../../tests/fixtures/production-route-incident.json")
	if err != nil {
		t.Fatal(err)
	}
	var i api.RouteMonitorIncident
	if err := json.Unmarshal(body, &i); err != nil {
		t.Fatal(err)
	}
	if err := routemonitor.ValidateIncident(i, "demo"); err != nil {
		t.Fatal(err)
	}
	return i
}

func TestRouteMonitorPreviewCLIUsesReadOnlyEndpoint(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	incident := cliProductionIncident(t)
	proposedRoutes := []api.RouteMonitorRoute{incident.OpeningReport.Routes[0].Route}
	routeFile := filepath.Join(t.TempDir(), "budgets.json")
	encodedRoutes, err := json.Marshal(proposedRoutes)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(routeFile, encodedRoutes, 0o600); err != nil {
		t.Fatal(err)
	}
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/demo/route-monitor/preview" || r.URL.RawQuery != "" {
			t.Errorf("preview used unexpected request %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			http.NotFound(w, r)
			return
		}
		var req api.PreviewRouteMonitorRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CustomerGroupBy != "" || !routemonitor.RoutesEqual(req.Routes, proposedRoutes) {
			t.Errorf("preview request does not match proposed budgets: %+v %v", req, err)
		}
		writeJSONTest(w, api.RouteMonitorPreview{
			CurrentRevision:                     incident.OpeningReport.Revision,
			PreviewOnly:                         true,
			ConfigChangeResetsObservationAnchor: true,
			Report:                              incident.OpeningReport,
		})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	var output bytes.Buffer
	old := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = old })
	if code := run([]string{"routes", "monitor", "preview", "demo", "--routes", routeFile, "--json"}); code != 0 {
		t.Fatalf("exit %d: %s", code, output.String())
	}
	var got api.RouteMonitorPreview
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || got.CurrentRevision != incident.OpeningReport.Revision || !got.PreviewOnly || got.Report.Status != "violated" {
		t.Fatalf("preview did not return one read-only evaluation: calls=%d preview=%+v", calls, got)
	}
}

func TestParseRouteMonitorPreviewOptions(t *testing.T) {
	options, err := parseRouteMonitorCLI([]string{"preview", "demo", "--routes", "budgets.json", "--customer-group-by", "tenant", "--customer-details", "--fail-on-unhealthy"})
	if err != nil {
		t.Fatal(err)
	}
	if options.action != "preview" || options.customerGroupBy != "tenant" || !options.customerDetails || !options.fail {
		t.Fatalf("preview options were not preserved: %+v", options)
	}
	if _, err := parseRouteMonitorCLI([]string{"preview", "demo"}); err == nil {
		t.Fatal("preview accepted no proposed budgets")
	}
}

func TestRenderRouteMonitorIncidentIncludesTransitionEvidence(t *testing.T) {
	incident := cliProductionIncident(t)
	finding := incident.OpeningReport.Routes[0]
	incident.Escalations = []api.RouteMonitorIncidentEscalation{{
		TransitionID: "44444444-4444-4444-8444-444444444444",
		CheckedAt:    incident.OpenedAt.Add(2 * time.Minute), PreviousCheckedAt: incident.OpenedAt.Add(time.Minute),
		NewlyViolatedRoutes: 1, NewlyViolatedSignals: 1,
		Signals:  []api.RouteMonitorIncidentEscalationSignal{{RouteIndex: 0, Signal: "latency", Finding: finding}},
		Evidence: []api.RouteMonitorEvidence{incident.Evidence[0]},
	}}
	var output bytes.Buffer
	old := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = old })
	renderRouteMonitorIncident(incident, "demo")
	for _, expected := range []string{"Transition 44444444-4444-4444-8444-444444444444", "Newly violated: POST /checkout — latency", "POST /checkout — errors evidence"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("incident explanation omitted %q: %s", expected, output.String())
		}
	}
}

func TestRouteMonitorIncidentSourceImpactCorrelation(t *testing.T) {
	for _, scenario := range []string{"matched", "repository_mismatch", "base_revision_mismatch", "baseline_unavailable", "baseline_repository_mismatch", "baseline_source_root_mismatch", "candidate_revision_mismatch", "incident_revision_mismatch", "app_identity_mismatch", "deployment_unavailable", "route_not_reported", "analysis_incomplete", "human", "out"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			incident := cliProductionIncident(t)
			incident.Baseline = &api.RouteMonitorDeploymentBaseline{DeploymentID: "66666666-6666-4666-8666-666666666666", CommitSHA: strings.Repeat("c", 40), Repository: "github.com/team/service", SourceRoot: "."}
			source := investigationSourceFixture("POST", "/checkout")
			source.Base.Revision = strings.Repeat("c", 40)
			source.Candidate.Revision = incident.OpeningReport.CommitSHA
			if scenario == "base_revision_mismatch" {
				incident.Baseline.CommitSHA = strings.Repeat("b", 40)
			}
			if scenario == "baseline_unavailable" {
				incident.Baseline = nil
			}
			if scenario == "baseline_repository_mismatch" {
				incident.Baseline.Repository = "github.com/other/service"
			}
			if scenario == "baseline_source_root_mismatch" {
				incident.Baseline.SourceRoot = "src"
			}
			if scenario == "repository_mismatch" {
				source.Repository = "github.com/other/service"
			}
			if scenario == "candidate_revision_mismatch" {
				source.Candidate.Revision = strings.Repeat("d", 40)
			}
			if scenario == "route_not_reported" {
				source.Routes = []routeimpact.Result{}
				source.Summary.SourceChanged = 0
			}
			if scenario == "analysis_incomplete" {
				issue := routeimpact.Issue{Code: "unresolved_dependency", Message: "a static dependency could not be resolved"}
				source.Status = "incomplete"
				source.Issues = []routeimpact.Issue{issue}
				source.Routes[0].Uncertainties = []routeimpact.Issue{issue}
			}
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
				case "/v1/apps/demo/route-monitor/incidents/" + incident.ID:
					writeJSONTest(w, incident)
				case "/v1/deployments/" + incident.DeploymentID:
					if scenario == "deployment_unavailable" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					appID, commit := incident.AppID, incident.OpeningReport.CommitSHA
					if scenario == "incident_revision_mismatch" {
						commit = strings.Repeat("e", 40)
					}
					if scenario == "app_identity_mismatch" {
						appID = "99999999-9999-4999-8999-999999999999"
					}
					writeJSONTest(w, api.DeploymentResponse{ID: incident.DeploymentID, AppID: appID, CommitSHA: commit, SourceURL: "https://github.com/team/service", SourceRoot: "."})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var output bytes.Buffer
			old := osStdout
			osStdout = &output
			t.Cleanup(func() { osStdout = old })
			args := []string{"routes", "monitor", "explain", "demo", "--incident", incident.ID, "--source-impact", impactPath}
			if scenario != "human" {
				args = append(args, "--json")
			}
			if scenario == "out" {
				args = append(args, "--out", filepath.Join(t.TempDir(), "incident.json"))
			}
			if code := run(args); code != 0 {
				t.Fatalf("exit %d: %s", code, output.String())
			}
			wantCalls := 2
			if scenario == "repository_mismatch" || scenario == "baseline_unavailable" || scenario == "base_revision_mismatch" || scenario == "baseline_repository_mismatch" || scenario == "baseline_source_root_mismatch" {
				wantCalls = 1
			}
			if len(calls) != wantCalls || wantCalls == 2 && calls[1] != "/v1/deployments/"+incident.DeploymentID {
				t.Fatalf("incident source binding calls = %v", calls)
			}
			if scenario == "human" {
				for _, expected := range []string{"Source change correlation: release_pair_bound", "errors, latency: matched", "source source_changed", "reference chain: handlers.checkout (handlers.py:23) → validation.check (validation.py:9)", "base binding declared_match", "Healthy baseline deployment:", "do not verify deployment archive bytes"} {
					if !strings.Contains(output.String(), expected) {
						t.Fatalf("human source output omitted %q: %s", expected, output.String())
					}
				}
				return
			}

			jsonBody := output.Bytes()
			if scenario == "out" {
				jsonBody, err = os.ReadFile(args[len(args)-1])
				if err != nil {
					t.Fatal(err)
				}
			}
			var got routeMonitorIncidentWithSource
			if err := json.Unmarshal(jsonBody, &got); err != nil {
				t.Fatal(err)
			}
			if got.ID != incident.ID || got.SourceCorrelation == nil {
				t.Fatal("source impact output omitted incident or correlation")
			}
			corr := got.SourceCorrelation
			wantStatus, wantReason := "release_pair_bound", ""
			switch scenario {
			case "repository_mismatch":
				wantStatus, wantReason = "unavailable", "base_repository_mismatch"
			case "base_revision_mismatch":
				wantStatus, wantReason = "unavailable", "base_revision_mismatch"
			case "baseline_unavailable":
				wantStatus, wantReason = "unavailable", "healthy_baseline_unavailable"
			case "baseline_repository_mismatch":
				wantStatus, wantReason = "unavailable", "base_repository_mismatch"
			case "baseline_source_root_mismatch":
				wantStatus, wantReason = "unavailable", "base_source_root_mismatch"
			case "candidate_revision_mismatch":
				wantStatus, wantReason = "unavailable", "candidate_revision_mismatch"
			case "incident_revision_mismatch":
				wantStatus, wantReason = "unavailable", "incident_deployment_revision_mismatch"
			case "app_identity_mismatch":
				wantStatus, wantReason = "unavailable", "app_identity_mismatch"
			case "deployment_unavailable":
				wantStatus, wantReason = "unavailable", "deployment_detail_unavailable"
			}
			if corr.Status != wantStatus || corr.Reason != wantReason {
				t.Fatalf("correlation = %s/%s, want %s/%s", corr.Status, corr.Reason, wantStatus, wantReason)
			}
			if wantStatus != "release_pair_bound" {
				if len(corr.Routes) != 0 || corr.CandidateBinding.Status == "declared_match" {
					t.Fatal("unbound source unexpectedly exposed affected source routes")
				}
				return
			}
			if len(corr.Routes) != 1 {
				t.Fatalf("affected route count %d", len(corr.Routes))
			}
			if scenario == "route_not_reported" {
				if corr.Routes[0].Status != "route_not_reported" || corr.Routes[0].Reason != "route_not_found_in_report" {
					t.Fatalf("missing route mapping hidden: %+v", corr.Routes[0])
				}
				return
			}
			matched := corr.Routes[0]
			if matched.Status != "matched" || matched.Source == nil || matched.Source.Change != "source_changed" || len(matched.Signals) != 2 {
				t.Fatalf("source route mapping is incomplete: %+v", matched)
			}
			if scenario == "analysis_incomplete" && (corr.AnalysisStatus != "incomplete" || matched.Source.UncertaintyCount != 1) {
				t.Fatal("incomplete static analysis was hidden")
			}
			if strings.Contains(string(jsonBody), "scope_must_not_be_exported") {
				t.Fatal("unrelated source report scope was exported")
			}
		})
	}
}

func TestRouteMonitorIncidentSourceImpactAutoUsesSavedReleasePair(t *testing.T) {
	setPreviewTestAuth(t)
	root := t.TempDir()
	routeImpactGit(t, root, "init", "-q")
	routeImpactGit(t, root, "remote", "add", "origin", "https://github.com/team/service.git")
	sourceRoot := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(sourceRoot, "main.go")
	baseSource := "package main\nimport \"net/http\"\nfunc main() { http.HandleFunc(\"POST /checkout\", checkout) }\nfunc checkout(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }\n"
	if err := os.WriteFile(mainPath, []byte(baseSource), 0o600); err != nil {
		t.Fatal(err)
	}
	routeImpactGit(t, root, "add", ".")
	routeImpactGit(t, root, "commit", "-qm", "healthy baseline")
	baseCommit := routeMonitorGitOutput(t, root, "rev-parse", "HEAD")
	candidateSource := strings.Replace(baseSource, "http.StatusOK", "http.StatusAccepted", 1)
	if err := os.WriteFile(mainPath, []byte(candidateSource), 0o600); err != nil {
		t.Fatal(err)
	}
	routeImpactGit(t, root, "add", ".")
	routeImpactGit(t, root, "commit", "-qm", "change checkout response")
	candidateCommit := routeMonitorGitOutput(t, root, "rev-parse", "HEAD")

	incident := cliProductionIncident(t)
	incident.Baseline = &api.RouteMonitorDeploymentBaseline{
		DeploymentID: "66666666-6666-4666-8666-666666666666", CommitSHA: baseCommit,
		Repository: "github.com/team/service", SourceRoot: "services/api",
	}
	incident.OpeningReport.CommitSHA = candidateCommit
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/deployments/"+incident.DeploymentID {
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		writeJSONTest(w, api.DeploymentResponse{
			ID: incident.DeploymentID, AppID: incident.AppID, CommitSHA: candidateCommit,
			SourceURL: "https://github.com/team/service", SourceRoot: "services/api",
		})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	client, err := authedClient()
	if err != nil {
		t.Fatal(err)
	}

	// Start inside the application root to verify auto mode finds the Git root
	// before applying the stored repository-relative source root.
	correlation := correlateRouteMonitorIncidentSourceAuto(context.Background(), client, incident, "demo", sourceRoot)
	if calls != 1 {
		t.Fatalf("deployment detail requests = %d, want 1", calls)
	}
	if correlation.Status != "release_pair_bound" || correlation.BaseRevision != baseCommit || correlation.CandidateRevision != candidateCommit || correlation.SourceRoot != "services/api" {
		t.Fatalf("automatic correlation did not use incident release pair: %+v", correlation)
	}
	if len(correlation.ArtifactSHA256) != 64 || len(correlation.Routes) != 1 || correlation.Routes[0].Status != "matched" || correlation.Routes[0].Source == nil || correlation.Routes[0].Source.Change != "source_changed" {
		t.Fatalf("automatic source analysis omitted route change: %+v", correlation)
	}

	incident.Baseline.CommitSHA = strings.Repeat("d", 40)
	correlation = correlateRouteMonitorIncidentSourceAuto(context.Background(), client, incident, "demo", sourceRoot)
	if correlation.Status != "unavailable" || correlation.Reason != "local_analysis_unavailable" || len(correlation.Routes) != 0 {
		t.Fatalf("missing local baseline revision did not fail safely: %+v", correlation)
	}
}

func TestParseRouteMonitorExplainAcceptsAutomaticSourceImpact(t *testing.T) {
	incidentID := cliProductionIncident(t).ID
	options, err := parseRouteMonitorCLI([]string{"explain", "demo", "--incident", incidentID, "--source-impact", "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if options.action != "explain" || options.sourceImpact != "auto" {
		t.Fatalf("automatic source-impact option was not preserved: %+v", options)
	}
}

func routeMonitorGitOutput(t *testing.T, directory string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", directory}, args...)...)
	body, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(body))
}

func TestRouteMonitorSourceMappingRequiresUniqueNormalizedRoute(t *testing.T) {
	incident := cliProductionIncident(t)
	incident.OpeningReport.Routes[0].Route.Path = "/orders/{id}"
	source := investigationSourceFixture("POST", "/orders/{order_id}")
	matched := correlateRouteMonitorAffectedRoutes(incident, source)
	if len(matched) != 1 || matched[0].Status != "matched" || matched[0].Source == nil || matched[0].Source.Mapping != "parameter_names" {
		t.Fatalf("unique parameter-name mapping was not accepted: %+v", matched)
	}

	second := source.Routes[0]
	second.Path = "/orders/{item_id}"
	if second.Before != nil {
		before := *second.Before
		before.Path = second.Path
		second.Before = &before
	}
	if second.After != nil {
		after := *second.After
		after.Path = second.Path
		second.After = &after
	}
	source.Routes = append(source.Routes, second)
	ambiguous := correlateRouteMonitorAffectedRoutes(incident, source)
	if len(ambiguous) != 1 || ambiguous[0].Status != "unavailable" || ambiguous[0].Reason != "ambiguous_route_mapping" || ambiguous[0].Source != nil {
		t.Fatalf("ambiguous parameter mapping exposed source evidence: %+v", ambiguous)
	}
}

// ADR-498: wire scopes, reports, bounded saved diagnostics and create-new export.
func TestProductionRouteMonitorCLIReportsAndIncidentEvidence(t *testing.T) {
	for _, scenario := range []string{"human", "timeline", "json", "export", "overwrite", "symlink", "wrong_id", "wrong_weight", "wrong_link", "wrong_verdict", "report", "report_gate", "incidents"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			incident := cliProductionIncident(t)
			switch scenario {
			case "wrong_id":
				incident.ID = "00000000-0000-4000-8000-000000000003"
			case "wrong_weight":
				incident.Evidence[0].Windows[0].Requests.MatchingRequests++
			case "wrong_link":
				incident.Evidence[0].Windows[0].Requests.Examples[0].EvidencePath = "/v1/apps/other/debug/requests/row/evidence"
			case "wrong_verdict":
				incident.OpeningReport.Status = "healthy"
			case "timeline":
				opening := routemonitor.IncidentTimelineEntry(incident.OpeningReport)
				followup := opening
				followup.CheckedAt = incident.OpenedAt.Add(time.Minute)
				followup.Status = "unknown"
				followup.Reason = "evidence_incomplete_or_unsettled"
				followup.Routes = append([]api.RouteMonitorIncidentTimelineRoute(nil), opening.Routes...)
				followup.Routes[0].Status = "unknown"
				followup.Routes[0].ErrorStatus = "unknown"
				followup.Routes[0].LatencyStatus = "unknown"
				incident.Timeline = []api.RouteMonitorIncidentTimelineEntry{opening, followup}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("unexpected write")
				}
				switch r.URL.Path {
				case "/v1/apps/demo/route-monitor/report":
					writeJSONTest(w, incident.OpeningReport)
				case "/v1/apps/demo/route-monitor/incidents":
					if r.URL.Query().Get("limit") != "5" {
						t.Error("page limit lost")
					}
					writeJSONTest(w, api.RouteMonitorIncidentPage{AppID: incident.AppID, Incidents: []api.RouteMonitorIncident{incident}})
				default:
					if r.URL.Path != "/v1/apps/demo/route-monitor/incidents/"+cliProductionIncident(t).ID {
						t.Error("incident scope lost")
					}
					writeJSONTest(w, incident)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var output bytes.Buffer
			old := osStdout
			osStdout = &output
			t.Cleanup(func() { osStdout = old })
			args := []string{"routes", "monitor", "explain", "demo", "--incident", cliProductionIncident(t).ID}
			if scenario == "report" || scenario == "report_gate" {
				args = []string{"routes", "monitor", "report", "demo"}
				if scenario == "report_gate" {
					args = append(args, "--fail-on-unhealthy")
				}
			}
			if scenario == "incidents" {
				args = []string{"routes", "monitor", "incidents", "demo"}
			}
			if scenario == "json" {
				args = append(args, "--json")
			}
			file := filepath.Join(t.TempDir(), "incident.json")
			if scenario == "export" || scenario == "overwrite" || scenario == "symlink" {
				args = append(args, "--out", file)
			}
			if scenario == "overwrite" {
				if err := os.WriteFile(file, []byte("preserved"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "symlink" {
				if err := os.Symlink("missing-target", file); err != nil {
					t.Fatal(err)
				}
			}
			want := 0
			if strings.HasPrefix(scenario, "wrong_") || scenario == "overwrite" || scenario == "symlink" || scenario == "report_gate" {
				want = 1
			}
			if code := run(args); code != want {
				t.Fatalf("exit %d want %d: %s", code, want, output.String())
			}
			if scenario == "human" && (!strings.Contains(output.String(), "gregale debug requests inspect demo") || !strings.Contains(output.String(), "managed_binding/postgres")) {
				t.Fatal("actionable diagnostics missing")
			}
			if scenario == "timeline" && (!strings.Contains(output.String(), "Impact timeline: 2 observations") || !strings.Contains(output.String(), "errors unknown (was violated)")) {
				t.Fatal("incident impact evolution missing")
			}
			if scenario == "export" {
				info, err := os.Stat(file)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatal("export permissions")
				}
			}
			if scenario == "overwrite" {
				body, _ := os.ReadFile(file)
				if string(body) != "preserved" {
					t.Fatal("existing file changed")
				}
			}
		})
	}
}
func TestProductionRouteMonitorCLIIntentAndEarlyValidation(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	i := cliProductionIncident(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "PUT" || r.URL.Path != "/v1/apps/demo/route-monitor" {
			t.Error("intent path")
		}
		var req api.SetRouteMonitorRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil || !req.Enabled || req.ExpectedRevision == nil || *req.ExpectedRevision != 0 || req.Routes[0].Max5xxRateBPS == nil || *req.Routes[0].Max5xxRateBPS != 0 {
			t.Error("zero budget lost")
		}
		writeJSONTest(w, api.RouteMonitorConfig{AppID: i.AppID, Enabled: true, Revision: 1, UpdatedAt: i.OpeningReport.ObservationAnchor, Routes: req.Routes})
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	path := filepath.Join(t.TempDir(), "routes.json")
	if err := os.WriteFile(path, []byte(`[{"method":"POST","path":"/checkout","max_5xx_rate_bps":0}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"routes", "monitor", "set", "demo", "--mode", "enabled", "--routes", path, "--expected-revision", "0", "--json"}); code != 0 {
		t.Fatal("zero budget rejected")
	}
	for _, args := range [][]string{{"explain", "demo"}, {"incidents", "demo", "--limit", "11"}, {"set", "demo", "--mode", "bad", "--routes", path, "--expected-revision", "0"}, {"explain", "demo", "--incident", "bad"}} {
		before := calls
		if code := cmdRoutesMonitor(args); code != 1 || calls != before {
			t.Fatal("invalid input reached server")
		}
	}
	if err := os.WriteFile(path, []byte(`[{"method":"POST","path":"/checkout","max_p95_ms":100,"typo":1}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRouteMonitorRoutes(path); err == nil {
		t.Fatal("unknown budget field accepted")
	}
}
