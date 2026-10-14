package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type initialPolicyChangePressure struct {
	retained   int
	active     int
	unresolved bool
	hourly     int
	last       *time.Time
}

func initialPolicyChangeEnabled(p api.WorkflowRecoveryPolicy) bool {
	return (p.Mode == "preview" || p.Mode == "automatic_initial") && recoveryPolicyHas(p.Triggers, "initial_failure")
}
func initialPolicyChangeMatches(p api.WorkflowRecoveryPolicy, f api.WorkflowInitialFailure) bool {
	return recoveryPolicyHas(p.WorkflowNames, f.Workflow) && recoveryPolicyHas(p.FailureStatuses, f.FailureStatus)
}
func initialPolicyChangeExcluded(reason string) bool {
	switch reason {
	case "policy_disabled", "workflow_excluded", "failure_status_excluded", "trigger_excluded", "initial_failure_requires_preview_or_automatic_initial":
		return true
	}
	return false
}
func newInitialPolicyChangePreview(pressure initialPolicyChangePressure) *api.WorkflowRecoveryInitialPolicyChangePreview {
	return &api.WorkflowRecoveryInitialPolicyChangePreview{Coverage: "partial", RetainedReviews: pressure.retained, RemainingReviewCapacity: max(0, api.WorkflowRecoveryPolicyReviewsMax-pressure.retained), ActiveRecoveryJobs: pressure.active, RemainingRecoveryJobCapacity: max(0, api.EventRecoveryActiveJobsMax-pressure.active), UnresolvedRecovery: pressure.unresolved, Candidates: []api.WorkflowRecoveryInitialPolicyChangeCandidate{}}
}
func addInitialPolicyChangeCandidate(out *api.WorkflowRecoveryPolicyChangePreview, preview api.WorkflowInitialRecoveryPreview, pending bool, budget *recoveryPolicyBudget, pressure *initialPolicyChangePressure) {
	sample := out.InitialFailures
	p := out.Proposed
	c := api.WorkflowRecoveryInitialPolicyChangeCandidate{Failure: preview.Failure, Eligibility: api.WorkflowInitialFailurePolicyReason(p, preview), Preparation: "not_eligible", Execution: "not_prepared", Blockers: append([]string{}, preview.Blockers...)}
	if c.Eligibility != "eligible" {
		if initialPolicyChangeExcluded(c.Eligibility) {
			sample.Excluded++
		} else {
			sample.SafetyBlocked++
		}
	} else {
		sample.Eligible++
		target := *preview.Target
		c.Target = &target
		c.Preparation = "would_prepare"
		switch {
		case pending:
			c.Preparation = "pending_recovery"
		case pressure.retained >= api.WorkflowRecoveryPolicyReviewsMax:
			c.Preparation = "review_limit"
		default:
			if reason := initialReviewBudgetReason(budget.Hourly, budget.Last, p, out.ObservedAt); reason != "" {
				c.Preparation = reason
			}
		}
		if c.Preparation == "would_prepare" {
			sample.WouldPrepare++
			pressure.retained++
			budget.Hourly++
			budget.Last = out.ObservedAt
			c.Execution = "not_enabled"
			if p.Mode == "automatic_initial" {
				// Replacing the policy clears its pause; charges and unresolved work survive.
				authority := api.WorkflowRecoveryPolicyResponse{Version: out.NextVersion, Policy: p, AutomaticExecution: &api.WorkflowRecoveryPolicyAutomaticExecution{}}
				c.Execution = initialAutomaticPressureReason(authority, pressure.hourly, pressure.last, pressure.unresolved, out.ObservedAt)
				if c.Execution == "" && pressure.active >= api.EventRecoveryActiveJobsMax {
					c.Execution = "execution_quota"
				}
				if c.Execution == "" {
					c.Execution = "would_submit"
					sample.WouldSubmit++
					pressure.hourly++
					pressure.active++
					pressure.unresolved = true
					at := out.ObservedAt
					pressure.last = &at
				}
			}
		}
	}
	sample.InspectedFailures++
	sample.Candidates = append(sample.Candidates, c)
}
