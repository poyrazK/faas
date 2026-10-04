package main

// ADR-448: normalized save/export and deployment-bound CI checks.

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

func TestSavedRouteRequirementsCLIWorkflow(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	dir := t.TempDir()
	input := filepath.Join(dir, "requirements.yaml")
	if err := os.WriteFile(input, []byte("version: 2\npublic:\n  - method: GET\n    path: /health\n    reason: private-local-rationale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deployment := "00000000-0000-4000-8000-000000000001"
	var saved api.SavedRouteRequirements
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case r.Method == "PUT" && r.URL.Path == "/v1/apps/my-api/route-requirements":
			var request api.SaveRouteRequirementsRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			if request.ExpectedRevision == nil || *request.ExpectedRevision != 0 || request.Requirements.Public[0].Reason != api.RoutePublicExceptionMarker {
				t.Errorf("revision/privacy: %+v", request)
			}
			config, digest, err := routerequirements.NormalizeCoverage(request.Requirements)
			if err != nil {
				t.Error(err)
				return
			}
			saved = api.SavedRouteRequirements{AppID: "app-id", Revision: 1, SHA256: digest, Requirements: config}
			writeJSONTest(w, saved)
		case r.Method == "GET" && r.URL.Path == "/v1/apps/my-api/route-requirements":
			writeJSONTest(w, saved)
		case r.Method == "POST" && r.URL.Path == "/v1/apps/my-api/route-requirements/check":
			var request api.CheckRouteRequirementsRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			if request.DeploymentID != deployment || request.ExpectedRevision == nil || *request.ExpectedRevision != 1 {
				t.Errorf("check binding: %+v", request)
			}
			inventory := routerequirements.CoverageInventory{Status: "available", Source: "captured_candidate_contract", Deployment: deployment, SHA256: strings.Repeat("a", 64), Routes: []routerequirements.CapturedRoute{{Method: "GET", Path: "/health"}, {Method: "POST", Path: "/new"}}, RouteCount: 2}
			result, err := routerequirements.BuildSavedCheck(saved, routerequirements.Context{App: api.AppResponse{ID: "app-id", Slug: "my-api"}}, "pro", deployment, inventory)
			if err != nil {
				t.Error(err)
				return
			}
			writeJSONTest(w, result)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	if code := run([]string{"routes", "requirements", "set", "my-api", "--requirements", input}); code != 1 || calls != 0 {
		t.Fatal("missing expected revision reached API")
	}
	if code := run([]string{"routes", "requirements", "set", "my-api", "--requirements", input, "--expected-revision", "0", "--json"}); code != 0 || calls != 1 || strings.Contains(out.String(), "private-local-rationale") {
		t.Fatalf("save: %d %s", code, out.String())
	}
	export := filepath.Join(dir, "saved.json")
	if code := run([]string{"routes", "requirements", "get", "my-api", "--out", export}); code != 0 {
		t.Fatalf("export: %d", code)
	}
	config, _, err := readPreviewCoverageRequirements(export)
	var digest string
	if err == nil {
		config, digest, err = routerequirements.NormalizeCoverage(config)
	}
	if err != nil || digest != saved.SHA256 || config.Version != 2 {
		t.Fatalf("export cannot feed planner: %+v %s %v", config, digest, err)
	}
	info, err := os.Stat(export)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("export permissions")
	}
	artifact := filepath.Join(dir, "check.json")
	out.Reset()
	if code := run([]string{"routes", "check", "my-api", "--deployment", deployment, "--expected-revision", "1", "--out", artifact, "--fail-on-requirements", "--json"}); code != 1 {
		t.Fatalf("uncovered operation passed CI: %d", code)
	}
	body, err := os.ReadFile(artifact)
	var result api.RouteRequirementsCheck
	if err != nil || json.Unmarshal(body, &result) != nil || result.Report.Status != "violated" || !strings.Contains(out.String(), `"requirements_revision": 1`) || strings.Contains(string(body), "private-local-rationale") {
		t.Fatalf("report not exported before gate: %s %v; stdout %s", body, err, out.String())
	}
	if code := run([]string{"routes", "requirements", "get", "my-api", "--out", export}); code != 1 {
		t.Fatal("existing export overwritten")
	}
}

func TestSavedRouteCheckCLIGatesAndResponseBinding(t *testing.T) {
	for _, scenario := range []string{"satisfied", "unknown", "foreign deployment", "stale revision", "foreign app", "missing fingerprint", "existing output", "symlink output"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			deployment := "00000000-0000-4000-8000-000000000001"
			result := api.RouteRequirementsCheck{Version: 1, App: "my-api", AppID: "app-id", DeploymentID: deployment, RequirementsRevision: 1, RequirementsSHA256: strings.Repeat("a", 64), ConfigurationSHA256: strings.Repeat("b", 64), Report: api.RouteRequirementsReport{Version: 2, Status: "satisfied", SHA256: strings.Repeat("a", 64), Coverage: &api.RouteCoverageInventory{Status: "available", Deployment: deployment, SHA256: strings.Repeat("c", 64), RouteCount: 1}}}
			want := 1
			switch scenario {
			case "satisfied":
				want = 0
			case "unknown":
				result.Report.Status = "unknown"
				result.Report.Coverage.Status = "unavailable"
			case "foreign deployment":
				result.DeploymentID = "another-deployment"
			case "stale revision":
				result.RequirementsRevision = 2
			case "foreign app":
				result.App = "another-app"
			case "missing fingerprint":
				result.ConfigurationSHA256 = ""
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSONTest(w, result) }))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			oldOut := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = oldOut })
			artifact := filepath.Join(t.TempDir(), "check.json")
			if scenario == "existing output" || scenario == "symlink output" {
				target := artifact
				if scenario == "symlink output" {
					target += ".target"
				}
				if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				if target != artifact {
					if err := os.Symlink(target, artifact); err != nil {
						t.Fatal(err)
					}
				}
			}
			if code := run([]string{"routes", "check", "my-api", "--deployment", deployment, "--expected-revision", "1", "--out", artifact, "--fail-on-requirements"}); code != want {
				t.Fatalf("exit %d want %d; %s", code, want, out.String())
			}
			body, err := os.ReadFile(artifact)
			switch scenario {
			case "satisfied", "unknown":
				if err != nil || !strings.Contains(string(body), `"requirements_revision": 1`) {
					t.Fatal("valid check not exported")
				}
			case "existing output", "symlink output":
				if err != nil || string(body) != "keep" {
					t.Fatal("output target overwritten")
				}
			default:
				if !os.IsNotExist(err) {
					t.Fatal("unbound check exported")
				}
			}
		})
	}
}
