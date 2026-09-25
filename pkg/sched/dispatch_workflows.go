package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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

func workflowTimedOutOutput(raw json.RawMessage) bool {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || len(value) != 1 {
		return false
	}
	return value["timeout"] == true
}

func workflowRetryDelay(spec api.WorkflowStepSpec, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second
	if spec.Retry != nil && spec.Retry.Backoff == "exponential" {
		shift := attempt - 1
		if shift > 8 {
			shift = 8
		}
		delay = time.Duration(1<<shift) * time.Second
	}
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

// workflowStepPath resolves the target used by the HTTP wake executor.
//
// ADR-081's canonical wire format names a handler with `run`, while the
// current gateway executor accepts an HTTP path. Keep the additive `path`
// compatibility field working, and translate the canonical handler name to
// the app route that invokes it. Both values are validated at the API
// boundary, so the fallback cannot introduce a path separator or header
// injection.
func workflowStepPath(spec api.WorkflowStepSpec) string {
	if path := strings.TrimSpace(spec.Path); path != "" {
		return spec.Path
	}
	if run := strings.TrimSpace(spec.Run); run != "" {
		return "/" + run
	}
	return ""
}

// workflowTimeoutHandlerDecision keeps an on_timeout target out of the normal
// DAG until its source wait actually times out. It also lets the event path
// skip that target, so a fallback step cannot run on both branches or leave a
// successful event-driven run permanently pending.
func workflowTimeoutHandlerDecision(stepName string, specs []api.WorkflowStepSpec, steps map[string]*state.WorkflowStep) (blocked, skip bool) {
	referenced := false
	waiting := false
	triggered := false
	for _, spec := range specs {
		if spec.OnTimeout != stepName {
			continue
		}
		referenced = true
		step, ok := steps[spec.Name]
		if !ok {
			waiting = true
			continue
		}
		switch step.Status {
		case state.WorkflowStepStatusSucceeded:
			if workflowTimedOutOutput(step.Output) {
				triggered = true
			}
		case state.WorkflowStepStatusSkipped:
			// A skipped source cannot trigger a timeout handler.
		default:
			waiting = true
		}
	}
	if !referenced || triggered {
		return false, false
	}
	if waiting {
		return true, false
	}
	return false, true
}

// DispatchTick runs one iteration of claiming pending runs and advancing active ones.
func (o *WorkflowOrchestrator) DispatchTick(ctx context.Context) error {
	if o.store == nil {
		return nil
	}

	// Claim one newly queued run or one parked wait whose deadline is due.
	claimed, err := o.store.ClaimNextDueWorkflowRun(ctx)
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
			if recoverErr := o.store.RecoverWorkflowRun(ctx, claimed.ID); recoverErr != nil {
				return errors.Join(err, recoverErr)
			}
			return err
		}
	}

	return nil
}

