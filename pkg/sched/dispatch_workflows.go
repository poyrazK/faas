package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// WorkflowStepExecutor dispatches a single step execution to an app instance.
type WorkflowStepExecutor interface {
	ExecuteStep(ctx context.Context, appID string, path, method string, headers map[string]string, body []byte, timeout time.Duration) (int, []byte, error)
}

// WorkflowOrchestrator coordinates workflow state transitions and step dispatch (ADR-081).
type WorkflowOrchestrator struct {
	store    state.Store
	executor WorkflowStepExecutor
	auditor  *audit.Auditor
	metrics  *wire.WorkflowMetrics
	log      *slog.Logger
}

// NewWorkflowOrchestrator constructs a new orchestrator.
func NewWorkflowOrchestrator(
	store state.Store,
	executor WorkflowStepExecutor,
	auditor *audit.Auditor,
	metrics *wire.WorkflowMetrics,
	log *slog.Logger,
) *WorkflowOrchestrator {
	return &WorkflowOrchestrator{
		store:    store,
		executor: executor,
		auditor:  auditor,
		metrics:  metrics,
		log:      log,
	}
}

func (o *WorkflowOrchestrator) emitAudit(ctx context.Context, kind string, payload map[string]any) {
	if o.auditor != nil {
		o.auditor.Emit(ctx, kind, nil, payload)
	}
}

// DispatchTick runs one iteration of claiming pending runs and advancing active ones.
func (o *WorkflowOrchestrator) DispatchTick(ctx context.Context) error {
	if o.store == nil {
		return nil
	}

	// 1. Claim any pending workflow runs ready for execution
	claimed, err := o.store.ClaimNextPendingRun(ctx)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		if o.log != nil {
			o.log.Warn("workflow orchestrator: claim pending failed", "err", err)
		}
		return err
	}

	if claimed != nil {
		if err := o.initAndAdvanceRun(ctx, claimed); err != nil {
			if o.log != nil {
				o.log.Warn("workflow orchestrator: init and advance run failed", "run_id", claimed.ID, "err", err)
			}
		}
	}

	return nil
}

// initAndAdvanceRun parses the definition snapshot, seeds step records, and advances.
func (o *WorkflowOrchestrator) initAndAdvanceRun(ctx context.Context, run *state.WorkflowRun) error {
	var spec api.WorkflowSpec
	if err := json.Unmarshal(run.DefinitionSnapshot, &spec); err != nil {
		lastErr := fmt.Sprintf("invalid definition snapshot: %v", err)
		_ = o.store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusFailed, nil, &lastErr)
		return err
	}

	// Check if step records already exist; if not, create them
	existingSteps, err := o.store.GetWorkflowSteps(ctx, run.ID)
	if err != nil {
		return err
	}

	if len(existingSteps) == 0 {
		var stepsToCreate []*state.WorkflowStep
		for _, s := range spec.Steps {
			stepsToCreate = append(stepsToCreate, &state.WorkflowStep{
				RunID:    run.ID,
				StepName: s.Name,
				Status:   state.WorkflowStepStatusPending,
				Attempt:  0,
				Input:    run.Input,
			})
		}
		if err := o.store.CreateWorkflowSteps(ctx, run.ID, stepsToCreate); err != nil {
			return err
		}
	}

	o.emitAudit(ctx, events.WorkflowStarted, map[string]any{
		"run_id":        run.ID,
		"app_id":        run.AppID,
		"workflow_name": run.WorkflowName,
		"input":         string(run.Input),
	})

	return o.AdvanceWorkflowRun(ctx, run.ID)
}

