package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/executionworkflow"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	executionWorkflowWorkerInterval = 2 * time.Second
	executionWorkflowWorkerLease    = 45 * time.Second
	executionWorkflowWorkerBatch    = 8
	managedWorkflowFatalErrorPrefix = "managed-fatal: "
)

type managedWorkflowPermanentStepError string

func (e managedWorkflowPermanentStepError) Error() string { return string(e) }

type managedWorkflowDependencyInput struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
}

type managedWorkflowObservedStep struct {
	Run             state.Execution
	HasRun          bool
	Blocked         bool
	ContractFailure string
}

func (s *server) runManagedExecutionWorkflowWorker(ctx context.Context) {
	jobs, jobsOK := s.store.(state.ExecutionWorkflowJobStore)
	if !jobsOK {
		return
	}
	owner := "apid-workflow-" + uuid.NewString()
	ticker := time.NewTicker(executionWorkflowWorkerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !s.executionAPIEnabled || mfaIdentities == nil || len(mfaIdentities()) == 0 {
			continue
		}
		for i := 0; i < executionWorkflowWorkerBatch; i++ {
			claim, err := jobs.ClaimExecutionWorkflowJob(ctx, owner, time.Now().UTC(), executionWorkflowWorkerLease)
			if errors.Is(err, state.ErrNotFound) || errors.Is(err, context.Canceled) {
				break
			}
			if err != nil {
				if s.log != nil {
					s.log.Error("managed execution workflow claim failed", "err", err)
				}
				break
			}
			if err := s.driveManagedExecutionWorkflow(ctx, claim); err != nil && s.log != nil {
				s.log.Error("managed execution workflow drive failed", "workflow_id", claim.WorkflowID, "account_id", claim.AccountID, "err", err)
			}
		}
	}
}

