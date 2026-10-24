package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workflowcode"
)

// codeRunResult is the function result kept on the $driver row until the
// run closes, so an interrupted close is finished on the next pass.
type codeRunResult struct {
	Status string          `json:"status"`
	Output json.RawMessage `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// advanceCodeWorkflowRunOnce drives one pass of a code-first run
// (ADR-20261021204307): resolve parked operations, then call the entry
// handler when the code has results it has not seen, else park the run.
func (o *WorkflowOrchestrator) advanceCodeWorkflowRunOnce(ctx context.Context, run *state.WorkflowRun, spec api.WorkflowSpec) (bool, error) {
	store, ok := o.store.(state.WorkflowCodeStore)
	if !ok {
		return false, o.closeCodeRun(ctx, run, 0, codeRunResult{Status: state.WorkflowRunStatusDead, Error: "code-first workflows are unavailable on this scheduler"})
	}
	steps, err := o.store.GetWorkflowSteps(ctx, run.ID)
	if err != nil {
		return false, err
	}
	driverAttempt := 0
	for _, step := range steps {
		if step.StepName == api.WorkflowCodeDriverStep {
			driverAttempt = step.Attempt
		}
	}
	journal, err := workflowcode.BuildJournal(workflowCodeRows(steps))
	if err != nil {
		return false, o.closeCodeRun(ctx, run, driverAttempt, codeRunResult{Status: state.WorkflowRunStatusDead, Error: err.Error()})
	}
	if journal.Driver != nil && len(journal.Driver.Row.Output) > 0 {
		var result codeRunResult
		if json.Unmarshal(journal.Driver.Row.Output, &result) == nil && result.Status != "" {
			return false, o.closeCodeRun(ctx, run, driverAttempt, result)
		}
	}
	resolved, err := o.resolveCodeWaits(ctx, store, run, journal)
	if err != nil || resolved {
		return resolved, err
	}
	due, wake, wakeStatus := workflowCodeDue(journal, time.Now().UTC())
	if !due {
		return false, o.store.SetWorkflowRunWake(ctx, run.ID, wakeStatus, wake)
	}
	limits, err := o.workflowCodeLimits(ctx, run, *spec.Code)
	if err != nil {
		return false, err
	}
	return o.invokeCodeWorkflow(ctx, store, run, *spec.Code, journal, limits)
}

func workflowCodeRows(steps []*state.WorkflowStep) []workflowcode.Row {
	rows := make([]workflowcode.Row, 0, len(steps))
	for _, step := range steps {
		rows = append(rows, workflowcode.Row{
			Name: step.StepName, Status: step.Status, Attempt: step.Attempt, Input: step.Input, Output: step.Output,
			Error: step.Error, StartedAt: step.StartedAt, FinishedAt: step.FinishedAt, NextRetry: step.NextRetryAt,
		})
	}
	return rows
}

// resolveCodeWaits completes due sleeps and matched or timed-out event
// waits, and parks the rest. It reports whether any wait resolved.
func (o *WorkflowOrchestrator) resolveCodeWaits(ctx context.Context, store state.WorkflowCodeStore, run *state.WorkflowRun, journal workflowcode.Journal) (bool, error) {
	resolved := false
	for _, entry := range journal.Entries {
		if entry.Status != workflowcode.EntryWaiting {
			continue
		}
		item := journal.Rows[entry.Name]
		switch entry.Kind {
		case workflowcode.KindSleep:
			if item.Row.StartedAt != nil && !time.Now().UTC().Before(item.Row.StartedAt.Add(item.Descriptor.Duration())) {
				if err := o.store.MarkWorkflowStepStatus(ctx, run.ID, entry.Name, state.WorkflowStepStatusSucceeded, item.Row.Attempt, nil, nil); err != nil {
					return false, err
				}
				resolved = true
				continue
			}
			if item.Row.Status == state.WorkflowStepStatusPending {
				deadline, err := o.store.ParkWorkflowTimer(ctx, run.ID, entry.Name, item.Descriptor.Duration())
				if err != nil {
					return false, err
				}
				o.emitAudit(ctx, events.WorkflowAwaitingTimer, map[string]any{
					"run_id": run.ID, "app_id": run.AppID, "workflow_name": run.WorkflowName, "step_name": entry.Name,
					"duration": item.Descriptor.Duration().String(), "wake_at": deadline,
				})
			}
		case workflowcode.KindEvent:
			event, timedOut, _, err := store.ParkWorkflowCodeEvent(ctx, run.ID, entry.Name, item.Descriptor.Event, item.Descriptor.Timeout())
			if err != nil {
				return false, err
			}
			if event != nil || timedOut {
				resolved = true
			} else if item.Row.Status == state.WorkflowStepStatusPending {
				o.emitAudit(ctx, events.WorkflowAwaitingEvent, map[string]any{
					"run_id": run.ID, "app_id": run.AppID, "workflow_name": run.WorkflowName, "step_name": entry.Name,
					"event_name": item.Descriptor.Event, "timeout": item.Descriptor.Timeout().String(),
				})
			}
		}
	}
	return resolved, nil
}

// workflowCodeSettled counts resolved operations. The code must be called
// again whenever more operations are resolved than it saw last time.
func workflowCodeSettled(journal workflowcode.Journal) int {
	settled := 0
	for _, entry := range journal.Entries {
		if entry.Status == workflowcode.EntrySucceeded || entry.Status == workflowcode.EntryFailed {
			settled++
		}
	}
	return settled
}

// workflowCodeDue reports whether the entry handler must be called now. If
// not, it returns the earliest wake and the run status to wait in.
func workflowCodeDue(journal workflowcode.Journal, now time.Time) (bool, time.Time, string) {
	if journal.Driver == nil {
		return true, time.Time{}, ""
	}
	if journal.Driver.Row.NextRetry != nil {
		// A failed call retries on its own schedule.
		if !now.Before(*journal.Driver.Row.NextRetry) {
			return true, time.Time{}, ""
		}
		return false, *journal.Driver.Row.NextRetry, state.WorkflowRunStatusPending
	}
	if workflowCodeSettled(journal) > journal.Driver.Descriptor.Seen {
		return true, time.Time{}, ""
	}
	var wake time.Time
	status := state.WorkflowRunStatusAwaitingEvent
	consider := func(at time.Time, candidate string) {
		if wake.IsZero() || at.Before(wake) {
			wake, status = at, candidate
		}
	}
	for _, entry := range journal.Entries {
		item := journal.Rows[entry.Name]
		switch {
		case entry.Status == workflowcode.EntryRetrying:
			if item.Row.NextRetry == nil || !now.Before(*item.Row.NextRetry) {
				return true, time.Time{}, ""
			}
			consider(*item.Row.NextRetry, state.WorkflowRunStatusPending)
		case entry.Status == workflowcode.EntryWaiting && item.Row.StartedAt != nil:
			limit := item.Descriptor.Duration()
			if entry.Kind == workflowcode.KindEvent {
				limit = item.Descriptor.Timeout()
			}
			consider(item.Row.StartedAt.Add(limit), state.WorkflowRunStatusAwaitingEvent)
		}
	}
	if wake.IsZero() {
		// Nothing left to wait for: let the code decide.
		return true, time.Time{}, ""
	}
	return false, wake, status
}

func (o *WorkflowOrchestrator) workflowCodeLimits(ctx context.Context, run *state.WorkflowRun, code api.WorkflowCodeSpec) (workflowcode.Limits, error) {
	app, err := o.store.AppByID(ctx, run.AppID)
	if err != nil {
		return workflowcode.Limits{}, err
	}
	account, err := o.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return workflowcode.Limits{}, err
	}
	maxWait := time.Duration(account.Plan.WorkflowMaxWaitDays()) * 24 * time.Hour
	return workflowcode.DefaultLimits(maxWait, api.WorkflowCodeRetry(code)), nil
}

// invokeCodeWorkflow makes one entry-handler call and applies its answer.
func (o *WorkflowOrchestrator) invokeCodeWorkflow(ctx context.Context, store state.WorkflowCodeStore, run *state.WorkflowRun, code api.WorkflowCodeSpec, journal workflowcode.Journal, limits workflowcode.Limits) (bool, error) {
	failures := 0
	if journal.Driver != nil {
		failures = journal.Driver.Descriptor.Failures
	}
	seen := workflowCodeSettled(journal)
	attempt, err := store.BeginWorkflowCodeInvocation(ctx, run.ID, workflowcode.EncodeDescriptor(workflowcode.Descriptor{Kind: workflowcode.KindDriver, Failures: failures, Seen: seen}))
	if err != nil {
		return false, err
	}
	timeout := code.Timeout
	if timeout <= 0 {
		timeout = api.WorkflowStepDefaultTimeout
	}
	if leaseStore, ok := o.store.(state.WorkflowRunLeaseStore); ok {
		if err := leaseStore.ExtendWorkflowRunLease(ctx, run.ID, timeout); err != nil {
			return false, err
		}
	}
	input := run.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	body, err := json.Marshal(workflowcode.Request{
		Protocol: api.WorkflowCodeProtocol, RunID: run.ID, Workflow: run.WorkflowName,
		Input: input, Invocation: attempt, Journal: journal.Entries,
	})
	if err != nil {
		return false, err
	}
	if int64(len(body)) > api.WorkflowCodeRequestMaxBytes {
		message := fmt.Sprintf("%s: entry request is %d bytes, limit %d", workflowcode.ErrLimit, len(body), api.WorkflowCodeRequestMaxBytes)
		return false, o.finishCodeRunDead(ctx, store, run, attempt, nil, failures, seen, message)
	}
	headers := map[string]string{
		"X-Faas-Internal-Wake":     "workflow",
		"X-Faas-Workflow-Run-Id":   run.ID,
		"X-Faas-Workflow-Step":     api.WorkflowCodeDriverStep,
		"X-Faas-Workflow-Attempt":  strconv.Itoa(attempt),
		"Idempotency-Key":          workflowStepIdempotencyKey(run.ID, api.WorkflowCodeDriverStep),
		"Content-Type":             "application/json",
		api.WorkflowCodeModeHeader: "code",
	}
	start := time.Now()
	statusCode, respBody, callErr := o.executeWorkflowHandler(ctx, run, api.WorkflowCodePath(code), "POST", headers, body, timeout, "", 0)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	httpStatus := workflowHTTPStatus(statusCode, callErr)
	if callErr == nil && statusCode >= 200 && statusCode < 300 {
		var response workflowcode.Response
		decodeErr := json.Unmarshal(respBody, &response)
		var plan workflowcode.Plan
		if decodeErr == nil {
			plan, decodeErr = workflowcode.Validate(journal, response, limits)
		} else {
			decodeErr = fmt.Errorf("%w: %w", workflowcode.ErrProtocol, decodeErr)
		}
		switch {
		case decodeErr == nil:
			return o.applyCodeResponse(ctx, store, run, attempt, httpStatus, seen, plan, time.Since(start))
		case errors.Is(decodeErr, workflowcode.ErrNondeterministic), errors.Is(decodeErr, workflowcode.ErrLimit):
			message := decodeErr.Error()
			return false, o.finishCodeRunDead(ctx, store, run, attempt, httpStatus, failures, seen, message)
		default:
			callErr = decodeErr
		}
	}
	message := fmt.Sprintf("HTTP %d: %s", statusCode, truncateCodeError(string(respBody)))
	if callErr != nil {
		message = truncateCodeError(callErr.Error())
	}
	failures++
	if failures >= api.WorkflowCodeDriverMaxAttempts(code) {
		return false, o.finishCodeRunDead(ctx, store, run, attempt, httpStatus, failures, seen, fmt.Sprintf("entry handler failed %d times: %s", failures, message))
	}
	retryAt := time.Now().UTC().Add(workflowcode.DriverRetryDelay(failures))
	if err := store.FinishWorkflowCodeInvocation(ctx, run.ID, state.WorkflowCodeInvocation{
		Attempt: attempt, HTTPStatus: httpStatus, Error: &message, RetryAt: &retryAt,
		DriverInput: workflowcode.EncodeDescriptor(workflowcode.Descriptor{Kind: workflowcode.KindDriver, Failures: failures, Seen: seen}),
	}); err != nil {
		return false, err
	}
	return false, o.store.SetWorkflowRunWake(ctx, run.ID, state.WorkflowRunStatusPending, retryAt)
}

func truncateCodeError(message string) string {
	if len(message) <= api.WorkflowCodeErrorMaxBytes {
		return message
	}
	return message[:api.WorkflowCodeErrorMaxBytes]
}

// applyCodeResponse records a validated answer in one transaction, then
// closes the run if the function finished.
func (o *WorkflowOrchestrator) applyCodeResponse(ctx context.Context, store state.WorkflowCodeStore, run *state.WorkflowRun, attempt int, httpStatus *int, seen int, plan workflowcode.Plan, duration time.Duration) (bool, error) {
	now := time.Now().UTC()
	writes := make([]state.WorkflowCodeOpWrite, 0, len(plan.Ops))
	for _, op := range plan.Ops {
		write := state.WorkflowCodeOpWrite{StepName: op.Name, New: op.New, Input: workflowcode.EncodeDescriptor(op.Descriptor), Status: state.WorkflowStepStatusPending}
		if op.Kind == workflowcode.KindStep {
			write.Attempt = op.Attempt
			switch {
			case op.Status == workflowcode.ResultSucceeded:
				write.Status = state.WorkflowStepStatusSucceeded
				write.Output = op.Output
			case workflowcode.ShouldRetry(op):
				message := op.Error
				retryAt := now.Add(workflowcode.RetryDelay(op.Retry, op.Attempt))
				write.Error, write.RetryAt = &message, &retryAt
			default:
				message := op.Error
				write.Status = state.WorkflowStepStatusFailed
				write.Error = &message
			}
		}
		writes = append(writes, write)
	}
	finish := state.WorkflowCodeInvocation{
		Attempt: attempt, HTTPStatus: httpStatus, Ops: writes,
		DriverInput: workflowcode.EncodeDescriptor(workflowcode.Descriptor{Kind: workflowcode.KindDriver, Seen: seen}),
	}
	var result *codeRunResult
	if plan.Result.Status != workflowcode.ResultRunning {
		result = &codeRunResult{Status: plan.Result.Status, Output: plan.Result.Output, Error: plan.Result.Error}
		if result.Status == workflowcode.ResultFailed && result.Error == "" {
			result.Error = "workflow function failed"
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return false, err
		}
		finish.Result = raw
	}
	if err := store.FinishWorkflowCodeInvocation(ctx, run.ID, finish); err != nil {
		return false, err
	}
	for _, write := range writes {
		o.auditCodeStep(ctx, run, write, duration)
	}
	if result != nil {
		return false, o.closeCodeRun(ctx, run, attempt, *result)
	}
	return true, nil
}

func (o *WorkflowOrchestrator) auditCodeStep(ctx context.Context, run *state.WorkflowRun, write state.WorkflowCodeOpWrite, duration time.Duration) {
	if write.Attempt == 0 || write.Status == state.WorkflowStepStatusPending {
		return
	}
	kind := events.WorkflowStepSucceeded
	payload := map[string]any{
		"run_id": run.ID, "app_id": run.AppID, "workflow_name": run.WorkflowName,
		"step_name": write.StepName, "attempt": write.Attempt, "status": write.Status,
	}
	if write.Status == state.WorkflowStepStatusSucceeded {
		payload["output"] = string(write.Output)
	} else {
		kind = events.WorkflowStepFailed
		if write.Error != nil {
			payload["error"] = *write.Error
		}
	}
	if o.metrics != nil {
		o.metrics.ObserveStepComplete(run.AppID, "unknown", write.StepName, write.Status, duration)
	}
	o.emitAudit(ctx, kind, payload)
}

// finishCodeRunDead records a terminal invocation and closes the run dead.
func (o *WorkflowOrchestrator) finishCodeRunDead(ctx context.Context, store state.WorkflowCodeStore, run *state.WorkflowRun, attempt int, httpStatus *int, failures, seen int, message string) error {
	result := codeRunResult{Status: state.WorkflowRunStatusDead, Error: truncateCodeError(message)}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if err := store.FinishWorkflowCodeInvocation(ctx, run.ID, state.WorkflowCodeInvocation{
		Attempt: attempt, HTTPStatus: httpStatus, Error: &result.Error, Result: raw,
		DriverInput: workflowcode.EncodeDescriptor(workflowcode.Descriptor{Kind: workflowcode.KindDriver, Failures: failures, Seen: seen}),
	}); err != nil {
		return err
	}
	return o.closeCodeRun(ctx, run, attempt, result)
}

// closeCodeRun closes the $driver row and the run. A function that returned
// or threw leaves the driver succeeded; a dead run leaves it dead.
func (o *WorkflowOrchestrator) closeCodeRun(ctx context.Context, run *state.WorkflowRun, driverAttempt int, result codeRunResult) error {
	driverStatus := state.WorkflowStepStatusSucceeded
	runStatus := state.WorkflowRunStatusSucceeded
	var output json.RawMessage
	var lastErr *string
	switch result.Status {
	case workflowcode.ResultSucceeded:
		output = result.Output
		if len(output) == 0 {
			output = json.RawMessage("null")
		}
	case workflowcode.ResultFailed:
		runStatus = state.WorkflowRunStatusFailed
		lastErr = &result.Error
	default:
		driverStatus, runStatus = state.WorkflowStepStatusDead, state.WorkflowRunStatusDead
		lastErr = &result.Error
	}
	if err := o.store.MarkWorkflowStepStatus(ctx, run.ID, api.WorkflowCodeDriverStep, driverStatus, driverAttempt, nil, lastErr); err != nil &&
		!errors.Is(err, state.ErrWorkflowStepNotFound) && !errors.Is(err, state.ErrConflict) {
		return err
	}
	if err := o.store.MarkWorkflowRunStatus(ctx, run.ID, runStatus, output, lastErr); err != nil {
		return err
	}
	if o.metrics != nil {
		o.metrics.ObserveRunComplete(run.AppID, "unknown", runStatus, time.Since(run.CreatedAt))
		if runStatus == state.WorkflowRunStatusDead {
			o.metrics.ObserveDeadLetter(run.AppID, "unknown", "code_workflow")
		}
	}
	payload := map[string]any{"run_id": run.ID, "app_id": run.AppID, "workflow_name": run.WorkflowName, "outcome": runStatus}
	kind := events.WorkflowSucceeded
	if runStatus == state.WorkflowRunStatusSucceeded {
		payload["output"] = string(output)
	} else {
		kind = events.WorkflowFailed
		payload["error"] = *lastErr
	}
	o.emitAudit(ctx, kind, payload)
	return nil
}
