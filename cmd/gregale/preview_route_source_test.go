package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeimpact"
	"github.com/onebox-faas/faas/pkg/routerequirements"
)

func previewSourceFixture() routeimpact.Report {
	route := routeimpact.Route{Method: "GET", Path: "/users/{user_id}", Handler: "user", HandlerSymbol: "main.user", Source: routeimpact.Location{File: "main.py", Line: 5}, Registration: routeimpact.Location{File: "main.py", Line: 4}, ContextFiles: []string{"main.py"}, DependencyFiles: []string{}, DependencySymbols: []string{}, FallbackFiles: []string{}}
	return routeimpact.Report{Version: 2, Framework: "fastapi", Repository: "github.com/team/service", SourceRoot: ".", Status: "complete", Scope: "secret-scope-never-exported", App: "untrusted-display-label",
		Base:         routeimpact.Snapshot{Revision: strings.Repeat("a", 40), SourceSHA256: strings.Repeat("1", 64), PythonFiles: 1},
		Candidate:    routeimpact.Snapshot{Revision: strings.Repeat("b", 40), SourceSHA256: strings.Repeat("2", 64), PythonFiles: 1},
		ChangedFiles: []routeimpact.FileChange{{File: "main.py", Change: "modified"}}, ChangedSymbols: []routeimpact.SymbolChange{}, Issues: []routeimpact.Issue{}, Summary: routeimpact.Summary{SourceChanged: 1},
		Routes: []routeimpact.Result{{Method: route.Method, Path: route.Path, Change: "source_changed", Precision: "function", Before: &route, After: &route, Uncertainties: []routeimpact.Issue{}, Evidence: []routeimpact.Evidence{{File: "helper.py", Change: "modified", Revision: "candidate", Kind: "function_reference", Symbol: "helper.lookup", Line: 1, Via: []string{}, ViaSymbols: []routeimpact.SymbolLocation{{Name: "main.user", File: "main.py", Line: 5}, {Name: "helper.lookup", File: "helper.py", Line: 1}}}}}},
	}
}

func previewSourceDeploymentFixture(commit, appID string) *api.DeploymentResponse {
	return &api.DeploymentResponse{ID: "deployment-" + appID, AppID: appID, Status: "live", CommitSHA: commit, SourceURL: "https://user:secret-origin@github.com/Team/Service.git", SourceRoot: ".", SourceSHA256: strings.Repeat("9", 64)}
}

func previewSourceReportFixture() previewRouteReport {
	source := previewSourceFixture()
	row := newPreviewReportRoute("GET", "/users/{id}")
	row.Change, row.RouteSource = "unchanged", "captured_deployment_contract"
	row.TestProfiles = []previewReportTest{{Profile: "warm", Passed: 1}}
	row.BaselineTraffic = &previewReportTraffic{Requests: 100, From: "start", Until: "end"}
	row.PolicyDrift = &previewRoutePolicyDrift{Status: "unchanged", Changes: []previewRoutePolicyRuleChange{}}
	return previewRouteReport{Version: 6, Outcome: "no_findings", PolicyDrift: previewRoutePolicyDriftEvidence{Status: "available", Scope: "current_app_pair"}, Routes: []previewReportRoute{*row},
		baselineSource:  previewSourceDeployment(previewSourceDeploymentFixture(source.Base.Revision, "parent"), "parent"),
		candidateSource: previewSourceDeployment(previewSourceDeploymentFixture(source.Candidate.Revision, "preview"), "preview"),
	}
}

func TestPreviewSourceBindsDeclaredMetadataAndKeepsContractClassification(t *testing.T) {
	report, source := previewSourceReportFixture(), previewSourceFixture()
	attachPreviewSourceImpact(&report, source, "digest")
	prioritizePreviewRouteReview(&report)
	if report.SourceImpact.CandidateRevision != source.Candidate.Revision || report.SourceImpact.Repository != source.Repository || report.SourceImpact.SourceRoot != source.SourceRoot {
		t.Fatal("analyzed provenance absent from the joined report")
	}
	if report.Version != 6 || report.SourceImpact.Status != "aligned" || report.SourceImpact.MappingStatus != "complete" || report.SourceImpact.Base.Status != "declared_match" {
		t.Fatalf("binding=%+v", report.SourceImpact)
	}
	row := report.Routes[0]
	if row.Change != "unchanged" || row.SourceImpact == nil || row.SourceImpact.Match != "parameter_names" || len(row.SourceImpact.Evidence[0].ViaSymbols) != 2 {
		t.Fatalf("row=%+v", row)
	}
	if report.Outcome != "review_required" || len(report.ReviewPriorities) != 1 || report.ReviewPriorities[0].Priority != "review" || report.ReviewPriorities[0].CandidateChecks != "passed_samples" {
		t.Fatalf("priorities=%+v outcome=%s", report.ReviewPriorities, report.Outcome)
	}
	// Neither the display label nor the Python fingerprint establishes identity.
	// Archive SHA is intentionally different, so metadata agreement still binds.
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-origin", "secret-scope", "untrusted-display-label"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
}

