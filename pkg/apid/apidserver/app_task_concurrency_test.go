package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func setAppTaskConcurrency(t *testing.T, e apidserver.TestEnv, value string) {
	t.Helper()
	if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigAppTaskConcurrency, json.RawMessage(value)); err != nil {
		t.Fatalf("apply app_task_concurrency: %v", err)
	}
}

// An account at its plan's cap gets a 429 naming the limit and how to free a
// slot; platform probes still run, and an operator 0 pauses the plan.
func TestAppTaskConcurrencyCap(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.EnableAppTaskAPIForTest(&e)
	apidserver.SeedAppTaskDeployment(t, e, "task-cap")
	setAppTaskConcurrency(t, e, `{"hobby": 1}`)

	rec := e.Do(t, http.MethodPost, "/v1/apps/task-cap/tasks", api.CreateAppTaskRequest{Command: []string{"bin/one"}}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first task = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	rec = e.Do(t, http.MethodPost, "/v1/apps/task-cap/tasks", api.CreateAppTaskRequest{Command: []string{"bin/two"}}, nil)
	apidserver.AssertProblem(t, rec, http.StatusTooManyRequests, api.CodePlanLimitAppTasks)
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Limit == nil || *problem.Limit != 1 || problem.Observed == nil || *problem.Observed != 1 {
		t.Fatalf("limit/observed = %v/%v, want 1/1", problem.Limit, problem.Observed)
	}
	if !strings.Contains(problem.Detail, "gregale app tasks cancel task-cap") {
		t.Fatalf("detail does not name the fix: %s", problem.Detail)
	}

	setAppTaskConcurrency(t, e, `{"hobby": 0}`)
	rec = e.Do(t, http.MethodPost, "/v1/apps/task-cap/tasks", api.CreateAppTaskRequest{Command: []string{"bin/three"}}, nil)
	apidserver.AssertProblem(t, rec, http.StatusTooManyRequests, api.CodePlanLimitAppTasks)
	if !strings.Contains(rec.Body.String(), "paused") {
		t.Fatalf("a 0 cap should read as paused: %s", rec.Body.String())
	}
}

func TestAppTaskConcurrencyConfigValidation(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	for _, bad := range []string{`{"enterprise": 1}`, `{"pro": -1}`, `3`} {
		if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigAppTaskConcurrency, json.RawMessage(bad)); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}