func (s *server) driveManagedExecutionWorkflow(ctx context.Context, claim state.ExecutionWorkflowJobClaim) error {
	job := claim.ExecutionWorkflowJob
	jobs, jobsOK := s.store.(state.ExecutionWorkflowJobStore)
	runs, runsOK := s.store.(state.ExecutionStore)
	workflowRuns, workflowOK := s.store.(state.ExecutionWorkflowStore)
	if !jobsOK || !runsOK || !workflowOK {
		return fmt.Errorf("managed workflow stores are unavailable")
	}
	identities := mfaIdentities()
	planBytes, err := executionpayload.DecodeWorkflowPlan(ctx, identities, job.SealedPlan, job.PayloadKID)
	if err != nil {
		return s.completeManagedWorkflowClaim(ctx, jobs, claim, api.ManagedExecutionWorkflowFailed, job.NextStep, "encrypted plan could not be opened")
	}
	defer clear(planBytes)
	var plan api.CreateManagedExecutionWorkflowRequest
	if err := json.Unmarshal(planBytes, &plan); err != nil || api.ValidateCreateManagedExecutionWorkflow(plan) != nil {
		return s.completeManagedWorkflowClaim(ctx, jobs, claim, api.ManagedExecutionWorkflowFailed, job.NextStep, "stored workflow plan is invalid")
	}
	canonical, err := json.Marshal(plan)
	if err != nil {
		return s.completeManagedWorkflowClaim(ctx, jobs, claim, api.ManagedExecutionWorkflowFailed, job.NextStep, "stored workflow plan is invalid")
	}
	digest := sha256.Sum256(canonical)
	if hex.EncodeToString(digest[:12]) != job.PlanID || len(plan.Steps) != job.StepCount {
		return s.completeManagedWorkflowClaim(ctx, jobs, claim, api.ManagedExecutionWorkflowFailed, job.NextStep, "stored workflow plan identity does not match")
	}
	dependencies, err := api.ManagedExecutionWorkflowStepDependencies(plan)
	if err != nil {
		return s.completeManagedWorkflowClaim(ctx, jobs, claim, api.ManagedExecutionWorkflowFailed, job.NextStep, "stored workflow dependencies are invalid")
	}
	resultSchemas := make([]*executionworkflow.Schema, len(plan.Steps))
	for i, step := range plan.Steps {
		resultSchemas[i], err = executionworkflow.CompileResultSchema(step.ResultSchema)
		if err != nil {
			return s.completeManagedWorkflowClaim(ctx, jobs, claim, api.ManagedExecutionWorkflowFailed, job.NextStep, "stored workflow result schema is invalid")
		}
	}
	account, err := s.store.AccountByID(ctx, job.AccountID)
	if err != nil {
		return s.retryManagedWorkflowClaim(ctx, jobs, claim, job.NextStep, "waiting for account state")
	}
	if job.NextStep < 0 || job.NextStep > len(plan.Steps) {
		return s.completeManagedWorkflowClaim(ctx, jobs, claim, api.ManagedExecutionWorkflowFailed, 0, "stored workflow progress is invalid")
	}
	observed := make([]managedWorkflowObservedStep, len(plan.Steps))
	startedSteps := 0
	activeRuns := 0
	failureDetail := ""
	hardFailure := false
	if strings.HasPrefix(job.LastError, managedWorkflowFatalErrorPrefix) {
		hardFailure = true
		failureDetail = strings.TrimPrefix(job.LastError, managedWorkflowFatalErrorPrefix)
	}
	for i, step := range plan.Steps {
		label := managedExecutionWorkflowStepLabel(job.PlanID, step.Label)
		row, getErr := workflowRuns.ExecutionWorkflowStepByLabel(ctx, job.AccountID, job.WorkflowID, job.RunsPrincipalID, label)
		if errors.Is(getErr, state.ErrNotFound) {
			continue
		}
		if getErr != nil {
			return s.retryManagedWorkflowClaim(ctx, jobs, claim, job.NextStep, "waiting for Run receipts")
		}
		observed[i] = managedWorkflowObservedStep{Run: row, HasRun: true}
		if row.Status == api.ExecutionStatusSucceeded {
			observed[i].ContractFailure = executionworkflow.ValidateResult(row.Result, resultSchemas[i])
			if observed[i].ContractFailure != "" && failureDetail == "" {
				failureDetail = managedWorkflowResultContractFailure(step.Label, observed[i].ContractFailure)
			}
		}
		startedSteps++
		if !row.Status.Terminal() {
			activeRuns++
		} else if row.Status != api.ExecutionStatusSucceeded && failureDetail == "" {
			failureDetail = fmt.Sprintf("step %q ended with Run status %s", step.Label, row.Status)
		}
	}
	if startedSteps < job.NextStep {
		startedSteps = job.NextStep
	}
	limits, planAllowsRuns := account.Plan.ExecutionLimits()
	if !planAllowsRuns || !limits.Allowed {
		return s.completeManagedWorkflowClaim(ctx, jobs, claim, api.ManagedExecutionWorkflowFailed, startedSteps, "the account plan no longer allows Runs")
	}
	parallelLimit := plan.MaxParallelSteps
	if parallelLimit == 0 {
		parallelLimit = 1
	}
	if parallelLimit > limits.MaxConcurrent {
		parallelLimit = limits.MaxConcurrent
	}
	if parallelLimit < 1 {
		return s.retryManagedWorkflowClaim(ctx, jobs, claim, startedSteps, "waiting for Run capacity")
	}
	continueIndependent := plan.FailurePolicy == api.ExecutionWorkflowManagedFailureContinueIndependent
	failFast := !continueIndependent
	stopAdmissions := hardFailure || failFast && failureDetail != ""
	for i, step := range plan.Steps {
		if observed[i].HasRun || observed[i].Blocked {
			continue
		}
		allDependenciesTerminal, allDependenciesSucceeded := true, true
		for _, dependency := range dependencies[step.Label] {
			dependencyIndex := managedWorkflowStepIndex(plan, dependency)
			status, terminal := managedWorkflowObservedStatus(observed[dependencyIndex])
			if !terminal {
				allDependenciesTerminal = false
				allDependenciesSucceeded = false
				break
			}
			if status != string(api.ExecutionStatusSucceeded) {
				allDependenciesSucceeded = false
			}
		}
		if continueIndependent && allDependenciesTerminal && !allDependenciesSucceeded &&
			!managedWorkflowCanConsumeFailedDependencies(step, dependencies[step.Label], plan, observed) {
			observed[i].Blocked = true
			continue
		}
	}
	if !stopAdmissions && parallelLimit > 0 {
		for i, step := range plan.Steps {
			if observed[i].HasRun || observed[i].Blocked || activeRuns >= parallelLimit {
				continue
			}
			allDependenciesTerminal, allDependenciesSucceeded := true, true
			for _, dependency := range dependencies[step.Label] {
				dependencyIndex := managedWorkflowStepIndex(plan, dependency)
				status, terminal := managedWorkflowObservedStatus(observed[dependencyIndex])
				if !terminal {
					allDependenciesTerminal = false
					allDependenciesSucceeded = false
					break
				}
				if status != string(api.ExecutionStatusSucceeded) {
					allDependenciesSucceeded = false
				}
			}
			ready := allDependenciesSucceeded
			if continueIndependent && allDependenciesTerminal &&
				managedWorkflowCanConsumeFailedDependencies(step, dependencies[step.Label], plan, observed) {
				ready = true
			}
			if !ready {
				continue
			}
			var previous state.Execution
			if step.InputFromPreviousResult {
				previous = observed[i-1].Run
			}
			dependencyInputs := managedWorkflowDependencyInputs(dependencies[step.Label], plan, observed)
			if err := s.admitManagedExecutionWorkflowStep(ctx, runs, account, job, step, previous, dependencyInputs, plan, observed); err != nil {
				var quota *state.ExecutionQuotaError
				if errors.As(err, &quota) || errors.Is(err, state.ErrExecutionRuntimeUnpinned) {
					return s.retryManagedWorkflowClaim(ctx, jobs, claim, startedSteps, "waiting for Run capacity")
				}
				if errors.Is(err, state.ErrExecutionWorkflowStepExists) {
					return s.retryManagedWorkflowClaim(ctx, jobs, claim, startedSteps, "waiting for an existing Run receipt")
				}
				var permanent managedWorkflowPermanentStepError
				if errors.As(err, &permanent) || errors.Is(err, state.ErrExecutionInvalid) ||
					errors.Is(err, state.ErrExecutionsNotAllowed) || errors.Is(err, state.ErrExecutionOutboundIntegrationUnavailable) {
					message := managedWorkflowSafeFailure(step.Label, err)
					if activeRuns > 0 {
						return s.retryManagedWorkflowClaim(ctx, jobs, claim, startedSteps, managedWorkflowFatalErrorPrefix+message)
					}
					return s.completeManagedWorkflowClaim(ctx, jobs, claim, api.ManagedExecutionWorkflowFailed, startedSteps, message)
				}
				if s.log != nil {
					s.log.Error("managed Run admission deferred", "workflow_id", job.WorkflowID, "step", step.Label, "err", err)
				}
				return s.retryManagedWorkflowClaim(ctx, jobs, claim, startedSteps, "waiting for Run admission")
			}
			observed[i] = managedWorkflowObservedStep{Run: state.Execution{Status: api.ExecutionStatusQueued}, HasRun: true}
			startedSteps++
			activeRuns++
		}
	}
	if activeRuns > 0 {
		message := ""
		if stopAdmissions {
			if hardFailure {
				message = managedWorkflowFatalErrorPrefix + failureDetail
			} else {
				message = "waiting for already-admitted Runs to finish after a step failure"
			}
		}
		return s.retryManagedWorkflowClaim(ctx, jobs, claim, startedSteps, message)
	}
	if stopAdmissions {
		return s.completeManagedWorkflowClaim(ctx, jobs, claim, api.ManagedExecutionWorkflowFailed, startedSteps, failureDetail)
	}
	allSettled, allSucceeded := true, true
	for _, step := range observed {
		if step.HasRun {
			if !step.Run.Status.Terminal() {
				allSettled = false
			}
			if step.Run.Status != api.ExecutionStatusSucceeded || step.ContractFailure != "" {
				allSucceeded = false
			}
		} else if !step.Blocked {
			allSettled = false
			allSucceeded = false
		}
		if step.Blocked {
			allSucceeded = false
		}
	}
	if allSettled {
		if allSucceeded {
			return s.completeManagedWorkflowClaim(ctx, jobs, claim, api.ManagedExecutionWorkflowSucceeded, startedSteps, "")
		}
		if failureDetail == "" {
			failureDetail = "one or more steps were blocked by unsuccessful dependencies"
		}
		return s.completeManagedWorkflowClaim(ctx, jobs, claim, api.ManagedExecutionWorkflowFailed, startedSteps, failureDetail)
	}
	if activeRuns == 0 {
		return s.completeManagedWorkflowClaim(ctx, jobs, claim, api.ManagedExecutionWorkflowFailed, startedSteps, "workflow could not make progress")
	}
	return s.retryManagedWorkflowClaim(ctx, jobs, claim, startedSteps, "waiting for workflow dependencies")
}

