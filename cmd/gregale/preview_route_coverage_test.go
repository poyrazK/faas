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
	"github.com/onebox-faas/faas/pkg/routerequirements"
)

const previewCoverageConfig = `version: 2
groups:
  - name: orders
    path_prefix: /orders/
    methods: [POST]
    require:
      authentication: consumer
      throttle: {key_by: consumer_id, max_rps: 10, missing_key_policy: reject}
      budget: {explicit: true, max_ms: 2000}
public:
  - method: GET
    path: /health
    reason: secret-public-rationale
`

const previewCoverageContract = `{"openapi":"3.1.0","info":{"title":"demo","version":"1"},"paths":{"/orders/{id}":{"post":{"responses":{"200":{"description":"ok"}}}},"/health":{"get":{"responses":{"200":{"description":"ok"}}}}}}`

func TestPreviewCoverageCLIUsesCandidateInventoryAndReadOnlyGate(t *testing.T) {
	for _, test := range []struct {
		name, selector, status                                            string
		baselineMissing, candidateMissing, addedUncovered, serverBasePath bool
		gate                                                              bool
		exit                                                              int
	}{
		{name: "full family", selector: "/orders/*", status: "satisfied", gate: true},
		{name: "baseline unavailable", selector: "/orders/*", status: "satisfied", baselineMissing: true, gate: true},
		{name: "one sample", selector: "/orders/x", status: "unknown", gate: true, exit: 1},
		{name: "missing policy", selector: "/other/*", status: "violated", gate: true, exit: 1},
		{name: "new unassigned operation", selector: "/orders/*", status: "violated", addedUncovered: true, gate: true, exit: 1},
		{name: "unavailable candidate", selector: "/orders/*", status: "unknown", candidateMissing: true, gate: true, exit: 1},
		{name: "server base path unavailable", selector: "/orders/*", status: "unknown", serverBasePath: true, gate: true, exit: 1},
		{name: "gate opt in", selector: "/orders/x", status: "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			configPath := filepath.Join(t.TempDir(), "coverage.yaml")
			if err := os.WriteFile(configPath, []byte(previewCoverageConfig), 0o600); err != nil {
				t.Fatal(err)
			}
			var reads []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads = append(reads, r.URL.Path)
				if r.Method != http.MethodGet {
					t.Errorf("coverage mutated state: %s %s", r.Method, r.URL.Path)
				}
				switch r.URL.Path {
				case "/v1/preview/pr-42-api":
					writeJSONTest(w, api.PreviewResourceResponse{App: api.AppResponse{ID: "preview-id", Slug: "pr-42-api", PreviewOfSlug: "api"}, Parent: &api.AppResponse{ID: "parent-id", Slug: "api"}, LatestDeployment: &api.DeploymentResponse{ID: "candidate", Status: "live"}, ProductionDeployment: &api.DeploymentResponse{ID: "baseline", Status: "live"}})
				case "/v1/apps/api/deployments/baseline/openapi":
					if test.baselineMissing {
						http.NotFound(w, r)
						return
					}
					// Removed baseline operations and policy-observed operations
					// must never enter the candidate requirement inventory.
					writePreviewReportDoc(t, w, "baseline", strings.Replace(previewCoverageContract, `"/health":`, `"/removed":{"get":{"responses":{}}},"/health":`, 1))
				case "/v1/apps/pr-42-api/deployments/candidate/openapi":
					if test.candidateMissing {
						http.NotFound(w, r)
						return
					}
					contract := previewCoverageContract
					if test.serverBasePath {
						contract = strings.Replace(contract, `"paths":`, `"servers":[{"url":"/secret-prefix"}],"paths":`, 1)
					}
					if test.addedUncovered {
						contract = strings.Replace(contract, `"/health":`, `"/new":{"get":{"responses":{}}},"/health":`, 1)
					}
					writePreviewReportDoc(t, w, "candidate", contract)
				case "/v1/apps/api/deployments/baseline/route-policy":
					writePreviewPolicySnapshotTest(w, "baseline", "parent-id", []api.EdgeRuleResponse{})
				case "/v1/apps/pr-42-api/deployments/candidate/route-policy":
					writePreviewPolicySnapshotTest(w, "candidate", "preview-id", []api.EdgeRuleResponse{})
				case "/v1/apps/pr-42-api/openapi/preview":
					writeJSONTest(w, api.AppOpenAPIPolicyPreviewResponse{Routes: []api.AppOpenAPIPolicyPreviewRoute{{Method: "get", Path: "/observed-only"}}})
				case "/v1/apps/pr-42-api":
					writeJSONTest(w, api.AppResponse{ID: "preview-id", Slug: "pr-42-api", URL: "https://pr-42-api.gregale.dev", ConsumerAuthMode: "required", RequestTimeoutS: 2, EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 10000, RequestBudgetMaxMS: 20000}})
				case "/v1/apps/api/edge-rules":
					writeJSONTest(w, []api.EdgeRuleResponse{})
				case "/v1/apps/pr-42-api/edge-rules":
					writeJSONTest(w, []api.EdgeRuleResponse{{ID: "consumer-limit", AppID: "preview-id", Enabled: true, Kind: "throttle", MatchHost: "*.gregale.dev", MatchPath: test.selector, Priority: 1, Action: json.RawMessage(`{"throttle":{"requests_per_second":10,"burst":20,"key_by":"consumer_id","missing_key_policy":"reject"}}`)}})
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
			args := []string{"report", "pr-42-api", "--requirements", configPath}
			if test.gate {
				args = append(args, "--fail-on-requirements")
			}
			if code := cmdPreview(args); code != test.exit {
				t.Fatalf("exit %d want %d: %s", code, test.exit, out.String())
			}
			var report previewRouteReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Version != 7 || report.Requirements == nil || report.Requirements.Version != 2 || report.Requirements.Status != test.status || report.Requirements.Coverage.Deployment != "candidate" {
				t.Fatalf("coverage=%+v", report.Requirements)
			}
			if len(reads) != 10 {
				t.Fatalf("unexpected reads %v", reads)
			}
			if !test.candidateMissing && !test.serverBasePath && (report.Requirements.Coverage.Status != "available" || len(report.Requirements.Coverage.SHA256) != 64) {
				t.Fatal(report.Requirements.Coverage)
			}
			for _, route := range report.Requirements.Routes {
				if route.Path == "/removed" || route.Path == "/observed-only" {
					t.Fatal("promoted noncandidate route", route)
				}
			}
			if strings.Contains(out.String(), "secret-") || strings.Contains(out.String(), `"action"`) {
				t.Fatal("report leaked local rationale or actions")
			}
			if test.status == "violated" || test.candidateMissing || test.serverBasePath {
				found := false
				for _, review := range report.ReviewPriorities {
					if review.Scope == "route_coverage" || containsString(review.Reasons, "declared_requirement_violated") {
						found = true
					}
				}
				if !found {
					t.Fatal("coverage finding absent from review queue", report.ReviewPriorities)
				}
			}
		})
	}
}

