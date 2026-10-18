package api

import (
	"testing"
	"time"
)

func TestRecoveryPolicyHealth(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, reason, want                   string
		age                                  int
		pending, paused, disabled, truncated bool
		budget                               int
	}{
		{"idle", "", "healthy", 0, false, false, false, false, 1},
		{"running", "retry_running", "healthy", 1, false, false, false, false, 1},
		{"unknown", "evidence_unknown", "blocked", 1, false, false, false, false, 1},
		{"job_paused", "job_paused", "blocked", 1, false, false, false, false, 1},
		{"quota", "execution_quota", "blocked", 1, true, false, false, false, 1},
		{"budget_reopened", "hourly_budget", "healthy", 1, true, false, false, false, 1},
		{"cooldown_expired", "cooldown", "healthy", 1, true, false, false, false, 1},
		{"overdue", "retry_running", "stalled", 300, false, false, false, false, 1},
		{"unevaluated", "", "stalled", 301, true, false, false, false, 1},
		{"paused", "evidence_unknown", "paused", 301, true, true, false, false, 1},
		{"budget", "hourly_budget", "blocked", 301, true, false, false, false, 0},
		{"disabled", "", "inactive", 301, true, false, true, false, 1},
		{"partial", "retry_running", "unknown", 1, false, false, false, true, 1},
		{"disabled_child", "inspection_error", "blocked", 1, false, false, true, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := WorkflowRecoveryAutomaticQueue{ObservedAt: now, RemainingBudget: tc.budget, Truncated: tc.truncated, Policy: WorkflowRecoveryPolicyResponse{Policy: WorkflowRecoveryPolicy{Enabled: !tc.disabled, Mode: "automatic_retry"}}}
			if tc.paused {
				q.Policy.AutomaticExecution = &WorkflowRecoveryPolicyAutomaticExecution{PausedAt: &now, PauseReason: "operator_pause"}
			}
			if tc.name != "idle" {
				at := now.Add(-time.Duration(tc.age) * time.Second)
				e := WorkflowRecoveryAutomaticQueueEntry{QueuedAt: at, Action: WorkflowRecoveryAutomaticAction{Status: "applied"}}
				q.Outstanding = 1
				if tc.pending {
					e.Action.Status = "pending"
					q.Pending = 1
					q.Outstanding = 0
				}
				if tc.reason != "" {
					e.Action.LastEvaluation = &WorkflowRecoveryAutomaticEvaluation{EvaluatedAt: at, Reason: tc.reason}
				}
				q.Entries = []WorkflowRecoveryAutomaticQueueEntry{e}
			}
			got := RecoveryPolicyHealth(q, 300)
			if got.Status != tc.want {
				t.Fatalf("got %+v want %s", got, tc.want)
			}
		})
	}
	q := WorkflowRecoveryAutomaticQueue{ObservedAt: now, Policy: WorkflowRecoveryPolicyResponse{Policy: WorkflowRecoveryPolicy{Enabled: true, Mode: "automatic_retry"}}, Pending: 1, RemainingBudget: 1}
	until := now.Add(time.Minute)
	q.CooldownUntil = &until
	q.Entries = []WorkflowRecoveryAutomaticQueueEntry{{QueuedAt: now.Add(-time.Hour), Action: WorkflowRecoveryAutomaticAction{Status: "pending"}}}
	if got := RecoveryPolicyHealth(q, 300); got.Status != "blocked" || got.Reason != "cooldown" {
		t.Fatalf("expected wait %+v", got)
	}
	q.Entries[0].Action.LastEvaluation = &WorkflowRecoveryAutomaticEvaluation{Reason: "child_created", EvaluatedAt: now}
	if got := RecoveryPolicyHealth(q, 300); got.SampleLastProgressAt == nil || !got.SampleLastProgressAt.Equal(now) {
		t.Fatal("missing sample progress")
	}
	q.CooldownUntil = nil
	q.Entries = []WorkflowRecoveryAutomaticQueueEntry{
		{QueuedAt: now.Add(-time.Hour), Action: WorkflowRecoveryAutomaticAction{Status: "pending"}},
		{QueuedAt: now.Add(-time.Minute), Action: WorkflowRecoveryAutomaticAction{Status: "pending", LastEvaluation: &WorkflowRecoveryAutomaticEvaluation{EvaluatedAt: now, Reason: "execution_quota"}}},
	}
	q.Pending = 2
	if got := RecoveryPolicyHealth(q, 300); got.Status != "blocked" || got.Reason != "execution_quota" {
		t.Fatalf("backlog falsely stalled %+v", got)
	}
	q.Outstanding = 1
	q.Entries[1].Action.Status = "applied"
	q.Entries[1].Action.LastEvaluation.Reason = "retry_running"
	if got := RecoveryPolicyHealth(q, 300); got.Status != "blocked" || got.Reason != "outstanding_retry" {
		t.Fatalf("serial wait falsely stalled %+v", got)
	}
	for _, seconds := range []int{0, WorkflowRecoveryHealthMaxStallSeconds + 1} {
		if ValidateRecoveryHealthThreshold(seconds) == nil {
			t.Fatal("accepted invalid threshold")
		}
	}
}
