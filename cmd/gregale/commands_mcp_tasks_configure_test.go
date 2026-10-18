package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPTasksConfigurePreviewApplyAndResume(t *testing.T) {
	for _, scenario := range []string{"preview", "apply", "already configured", "partial failure"} {
		t.Run(scenario, func(t *testing.T) {
			app := api.AppResponse{Slug: "reports-worker", WorkloadClass: "worker", Manifest: api.AppManifest{Env: map[string]string{"UNRELATED": "keep"}}}
			if scenario == "already configured" {
				app.Manifest.Env["MCP_TASKS_ROLE"] = "worker"
				app.Manifest.Env["MCP_TASK_NAMESPACE"] = "reports"
				app.Manifest.Env["MCP_TASKS_SCALING_APP_SLUG"] = "reports-worker"
				app.DeploymentAvailability = api.AppDeploymentAvailabilityLive
			}
			writes := []string{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/v1/apps/reports-worker" {
					writeJSONTest(w, app)
					return
				}
				prefix := "/v1/apps/reports-worker/env/"
				if r.Method != http.MethodPut || !strings.HasPrefix(r.URL.Path, prefix) {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 500)
					return
				}
				key := strings.TrimPrefix(r.URL.Path, prefix)
				var req api.PutAppEnvRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if scenario == "partial failure" && key == "MCP_TASK_NAMESPACE" {
					http.Error(w, "fixture failure", 500)
					return
				}
				writes = append(writes, key)
				app.Manifest.Env[key] = req.Value
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "operator")
			stdout, stderr, restore := swapIO(t)
			defer restore()
			oldJSON := jsonOutput
			jsonOutput = true
			defer func() { jsonOutput = oldJSON }()
			args := []string{"tasks", "configure", "--app", "reports-worker", "--role", "worker", "--namespace", "reports", "--worker-app", "reports-worker"}
			if scenario != "preview" {
				args = append(args, "--apply")
			}
			code := cmdMCP(args)
			if scenario == "partial failure" {
				if code == 0 {
					t.Fatal("partial failure reported success")
				}
			} else if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr())
			}
			var receipt struct {
				Applied     bool     `json:"applied"`
				WrittenKeys []string `json:"written_keys"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &receipt); err != nil {
				t.Fatalf("receipt %s: %v", stdout.String(), err)
			}
			want := map[string]int{"preview": 0, "apply": 3, "already configured": 0, "partial failure": 1}[scenario]
			if len(writes) != want || len(receipt.WrittenKeys) != want || app.Manifest.Env["UNRELATED"] != "keep" {
				t.Fatalf("writes=%v receipt=%+v env=%v", writes, receipt, app.Manifest.Env)
			}
			if receipt.Applied != (scenario == "apply" || scenario == "already configured") {
				t.Fatalf("incorrect applied state: %+v", receipt)
			}
		})
	}
}

func TestMCPTasksConfigureRejectsUnsafeChangesBeforeWrites(t *testing.T) {
	for _, scenario := range []string{"secret conflict", "live reconfiguration", "wrong mode"} {
		t.Run(scenario, func(t *testing.T) {
			app := api.AppResponse{Slug: "reports-worker", WorkloadClass: "worker", Manifest: api.AppManifest{Env: map[string]string{}, EnvSecrets: map[string]string{}}}
			switch scenario {
			case "secret conflict":
				app.Manifest.EnvSecrets["MCP_TASK_NAMESPACE"] = "secret:queue"
			case "live reconfiguration":
				app.DeploymentAvailability = api.AppDeploymentAvailabilityLive
			case "wrong mode":
				app.WorkloadClass = "http"
			}
			writes := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					writeJSONTest(w, app)
					return
				}
				writes++
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "operator")
			_, _, restore := swapIO(t)
			defer restore()
			if cmdMCP([]string{"tasks", "configure", "--app", "reports-worker", "--role", "worker", "--namespace", "reports", "--worker-app", "reports-worker", "--apply"}) == 0 || writes != 0 {
				t.Fatalf("unsafe config accepted; writes=%d", writes)
			}
		})
	}
}

func TestMCPTasksConfigureRejectsInvalidSelectorsWithoutNetwork(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; http.Error(w, "unexpected", 500) }))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "operator")
	_, _, restore := swapIO(t)
	defer restore()
	for _, args := range [][]string{
		{"--app", "reports-worker", "--role", "worker", "--namespace", "reports", "--worker-app", "other-worker"},
		{"--app", "reports-observer", "--role", "observer", "--namespace", "reports", "--worker-app", "reports-observer"},
		{"--app", "reports-web", "--role", "web", "--namespace", "reports", "--worker-app", "reports-worker"},
		{"--app", "reports-worker", "--role", "worker", "--namespace", "invalid\nnamespace", "--worker-app", "reports-worker"},
	} {
		if cmdMCPTasksConfigure(args) == 0 {
			t.Fatalf("invalid selectors accepted: %v", args)
		}
	}
	if requests != 0 {
		t.Fatal("invalid flags made network requests")
	}
}