// AdvanceWorkflowRun evaluates the DAG and dispatches runnable steps.
func (o *WorkflowOrchestrator) AdvanceWorkflowRun(ctx context.Context, runID string) error {
	run, err := o.store.GetWorkflowRun(ctx, runID)
	if err != nil {
		return err
	}

	// Terminal states do not advance
	if run.Status == state.WorkflowRunStatusSucceeded ||
		run.Status == state.WorkflowRunStatusFailed ||
		run.Status == state.WorkflowRunStatusDead {
		return nil
	}

	var spec api.WorkflowSpec
	if err := json.Unmarshal(run.DefinitionSnapshot, &spec); err != nil {
		return err
	}

	steps, err := o.store.GetWorkflowSteps(ctx, runID)
	if err != nil {
		return err
	}

	stepMap := make(map[string]*state.WorkflowStep, len(steps))
	for _, s := range steps {
		stepMap[s.StepName] = s
	}

	specStepMap := make(map[string]api.WorkflowStepSpec, len(spec.Steps))
	for _, s := range spec.Steps {
		specStepMap[s.Name] = s
	}

	// Check if all steps have reached terminal status
	allSucceeded := true
	hasDead := false
	hasFailed := false

	for _, s := range steps {
		switch s.Status {
		case state.WorkflowStepStatusSucceeded, state.WorkflowStepStatusSkipped:
			// good
		case state.WorkflowStepStatusDead:
			hasDead = true
		case state.WorkflowStepStatusFailed:
			hasFailed = true
		default:
			allSucceeded = false
		}
	}

	if hasDead {
		_ = o.store.MarkWorkflowRunStatus(ctx, runID, state.WorkflowRunStatusDead, nil, nil)
		if o.metrics != nil {
			o.metrics.ObserveRunComplete(run.AppID, "unknown", "dead", time.Since(run.CreatedAt))
		}
		return nil
	}

	if hasFailed {
		_ = o.store.MarkWorkflowRunStatus(ctx, runID, state.WorkflowRunStatusFailed, nil, nil)
		if o.metrics != nil {
			o.metrics.ObserveRunComplete(run.AppID, "unknown", "failed", time.Since(run.CreatedAt))
		}
		return nil
	}

	if allSucceeded && len(steps) > 0 {
		var lastOutput json.RawMessage
		for i := len(steps) - 1; i >= 0; i-- {
			if len(steps[i].Output) > 0 {
				lastOutput = steps[i].Output
				break
			}
		}
		_ = o.store.MarkWorkflowRunStatus(ctx, runID, state.WorkflowRunStatusSucceeded, lastOutput, nil)
		if o.metrics != nil {
			o.metrics.ObserveRunComplete(run.AppID, "unknown", "succeeded", time.Since(run.CreatedAt))
		}
		o.emitAudit(ctx, events.WorkflowSucceeded, map[string]any{
			"run_id":        run.ID,
			"app_id":        run.AppID,
			"workflow_name": run.WorkflowName,
			"outcome":       "succeeded",
			"output":        string(lastOutput),
		})
		return nil
	}

	// Find runnable steps: status is pending and all depends_on are succeeded
	advancedAny := false
	for _, s := range steps {
		if s.Status != state.WorkflowStepStatusPending {
			continue
		}

		stepSpec, exists := specStepMap[s.StepName]
		if !exists {
			continue
		}

		depsMet := true
		depFailed := false
		for _, dep := range stepSpec.DependsOn {
			depStep, ok := stepMap[dep]
			if !ok || depStep.Status != state.WorkflowStepStatusSucceeded {
				depsMet = false
			}
			if ok && (depStep.Status == state.WorkflowStepStatusFailed || depStep.Status == state.WorkflowStepStatusDead) {
				depFailed = true
			}
		}

		if depFailed {
			// Skip step whose dependency failed
			_ = o.store.MarkWorkflowStepStatus(ctx, runID, s.StepName, state.WorkflowStepStatusSkipped, s.Attempt, nil, nil)
			s.Status = state.WorkflowStepStatusSkipped
			advancedAny = true
			continue
		}

		if !depsMet {
			continue
		}

		// Step is ready to execute!
		adv, err := o.executeStep(ctx, run, s, stepSpec)
		if err != nil && o.log != nil {
			o.log.Warn("workflow orchestrator: execute step error", "run_id", runID, "step", s.StepName, "err", err)
		}
		if adv {
			advancedAny = true
		}
	}

	if advancedAny {
		// Recurse to see if downstream steps are unlocked
		return o.AdvanceWorkflowRun(ctx, runID)
	}

	return nil
}

