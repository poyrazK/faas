package runpipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/executionworkflow"
	"github.com/onebox-faas/faas/pkg/state"
)

const FatalPrefix = "pipeline-fatal: "

type PermanentError string

func (e PermanentError) Error() string { return string(e) }

type Worker struct {
	Jobs         state.ExecutionPipelineStore
	Runs         state.ExecutionStore
	WorkflowRuns state.ExecutionWorkflowStore
	Open         func(context.Context, state.ExecutionWorkflowJob) (Plan, error)
	Admit        func(context.Context, state.ExecutionWorkflowJobClaim, state.ExecutionPipeline, int, int, string, api.CreateExecutionRequest, []runfiles.AgentFilePin) error
	Enabled      func() bool
	Now          func() time.Time
}
type observed struct {
	run       state.Execution
	attempt   int
	retry     bool
	blocked   bool
	invalid   bool
	validated bool
	reason    string
	reused    bool
}

func (o observed) settled() bool {
	return o.blocked || o.attempt > 0 && !o.retry && o.run.Status.Terminal()
}
func (o observed) success() bool {
	return o.attempt > 0 && !o.invalid && o.run.Status == api.ExecutionStatusSucceeded
}
func (w Worker) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}
func (w Worker) update(ctx context.Context, c state.ExecutionWorkflowJobClaim, status api.ManagedExecutionWorkflowStatus, n int, detail string) error {
	return w.updateObserved(ctx, c, status, n, detail, nil)
}
func (w Worker) Drive(ctx context.Context, c state.ExecutionWorkflowJobClaim) error {
	meta, err := w.Jobs.ExecutionPipelineByID(ctx, c.ID)
	if err != nil {
		return err
	}
	if meta.CancelRequested || !w.now().Before(meta.DeadlineAt) {
		return w.stop(ctx, c, meta)
	}
	plan, err := w.Open(ctx, c.ExecutionWorkflowJob)
	if err != nil {
		return w.update(ctx, c, api.ManagedExecutionWorkflowQueued, c.NextStep, "waiting for encrypted pipeline identity")
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	policy, _ := json.Marshal(meta.Policy)
	sealedPolicy, _ := json.Marshal(plan.Policy)
	if hex.EncodeToString(digest[:12]) != c.PlanID || string(policy) != string(sealedPolicy) || len(plan.Request.Steps) != c.StepCount || meta.Policy.ScopedFileInputs != (len(plan.AgentFiles) > 0) || api.ValidateCreateManagedExecutionWorkflow(plan.Request) != nil {
		return w.update(ctx, c, api.ManagedExecutionWorkflowFailed, c.NextStep, "stored pipeline identity is invalid")
	}
	deps, err := api.ManagedExecutionWorkflowStepDependencies(plan.Request)
	if err != nil {
		return w.update(ctx, c, api.ManagedExecutionWorkflowFailed, c.NextStep, "stored pipeline dependencies are invalid")
	}
	obs := make([]observed, c.StepCount)
	active, started := 0, 0
	failed := false
	reuseUnavailable := false
	for i, s := range meta.Policy.Steps {
		if ref := reusedStep(meta.Policy.Rerun, s.Label); ref != nil {
			r, e := w.reusedRun(ctx, c.ExecutionWorkflowJob, *ref)
			if e != nil {
				obs[i] = observed{blocked: true, reason: "admission_refused"}
				reuseUnavailable = true
				failed = true
				continue
			}
			obs[i] = observed{run: r, attempt: 1, reused: true}
			continue
		}
		for attempt := 1; attempt <= s.Retry.MaxAttempts; attempt++ {
			r, err := w.WorkflowRuns.ExecutionWorkflowStepByLabel(ctx, c.AccountID, c.WorkflowID, c.RunsPrincipalID, Label(c.PlanID, i, attempt))
			if errors.Is(err, state.ErrNotFound) {
				break
			}
			if err != nil {
				return err
			}
			obs[i] = observed{run: r, attempt: attempt}
		}
		o := &obs[i]
		if o.attempt == 0 {
			continue
		}
		started++
		if !o.run.Status.Terminal() {
			active++
			continue
		}
		if o.run.Status == api.ExecutionStatusSucceeded {
			schema, err := executionworkflow.CompileResultSchema(plan.Request.Steps[i].ResultSchema)
			if err != nil {
				return err
			}
			o.validated = schema != nil
			o.invalid = executionworkflow.ValidateResult(o.run.Result, schema) != "" || o.run.ResultValidation != nil && o.run.ResultValidation.Status == "invalid"
		}
		o.retry = Retryable(o.run) && s.Retry.SafeToRepeat && o.attempt < s.Retry.MaxAttempts
		if !o.retry && !o.success() {
			failed = true
		}
	}
	stopping := meta.CancelRequested || !w.now().Before(meta.DeadlineAt)
	// Cancellation closes the admission fence before this worker sees it. Wait for
	// all admitted Runs to reach their existing terminal/usage settlement path.
	if stopping {
		for _, o := range obs {
			if o.attempt > 0 && !o.run.Status.Terminal() {
				if _, err := w.Runs.RequestExecutionCancellation(ctx, c.AccountID, o.run.ID, w.now()); err != nil {
					return err
				}
			}
		}
		detail := "pipeline deadline exceeded"
		if meta.CancelRequested {
			detail = "pipeline cancelled"
		}
		if active > 0 {
			return w.update(ctx, c, api.ManagedExecutionWorkflowQueued, started, detail)
		}
		return w.update(ctx, c, api.ManagedExecutionWorkflowFailed, started, detail)
	}
	hardFailure := reuseUnavailable || len(c.LastError) >= len(FatalPrefix) && c.LastError[:len(FatalPrefix)] == FatalPrefix
	continueIndependent := plan.Request.FailurePolicy == api.ExecutionWorkflowManagedFailureContinueIndependent
	stop := hardFailure || failed && !continueIndependent
	parallel := plan.Request.MaxParallelSteps
	if parallel == 0 {
		parallel = 1
	}
	for i, step := range plan.Request.Steps {
		o := &obs[i]
		if o.attempt > 0 && !o.retry {
			continue
		}
		ready, allTerminal := true, true
		for _, label := range deps[step.Label] {
			d := obs[index(plan.Request, label)]
			if !d.settled() {
				allTerminal = false
			}
			if !d.success() {
				ready = false
			}
		}
		if !ready && allTerminal && continueIndependent && step.IncludeDependencyResults {
			ready = true
			for _, input := range step.ArtifactInputs {
				if !obs[index(plan.Request, input.FromStep)].success() {
					ready = false
				}
			}
		}
		if !ready && allTerminal {
			o.blocked = true
			continue
		}
		if !ready {
			o.reason = "dependencies"
			continue
		}
		if stop {
			o.reason = "fail_fast"
			continue
		}
		if active >= parallel {
			o.reason = "parallel_limit"
			continue
		}
		if o.retry && w.now().Before(RetryAt(o.run, meta.Policy.Steps[i].Retry, o.attempt)) {
			o.reason = "retry_backoff"
			continue
		}
		if w.Enabled != nil && !w.Enabled() {
			o.reason = "admissions_disabled"
			continue
		}
		request, err := stepRequest(plan.Request, i, obs)
		if err == nil {
			err = w.Admit(ctx, c, meta, i, o.attempt+1, o.run.ID, request, plan.AgentFiles[step.Label])
		}
		if err != nil {
			if errors.Is(err, state.ErrExecutionWorkflowStepExists) || errors.Is(err, state.ErrExecutionPipelineStopped) {
				return w.updateObserved(ctx, c, api.ManagedExecutionWorkflowQueued, started, "waiting for admission reconciliation", obs)
			}
			var quota *state.ExecutionQuotaError
			var files *state.ExecutionFileQuotaError
			var budget *state.RunBudgetError
			var permanent PermanentError
			if errors.As(err, &budget) && budget.Metric == "concurrent" {
				o.reason = "budget_concurrency"
				continue
			}
			if errors.As(err, &permanent) || errors.As(err, &budget) || errors.Is(err, state.ErrRunAttributionDenied) || errors.Is(err, state.ErrExecutionTaskUnavailable) || errors.Is(err, state.ErrExecutionInvalid) || errors.Is(err, state.ErrExecutionsNotAllowed) || errors.Is(err, state.ErrExecutionOutboundIntegrationUnavailable) {
				o.reason = "admission_refused"
				o.blocked = true
				stop = true
				hardFailure = true
				break
			}
			if errors.As(err, &quota) || errors.As(err, &files) || errors.Is(err, state.ErrExecutionRuntimeUnpinned) {
				o.reason = "capacity"
				continue
			}
			return err
		}
		if o.attempt == 0 {
			started++
		}
		o.attempt++
		o.retry = false
		o.run = state.Execution{Status: api.ExecutionStatusQueued}
		active++
	}
	if hardFailure {
		if active > 0 {
			return w.updateObserved(ctx, c, api.ManagedExecutionWorkflowQueued, started, FatalPrefix+"step admission refused; inspect task availability, contracts and budgets", obs)
		}
		return w.updateObserved(ctx, c, api.ManagedExecutionWorkflowFailed, started, "step admission refused; inspect task availability, contracts and budgets", obs)
	}
	if active > 0 {
		return w.updateObserved(ctx, c, api.ManagedExecutionWorkflowQueued, started, "", obs)
	}
	if stop {
		return w.updateObserved(ctx, c, api.ManagedExecutionWorkflowFailed, started, "one or more steps failed", obs)
	}
	all, success := true, true
	for _, o := range obs {
		all = all && o.settled()
		success = success && o.success()
	}
	if all {
		status := api.ManagedExecutionWorkflowFailed
		if success {
			status = api.ManagedExecutionWorkflowSucceeded
		}
		return w.updateObserved(ctx, c, status, started, "", obs)
	}
	return w.updateObserved(ctx, c, api.ManagedExecutionWorkflowQueued, started, "", obs)
}
func index(p api.CreateManagedExecutionWorkflowRequest, label string) int {
	for i, s := range p.Steps {
		if s.Label == label {
			return i
		}
	}
	return -1
}
func stepRequest(plan api.CreateManagedExecutionWorkflowRequest, i int, obs []observed) (api.CreateExecutionRequest, error) {
	step := plan.Steps[i]
	r := step.Request
	if step.InputFromPreviousResult {
		if !obs[i-1].success() || !json.Valid(obs[i-1].run.Result) {
			return r, PermanentError("dependency result unavailable")
		}
		r.Input = append(json.RawMessage(nil), obs[i-1].run.Result...)
	}
	if step.IncludeDependencyResults {
		deps, err := api.ManagedExecutionWorkflowStepDependencies(plan)
		if err != nil {
			return r, err
		}
		values := map[string]map[string]any{}
		for _, label := range deps[step.Label] {
			o := obs[index(plan, label)]
			status := string(o.run.Status)
			if o.blocked {
				status = "blocked"
			}
			if o.invalid {
				status = "contract_failed"
			}
			v := map[string]any{"status": status}
			if o.success() && json.Valid(o.run.Result) {
				v["result"] = json.RawMessage(o.run.Result)
			}
			values[label] = v
		}
		r.Input, err = json.Marshal(values)
		if err != nil {
			return r, err
		}
	}
	for _, input := range step.ArtifactInputs {
		o := obs[index(plan, input.FromStep)]
		if !o.success() {
			return r, PermanentError("artifact dependency failed")
		}
		found := false
		for _, a := range o.run.Artifacts {
			if a.Name != input.Name {
				continue
			}
			found = true
			if err := api.ValidateExecutionArtifacts([]api.ExecutionArtifact{a}); err != nil {
				return r, PermanentError("artifact integrity invalid")
			}
			if a.FileID != "" {
				r.InputFiles = append(r.InputFiles, api.ExecutionFileInput{FileID: a.FileID, Path: input.Path})
			} else {
				r.Files = append(r.Files, api.ExecutionFile{Path: input.Path, Content: append([]byte(nil), a.Content...)})
			}
			break
		}
		if !found {
			return r, PermanentError(fmt.Sprintf("artifact %q missing", input.Name))
		}
	}
	return r, nil
}

// stop needs only non-secret policy metadata, so cancellation and deadlines
// remain enforceable even when the plan decryption identity is unavailable.
func (w Worker) stop(ctx context.Context, c state.ExecutionWorkflowJobClaim, p state.ExecutionPipeline) error {
	active, started := 0, 0
	for i, s := range p.Policy.Steps {
		seen := false
		for a := 1; a <= s.Retry.MaxAttempts; a++ {
			r, err := w.WorkflowRuns.ExecutionWorkflowStepByLabel(ctx, c.AccountID, c.WorkflowID, c.RunsPrincipalID, Label(c.PlanID, i, a))
			if errors.Is(err, state.ErrNotFound) {
				break
			}
			if err != nil {
				return err
			}
			seen = true
			if !r.Status.Terminal() {
				active++
				if _, err = w.Runs.RequestExecutionCancellation(ctx, c.AccountID, r.ID, w.now()); err != nil {
					return err
				}
			}
		}
		if seen {
			started++
		}
	}
	detail := "pipeline deadline exceeded"
	if p.CancelRequested {
		detail = "pipeline cancelled"
	}
	status := api.ManagedExecutionWorkflowFailed
	if active > 0 {
		status = api.ManagedExecutionWorkflowQueued
	}
	return w.update(ctx, c, status, started, detail)
}
