package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 249
func TestCmdBindingsSmokeRunsInCallerTaskAndReportsPinnedResponse(t *testing.T) {
	const targetDeployment = "d6e281f3-f5b2-436c-b4ad-8529a956609c"
	const callerDeployment = "8e37c9f3-69cb-4c20-a524-a6a5fe17e63a"
	reportJSON, err := json.Marshal(api.ServiceBindingSmokeReport{
		Service:            "billing",
		TargetDeploymentID: targetDeployment,
		URL:                "https://billing.internal",
		Path:               "/health",
		HTTPStatus:         http.StatusNoContent,
		ExpectedStatus:     "204",
		ElapsedMillis:      17,
		Passed:             true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var created api.CreateAppTaskRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api":
			_ = json.NewEncoder(w).Encode(api.AppResponse{
				ID: "app-1", Slug: "api",
				ServiceBindings: []api.AppServiceBinding{{Binding: api.ServiceBindingEnvKey("billing"), Service: "billing"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks":
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Errorf("decode task request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(api.AppTaskResponse{
				ID: "task-1", AppID: "app-1", DeploymentID: callerDeployment,
				Kind: api.AppTaskKindManual, Status: api.AppTaskStatusSucceeded, StdoutTail: string(reportJSON),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	previousJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = previousJSON })
	var out bytes.Buffer
	previousOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previousOut })

	code := run([]string{"bindings", "smoke", "api", "billing", "--deployment", targetDeployment, "--path", "/health?token=do-not-report", "--expect-status", "204", "--poll-interval", "1ms", "--wait-timeout", "1s"})
	if code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	wantCommand := []string{api.AppTaskServiceBindingSmokeCommand, "billing", targetDeployment, "/health?token=do-not-report", "204"}
	if !reflect.DeepEqual(created.Command, wantCommand) || created.CommandShell {
		t.Fatalf("task command = %+v, want argv=%v without shell", created, wantCommand)
	}
	if created.TimeoutSeconds != bindingSmokeTaskTimeoutSeconds || created.MaxOutputBytes != 4096 {
		t.Fatalf("task limits = %+v", created)
	}
	var got api.ServiceBindingSmokeReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode report: %v; output=%s", err, out.String())
	}
	if !got.Passed || got.App != "api" || got.Service != "billing" || got.TargetDeploymentID != targetDeployment ||
		got.CallerDeploymentID != callerDeployment || got.Path != "/health" || got.HTTPStatus != http.StatusNoContent {
		t.Fatalf("report = %+v", got)
	}
	if strings.Contains(out.String(), "do-not-report") {
		t.Fatalf("JSON report exposed request query: %s", out.String())
	}
}

// adr: 249
func TestCmdBindingsSmokeRequiresDeclaredServiceAndExplicitTarget(t *testing.T) {
	var taskCreates int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api" {
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "api"})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks" {
			taskCreates++
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	for _, args := range [][]string{
		{"bindings", "smoke", "api", "billing", "--path", "/health"},
		{"bindings", "smoke", "api", "billing", "--deployment", "d6e281f3-f5b2-436c-b4ad-8529a956609c", "--path", "/health"},
	} {
		if code := run(args); code == 0 {
			t.Errorf("accepted incomplete or unbound smoke request %v", args)
		}
	}
	if taskCreates != 0 {
		t.Fatalf("created %d task(s) for a smoke request without a declared binding", taskCreates)
	}
}

// adr: 249
func TestCmdBindingsSmokeRejectsNonOriginPathBeforeTaskAdmission(t *testing.T) {
	var apiCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		apiCalls++
		http.Error(w, "unexpected API request", http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	code := run([]string{"bindings", "smoke", "api", "billing", "--deployment", "d6e281f3-f5b2-436c-b4ad-8529a956609c", "--path", "https://outside.example/health"})
	if code == 0 || apiCalls != 0 {
		t.Fatalf("exit=%d API calls=%d for non-origin path", code, apiCalls)
	}
}
