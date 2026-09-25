package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func createCronCommandRunForDetailsTest(t *testing.T, e testEnv, app state.App, deployment state.Deployment, cron state.Cron) state.AppTask {
	t.Helper()
	var tasks state.AppTaskStore = e.store
	task, err := tasks.CreateAppTask(context.Background(), state.CreateAppTaskParams{
		AccountID: e.acct.ID, AppID: app.ID, DeploymentID: deployment.ID,
		CronID: cron.ID, Kind: state.AppTaskKindCron, Command: cron.Command,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateAppTask: %v", err)
	}
	claimed, err := tasks.ClaimNextAppTask(context.Background(), "cron-run-details-test", time.Now().UTC(), time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextAppTask: %v", err)
	}
	if claimed.ID != task.ID || claimed.LeaseToken == nil {
		t.Fatalf("claimed task = %+v, want task %s with lease", claimed, task.ID)
	}
	running, err := tasks.MarkAppTaskRunning(context.Background(), task.ID, *claimed.LeaseToken, time.Now().UTC())
	if err != nil {
		t.Fatalf("MarkAppTaskRunning: %v", err)
	}
	exitCode := 0
	if _, err := tasks.CompleteAppTask(context.Background(), state.CompleteAppTaskParams{
		ID: task.ID, LeaseToken: *running.LeaseToken, Status: state.AppTaskSucceeded,
		StdoutTail: "daily job complete\n", StderrTail: "notice: cache cold\n",
		ExitCode: &exitCode, FinishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CompleteAppTask: %v", err)
	}
	return task
}

func TestGetCronCommandRunReturnsTaskDetailsOnlyForOwningCron(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, deployment := seedAppTaskDeployment(t, e, "cron-run-detail")
	createCron := func(schedule string) state.Cron {
		t.Helper()
		cron, err := e.store.CreateCronWithOptions(context.Background(), app.ID, schedule, "", true, state.CronOptions{
			Command: []string{"bin/maintenance"},
		})
		if err != nil {
			t.Fatalf("CreateCronWithOptions: %v", err)
		}
		return cron
	}
	cron := createCron("0 * * * *")
	otherCron := createCron("30 * * * *")
	task := createCronCommandRunForDetailsTest(t, e, app, deployment, cron)
	otherTask := createCronCommandRunForDetailsTest(t, e, app, deployment, otherCron)

	path := "/v1/crons/" + cron.ID + "/runs/" + task.ID
	rec := e.do(t, http.MethodGet, path, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET command run = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got api.AppTaskResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode command run: %v", err)
	}
	if got.ID != task.ID || got.Status != api.AppTaskStatusSucceeded || got.ExitCode == nil || *got.ExitCode != 0 ||
		got.StdoutTail != "daily job complete\n" || got.StderrTail != "notice: cache cold\n" {
		t.Fatalf("command run details = %+v", got)
	}

	wrongCron := e.do(t, http.MethodGet, "/v1/crons/"+cron.ID+"/runs/"+otherTask.ID, nil, nil)
	if wrongCron.Code != http.StatusNotFound {
		t.Fatalf("run from sibling cron = %d, want 404; body=%s", wrongCron.Code, wrongCron.Body.String())
	}
	foreignAccount, err := e.store.CreateAccount(context.Background(), "cron-run-foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount foreign: %v", err)
	}
	foreignToken, foreignHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey foreign: %v", err)
	}
	if _, err := e.store.CreateAPIKey(context.Background(), foreignAccount.ID, foreignHash, "foreign", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey foreign: %v", err)
	}
	foreign := e
	foreign.acct = foreignAccount
	foreign.key = foreignToken
	foreignRead := foreign.do(t, http.MethodGet, path, nil, nil)
	if foreignRead.Code != http.StatusNotFound {
		t.Fatalf("cross-account run detail = %d, want 404; body=%s", foreignRead.Code, foreignRead.Body.String())
	}
}

func TestGetCronCommandRunHidesHTTPCronRuns(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	cronID, _ := seedCron(t, e, "cron-run-detail-http", "0 * * * *")
	rec := e.do(t, http.MethodGet, "/v1/crons/"+cronID+"/runs/01234567-89ab-cdef-0123-456789abcdef", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("HTTP cron run details = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