func TestPreviewSourceProvenanceMismatchIsExplicitAndDoesNotJoinEvidence(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*routeimpact.Report, *api.DeploymentResponse)
		reason string
	}{
		{"working tree", func(s *routeimpact.Report, _ *api.DeploymentResponse) { s.Candidate.Revision = "working-tree" }, "working_tree_candidate"},
		{"legacy repository missing", func(s *routeimpact.Report, _ *api.DeploymentResponse) { s.Repository = "" }, "source_repository_unavailable"},
		{"different repository", func(s *routeimpact.Report, _ *api.DeploymentResponse) { s.Repository = "github.com/team/other" }, "repository_mismatch"},
		{"different revision", func(s *routeimpact.Report, _ *api.DeploymentResponse) { s.Candidate.Revision = strings.Repeat("c", 40) }, "revision_mismatch"},
		{"different build root", func(s *routeimpact.Report, _ *api.DeploymentResponse) { s.SourceRoot = "service" }, "source_root_mismatch"},
		{"wrong app", func(_ *routeimpact.Report, d *api.DeploymentResponse) { d.AppID = "other" }, "deployment_identity_unavailable"},
		{"missing deployment identity", func(_ *routeimpact.Report, d *api.DeploymentResponse) { d.ID = "" }, "deployment_identity_unavailable"},
		{"missing repository annotation", func(_ *routeimpact.Report, d *api.DeploymentResponse) { d.SourceURL = "" }, "repository_unavailable"},
		{"unsupported repository", func(_ *routeimpact.Report, d *api.DeploymentResponse) {
			d.SourceURL = "https://gitlab.com/team/service"
		}, "repository_unavailable"},
		{"abbreviated annotation", func(_ *routeimpact.Report, d *api.DeploymentResponse) { d.CommitSHA = "bbbbbbb" }, "revision_unavailable"},
		{"inconsistent reference", func(_ *routeimpact.Report, d *api.DeploymentResponse) {
			d.SourceURL = "github://team/service@" + strings.Repeat("c", 40)
		}, "source_reference_conflict"},
		{"unsafe root", func(_ *routeimpact.Report, d *api.DeploymentResponse) { d.SourceRoot = "../service" }, "source_root_invalid"},
		{"noncanonical root", func(_ *routeimpact.Report, d *api.DeploymentResponse) { d.SourceRoot = " . " }, "source_root_invalid"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			source, report := previewSourceFixture(), previewSourceReportFixture()
			deployment := previewSourceDeploymentFixture(source.Candidate.Revision, "preview")
			test.mutate(&source, deployment)
			report.candidateSource = previewSourceDeployment(deployment, "preview")
			attachPreviewSourceImpact(&report, source, "digest")
			prioritizePreviewRouteReview(&report)
			if report.SourceImpact.CandidateRevision != source.Candidate.Revision || report.SourceImpact.Repository != source.Repository || report.SourceImpact.SourceRoot != source.SourceRoot {
				t.Fatal("lost analyzed provenance needed to resolve the mismatch")
			}
			if report.SourceImpact.Status != "unbound" || report.SourceImpact.Candidate.Reason != test.reason || report.Routes[0].SourceImpact != nil || len(report.SourceImpact.Unmatched) != 1 || report.Outcome != "incomplete" {
				t.Fatalf("report=%+v source=%+v", report, report.SourceImpact)
			}
			for _, item := range report.ReviewPriorities {
				if item.SourceChange != "" || item.BaselineTraffic != nil || item.CandidateChecks != "unbound" {
					t.Fatalf("unbound analysis inherited deployment evidence: %+v", item)
				}
			}
		})
	}
	// One matching side is insufficient.
	report, source := previewSourceReportFixture(), previewSourceFixture()
	report.baselineSource.commit = strings.Repeat("c", 40)
	attachPreviewSourceImpact(&report, source, "digest")
	if report.SourceImpact.Status != "unbound" || report.SourceImpact.Base.Reason != "revision_mismatch" || report.Routes[0].SourceImpact != nil {
		t.Fatal("baseline mismatch joined evidence")
	}
}

