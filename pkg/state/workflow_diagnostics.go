package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type WorkflowDiagnosticsOptions struct {
	RunID, AccountID, PlatformTenantID string
}

type WorkflowRunDiagnosticsStore interface {
	GetWorkflowRunDiagnostics(context.Context, WorkflowDiagnosticsOptions) (api.WorkflowRunDiagnosticsResponse, error)
}

type workflowResumeBlock struct {
	code, step string
	cause      error
}

func (e *workflowResumeBlock) Error() string { return e.cause.Error() + ": " + e.code }
func (e *workflowResumeBlock) Unwrap() error { return e.cause }
func workflowResumeBlocked(code, step string, cause error) error {
	return &workflowResumeBlock{code: code, step: step, cause: cause}
}

func sortedWorkflowStepNames(steps map[string]WorkflowStep) []string {
	names := make([]string, 0, len(steps))
	for name := range steps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type workflowRecoveryTarget struct {
	plan                                                   api.Plan
	accountActive, appDeleted, maintenance, tenantRequired bool
	liveDeployment, pinnedDeployment, tenantActive         bool
	activeRuns                                             int
}

// Shared by the actual admission and the read-only preview. A preview does not
// hold the admission lock and cannot promise that these gates remain open.
func workflowRecoveryTargetError(run WorkflowRun, target workflowRecoveryTarget) error {
	checks := []struct {
		blocked bool
		code    string
	}{
		{!target.accountActive, "account_inactive"},
		{!target.plan.WorkflowsAllowed(), "plan_not_allowed"},
		{target.appDeleted, "app_deleted"},
		{target.maintenance, "maintenance"},
		{target.tenantRequired && run.PlatformTenantID == "", "tenant_required"},
		{run.PlatformTenantID != "" && !target.tenantActive, "tenant_unavailable"},
		{!target.liveDeployment, "deployment_unavailable"},
		{run.DeploymentID != "" && !target.pinnedDeployment, "pinned_deployment_unavailable"},
	}
	for _, check := range checks {
		if check.blocked {
			return workflowResumeBlocked(check.code, "", ErrWorkflowResumeUnavailable)
		}
	}
	return nil
}

func workflowDiagnosticBlock(err error) api.WorkflowDiagnosticBlocker {
	var blocked *workflowResumeBlock
	if errors.As(err, &blocked) {
		return api.WorkflowRecoveryBlocker(blocked.code, blocked.step)
	}
	return api.WorkflowRecoveryBlocker("integration_unavailable", "")
}

func buildWorkflowRunDiagnostics(run WorkflowRun, steps map[string]WorkflowStep, deadline, now time.Time, counts workflowDispatchOccupancy, target workflowRecoveryTarget, runningAttempt bool, outboundError error) api.WorkflowRunDiagnosticsResponse {
	result := api.WorkflowRunDiagnosticsResponse{
		RunID: run.ID, WorkflowName: run.WorkflowName, Status: run.Status, ObservedAt: now.UTC().Format(time.RFC3339Nano),
		DeploymentID: run.DeploymentID, LegacyUnpinned: run.DeploymentID == "", StateReason: run.Status,
		Steps:  make([]api.WorkflowDiagnosticStep, 0, len(steps)),
		Resume: api.WorkflowResumePreview{ExpectedResumeCount: run.ResumeCount, ReopenedSteps: []string{}, PreservedSteps: []string{}, Blockers: []api.WorkflowDiagnosticBlocker{}},
	}
	workflowRunQueueDiagnostic(&result, run, steps, deadline, now, counts)
	_, names, err := workflowResumePlan(run, steps, run.ResumeCount, target.plan)
	if err != nil {
		result.Resume.Blockers = append(result.Resume.Blockers, workflowDiagnosticBlock(err))
	} else {
		result.Resume.ReopenedSteps = names
	}
	if err := workflowRecoveryTargetError(run, target); err != nil {
		result.Resume.Blockers = append(result.Resume.Blockers, workflowDiagnosticBlock(err))
	}
	if runningAttempt {
		result.Resume.Blockers = append(result.Resume.Blockers, api.WorkflowRecoveryBlocker("active_attempt", ""))
	}
	if outboundError != nil {
		result.Resume.Blockers = append(result.Resume.Blockers, workflowDiagnosticBlock(outboundError))
	}
	if target.plan.WorkflowsAllowed() && target.activeRuns >= target.plan.WorkflowMaxConcurrentRuns() {
		result.Resume.Blockers = append(result.Resume.Blockers, api.WorkflowRecoveryBlocker("active_run_quota", ""))
	}
	reopened := make(map[string]bool, len(names))
	for _, name := range names {
		reopened[name] = true
	}
	kinds := workflowDiagnosticStepKinds(run, steps)
	for _, name := range sortedWorkflowStepNames(steps) {
		step := steps[name]
		result.Steps = append(result.Steps, api.WorkflowDiagnosticStep{
			StepName: name, Status: step.Status, Kind: kinds[name], Attempt: step.Attempt, RetryBase: step.RetryBase,
			NextRetryAt: workflowDiagnosticTime(step.NextRetryAt), NextCheckAt: workflowDiagnosticTime(step.NextCheckAt),
		})
		if !reopened[name] {
			result.Resume.PreservedSteps = append(result.Resume.PreservedSteps, name)
		}
	}
	result.Resume.Eligible = len(result.Resume.Blockers) == 0
	return result
}

func workflowDiagnosticTime(at *time.Time) *string {
	if at == nil {
		return nil
	}
	value := at.UTC().Format(time.RFC3339Nano)
	return &value
}

// Decode once, including for large iteration histories. Diagnostics never
// resolve templates or copy customer input into metadata.
func workflowDiagnosticStepKinds(run WorkflowRun, steps map[string]WorkflowStep) map[string]string {
	var spec api.WorkflowSpec
	_ = json.Unmarshal(run.DefinitionSnapshot, &spec)
	definitions := make(map[string]*api.WorkflowStepSpec, len(spec.Steps))
	for i := range spec.Steps {
		definitions[spec.Steps[i].Name] = &spec.Steps[i]
	}
	kinds := make(map[string]string, len(steps))
	for name, step := range steps {
		action := definitions[name]
		if step.ForEachParent != nil {
			action = nil
			if parent := definitions[*step.ForEachParent]; parent != nil && parent.ForEach != nil {
				item := parent.ForEach.Action.Step(name)
				action = &item
			}
		}
		kinds[name] = workflowDiagnosticStepKind(action)
	}
	return kinds
}

func workflowDiagnosticStepKind(action *api.WorkflowStepSpec) string {
	if action == nil {
		return "unknown"
	}
	switch {
	case action.ForEach != nil:
		return "for_each"
	case action.Join != nil:
		return "join"
	case action.WaitForDuration > 0:
		return "timer"
	case action.WaitForEvent != "":
		return "event"
	case action.WaitForCallback:
		return "callback"
	case action.WaitForCondition != nil:
		return "condition"
	default:
		return "action"
	}
}

func workflowRunQueueDiagnostic(result *api.WorkflowRunDiagnosticsResponse, run WorkflowRun, steps map[string]WorkflowStep, deadline, now time.Time, counts workflowDispatchOccupancy) {
	if run.CancelledAt != nil || run.LastError != nil && *run.LastError == "cancelled by operator" {
		result.StateReason = "cancelled"
		return
	}
	if !workflowGuardRunActive(run.Status) {
		return
	}
	at := run.ScheduledFor
	if run.Status == WorkflowRunStatusRunning {
		if deadline.After(now) {
			return
		}
		result.StaleLease, at = true, deadline
	}
	if at.After(now) {
		result.NextWakeAt = workflowDiagnosticTime(&at)
		result.StateReason = api.AutomationQueueScheduled
		if run.Status == WorkflowRunStatusAwaitingEvent {
			result.StateReason = api.AutomationQueueParkedWait
		} else {
			for _, step := range steps {
				if step.Status == WorkflowStepStatusPending && step.NextRetryAt != nil && step.NextRetryAt.Equal(at) {
					result.StateReason = api.AutomationQueueRetryBackoff
				}
			}
		}
		return
	}
	result.StateReason = api.AutomationQueueReady
	if blocked := counts.capacityReason(run); blocked != "" {
		result.StateReason = blocked
	}
	if at.Before(run.CreatedAt) {
		at = run.CreatedAt
	}
	result.DueAgeSeconds = max(0, now.Sub(at).Seconds())
}
