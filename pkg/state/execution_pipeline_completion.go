package state

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type ExecutionPipelineDelivery struct {
	JobID      string
	AccountID  string
	Callback   *ExecutionTaskCallback
	Completion *api.ExecutionPipelineCompletion
}
type ExecutionPipelineCallbackStore interface {
	PendingExecutionPipelineCallbacks(context.Context, string, int) ([]ExecutionPipelineDelivery, error)
	MarkExecutionPipelineCallbackDelivered(context.Context, string, string) error
	ExecutionPipelineDeliveryByID(context.Context, string, string) (ExecutionPipelineDelivery, error)
	ExecutionPipelineDeliveryForRun(context.Context, string, string) (ExecutionPipelineDelivery, error)
}

func pipelineEndpoint(p ExecutionPipelinePolicy) string {
	if p.Callback != nil {
		return p.Callback.EndpointID
	}
	return ""
}
func pipelineDelivery(j ExecutionWorkflowJob, p ExecutionPipeline) ExecutionPipelineDelivery {
	p = clonePipeline(p)
	return ExecutionPipelineDelivery{JobID: j.ID, AccountID: j.AccountID, Callback: p.Policy.Callback, Completion: p.Completion}
}
func validPipelineCompletion(j ExecutionWorkflowJob, p ExecutionPipeline, u ExecutionWorkflowJobUpdate) error {
	c := u.PipelineCompletion
	terminal := u.Status == api.ManagedExecutionWorkflowSucceeded || u.Status == api.ManagedExecutionWorkflowFailed
	if c == nil {
		if terminal && (p.Policy.Callback != nil || p.Policy.CompletionVersion != "") {
			return ErrExecutionInvalid
		}
		return nil
	}
	if !terminal || c.Version != "v1" || c.CompletionID != j.ID || c.WorkflowID != j.WorkflowID || len(c.Steps) != j.StepCount || c.FinishedAt != u.UpdatedAt.UTC().Format(time.RFC3339Nano) {
		return ErrExecutionInvalid
	}
	if p.CancelRequested && c.Outcome != "cancelled" {
		return ErrExecutionPipelineStopped
	}
	if c.Outcome != "succeeded" && c.Outcome != "failed" && c.Outcome != "cancelled" && c.Outcome != "deadline_exceeded" {
		return ErrExecutionInvalid
	}
	if (u.Status == api.ManagedExecutionWorkflowSucceeded) != (c.Outcome == "succeeded") {
		return ErrExecutionInvalid
	}
	raw, err := json.Marshal(c)
	if err != nil || len(raw) > api.ExecutionPipelineCompletionMaxBytes {
		return ErrExecutionInvalid
	}
	return nil
}