func (o *WorkflowOrchestrator) executeStep(ctx context.Context, run *state.WorkflowRun, step *state.WorkflowStep, spec api.WorkflowStepSpec) (bool, error) {
	// Case A: wait_for_event step
	if spec.WaitForEvent != "" {
		evt, err := o.store.FindMatchingEvent(ctx, run.ID, spec.WaitForEvent)
		if err == nil && evt != nil {
			// Event already received! Complete step.
			_ = o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusSucceeded, step.Attempt, evt.Payload, nil)
			o.emitAudit(ctx, events.WorkflowStepSucceeded, map[string]any{
				"run_id":        run.ID,
				"app_id":        run.AppID,
				"workflow_name": run.WorkflowName,
				"step_name":     step.StepName,
				"status":        state.WorkflowStepStatusSucceeded,
				"output":        string(evt.Payload),
			})
			return true, nil
		}

		// Check timeout
		if spec.Timeout > 0 && time.Since(step.CreatedAt) > spec.Timeout {
			if spec.OnTimeout != "" {
				errMsg := "wait_for_event timed out"
				_ = o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusFailed, step.Attempt, nil, &errMsg)
			} else {
				errMsg := "wait_for_event timed out with no handler"
				_ = o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusDead, step.Attempt, nil, &errMsg)
				_ = o.store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusDead, nil, &errMsg)
			}
			return true, nil
		}

		// Park run in awaiting_event
		_ = o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusAwaitingEvent, step.Attempt, nil, nil)
		_ = o.store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusAwaitingEvent, nil, nil)
		o.emitAudit(ctx, events.WorkflowAwaitingEvent, map[string]any{
			"run_id":        run.ID,
			"app_id":        run.AppID,
			"workflow_name": run.WorkflowName,
			"step_name":     step.StepName,
			"event_name":    spec.WaitForEvent,
			"timeout":       spec.Timeout.String(),
		})
		return false, nil
	}

	// Case B: Execution step (HTTP to container)
	if o.executor == nil {
		return false, errors.New("no workflow step executor configured")
	}

	start := time.Now()
	_ = o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusRunning, step.Attempt+1, nil, nil)
	_ = o.store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusRunning, nil, nil)

	method := spec.Method
	if method == "" {
		method = "POST"
	}

	headers := map[string]string{
		"X-Faas-Internal-Wake":    "workflow",
		"X-Faas-Workflow-Run-Id":  run.ID,
		"X-Faas-Workflow-Step":    step.StepName,
		"X-Faas-Workflow-Attempt": fmt.Sprintf("%d", step.Attempt+1),
		"Content-Type":            "application/json",
	}

	inputBytes := step.Input
	if len(inputBytes) == 0 {
		inputBytes = run.Input
	}

	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	statusCode, body, err := o.executor.ExecuteStep(ctx, run.AppID, spec.Path, method, headers, inputBytes, timeout)
	duration := time.Since(start)

	if err == nil && statusCode >= 200 && statusCode < 300 {
		// Success (2xx)
		_ = o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusSucceeded, step.Attempt+1, body, nil)
		if o.metrics != nil {
			o.metrics.ObserveStepComplete(run.AppID, "unknown", step.StepName, "succeeded", duration)
		}
		o.emitAudit(ctx, events.WorkflowStepSucceeded, map[string]any{
			"run_id":        run.ID,
			"app_id":        run.AppID,
			"workflow_name": run.WorkflowName,
			"step_name":     step.StepName,
			"attempt":       step.Attempt + 1,
			"status":        state.WorkflowStepStatusSucceeded,
			"output":        string(body),
		})
		return true, nil
	}

	// Handle failures: determine retry policy
	maxAttempts := 3
	if spec.Retry != nil && spec.Retry.MaxAttempts > 0 {
		maxAttempts = spec.Retry.MaxAttempts
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	} else {
		errMsg = fmt.Sprintf("HTTP %d: %s", statusCode, string(body))
	}

	if (statusCode >= 500 || err != nil) && step.Attempt+1 < maxAttempts {
		// Retry eligible — kept in pending for next tick
		_ = o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusPending, step.Attempt+1, nil, &errMsg)
		return false, nil
	}

	// Failure is terminal for this step
	finalStatus := state.WorkflowStepStatusFailed
	if statusCode >= 500 || err != nil {
		finalStatus = state.WorkflowStepStatusDead
	}

	_ = o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, finalStatus, step.Attempt+1, nil, &errMsg)
	_ = o.store.MarkWorkflowRunStatus(ctx, run.ID, finalStatus, nil, &errMsg)

	if o.metrics != nil {
		o.metrics.ObserveStepComplete(run.AppID, "unknown", step.StepName, finalStatus, duration)
		if finalStatus == state.WorkflowStepStatusDead {
			o.metrics.ObserveDeadLetter(run.AppID, "unknown", "step_failed")
		}
	}

	o.emitAudit(ctx, events.WorkflowStepFailed, map[string]any{
		"run_id":        run.ID,
		"app_id":        run.AppID,
		"workflow_name": run.WorkflowName,
		"step_name":     step.StepName,
		"attempt":       step.Attempt + 1,
		"status":        finalStatus,
		"error":         errMsg,
	})

	return true, nil
}

