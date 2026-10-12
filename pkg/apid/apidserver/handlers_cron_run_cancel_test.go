package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func createQueuedCronRunForCancelTest(t *testing.T, e apidserver.TestEnv, app state.App, deployment state.Deployment, cron state.Cron) state.AppTask {
	t.Helper()
	tasks := (*e.Store())
	task, err := tasks.CreateAppTask(context.Background(), state.CreateAppTaskParams{
		AccountID: (*e.Acct()).ID, AppID: app.ID, DeploymentID: deployment.ID,
		CronID: cron.ID, Kind: state.AppTaskKindCron, Command: cron.Command,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateAppTask: %v", err)
	}
	return task
}

func TestCancelCronCommandRunHandlesQueuedAndRunningTasks(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	app, deployment := apidserver.SeedAppTaskDeployment(t, e, "cron-run-cancel")
	cron, err := (*e.Store()).CreateCronWithOptions(context.Background(), app.ID, "0 * * * *", "", true, state.CronOptions{
		Command: []string{"bin/maintenance"},
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}

	queued := createQueuedCronRunForCancelTest(t, e, app, deployment, cron)
	path := "/v1/crons/" + cron.ID + "/runs/" + queued.ID + "/cancel"
	idempotencyHeaders := map[string]string{"Idempotency-Key": "cron-run-cancel-queued"}
	first := e.Do(t, http.MethodPost, path, nil, idempotencyHeaders)
	if first.Code != http.StatusAccepted {
		t.Fatalf("cancel queued run = %d, want 202; body=%s", first.Code, first.Body.String())
	}
	var queuedResponse api.AppTaskResponse
	if err := json.Unmarshal(first.Body.Bytes(), &queuedResponse); err != nil {
		t.Fatalf("decode queued cancellation: %v", err)
	}
	if queuedResponse.ID != queued.ID || queuedResponse.Status != api.AppTaskStatusCancelled {
		t.Fatalf("queued cancellation = %+v, want cancelled task %s", queuedResponse, queued.ID)
	}

	// Repeated requests preserve the terminal result rather than producing a
	// second transition or an error.
	replay := e.Do(t, http.MethodPost, path, nil, idempotencyHeaders)
	if replay.Code != http.StatusAccepted {
		t.Fatalf("repeat cancel queued run = %d, want 202; body=%s", replay.Code, replay.Body.String())
	}
	if replay.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("repeat cancel response header = %q, want true", replay.Header().Get("Idempotent-Replayed"))
	}
	var replayResponse api.AppTaskResponse
	if err := json.Unmarshal(replay.Body.Bytes(), &replayResponse); err != nil || replayResponse.Status != api.AppTaskStatusCancelled {
		t.Fatalf("repeat queued cancellation = %+v, err=%v; want cancelled", replayResponse, err)
	}

	running := createQueuedCronRunForCancelTest(t, e, app, deployment, cron)
	tasks := (*e.Store())
	claimed, err := tasks.ClaimNextAppTask(context.Background(), "cron-run-cancel-test", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextAppTask: %v", err)
	}
	if claimed.ID != running.ID || claimed.LeaseToken == nil {
		t.Fatalf("claimed task = %+v, want task %s with lease", claimed, running.ID)
	}
	if _, err := tasks.MarkAppTaskRunning(context.Background(), running.ID, *claimed.LeaseToken, time.Now().UTC()); err != nil {
		t.Fatalf("MarkAppTaskRunning: %v", err)
	}

	runningPath := "/v1/crons/" + cron.ID + "/runs/" + running.ID + "/cancel"
	active := e.Do(t, http.MethodPost, runningPath, nil, nil)
	if active.Code != http.StatusAccepted {
		t.Fatalf("cancel running run = %d, want 202; body=%s", active.Code, active.Body.String())
	}
	var activeResponse api.AppTaskResponse
	if err := json.Unmarshal(active.Body.Bytes(), &activeResponse); err != nil {
		t.Fatalf("decode running cancellation: %v", err)
	}
	if activeResponse.ID != running.ID || activeResponse.Status != api.AppTaskStatusRunning || activeResponse.CancelRequestedAt == nil {
		t.Fatalf("running cancellation = %+v, want active task with cancel_requested_at", activeResponse)
	}
}

func TestCancelCronCommandRunHidesWrongCronAndForeignAccount(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	app, deployment := apidserver.SeedAppTaskDeployment(t, e, "cron-run-cancel-ownership")
	createCron := func(schedule string) state.Cron {
		t.Helper()
		cron, err := (*e.Store()).CreateCronWithOptions(context.Background(), app.ID, schedule, "", true, state.CronOptions{
			Command: []string{"bin/maintenance"},
		})
		if err != nil {
			t.Fatalf("CreateCronWithOptions: %v", err)
		}
		return cron
	}
	cron := createCron("0 * * * *")
	otherCron := createCron("30 * * * *")
	task := createQueuedCronRunForCancelTest(t, e, app, deployment, otherCron)

	path := "/v1/crons/" + cron.ID + "/runs/" + task.ID + "/cancel"
	wrongCron := e.Do(t, http.MethodPost, path, nil, nil)
	if wrongCron.Code != http.StatusNotFound {
		t.Fatalf("cancel run from sibling cron = %d, want 404; body=%s", wrongCron.Code, wrongCron.Body.String())
	}
	manualTask, err := (*e.Store()).CreateAppTask(context.Background(), state.CreateAppTaskParams{
		AccountID: (*e.Acct()).ID, AppID: app.ID, DeploymentID: deployment.ID,
		Kind: state.AppTaskKindManual, Command: []string{"bin/manual"},
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateAppTask manual: %v", err)
	}
	manualRun := e.Do(t, http.MethodPost, "/v1/crons/"+cron.ID+"/runs/"+manualTask.ID+"/cancel", nil, nil)
	if manualRun.Code != http.StatusNotFound {
		t.Fatalf("cancel manual app task as cron run = %d, want 404; body=%s", manualRun.Code, manualRun.Body.String())
	}

	foreignAccount, err := (*e.Store()).CreateAccount(context.Background(), "cron-run-cancel-foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount foreign: %v", err)
	}
	foreignToken, foreignHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey foreign: %v", err)
	}
	if _, err := (*e.Store()).CreateAPIKey(context.Background(), foreignAccount.ID, foreignHash, "foreign", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey foreign: %v", err)
	}
	foreign := e
	(*foreign.Acct()) = foreignAccount
	(*foreign.Key()) = foreignToken
	foreignRequest := foreign.Do(t, http.MethodPost, "/v1/crons/"+otherCron.ID+"/runs/"+task.ID+"/cancel", nil, nil)
	if foreignRequest.Code != http.StatusNotFound {
		t.Fatalf("cross-account cancel = %d, want 404; body=%s", foreignRequest.Code, foreignRequest.Body.String())
	}
	if foreignRequest.Body.String() != wrongCron.Body.String() {
		t.Fatalf("cross-account 404 body = %q, sibling-cron 404 body = %q; want byte-identical bodies", foreignRequest.Body.String(), wrongCron.Body.String())
	}
}

func TestCancelCronCommandRunHidesHTTPCronRuns(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	cronID, _ := seedCron(t, e, "cron-run-cancel-http", "0 * * * *")
	rec := e.Do(t, http.MethodPost, "/v1/crons/"+cronID+"/runs/01234567-89ab-cdef-0123-456789abcdef/cancel", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cancel HTTP cron run = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
