// adr: 230 — public app-task admission pins a live deployment, remains
// fail-closed by default, and never exposes scheduler or artifact internals.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const appTaskTestDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func enableAppTaskAPIForTest(e *testEnv) {
	e.s.WithAppTaskAPIEnabled(true)
}

func seedAppTaskDeployment(t *testing.T, e testEnv, slug string) (state.App, state.Deployment) {
	t.Helper()
	app := seedOneApp(t, e, slug)
	deployment, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID:          app.ID,
		ImageDigest:    appTaskTestDigest,
		Kind:           state.DeploymentKindImage,
		Status:         state.DeployLive,
		TrafficPercent: 100,
		CreatedAt:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := e.store.SetDeploymentRootfs(
		context.Background(), deployment.ID,
		"/srv/fc/apps/"+slug+"/"+deployment.ID+".ext4",
		"apps/"+slug+"/"+deployment.ID+".ext4", 4096,
	); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	deployment, err = e.store.DeploymentByID(context.Background(), deployment.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	return app, deployment
}

func createAppTaskForTest(t *testing.T, e testEnv, slug string, request api.CreateAppTaskRequest) api.AppTaskResponse {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/v1/apps/"+slug+"/tasks", request, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST app task = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var response api.AppTaskResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode app task response: %v", err)
	}
	return response
}

func TestAppTaskAPIIsDisabledByDefault(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, http.MethodPost, "/v1/apps/not-loaded/tasks", api.CreateAppTaskRequest{
		Command: []string{"bin/maintenance"},
	}, nil)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("POST app task with gate off = %d, want 501; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), api.CodeNotImplemented) {
		t.Fatalf("disabled response missing %q: %s", api.CodeNotImplemented, rec.Body.String())
	}
}

func TestCreateAppTaskPinsLiveDeploymentAndHidesRuntimeInternals(t *testing.T) {
	e := setup(t, api.PlanHobby)
	enableAppTaskAPIForTest(&e)
	app, deployment := seedAppTaskDeployment(t, e, "task-pin")

	rec := e.do(t, http.MethodPost, "/v1/apps/task-pin/tasks", api.CreateAppTaskRequest{
		Command: []string{"bin/maintenance", "--compact"},
	}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST app task = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var response api.AppTaskResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID == "" || response.AppID != app.ID || response.DeploymentID != deployment.ID {
		t.Fatalf("unexpected task identity: %+v", response)
	}
	if response.Kind != api.AppTaskKindManual || response.Status != api.AppTaskStatusQueued {
		t.Fatalf("unexpected task lifecycle: %+v", response)
	}
	if response.TimeoutSeconds != api.AppTaskDefaultTimeoutSeconds || response.MaxOutputBytes != api.AppTaskDefaultMaxOutputBytes {
		t.Fatalf("defaults = timeout %d output %d", response.TimeoutSeconds, response.MaxOutputBytes)
	}
	for _, forbidden := range []string{"artifact_key", "image_digest", "lease_token", "lease_owner", "lease_expires_at"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("response exposed %q: %s", forbidden, rec.Body.String())
		}
	}

	stored, err := e.store.AppTaskByID(context.Background(), e.acct.ID, app.ID, response.ID)
	if err != nil {
		t.Fatalf("AppTaskByID: %v", err)
	}
	if stored.DeploymentID != deployment.ID || stored.ArtifactKey != deployment.RootfsKey || stored.ImageDigest != deployment.ImageDigest {
		t.Fatalf("stored task did not pin deployment artifact: %+v", stored)
	}
}

