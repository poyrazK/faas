package main

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMCPStackDoctorPreservesPartialEvidenceAndActions(t *testing.T) {
	for _, scenario := range []string{"healthy", "missing metric", "catalog drift"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newOverviewFixture(t, scenario)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			report := collectMCPStackDoctor(ctx, NewClient(fixture.server.URL, "PLATFORM_TOKEN_SENTINEL"), mcpOverviewOptions{App: "reports-web", Worker: "reports-worker", Endpoint: "/mcp", Timeout: time.Second, Roles: []mcpReleaseRole{{Name: "default", token: "CLIENT_TOKEN_SENTINEL"}}}, nil)
			if report.OK {
				t.Fatal("missing auth configuration certified as healthy")
			}
			found := false
			for _, check := range report.Checks {
				if check.Name == "oauth_configuration" && check.Status == "unknown" {
					found = true
				}
			}
			if scenario == "missing metric" && report.Overview.Tasks.State != "unknown" {
				t.Fatal("missing metrics lost")
			}
			if scenario == "catalog drift" && report.Overview.Serving.State != "degraded" {
				t.Fatal("catalog drift lost")
			}
			if !found || fixture.mutations.Load() != 0 || fixture.toolCalls.Load() != 0 {
				t.Fatalf("missing or unsafe evidence: %+v", report)
			}
			data, _ := json.Marshal(report)
			if strings.Contains(string(data), "SENTINEL") {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestMCPStackDoctorRequestedScopeAndExit(t *testing.T) {
	overview := mcpOverviewReport{Serving: mcpOverviewServing{State: "healthy"}, Tasks: mcpOverviewTasks{State: "not_requested"}, Releases: mcpOverviewReleases{State: "healthy"}}
	report := buildMCPStackDoctor(overview, []mcphosting.Check{{Name: "oauth_configuration", Status: "passed"}})
	if !report.OK {
		t.Fatal(report)
	}
	for _, check := range report.Checks {
		if check.Name == "tasks" {
			t.Fatal("unrequested task scope qualified")
		}
	}
	_, _, restore := swapIO(t)
	defer restore()
	if writeMCPStackDoctor(report) != 0 {
		t.Fatal("healthy report failed")
	}
	report.OK = false
	if writeMCPStackDoctor(report) != 1 {
		t.Fatal("unknown report passed")
	}
}

func TestMCPStackDoctorRejectsFlagsBeforeReads(t *testing.T) {
	_, _, restore := swapIO(t)
	defer restore()
	for _, args := range [][]string{{"doctor", "--stack"}, {"doctor", "--stack", "--hosting", "--app", "reports-web"}, {"doctor", "--stack", "--app", "reports-web", "--path", "/nonexistent"}, {"doctor", "--stack", "--app", "reports-web", "--worker-app", "reports-web"}} {
		if cmdMCP(args) == 0 {
			t.Fatal(args)
		}
	}
}

func TestMCPStackDoctorChecksRoleAndNamespaceBindings(t *testing.T) {
	for _, scenario := range []string{"healthy", "wrong role", "namespace mismatch", "missing namespace"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newOverviewFixture(t, "healthy")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/reports-web" {
					env := map[string]string{"MCP_TASKS_ROLE": "web", "MCP_TASK_NAMESPACE": "reports"}
					if scenario == "wrong role" {
						env["MCP_TASKS_ROLE"] = "worker"
					}
					if scenario == "namespace mismatch" {
						env["MCP_TASK_NAMESPACE"] = "other"
					}
					if scenario == "missing namespace" {
						delete(env, "MCP_TASK_NAMESPACE")
					}
					writeJSONTest(w, api.AppResponse{Slug: "reports-web", Manifest: api.AppManifest{Env: env}})
					return
				}
				if r.URL.Path == "/v1/apps/reports-worker" {
					worker := overviewWorker()
					worker.Manifest.Env["MCP_TASKS_ROLE"] = "worker"
					worker.Manifest.Env["MCP_TASKS_SCALING_APP_SLUG"] = "reports-worker"
					writeJSONTest(w, worker)
					return
				}
				fixture.serve(t, w, r)
			}))
			defer server.Close()
			checks := collectMCPDoctorBindings(context.Background(), NewClient(server.URL, "PLATFORM_TOKEN_SENTINEL"), mcpOverviewOptions{App: "reports-web", Worker: "reports-worker"})
			passed := true
			for _, check := range checks {
				passed = passed && check.Status == "passed"
			}
			if passed != (scenario == "healthy") {
				t.Fatalf("unexpected bindings: %+v", checks)
			}
		})
	}
}

func TestMCPStackDoctorOpenServingScopePasses(t *testing.T) {
	fixture := newOverviewFixture(t, "healthy")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	report := collectMCPStackDoctor(ctx, NewClient(fixture.server.URL, "PLATFORM_TOKEN_SENTINEL"), mcpOverviewOptions{App: "reports-web", Endpoint: "/mcp", Timeout: time.Second, Roles: []mcpReleaseRole{{Name: "default", token: "CLIENT_TOKEN_SENTINEL"}}}, &mcphosting.Config{Auth: mcphosting.AuthConfig{Mode: "open"}})
	if !report.OK || report.Overview.Tasks.State != "not_requested" {
		t.Fatalf("valid serving scope failed: %+v", report)
	}
}
