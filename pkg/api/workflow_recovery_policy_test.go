package api

import (
	"testing"
	"time"
)

func TestWorkflowRecoveryPolicyValidation(t *testing.T) {
	p := WorkflowRecoveryPolicy{Mode: "preview", WorkflowNames: []string{"recover"}, Triggers: []string{"saved_failure"}, MaxResumeCount: 3, CooldownSeconds: 60, HourlyActionBudget: 10, RatePerSecond: 1, Reason: "incident"}
	if err := (SetWorkflowRecoveryPolicyRequest{Policy: p}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"mode", "names", "trigger", "status", "resume", "cooldown", "budget", "reason"} {
		t.Run(scenario, func(t *testing.T) {
			bad := p
			switch scenario {
			case "mode":
				bad.Mode = "automatic"
			case "names":
				bad.WorkflowNames = []string{"recover", "recover"}
			case "trigger":
				bad.Triggers = []string{"unknown"}
			case "status":
				bad.FailureStatuses = []string{"running"}
			case "resume":
				bad.MaxResumeCount = 17
			case "cooldown":
				bad.CooldownSeconds = 0
			case "budget":
				bad.HourlyActionBudget = 101
			case "reason":
				bad.Reason = " "
			}
			if (SetWorkflowRecoveryPolicyRequest{Policy: bad}).Validate() == nil {
				t.Fatal("invalid policy accepted")
			}
		})
	}
}

func TestWorkflowRecoveryAutomaticPolicyValidation(t *testing.T) {
	p := WorkflowRecoveryPolicy{Mode: "automatic_retry", WorkflowNames: []string{"recover"}, Triggers: []string{"saved_failure"}, FailureStatuses: []string{"dead"}, MaxResumeCount: 3, CooldownSeconds: 60, HourlyActionBudget: 10, HourlyExecutionBudget: 2, PauseAfterFailures: 2, RatePerSecond: 1, Reason: "bounded incident recovery"}
	if err := (SetWorkflowRecoveryPolicyRequest{Policy: p}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"cancellation", "implicit_statuses", "no_budget", "large_budget", "no_pause", "large_pause", "preview_limits"} {
		t.Run(scenario, func(t *testing.T) {
			bad := p
			switch scenario {
			case "cancellation":
				bad.Triggers = []string{"saved_failure", "stalled"}
			case "implicit_statuses":
				bad.FailureStatuses = nil
			case "no_budget":
				bad.HourlyExecutionBudget = 0
			case "large_budget":
				bad.HourlyExecutionBudget = 101
			case "no_pause":
				bad.PauseAfterFailures = 0
			case "large_pause":
				bad.PauseAfterFailures = 11
			case "preview_limits":
				bad.Mode = "preview"
			}
			if (SetWorkflowRecoveryPolicyRequest{Policy: bad}).Validate() == nil {
				t.Fatal("unbounded or implicit automatic authority accepted")
			}
		})
	}
}

func TestWorkflowRecoveryPolicyControlValidation(t *testing.T) {
	valid := ControlWorkflowRecoveryPolicyRequest{ExpectedVersion: 1, ExpectedUpdatedAt: time.Now().UTC(), Operation: "pause", Reason: "investigate"}
	cases := []struct {
		name   string
		modify func(*ControlWorkflowRecoveryPolicyRequest)
	}{
		{"version", func(r *ControlWorkflowRecoveryPolicyRequest) { r.ExpectedVersion = 0 }},
		{"timestamp", func(r *ControlWorkflowRecoveryPolicyRequest) { r.ExpectedUpdatedAt = time.Time{} }},
		{"operation", func(r *ControlWorkflowRecoveryPolicyRequest) { r.Operation = "cancel" }},
		{"reason", func(r *ControlWorkflowRecoveryPolicyRequest) { r.Reason = " " }},
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	valid.Operation = "resume"
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := valid
			c.modify(&r)
			if r.Validate() == nil {
				t.Fatal("invalid control accepted")
			}
		})
	}
}

func TestWorkflowRecoverySuppressionValidation(t *testing.T) {
	for _, r := range []SuppressWorkflowRecoveryAutomaticActionRequest{{ExpectedPolicyVersion: 0, Reason: "skip"}, {ExpectedPolicyVersion: 1, Reason: " "}} {
		if r.Validate() == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	if err := (SuppressWorkflowRecoveryAutomaticActionRequest{ExpectedPolicyVersion: 1, Reason: "handled externally"}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowRecoveryInitialAutomaticPolicyValidation(t *testing.T) {
	p := WorkflowRecoveryPolicy{Mode: "automatic_initial", WorkflowNames: []string{"recover"}, Triggers: []string{"initial_failure"}, FailureStatuses: []string{"dead"}, MaxResumeCount: 1, CooldownSeconds: 60, HourlyActionBudget: 10, HourlyExecutionBudget: 1, PauseAfterFailures: 1, RatePerSecond: 1, Reason: "bounded first recovery"}
	if err := (SetWorkflowRecoveryPolicyRequest{Policy: p}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"mixed_triggers", "retry_trigger", "missing_statuses", "missing_budget", "missing_pause"} {
		t.Run(scenario, func(t *testing.T) {
			bad := p
			switch scenario {
			case "mixed_triggers":
				bad.Triggers = []string{"initial_failure", "saved_failure"}
			case "retry_trigger":
				bad.Triggers = []string{"saved_failure"}
			case "missing_statuses":
				bad.FailureStatuses = nil
			case "missing_budget":
				bad.HourlyExecutionBudget = 0
			case "missing_pause":
				bad.PauseAfterFailures = 0
			}
			if (SetWorkflowRecoveryPolicyRequest{Policy: bad}).Validate() == nil {
				t.Fatal("unsafe automatic authority accepted")
			}
		})
	}
}

func TestWorkflowRecoveryInitialAutomaticHealth(t *testing.T) {
	now := time.Now().UTC()
	q := WorkflowRecoveryAutomaticQueue{ObservedAt: now, Policy: WorkflowRecoveryPolicyResponse{Policy: WorkflowRecoveryPolicy{Enabled: true, Mode: "automatic_initial"}}, Pending: 1, RemainingBudget: 1, Entries: []WorkflowRecoveryAutomaticQueueEntry{{QueuedAt: now, Action: WorkflowRecoveryAutomaticAction{Status: "pending", LastEvaluation: &WorkflowRecoveryAutomaticEvaluation{EvaluatedAt: now, Reason: "outstanding_recovery"}}}}}
	h := RecoveryPolicyHealth(q, 300)
	if h.Status != "blocked" || h.Reason != "outstanding_recovery" {
		t.Fatalf("initial health %+v", h)
	}
	q.Entries = nil
	q.Pending = 0
	if h = RecoveryPolicyHealth(q, 300); h.Status != "healthy" {
		t.Fatalf("initial automatic mode inactive %+v", h)
	}
}
