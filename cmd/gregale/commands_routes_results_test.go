package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func automaticResultFixture() api.AutomaticRouteCheck {
	deployment := "00000000-0000-4000-8000-000000000001"
	now := time.Now().UTC()
	check := api.RouteRequirementsCheck{Version: 1, App: "my-api", AppID: "app-id", DeploymentID: deployment, RequirementsRevision: 1, RequirementsSHA256: strings.Repeat("a", 64), ConfigurationSHA256: strings.Repeat("b", 64), Report: api.RouteRequirementsReport{Version: 2, SHA256: strings.Repeat("a", 64), Status: "satisfied", Coverage: &api.RouteCoverageInventory{Status: "available", Deployment: deployment, SHA256: strings.Repeat("c", 64), RouteCount: 1}}}
	return api.AutomaticRouteCheck{Version: 1, App: "my-api", AppID: "app-id", DeploymentID: deployment, State: "complete", Freshness: "current", StaleReasons: []string{}, CurrentRequirementsRevision: 1, CurrentRequirementsSHA256: check.RequirementsSHA256, QueuedAt: now, CheckedAt: &now, Check: &check}
}

func TestAutomaticRouteResultCLIExportGateAndBinding(t *testing.T) {
	for _, scenario := range []string{"satisfied", "violated", "unknown", "stale", "pending", "running", "retrying", "foreign deployment", "foreign app", "stale pin", "false freshness", "missing check", "unrecognized state", "existing output", "symlink output"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			result := automaticResultFixture()
			deployment := result.DeploymentID
			want := 1
			exported := true
			switch scenario {
			case "satisfied":
				want = 0
			case "violated":
				result.Check.Report.Status = "violated"
			case "unknown":
				result.Check.Report.Status = "unknown"
				result.Check.Report.Coverage.Status = "unavailable"
			case "stale":
				result.Freshness = "stale"
				result.StaleReasons = []string{"configuration_changed"}
			case "pending", "running", "retrying":
				result.State = scenario
				result.Freshness = "unavailable"
				result.Check = nil
				result.CheckedAt = nil
			case "foreign deployment":
				result.DeploymentID = "another-deployment"
				exported = false
			case "foreign app":
				result.App = "another-app"
				exported = false
			case "stale pin":
				result.CurrentRequirementsRevision = 2
				exported = false
			case "false freshness":
				result.Check.RequirementsRevision = 2
				exported = false
			case "missing check":
				result.Check = nil
				result.CheckedAt = nil
				exported = false
			case "unrecognized state":
				result.State = "success"
				exported = false
			case "existing output", "symlink output":
				exported = false
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSONTest(w, result) }))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			oldOut := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = oldOut })
			path := filepath.Join(t.TempDir(), "result.json")
			if scenario == "existing output" || scenario == "symlink output" {
				target := path
				if scenario == "symlink output" {
					target += ".target"
				}
				if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				if target != path {
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				}
			}
			code := run([]string{"routes", "results", "my-api", "--deployment", deployment, "--expected-revision", "1", "--out", path, "--fail-on-requirements", "--json"})
			if code != want {
				t.Fatalf("exit %d want %d; %s", code, want, out.String())
			}
			body, err := os.ReadFile(path)
			if exported {
				info, statErr := os.Stat(path)
				if err != nil || statErr != nil || info.Mode().Perm() != 0o600 || !strings.Contains(string(body), `"current_requirements_revision": 1`) || !strings.Contains(out.String(), `"freshness"`) {
					t.Fatalf("export missing before CI gate: %s %v", body, err)
				}
			} else if scenario == "existing output" || scenario == "symlink output" {
				if err != nil || string(body) != "keep" {
					t.Fatal("existing result target overwritten")
				}
			} else if !os.IsNotExist(err) {
				t.Fatal("unbound result exported")
			}
		})
	}
}

func TestAutomaticRouteResultCLIRefreshWaitAndTimeout(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "completes", true: "timeout exports pending"}[timeout], func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			fixture := automaticResultFixture()
			refreshed, reads := false, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case "POST":
					if r.URL.Path != "/v1/apps/my-api/route-requirements/checks/"+fixture.DeploymentID+"/refresh" || r.ContentLength != 0 {
						t.Error("refresh request changed")
					}
					refreshed = true
					w.WriteHeader(http.StatusAccepted)
				case "GET":
					if !refreshed {
						t.Error("lookup preceded refresh")
					}
					reads++
					result := fixture
					if timeout || reads == 1 {
						result.State = "pending"
						result.Freshness = "unavailable"
						result.Check = nil
						result.CheckedAt = nil
					}
					writeJSONTest(w, result)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			oldOut := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = oldOut })
			duration := "10s"
			want := 0
			if timeout {
				duration = "100ms"
				want = 1
			}
			path := filepath.Join(t.TempDir(), "latest.json")
			code := run([]string{"routes", "results", "my-api", "--deployment", fixture.DeploymentID, "--refresh", "--wait", "--timeout", duration, "--out", path, "--fail-on-requirements", "--json"})
			if code != want || !refreshed || reads < 1 || !timeout && reads < 2 {
				t.Fatalf("wait: %d refresh=%v reads=%d; %s", code, refreshed, reads, out.String())
			}
			body, err := os.ReadFile(path)
			if err != nil || timeout && !strings.Contains(string(body), `"state": "pending"`) {
				t.Fatal("timeout did not preserve latest pending evidence")
			}
		})
	}
}