func managedWorkflowStepIndex(plan api.CreateManagedExecutionWorkflowRequest, label string) int {
	for i, step := range plan.Steps {
		if step.Label == label {
			return i
		}
	}
	return -1
}

func managedWorkflowObservedStatus(step managedWorkflowObservedStep) (string, bool) {
	if step.Blocked {
		return "blocked", true
	}
	if step.ContractFailure != "" {
		return "contract_failed", true
	}
	if !step.HasRun || step.Run.ID == "" && !step.Run.Status.Terminal() {
		return "", false
	}
	return string(step.Run.Status), step.Run.Status.Terminal()
}

func managedWorkflowDependencyInputs(dependencies []string, plan api.CreateManagedExecutionWorkflowRequest, observed []managedWorkflowObservedStep) map[string]managedWorkflowDependencyInput {
	inputs := make(map[string]managedWorkflowDependencyInput, len(dependencies))
	for _, dependency := range dependencies {
		index := managedWorkflowStepIndex(plan, dependency)
		status, _ := managedWorkflowObservedStatus(observed[index])
		input := managedWorkflowDependencyInput{Status: status}
		result := observed[index].Run.Result
		if status == string(api.ExecutionStatusSucceeded) && len(result) > 0 && json.Valid(result) {
			input.Result = append(json.RawMessage(nil), result...)
		}
		inputs[dependency] = input
	}
	return inputs
}

