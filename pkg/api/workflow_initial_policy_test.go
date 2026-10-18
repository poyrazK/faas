package api

import "testing"

func TestInitialFailurePolicyDryRun(t *testing.T) {
	p := WorkflowRecoveryPolicy{Enabled: true, Mode: "preview", WorkflowNames: []string{"recover"}, Triggers: []string{"initial_failure"}, FailureStatuses: []string{"failed"}, MaxResumeCount: 3, CooldownSeconds: 60, HourlyActionBudget: 10, RatePerSecond: 1, Reason: "incident"}
	preview := WorkflowInitialRecoveryPreview{Failure: WorkflowInitialFailure{ID: "evidence", RunID: "run", Workflow: "recover", FailureStatus: "failed"}, Reason: "eligible", Target: &WorkflowRunRecoveryTarget{RunID: "run", FailureEvidenceID: "evidence"}}
	for _, tc := range []struct{ name, want string }{
		{"eligible", "eligible"}, {"disabled", "policy_disabled"}, {"trigger", "trigger_excluded"}, {"workflow", "workflow_excluded"}, {"status", "failure_status_excluded"}, {"blocked", "unsafe_resume"}, {"target", "initial_failure_evidence_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate, observation := p, preview
			switch tc.name {
			case "disabled":
				candidate.Enabled = false
			case "trigger":
				candidate.Triggers = []string{"saved_failure"}
			case "workflow":
				candidate.WorkflowNames = []string{"other"}
			case "status":
				candidate.FailureStatuses = []string{"dead"}
			case "blocked":
				observation.Reason = "unsafe_resume"
				observation.Target = nil
			case "target":
				observation.Target = &WorkflowRunRecoveryTarget{RunID: "other", FailureEvidenceID: "evidence"}
			}
			if got := WorkflowInitialFailurePolicyReason(candidate, observation); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
	if err := (SetWorkflowRecoveryPolicyRequest{Policy: p}).Validate(); err != nil {
		t.Fatal(err)
	}
	p.FailureStatuses = nil
	if (SetWorkflowRecoveryPolicyRequest{Policy: p}).Validate() == nil {
		t.Fatal("implicit statuses accepted")
	}
	p.FailureStatuses = []string{"failed"}
	p.Mode = "automatic_retry"
	p.HourlyExecutionBudget = 1
	p.PauseAfterFailures = 1
	if (SetWorkflowRecoveryPolicyRequest{Policy: p}).Validate() == nil {
		t.Fatal("automatic initial failure accepted")
	}
}
