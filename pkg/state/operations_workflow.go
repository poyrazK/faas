package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

// WorkflowOperationStore is an explicit adapter seam. HTTP runtime claims are
// never inferred from native workflow headers or managed step identities.
type WorkflowOperationStore interface {
	OperationForWorkflowRun(context.Context, string) (Operation, bool, error)
}

func operationWorkflowResumeError(err error, plan api.Plan, active int) error {
	switch {
	case errors.Is(err, ErrWorkflowRunQuotaExceeded):
		return NewOperationLimitError("workflow_active_runs", int64(plan.WorkflowMaxConcurrentRuns()), int64(active+1))
	case errors.Is(err, ErrWorkflowResumeLimit):
		return NewOperationLimitError("workflow_resumes", int64(api.WorkflowRunMaxResumes), int64(api.WorkflowRunMaxResumes+1))
	default:
		return err
	}
}

func operationWorkflowDefinition(def OperationDefinition, raw json.RawMessage, plan api.Plan) (api.WorkflowSpec, error) {
	if def.Scope != DefaultEnvScope && def.Scope != "production" {
		return api.WorkflowSpec{}, fmt.Errorf("%w: workflow operations require the default production scope", ErrInvalidArgument)
	}
	var workflows []api.WorkflowSpec
	if err := json.Unmarshal(raw, &workflows); err != nil {
		return api.WorkflowSpec{}, fmt.Errorf("%w: invalid deployment workflows", ErrInvalidArgument)
	}
	var selected *api.WorkflowSpec
	for index := range workflows {
		if workflows[index].Name == def.Spec.Workflow {
			if selected != nil {
				return api.WorkflowSpec{}, ErrInvalidArgument
			}
			selected = &workflows[index]
		}
	}
	if selected == nil {
		return api.WorkflowSpec{}, fmt.Errorf("%w: operation workflow is missing from its deployment", ErrInvalidArgument)
	}
	if err := operations.ValidateWorkflow(def.Spec, *selected, plan); err != nil {
		return api.WorkflowSpec{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	return *selected, nil
}

func prepareOperationWorkflow(op *Operation, inv Invocation, spec api.WorkflowSpec) (WorkflowRun, []*WorkflowStep, error) {
	// Native workflows default to three HTTP attempts. This adapter never
	// infers that an uncertain action is safe to repeat inside a generation.
	spec.Steps = append([]api.WorkflowStepSpec(nil), spec.Steps...)
	for index := range spec.Steps {
		spec.Steps[index].Retry = &api.WorkflowRetrySpec{MaxAttempts: 1, Backoff: "fixed"}
	}
	snapshot, err := json.Marshal(spec)
	if err != nil {
		return WorkflowRun{}, nil, err
	}
	run := WorkflowRun{ID: newOperationID(), AppID: op.AppID, PlatformTenantID: op.PlatformTenantID,
		WorkflowName: spec.Name, Status: WorkflowRunStatusPending, Input: cloneWorkflowJSON(inv.Payload),
		DefinitionSnapshot: snapshot, ScheduledFor: op.CreatedAt, CreatedAt: op.CreatedAt, UpdatedAt: op.CreatedAt}
	op.CurrentInvocationID, op.WorkflowRunID = "", run.ID
	steps := make([]*WorkflowStep, 0, len(spec.Steps))
	for _, step := range spec.Steps {
		steps = append(steps, &WorkflowStep{RunID: run.ID, StepName: step.Name, Status: WorkflowStepStatusPending, CreatedAt: op.CreatedAt})
	}
	return run, steps, nil
}

func operationWorkflowEvent(op *Operation, run WorkflowRun, kind string, data map[string]any, now time.Time) api.OperationEvent {
	data["workflow_run_id"] = run.ID
	data["generation"] = op.Generation
	return operationEvent(op, Invocation{Attempts: run.ResumeCount + 1}, kind, data, now)
}

// Project only confirmed ledger facts. Any failed dispatched HTTP action can
// have produced an external effect, irrespective of its response status.
func operationWorkflowProjection(op *Operation, def OperationDefinition, run WorkflowRun, steps map[string]WorkflowStep, now time.Time) ([]api.OperationEvent, error) {
	if op.WorkflowRunID != run.ID || op.Generation != run.ResumeCount+1 {
		return nil, ErrOperationStaleAttempt
	}
	if op.State.Terminal() || op.State == api.OperationRequiresReconciliation {
		return nil, nil
	}
	events := []api.OperationEvent{}
	completed, unknown := int64(0), false
	stage := def.Spec.ProgressStages[0]
	stageChosen := false
	for _, name := range def.Spec.ProgressStages {
		step := steps[name]
		if step.Status == WorkflowStepStatusSucceeded {
			completed++
		}
		if !stageChosen {
			stage = name
			stageChosen = step.Status != WorkflowStepStatusSucceeded
		}
		if (step.Status == WorkflowStepStatusFailed || step.Status == WorkflowStepStatusDead || step.Status == WorkflowStepStatusSkipped) && step.Attempt > 0 && len(step.Output) == 0 {
			unknown = true
		}
	}
	if run.Status == WorkflowRunStatusRunning && op.State == api.OperationAccepted {
		op.State = api.OperationRunning
		events = append(events, operationWorkflowEvent(op, run, "running", map[string]any{"state": op.State}, now))
	}
	if op.State == api.OperationRunning && (op.Progress == nil || op.Progress.Stage != stage || op.Progress.Completed != completed || op.Progress.Attempt != run.ResumeCount+1) {
		op.Progress = &api.OperationProgress{Stage: stage, Completed: completed, Total: int64(len(def.Spec.ProgressStages)), Attempt: run.ResumeCount + 1, UpdatedAt: now}
		events = append(events, operationWorkflowEvent(op, run, "progress", map[string]any{"stage": stage, "completed": completed, "total": op.Progress.Total, "attempt": op.Progress.Attempt, "updated_at": now}, now))
	}
	kind := ""
	switch run.Status {
	case WorkflowRunStatusPending, WorkflowRunStatusRunning:
		if unknown {
			op.State, op.FailureCode, kind = api.OperationRequiresReconciliation, "execution_outcome_unknown", "reconciliation_required"
		}
	case WorkflowRunStatusSucceeded:
		contract, err := operations.Compile(def.Spec, op.PlanLimits)
		if err != nil {
			return nil, err
		}
		if err := contract.ValidateOutput(run.Output, op.ValueMaxBytes); err != nil {
			op.State, op.FailureCode, kind = api.OperationRequiresReconciliation, "invalid_output", "reconciliation_required"
		} else {
			op.State, op.Result, kind = api.OperationSucceeded, cloneWorkflowJSON(run.Output), "succeeded"
		}
	case WorkflowRunStatusFailed, WorkflowRunStatusDead:
		if unknown {
			op.State, op.FailureCode, kind = api.OperationRequiresReconciliation, "execution_outcome_unknown", "reconciliation_required"
		} else if run.CancelledAt != nil {
			op.State, kind = api.OperationCancelled, "cancelled"
		} else {
			op.State, op.FailureCode, kind = api.OperationFailed, "execution_failed", "failed"
		}
	}
	if kind != "" {
		op.ExpiresAt = now.Add(time.Duration(op.PlanLimits.ResultRetentionSeconds) * time.Second)
		op.EventExpiresAt = now.Add(time.Duration(op.PlanLimits.EventRetentionSeconds) * time.Second)
		events = append(events, operationWorkflowEvent(op, run, kind, map[string]any{"state": op.State, "failure_code": op.FailureCode}, now))
	}
	return events, nil
}