func managedWorkflowCanConsumeFailedDependencies(step api.CreateManagedExecutionWorkflowStep, dependencies []string, plan api.CreateManagedExecutionWorkflowRequest, observed []managedWorkflowObservedStep) bool {
	if !step.IncludeDependencyResults || len(dependencies) == 0 {
		return false
	}
	for _, input := range step.ArtifactInputs {
		index := managedWorkflowStepIndex(plan, input.FromStep)
		if index < 0 || index >= len(observed) {
			return false
		}
		status, _ := managedWorkflowObservedStatus(observed[index])
		if status != string(api.ExecutionStatusSucceeded) {
			return false
		}
	}
	return true
}

func (s *server) admitManagedExecutionWorkflowStep(ctx context.Context, runs state.ExecutionStore, account state.Account, job state.ExecutionWorkflowJob, step api.CreateManagedExecutionWorkflowStep, previous state.Execution, dependencyInputs map[string]managedWorkflowDependencyInput, plan api.CreateManagedExecutionWorkflowRequest, observed []managedWorkflowObservedStep) error {
	request := step.Request
	if len(step.ArtifactInputs) > 0 {
		if len(request.Files)+len(step.ArtifactInputs) > api.ExecutionBundleMaxFiles {
			return managedWorkflowPermanentStepError("artifact inputs exceed the bundle file limit")
		}
		for _, input := range step.ArtifactInputs {
			index := managedWorkflowStepIndex(plan, input.FromStep)
			if index < 0 || index >= len(observed) {
				return managedWorkflowPermanentStepError(fmt.Sprintf("artifact source step %q did not succeed", input.FromStep))
			}
			status, _ := managedWorkflowObservedStatus(observed[index])
			if !observed[index].HasRun || status != string(api.ExecutionStatusSucceeded) {
				return managedWorkflowPermanentStepError(fmt.Sprintf("artifact source step %q did not succeed", input.FromStep))
			}
			var artifact *api.ExecutionArtifact
			for i := range observed[index].Run.Artifacts {
				if observed[index].Run.Artifacts[i].Name == input.Name {
					artifact = &observed[index].Run.Artifacts[i]
					break
				}
			}
			if artifact == nil {
				return managedWorkflowPermanentStepError(fmt.Sprintf("source step %q did not produce artifact %q", input.FromStep, input.Name))
			}
			if err := api.ValidateExecutionArtifacts([]api.ExecutionArtifact{*artifact}); err != nil {
				return managedWorkflowPermanentStepError("stored source artifact failed integrity verification")
			}
			request.Files = append(request.Files, api.ExecutionFile{Path: input.Path, Content: append([]byte(nil), artifact.Content...)})
		}
	}
	if step.InputFromPreviousResult {
		if previous.ID == "" || previous.Status != api.ExecutionStatusSucceeded || len(previous.Result) == 0 || !json.Valid(previous.Result) {
			return managedWorkflowPermanentStepError("previous Run did not return a valid JSON result")
		}
		limits, allowed := account.Plan.ExecutionLimits()
		if !allowed || len(previous.Result) > limits.MaxInputBytes {
			return managedWorkflowPermanentStepError("previous Run result exceeds this account's input limit")
		}
		request.Input = append(json.RawMessage(nil), previous.Result...)
	} else if step.IncludeDependencyResults {
		input, err := json.Marshal(dependencyInputs)
		if err != nil {
			return managedWorkflowPermanentStepError("dependency results could not be encoded")
		}
		limits, allowed := account.Plan.ExecutionLimits()
		if !allowed || len(input) > limits.MaxInputBytes {
			return managedWorkflowPermanentStepError("dependency results exceed this account's input limit")
		}
		request.Input = input
	}
	request.WorkflowID = job.WorkflowID
	request.StepLabel = managedExecutionWorkflowStepLabel(job.PlanID, step.Label)
	resolved, problem := request.Resolve(account.Plan)
	if problem != nil {
		return managedWorkflowPermanentStepError("step request no longer resolves within the account plan")
	}
	integrationIDs, err := api.NormalizeExecutionIntegrationIDs(request.IntegrationIDs)
	if err != nil {
		return managedWorkflowPermanentStepError("step has invalid integration ids")
	}
	if setSecretRecipient == nil {
		return fmt.Errorf("host age recipient is not loaded")
	}
	recipient := setSecretRecipient()
	if recipient == nil {
		return fmt.Errorf("host age recipient is not loaded")
	}
	sealed, err := executionpayload.SealRequest(recipient, resolved)
	if err != nil {
		return fmt.Errorf("step payload could not be sealed")
	}
	now := time.Now().UTC()
	_, err = runs.CreateExecution(ctx, state.CreateExecutionParams{
		AccountID: job.AccountID, WorkflowID: job.WorkflowID, StepLabel: request.StepLabel,
		OutboundIntegrationIDs: integrationIDs, RunsPrincipalID: job.RunsPrincipalID,
		Request: resolved, SourceBytes: resolved.SourceBytes(), InputBytes: len(resolved.Input),
		AdmittedAt: now, DeadlineAt: now.Add(time.Duration(resolved.Limits.TimeoutMS) * time.Millisecond),
		SealedPayload: sealed, PayloadKID: recipient.String(),
	})
	if err != nil {
		return err
	}
	return nil
}