// initAndAdvanceRun parses the definition snapshot, seeds step records, and advances.
func (o *WorkflowOrchestrator) initAndAdvanceRun(ctx context.Context, run *state.WorkflowRun) error {
	var spec api.WorkflowSpec
	if err := json.Unmarshal(run.DefinitionSnapshot, &spec); err != nil {
		lastErr := fmt.Sprintf("invalid definition snapshot: %v", err)
		if markErr := o.store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusFailed, nil, &lastErr); markErr != nil {
			return errors.Join(err, markErr)
		}
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
		if err := o.store.MarkWorkflowRunStatus(ctx, runID, state.WorkflowRunStatusDead, nil, nil); err != nil {
			return err
		}
		if o.metrics != nil {
			o.metrics.ObserveRunComplete(run.AppID, "unknown", "dead", time.Since(run.CreatedAt))
		}
		return nil
	}

	if hasFailed {
		if err := o.store.MarkWorkflowRunStatus(ctx, runID, state.WorkflowRunStatusFailed, nil, nil); err != nil {
			return err
		}
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
		if err := o.store.MarkWorkflowRunStatus(ctx, runID, state.WorkflowRunStatusSucceeded, lastOutput, nil); err != nil {
			return err
		}
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

	// First revisit parked waits. A duration wait may complete here, while
	// an event wait may take its timeout path. Event delivery normally marks
	// the event step succeeded before advancing the run.
	advancedAny := false
	for _, s := range steps {
		if s.Status != state.WorkflowStepStatusAwaitingEvent {
			continue
		}
		stepSpec, exists := specStepMap[s.StepName]
		if !exists {
			continue
		}
		adv, err := o.executeStep(ctx, run, s, stepSpec)
		if err != nil {
			if o.log != nil {
				o.log.Warn("workflow orchestrator: resume wait error", "run_id", runID, "step", s.StepName, "err", err)
			}
			return err
		}
		advancedAny = advancedAny || adv
	}

	// Find runnable steps: status is pending and all depends_on are succeeded
	for _, s := range steps {
		if s.Status != state.WorkflowStepStatusPending {
			continue
		}

		stepSpec, exists := specStepMap[s.StepName]
		if !exists {
			continue
		}
		blocked, skip := workflowTimeoutHandlerDecision(s.StepName, spec.Steps, stepMap)
		if skip {
			if err := o.store.MarkWorkflowStepStatus(ctx, runID, s.StepName, state.WorkflowStepStatusSkipped, s.Attempt, nil, nil); err != nil {
				return err
			}
			s.Status = state.WorkflowStepStatusSkipped
			advancedAny = true
			continue
		}
		if blocked {
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
			if err := o.store.MarkWorkflowStepStatus(ctx, runID, s.StepName, state.WorkflowStepStatusSkipped, s.Attempt, nil, nil); err != nil {
				return err
			}
			s.Status = state.WorkflowStepStatusSkipped
			advancedAny = true
			continue
		}

		if !depsMet {
			continue
		}

		// Step is ready to execute!
		adv, err := o.executeStep(ctx, run, s, stepSpec)
		if err != nil {
			if o.log != nil {
				o.log.Warn("workflow orchestrator: execute step error", "run_id", runID, "step", s.StepName, "err", err)
			}
			return err
		}
		if adv {
			advancedAny = true
		}
	}

	if advancedAny {
		// Recurse to see if downstream steps are unlocked
		return o.AdvanceWorkflowRun(ctx, runID)
	}
	// An active condition has an independent next-check timestamp. Reconcile
	// the run wake against every parked wait after this pass, so a parallel
	// timer or event deadline is not lost when the checker re-parks.
	if err := o.reconcileConditionWake(ctx, runID, spec); err != nil {
		return err
	}

	return nil
}

func (o *WorkflowOrchestrator) reconcileConditionWake(ctx context.Context, runID string, spec api.WorkflowSpec) error {
	steps, err := o.store.GetWorkflowSteps(ctx, runID)
	if err != nil {
		return err
	}
	byName := make(map[string]api.WorkflowStepSpec, len(spec.Steps))
	for _, item := range spec.Steps {
		byName[item.Name] = item
	}
	conditionAwaiting := false
	var earliest time.Time
	for _, step := range steps {
		if step.Status != state.WorkflowStepStatusAwaitingEvent || step.StartedAt == nil {
			continue
		}
		item := byName[step.StepName]
		var due time.Time
		switch {
		case item.WaitForCondition != nil && step.NextCheckAt != nil:
			conditionAwaiting = true
			due = *step.NextCheckAt
		case item.WaitForDuration > 0:
			due = step.StartedAt.Add(item.WaitForDuration)
		case item.WaitForEvent != "" || item.WaitForCallback:
			due = step.StartedAt.Add(item.Timeout)
		}
		if !due.IsZero() && (earliest.IsZero() || due.Before(earliest)) {
			earliest = due
		}
	}
	if conditionAwaiting && !earliest.IsZero() {
		return o.store.SetWorkflowRunWaitWake(ctx, runID, earliest)
	}
	return nil
}