func TestPreviewSourceRouteMappingIsConservative(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*previewRouteReport, *routeimpact.Report)
		reason string
	}{
		{"observed only", func(r *previewRouteReport, _ *routeimpact.Report) {
			r.Routes[0].RouteSource = "current_policy_or_observation"
		}, "captured_route_missing"},
		{"missing captured route", func(r *previewRouteReport, _ *routeimpact.Report) { r.Routes = nil }, "captured_route_missing"},
		{"ambiguous captured templates", func(r *previewRouteReport, _ *routeimpact.Report) {
			extra := r.Routes[0]
			extra.Path = "/users/{key}"
			r.Routes = append(r.Routes, extra)
		}, "ambiguous_route_template"},
		{"ambiguous static templates", func(_ *previewRouteReport, s *routeimpact.Report) {
			extra := s.Routes[0]
			extra.Path = "/users/{key}"
			s.Routes = append(s.Routes, extra)
		}, "ambiguous_route_template"},
		{"inline parameter", func(_ *previewRouteReport, s *routeimpact.Report) { s.Routes[0].Path = "/users/x-{id}" }, "unsupported_route_template"},
		{"converter", func(_ *previewRouteReport, s *routeimpact.Report) { s.Routes[0].Path = "/users/{path:path}" }, "unsupported_route_template"},
		{"encoded path", func(_ *previewRouteReport, s *routeimpact.Report) { s.Routes[0].Path = "/users/%7Bid%7D" }, "unsupported_route_template"},
		{"query", func(_ *previewRouteReport, s *routeimpact.Report) { s.Routes[0].Path = "/users/{id}?x=1" }, "unsupported_route_template"},
		{"presence conflict", func(r *previewRouteReport, _ *routeimpact.Report) { r.Routes[0].Change = "added" }, "route_presence_conflict"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			report, source := previewSourceReportFixture(), previewSourceFixture()
			test.mutate(&report, &source)
			attachPreviewSourceImpact(&report, source, "digest")
			prioritizePreviewRouteReview(&report)
			if report.SourceImpact.MappingStatus != "partial" || len(report.SourceImpact.Unmatched) == 0 || report.SourceImpact.Unmatched[0].Reason != test.reason {
				t.Fatalf("source=%+v", report.SourceImpact)
			}
			for _, row := range report.Routes {
				if row.SourceImpact != nil {
					t.Fatal("ambiguous/unsupported mapping attached")
				}
			}
		})
	}
	for _, change := range []string{"added", "removed"} {
		report, source := previewSourceReportFixture(), previewSourceFixture()
		report.Routes[0].Change, source.Routes[0].Change = change, change
		if change == "added" {
			source.Routes[0].Before = nil
		} else {
			source.Routes[0].After = nil
		}
		attachPreviewSourceImpact(&report, source, "digest")
		if report.Routes[0].SourceImpact == nil {
			t.Fatalf("failed %s presence match", change)
		}
	}
}

func TestPreviewSourceReviewUsesChecksAndRequirementsWithoutClaimingTemplateCoverage(t *testing.T) {
	for _, test := range []struct{ name, requirement, checks, wantPriority, wantChecks string }{
		{"missing candidate checks", "not_supplied", "missing", "needs_evidence", "missing"},
		{"source receipts supplemental", "not_supplied", "supplemental", "needs_evidence", "missing"},
		{"candidate failed", "not_supplied", "failed", "blocker", "failed"},
		{"passing samples", "not_supplied", "passed", "review", "passed_samples"},
		{"known requirement violation", "violated", "passed", "blocker", "passed_samples"},
		{"unknown requirement", "unknown", "passed", "needs_evidence", "passed_samples"},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := previewSourceReportFixture().Routes[0]
			row.SourceImpact = &previewRouteSource{Change: "potentially_affected", Precision: "function"}
			switch test.checks {
			case "missing":
				row.TestProfiles = nil
			case "supplemental":
				row.TestProfiles = nil
				row.SourceTestProfiles = []previewReportTest{{Passed: 1}}
			case "failed":
				row.TestProfiles = []previewReportTest{{Failed: 1}}
			}
			item, relevant := previewSourceReviewRoute(row, test.requirement)
			if !relevant || item.Priority != test.wantPriority || item.CandidateChecks != test.wantChecks {
				t.Fatalf("review=%+v relevant=%v", item, relevant)
			}
		})
	}
	report := previewSourceReportFixture()
	report.Requirements = &routerequirements.PreviewReport{Report: routerequirements.Report{Routes: []api.RouteRequirementsResult{{Method: "GET", Path: "/users/42", Status: "violated"}}}}
	if status := previewReviewRequirementStatus(&report, report.Routes[0]); status != "not_established" {
		t.Fatal("one concrete path became template coverage")
	}
	row := report.Routes[0]
	row.Path = "/users/42"
	if status := previewReviewRequirementStatus(&report, row); status != "violated" {
		t.Fatal("exact request requirement was lost")
	}
	row.Change, row.TestProfiles = "removed", nil
	item, _ := previewSourceReviewRoute(row, "not_supplied")
	if item.Priority != "blocker" || item.CandidateChecks != "route_removed" || strings.Contains(strings.Join(item.Reasons, ","), "candidate_checks_missing") {
		t.Fatalf("removed=%+v", item)
	}
	row = previewSourceReportFixture().Routes[0]
	item, relevant := previewSourceReviewRoute(row, "unknown")
	if !relevant || item.Priority != "needs_evidence" || len(item.NextActions) == 0 {
		t.Fatal("unknown requirement did not produce a missing-evidence action")
	}
}

