package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A batch send accepts its messages in order, refuses an oversized one with
// the plan's message-size problem and does not attempt the rest; an empty or
// oversized batch is refused whole (ADR-20261018101500 amendment 2026-10-31).
func TestQueueSendBatchAcceptsInOrderAndStopsAtTheFirstRefusal(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	ctx := context.Background()
	if _, err := (*e.Store()).CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "batch-worker", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker}); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodPost, "/v1/apps/batch-worker/queue-bindings", api.CreateQueueBindingRequest{Name: "jobs", QueueName: "jobs"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create binding: %d %s", rec.Code, rec.Body)
	}
	big := json.RawMessage(`{"blob":"` + strings.Repeat("x", api.MustLimitsFor(api.PlanHobby).MaxSourceBytesPerInvocation) + `"}`)
	rec := e.Do(t, http.MethodPost, "/v1/apps/batch-worker/queues/send-batch", api.QueueSendBatchRequest{QueueName: "jobs", Messages: []api.QueueSendBatchMessage{
		{Payload: json.RawMessage(`{"n":0}`)}, {Payload: json.RawMessage(`{"n":1}`)}, {Payload: big}, {Payload: json.RawMessage(`{"n":3}`)},
	}}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch send: %d %s", rec.Code, rec.Body)
	}
	var resp api.QueueSendBatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	want := []string{"accepted", "accepted", "rejected", "not_attempted"}
	if len(resp.Results) != len(want) {
		t.Fatalf("results = %+v", resp.Results)
	}
	for i, result := range resp.Results {
		if result.Index != i || result.Status != want[i] || (result.Status == "accepted") != (result.Message != nil && result.Message.ID != "") {
			t.Fatalf("result %d = %+v, want %s", i, result, want[i])
		}
	}
	if resp.Results[2].Problem == nil || resp.Results[2].Problem.Code != api.CodePlanSourceBytes {
		t.Fatalf("oversized message problem = %+v", resp.Results[2].Problem)
	}
	stats, err := (*e.Store()).QueueState(ctx, appIDBySlug(t, e, "batch-worker"))
	if err != nil || stats.Depth != 2 {
		t.Fatalf("queue depth = %+v, %v; want the two accepted messages", stats, err)
	}
	for _, messages := range [][]api.QueueSendBatchMessage{nil, make([]api.QueueSendBatchMessage, api.QueueSendBatchMaxMessages+1)} {
		rec := e.Do(t, http.MethodPost, "/v1/apps/batch-worker/queues/send-batch", api.QueueSendBatchRequest{QueueName: "jobs", Messages: messages}, nil)
		apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	}
}

// A worker the plan cannot run is refused when it is declared, not left to a
// pool that never starts or is silently capped (ADR-20261018101500
// amendment 2026-10-31).
func TestUpdateAppRefusesWorkersOverThePlan(t *testing.T) {
	worker := api.ExecutionModeWorker
	for _, tc := range []struct {
		plan     api.Plan
		replicas *api.WorkerScaling
		limit    int64
	}{
		{plan: api.PlanFree, limit: 0},
		{plan: api.PlanHobby, replicas: &api.WorkerScaling{Max: 3}, limit: 1},
	} {
		t.Run(string(tc.plan), func(t *testing.T) {
			e := apidserver.Setup(t, tc.plan)
			if _, err := (*e.Store()).CreateApp(context.Background(), state.App{AccountID: (*e.Acct()).ID, Slug: "plan-worker", Type: state.AppTypeApp, RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 1}); err != nil {
				t.Fatal(err)
			}
			rec := e.Do(t, http.MethodPatch, "/v1/apps/plan-worker", api.UpdateAppRequest{ExecutionMode: &worker, WorkerReplicas: tc.replicas}, nil)
			apidserver.AssertProblem(t, rec, http.StatusForbidden, api.CodePlanWorkerReplicas)
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Limit == nil || *problem.Limit != tc.limit {
				t.Fatalf("problem limit = %v, want %d", problem.Limit, tc.limit)
			}
		})
	}
	e := apidserver.Setup(t, api.PlanPro)
	if _, err := (*e.Store()).CreateApp(context.Background(), state.App{AccountID: (*e.Acct()).ID, Slug: "pro-worker", Type: state.AppTypeApp, RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodPatch, "/v1/apps/pro-worker", api.UpdateAppRequest{ExecutionMode: &worker, WorkerReplicas: &api.WorkerScaling{Max: 3}}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Pro worker with max 3: %d %s", rec.Code, rec.Body)
	}
}

func appIDBySlug(t *testing.T, e apidserver.TestEnv, slug string) string {
	t.Helper()
	app, err := (*e.Store()).AppBySlug(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	return app.ID
}
