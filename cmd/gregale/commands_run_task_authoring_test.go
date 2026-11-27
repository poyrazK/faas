package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const authoringTask = `{
	"name":"report", "version":2,
	"definition":{
		"runtime":"python313", "source":"def main(value, context): return value",
		"environment_id":"11111111-1111-4111-8111-111111111111",
		"limits":{"execution_class":"extended","timeout_ms":120000,"memory_mb":512,
			"ephemeral_disk_mb":128,"max_artifact_bytes":262144},
		"artifact_storage":"stored", "output_files":["report.html"],
		"input_schema":{"type":"object","required":["title"],"properties":{"title":{"type":"string"}}},
		"result_schema":{"type":"object","required":["pages"],"properties":{"pages":{"type":"integer"}}}
	}
}`

func writeRunTaskAuthoringFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCmdRunTaskAuthoringOffline(t *testing.T) {
	account := &capabilitiesAPI{}
	account.setHidden("disposable-runs")
	server := httptest.NewServer(account)
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fixture-token")
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))
	clearCapabilityReleaseAll(t)
	_, _, _ = runSourceRunCLI(t, "help", "--all")
	baseline := account.reads.Load()
	if baseline != 1 {
		t.Fatalf("expected the cached account view to be initialized, got %d reads", baseline)
	}
	manifest := writeRunTaskAuthoringFixture(t, "task.json", authoringTask)
	fixture := writeRunTaskAuthoringFixture(t, "input.json", `{"title":"Quarterly report"}`)
	code, stdout, stderr := runSourceRunCLI(t, "run", "tasks", "validate", "--manifest", manifest, "--input", "@"+fixture, "--json")
	var valid struct {
		DefinitionDigest string `json:"definition_digest"`
		FixtureValidated bool   `json:"fixture_validated"`
	}
	if code != 0 || json.Unmarshal([]byte(stdout), &valid) != nil || len(valid.DefinitionDigest) != 64 || !valid.FixtureValidated {
		t.Fatalf("valid fixture failed: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, _, stderr = runSourceRunCLI(t, "run", "tasks", "validate", "--manifest", manifest, "--input", `{"wrong":"private-value"}`)
	if code == 0 || !strings.Contains(stderr, "input does not match input_schema") || strings.Contains(stderr, "private-value") {
		t.Fatalf("invalid fixture diagnostics: code=%d stderr=%s", code, stderr)
	}
	code, _, stderr = runSourceRunCLI(t, "run", "tasks", "validate", "--manifest", manifest, "--input=")
	if code == 0 || !strings.Contains(stderr, "one complete JSON value") {
		t.Fatalf("empty fixture diagnostics: code=%d stderr=%s", code, stderr)
	}
	args := []string{"run", "tasks", "generate", "--manifest", manifest, "--language", "typescript"}
	code, generated, stderr := runSourceRunCLI(t, args...)
	if code != 0 || !strings.Contains(generated, `client.task<TaskInput, TaskResult>("report", 2, decodeResult)`) {
		t.Fatalf("binding generation: code=%d stdout=%s stderr=%s", code, generated, stderr)
	}
	path := writeRunTaskAuthoringFixture(t, "binding.ts", generated)
	if code, _, stderr = runSourceRunCLI(t, append(args, "--check", path)...); code != 0 {
		t.Fatalf("current bindings were rejected: %s", stderr)
	}
	if err := os.WriteFile(path, []byte("// stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ = runSourceRunCLI(t, append(args, "--check", path)...); code == 0 {
		t.Fatal("stale bindings passed")
	}
	if account.reads.Load() != baseline {
		t.Fatal("offline commands refreshed the account capability view")
	}
}

func TestCmdRunTaskTestUsesDraftRunWithoutPublication(t *testing.T) {
	const runID = "33333333-3333-4333-8333-333333333333"
	const fileID = "22222222-2222-4222-8222-222222222222"
	manifest := writeRunTaskAuthoringFixture(t, "task.json", authoringTask)
	fixture := writeRunTaskAuthoringFixture(t, "input.pdf", "fixture bytes")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	t.Setenv("FAAS_TOKEN", "fixture-token")
	for _, tc := range []struct {
		name       string
		validation string
		artifact   bool
		wantError  string
	}{
		{"valid", "valid", true, ""},
		{"invalid result", "invalid", true, "valid result contract"},
		{"missing artifact", "valid", false, "declared output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var submits atomic.Int32
			apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/executions/capabilities":
					_ = json.NewEncoder(w).Encode(api.ExecutionCapabilitiesResponse{Plan: "pro"})
				case r.Method == http.MethodPost && r.URL.Path == "/v1/execution-files":
					_ = json.NewEncoder(w).Encode(api.ExecutionFileResponse{ID: fileID})
				case r.Method == http.MethodPost && r.URL.Path == "/v2/executions":
					submits.Add(1)
					var request api.CreateExecutionRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if r.Header.Get("Idempotency-Key") != "report-v2-ci" || request.EnvironmentID != "11111111-1111-4111-8111-111111111111" ||
						request.Limits == nil || request.Limits.TimeoutMS != 120000 || request.Limits.MaxArtifactBytes != 262144 ||
						len(request.ResultSchema) == 0 || len(request.InputFiles) != 1 || request.InputFiles[0].FileID != fileID ||
						request.InputFiles[0].Path != "input.pdf" || string(request.Input) != `{"title":"Report"}` {
						t.Errorf("smoke request changed: %+v", request)
					}
					_ = json.NewEncoder(w).Encode(api.ExecutionResponse{ID: runID, Status: api.ExecutionStatusQueued})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/executions/"+runID:
					receipt := api.ExecutionResponse{ID: runID, Status: api.ExecutionStatusSucceeded,
						ResultValidation: &api.ExecutionResultValidation{Status: tc.validation}}
					if tc.artifact {
						receipt.Artifacts = []api.ExecutionArtifact{{Name: "report.html"}}
					}
					_ = json.NewEncoder(w).Encode(receipt)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer apiServer.Close()
			t.Setenv("FAAS_API", apiServer.URL)
			code, stdout, stderr := runSourceRunCLI(t, "run", "tasks", "test",
				"--manifest", manifest, "--input", `{"title":"Report"}`, "--input-file", "input.pdf="+fixture,
				"--idempotency-key", "report-v2-ci", "--json")
			if (code == 0) != (tc.wantError == "") || !strings.Contains(stderr, tc.wantError) ||
				!strings.Contains(stdout, runID) || submits.Load() != 1 {
				t.Fatalf("smoke outcome: code=%d stdout=%s stderr=%s submits=%d", code, stdout, stderr, submits.Load())
			}
		})
	}
}
