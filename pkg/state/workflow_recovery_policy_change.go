package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type WorkflowRecoveryPolicyChangeStore interface {
	PreviewWorkflowRecoveryPolicyChange(context.Context, string, string, api.SetWorkflowRecoveryPolicyRequest) (api.WorkflowRecoveryPolicyChangePreview, error)
}

func recoveryPolicyChangePreview(queue api.WorkflowRecoveryAutomaticQueue, proposed api.WorkflowRecoveryPolicy, lastExecution *time.Time, budget recoveryPolicyBudget) api.WorkflowRecoveryPolicyChangePreview {
	out := api.WorkflowRecoveryPolicyChangePreview{Current: queue.Policy, Proposed: recoveryPolicyClone(proposed), ObservedAt: queue.ObservedAt, NextVersion: queue.Policy.Version + 1, PendingSuppressed: queue.Pending, OutstandingRetained: queue.Outstanding, Entries: []api.WorkflowRecoveryPolicyChangeEntry{}, EntriesTruncated: queue.Truncated, HourlyExecutions: queue.HourlyExecutions, HourlyPreparedActions: budget.Hourly, Candidates: []api.WorkflowRecoveryPolicyChangeCandidate{}}
	out.AutomaticExecutionEnabled = proposed.Enabled && api.WorkflowRecoveryPolicyAutomaticMode(proposed.Mode)
	if api.WorkflowRecoveryPolicyAutomaticMode(proposed.Mode) {
		out.RemainingExecutionBudget = max(0, proposed.HourlyExecutionBudget-queue.HourlyExecutions)
	}
	out.RemainingPreparationBudget = max(0, proposed.HourlyActionBudget-budget.Hourly)
	out.ExecutionCooldownUntil = recoveryPolicyChangeCooldown(lastExecution, proposed.CooldownSeconds, out.ObservedAt)
	out.PreparationCooldownUntil = recoveryPolicyChangeCooldown(&budget.Last, proposed.CooldownSeconds, out.ObservedAt)
	for _, e := range queue.Entries {
		effect := "would_suppress"
		if e.Action.Status == "applied" {
			effect = "retain_slot"
		}
		out.Entries = append(out.Entries, api.WorkflowRecoveryPolicyChangeEntry{Entry: e, Effect: effect})
	}
	return out
}
func recoveryPolicyChangeCooldown(last *time.Time, seconds int, now time.Time) *time.Time {
	if last != nil && !last.IsZero() {
		until := last.Add(time.Duration(seconds) * time.Second)
		if until.After(now) {
			return &until
		}
	}
	return nil
}
func addRecoveryPolicyChangeCandidates(out *api.WorkflowRecoveryPolicyChangePreview, job api.EventRecoveryJob, candidates []recoveryPolicyCandidate, budget *recoveryPolicyBudget) {
	p := api.WorkflowRecoveryPolicyResponse{Version: out.NextVersion, AppID: out.Current.AppID, Policy: out.Proposed}
	if !p.Policy.Enabled {
		for i := range candidates {
			if candidates[i].Decision.Outcome == "eligible" {
				candidates[i].Decision.Outcome = "policy_disabled"
			}
		}
	}
	r, _ := buildRecoveryPolicyReview(p, "", job, candidates, *budget, out.ObservedAt)
	for _, d := range r.Decisions {
		if d.Outcome == "prepared" {
			budget.Hourly++
			budget.Last = out.ObservedAt
			budget.Existing[recoveryPolicyTargetKey(d.RunID, d.ResumeCount, d.Action)] = true
			d.Outcome = "would_prepare"
		}
		if len(out.Candidates) < api.AutomationRecoveryBatchMaxRuns {
			out.Candidates = append(out.Candidates, api.WorkflowRecoveryPolicyChangeCandidate{JobID: job.ID, Decision: d})
		} else {
			out.CandidatesTruncated = true
		}
	}
}
