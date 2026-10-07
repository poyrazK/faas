package sched

import (
	"bytes"
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
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

const workflowActionCapacityPollInterval = time.Second

// WorkflowStepExecutor dispatches a single step execution to an app instance.
type WorkflowStepExecutor interface {
	ExecuteStep(ctx context.Context, appID string, path, method string, headers map[string]string, body []byte, timeout time.Duration) (int, []byte, error)
}

// WorkflowStepIdentity is persisted with the run and accompanies every
// synthetic app invocation. Executors must treat it as internal metadata.
type WorkflowStepIdentity struct {
	RunID            string
	PlatformTenantID string
}

// WorkflowIdentityExecutor carries the durable run identity through the
// scheduler-to-gateway envelope. Legacy executors remain usable for unscoped
// runs; tenant-bound runs fail closed if the executor cannot carry identity.
type WorkflowIdentityExecutor interface {
	ExecuteWorkflowStep(ctx context.Context, appID string, identity WorkflowStepIdentity, path, method string, headers map[string]string, body []byte, timeout time.Duration, managedOperationID string, generation int64) (int, []byte, error)
}

// WorkflowRetryAfterExecutor preserves downstream throttling deadlines while
// carrying the same trusted identity and operation context as normal dispatch.
type WorkflowRetryAfterExecutor interface {
	ExecuteWorkflowStepWithRetryAfter(context.Context, string, WorkflowStepIdentity, string, string, map[string]string, []byte, time.Duration, string, int64) (int, []byte, time.Time, error)
}

// WorkflowManagedOperationExecutor is implemented by the authenticated
// scheduler-to-gateway transport. The operation identity is host metadata;
// it must never be supplied as a customer HTTP header.
type WorkflowManagedOperationExecutor interface {
	ExecuteManagedOperationStep(ctx context.Context, appID string, path, method string, headers map[string]string, body []byte, timeout time.Duration, operationID string, generation int64) (int, []byte, error)
}

// WorkflowOrchestrator coordinates workflow state transitions and step dispatch (ADR-081).
type WorkflowOrchestrator struct {
	store    state.Store
	executor WorkflowStepExecutor
	outbound WorkflowOutboundExecutor
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

// workflowStepIdempotencyKey identifies the logical step across all of its
// handler retries. The attempt header remains separate for observability.
func workflowStepIdempotencyKey(runID, stepName string) string {
	return fmt.Sprintf("workflow/%s/%s", runID, stepName)
}

func workflowHTTPStatus(statusCode int, callErr error) *int {
	if callErr != nil || statusCode < 100 || statusCode > 599 {
		return nil
	}
	return &statusCode
}

func (o *WorkflowOrchestrator) executeWorkflowHandlerWithRetryAfter(ctx context.Context, run *state.WorkflowRun, path, method string, headers map[string]string, body []byte, timeout time.Duration, operationID string, generation int64) (int, []byte, time.Time, error) {
	if executor, ok := o.executor.(WorkflowRetryAfterExecutor); ok {
		return executor.ExecuteWorkflowStepWithRetryAfter(ctx, run.AppID, WorkflowStepIdentity{RunID: run.ID, PlatformTenantID: run.PlatformTenantID}, path, method, headers, body, timeout, operationID, generation)
	}
	status, body, err := o.executeWorkflowHandler(ctx, run, path, method, headers, body, timeout, operationID, generation)
	return status, body, time.Time{}, err
}

func (o *WorkflowOrchestrator) executeWorkflowHandler(ctx context.Context, run *state.WorkflowRun, path, method string, headers map[string]string, body []byte, timeout time.Duration, managedOperationID string, generation int64) (int, []byte, error) {
	if executor, ok := o.executor.(WorkflowIdentityExecutor); ok {
		return executor.ExecuteWorkflowStep(ctx, run.AppID, WorkflowStepIdentity{RunID: run.ID, PlatformTenantID: run.PlatformTenantID}, path, method, headers, body, timeout, managedOperationID, generation)
	}
	if run.PlatformTenantID != "" {
		return 0, nil, errors.New("workflow executor cannot carry platform tenant identity")
	}
	if managedOperationID != "" {
		executor, ok := o.executor.(WorkflowManagedOperationExecutor)
		if !ok {
			return 0, nil, errors.New("managed workflow operation transport is unavailable")
		}
		return executor.ExecuteManagedOperationStep(ctx, run.AppID, path, method, headers, body, timeout, managedOperationID, generation)
	}
	return o.executor.ExecuteStep(ctx, run.AppID, path, method, headers, body, timeout)
}

// workflowFinalOutput uses completion order, never insertion order. Among
// equally recent outputs, discard ancestors before the stable name tie-break:
// a pairwise dependency/name comparator is not transitive for parallel DAGs.
func workflowFinalOutput(steps []*state.WorkflowStep, specs map[string]api.WorkflowStepSpec) json.RawMessage {
	var candidates []*state.WorkflowStep
	var latest *time.Time
	for _, step := range steps {
		if step.ForEachParent != nil {
			continue
		}
		if step.Status != state.WorkflowStepStatusSucceeded || len(step.Output) == 0 {
			continue
		}
		if step.FinishedAt != nil && (latest == nil || step.FinishedAt.After(*latest)) {
			latest = step.FinishedAt
			candidates = nil
		}
		if latest == nil || (step.FinishedAt != nil && step.FinishedAt.Equal(*latest)) {
			candidates = append(candidates, step)
		}
	}
	var chosen *state.WorkflowStep
	for _, candidate := range candidates {
		ancestor := false
		for _, other := range candidates {
			if candidate != other && workflowOutputDependsOn(other.StepName, candidate.StepName, specs) {
				ancestor = true
				break
			}
		}
		if !ancestor && (chosen == nil || candidate.StepName > chosen.StepName) {
			chosen = candidate
		}
	}
	if chosen == nil {
		return nil
	}
	return chosen.Output
}

func workflowOutputDependsOn(step, ancestor string, specs map[string]api.WorkflowStepSpec) bool {
	pending := []string{step}
	seen := make(map[string]bool, len(specs))
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[name] {
			continue
		}
		seen[name] = true
		dependencies := append([]string(nil), specs[name].DependsOn...)
		for source, spec := range specs {
			if spec.OnTimeout == name || spec.OnFailure == name {
				dependencies = append(dependencies, source)
			}
		}
		for _, dependency := range dependencies {
			if dependency == ancestor {
				return true
			}
			pending = append(pending, dependency)
		}
	}
	return false
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

// workflowFailureHandlerDecision holds an on_failure target until its source
// either fails terminally or succeeds. A source that succeeds (or is skipped)
// makes the failure-only handler ineligible.
func workflowFailureHandlerDecision(stepName string, specs []api.WorkflowStepSpec, steps map[string]*state.WorkflowStep) (blocked, skip bool) {
	referenced := false
	waiting := false
	triggered := false
	for _, spec := range specs {
		if spec.OnFailure != stepName {
			continue
		}
		referenced = true
		source, ok := steps[spec.Name]
		if !ok {
			waiting = true
			continue
		}
		switch source.Status {
		case state.WorkflowStepStatusFailed, state.WorkflowStepStatusDead:
			triggered = true
		case state.WorkflowStepStatusSucceeded, state.WorkflowStepStatusSkipped:
			// A successful or skipped source cannot trigger recovery.
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

func workflowFailureHandlerPending(specs []api.WorkflowStepSpec, steps map[string]*state.WorkflowStep) bool {
	for _, sourceSpec := range specs {
		if sourceSpec.OnFailure == "" {
			continue
		}
		source, sourceExists := steps[sourceSpec.Name]
		if !sourceExists || (source.Status != state.WorkflowStepStatusFailed && source.Status != state.WorkflowStepStatusDead) {
			continue
		}
		handler, handlerExists := steps[sourceSpec.OnFailure]
		if handlerExists && (handler.Status == state.WorkflowStepStatusPending || handler.Status == state.WorkflowStepStatusRunning) {
			return true
		}
	}
	return false
}

func workflowFailureError(specs []api.WorkflowStepSpec, steps map[string]*state.WorkflowStep) *string {
	// A handler may appear before its source in manifest order. Prefer the
	// routed source error so a failed compensation does not hide why it ran.
	for _, spec := range specs {
		if spec.OnFailure == "" {
			continue
		}
		step, ok := steps[spec.Name]
		if ok && (step.Status == state.WorkflowStepStatusFailed || step.Status == state.WorkflowStepStatusDead) && step.Error != nil {
			message := *step.Error
			return &message
		}
	}
	for _, spec := range specs {
		step, ok := steps[spec.Name]
		if ok && (step.Status == state.WorkflowStepStatusFailed || step.Status == state.WorkflowStepStatusDead) && step.Error != nil {
			message := *step.Error
			return &message
		}
	}
	return nil
}

func workflowFailureContextForHandler(handlerName string, specs []api.WorkflowStepSpec, steps map[string]*state.WorkflowStep) (json.RawMessage, error) {
	for _, sourceSpec := range specs {
		if sourceSpec.OnFailure != handlerName {
			continue
		}
		source, ok := steps[sourceSpec.Name]
		if !ok || (source.Status != state.WorkflowStepStatusFailed && source.Status != state.WorkflowStepStatusDead) {
			continue
		}
		message := ""
		if source.Error != nil {
			message = *source.Error
		}
		return json.Marshal(struct {
			Step    string `json:"step"`
			Status  string `json:"status"`
			Attempt int    `json:"attempt"`
			Message string `json:"message"`
		}{Step: source.StepName, Status: source.Status, Attempt: source.Attempt, Message: message})
	}
	return nil, nil
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
		ctx = state.WithWorkflowRunGeneration(ctx, claimed.ID, claimed.ResumeCount)
		if err := o.initAndAdvanceRun(ctx, claimed); err != nil {
			if errors.Is(err, state.ErrWorkflowOutboundAttemptExpired) || errors.Is(err, state.ErrWorkflowGuardNotReady) {
				return nil
			}
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
	ctx = state.WithWorkflowRunGeneration(ctx, runID, run.ResumeCount)
	// Iteration can create many item transitions. Release each pass's step and
	// output snapshots before refreshing rather than retaining recursive frames.
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		advanced, err := o.advanceWorkflowRunOnce(ctx, runID)
		if errors.Is(err, state.ErrWorkflowActionConcurrencyLimit) {
			return o.store.ScheduleWorkflowRun(ctx, runID, state.WorkflowRunStatusPending, time.Now().UTC().Add(workflowActionCapacityPollInterval))
		}
		if err != nil || !advanced {
			return err
		}
	}
}

func (o *WorkflowOrchestrator) advanceWorkflowRunOnce(ctx context.Context, runID string) (bool, error) {
	run, err := o.store.GetWorkflowRun(ctx, runID)
	if err != nil {
		return false, err
	}

	if !state.WorkflowRunGenerationMatches(ctx, runID, run.ResumeCount) {
		return false, state.ErrWorkflowOutboundAttemptExpired
	}
	// Terminal states do not advance
	if run.Status == state.WorkflowRunStatusSucceeded ||
		run.Status == state.WorkflowRunStatusFailed ||
		run.Status == state.WorkflowRunStatusDead {
		return false, nil
	}

	var spec api.WorkflowSpec
	if err := json.Unmarshal(run.DefinitionSnapshot, &spec); err != nil {
		return false, err
	}

	steps, err := o.store.GetWorkflowSteps(ctx, runID)
	if err != nil {
		return false, err
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
		if s.ForEachParent != nil {
			continue
		}
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

	failureHandlerPending := workflowFailureHandlerPending(spec.Steps, stepMap)
	failureMessage := workflowFailureError(spec.Steps, stepMap)
	if hasDead && !failureHandlerPending {
		if err := o.store.MarkWorkflowRunStatus(ctx, runID, state.WorkflowRunStatusDead, nil, failureMessage); err != nil {
			return false, err
		}
		if o.metrics != nil {
			o.metrics.ObserveRunComplete(run.AppID, "unknown", "dead", time.Since(run.CreatedAt))
		}
		return false, nil
	}

	if hasFailed && !failureHandlerPending {
		if err := o.store.MarkWorkflowRunStatus(ctx, runID, state.WorkflowRunStatusFailed, nil, failureMessage); err != nil {
			return false, err
		}
		if o.metrics != nil {
			o.metrics.ObserveRunComplete(run.AppID, "unknown", "failed", time.Since(run.CreatedAt))
		}
		return false, nil
	}

	if allSucceeded && len(steps) > 0 {
		lastOutput := workflowFinalOutput(steps, specStepMap)
		if err := o.store.MarkWorkflowRunStatus(ctx, runID, state.WorkflowRunStatusSucceeded, lastOutput, nil); err != nil {
			return false, err
		}
		if o.metrics != nil {
			o.metrics.ObserveRunComplete(run.AppID, "unknown", "succeeded", time.Since(run.CreatedAt))
		}
		runAuditOutput := string(lastOutput)
		if workflowHasOutbound(spec.Steps) {
			runAuditOutput = ""
		}
		o.emitAudit(ctx, events.WorkflowSucceeded, map[string]any{
			"run_id":        run.ID,
			"app_id":        run.AppID,
			"workflow_name": run.WorkflowName,
			"outcome":       "succeeded",
			"output":        runAuditOutput,
		})
		return false, nil
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
		adv, err := o.executeStep(ctx, run, s, stepSpec, stepMap, spec.Steps)
		if err != nil {
			if o.log != nil {
				o.log.Warn("workflow orchestrator: resume wait error", "run_id", runID, "step", s.StepName, "err", err)
			}
			return false, err
		}
		advancedAny = advancedAny || adv
	}

	// Find runnable steps: status is pending and all depends_on are succeeded
	for _, s := range steps {
		if s.Status != state.WorkflowStepStatusPending {
			continue
		}
		if s.NextRetryAt != nil && time.Now().UTC().Before(*s.NextRetryAt) {
			continue
		}

		stepSpec, exists := specStepMap[s.StepName]
		if !exists {
			continue
		}
		if stepSpec.Join != nil {
			ready, err := o.store.ResolveWorkflowStepJoin(ctx, runID, s.StepName)
			if errors.Is(err, state.ErrWorkflowJoinEvaluation) {
				message := state.ErrWorkflowJoinEvaluation.Error()
				if markErr := o.store.MarkWorkflowStepStatus(ctx, runID, s.StepName, state.WorkflowStepStatusDead, s.Attempt, nil, &message); markErr != nil {
					return false, markErr
				}
				advancedAny = true
				continue
			}
			if err != nil {
				return false, err
			}
			advancedAny = advancedAny || ready
			continue
		}
		timeoutBlocked, timeoutSkip := workflowTimeoutHandlerDecision(s.StepName, spec.Steps, stepMap)
		failureBlocked, failureSkip := workflowFailureHandlerDecision(s.StepName, spec.Steps, stepMap)
		if timeoutSkip || failureSkip {
			if err := o.store.SkipWorkflowStep(ctx, runID, s.StepName, state.WorkflowSkipRouteNotTaken); err != nil {
				return false, err
			}
			s.Status = state.WorkflowStepStatusSkipped
			advancedAny = true
			continue
		}
		if timeoutBlocked || failureBlocked {
			continue
		}

		depsMet := true
		depFailed := false
		depSkipped := false
		for _, dep := range stepSpec.DependsOn {
			depStep, ok := stepMap[dep]
			if ok && depStep.Status == state.WorkflowStepStatusSkipped {
				depSkipped = true
			}
			failureRouteDependency := ok && (depStep.Status == state.WorkflowStepStatusFailed || depStep.Status == state.WorkflowStepStatusDead) && specStepMap[dep].OnFailure == s.StepName
			if !ok || (depStep.Status != state.WorkflowStepStatusSucceeded && !failureRouteDependency) {
				depsMet = false
			}
			if ok && (depStep.Status == state.WorkflowStepStatusFailed || depStep.Status == state.WorkflowStepStatusDead) && !failureRouteDependency {
				depFailed = true
			}
		}

		if depFailed || depSkipped {
			reason := state.WorkflowSkipDependencyFailed
			if depSkipped {
				reason = state.WorkflowSkipDependencySkipped
			}
			// Close the inactive branch instead of leaving its descendants pending.
			if err := o.store.SkipWorkflowStep(ctx, runID, s.StepName, reason); err != nil {
				return false, err
			}
			s.Status = state.WorkflowStepStatusSkipped
			advancedAny = true
			continue
		}

		if !depsMet {
			continue
		}

		if stepSpec.When != nil {
			matched, err := o.store.ResolveWorkflowStepGuard(ctx, runID, s.StepName)
			if errors.Is(err, state.ErrWorkflowGuardEvaluation) {
				message := state.ErrWorkflowGuardEvaluation.Error()
				if markErr := o.store.MarkWorkflowStepStatus(ctx, runID, s.StepName, state.WorkflowStepStatusDead, s.Attempt, nil, &message); markErr != nil {
					return false, markErr
				}
				s.Status = state.WorkflowStepStatusDead
				advancedAny = true
				continue
			}
			if err != nil {
				return false, err
			}
			if !matched {
				s.Status = state.WorkflowStepStatusSkipped
				advancedAny = true
				continue
			}
		}
		// Step is ready to execute!
		adv, err := o.executeStep(ctx, run, s, stepSpec, stepMap, spec.Steps)
		if err != nil {
			if o.log != nil {
				o.log.Warn("workflow orchestrator: execute step error", "run_id", runID, "step", s.StepName, "err", err)
			}
			return false, err
		}
		if adv {
			advancedAny = true
		}
	}

	if advancedAny {
		// Refresh state to see if downstream steps are unlocked.
		return true, nil
	}
	// Reconcile all parked waits and retry deadlines after this pass. This also
	// corrects the run wake after an unrelated event caused an early dispatch.
	if err := o.reconcileWorkflowWake(ctx, runID, spec); err != nil {
		return false, err
	}

	return false, nil
}

func (o *WorkflowOrchestrator) reconcileWorkflowWake(ctx context.Context, runID string, spec api.WorkflowSpec) error {
	steps, err := o.store.GetWorkflowSteps(ctx, runID)
	if err != nil {
		return err
	}
	byName := make(map[string]api.WorkflowStepSpec, len(spec.Steps))
	for _, item := range spec.Steps {
		byName[item.Name] = item
	}
	var earliest time.Time
	nextStatus := ""
	consider := func(due time.Time, status string) {
		if due.IsZero() {
			return
		}
		if earliest.IsZero() || due.Before(earliest) || (due.Equal(earliest) && status == state.WorkflowRunStatusPending) {
			earliest = due
			nextStatus = status
		}
	}
	for _, step := range steps {
		if step.Status == state.WorkflowStepStatusPending && step.NextRetryAt != nil {
			consider(*step.NextRetryAt, state.WorkflowRunStatusPending)
		}
		if step.Status != state.WorkflowStepStatusAwaitingEvent || step.StartedAt == nil {
			continue
		}
		item := byName[step.StepName]
		var due time.Time
		switch {
		case item.WaitForCondition != nil && step.NextCheckAt != nil:
			due = *step.NextCheckAt
		case item.WaitForDuration > 0:
			due = step.StartedAt.Add(item.WaitForDuration)
		case item.WaitForEvent != "" || item.WaitForCallback:
			due = step.StartedAt.Add(item.Timeout)
		}
		consider(due, state.WorkflowRunStatusAwaitingEvent)
	}
	if !earliest.IsZero() {
		return o.store.SetWorkflowRunWake(ctx, runID, nextStatus, earliest)
	}
	return nil
}

func workflowStepInput(run *state.WorkflowRun, step *state.WorkflowStep, spec api.WorkflowStepSpec, steps map[string]*state.WorkflowStep, failureContext json.RawMessage) ([]byte, error) {
	if step.ForEachParent != nil {
		return append([]byte(nil), step.Input...), nil
	}
	if step.RetryBase > 0 {
		return append([]byte(nil), step.Input...), nil
	}
	if step.Attempt > 0 && len(step.Input) > 0 {
		if len(spec.Input) == 0 || !workflowJSONEqual(step.Input, run.Input) {
			return append([]byte(nil), step.Input...), nil
		}
		// An older scheduler persisted run.Input on every step. Re-render only
		// that recognizable legacy case; inputs persisted by this version are
		// returned above and never re-evaluated on retry.
	}
	if len(spec.Input) == 0 && len(failureContext) > 0 {
		return json.Marshal(struct {
			Input   json.RawMessage `json:"input"`
			Failure json.RawMessage `json:"failure"`
		}{Input: run.Input, Failure: failureContext})
	}
	dependencyOutputs := make(map[string]json.RawMessage, len(spec.DependsOn))
	for _, dependency := range spec.DependsOn {
		if completed, ok := steps[dependency]; ok && completed.Status == state.WorkflowStepStatusSucceeded {
			dependencyOutputs[dependency] = completed.Output
		}
	}
	resolved, err := api.ResolveWorkflowStepInput(spec.Input, run.Input, dependencyOutputs, failureContext)
	if err != nil {
		return nil, err
	}

	// In the legacy retry case, retain the stored payload if it already
	// matches the rendered template; otherwise replace the old run input.
	if step.Attempt > 0 && len(step.Input) > 0 && workflowJSONEqual(step.Input, resolved) {
		return append([]byte(nil), step.Input...), nil
	}
	return resolved, nil
}

func workflowOutboundTemplateContext(run *state.WorkflowRun, step *state.WorkflowStep, spec api.WorkflowStepSpec, steps map[string]*state.WorkflowStep) (json.RawMessage, map[string]json.RawMessage, error) {
	contextInput := run.Input
	dependencyNames := spec.DependsOn
	if step.ForEachParent != nil {
		parentSpec := api.WorkflowRuntimeStep(run.DefinitionSnapshot, *step.ForEachParent)
		parent, exists := steps[*step.ForEachParent]
		if parentSpec == nil || parentSpec.ForEach == nil || !exists || step.ForEachIndex == nil {
			return nil, nil, errors.New("for_each outbound context is incomplete")
		}
		var items []json.RawMessage
		if err := json.Unmarshal(parent.Input, &items); err != nil || *step.ForEachIndex < 0 || *step.ForEachIndex >= len(items) {
			return nil, nil, errors.New("for_each outbound item is unavailable")
		}
		var err error
		contextInput, err = json.Marshal(struct {
			Item  json.RawMessage `json:"item"`
			Index int             `json:"index"`
			Input json.RawMessage `json:"input"`
		}{Item: items[*step.ForEachIndex], Index: *step.ForEachIndex, Input: run.Input})
		if err != nil {
			return nil, nil, err
		}
		dependencyNames = parentSpec.DependsOn
	}
	outputs := make(map[string]json.RawMessage, len(dependencyNames))
	for _, name := range dependencyNames {
		dependency, exists := steps[name]
		if !exists || dependency.Status != state.WorkflowStepStatusSucceeded {
			return nil, nil, fmt.Errorf("workflow outbound dependency %q is not complete", name)
		}
		outputs[name] = dependency.Output
	}
	return contextInput, outputs, nil
}

func workflowJSONEqual(left, right []byte) bool {
	canonical := func(raw []byte) ([]byte, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	canonicalLeft, err := canonical(left)
	if err != nil {
		return false
	}
	canonicalRight, err := canonical(right)
	if err != nil {
		return false
	}
	return bytes.Equal(canonicalLeft, canonicalRight)
}

func (o *WorkflowOrchestrator) failWorkflowStepInput(ctx context.Context, run *state.WorkflowRun, step *state.WorkflowStep, spec api.WorkflowStepSpec, failureContext json.RawMessage, cause error) (bool, error) {
	return o.failWorkflowStepBeforeStart(ctx, run, step, spec, failureContext, "invalid step input: "+cause.Error())
}
func (o *WorkflowOrchestrator) failWorkflowStepBeforeStart(ctx context.Context, run *state.WorkflowRun, step *state.WorkflowStep, spec api.WorkflowStepSpec, failureContext json.RawMessage, errMsg string) (bool, error) {
	if err := o.store.MarkWorkflowStepStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusFailed, step.Attempt, nil, &errMsg); err != nil {
		return false, err
	}
	if spec.OnFailure == "" && len(failureContext) == 0 && step.ForEachParent == nil {
		if err := o.store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusFailed, nil, &errMsg); err != nil {
			return false, err
		}
	}
	if o.metrics != nil {
		o.metrics.ObserveStepComplete(run.AppID, "unknown", step.StepName, state.WorkflowStepStatusFailed, 0)
	}
	o.emitAudit(ctx, events.WorkflowStepFailed, map[string]any{
		"run_id": run.ID, "app_id": run.AppID,
		"workflow_name": run.WorkflowName, "step_name": step.StepName,
		"attempt": step.Attempt, "status": state.WorkflowStepStatusFailed,
		"error": errMsg,
	})
	return true, nil
}

func (o *WorkflowOrchestrator) executeStep(ctx context.Context, run *state.WorkflowRun, step *state.WorkflowStep, spec api.WorkflowStepSpec, steps map[string]*state.WorkflowStep, workflowSpecs []api.WorkflowStepSpec) (bool, error) {
	if spec.ForEach != nil {
		return o.executeForEach(ctx, run, step, spec, workflowSpecs)
	}
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
			auditOutput := string(evt.Payload)
			if workflowHasOutbound(workflowSpecs) {
				auditOutput = ""
			}
			o.emitAudit(ctx, events.WorkflowStepSucceeded, map[string]any{
				"run_id":        run.ID,
				"app_id":        run.AppID,
				"workflow_name": run.WorkflowName,
				"step_name":     step.StepName,
				"status":        state.WorkflowStepStatusSucceeded,
				"output":        auditOutput,
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
	if o.executor == nil && spec.Outbound == nil {
		return false, errors.New("no workflow step executor configured")
	}

	failureContext, err := workflowFailureContextForHandler(step.StepName, workflowSpecs, steps)
	if err != nil {
		return false, err
	}
	inputBytes, err := workflowStepInput(run, step, spec, steps, failureContext)
	if err != nil {
		return o.failWorkflowStepInput(ctx, run, step, spec, failureContext, err)
	}
	var outboundSpec api.WorkflowOutboundSpec
	if spec.Outbound != nil {
		templateInput, dependencyOutputs, contextErr := workflowOutboundTemplateContext(run, step, spec, steps)
		if contextErr != nil {
			return o.failWorkflowStepInput(ctx, run, step, spec, failureContext, contextErr)
		}
		outboundSpec, err = api.ResolveWorkflowOutboundTarget(*spec.Outbound, templateInput, dependencyOutputs, failureContext)
		if err != nil {
			return o.failWorkflowStepInput(ctx, run, step, spec, failureContext, err)
		}
	}

	if spec.Outbound != nil {
		if o.outbound == nil {
			return o.failWorkflowStepBeforeStart(ctx, run, step, spec, failureContext, "workflow outbound executor is disabled or unavailable")
		}
		if step.Attempt-step.RetryBase >= api.WorkflowStepMaxAttempts(spec) {
			return o.failWorkflowStepBeforeStart(ctx, run, step, spec, failureContext, "workflow outbound attempt limit exhausted after recovery")
		}
	}
	if (step.ForEachParent != nil || run.ResumeCount > 0) && step.Attempt-step.RetryBase >= api.WorkflowStepMaxAttempts(spec) {
		return o.failWorkflowStepBeforeStart(ctx, run, step, spec, nil, "for_each item attempt limit exhausted after recovery")
	}
	start := time.Now()
	persistedInput, err := o.store.StartWorkflowStep(ctx, run.ID, step.StepName, step.Attempt+1, inputBytes)
	if err != nil {
		return false, err
	}
	inputBytes = persistedInput
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
		"Idempotency-Key":         workflowStepIdempotencyKey(run.ID, step.StepName),
		"Content-Type":            "application/json",
	}

	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if leaseStore, ok := o.store.(state.WorkflowRunLeaseStore); ok {
		if err := leaseStore.ExtendWorkflowRunLease(ctx, run.ID, timeout); err != nil {
			return false, err
		}
	}

	var statusCode int
	var body []byte
	var outboundRetryAt time.Time
	managedOperationID := ""
	var managedEffects []exclusivework.Effect
	if spec.Outbound != nil {
		statusCode, body, outboundRetryAt, err = o.outbound.ExecuteOutboundStep(ctx, run.ID, step.StepName, step.Attempt+1, outboundSpec, inputBytes, timeout)
		if errors.Is(err, state.ErrWorkflowOutboundAttemptExpired) {
			return false, err
		}
	} else {
		execCtx := ctx
		if step.ForEachParent != nil || run.ResumeCount > 0 {
			var release func()
			execCtx, release = o.workflowItemContext(ctx, run.ID, step.StepName, step.Attempt+1)
			defer release()
		}
		if spec.ManagedOperation {
			managedOperationID, err = api.ManagedWorkflowStepOperationID(run.ID, step.StepName)
		}
		if err == nil {
			generation := int64(0)
			if managedOperationID != "" {
				generation = int64(step.Attempt + 1)
			}
			statusCode, body, outboundRetryAt, err = o.executeWorkflowHandlerWithRetryAfter(execCtx, run, workflowStepPath(spec), method, headers, inputBytes, timeout, managedOperationID, generation)
		}
	}
	auditOutput := string(body)
	if workflowHasOutbound(workflowSpecs) {
		auditOutput = ""
	}
	duration := time.Since(start)

	if err == nil && statusCode >= 200 && statusCode < 300 {
		if spec.ManagedOperation {
			var envelope map[string]json.RawMessage
			if json.Unmarshal(body, &envelope) != nil || envelope["gregale_operation_result"] == nil {
				err = errors.New("managed workflow step must return a negotiated operation result")
			} else {
				result, effects, decodeErr := decodeOperationResult(body)
				switch {
				case decodeErr != nil:
					err = errors.New("managed workflow step returned an invalid operation result")
				default:
					body = result
					managedEffects = effects
				}
			}
			if err != nil {
				statusCode = 500
				body = nil
			}
		}
	}

	if err == nil && statusCode >= 200 && statusCode < 300 {
		// Bound item outputs before persisting them as a collected for_each result.
		if step.ForEachParent != nil {
			if int64(len(body)) > api.WorkflowForEachMaxOutputBytes {
				message := state.ErrWorkflowForEachOutputLimit.Error()
				return true, o.store.MarkWorkflowStepAttemptStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusFailed, step.Attempt+1, workflowHTTPStatus(statusCode, nil), nil, &message)
			}
			if len(body) > 0 && !json.Valid(body) {
				body, _ = json.Marshal(string(body))
			}
		}
		if !workflowHasOutbound(workflowSpecs) {
			auditOutput = string(body)
		}
		if spec.ManagedOperation {
			committer, ok := o.store.(state.ManagedWorkflowStepCommitter)
			if !ok {
				err = errors.New("managed workflow result store is unavailable")
				statusCode = 500
			} else {
				commitErr := committer.CommitManagedWorkflowStep(ctx, state.ManagedWorkflowStepCommit{
					RunID: run.ID, StepName: step.StepName, OperationID: managedOperationID,
					Attempt: step.Attempt + 1, HTTPStatus: statusCode, Output: body, Effects: managedEffects,
				})
				switch {
				case commitErr == nil:
					// The result, delivery rows, step, and attempt now share one commit.
				case errors.Is(commitErr, state.ErrInvalidArgument), errors.Is(commitErr, state.ErrOperationEffectDestination):
					statusCode = 409
					body = []byte("managed workflow operation result or effect destination is invalid")
				case errors.Is(commitErr, state.ErrConflict), errors.Is(commitErr, state.ErrWorkflowNotRunning),
					errors.Is(commitErr, state.ErrWorkflowRunNotFound), errors.Is(commitErr, state.ErrWorkflowStepNotFound),
					errors.Is(commitErr, state.ErrWorkflowAttemptNotFound):
					return false, commitErr
				default:
					if o.log != nil {
						o.log.WarnContext(ctx, "managed workflow result persistence failed", "run_id", run.ID, "step", step.StepName, "err", commitErr)
					}
					err = errors.New("managed workflow result persistence failed")
					statusCode = 500
					body = nil
				}
			}
		} else if markErr := o.store.MarkWorkflowStepAttemptStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusSucceeded, step.Attempt+1, workflowHTTPStatus(statusCode, err), body, nil); markErr != nil {
			if errors.Is(markErr, state.ErrWorkflowForEachOutputLimit) {
				message := markErr.Error()
				return true, o.store.MarkWorkflowStepAttemptStatus(ctx, run.ID, step.StepName, state.WorkflowStepStatusFailed, step.Attempt+1, workflowHTTPStatus(statusCode, nil), nil, &message)
			}
			return false, markErr
		}
	}

	if err == nil && statusCode >= 200 && statusCode < 300 {
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
			"output":        auditOutput,
		})
		return true, nil
	}

	// Handle failures using the policy shared with the automation simulator.
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	} else {
		errMsg = fmt.Sprintf("HTTP %d: %s", statusCode, string(body))
	}

	if step.ForEachParent != nil {
		errMsg = fmt.Sprintf("Item HTTP %d", statusCode)
		if err != nil {
			errMsg = "for_each item request failed"
		}
	}
	if spec.Outbound != nil {
		errMsg = fmt.Sprintf("Outbound HTTP %d", statusCode)
		if err != nil {
			errMsg = "outbound integration request failed"
			if !spec.Outbound.SafeToRepeat() {
				errMsg = "outbound result unknown; unsafe to repeat"
			}
		}
	}
	attemptNumber := step.Attempt + 1 - step.RetryBase
	retryDecision := api.EvaluateWorkflowRetry(spec, statusCode, err != nil, attemptNumber)
	if retryDecision.ShouldRetry {
		// Retry eligible — schedule the next attempt after the configured
		// backoff so a failing dependency cannot hot-loop the dispatcher.
		retryAt := time.Now().UTC().Add(workflowRetryDelay(spec, step.Attempt+1-step.RetryBase))
		if outboundRetryAt.After(retryAt) {
			retryAt = outboundRetryAt
		}
		if err := o.store.ScheduleWorkflowStepRetryWithHTTPStatus(ctx, run.ID, step.StepName, step.Attempt+1, retryAt, workflowHTTPStatus(statusCode, err), errMsg); err != nil {
			return false, err
		}
		return false, nil
	}

	// Failure is terminal for this step
	finalStatus := state.WorkflowStepStatusFailed
	if retryDecision.IsDead {
		finalStatus = state.WorkflowStepStatusDead
	}

	if err := o.store.MarkWorkflowStepAttemptStatus(ctx, run.ID, step.StepName, finalStatus, step.Attempt+1, workflowHTTPStatus(statusCode, err), nil, &errMsg); err != nil {
		return false, err
	}
	if spec.OnFailure == "" && len(failureContext) == 0 && step.ForEachParent == nil {
		if err := o.store.MarkWorkflowRunStatus(ctx, run.ID, finalStatus, nil, &errMsg); err != nil {
			return false, err
		}
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
	input := step.Input
	if step.Attempt > 0 && len(step.Output) > 0 {
		input = step.Output
	} else if len(input) == 0 {
		input = run.Input
	}
	persistedInput, err := o.store.StartWorkflowStep(ctx, run.ID, step.StepName, attempt, input)
	if err != nil {
		return false, err
	}
	input = persistedInput
	headers := map[string]string{
		"X-Faas-Internal-Wake": "workflow", "X-Faas-Workflow-Run-Id": run.ID,
		"X-Faas-Workflow-Step": step.StepName, "X-Faas-Workflow-Attempt": fmt.Sprintf("%d", attempt),
		"Idempotency-Key": fmt.Sprintf("workflow/%s/%s/%d", run.ID, step.StepName, attempt),
		"Content-Type":    "application/json",
	}
	statusCode, body, callErr := o.executeWorkflowHandler(ctx, run, "/"+condition.Run, "POST", headers, input, 30*time.Second, "", 0)
	update.Checked = true
	update.HTTPStatus = workflowHTTPStatus(statusCode, callErr)
	if callErr == nil && statusCode >= 200 && statusCode < 300 {
		var response struct {
			Done *bool `json:"done"`
		}
		if err := json.Unmarshal(body, &response); err != nil || response.Done == nil {
			return o.failConditionCheck(ctx, run, step, attempt, "condition checker must return a JSON object with boolean done", state.WorkflowStepStatusDead, update.HTTPStatus)
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
		return o.failConditionCheck(ctx, run, step, attempt, fmt.Sprintf("condition checker HTTP %d", statusCode), state.WorkflowStepStatusFailed, update.HTTPStatus)
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

func (o *WorkflowOrchestrator) failConditionCheck(ctx context.Context, run *state.WorkflowRun, step *state.WorkflowStep, attempt int, message, status string, httpStatus *int) (bool, error) {
	if err := o.store.MarkWorkflowStepAttemptStatus(ctx, run.ID, step.StepName, status, attempt, httpStatus, nil, &message); err != nil {
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
