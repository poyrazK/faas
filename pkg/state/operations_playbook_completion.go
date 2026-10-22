package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Observe the frozen escalation expectation using exact scoped identity, independently of watch retirement.
func readPlaybookCompletionOutcome(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope string, n api.OperationWorkflowReadinessNotification, resolution string, operator bool) (*api.OperationWorkflowPlaybookCompletionOutcome, error) {
	source, escalation := n.PlaybookEvidence, n.PlaybookEscalation
	opts := api.OperationMilestoneListOptions{AppID: app, Scope: scope, SubjectType: source.Subject.Type, SubjectID: source.Subject.ID, Workflow: source.Workflow, WorkflowInstanceID: source.InstanceID, Limit: 1}
	var page api.OperationMilestonesResponse
	var err error
	if operator {
		opts.TenantID = source.PlatformTenantID
		page, err = store.ListAccountOperationMilestones(ctx, account, opts)
	} else {
		page, err = store.ListPlatformTenantOperationMilestones(ctx, account, tenant, opts)
	}
	if err != nil {
		return nil, err
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	// Reject a retained report older than either the delivery or latest accepted escalation evidence.
	revision := escalation.Current.Evidence.StateRevision
	if source.BusinessOutcome != nil && source.BusinessOutcome.Evidence.StateRevision > revision {
		revision = source.BusinessOutcome.Evidence.StateRevision
	}
	evidence, reason := observeWorkflowBusinessOutcome(page.WorkflowInstance, escalation.Current.Evidence.ExpectedOutcomeCode, source.ContractVersion, revision, at)
	status := evidence.Status
	if status == "unknown" {
		status = "unavailable"
	}
	result := &api.OperationWorkflowPlaybookCompletionOutcome{Resolution: resolution, Alignment: "unavailable", Explanation: "Current application evidence cannot determine whether the declared resolution matches the expected result.", Observation: api.OperationWorkflowPlaybookBusinessOutcome{Status: status, Reason: reason, Evidence: evidence, DeadlineAt: &escalation.DeadlineAt, DeadlineReached: !at.Before(escalation.DeadlineAt)}}
	result.Observation.OutcomeGuidance = append([]api.OperationWorkflowPlaybookOutcomeGuidance(nil), escalation.Current.OutcomeGuidance...)
	projectPlaybookOutcomeGuidance(&result.Observation)
	classifyPlaybookOutcomeAcceptance(&result.Observation, escalation.Current.AcceptancePolicy)
	switch {
	case resolution == "follow_up_canceled":
		result.Alignment, result.Explanation = "not_applicable", "The follow-up was canceled; no business success is asserted."
	case status == "unavailable":
	case resolution == "expected_outcome_achieved" && status == "achieved":
		result.Alignment, result.Explanation = "matches", "The application currently reports the frozen expected terminal outcome."
	case resolution == "alternative_accepted" && status == "different_outcome" && (result.Observation.AcceptancePolicy == nil || result.Observation.Acceptance == "acceptable_alternative"):
		result.Alignment, result.Explanation = "matches", "The application currently reports a different terminal outcome, accepted by the reviewer."
		if result.Observation.AcceptancePolicy != nil {
			result.Explanation = "The application reports a terminal alternative explicitly allowed by the pinned playbook policy."
		}
	default:
		result.Alignment, result.Explanation = "mismatch", "The declared resolution differs from the current application-reported business result."
	}
	return result, nil
}
