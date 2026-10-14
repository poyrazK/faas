package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// WorkflowRecoveryPolicyHealth reports recovery automation observations, not execution authority.
type WorkflowRecoveryPolicyHealth struct {
	Overview             *WorkflowRecoveryOverview      `json:"overview,omitempty"`
	Status               string                         `json:"status"`
	Reason               string                         `json:"reason"`
	StallAfterSeconds    int                            `json:"stall_after_seconds"`
	SampleLastProgressAt *time.Time                     `json:"sample_last_progress_at,omitempty"`
	Queue                WorkflowRecoveryAutomaticQueue `json:"queue"`
}

func ValidateRecoveryHealthThreshold(seconds int) error {
	if seconds < WorkflowRecoveryHealthMinStallSeconds || seconds > WorkflowRecoveryHealthMaxStallSeconds {
		return fmt.Errorf("stall_after_seconds must be between %d and %d", WorkflowRecoveryHealthMinStallSeconds, WorkflowRecoveryHealthMaxStallSeconds)
	}
	return nil
}

// RecoveryPolicyHealth uses a bounded, consistent queue snapshot. Progress is sample-local.
func RecoveryPolicyHealth(q WorkflowRecoveryAutomaticQueue, seconds int) WorkflowRecoveryPolicyHealth {
	h := WorkflowRecoveryPolicyHealth{Status: "healthy", Reason: "no_observed_blocker", StallAfterSeconds: seconds, Queue: q}
	auto := q.Policy.Policy.Enabled && WorkflowRecoveryPolicyAutomaticMode(q.Policy.Policy.Mode)
	blocked, stalled := "", ""
	var latestPendingEvaluation time.Time
	for _, entry := range q.Entries {
		if entry.Action.Status == "pending" && entry.Action.LastEvaluation != nil && entry.Action.LastEvaluation.EvaluatedAt.After(latestPendingEvaluation) {
			latestPendingEvaluation = entry.Action.LastEvaluation.EvaluatedAt
		}
	}
	for _, entry := range q.Entries {
		e := entry.Action.LastEvaluation
		if e != nil && (e.Reason == "child_created" || e.Reason == "result_recorded" || e.Reason == "suppressed") && !e.EvaluatedAt.After(q.ObservedAt) {
			if h.SampleLastProgressAt == nil || e.EvaluatedAt.After(*h.SampleLastProgressAt) {
				at := e.EvaluatedAt
				h.SampleLastProgressAt = &at
			}
		}
		if entry.Action.Status == "pending" && !auto {
			continue
		}
		expectedWait := entry.Action.Status == "pending" && (q.Outstanding > 0 || q.RemainingBudget == 0 || (q.CooldownUntil != nil && q.CooldownUntil.After(q.ObservedAt)))
		at := entry.QueuedAt
		if e != nil {
			at = e.EvaluatedAt
		}
		if entry.Action.Status == "pending" && latestPendingEvaluation.After(at) {
			at = latestPendingEvaluation
		}
		if !expectedWait && !at.IsZero() && q.ObservedAt.Sub(at) >= time.Duration(seconds)*time.Second {
			stalled = "evaluation_overdue"
		}
		if e == nil {
			continue
		}
		switch e.Reason {
		case "job_paused", "outstanding_recovery", "terminal_outcome_not_saved", "evidence_unknown", "inspection_error", "execution_wait", "admission_wait", "outstanding_retry", "execution_quota":
			if blocked == "" {
				blocked = e.Reason
			}
		}
	}
	switch {
	case q.Policy.AutomaticExecution != nil && q.Policy.AutomaticExecution.PausedAt != nil:
		h.Status, h.Reason = "paused", q.Policy.AutomaticExecution.PauseReason
	case stalled != "":
		h.Status, h.Reason = "stalled", stalled
	case blocked != "":
		h.Status, h.Reason = "blocked", blocked
	case auto && q.Pending > 0 && q.Outstanding > 0:
		h.Status, h.Reason = "blocked", "outstanding_retry"
	case auto && q.Pending > 0 && q.RemainingBudget == 0:
		h.Status, h.Reason = "blocked", "hourly_budget"
	case auto && q.Pending > 0 && q.CooldownUntil != nil && q.CooldownUntil.After(q.ObservedAt):
		h.Status, h.Reason = "blocked", "cooldown"
	case q.Truncated:
		h.Status, h.Reason = "unknown", "queue_sample_truncated"
	case !auto && q.Outstanding == 0:
		h.Status, h.Reason = "inactive", "automatic_execution_disabled"
	}
	return h
}

func (c *Client) GetWorkflowRecoveryPolicyHealth(ctx context.Context, app string, seconds int) (WorkflowRecoveryPolicyHealth, error) {
	var out WorkflowRecoveryPolicyHealth
	if err := ValidateRecoveryHealthThreshold(seconds); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodGet, recoveryPolicyPath(app)+"/health?stall_after_seconds="+strconv.Itoa(seconds), nil, &out)
	return out, err
}
