package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCmdRunTasksRoutingIdentityAndContractExit(t *testing.T) {
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/run-tasks/invoke" || r.Method != "POST" {
			t.Error("task command routed outside the catalog", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		posts++
		if r.Header.Get("Idempotency-Key") != "report-call" {
			t.Error("retry key lost")
		}
		var body api.InvokeExecutionTaskRequest
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Name != "report" || body.Version != 1 || string(body.Input) != `{"rows":3}` {
			t.Error("task arguments changed")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(api.ExecutionResponse{ID: "run", Status: api.ExecutionStatusSucceeded, Task: &api.ExecutionTaskRef{Name: "report", Version: 1}, ResultValidation: &api.ExecutionResultValidation{Status: "invalid", Message: "Run result does not match result_schema"}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	for range 2 {
		code, _, stderr := runSourceRunCLI(t, "run", "tasks", "invoke", "report@v1", "--input", `{"rows":3}`, "--idempotency-key", "report-call", "--wait", "--json")
		if code != 1 {
			t.Fatal("invalid contract accepted", code, stderr)
		}
	}
	if posts != 2 {
		t.Fatal("task invocation was not dispatched", posts)
	}
}