func TestPreviewSourceReviewOrderingAndIncompleteAnalysis(t *testing.T) {
	report := previewSourceReportFixture()
	report.SourceImpact = &previewSourceImpact{Status: "aligned", AnalysisStatus: "complete", MappingStatus: "complete"}
	report.Routes = nil
	for _, value := range []struct {
		path     string
		requests int64
		checks   int
	}{{"/low-blocker", 1, -1}, {"/missing", 20, 0}, {"/unknown-traffic", -1, 0}, {"/high-review", 10000, 1}, {"/high-missing", 100, 0}} {
		row := previewSourceReportFixture().Routes[0]
		row.Path = value.path
		row.SourceImpact = &previewRouteSource{Change: "source_changed"}
		row.TestProfiles = nil
		if value.checks < 0 {
			row.TestProfiles = []previewReportTest{{Failed: 1}}
		} else if value.checks > 0 {
			row.TestProfiles = []previewReportTest{{Passed: 1}}
		}
		row.BaselineTraffic = nil
		if value.requests >= 0 {
			row.BaselineTraffic = &previewReportTraffic{Requests: value.requests}
		}
		report.Routes = append(report.Routes, row)
	}
	prioritizePreviewRouteReview(&report)
	for index, path := range []string{"/low-blocker", "/high-missing", "/missing", "/unknown-traffic", "/high-review"} {
		if report.ReviewPriorities[index].Path != path {
			t.Fatalf("order=%+v", report.ReviewPriorities)
		}
	}
	// An uncertain assembly can contain no registered routes at all.
	report = previewSourceReportFixture()
	report.Routes = nil
	report.SourceImpact = &previewSourceImpact{Status: "aligned", AnalysisStatus: "incomplete", MappingStatus: "complete"}
	prioritizePreviewRouteReview(&report)
	if report.Outcome != "incomplete" || len(report.ReviewPriorities) != 1 || report.ReviewPriorities[0].Scope != "source_analysis" {
		t.Fatalf("incomplete=%+v", report)
	}
	for _, outcome := range []string{"breaking_changes", "test_failures", "policy_violations"} {
		report.Outcome = outcome
		prioritizePreviewRouteReview(&report)
		if report.Outcome != outcome {
			t.Fatal("source status hid known findings")
		}
	}
	row := previewSourceReportFixture().Routes[0]
	row.SourceImpact = &previewRouteSource{Change: "no_linked_changes"}
	if _, relevant := previewSourceReviewRoute(row, "not_supplied"); relevant {
		t.Fatal("unchanged source created review work")
	}
}

func writePreviewSourceFixture(t *testing.T, source routeimpact.Report) (string, []byte) {
	t.Helper()
	body, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "impact.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, body
}

