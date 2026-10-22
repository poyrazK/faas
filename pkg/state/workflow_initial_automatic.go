package state

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func initialAutomaticPressureReason(p api.WorkflowRecoveryPolicyResponse, hourly int, last *time.Time, unresolved bool, now time.Time) string {
	if !p.Policy.Enabled || p.Policy.Mode != "automatic_initial" {
		return "policy_changed"
	}
	if p.AutomaticExecution == nil || p.AutomaticExecution.PausedAt != nil {
		return "automatic_paused"
	}
	if unresolved {
		return "outstanding_recovery"
	}
	if hourly >= p.Policy.HourlyExecutionBudget {
		return "hourly_execution_budget"
	}
	if last != nil && last.Add(time.Duration(p.Policy.CooldownSeconds)*time.Second).After(now) {
		return "execution_cooldown"
	}
	return ""
}
func initialAutomaticEvaluation(r *api.WorkflowInitialRecoveryReview, reason string, now time.Time) {
	r.AutomaticEvaluation = recoveryAutomaticEvaluation(r.AutomaticEvaluation, reason, now)
	r.ObservedAt = now
}
func initialAutomaticBlockedReason(err error) (string, bool) {
	var blocked *initialReviewBlockError
	if errors.As(err, &blocked) {
		return blocked.Reason, true
	}
	if automaticRecoveryDeferrable(err) {
		return "execution_quota", true
	}
	return "", false
}
func initialAutomaticOutcome(r api.WorkflowInitialRecoveryReview, job api.EventRecoveryJob, item api.EventRecoveryItem, execution api.EventRecoveryExecution, now time.Time) (*api.WorkflowRecoveryReviewActionStatus, string) {
	out := &api.WorkflowRecoveryReviewActionStatus{Position: 1, Kind: "initial_recovery", Status: "applied", Reason: "result_recorded", RunID: r.Target.RunID, ResumeCount: 1, RunURL: "/v1/workflows/runs/" + r.Target.RunID, ChildJobID: r.JobID, ItemsURL: "/v1/event-recoveries/" + r.JobID + "/items", RecordedAt: &now, Complete: true}
	if (item.State == "failed" || item.State == "skipped" || item.State == "cancelled") && job.PendingCount == 0 && job.CompletedAt != nil && (job.State == "completed" || job.State == "cancelled" || job.State == "expired") {
		out.Outcome, out.OutcomeReason, out.EvidenceSource = "cancelled", "admission_"+item.State, "admission"
		if item.State == "failed" {
			out.Outcome = "failed"
		}
		return out, "result_recorded"
	}
	if item.State != "queued" {
		return nil, "admission_wait"
	}
	switch execution.State {
	case "succeeded", "failed", "cancelled":
		if execution.Source != "recovery_result" || execution.RecordedAt == nil || execution.EvidenceSource != "workflow_run" {
			return nil, "terminal_outcome_not_saved"
		}
		out.RecordedAt = cloneEventReceiptTime(execution.RecordedAt)
		out.Outcome, out.EvidenceSource = execution.State, execution.EvidenceSource
		return out, "result_recorded"
	case "queued":
		return nil, "execution_wait"
	case "running":
		return nil, "recovery_running"
	default:
		return nil, "evidence_unknown"
	}
}
func initialAutomaticQueueEntry(r api.WorkflowInitialRecoveryReview) api.WorkflowRecoveryAutomaticQueueEntry {
	a := api.WorkflowRecoveryAutomaticAction{ActionID: r.ID, Position: 1, Status: "pending", Reason: "awaiting_execution", LastEvaluation: r.AutomaticEvaluation, BlockedNotification: r.AutomaticBlockNotification, ChildJobID: r.JobID, AppliedAt: r.SubmittedAt, Result: r.AutomaticResult}
	if r.SubmittedAt != nil {
		a.Status = "applied"
	}
	if r.AutomaticEvaluation != nil {
		a.Reason = r.AutomaticEvaluation.Reason
	}
	return api.WorkflowRecoveryAutomaticQueueEntry{ReviewID: r.ID, PolicyVersion: r.PolicyVersion, QueuedAt: r.PreparedAt, Action: a}
}

func initialAutomaticSuppressionAction(r api.WorkflowInitialRecoveryReview) api.WorkflowRecoveryAutomaticAction {
	a := initialAutomaticQueueEntry(r).Action
	a.Status, a.Reason = "suppressed", "operator_suppressed"
	if len(r.Events) > 0 {
		last := r.Events[len(r.Events)-1]
		a.Suppression = &api.WorkflowRecoveryAutomaticSuppression{Reason: last.Reason, ActorKind: last.ActorKind, ActorID: last.ActorID, SuppressedAt: last.OccurredAt}
	}
	return a
}
