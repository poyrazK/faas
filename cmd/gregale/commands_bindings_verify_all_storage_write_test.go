package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingprobe"
)

func TestAllObjectStorageWriteJourney(t *testing.T) {
	for _, tc := range []struct{ access, failure string }{
		{"read", ""}, {"write", ""}, {"read_write", ""},
		{"write", "cleanup"}, {"read_write", "cleanup"}, {"read_write", "download"}, {"write", "upload"},
		{"write", "legacy report"}, {"read_write", "wrong mode"}, {"write", "wrong prefix"},
		{"write", "wrong deployment"}, {"read_write", "wrong scope"},
	} {
		t.Run(tc.access+"/"+tc.failure, func(t *testing.T) {
			var providerMu sync.Mutex
			var object []byte
			exists := false
			puts, gets, deletes := 0, 0, 0
			storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				providerMu.Lock()
				defer providerMu.Unlock()
				if !strings.HasPrefix(r.URL.Path, "/assets/.gregale/binding-probes/") {
					t.Errorf("unexpected object path %s", r.URL.Path)
				}
				switch r.Method {
				case http.MethodPut:
					puts++
					if tc.failure == "upload" {
						w.WriteHeader(403)
						_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>PRIVATE_PROVIDER_DETAIL</Message></Error>`)
						return
					}
					if r.Header.Get("If-None-Match") != "*" {
						t.Error("missing create-only upload condition")
					}
					object, _ = io.ReadAll(r.Body)
					exists = true
					w.Header().Set("ETag", `"fixture"`)
				case http.MethodGet:
					gets++
					if !exists {
						w.WriteHeader(404)
						_, _ = io.WriteString(w, `<Error><Code>NoSuchKey</Code></Error>`)
						return
					}
					if tc.failure == "download" {
						_, _ = io.WriteString(w, "CORRUPT_PRIVATE_BYTES")
						return
					}
					_, _ = w.Write(object)
				case http.MethodDelete:
					deletes++
					if tc.failure == "cleanup" {
						w.WriteHeader(403)
						_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code><Message>PRIVATE_PROVIDER_DETAIL</Message></Error>`)
						return
					}
					exists = false
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected provider method %s", r.Method)
					http.Error(w, "unexpected", 400)
				}
			}))
			defer storage.Close()
			inventory := api.AppBindingInventory{App: "api", Complete: true, RequestedDeploymentID: pinnedBindingDeployment, VerificationDeploymentID: pinnedBindingDeployment, VerificationScope: "production", Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeObjectStorage, Name: "assets", Binding: "ASSETS", Scope: "production", Access: tc.access, State: "enabled"}}}
			var tasks, cancels atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/bindings":
					if r.URL.Query().Get("deployment_id") != pinnedBindingDeployment {
						t.Errorf("inventory not pinned: %s", r.URL)
					}
					writeJSONTest(w, inventory)
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks":
					tasks.Add(1)
					var request api.CreateAppTaskRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					expected := []string{api.AppTaskObjectStorageBindingProbeCommand, "ASSETS"}
					if tc.access != "read" {
						expected = []string{api.AppTaskObjectStorageWriteProbeCommand, "ASSETS", tc.access}
					}
					if !reflect.DeepEqual(request.Command, expected) || request.VerificationDeploymentID != pinnedBindingDeployment {
						t.Errorf("wrong task request: %+v", request)
					}
					report := passedObjectStorageCLIReport("ASSETS")
					if tc.access != "read" {
						env := map[string]string{"ASSETS_ENDPOINT": storage.URL, "ASSETS_REGION": "auto", "ASSETS_BUCKET": "assets", "ASSETS_ACCESS_KEY_ID": "private-access", "ASSETS_SECRET_ACCESS_KEY": "private-secret", "ASSETS_ADDRESSING_STYLE": "path"}
						report = bindingprobe.ObjectStorageWrite(r.Context(), "ASSETS", tc.access, func(k string) string { return env[k] })
					}
					switch tc.failure {
					case "legacy report":
						report = passedObjectStorageCLIReport("ASSETS")
					case "wrong mode":
						report.Mode = "write"
					case "wrong prefix":
						report.Prefix = "OTHER"
					}
					raw, _ := json.Marshal(report)
					task := api.AppTaskResponse{ID: "storage-task", DeploymentID: pinnedBindingDeployment, DeploymentScope: "production", Status: api.AppTaskStatusSucceeded, ExitCode: new(int), StdoutTail: string(raw)}
					if tc.failure == "wrong deployment" {
						task.DeploymentID = "bb1d2efe-3e03-452d-8ebc-a28e1328c3c6"
					}
					if tc.failure == "wrong scope" {
						task.DeploymentScope = "staging"
					}
					writeJSONTest(w, task)
				case r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/api/tasks/storage-task":
					cancels.Add(1)
					writeJSONTest(w, api.AppTaskResponse{ID: "storage-task", Status: api.AppTaskStatusCancelled})
				default:
					t.Errorf("unexpected API request %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			code, out, errs := captureBindingCLI(t, server.URL, "bindings", "verify", "api", "--all", "--deployment", pinnedBindingDeployment, "--object-storage-write", "--require-complete", "--json")
			var batch serviceBindingProbeBatchReport
			if err := json.Unmarshal([]byte(out), &batch); err != nil {
				t.Fatalf("%v output=%s errors=%s", err, out, errs)
			}
			pass := tc.failure == ""
			if (code == 0) != pass || tasks.Load() != 1 || batch.Gate == nil || batch.Gate.Passed != pass {
				t.Fatalf("code=%d tasks=%d report=%s errors=%s", code, tasks.Load(), out, errs)
			}
			if len(batch.Bindings) != 1 || batch.Bindings[0].ObjectStorageReport == nil {
				t.Fatalf("missing typed storage report: %s", out)
			}
			if !pass && (batch.Failed != 1 || batch.Gate.Blockers[0].Code != "probe_failed") {
				t.Fatalf("missing failed gate: %s", out)
			}
			for _, private := range []string{storage.URL, "private-access", "private-secret", "PRIVATE_PROVIDER_DETAIL", "CORRUPT_PRIVATE_BYTES"} {
				if strings.Contains(out, private) {
					t.Fatalf("private provider data in report: %s", out)
				}
			}
			providerMu.Lock()
			defer providerMu.Unlock()
			if tc.access == "read" && puts+gets+deletes != 0 {
				t.Fatal("read-only binding ran write probe")
			}
			if tc.access == "write" && gets != 0 {
				t.Fatal("write-only binding attempted a read")
			}
			if tc.access != "read" && tc.failure != "upload" && deletes != 1 {
				t.Fatalf("cleanup attempts=%d", deletes)
			}
			if tc.failure != "cleanup" && exists {
				t.Fatal("fixture object left after canary")
			}
			if (tc.failure == "wrong deployment" || tc.failure == "wrong scope") && cancels.Load() != 1 {
				t.Fatal("mismatched task not cancelled")
			}
			if tc.failure == "cleanup" && batch.Bindings[0].ObjectStorageReport.Cleanup.Status != "failed" {
				t.Fatal("cleanup failure not reported")
			}
		})
	}
}

