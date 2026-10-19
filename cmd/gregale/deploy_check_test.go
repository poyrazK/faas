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
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261019120041
func TestDeployCheckReportsCIOutcomeWithoutSubmission(t *testing.T) {
	for _, status := range []string{"passed", "blocked", "unknown", "inconsistent"} {
		t.Run(status, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services:\n  api:\n    build: .\n    ports: ['3000:3000']\n    environment:\n      - TOKEN\n"), 0600); err != nil {
				t.Fatal(err)
			}
			secretFile := filepath.Join(t.TempDir(), "secrets.env")
			if err := os.WriteFile(secretFile, []byte("TOKEN=must-stay-on-this-machine\n"), 0600); err != nil {
				t.Fatal(err)
			}
			resetJSONOut(t)
			oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
			var out, errOut bytes.Buffer
			osStdout, osStderr, jsonOutput = &out, &errOut, true
			t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodPost || r.URL.Path != "/v1/projects/shop/deployment-readiness" {
					t.Errorf("unexpected mutation or source request: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", 500)
					return
				}
				var request api.ProjectDeploymentCheckRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				raw, _ := json.Marshal(request)
				if bytes.Contains(raw, []byte("must-stay-on-this-machine")) {
					t.Error("secret value left this machine")
				}
				if len(request.Workloads) != 1 || request.Workloads[0].Workload.Name != "api" || len(request.Workloads[0].ProvidedKeys) != 1 || request.Workloads[0].ProvidedKeys[0] != "TOKEN" {
					t.Errorf("intent lost: %+v", request)
				}
				checkStatus := status
				if status == "inconsistent" {
					checkStatus = "blocked"
				}
				checks := []api.ProjectDeploymentCheck{{Code: "fixture", Status: checkStatus, Message: "fixture result"}}
				result := api.ProjectDeploymentReadiness{SchemaVersion: 1, ProjectSlug: "shop", Environment: "production", Status: api.DeploymentReadinessStatus(checks), CheckedAt: time.Now().UTC(), Checks: checks}
				if status == "inconsistent" {
					result.Status = "ready"
				}
				writeJSONTest(w, result)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fixture-token")
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			code := run([]string{"deploy", "check", "--project", "--project-slug", "shop", "--path", root, "--source", "worktree", "--secrets-file", secretFile, "--json"})
			want := map[string]int{"passed": 0, "blocked": 1, "unknown": 2, "inconsistent": 2}[status]
			if code != want || requests != 1 {
				t.Fatalf("code=%d want=%d calls=%d out=%s errors=%s", code, want, requests, &out, &errOut)
			}
			if bytes.Contains(out.Bytes(), []byte("must-stay-on-this-machine")) {
				t.Fatal("secret value entered report")
			}
		})
	}
}

// adr: 20261019120041
func TestDeployCheckPromotionsSkipLocalSourceAndFailClosedOnOlderServer(t *testing.T) {
	resetJSONOut(t)
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	var out, errOut bytes.Buffer
	osStdout, osStderr, jsonOutput = &out, &errOut, true
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request api.ProjectDeploymentCheckRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.FromEnvironment != "staging" || !request.SyncConfig || !request.Verify || len(request.Workloads) != 0 {
			t.Error("promotion intent changed", request)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fixture-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if code := cmdDeployCheck([]string{"--project", "--project-slug", "shop", "--from", "staging", "--verify", "--sync-config"}); code != 2 || requests != 1 {
		t.Fatalf("older server readiness did not fail closed: code=%d calls=%d", code, requests)
	}
	if strings.Contains(out.String(), "\"status\":\"ready\"") {
		t.Fatal("older server was reported ready")
	}
}
