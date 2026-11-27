package runpipeline

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

func (w Worker) progress(ctx context.Context, c state.ExecutionWorkflowJobClaim, u state.ExecutionWorkflowJobUpdate, obs []observed) (*api.ExecutionPipelineProgress, error) {
	p, err := w.Jobs.ExecutionPipelineByID(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	if p.Policy.ProgressVersion == "" {
		return nil, nil
	}
	out := &api.ExecutionPipelineProgress{Status: string(u.Status), Steps: []api.ExecutionPipelineStepProgress{}}
	if u.PipelineCompletion != nil {
		out.Status = u.PipelineCompletion.Outcome
	}
	for i, s := range p.Policy.Steps {
		o := observed{}
		if i < len(obs) {
			o = obs[i]
		} else {
			for a := 1; a <= s.Retry.MaxAttempts; a++ {
				r, e := w.WorkflowRuns.ExecutionWorkflowStepByLabel(ctx, c.AccountID, c.WorkflowID, c.RunsPrincipalID, Label(c.PlanID, i, a))
				if errors.Is(e, state.ErrNotFound) {
					break
				}
				if e != nil {
					return nil, e
				}
				o.run = r
				o.attempt = a
			}
		}
		if o.attempt > 0 && o.run.ID == "" {
			r, e := w.WorkflowRuns.ExecutionWorkflowStepByLabel(ctx, c.AccountID, c.WorkflowID, c.RunsPrincipalID, Label(c.PlanID, i, o.attempt))
			if e != nil && !errors.Is(e, state.ErrNotFound) {
				return nil, e
			}
			if e == nil {
				o.run = r
			}
		}
		step := api.ExecutionPipelineStepProgress{Label: s.Label, State: "queued", Reason: o.reason, Attempt: o.attempt, RunID: o.run.ID}
		if step.RunID != "" {
			step.EventsURL = "/v1/executions/" + step.RunID + "/events"
		}
		if o.attempt > 0 {
			step.State = string(o.run.Status)
		}
		if o.reused || reusedStep(p.Policy.Rerun, s.Label) != nil {
			step.State = "reused"
			step.Reason = ""
			if ref := reusedStep(p.Policy.Rerun, s.Label); ref != nil {
				step.RunID = ref.RunID
				step.EventsURL = "/v1/executions/" + ref.RunID + "/events"
			}
		}
		if o.retry {
			step.State = "retrying"
			step.RetryAt = RetryAt(o.run, s.Retry, o.attempt).UTC().Format(time.RFC3339Nano)
		}
		if o.blocked {
			step.State = "blocked"
			step.Reason = "dependencies"
			if reusedStep(p.Policy.Rerun, s.Label) != nil || o.reason == "admission_refused" {
				step.Reason = "admission_refused"
			}
		}
		if o.invalid {
			step.State = "contract_failed"
		}
		if u.PipelineCompletion != nil {
			step.RetryAt = ""
			for _, done := range u.PipelineCompletion.Steps {
				if done.Label != s.Label {
					continue
				}
				switch done.Outcome {
				case "not_started":
					step.State = "blocked"
					step.Reason = "fail_fast"
					if p.CancelRequested || u.PipelineCompletion.Outcome == "deadline_exceeded" {
						step.Reason = "cancelling"
					}
				case "blocked":
					step.State = "blocked"
					if step.Reason == "" {
						step.Reason = "dependencies"
					}
				case "contract_failed":
					step.State = "contract_failed"
					step.Reason = ""
				default:
					if reusedStep(p.Policy.Rerun, s.Label) == nil {
						step.State = done.Outcome
						step.Reason = ""
					}
				}
			}
		}
		if o.attempt == 0 && step.Reason == "" {
			step.Reason = "pending"
			if u.LastError == "waiting for encrypted pipeline identity" {
				step.Reason = "plan_identity"
			}
		}
		if !o.run.Status.Terminal() && o.attempt > 0 && u.PipelineCompletion == nil && u.Status == api.ManagedExecutionWorkflowQueued {
			out.Status = "running"
		}
		out.Steps = append(out.Steps, step)
	}
	return out, nil
}
