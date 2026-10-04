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

const previewRequirementsConfig = `version: 1
routes:
  - name: checkout
    method: POST
    path: /checkout
    require:
      authentication: consumer
      throttle: {key_by: consumer_id, max_rps: 10, missing_key_policy: reject}
      budget: {explicit: true, max_ms: 2000}
`

// ADR-436: CI gates only requested requirements, preserves output, and performs account-scoped reads.
func TestPreviewRequirementsCLIReadOnlyGateAndEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, keyBy, status      string
		gate, conditional        bool
		appFailure, rulesFailure bool
		wantExit                 int
	}{
		{name: "satisfied despite unavailable other evidence", keyBy: "consumer_id", status: "satisfied", gate: true, wantExit: 0},
		{name: "shared limiter gate", keyBy: "none", status: "violated", gate: true, wantExit: 1},
		{name: "findings without gate", keyBy: "none", status: "violated", wantExit: 0},
		{name: "conditional limiter", keyBy: "consumer_id", status: "unknown", gate: true, conditional: true, wantExit: 1},
		{name: "rules unavailable", status: "unknown", gate: true, rulesFailure: true, wantExit: 1},
		{name: "app unavailable", status: "unknown", gate: true, appFailure: true, wantExit: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			path := filepath.Join(t.TempDir(), "routes.yaml")
			if err := os.WriteFile(path, []byte(previewRequirementsConfig), 0o600); err != nil {
				t.Fatal(err)
			}
			var reads []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads = append(reads, r.URL.Path)
				if r.Method != http.MethodGet {
					t.Errorf("requirements mutated state: %s %s", r.Method, r.URL.Path)
				}
				switch r.URL.Path {
				case "/v1/preview/pr-42-api":
					writeJSONTest(w, api.PreviewResourceResponse{
						App: api.AppResponse{ID: "preview-id", Slug: "pr-42-api", PreviewOfSlug: "api"}, Parent: &api.AppResponse{ID: "parent-id", Slug: "api"},
						LatestDeployment: &api.DeploymentResponse{ID: "candidate", Status: "live"}, ProductionDeployment: &api.DeploymentResponse{ID: "baseline", Status: "live"},
					})
				case "/v1/apps/api/deployments/baseline/route-policy":
					writePreviewPolicySnapshotTest(w, "baseline", "parent-id", []api.EdgeRuleResponse{})
				case "/v1/apps/pr-42-api/deployments/candidate/route-policy":
					writePreviewPolicySnapshotTest(w, "candidate", "preview-id", []api.EdgeRuleResponse{})
				case "/v1/apps/pr-42-api":
					if tc.appFailure {
						http.NotFound(w, r)
						return
					}
					writeJSONTest(w, api.AppResponse{ID: "preview-id", Slug: "pr-42-api", URL: "https://pr-42-api.gregale.dev", ConsumerAuthMode: "required", RequestTimeoutS: 2,
						EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 10000, RequestBudgetMaxMS: 20000}})
				case "/v1/apps/api/edge-rules":
					writeJSONTest(w, []api.EdgeRuleResponse{})
				case "/v1/apps/pr-42-api/edge-rules":
					if tc.rulesFailure {
						http.NotFound(w, r)
						return
					}
					action := map[string]any{"requests_per_second": 10, "burst": 20, "key_by": tc.keyBy}
					if tc.keyBy != "none" {
						action["missing_key_policy"] = "reject"
					}
					encoded, _ := json.Marshal(map[string]any{"throttle": action})
					rule := api.EdgeRuleResponse{ID: "limit-rule", AppID: "preview-id", Enabled: true, Kind: "throttle", MatchHost: "*.gregale.dev", MatchPath: "/*", Priority: 1, Action: encoded}
					if tc.conditional {
						rule.MatchHeaders = map[string]string{"Authorization": "Bearer secret-selector"}
					}
					writeJSONTest(w, []api.EdgeRuleResponse{rule})
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
			args := []string{"report", "pr-42-api", "--requirements", path}
			if tc.gate {
				args = append(args, "--fail-on-requirements")
			}
			if code := cmdPreview(args); code != tc.wantExit {
				t.Fatalf("exit = %d, want %d: %s", code, tc.wantExit, out.String())
			}
			var report previewRouteReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Requirements == nil || report.Requirements.Status != tc.status || len(report.Requirements.SHA256) != 64 || report.Requirements.PolicyScope != "current_app" {
				t.Fatalf("requirements = %+v", report.Requirements)
			}
			if strings.Contains(out.String(), "secret-selector") || strings.Contains(out.String(), "match_headers") || strings.Contains(out.String(), `"action"`) {
				t.Fatal("report included raw rule configuration")
			}
			if tc.status == "satisfied" && report.Outcome != "incomplete" {
				t.Fatalf("requirements should not invent contract/test evidence: %s", report.Outcome)
			}
			if tc.status == "violated" && report.Outcome != "policy_violations" {
				t.Fatalf("missing policy outcome: %s", report.Outcome)
			}
			for _, read := range reads {
				if strings.Contains(read, "/v1/account") {
					t.Fatal("requirements introduced an unrelated account lookup")
				}
			}
		})
	}
}