func (o *WorkflowOrchestrator) executeStep(ctx context.Context, run *state.WorkflowRun, step *state.WorkflowStep, spec api.WorkflowStepSpec) (bool, error) {
	if spec.WaitForCondition != nil {
		return o.executeConditionCheck(ctx, run, step, spec)
	}
	// A duration wait has no executor call: only a persisted deadline is left
	// behind while the run is parked. The step's first started_at is the source
	// of truth, so an unrelated event cannot move the deadline forward.
	if spec.WaitForDuration > 0 {
		if step.StartedAt != nil && !time.Now().UTC().Before(step.StartedAt.Add(spec.WaitForDuration)) {
			if err := o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusSucceeded, step.Attempt, nil, nil); err != nil {
				return false, err
			}
			o.emitAudit(ctx, events.WorkflowStepSucceeded, map[string]any{
				"run_id": run.ID, "app_id": run.AppID,
				"workflow_name": run.WorkflowName, "step_name": step.StepName,
				"status": state.WorkflowStepStatusSucceeded,
			})
			return true, nil
		}
		deadline, err := o.store.ParkWorkflowTimer(ctx, run.ID, step.StepName, spec.WaitForDuration)
		if err != nil {
			return false, err
		}
		if step.StartedAt == nil {
			o.emitAudit(ctx, events.WorkflowAwaitingTimer, map[string]any{
				"run_id": run.ID, "app_id": run.AppID,
				"workflow_name": run.WorkflowName, "step_name": step.StepName,
				"duration": spec.WaitForDuration.String(), "wake_at": deadline,
			})
		}
		return false, nil
	}

	// Case A: event or account-authorized callback wait. Registration and
	// event lookup share the run lock with event insertion, so an arrival in
	// the check/park window cannot be lost.
	eventName := spec.WaitForEvent
	if spec.WaitForCallback {
		eventName = api.WorkflowCallbackEventName(run.ID, step.StepName)
	}
	if eventName != "" {
		evt, deadline, err := o.store.ParkWorkflowEvent(ctx, run.ID, step.StepName, eventName, spec.Timeout)
		if err != nil {
			return false, err
		}
		if evt == nil && !time.Now().UTC().Before(deadline) {
			var timedOut bool
			evt, timedOut, err = o.store.ResolveWorkflowEventWait(ctx, run.ID, step.StepName, eventName, spec.Timeout, spec.OnTimeout != "")
			if err != nil {
				return false, err
			}
			if timedOut {
				return true, nil
			}
		}
		if evt != nil {
			// Event already received! Complete step.
			if err := o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusSucceeded, step.Attempt, evt.Payload, nil); err != nil {
				return false, err
			}
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

		if step.StartedAt == nil {
			o.emitAudit(ctx, events.WorkflowAwaitingEvent, map[string]any{
				"run_id": run.ID, "app_id": run.AppID,
				"workflow_name": run.WorkflowName, "step_name": step.StepName,
				"event_name": eventName, "timeout": spec.Timeout.String(),
			})
		}
		return false, nil
	}

	// Case B: Execution step (HTTP to container)
	if o.executor == nil {
		return false, errors.New("no workflow step executor configured")
	}

	start := time.Now()
	if err := o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusRunning, step.Attempt+1, nil, nil); err != nil {
		return false, err
	}
	if err := o.store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusRunning, nil, nil); err != nil {
		return false, err
	}

	method := spec.Method
	if method == "" {
		method = "POST"
	}

	headers := map[string]string{
		"X-Faas-Internal-Wake":    "workflow",
		"X-Faas-Workflow-Run-Id":  run.ID,
		"X-Faas-Workflow-Step":    step.StepName,
		"X-Faas-Workflow-Attempt": fmt.Sprintf("%d", step.Attempt+1),
		"Idempotency-Key":         fmt.Sprintf("workflow/%s/%s/%d", run.ID, step.StepName, step.Attempt+1),
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

	statusCode, body, err := o.executor.ExecuteStep(ctx, run.AppID, workflowStepPath(spec), method, headers, inputBytes, timeout)
	duration := time.Since(start)

	if err == nil && statusCode >= 200 && statusCode < 300 {
		// Success (2xx)
		if err := o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusSucceeded, step.Attempt+1, body, nil); err != nil {
			return false, err
		}
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
		// Retry eligible — schedule the next attempt after the configured
		// backoff so a failing dependency cannot hot-loop the dispatcher.
		if err := o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusPending, step.Attempt+1, nil, &errMsg); err != nil {
			return false, err
		}
		if err := o.store.ScheduleWorkflowRun(ctx, run.ID, state.WorkflowRunStatusPending,
			time.Now().UTC().Add(workflowRetryDelay(spec, step.Attempt+1))); err != nil {
			return false, err
		}
		return false, nil
	}

	// Failure is terminal for this step
	finalStatus := state.WorkflowStepStatusFailed
	if statusCode >= 500 || err != nil {
		finalStatus = state.WorkflowStepStatusDead
	}

	if err := o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, finalStatus, step.Attempt+1, nil, &errMsg); err != nil {
		return false, err
	}
	if err := o.store.MarkWorkflowRunStatus(ctx, run.ID, finalStatus, nil, &errMsg); err != nil {
		return false, err
	}

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

