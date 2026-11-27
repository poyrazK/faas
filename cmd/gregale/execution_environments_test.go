package main_test

import (
	"encoding/json"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCmdRunExtendedEnvironmentSubmitsV2(t *testing.T) {
	const env = "12345678-1234-4234-8234-123456789abc"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v2/executions" {
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
		}
		var req api.CreateExecutionRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.EnvironmentID != env || req.Limits == nil || req.Limits.Class != api.ExecutionClassExtended || req.Limits.TimeoutMS != 300000 {
			t.Errorf("invalid admission %+v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(api.ExecutionResponse{ID: "run-1", Status: api.ExecutionStatusQueued, EnvironmentID: env})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "token")
	old := *main.JsonOutput
	*main.JsonOutput = true
	t.Cleanup(func() { *main.JsonOutput = old })
	_, _, restore := main.SwapIO(t)
	defer restore()
	if exit := main.CmdRun([]string{"--runtime", "node24", "--source", "export default ()=>42", "--execution-class", "extended", "--timeout-ms", "300000", "--environment", env}); exit != 0 {
		t.Fatal(exit)
	}
}

func TestCmdEnvironmentLogsPreservesCursorAndReportsGap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/execution-environments/env/events" || r.URL.Query().Get("after") != "3" {
			t.Errorf("wrong diagnostic request %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.ExecutionEnvironmentEventsResponse{Status: "ready", NextSequence: 8, Gap: true, Events: []api.ExecutionEnvironmentEvent{{Sequence: 8, Stage: "publish", Message: "ready", Attempt: 1, CreatedAt: "2026-11-15T00:00:00Z"}}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "token")
	previous := *main.JsonOutput
	*main.JsonOutput = true
	t.Cleanup(func() { *main.JsonOutput = previous })
	_, _, restore := main.SwapIO(t)
	defer restore()
	if code := main.CmdRun([]string{"environments", "logs", "env", "--after", "3", "--follow"}); code != 0 {
		t.Fatal(code)
	}
}
