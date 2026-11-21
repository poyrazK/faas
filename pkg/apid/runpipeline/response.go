package runpipeline

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

func Response(ctx context.Context, j state.ExecutionWorkflowJob, jobs state.ExecutionPipelineStore, runs state.ExecutionWorkflowStore) (*api.ExecutionPipelineResponse, error) {
	p, err := jobs.ExecutionPipelineByID(ctx, j.ID)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := &api.ExecutionPipelineResponse{Progress: p.Journal.Progress, Rerun: p.Policy.Rerun, DeadlineAt: p.DeadlineAt.UTC().Format(time.RFC3339Nano), CancelRequested: p.CancelRequested, Completion: p.Completion, Steps: []api.ExecutionPipelineStepResponse{}}
	for i, s := range p.Policy.Steps {
		step := api.ExecutionPipelineStepResponse{Label: s.Label, Task: s.Task, MaxAttempts: s.Retry.MaxAttempts, Attempts: []api.ExecutionPipelineAttemptResponse{}}
		previous := ""
		for n := 1; n <= s.Retry.MaxAttempts; n++ {
			r, err := runs.ExecutionWorkflowStepByLabel(ctx, j.AccountID, j.WorkflowID, j.RunsPrincipalID, Label(j.PlanID, i, n))
			if errors.Is(err, state.ErrNotFound) {
				break
			}
			if err != nil {
				return nil, err
			}
			a := api.ExecutionPipelineAttemptResponse{RunID: r.ID, Attempt: n, PreviousRunID: previous, Status: r.Status}
			if r.FailureCode != nil {
				a.FailureCode = *r.FailureCode
			}
			step.Attempts = append(step.Attempts, a)
			previous = r.ID
		}
		out.Steps = append(out.Steps, step)
	}
	return out, nil
}