// executeConditionCheck makes at most one bounded checker call. Every false or
// transient result commits the next wake before returning to the scheduler.
func (o *WorkflowOrchestrator) executeConditionCheck(ctx context.Context, run *state.WorkflowRun, step *state.WorkflowStep, spec api.WorkflowStepSpec) (bool, error) {
	condition := spec.WaitForCondition
	update := state.WorkflowConditionUpdate{
		RunID: run.ID, StepName: step.StepName, Interval: condition.Interval,
		Timeout: spec.Timeout, MaxAttempts: condition.MaxAttempts, OnTimeout: spec.OnTimeout != "",
	}
	outcome, err := o.store.ResolveWorkflowCondition(ctx, update)
	if err != nil {
		return false, err
	}
	switch outcome.Status {
	case state.WorkflowConditionWaiting:
		return false, nil
	case state.WorkflowConditionTimedOut:
		return true, nil
	case state.WorkflowConditionReady:
		// Continue below.
	default:
		return false, fmt.Errorf("unexpected workflow condition status %q", outcome.Status)
	}
	if o.executor == nil {
		return false, errors.New("no workflow step executor configured")
	}
	attempt := step.Attempt + 1
	if err := o.store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusRunning, nil, nil); err != nil {
		return false, err
	}
	if err := o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusRunning, attempt, nil, nil); err != nil {
		return false, err
	}
	input := step.Input
	if step.Attempt > 0 && len(step.Output) > 0 {
		input = step.Output
	} else if len(input) == 0 {
		input = run.Input
	}
	headers := map[string]string{
		"X-Faas-Internal-Wake": "workflow", "X-Faas-Workflow-Run-Id": run.ID,
		"X-Faas-Workflow-Step": step.StepName, "X-Faas-Workflow-Attempt": fmt.Sprintf("%d", attempt),
		"Idempotency-Key": fmt.Sprintf("workflow/%s/%s/%d", run.ID, step.StepName, attempt),
		"Content-Type":    "application/json",
	}
	statusCode, body, callErr := o.executor.ExecuteStep(ctx, run.AppID, "/"+condition.Run, "POST", headers, input, 30*time.Second)
	update.Checked = true
	if callErr == nil && statusCode >= 200 && statusCode < 300 {
		var response struct {
			Done *bool `json:"done"`
		}
		if err := json.Unmarshal(body, &response); err != nil || response.Done == nil {
			return o.failConditionCheck(ctx, run, step, attempt, "condition checker must return a JSON object with boolean done", state.WorkflowStepStatusDead)
		}
		update.Done = *response.Done
		update.Result = json.RawMessage(body)
	} else if callErr != nil || statusCode >= 500 {
		message := fmt.Sprintf("condition checker HTTP %d", statusCode)
		if callErr != nil {
			message = callErr.Error()
		}
		update.Error = &message
		update.Result = json.RawMessage(`{"done":false}`)
	} else {
		return o.failConditionCheck(ctx, run, step, attempt, fmt.Sprintf("condition checker HTTP %d", statusCode), state.WorkflowStepStatusFailed)
	}
	outcome, err = o.store.ResolveWorkflowCondition(ctx, update)
	if err != nil {
		return false, err
	}
	if outcome.Status == state.WorkflowConditionWaiting {
		if step.StartedAt == nil {
			o.emitAudit(ctx, events.WorkflowAwaitingCondition, map[string]any{
				"run_id": run.ID, "app_id": run.AppID, "workflow_name": run.WorkflowName,
				"step_name": step.StepName, "next_check_at": outcome.NextCheckAt,
			})
		}
		return false, nil
	}
	if outcome.Status == state.WorkflowConditionSucceeded || outcome.Status == state.WorkflowConditionTimedOut {
		return true, nil
	}
	return false, fmt.Errorf("unexpected workflow condition status %q", outcome.Status)
}

func (o *WorkflowOrchestrator) failConditionCheck(ctx context.Context, run *state.WorkflowRun, step *state.WorkflowStep, attempt int, message, status string) (bool, error) {
	if err := o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, status, attempt, nil, &message); err != nil {
		return false, err
	}
	if err := o.store.MarkWorkflowRunStatus(ctx, run.ID, status, nil, &message); err != nil {
		return false, err
	}
	o.emitAudit(ctx, events.WorkflowStepFailed, map[string]any{
		"run_id": run.ID, "app_id": run.AppID, "workflow_name": run.WorkflowName,
		"step_name": step.StepName, "attempt": attempt, "status": status, "error": message,
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
	if err := json.Unmarshal(run.DefinitionSnapshot, &spec); err != nil {
		return fmt.Errorf("decode workflow definition: %w", err)
	}

	steps, err := o.store.GetWorkflowSteps(ctx, runID)
	if err != nil {
		return err
	}
	for _, s := range steps {
		if s.Status == state.WorkflowStepStatusAwaitingEvent {
			for _, stepSpec := range spec.Steps {
				if stepSpec.Name == s.StepName && stepSpec.WaitForEvent == eventName {
					if err := o.store.MarkWorkflowStepStatus(ctx, runID, s.StepName, state.WorkflowStepStatusSucceeded, s.Attempt, payload, nil); err != nil {
						return err
					}
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
		if err := o.store.ScheduleWorkflowRun(ctx, runID, state.WorkflowRunStatusPending, time.Now().UTC()); err != nil {
			return err
		}
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
	run, err = o.store.CancelWorkflowRun(ctx, runID, cancelMsg)
	if err != nil {
		return err
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
