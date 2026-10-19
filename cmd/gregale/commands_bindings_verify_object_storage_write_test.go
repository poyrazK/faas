// adr: 1027 — writable canaries require explicit opt-in and matching operation evidence.
package main_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingprobe"
)

func TestCLIObjectStorageWriteCanaryJourney(t *testing.T) {
	for _, permission := range []string{"write", "read_write", "read"} {
		for _, legacy := range []bool{false, true} {
			t.Run(permission+map[bool]string{false: "/current", true: "/old-guest"}[legacy], func(t *testing.T) {
				var object []byte
				var deleted atomic.Bool
				storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.Method {
					case "PUT":
						object, _ = io.ReadAll(r.Body)
						w.Header().Set("ETag", `"probe"`)
					case "DELETE":
						deleted.Store(true)
						w.WriteHeader(204)
					case "GET":
						if deleted.Load() {
							w.WriteHeader(404)
							_, _ = io.WriteString(w, "<Error><Code>NoSuchKey</Code></Error>")
						} else {
							_, _ = w.Write(object)
						}
					default:
						t.Error("unexpected storage request")
					}
				}))
				defer storage.Close()
				created := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method == "GET" {
						_ = json.NewEncoder(w).Encode(api.AppBindingInventory{VerificationDeploymentID: "dep", VerificationScope: "production", Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeObjectStorage, Binding: "ASSETS", Scope: "production", Access: permission}}})
						return
					}
					created++
					var request api.CreateAppTaskRequest
					if json.NewDecoder(r.Body).Decode(&request) != nil || !api.IsObjectStorageWriteProbeCommand(request.Command, request.CommandShell) || request.Command[2] != permission {
						t.Fatal("wrong permission in CLI admission")
					}
					env := map[string]string{"ASSETS_ENDPOINT": storage.URL, "ASSETS_REGION": "auto", "ASSETS_BUCKET": "assets", "ASSETS_ACCESS_KEY_ID": "private-access", "ASSETS_SECRET_ACCESS_KEY": "private-secret", "ASSETS_ADDRESSING_STYLE": "path"}
					report := bindingprobe.ObjectStorageWrite(r.Context(), request.Command[1], request.Command[2], func(k string) string { return env[k] })
					if legacy {
						report = main.PassedObjectStorageCLIReport("ASSETS")
					}
					body, _ := json.Marshal(report)
					_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "task", DeploymentID: "dep", DeploymentScope: "production", Status: api.AppTaskStatusSucceeded, ExitCode: new(int), StdoutTail: string(body)})
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", "fp_live_test")
				var out, err bytes.Buffer
				oldOut, oldErr, oldJSON := (*main.OsStdout), (*main.OsStderr), (*main.JsonOutput)
				(*main.OsStdout), (*main.OsStderr), (*main.JsonOutput) = &out, &err, true
				t.Cleanup(func() { (*main.OsStdout), (*main.OsStderr), (*main.JsonOutput) = oldOut, oldErr, oldJSON })
				code := main.Run([]string{"bindings", "verify", "api", "--object-storage", "ASSETS", "--object-storage-write"})
				if (code == 0) != (permission != "read" && !legacy) {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), err.String())
				}
				if permission == "read" && created != 0 {
					t.Fatal("read-only binding submitted a mutation task")
				}
				if permission != "read" && !deleted.Load() {
					t.Fatal("CLI journey left its probe object")
				}
			})
		}
	}
}

func TestCLIObjectStorageWriteRequiresExplicitSelection(t *testing.T) {
	var out bytes.Buffer
	oldErr := (*main.OsStderr)
	(*main.OsStderr) = &out
	t.Cleanup(func() { (*main.OsStderr) = oldErr })
	for _, args := range [][]string{{"api", "--all", "--object-storage-write"}, {"api", "--object-storage-write"}, {"api", "service", "--object-storage-write"}, {"api", "--postgres", "DATABASE_URL", "--object-storage-write"}, {"api", "--outbound", "c1b76d53-ded7-4ccd-8714-bc3dcb32e9cc", "--object-storage-write"}} {
		out.Reset()
		if main.CmdBindingsVerify(args) != 1 {
			t.Fatal("write opt-in without an object-storage binding accepted")
		}
		if !strings.Contains(out.String(), "usage:") {
			t.Fatalf("invalid selection reached login or API handling: %s", out.String())
		}
	}
	status, reason := main.BindingProbeOmission(api.AppBindingInventoryItem{Type: api.BindingTypeObjectStorage, Access: "write", Scope: "default"}, "default")
	if status != "unsupported" || reason != "write_probe_requires_opt_in" {
		t.Fatal("batch probe would attempt a forbidden list or implicit write")
	}
}