func TestPreviewSourceInputRejectedBeforeNetwork(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reads++; http.NotFound(w, r) }))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	var out bytes.Buffer
	oldErr, oldOut := osStderr, osStdout
	osStderr, osStdout = &out, &out
	t.Cleanup(func() { osStderr, osStdout = oldErr, oldOut })
	path, _ := writePreviewSourceFixture(t, previewSourceFixture())
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	invalid := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalid, []byte(`{"secret-value":"private"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{link, invalid, filepath.Join(t.TempDir(), "missing")} {
		out.Reset()
		if code := cmdPreviewReport([]string{"pr-42-api", "--source-impact", input}); code != 1 || reads != 0 || strings.Contains(out.String(), "secret-value") {
			t.Fatalf("code=%d reads=%d output=%s", code, reads, out.String())
		}
	}
}

func TestPreviewSourceReportCommandAndMarkdown(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	source := previewSourceFixture()
	source.Status = "incomplete"
	source.Issues = []routeimpact.Issue{{Code: "dynamic_call", Message: "secret-issue-never-exported"}}
	path, body := writePreviewSourceFixture(t, source)
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != http.MethodGet {
			t.Errorf("mutated state: %s", r.Method)
		}
		switch r.URL.Path {
		case "/v1/preview/pr-42-api":
			writeJSONTest(w, api.PreviewResourceResponse{App: api.AppResponse{ID: "preview", Slug: "pr-42-api", PreviewOfSlug: "api"}, Parent: &api.AppResponse{ID: "parent", Slug: "api"}, LatestDeployment: previewSourceDeploymentFixture(source.Candidate.Revision, "preview"), ProductionDeployment: previewSourceDeploymentFixture(source.Base.Revision, "parent")})
		case "/v1/apps/api/deployments/deployment-parent/openapi":
			writePreviewReportDoc(t, w, "deployment-parent", previewReportBefore)
		case "/v1/apps/pr-42-api/deployments/deployment-preview/openapi":
			writePreviewReportDoc(t, w, "deployment-preview", previewReportBefore)
		case "/v1/apps/pr-42-api/openapi/preview":
			writeJSONTest(w, api.AppOpenAPIPolicyPreviewResponse{})
		case "/v1/apps/api/edge-rules", "/v1/apps/pr-42-api/edge-rules":
			writeJSONTest(w, []api.EdgeRuleResponse{})
		case "/v1/apps/api/analytics":
			writeJSONTest(w, previewReportAnalytics("deployment-parent", 100, 42))
		case "/v1/apps/pr-42-api/analytics":
			writeJSONTest(w, previewReportAnalytics("deployment-preview", 100, 1))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	jsonOutput = true
	if code := cmdPreviewReport([]string{"pr-42-api", "--source-impact", path, "--fail-on-incomplete"}); code != 1 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
	var report previewRouteReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if reads != 8 || report.Version != 6 || report.SourceImpact.SHA256 != fmt.Sprintf("%x", sha256.Sum256(body)) || report.SourceImpact.Status != "aligned" || report.Outcome != "incomplete" {
		t.Fatalf("reads=%d report=%+v", reads, report)
	}
	for _, secret := range []string{"secret-origin", "secret-scope", "secret-issue"} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
	out.Reset()
	renderPreviewRouteReport(&out, report, true)
	for _, expected := range []string{"Source impact and review priorities", "needs_evidence", "helper.py:1", "main.user", "declared metadata alignment"} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("missing %s: %s", expected, out.String())
		}
	}
	out.Reset()
	if code := cmdPreviewReport([]string{"pr-42-api", "--source-impact", path, "--fail-on-breaking"}); code != 0 {
		t.Fatalf("source changes alone triggered the contract-break gate: %d; %s", code, out.String())
	}
	// Escape syntax in source labels and reference filenames in shareable Markdown.
	for index := range report.Routes {
		if report.Routes[index].SourceImpact != nil {
			report.Routes[index].Path = "/users/[x]|<script>"
			report.Routes[index].SourceImpact.After.File = "[x]|<script>.py"
		}
	}
	out.Reset()
	renderPreviewSourceReview(&out, report, true)
	if strings.Contains(out.String(), "<script>") || strings.Contains(out.String(), "/users/[x]|") {
		t.Fatalf("unescaped markdown: %s", out.String())
	}
}

func TestPreviewSourceSelectionRequiresMatchingResourceIdentities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/preview/") {
			writeJSONTest(w, api.PreviewResourceResponse{App: api.AppResponse{Slug: "different", PreviewOfSlug: "api"}, Parent: &api.AppResponse{Slug: "api"}})
		} else {
			writeJSONTest(w, api.DeploymentResponse{ID: "different", AppID: "parent"})
		}
	}))
	defer srv.Close()
	client := api.NewClient(srv.URL, "token")
	if _, err := previewReportBaseline(t.Context(), client, "requested", "parent"); err == nil {
		t.Fatal("accepted a different baseline deployment")
	}
	if _, err := collectPreviewRouteReport(t.Context(), client, "pr-42-api", "", "24h"); err == nil {
		t.Fatal("accepted a different preview app")
	}
}
