package state

import (
	"fmt"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
)

func recoveryQueueEvidence(slug string, d api.WorkflowRetryEvidenceDiagnosis) *api.WorkflowRecoveryQueueEvidence {
	base := "/v1/apps/" + url.PathEscape(slug) + "/recovery-policy/reviews/" + d.ReviewID + "/actions/" + d.ActionID + "/evidence"
	command := fmt.Sprintf("gregale automations recovery-retry-evidence --app %s --review %s --action %s", slug, d.ReviewID, d.ActionID)
	if d.EvidenceKind == "parent" {
		base += "/parent"
		command += " --parent"
	}
	return &api.WorkflowRecoveryQueueEvidence{Diagnosis: d, EvidenceURL: base, CLICommand: command}
}

// Operational waits are the last worker observation, except current policy controls.
// Fresh proof is independent: neither field asserts eligibility or runs a worker.
func recoveryQueueWait(p api.WorkflowRecoveryPolicyResponse, entry *api.WorkflowRecoveryAutomaticQueueEntry) {
	reason := entry.Action.Reason
	if entry.Action.LastEvaluation != nil {
		reason = entry.Action.LastEvaluation.Reason
	}
	category := "worker"
	if entry.Action.Status == "pending" && !p.Policy.Enabled {
		category, reason = "policy", "policy_disabled"
	} else if entry.Action.Status == "pending" && p.AutomaticExecution != nil && p.AutomaticExecution.PausedAt != nil {
		category, reason = "policy", "automatic_paused"
	} else {
		switch reason {
		case "hourly_budget", "hourly_execution_budget":
			category = "budget"
		case "cooldown", "execution_cooldown":
			category = "cooldown"
		case "evidence_unknown", "terminal_outcome_not_saved":
			category = "evidence"
		case "automatic_paused", "policy_changed", "policy_disabled", "job_paused":
			category = "policy"
		case "execution_quota", "admission_wait", "execution_wait", "retry_running", "recovery_running", "outstanding_retry", "outstanding_recovery":
			category = "execution"
		}
	}
	entry.WaitCategory, entry.WaitReason = category, reason
}

func recoveryInitialQueueEvidence(r api.WorkflowInitialRecoveryReview, d api.WorkflowInitialEvidenceDiagnosis) *api.WorkflowRecoveryInitialQueueEvidence {
	stage := "prepared"
	if r.SubmittedAt != nil {
		stage = "submitted"
	}
	return &api.WorkflowRecoveryInitialQueueEvidence{Stage: stage, Diagnosis: d, EvidenceURL: "/v1/apps/" + url.PathEscape(r.App) + "/initial-recovery-reviews/" + r.ID + "/evidence", CLICommand: fmt.Sprintf("gregale automations failure-recovery-evidence --app %s --review %s", r.App, r.ID)}
}
