package state

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"reflect"
	"slices"
	"time"
)

type ExecutionPipelineProgressStore interface {
	ExecutionPipelineJob(context.Context, string, string, *string) (ExecutionWorkflowJob, error)
	ListExecutionPipelineEvents(context.Context, string, string, *string, int64, int) (api.ExecutionPipelineEventsResponse, error)
}
type ExecutionPipelineJournal struct {
	Progress *api.ExecutionPipelineProgress `json:"progress,omitempty"`
	Events   []api.ExecutionPipelineEvent   `json:"events,omitempty"`
	Sequence int64                          `json:"sequence"`
}

func advancePipelineProgress(p *ExecutionPipeline, progress *api.ExecutionPipelineProgress, at time.Time) error {
	if progress == nil {
		if p.Policy.ProgressVersion != "" {
			return ErrExecutionInvalid
		}
		return nil
	}
	if len(progress.Steps) != len(p.Policy.Steps) || !slices.Contains([]string{"queued", "running", "succeeded", "failed", "cancelled", "deadline_exceeded"}, progress.Status) {
		return ErrExecutionInvalid
	}
	previous := p.Journal.Progress
	appendEvent := func(e api.ExecutionPipelineEvent) {
		p.Journal.Sequence++
		e.Sequence = p.Journal.Sequence
		e.CreatedAt = at.UTC().Format(time.RFC3339Nano)
		p.Journal.Events = append(p.Journal.Events, e)
	}
	if previous == nil || previous.Status != progress.Status {
		appendEvent(api.ExecutionPipelineEvent{Type: "pipeline", Status: progress.Status})
	}
	for i, step := range progress.Steps {
		if step.Label != p.Policy.Steps[i].Label || !slices.Contains([]string{"queued", "restoring", "running", "succeeded", "failed", "timed_out", "out_of_memory", "cancelled", "retrying", "blocked", "contract_failed", "reused"}, step.State) || !slices.Contains([]string{"", "pending", "dependencies", "parallel_limit", "capacity", "budget_concurrency", "retry_backoff", "admissions_disabled", "admission_refused", "fail_fast", "plan_identity", "cancelling"}, step.Reason) || step.Attempt < 0 || step.Attempt > api.ExecutionPipelineMaxAttempts {
			return ErrExecutionInvalid
		}
		if previous == nil || len(previous.Steps) <= i || !reflect.DeepEqual(previous.Steps[i], step) {
			copy := step
			appendEvent(api.ExecutionPipelineEvent{Type: "step", Step: &copy})
		}
	}
	p.Journal.Progress = progress
	if len(p.Journal.Events) > api.ExecutionPipelineProgressRetained {
		p.Journal.Events = p.Journal.Events[len(p.Journal.Events)-api.ExecutionPipelineProgressRetained:]
	}
	raw, err := json.Marshal(p.Journal)
	if err != nil || len(raw) > api.ExecutionPipelineProgressMaxBytes {
		return ErrExecutionInvalid
	}
	return nil
}
func pipelineEvents(j ExecutionWorkflowJob, p ExecutionPipeline, after int64, limit int) (api.ExecutionPipelineEventsResponse, error) {
	if after < 0 || after > p.Journal.Sequence || limit < 1 || limit > api.ExecutionPipelineProgressRetained {
		return api.ExecutionPipelineEventsResponse{}, ErrExecutionInvalid
	}
	out := api.ExecutionPipelineEventsResponse{PipelineID: j.ID, Events: []api.ExecutionPipelineEvent{}, NextSequence: after, Progress: p.Journal.Progress}
	if len(p.Journal.Events) > 0 {
		out.Gap = after < p.Journal.Events[0].Sequence-1
	}
	for _, e := range p.Journal.Events {
		if e.Sequence > after && len(out.Events) < limit {
			out.Events = append(out.Events, e)
			out.NextSequence = e.Sequence
		}
	}
	terminal := j.Status == api.ManagedExecutionWorkflowSucceeded || j.Status == api.ManagedExecutionWorkflowFailed
	out.Terminal = terminal && out.NextSequence >= p.Journal.Sequence
	return out, nil
}
func initialPipelineJournal(policy ExecutionPipelinePolicy, at time.Time) (ExecutionPipelineJournal, error) {
	p := ExecutionPipeline{Policy: policy}
	if policy.ProgressVersion == "" {
		return p.Journal, nil
	}
	progress := api.ExecutionPipelineProgress{Status: "queued", Steps: []api.ExecutionPipelineStepProgress{}}
	for _, s := range policy.Steps {
		progress.Steps = append(progress.Steps, api.ExecutionPipelineStepProgress{Label: s.Label, State: "queued", Reason: "pending"})
	}
	err := advancePipelineProgress(&p, &progress, at)
	return p.Journal, err
}
