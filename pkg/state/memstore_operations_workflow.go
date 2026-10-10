package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

func (m *MemStore) operationForWorkflowLocked(runID string) (Operation, bool) {
	if m.operationData != nil {
		for _, operation := range m.operationData.operations {
			if operation.WorkflowRunID == runID && runID != "" {
				return cloneOperation(operation), true
			}
		}
	}
	return Operation{}, false
}

func (m *MemStore) OperationForWorkflowRun(_ context.Context, runID string) (Operation, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, exists := m.operationForWorkflowLocked(runID)
	return op, exists, nil
}

func (m *MemStore) prepareOperationWorkflowLocked(op *Operation, inv Invocation, def OperationDefinition, plan api.Plan) (WorkflowRun, []*WorkflowStep, error) {
	app := m.apps[op.AppID]
	if !m.accounts[op.AccountID].Active() || app.MaintenanceMode {
		return WorkflowRun{}, nil, ErrWorkflowResumeUnavailable
	}
	spec, err := operationWorkflowDefinition(def, m.deployments[op.DeploymentID].Workflows, plan)
	if err != nil {
		return WorkflowRun{}, nil, err
	}
	active := 0
	for _, run := range m.workflowRuns {
		if run.AppID == op.AppID && workflowGuardRunActive(run.Status) {
			active++
		}
	}
	if active >= plan.WorkflowMaxConcurrentRuns() {
		return WorkflowRun{}, nil, NewOperationLimitError("workflow_active_runs", int64(plan.WorkflowMaxConcurrentRuns()), int64(active+1))
	}
	return prepareOperationWorkflow(op, inv, spec)
}

func (m *MemStore) insertOperationWorkflowLocked(op Operation, run WorkflowRun, steps []*WorkflowStep) {
	m.workflowRuns[run.ID] = run
	m.workflowSteps[run.ID] = map[string]WorkflowStep{}
	for _, step := range steps {
		m.workflowSteps[run.ID][step.StepName] = *step
	}
	m.saveOperationWorkflowExecutionLocked(op, run)
}

func (m *MemStore) saveOperationWorkflowExecutionLocked(op Operation, run WorkflowRun) {
	data := m.operationMemoryLocked()
	if data.workflowExecutions == nil {
		data.workflowExecutions = map[string]map[int]api.OperationExecution{}
	}
	if data.workflowExecutions[op.ID] == nil {
		data.workflowExecutions[op.ID] = map[int]api.OperationExecution{}
	}
	created := op.CreatedAt
	if run.ResumeCount > 0 {
		created = run.ScheduledFor
	}
	if prior, exists := data.workflowExecutions[op.ID][op.Generation]; exists {
		created = prior.CreatedAt
	}
	data.workflowExecutions[op.ID][op.Generation] = operationWorkflowExecution(op, run, m.workflowSteps[run.ID], created)
}

func operationWorkflowExecution(op Operation, run WorkflowRun, steps map[string]WorkflowStep, created time.Time) api.OperationExecution {
	attempts := 0
	for _, step := range steps {
		attempts += step.Attempt
	}
	return api.OperationExecution{Generation: op.Generation, WorkflowRunID: run.ID, State: run.Status, Attempts: attempts, CreatedAt: created, CompletedAt: cloneWorkflowTime(run.FinishedAt)}
}

func (m *MemStore) syncOperationWorkflowLocked(runID string) error {
	op, exists := m.operationForWorkflowLocked(runID)
	if !exists {
		return nil
	}
	run := m.workflowRuns[runID]
	def := m.operationData.definitions[op.DefinitionID]
	events, err := operationWorkflowProjection(&op, def, run, m.workflowSteps[runID], time.Now().UTC())
	if err != nil {
		return err
	}
	events = append(events, publishWorkflowArtifacts(&op, run, m.workflowSteps[runID], false, time.Now().UTC())...)
	m.saveOperationWorkflowExecutionLocked(op, run)
	if len(events) == 0 {
		return nil
	}
	if op.State.Terminal() {
		if err := m.operationCompletionLocked(&op, def); err != nil {
			return err
		}
	}
	for _, event := range events {
		m.operationSaveLocked(op, event)
	}
	return nil
}

// Closing an in-flight attempt is evidence of uncertainty, not evidence that
// its external effects did not happen. Preserve its attempt number and input.
func (m *MemStore) interruptOperationWorkflowLocked(runID string, now time.Time) {
	message := "workflow action result unknown after interruption"
	for name, step := range m.workflowSteps[runID] {
		if step.Status == WorkflowStepStatusRunning {
			step.Status, step.Error, step.FinishedAt = WorkflowStepStatusDead, &message, &now
			step.NextRetryAt, step.outboundAttemptToken = nil, ""
			m.workflowSteps[runID][name] = step
		}
	}
	for key, record := range m.workflowStepAttempts {
		if key.runID == runID && record.Status == WorkflowAttemptStatusRunning {
			record.Status, record.Error, record.FinishedAt = WorkflowAttemptStatusFailed, &message, &now
			m.workflowStepAttempts[key] = record
		}
	}
}

