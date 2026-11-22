package runpipeline

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/url"
	"time"
)

// Completion snapshots terminal receipt metadata before the encrypted plan is
// erased. It includes every physical attempt, including failed retries.
func (w Worker) completion(ctx context.Context, c state.ExecutionWorkflowJobClaim, u state.ExecutionWorkflowJobUpdate, obs []observed) (*api.ExecutionPipelineCompletion, error) {
	p, err := w.Jobs.ExecutionPipelineByID(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	out := &api.ExecutionPipelineCompletion{Version: "v1", CompletionID: c.ID, WorkflowID: c.WorkflowID, Outcome: string(u.Status), FinishedAt: u.UpdatedAt.UTC().Format(time.RFC3339Nano), Receipt: "/v1/execution-workflows/" + url.PathEscape(c.WorkflowID), Steps: []api.ExecutionPipelineCompletedStep{}, Rerun: p.Policy.Rerun}
	if p.Policy.Callback != nil {
		out.Receipt = "/v1/agent-tools/pipelines/" + c.ID
	}
	if p.CancelRequested {
		out.Outcome = "cancelled"
	} else if u.LastError == "pipeline deadline exceeded" {
		out.Outcome = "deadline_exceeded"
	}
	for i, s := range p.Policy.Steps {
		step := api.ExecutionPipelineCompletedStep{Label: s.Label, Task: s.Task, Outcome: "not_started", Attempts: []api.ExecutionPipelineCompletedAttempt{}}
		if ref := reusedStep(p.Policy.Rerun, s.Label); ref != nil {
			step.Outcome = "succeeded"
			if i < len(obs) && obs[i].blocked {
				step.Outcome = "failed"
			}
			step.ReusedRunID = ref.RunID
			step.OutputSHA256 = ref.OutputSHA256
			out.Steps = append(out.Steps, step)
			continue
		}
		if i < len(obs) && obs[i].blocked {
			step.Outcome = "blocked"
		}
		for n := 1; n <= s.Retry.MaxAttempts; n++ {
			r, e := w.WorkflowRuns.ExecutionWorkflowStepByLabel(ctx, c.AccountID, c.WorkflowID, c.RunsPrincipalID, Label(c.PlanID, i, n))
			if errors.Is(e, state.ErrNotFound) {
				break
			}
			if e != nil {
				return nil, e
			}
			// A terminal pipeline never drops an unsettled admitted attempt.
			if !r.Status.Terminal() {
				return nil, state.ErrExecutionPipelineStopped
			}
			receipt := "/v1/executions/" + r.ID
			if p.Policy.Callback != nil {
				receipt = "/v1/agent-tools/runs/" + r.ID
			}
			a := api.ExecutionPipelineCompletedAttempt{RunID: r.ID, Attempt: n, Status: r.Status, Receipt: receipt, Usage: r.Usage, Artifacts: []api.ExecutionPipelineArtifactRef{}}
			if r.FailureCode != nil {
				a.FailureCode = *r.FailureCode
			}
			for _, artifact := range r.Artifacts {
				a.Artifacts = append(a.Artifacts, api.ExecutionPipelineArtifactRef{Name: artifact.Name, SizeBytes: artifact.SizeBytes, SHA256: artifact.SHA256, URL: receipt + "/artifacts/" + url.PathEscape(artifact.Name)})
			}
			step.Attempts = append(step.Attempts, a)
			step.Outcome = string(r.Status)
			if r.Status == api.ExecutionStatusSucceeded {
				step.OutputSHA256 = outputDigest(r)
			}
			if r.ResultValidation != nil {
				step.ResultValidation = r.ResultValidation.Status
				if r.ResultValidation.Status == "invalid" {
					step.Outcome = "contract_failed"
				}
			}
			out.Usage.WallTimeMS += r.Usage.WallTimeMS
			out.Usage.CPUTimeMS += r.Usage.CPUTimeMS
			if r.Usage.PeakMemoryMB > out.Usage.PeakMemoryMB {
				out.Usage.PeakMemoryMB = r.Usage.PeakMemoryMB
			}
		}
		if i < len(obs) && obs[i].validated && !obs[i].invalid {
			step.ResultValidation = "valid"
		}
		if i < len(obs) && obs[i].invalid {
			step.Outcome = "contract_failed"
			step.ResultValidation = "invalid"
		}
		out.Steps = append(out.Steps, step)
	}
	return out, nil
}
func (w Worker) updateObserved(ctx context.Context, c state.ExecutionWorkflowJobClaim, status api.ManagedExecutionWorkflowStatus, n int, detail string, obs []observed) error {
	now := w.now()
	var err error
	u := state.ExecutionWorkflowJobUpdate{Status: status, NextStep: n, LastError: detail, UpdatedAt: now, ScheduledFor: now.Add(2 * time.Second)}
	if status == api.ManagedExecutionWorkflowSucceeded || status == api.ManagedExecutionWorkflowFailed {
		var err error
		u.PipelineCompletion, err = w.completion(ctx, c, u, obs)
		if err != nil {
			return err
		}
	}
	u.PipelineProgress, err = w.progress(ctx, c, u, obs)
	if err != nil {
		return err
	}
	return w.Jobs.UpdateExecutionPipeline(ctx, c.ID, c.ClaimToken, u)
}