func managedExecutionWorkflowStepLabel(planID, label string) string {
	return "gwf:" + planID + ":" + label
}

func managedWorkflowSafeFailure(label string, err error) string {
	message := "Run admission failed; inspect the step request and account limits"
	var permanent managedWorkflowPermanentStepError
	switch {
	case errors.As(err, &permanent):
		message = string(permanent)
	case errors.Is(err, state.ErrExecutionOutboundIntegrationUnavailable):
		message = "one or more requested integrations are no longer available for Runs"
	case errors.Is(err, state.ErrExecutionInvalid):
		message = "the step request is invalid or no longer fits the account limits"
	case errors.Is(err, state.ErrExecutionsNotAllowed):
		message = "the account plan no longer allows Runs"
	}
	return fmt.Sprintf("step %q: %s", label, message)
}

func managedWorkflowResultContractFailure(label, detail string) string {
	const maxDetailBytes = 1800
	if len(detail) > maxDetailBytes {
		limit := maxDetailBytes - len("…")
		for limit > 0 && !utf8.RuneStart(detail[limit]) {
			limit--
		}
		detail = detail[:limit] + "…"
	}
	return fmt.Sprintf("step %q result contract failed: %s", label, detail)
}

func (s *server) retryManagedWorkflowClaim(ctx context.Context, jobs state.ExecutionWorkflowJobStore, claim state.ExecutionWorkflowJobClaim, nextStep int, message string) error {
	now := time.Now().UTC()
	return jobs.UpdateExecutionWorkflowJob(ctx, claim.ID, claim.ClaimToken, state.ExecutionWorkflowJobUpdate{
		Status: api.ManagedExecutionWorkflowQueued, NextStep: nextStep,
		ScheduledFor: now.Add(executionWorkflowWorkerInterval), LastError: message, UpdatedAt: now,
	})
}

func (s *server) completeManagedWorkflowClaim(ctx context.Context, jobs state.ExecutionWorkflowJobStore, claim state.ExecutionWorkflowJobClaim, status api.ManagedExecutionWorkflowStatus, nextStep int, message string) error {
	now := time.Now().UTC()
	return jobs.UpdateExecutionWorkflowJob(ctx, claim.ID, claim.ClaimToken, state.ExecutionWorkflowJobUpdate{
		Status: status, NextStep: nextStep, ScheduledFor: now,
		LastError: message, UpdatedAt: now,
	})
}
