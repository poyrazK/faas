// adr: 426 — object-storage canaries report bounded read-access evidence.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func passedObjectStorageCLIReport(prefix string) api.ObjectStorageBindingProbeReport {
	check := api.ObjectStorageBindingProbeCheck{Status: "passed"}
	return api.ObjectStorageBindingProbeReport{Prefix: prefix, Environment: check, Configuration: check, Connection: check, Authorization: check, BucketAccess: check}
}

func TestCmdBindingsVerifyObjectStorageRunsSelectedScopedGuest(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[asJSON], func(t *testing.T) {
			inventoryCalls, created, polled := 0, 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/bindings":
					inventoryCalls++
					_ = json.NewEncoder(w).Encode(api.AppBindingInventory{VerificationDeploymentID: "deployment-1", VerificationScope: "production", Bindings: []api.AppBindingInventoryItem{
						{Type: api.BindingTypeObjectStorage, Binding: "ASSETS", Scope: "staging"}, {Type: api.BindingTypeObjectStorage, Binding: "ASSETS", Scope: "production"}}})
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks":
					created++
					var request api.CreateAppTaskRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Command) != 2 || request.Command[0] != api.AppTaskObjectStorageBindingProbeCommand || request.Command[1] != "ASSETS" || request.CommandShell || request.MaxOutputBytes != 4096 || request.TimeoutSeconds != 15 {
						t.Errorf("probe admission=%+v err=%v", request, err)
					}
					_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "task-1", Status: api.AppTaskStatusQueued, DeploymentID: "deployment-1", DeploymentScope: "production"})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/tasks/task-1":
					polled++
					body, _ := json.Marshal(passedObjectStorageCLIReport("ASSETS"))
					_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "task-1", Status: api.AppTaskStatusSucceeded, DeploymentID: "deployment-1", DeploymentScope: "production", StdoutTail: string(body), ExitCode: new(int)})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_test")
			var stdout, stderr bytes.Buffer
			oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
			osStdout, osStderr, jsonOutput = &stdout, &stderr, asJSON
			t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
			if code := run([]string{"bindings", "verify", "api", "--object-storage", "ASSETS", "--poll-interval", "1ms", "--wait-timeout", "1s"}); code != 0 || inventoryCalls != 1 || created != 1 || polled != 1 {
				t.Fatalf("exit=%d calls=%d/%d/%d output=%s %s", code, inventoryCalls, created, polled, stdout.String(), stderr.String())
			}
			if asJSON {
				var report api.ObjectStorageBindingProbeReport
				if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || !report.Passed() || report.Prefix != "ASSETS" || report.DeploymentID != "deployment-1" {
					t.Fatalf("report=%+v err=%v", report, err)
				}
			} else if !strings.Contains(stdout.String(), "read access only") || !strings.Contains(stdout.String(), "Bucket access") {
				t.Fatalf("missing human diagnostics: %s", stdout.String())
			}
		})
	}
}

func TestCmdBindingsVerifyObjectStorageRejectsUnsafeResults(t *testing.T) {
	for _, all := range []bool{false, true} {
		for _, failure := range []string{"wrong prefix", "missing stage", "truncated", "missing exit", "nonzero exit", "contradictory error", "deployment changed", "scope changed"} {
			t.Run(map[bool]string{false: "single", true: "all"}[all]+" "+failure, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method == http.MethodGet {
						_ = json.NewEncoder(w).Encode(api.AppBindingInventory{VerificationDeploymentID: "deployment-1", VerificationScope: "production", Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeObjectStorage, Binding: "ASSETS", Scope: "production"}}})
						return
					}
					report := passedObjectStorageCLIReport("ASSETS")
					task := api.AppTaskResponse{ID: "task-1", Status: api.AppTaskStatusSucceeded, DeploymentID: "deployment-1", DeploymentScope: "production", ExitCode: new(int)}
					switch failure {
					case "wrong prefix":
						report.Prefix = "OTHER"
					case "missing stage":
						report.BucketAccess.Status = ""
					case "truncated":
						task.OutputTruncated = true
					case "missing exit":
						task.ExitCode = nil
					case "nonzero exit":
						*task.ExitCode = 1
					case "contradictory error":
						report.Error = "bucket listing failed"
					case "deployment changed":
						task.DeploymentID = "deployment-2"
					case "scope changed":
						task.DeploymentScope = "staging"
					}
					body, _ := json.Marshal(report)
					task.StdoutTail = string(body)
					_ = json.NewEncoder(w).Encode(task)
				}))
				defer srv.Close()
				t.Setenv("FAAS_API", srv.URL)
				t.Setenv("FAAS_TOKEN", "fp_live_test")
				args := []string{"bindings", "verify", "api", "--object-storage", "ASSETS"}
				if all {
					args = []string{"bindings", "verify", "api", "--all"}
				}
				if code := run(args); code != 1 {
					t.Fatalf("unsafe result accepted: exit=%d", code)
				}
			})
		}
	}
}

func TestCmdBindingsVerifyObjectStorageRejectsWrongScopeAndConflictingSelectors(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/api/bindings" {
			t.Errorf("unbound canary admitted: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.AppBindingInventory{VerificationScope: "production", Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeObjectStorage, Binding: "ASSETS", Scope: "staging"}}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	if code := run([]string{"bindings", "verify", "api", "--object-storage", "ASSETS"}); code != 1 || requests != 1 {
		t.Fatalf("wrong scope accepted: exit=%d requests=%d", code, requests)
	}
	for _, args := range [][]string{
		{"--object-storage", "lowercase"}, {"--object-storage", "ASSETS-INVALID"}, {"--object-storage", "ASSETS", "--all"},
		{"--object-storage", "ASSETS", "--postgres", "DATABASE_URL"}, {"billing", "--object-storage", "ASSETS"},
	} {
		if code := run(append([]string{"bindings", "verify", "api"}, args...)); code != 1 || requests != 1 {
			t.Fatalf("invalid selection reached API: %v exit=%d requests=%d", args, code, requests)
		}
	}
}
