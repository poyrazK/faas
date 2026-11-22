package state_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMemExecutionPipelineFences(t *testing.T) {
	exerciseExecutionPipelineFences(t, state.NewMemStore())
}
func exerciseExecutionPipelineFences(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@pipeline.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	principal := a.ID
	policy := state.ExecutionPipelinePolicy{TotalTimeoutMS: 60000, Steps: []state.ExecutionPipelineStep{{Label: "work", Retry: api.ExecutionRetryPolicy{MaxAttempts: 3, BackoffMS: 1000, SafeToRepeat: true}}}}
	job, err := s.CreateExecutionPipeline(ctx, state.CreateExecutionWorkflowJobParams{AccountID: a.ID, RunsPrincipalID: &principal, WorkflowID: "pipeline", PlanID: strings.Repeat("a", 24), StepCount: 1, SealedPlan: []byte("private-plan"), PayloadKID: "key", CreatedAt: now.Add(-10 * time.Second)}, policy)
	if err != nil {
		t.Fatal(err)
	}
	policy.Steps[0].Retry.MaxAttempts = 1
	snapshot, err := s.ExecutionPipelineByID(ctx, job.ID)
	if err != nil || snapshot.Policy.Steps[0].Retry.MaxAttempts != 3 {
		t.Fatal("creation retained caller policy aliases", snapshot, err)
	}
	snapshot.Policy.Steps[0].Retry.MaxAttempts = 1
	snapshot, err = s.ExecutionPipelineByID(ctx, job.ID)
	if err != nil || snapshot.Policy.Steps[0].Retry.MaxAttempts != 3 {
		t.Fatal("read leaked mutable policy aliases", snapshot, err)
	}
	if _, err = s.ClaimExecutionWorkflowJob(ctx, "old-worker", now, time.Minute); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("old worker claimed pipeline", err)
	}
	c, err := s.ClaimExecutionPipeline(ctx, "new-worker", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	p := executionTestParams(t, a.ID, now.Add(-9*time.Second), 0, "sealed")
	p.WorkflowID = job.WorkflowID
	p.StepLabel = "gwf:p2:" + job.PlanID + ":p:0:a:1"
	p.RunsPrincipalID = &principal
	if _, err = s.CreateExecution(ctx, p); !errors.Is(err, state.ErrExecutionInvalid) {
		t.Fatal("unfenced admission", err)
	}
	p.Pipeline = &state.ExecutionPipelineAdmission{JobID: job.ID, LeaseToken: "stale", Step: 0, Attempt: 1}
	if _, err = s.CreateExecution(ctx, p); !errors.Is(err, state.ErrExecutionPipelineStopped) {
		t.Fatal("stale admission", err)
	}
	p.Pipeline.LeaseToken = c.ClaimToken
	const callers = 8
	var wg sync.WaitGroup
	results := make(chan state.Execution, callers)
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.CreateExecution(ctx, p)
			if err != nil {
				errs <- err
			} else {
				results <- r
			}
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	var first state.Execution
	success := 0
	for r := range results {
		success++
		first = r
	}
	for err := range errs {
		if !errors.Is(err, state.ErrExecutionWorkflowStepExists) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("duplicate physical attempts", success)
	}
	claim, err := s.ClaimExecution(ctx, "schedd", now.Add(-9*time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	code, message := "restore_failed", "restore unavailable"
	finished := now.Add(-8 * time.Second)
	if _, err = s.CompleteExecution(ctx, state.CompleteExecutionParams{ID: first.ID, LeaseToken: *claim.LeaseToken, Status: api.ExecutionStatusFailed, FailureCode: &code, FailureMessage: &message, FinishedAt: finished, Usage: api.ExecutionUsage{WallTimeMS: 1}}); err != nil {
		t.Fatal(err)
	}
	retry := p
	retry.AdmittedAt = now
	retry.DeadlineAt = now.Add(time.Duration(retry.Request.Limits.TimeoutMS) * time.Millisecond)
	retry.StepLabel = "gwf:p2:" + job.PlanID + ":p:0:a:2"
	retry.Pipeline = &state.ExecutionPipelineAdmission{JobID: job.ID, LeaseToken: c.ClaimToken, Step: 0, Attempt: 2, PreviousRunID: uuid.NewString()}
	if _, err = s.CreateExecution(ctx, retry); err == nil {
		t.Fatal("forged previous attempt accepted")
	}
	retry.Pipeline.PreviousRunID = first.ID
	second, err := s.CreateExecution(ctx, retry)
	if err != nil || second.ID == first.ID {
		t.Fatal("retry admission", second.ID, err)
	}
	if err = s.CancelExecutionPipeline(ctx, a.ID, "pipeline", &principal, now); err != nil {
		t.Fatal(err)
	}
	retry.Pipeline.Attempt = 3
	retry.Pipeline.PreviousRunID = second.ID
	retry.StepLabel = "gwf:p2:" + job.PlanID + ":p:0:a:3"
	if _, err = s.CreateExecution(ctx, retry); !errors.Is(err, state.ErrExecutionPipelineStopped) {
		t.Fatal("admission after cancel", err)
	}
	foreign := uuid.NewString()
	if err = s.CancelExecutionPipeline(ctx, a.ID, "pipeline", &foreign, now); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-family cancellation", err)
	}
	if err = s.UpdateExecutionPipeline(ctx, job.ID, c.ClaimToken, state.ExecutionWorkflowJobUpdate{Status: api.ManagedExecutionWorkflowFailed, NextStep: 1, ScheduledFor: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	final, err := s.ExecutionWorkflowJobByKey(ctx, a.ID, "pipeline", &principal)
	if err != nil || len(final.SealedPlan) != 0 || final.PayloadKID != "" {
		t.Fatal("terminal payload erasure", err)
	}
}
