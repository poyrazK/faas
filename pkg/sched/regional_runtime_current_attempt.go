package sched

// adr: 790
import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// Run after physical observation. A collected/replaced row cannot turn a
// retained physical hold into current execution authority. The original match
// remains available for retirement, with its original resources still charged.
func (e *Engine) observeCurrentRegionalAttempts(ctx context.Context, report *RegionalRuntimeInventoryReport, at time.Time) error {
	var result error
	for _, source := range report.Matches {
		if err := ctx.Err(); err != nil {
			return errors.Join(result, err)
		}
		reason, err := e.currentRegionalAttemptIssue(ctx, source, at)
		if err != nil {
			result = errors.Join(result, err)
			if reason == "" {
				reason = "current attempt observation was interrupted"
			}
		}
		if reason != "" {
			report.CurrentIssues = append(report.CurrentIssues, RegionalRuntimeInventoryIssue{Permit: source.Permit, Reason: reason})
		} else {
			report.CurrentAttempts = append(report.CurrentAttempts, source)
		}
	}
	return result
}

func (e *Engine) currentRegionalAttemptIssue(ctx context.Context, source runtimequota.LaunchSource, at time.Time) (string, error) {
	if source.Validate() != nil {
		return "original current-attempt source is invalid", runtimequota.ErrBinding
	}
	binding := source.Permit.Grant.Binding
	app, err := e.store.AppByID(ctx, binding.RegionalAppID)
	if err != nil {
		return regionalAttemptReadIssue("app", err)
	}
	if !runtimequota.MatchesID(binding.RegionalAppID, app.ID) || !runtimequota.MatchesID(binding.RegionalAccountID, app.AccountID) ||
		binding.RegionalOrgID == "" && app.OrgID != "" || binding.RegionalOrgID != "" && !runtimequota.MatchesID(binding.RegionalOrgID, app.OrgID) {
		return "current app differs from original account/app/org ownership", nil
	}
	if source.Permit.Kind == runtimequota.CapacityAppTask {
		if err := source.ValidateTask(); err != nil {
			return "original task claim provenance is unavailable", fmt.Errorf("sched: regional current task claim: %w", err)
		}
		task, err := e.store.AppTaskByID(ctx, app.AccountID, app.ID, source.TaskID)
		if err != nil {
			return regionalAttemptReadIssue("task", err)
		}
		return currentRegionalTaskIssue(source, task, at), ctx.Err()
	}
	ins, err := e.store.InstanceByID(ctx, source.InstanceKey)
	if err != nil {
		return regionalAttemptReadIssue("instance", err)
	}
	return currentRegionalInstanceIssue(source, ins), ctx.Err()
}

func regionalAttemptReadIssue(kind string, err error) (string, error) {
	if errors.Is(err, state.ErrNotFound) {
		return "original " + kind + " row is no longer available", nil
	}
	return "current " + kind + " observation is unavailable", fmt.Errorf("sched: regional current %s: %w", kind, err)
}

func currentRegionalInstanceIssue(source runtimequota.LaunchSource, ins state.Instance) string {
	if source.Permit.Kind == runtimequota.CapacityAppTask || source.InstanceKey != ins.ID || !state.RegionalRuntimeAttemptMatches(source, ins) {
		return "current instance differs from the original run/app/deployment/node"
	}
	if !state.State(ins.State).CountsForRAM() {
		return "original instance run is no longer resident"
	}
	return ""
}

func currentRegionalTaskIssue(source runtimequota.LaunchSource, task state.AppTask, at time.Time) string {
	p := source.Permit
	if source.ValidateTask() != nil || !runtimequota.MatchesID(source.TaskID, task.ID) || !runtimequota.MatchesID(p.Grant.Binding.RegionalAccountID, task.AccountID) ||
		!runtimequota.MatchesID(p.Grant.Binding.RegionalAppID, task.AppID) || !runtimequota.MatchesID(p.DeploymentID, task.DeploymentID) ||
		task.LeaseToken == nil || !runtimequota.MatchesID(p.RunID, *task.LeaseToken) {
		return "current task differs from the original claim/account/app/deployment"
	}
	if task.Status != state.AppTaskRestoring && task.Status != state.AppTaskRunning || task.CancelRequested != nil ||
		task.LeaseOwner == nil || *task.LeaseOwner == "" || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(at) || at.IsZero() {
		return "original task claim is expired, cancelled or no longer active"
	}
	return ""
}