func TestCreateAppTaskRequiresMaterializedLiveDeployment(t *testing.T) {
	e := setup(t, api.PlanHobby)
	enableAppTaskAPIForTest(&e)
	seedOneApp(t, e, "task-no-deploy")
	rec := e.do(t, http.MethodPost, "/v1/apps/task-no-deploy/tasks", api.CreateAppTaskRequest{
		Command: []string{"bin/maintenance"},
	}, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), api.CodeConflict) {
		t.Fatalf("POST without live deployment = %d %s, want 409 conflict", rec.Code, rec.Body.String())
	}

	app := seedOneApp(t, e, "task-no-rootfs")
	if _, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, ImageDigest: appTaskTestDigest, Kind: state.DeploymentKindImage,
		Status: state.DeployLive, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodPost, "/v1/apps/task-no-rootfs/tasks", api.CreateAppTaskRequest{
		Command: []string{"bin/maintenance"},
	}, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), api.CodeConflict) {
		t.Fatalf("POST without rootfs = %d %s, want 409 conflict", rec.Code, rec.Body.String())
	}
}

func TestAppTaskValidation(t *testing.T) {
	e := setup(t, api.PlanHobby)
	enableAppTaskAPIForTest(&e)
	seedAppTaskDeployment(t, e, "task-validation")

	for name, request := range map[string]api.CreateAppTaskRequest{
		"empty command": {Command: nil},
		"shell argv":    {Command: []string{"echo", "hello"}, CommandShell: true},
		"bad timeout":   {Command: []string{"echo"}, TimeoutSeconds: 3601},
		"bad output":    {Command: []string{"echo"}, MaxOutputBytes: 100},
	} {
		t.Run(name, func(t *testing.T) {
			rec := e.do(t, http.MethodPost, "/v1/apps/task-validation/tasks", request, nil)
			if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), api.CodeValidation) {
				t.Fatalf("invalid request = %d %s, want 422 validation", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAppTaskReadsCancellationAndAppScope(t *testing.T) {
	e := setup(t, api.PlanHobby)
	enableAppTaskAPIForTest(&e)
	seedAppTaskDeployment(t, e, "task-lifecycle")
	seedAppTaskDeployment(t, e, "task-other-app")
	task := createAppTaskForTest(t, e, "task-lifecycle", api.CreateAppTaskRequest{
		Command: []string{"bin/maintenance"}, TimeoutSeconds: 30, MaxOutputBytes: 2048,
	})

	get := e.do(t, http.MethodGet, "/v1/apps/task-lifecycle/tasks/"+task.ID, nil, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("GET app task = %d; body=%s", get.Code, get.Body.String())
	}
	wrongApp := e.do(t, http.MethodGet, "/v1/apps/task-other-app/tasks/"+task.ID, nil, nil)
	if wrongApp.Code != http.StatusNotFound {
		t.Fatalf("cross-app GET = %d %s, want 404", wrongApp.Code, wrongApp.Body.String())
	}
	badID := e.do(t, http.MethodGet, "/v1/apps/task-lifecycle/tasks/not-a-uuid", nil, nil)
	if badID.Code != http.StatusNotFound {
		t.Fatalf("malformed task id = %d %s, want 404", badID.Code, badID.Body.String())
	}

	other, err := e.store.CreateAccount(context.Background(), "app-task-other@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, otherHash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(context.Background(), other.ID, otherHash, "other", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/apps/task-lifecycle/tasks/"+task.ID, nil)
	request.Header.Set("Authorization", "Bearer "+otherToken)
	crossAccount := httptest.NewRecorder()
	e.h.ServeHTTP(crossAccount, request)
	if crossAccount.Code != http.StatusNotFound {
		t.Fatalf("cross-account GET = %d %s, want 404", crossAccount.Code, crossAccount.Body.String())
	}

	list := e.do(t, http.MethodGet, "/v1/apps/task-lifecycle/tasks?limit=1", nil, nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list app tasks = %d; body=%s", list.Code, list.Body.String())
	}
	var page api.AppTaskListResponse
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Tasks) != 1 || page.Tasks[0].ID != task.ID || page.NextOffset != -1 {
		t.Fatalf("unexpected task page: %+v", page)
	}

	cancel := e.do(t, http.MethodDelete, "/v1/apps/task-lifecycle/tasks/"+task.ID, nil, nil)
	if cancel.Code != http.StatusAccepted {
		t.Fatalf("DELETE app task = %d; body=%s", cancel.Code, cancel.Body.String())
	}
	var cancelled api.AppTaskResponse
	if err := json.Unmarshal(cancel.Body.Bytes(), &cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != api.AppTaskStatusCancelled || cancelled.FinishedAt == nil {
		t.Fatalf("cancelled task = %+v", cancelled)
	}
}

func TestAppTaskResponseProjectsTerminalEvidence(t *testing.T) {
	now := time.Now().UTC()
	exitCode := 17
	failureCode := "process_exit"
	failureMessage := "command exited unsuccessfully"
	lease := "scheduler-secret"
	response := appTaskResponse(state.AppTask{
		ID:              "2bdd4251-f567-4a48-9f66-a155bbfa7751",
		AppID:           "app-id",
		DeploymentID:    "deployment-id",
		DeploymentScope: "default",
		Kind:            state.AppTaskKindManual,
		Command:         []string{"bin/task"},
		Status:          state.AppTaskFailed,
		TimeoutSeconds:  60,
		MaxOutputBytes:  4096,
		StdoutTail:      "stdout",
		StderrTail:      "stderr",
		OutputTruncated: true,
		ExitCode:        &exitCode,
		FailureCode:     &failureCode,
		FailureMessage:  &failureMessage,
		LeaseToken:      &lease,
		LeaseOwner:      &lease,
		StartedAt:       &now,
		FinishedAt:      &now,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if response.Status != api.AppTaskStatusFailed || response.StdoutTail != "stdout" || response.StderrTail != "stderr" || !response.OutputTruncated {
		t.Fatalf("terminal output projection = %+v", response)
	}
	if response.ExitCode == nil || *response.ExitCode != exitCode || response.Failure == nil || response.Failure.Code != failureCode {
		t.Fatalf("terminal failure projection = %+v", response)
	}
	if response.StartedAt == nil || response.FinishedAt == nil {
		t.Fatalf("terminal timestamps missing: %+v", response)
	}
}

func TestAppTaskResponseProjectsScheduledRetry(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	retryAt := now.Add(30 * time.Second)
	failureCode, failureMessage := "command_failed", "temporary outage"
	response := appTaskResponse(state.AppTask{
		ID: "2bdd4251-f567-4a48-9f66-a155bbfa7751", AppID: "app-id", DeploymentID: "deployment-id",
		DeploymentScope: "default", Kind: state.AppTaskKindCron, Command: []string{"bin/task"},
		Status: state.AppTaskQueued, TimeoutSeconds: 60, MaxOutputBytes: 4096,
		RetryMax: 3, RetryBackoffSeconds: 15, AttemptCount: 2, RetryAt: &retryAt,
		FailureCode: &failureCode, FailureMessage: &failureMessage, CreatedAt: now, UpdatedAt: now,
	})
	if response.Status != api.AppTaskStatusQueued || response.AttemptCount != 2 ||
		response.RetryMax != 3 || response.RetryBackoffSeconds != 15 || response.RetryAt == nil ||
		*response.RetryAt != retryAt.Format(time.RFC3339Nano) {
		t.Fatalf("scheduled retry projection = %+v", response)
	}
	if response.Failure == nil || response.Failure.Code != failureCode || response.Failure.Message != failureMessage ||
		response.FinishedAt != nil {
		t.Fatalf("queued retry evidence = %+v", response)
	}
}

func TestAppTaskAPIGateEnv(t *testing.T) {
	getenv := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	if appTaskAPIEnabledFromEnv(getenv(map[string]string{})) {
		t.Fatal("empty environment enabled app task API")
	}
	if !appTaskAPIEnabledFromEnv(getenv(map[string]string{"FAAS_APP_TASK_API_ENABLED": "1"})) {
		t.Fatal("FAAS_APP_TASK_API_ENABLED=1 did not enable app task API")
	}
	if appTaskAPIEnabledFromEnv(getenv(map[string]string{"FAAS_APP_TASK_API_ENABLED": "true"})) {
		t.Fatal("non-canonical app task API value enabled app task API")
	}
}