// ProcessEvent injects an external event and resumes any parked awaiting_event step.
func (o *WorkflowOrchestrator) ProcessEvent(ctx context.Context, runID, eventName string, payload json.RawMessage) error {
	evt := &state.WorkflowEvent{
		RunID:     runID,
		EventName: eventName,
		Payload:   payload,
	}
	if err := o.store.InsertWorkflowEvent(ctx, evt); err != nil {
		return err
	}

	run, err := o.store.GetWorkflowRun(ctx, runID)
	if err != nil {
		return err
	}

	var spec api.WorkflowSpec
	_ = json.Unmarshal(run.DefinitionSnapshot, &spec)

	steps, _ := o.store.GetWorkflowSteps(ctx, runID)
	for _, s := range steps {
		if s.Status == state.WorkflowStepStatusAwaitingEvent {
			for _, stepSpec := range spec.Steps {
				if stepSpec.Name == s.StepName && stepSpec.WaitForEvent == eventName {
					_ = o.store.MarkWorkflowStepStatus(ctx, runID, s.StepName, state.WorkflowStepStatusSucceeded, s.Attempt, payload, nil)
				}
			}
		}
	}

	o.emitAudit(ctx, events.WorkflowEventReceived, map[string]any{
		"run_id":        run.ID,
		"app_id":        run.AppID,
		"workflow_name": run.WorkflowName,
		"event_name":    eventName,
		"payload":       string(payload),
	})

	if run.Status == state.WorkflowRunStatusAwaitingEvent {
		_ = o.store.MarkWorkflowRunStatus(ctx, runID, state.WorkflowRunStatusRunning, nil, nil)
	}

	return o.AdvanceWorkflowRun(ctx, runID)
}

// CancelRun aborts a running or awaiting workflow run.
func (o *WorkflowOrchestrator) CancelRun(ctx context.Context, runID string) error {
	run, err := o.store.GetWorkflowRun(ctx, runID)
	if err != nil {
		return err
	}

	if run.Status == state.WorkflowRunStatusSucceeded ||
		run.Status == state.WorkflowRunStatusFailed ||
		run.Status == state.WorkflowRunStatusDead {
		return nil
	}

	cancelMsg := "cancelled by operator"
	if err := o.store.MarkWorkflowRunStatus(ctx, runID, state.WorkflowRunStatusFailed, nil, &cancelMsg); err != nil {
		return err
	}

	// Mark any non-terminal steps as skipped
	steps, _ := o.store.GetWorkflowSteps(ctx, runID)
	for _, s := range steps {
		if s.Status == state.WorkflowStepStatusPending || s.Status == state.WorkflowStepStatusRunning || s.Status == state.WorkflowStepStatusAwaitingEvent {
			_ = o.store.MarkWorkflowStepStatus(ctx, runID, s.StepName, state.WorkflowStepStatusSkipped, s.Attempt, nil, &cancelMsg)
		}
	}

	o.emitAudit(ctx, events.WorkflowFailed, map[string]any{
		"run_id":        run.ID,
		"app_id":        run.AppID,
		"workflow_name": run.WorkflowName,
		"outcome":       "cancelled",
		"error":         cancelMsg,
	})

	return nil
}
