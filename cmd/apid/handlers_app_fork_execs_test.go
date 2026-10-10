package main

// adr: 732
// Fork exec API: only a running fork takes commands, bounds are validated,
// the pending cap applies, and results read back.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppForkExecs_CreateAndRead(t *testing.T) {
	e := appForkEnv(t, api.PlanPro)
	app, _ := seedAppTaskDeployment(t, e, "my-api")
	rec := e.do(t, http.MethodPost, "/v1/apps/my-api/forks", api.CreateAppForkRequest{}, nil)
	fork := decodeAppFork(t, rec.Body.Bytes())

	cmd := api.CreateAppForkExecRequest{Command: []string{"cat", "/app/hello.txt"}}
	queued := e.do(t, http.MethodPost, "/v1/apps/my-api/forks/"+fork.ID+"/execs", cmd, nil)
	assertProblem(t, queued, http.StatusConflict, api.CodeAppForkExecRefused)

	ctx := context.Background()
	claimed, err := e.store.ClaimNextAppFork(ctx, "schedd", time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.MarkAppForkRunning(ctx, fork.ID, *claimed.LeaseToken, "snap", "ins", time.Now()); err != nil {
		t.Fatal(err)
	}

	bad := e.do(t, http.MethodPost, "/v1/apps/my-api/forks/"+fork.ID+"/execs",
		api.CreateAppForkExecRequest{Command: []string{"a", "b"}, Shell: true}, nil)
	assertProblem(t, bad, http.StatusBadRequest, api.CodeValidation)
	tooLong := 3600
	bad = e.do(t, http.MethodPost, "/v1/apps/my-api/forks/"+fork.ID+"/execs",
		api.CreateAppForkExecRequest{Command: []string{"x"}, TimeoutSeconds: &tooLong}, nil)
	assertProblem(t, bad, http.StatusBadRequest, api.CodeValidation)

	created := e.do(t, http.MethodPost, "/v1/apps/my-api/forks/"+fork.ID+"/execs", cmd, nil)
	if created.Code != http.StatusAccepted {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var exec api.AppForkExecResponse
	if err := json.Unmarshal(created.Body.Bytes(), &exec); err != nil || exec.Status != "queued" || exec.TimeoutSeconds != 60 || exec.MaxOutputBytes != 64<<10 {
		t.Fatalf("exec = %+v, %v", exec, err)
	}

	// schedd runs it.
	running, err := e.store.ClaimNextAppForkExec(ctx, "schedd", time.Now())
	if err != nil || running.ID != exec.ID {
		t.Fatalf("claim = %+v, %v", running, err)
	}
	zero := 0
	if _, err := e.store.FinishAppForkExec(ctx, state.FinishAppForkExecParams{
		ID: exec.ID, Status: state.AppForkExecSucceeded, ExitCode: &zero, Stdout: []byte("hello\n"), FinishedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	got := e.do(t, http.MethodGet, "/v1/apps/my-api/forks/"+fork.ID+"/execs/"+exec.ID, nil, nil)
	var done api.AppForkExecResponse
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &done) != nil || done.Status != "succeeded" || done.Stdout != "hello\n" || *done.ExitCode != 0 {
		t.Fatalf("get = %d %s", got.Code, got.Body.String())
	}
	list := e.do(t, http.MethodGet, "/v1/apps/my-api/forks/"+fork.ID+"/execs", nil, nil)
	var listed api.AppForkExecListResponse
	if list.Code != http.StatusOK || json.Unmarshal(list.Body.Bytes(), &listed) != nil || len(listed.Items) != 1 {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	_ = app
}