func TestPreviewRequirementsInvalidConfigFailsBeforeNetwork(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.NotFound(w, r) }))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	path := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nroutes: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"pr-42-api", "--fail-on-requirements"}, {"pr-42-api", "--requirements", path}} {
		if code := cmdPreviewReport(args); code != 1 {
			t.Fatalf("%v: exit = %d", args, code)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid inputs made %d API calls", calls)
	}
}

func TestPreviewRequirementsHostAndRuleIdentity(t *testing.T) {
	for _, raw := range []string{"", "http://app.example", "https://user:secret@app.example", "https://app.example/path", "https://app.example?token=secret", "https://app.example:8443", "https://app.example#fragment"} {
		if _, reason := previewRequirementsHost(api.AppResponse{Slug: "pr-42-api", URL: raw}); reason != "platform_host_unavailable" {
			t.Errorf("accepted non-platform origin %q", raw)
		}
	}
	config, _, err := readPreviewRouteRequirements(writePreviewRequirementsFile(t))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/pr-42-api" {
			writeJSONTest(w, api.AppResponse{ID: "preview-id", Slug: "pr-42-api", URL: "https://pr-42-api.gregale.dev", ConsumerAuthMode: "required"})
		} else {
			writeJSONTest(w, []api.EdgeRuleResponse{{ID: "other-rule", AppID: "other-app"}})
		}
	}))
	defer srv.Close()
	report := previewRouteReport{Preview: "pr-42-api"}
	attachPreviewRouteRequirements(t.Context(), api.NewClient(srv.URL, "token"), &report, config, "digest")
	if report.Requirements.Status != "unknown" || report.Requirements.Routes[0].Checks[0].Code != "rule_identity_unavailable" {
		t.Fatalf("accepted cross-app rule evidence: %+v", report.Requirements)
	}
}

func writePreviewRequirementsFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "routes.yaml")
	if err := os.WriteFile(path, []byte(previewRequirementsConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPreviewRequirementsCustomerFileAndMarkdown(t *testing.T) {
	path := writePreviewRequirementsFile(t)
	link := filepath.Join(t.TempDir(), "link.yaml")
	if err := os.Symlink(path, link); err == nil {
		if _, _, err := readPreviewRouteRequirements(link); err == nil {
			t.Fatal("requirements followed a symlink")
		}
	}
	report := &routerequirements.Report{Status: "unknown", Host: "pr-42-api.gregale.dev", PolicyScope: "current_app", Scope: routerequirements.Scope,
		Routes: []routerequirements.RouteResult{{Method: "GET", Path: "/a|<script>\x1b", Checks: []routerequirements.Finding{{Requirement: "authentication", Status: "unknown", Code: "application_auth_not_established", Reason: "Use an assertion.", NextAction: "Review the scenario."}}}}}
	for _, markdown := range []bool{false, true} {
		var out bytes.Buffer
		wrapped := routerequirements.WrapReport(*report)
		renderPreviewRequirements(&out, &wrapped, markdown)
		if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "Review the scenario.") || (markdown && strings.Contains(out.String(), "<script>")) {
			t.Fatalf("unsafe or incomplete output: %s", out.String())
		}
	}
}
