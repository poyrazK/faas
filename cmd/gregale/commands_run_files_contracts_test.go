package main_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdRunStoredFilesProbeUploadAndContractExit(t *testing.T) {
	data := []byte("file input")
	digest := sha256.Sum256(data)
	sha := "sha256:" + hex.EncodeToString(digest[:])
	id := uuid.NewString()
	var calls []string
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/execution-files":
			_ = json.NewEncoder(w).Encode(api.ExecutionFileResponse{ID: id, Status: "missing", SizeBytes: int64(len(data)), SHA256: sha, UploadRequired: true})
		case r.Method == "POST" && r.URL.Path == "/v2/executions":
			posts++
			if r.Header.Get("Idempotency-Key") != "same-file-key" {
				t.Error("admission key changed")
			}
			var req api.CreateExecutionRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || len(req.InputFiles) != 1 || req.InputFiles[0].FileID != id || string(req.ResultSchema) != `{"type":"integer"}` {
				t.Error("request lost file or contract")
			}
			if posts == 1 {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(409)
				_ = json.NewEncoder(w).Encode(api.NewProblem(409, "execution_files_not_ready", "Run files not ready", "no Run admitted"))
				return
			}
			_ = json.NewEncoder(w).Encode(api.ExecutionResponse{ID: "run-1", Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`"debug-value"`), ResultValidation: &api.ExecutionResultValidation{Status: "invalid", Message: "Run result does not match result_schema"}})
		case r.Method == "PUT":
			body, _ := io.ReadAll(r.Body)
			if string(body) != string(data) || r.Header.Get("X-Content-SHA256") != sha || r.ContentLength != int64(len(data)) {
				t.Error("upload did not preserve bytes or checksum")
			}
			_ = json.NewEncoder(w).Encode(api.ExecutionFileResponse{ID: id, Status: "ready", SizeBytes: int64(len(data)), SHA256: sha})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fixture-token")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	dir := t.TempDir()
	input, schema := filepath.Join(dir, "data.bin"), filepath.Join(dir, "schema.json")
	if err := os.WriteFile(input, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schema, []byte(`{"type":"integer"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runSourceRunCLI(t, "--json", "run", "--source", "return 42", "--input-file", "data.bin="+input, "--result-schema", schema, "--idempotency-key", "same-file-key", "--wait")
	if code != 1 {
		t.Fatalf("contract failure returned %d, stdout=%s stderr=%s", code, stdout, stderr)
	}
	var receipt api.ExecutionResponse
	if json.Unmarshal([]byte(stdout), &receipt) != nil || !receipt.ContractFailed() || receipt.Status != api.ExecutionStatusSucceeded {
		t.Fatal("contract receipt was lost", stdout, stderr)
	}
	want := []string{"POST /v1/execution-files", "POST /v2/executions", "PUT /v1/execution-files/" + id + "/content", "POST /v2/executions"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("transfer order: %v", calls)
	}
}

func TestCmdRunStoredFilesNeverUploadsAfterUncertainAdmission(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"capacity", http.StatusServiceUnavailable, "execution_files_not_ready"},
		{"different_conflict", http.StatusConflict, "execution_conflict"},
		{"server_failure", http.StatusInternalServerError, "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const id = "12345678-1234-4234-8234-123456789abc"
			sum := sha256.Sum256([]byte("input"))
			var uploads atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/execution-files" {
					_ = json.NewEncoder(w).Encode(api.ExecutionFileResponse{ID: id, UploadRequired: true, SizeBytes: 5, SHA256: "sha256:" + hex.EncodeToString(sum[:])})
					return
				}
				if r.Method == http.MethodPut {
					uploads.Add(1)
				}
				api.WriteProblem(w, api.NewProblem(tc.status, tc.code, "Admission unavailable", "no retry authority"))
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fixture-token")
			t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
			input := filepath.Join(t.TempDir(), "input.bin")
			if err := os.WriteFile(input, []byte("input"), 0o600); err != nil {
				t.Fatal(err)
			}
			code, _, _ := runSourceRunCLI(t, "--json", "run", "--source", "return 42", "--input-file", "data.bin="+input, "--idempotency-key", "uncertain-file-key")
			if code == 0 || uploads.Load() != 0 {
				t.Fatalf("uncertain admission returned %d and uploaded %d files", code, uploads.Load())
			}
		})
	}
}
