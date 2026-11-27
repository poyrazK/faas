package apidserver_test

import (
	"encoding/json"
	"errors"
	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNamedPipelineRetryRestartBudgetAndCancellation(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	identity := apidserver.EnableExecutionAPIForTest(t, &e)
	old, olds := *apidserver.MfaIdentity, *apidserver.MfaIdentities
	*apidserver.MfaIdentity = func() *age.X25519Identity { return identity }
	*apidserver.MfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { *apidserver.MfaIdentity, *apidserver.MfaIdentities = old, olds })
	server := *e.S()
	store := *e.Store()
	server.WithExecutionV2Enabled(true).WithExecutionTasksEnabled(true)
	definition := apidserver.ExecutionRequest()
	definition.Input = nil
	definition.Limits = &api.ExecutionLimitRequest{TimeoutMS: 30000}
	pub := api.PublishExecutionTaskRequest{Name: "report", Version: 1, Definition: definition}
	if r := e.Do(t, http.MethodPost, "/v1/run-tasks", pub, nil); r.Code != 201 {
		t.Fatal(r.Code, r.Body.String())
	}
	max := int64(2)
	if _, err := store.PutRunBudget(t.Context(), e.Acct().ID, "task", "report", api.SetRunBudgetRequest{Enabled: true, Limits: api.RunBudgetLimits{MaxInvocationsPerDay: &max}}); err != nil {
		t.Fatal(err)
	}
	request := api.CreateManagedExecutionWorkflowRequest{WorkflowID: "retry-pipeline", Version: "v1", Pipeline: &api.ExecutionPipelineOptions{TotalTimeoutMS: 10000}, Steps: []api.CreateManagedExecutionWorkflowStep{{Label: "report", Task: &api.InvokeExecutionTaskRequest{Name: "report", Version: 1, Input: json.RawMessage(`{"private":"customer-input"}`)}, Retry: &api.ExecutionRetryPolicy{MaxAttempts: 2, SafeToRepeat: true}}}}
	if r := e.Do(t, http.MethodPost, "/v2/execution-workflows", request, nil); r.Code != 503 {
		t.Fatal("default-off", r.Code, r.Body.String())
	}
	server.WithExecutionPipelinesEnabled(true)
	accepted := e.Do(t, http.MethodPost, "/v2/execution-workflows", request, nil)
	if accepted.Code != 202 {
		t.Fatal(accepted.Code, accepted.Body.String())
	}
	if strings.Contains(accepted.Body.String(), "customer-input") || strings.Contains(accepted.Body.String(), definition.Source) {
		t.Fatal("plaintext response")
	}
	var receipt api.ManagedExecutionWorkflowResponse
	_ = json.Unmarshal(accepted.Body.Bytes(), &receipt)
	if receipt.Pipeline == nil || receipt.Pipeline.Steps[0].Task == nil {
		t.Fatal("task pin missing", accepted.Body.String())
	}
	drive := func() {
		t.Helper()
		claim, err := store.ClaimExecutionPipeline(t.Context(), "restarted-apid", time.Now().Add(3*time.Second), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err = server.DriveExecutionPipelineForTest(t.Context(), claim); err != nil {
			t.Fatal(err)
		}
	}
	finish := func(failed bool) {
		t.Helper()
		claim, err := store.ClaimExecution(t.Context(), "schedd", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if !failed {
			if _, err = store.MarkExecutionRunning(t.Context(), claim.ID, *claim.LeaseToken, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		p := state.CompleteExecutionParams{ID: claim.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusSucceeded, Result: json.RawMessage(`{"ok":true}`), Usage: api.ExecutionUsage{WallTimeMS: 1}, FinishedAt: time.Now()}
		if failed {
			code, message := "restore_failed", "host restore unavailable"
			p.Status = api.ExecutionStatusFailed
			p.Result = nil
			p.FailureCode = &code
			p.FailureMessage = &message
		}
		if _, err = store.CompleteExecution(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	drive()
	finish(true)
	drive() // Backoff survives another worker/claim.
	runs, err := store.ListExecutions(t.Context(), e.Acct().ID, 100, 0)
	if err != nil || len(runs) != 1 {
		t.Fatal("early retry", len(runs), err)
	}
	deadline, err := time.Parse(time.RFC3339Nano, receipt.Pipeline.DeadlineAt)
	if err != nil || !runs[0].DeadlineAt.Equal(deadline) || runs[0].Limits.TimeoutMS != 30000 {
		t.Fatal("Run deadline was not clipped to pipeline budget", runs[0].DeadlineAt, deadline, err)
	}
	time.Sleep(1100 * time.Millisecond)
	drive()
	finish(false)
	drive()
	result := e.Do(t, http.MethodGet, "/v1/execution-workflows/retry-pipeline", nil, nil)
	var summary api.ExecutionWorkflowResponse
	_ = json.Unmarshal(result.Body.Bytes(), &summary)
	if result.Code != 200 || summary.RunCount != 2 || len(summary.Managed) != 1 || summary.Managed[0].Status != api.ManagedExecutionWorkflowSucceeded || len(summary.Managed[0].Pipeline.Steps[0].Attempts) != 2 {
		t.Fatal("completion", result.Code, result.Body.String())
	}
	completion := summary.Managed[0].Pipeline.Completion
	if completion == nil || completion.Outcome != "succeeded" || len(completion.Steps) != 1 || len(completion.Steps[0].Attempts) != 2 {
		t.Fatal("whole-pipeline manifest missing", completion)
	}
	var total int64
	for _, attempt := range completion.Steps[0].Attempts {
		r, readErr := store.ExecutionByID(t.Context(), e.Acct().ID, attempt.RunID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		total += r.Usage.WallTimeMS
	}
	if completion.Usage.WallTimeMS != total {
		t.Fatal("retry usage omitted", completion.Usage, total)
	}
	attempts := summary.Managed[0].Pipeline.Steps[0].Attempts
	if attempts[1].PreviousRunID != attempts[0].RunID || attempts[1].RunID == attempts[0].RunID {
		t.Fatal("attempt lineage", attempts)
	}
	server.WithExecutionPipelinesEnabled(false)
	if r := e.Do(t, http.MethodPost, "/v2/execution-workflows", request, nil); r.Code != 202 {
		t.Fatal("rollback replay", r.Code, r.Body.String())
	}
	server.WithExecutionPipelinesEnabled(true)
	request.WorkflowID = "budget-pipeline"
	if r := e.Do(t, http.MethodPost, "/v2/execution-workflows", request, nil); r.Code != 202 {
		t.Fatal(r.Code, r.Body.String())
	}
	drive()
	runs, _ = store.ListExecutions(t.Context(), e.Acct().ID, 100, 0)
	if len(runs) != 2 {
		t.Fatal("retry evaded task budget", len(runs))
	}
	request.WorkflowID = "cancel-pipeline"
	if r := e.Do(t, http.MethodPost, "/v2/execution-workflows", request, nil); r.Code != 202 {
		t.Fatal(r.Code, r.Body.String())
	}
	server.WithExecutionPipelinesEnabled(false)
	if r := e.Do(t, http.MethodPost, "/v1/execution-workflows/cancel-pipeline/cancel", nil, nil); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	drive()
	if _, err = store.ClaimExecution(t.Context(), "schedd", time.Now(), time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cancel admitted a Run", err)
	}
	server.WithExecutionPipelinesEnabled(true)
	pub.Name = "batch-report"
	if r := e.Do(t, http.MethodPost, "/v1/run-tasks", pub, nil); r.Code != 201 {
		t.Fatal("batch publication", r.Code, r.Body.String())
	}
	concurrent := int64(1)
	if _, err = store.PutRunBudget(t.Context(), e.Acct().ID, "task", "batch-report", api.SetRunBudgetRequest{Enabled: true, Limits: api.RunBudgetLimits{MaxConcurrent: &concurrent}}); err != nil {
		t.Fatal(err)
	}
	batch := api.CreateManagedExecutionWorkflowRequest{WorkflowID: "serialized-batch", Version: "v1", MaxParallelSteps: 2, Batch: &api.ExecutionTaskBatch{Name: "batch-report", Version: 1, Items: []api.ExecutionTaskBatchItem{{Label: "one"}, {Label: "two"}}}}
	if r := e.Do(t, http.MethodPost, "/v2/execution-workflows", batch, nil); r.Code != 202 {
		t.Fatal("batch submission", r.Code, r.Body.String())
	}
	drive()
	waiting := e.Do(t, http.MethodGet, "/v1/execution-workflows/serialized-batch", nil, nil)
	var queued api.ExecutionWorkflowResponse
	if err = json.Unmarshal(waiting.Body.Bytes(), &queued); err != nil {
		t.Fatal(err)
	}
	if waiting.Code != 200 || queued.RunCount != 1 || len(queued.Managed) != 1 || queued.Managed[0].Error != "" || len(queued.Managed[0].Pipeline.Steps[1].Attempts) != 0 {
		t.Fatal("concurrency must wait without consuming attempts", waiting.Code, waiting.Body.String())
	}
	finish(false)
	drive()
	finish(false)
	drive()
	complete := e.Do(t, http.MethodGet, "/v1/execution-workflows/serialized-batch", nil, nil)
	var done api.ExecutionWorkflowResponse
	if err = json.Unmarshal(complete.Body.Bytes(), &done); err != nil {
		t.Fatal(err)
	}
	if complete.Code != 200 || done.RunCount != 2 || len(done.Managed) != 1 || done.Managed[0].Status != api.ManagedExecutionWorkflowSucceeded {
		t.Fatal("serialized batch", complete.Code, complete.Body.String())
	}

}