func TestPreviewCoveragePlannerRejectsV2BeforeNetwork(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	path := filepath.Join(t.TempDir(), "coverage.yaml")
	if err := os.WriteFile(path, []byte(previewCoverageConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.NotFound(w, r) }))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	if code := cmdRoutes([]string{"plan", "pr-42-api", "--requirements", path}); code != 1 || calls != 0 {
		t.Fatalf("planner admitted preview-only config: exit=%d calls=%d", code, calls)
	}
}

func TestPreviewCoverageGroupReviewAndRendering(t *testing.T) {
	config, err := routerequirements.ParsePreview([]byte(`{"version":2,"groups":[{"name":"<script>|group","path_prefix":"/admin/","methods":["GET"],"require":{"authentication":"consumer"}}],"public":[{"method":"GET","path":"/health","reason":"secret-rationale"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	result := routerequirements.EvaluatePreview(config, "digest", routerequirements.Context{}, routerequirements.CoverageInventory{Status: "available", Source: "captured_candidate_contract", Routes: []routerequirements.CapturedRoute{{Method: "GET", Path: "/health"}}})
	report := previewRouteReport{Outcome: "policy_violations", Requirements: &result}
	prioritizePreviewRouteReview(&report)
	if len(report.ReviewPriorities) != 1 || report.ReviewPriorities[0].Priority != "blocker" || report.ReviewPriorities[0].Reasons[0] != "group_no_routes" {
		t.Fatal(report.ReviewPriorities)
	}
	var out bytes.Buffer
	renderPreviewRequirements(&out, &result, true)
	if strings.Contains(out.String(), "<script>") || strings.Contains(out.String(), "secret-") || !strings.Contains(out.String(), "group_no_routes") || !strings.Contains(out.String(), "public_exception") {
		t.Fatal(out.String())
	}
}

func TestPreviewCoveragePassingSamplesCannotEraseFamilyViolation(t *testing.T) {
	config, err := routerequirements.ParsePreview([]byte(previewCoverageConfig))
	if err != nil {
		t.Fatal(err)
	}
	result := routerequirements.EvaluatePreview(config, "digest", routerequirements.Context{App: api.AppResponse{ConsumerAuthMode: "required", RequestTimeoutS: 2, EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 10000, RequestBudgetMaxMS: 20000}}}, routerequirements.CoverageInventory{Status: "available", Source: "captured_candidate_contract", Routes: []routerequirements.CapturedRoute{{Method: "POST", Path: "/orders/{id}"}, {Method: "GET", Path: "/health"}}})
	report := previewRouteReport{Outcome: "policy_violations", Requirements: &result, Routes: []previewReportRoute{{Method: "POST", Path: "/orders/{id}", RouteSource: "captured_deployment_contract", TestProfiles: []previewReportTest{{Passed: 1}}}}}
	prioritizePreviewRouteReview(&report)
	if len(report.ReviewPriorities) != 1 || report.ReviewPriorities[0].Priority != "blocker" || report.ReviewPriorities[0].CandidateChecks != "passed_samples" || !containsString(report.ReviewPriorities[0].Reasons, "declared_requirement_violated") {
		t.Fatal(report.ReviewPriorities)
	}
}