func TestAllObjectStorageWriteSelectionAndConsent(t *testing.T) {
	for _, scenario := range []string{"consent", "no consent", "false consent", "unknown access", "invalid prefix", "ambiguous"} {
		t.Run(scenario, func(t *testing.T) {
			bindings := []api.AppBindingInventoryItem{
				{Type: api.BindingTypeObjectStorage, Name: "read", Binding: "READ", Scope: "production", Access: "read"},
				{Type: api.BindingTypeObjectStorage, Name: "write", Binding: "WRITE", Scope: "production", Access: "write"},
				{Type: api.BindingTypeObjectStorage, Name: "both", Binding: "BOTH", Scope: "production", Access: "read_write"},
				{Type: api.BindingTypeObjectStorage, Name: "disabled", Binding: "DISABLED", Scope: "production", Access: "write", State: "disabled"},
				{Type: api.BindingTypeObjectStorage, Name: "staging", Binding: "STAGING", Scope: "staging", Access: "write"},
				{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: "app", Access: "push", ConsumerState: "active", QueueCanarySupported: true},
			}
			switch scenario {
			case "unknown access":
				bindings[2].Access = "future"
			case "invalid prefix":
				bindings[2].Binding = "bad-prefix"
			case "ambiguous":
				bindings = append(bindings, bindings[2])
			}
			inventory := api.AppBindingInventory{App: "api", Complete: true, RequestedDeploymentID: pinnedBindingDeployment, VerificationDeploymentID: pinnedBindingDeployment, VerificationScope: "production", Bindings: bindings}
			commands := map[string][]string{}
			var commandMu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/bindings":
					writeJSONTest(w, inventory)
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks":
					var request api.CreateAppTaskRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if len(request.Command) < 2 {
						t.Error("missing command")
						http.Error(w, "bad task", 400)
						return
					}
					prefix := request.Command[1]
					commandMu.Lock()
					if _, duplicate := commands[prefix]; duplicate {
						t.Errorf("duplicate task %s", prefix)
					}
					commands[prefix] = request.Command
					commandMu.Unlock()
					report := passedObjectStorageCLIReport(prefix)
					if len(request.Command) == 3 {
						check := api.ObjectStorageBindingProbeCheck{Status: "passed"}
						report.Mode = request.Command[2]
						report.Upload = &check
						report.Cleanup = &check
						download := check
						if report.Mode == "write" {
							download.Status = "not_checked"
						}
						report.Download = &download
					}
					raw, _ := json.Marshal(report)
					writeJSONTest(w, api.AppTaskResponse{ID: "task-" + prefix, DeploymentID: pinnedBindingDeployment, DeploymentScope: "production", Status: api.AppTaskStatusSucceeded, ExitCode: new(int), StdoutTail: string(raw)})
				default:
					t.Errorf("unexpected request (including unconsented queue publication) %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			args := []string{"bindings", "verify", "api", "--all", "--deployment", pinnedBindingDeployment, "--require-complete", "--json"}
			if scenario == "false consent" {
				args = append(args, "--object-storage-write=false")
			} else if scenario != "no consent" {
				args = append(args, "--object-storage-write")
			}
			code, out, errs := captureBindingCLI(t, server.URL, args...)
			var report serviceBindingProbeBatchReport
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatalf("%v output=%s errors=%s", err, out, errs)
			}
			if code != 1 || report.Gate == nil || report.Gate.Passed || report.Skipped != 2 {
				t.Fatalf("code=%d report=%s errors=%s", code, out, errs)
			}
			commandMu.Lock()
			defer commandMu.Unlock()
			if !reflect.DeepEqual(commands["READ"], []string{api.AppTaskObjectStorageBindingProbeCommand, "READ"}) {
				t.Fatalf("read-only command=%v", commands["READ"])
			}
			if scenario == "no consent" || scenario == "false consent" {
				if commands["WRITE"] != nil || !reflect.DeepEqual(commands["BOTH"], []string{api.AppTaskObjectStorageBindingProbeCommand, "BOTH"}) || !strings.Contains(out, "write_probe_requires_opt_in") {
					t.Fatalf("implicit write: commands=%v report=%s", commands, out)
				}
			} else {
				if !reflect.DeepEqual(commands["WRITE"], []string{api.AppTaskObjectStorageWriteProbeCommand, "WRITE", "write"}) {
					t.Fatalf("write command=%v", commands["WRITE"])
				}
				if scenario == "consent" {
					if !reflect.DeepEqual(commands["BOTH"], []string{api.AppTaskObjectStorageWriteProbeCommand, "BOTH", "read_write"}) {
						t.Fatalf("read/write command=%v", commands["BOTH"])
					}
				} else {
					reason := map[string]string{"unknown access": "object_storage_access_unsupported", "invalid prefix": "object_storage_binding_invalid", "ambiguous": "object_storage_binding_ambiguous"}[scenario]
					if commands["BOTH"] != nil || commands["bad-prefix"] != nil || !strings.Contains(out, reason) {
						t.Fatalf("unsupported metadata submitted: commands=%v report=%s", commands, out)
					}
				}
			}
			if !strings.Contains(out, "queue_publish_consent_required") {
				t.Fatal("storage opt-in granted queue publication")
			}
		})
	}
}