func (m *MemStore) cancelOperationWorkflowLocked(op Operation, generation int) (Operation, error) {
	now := time.Now().UTC()
	if err := validateOperationCancellation(op, op.AccountID, op.PlatformTenantID, generation, now); err != nil {
		return Operation{}, err
	}
	if op.State.Terminal() || op.CancellationRequested {
		return cloneOperation(m.operationDeliveryLocked(op)), nil
	}
	run := m.workflowRuns[op.WorkflowRunID]
	if run.ResumeCount+1 != op.Generation {
		return Operation{}, ErrOperationStaleAttempt
	}
	op.CancellationRequested = true
	event := operationWorkflowEvent(&op, run, "cancellation_requested", map[string]any{"cancellation_requested": true}, now)
	m.operationSaveLocked(op, event)
	m.interruptOperationWorkflowLocked(run.ID, now)
	for name, step := range m.workflowSteps[run.ID] {
		if step.Status == WorkflowStepStatusPending {
			reason := WorkflowSkipDependencyFailed
			step.Status, step.SkipReason, step.FinishedAt = WorkflowStepStatusSkipped, &reason, &now
			m.workflowSteps[run.ID][name] = step
		}
	}
	if workflowGuardRunActive(run.Status) {
		message := "cancelled by operation owner"
		run.Status, run.LastError, run.CancelledAt, run.FinishedAt, run.UpdatedAt = WorkflowRunStatusFailed, &message, &now, &now, now
		m.workflowRuns[run.ID] = run
		delete(m.workflowRunLeases, run.ID)
	}
	if err := m.syncOperationWorkflowLocked(run.ID); err != nil {
		return Operation{}, err
	}
	return cloneOperation(m.operationDeliveryLocked(m.operationData.operations[op.ID])), nil
}

func operationWorkflowRecovery(op *Operation, def OperationDefinition, run WorkflowRun, req api.OperationRecoveryRequest, now time.Time) (api.OperationEvent, error) {
	if op.State != api.OperationRequiresReconciliation || op.Generation != req.ExpectedGeneration || run.ResumeCount+1 != op.Generation {
		return api.OperationEvent{}, ErrConflict
	}
	if op.RecoveryCount >= op.PlanLimits.RecoveriesPerOperation {
		return api.OperationEvent{}, NewOperationLimitError("recoveries_per_operation", int64(op.PlanLimits.RecoveriesPerOperation), int64(op.RecoveryCount+1))
	}
	if req.Resolution == "safe_to_retry" {
		op.Generation++
		op.State, op.Progress, op.Result, op.CancellationRequested = api.OperationAccepted, nil, nil, false
		op.CompletionDelivery = api.OperationDeliveryResponse{State: "not_requested"}
		if def.Spec.CompletionWebhookID != "" {
			op.CompletionDelivery.State = "awaiting_outcome"
		}
	} else {
		switch req.Resolution {
		case "succeeded":
			contract, err := operations.Compile(def.Spec, op.PlanLimits)
			if err != nil {
				return api.OperationEvent{}, err
			}
			if err := contract.ValidateOutput(req.Result, op.ValueMaxBytes); err != nil {
				return api.OperationEvent{}, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
			}
			op.State, op.Result = api.OperationSucceeded, cloneWorkflowJSON(req.Result)
		case "failed":
			op.State = api.OperationFailed
		case "cancelled":
			op.State = api.OperationCancelled
		default:
			return api.OperationEvent{}, ErrInvalidArgument
		}
	}
	op.RecoveryCount++
	op.FailureCode = ""
	if req.Resolution == "failed" {
		op.FailureCode = "reconciled_failure"
	}
	op.ExpiresAt = now.Add(time.Duration(op.PlanLimits.ResultRetentionSeconds) * time.Second)
	op.EventExpiresAt = now.Add(time.Duration(op.PlanLimits.EventRetentionSeconds) * time.Second)
	kind := req.Resolution
	if kind == "safe_to_retry" {
		kind = "recovery_requested"
	}
	if req.Resolution == "succeeded" {
		_ = publishWorkflowArtifacts(op, run, nil, true, now)
	}
	refreshOperationArtifactExpiry(op)
	evidenceDigest, _ := operations.InputFingerprint(mustOperationJSON(req.Evidence))
	return operationWorkflowEvent(op, run, kind, map[string]any{"state": op.State, "resolution": req.Resolution, "evidence_digest": evidenceDigest}, now), nil
}
