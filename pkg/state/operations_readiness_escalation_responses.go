package state

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Response checks need only the retained inbox; business report failures do not suppress coordination deadlines.
func readinessEscalationResponseUpdates(items []api.OperationWorkflowReadinessNotification, r *readinessSubscriptionRecord) []readinessWatchUpdate {
	candidates := []api.OperationWorkflowReadinessNotification{}
	for _, n := range items {
		if _, response := escalationResponse(n); response != nil && response.Status == "awaiting" {
			candidates = append(candidates, n)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].Sequence > r.ResponseCursor, candidates[j].Sequence > r.ResponseCursor
		if a != b {
			return a
		}
		return candidates[i].Sequence < candidates[j].Sequence
	})
	if len(candidates) > api.OperationWorkflowReadinessEscalationResponsesPerPassMax {
		candidates = candidates[:api.OperationWorkflowReadinessEscalationResponsesPerPassMax]
	}
	updates := []readinessWatchUpdate{}
	for _, n := range candidates {
		updates = append(updates, readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, ResponseWatchID: escalationResponseWatchID(n)})
		r.ResponseCursor = n.Sequence
	}
	return updates
}

// Called under the subscription and exact notification lock; recheck live coordination before taking action.
func applyReadinessEscalationResponse(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	watchID, response := escalationResponse(n)
	if n.ID != u.SourceID || watchID != u.ResponseWatchID || response == nil || n.Coordination == nil {
		return n, nil, false
	}
	if response.Status != "awaiting" || response.DeadlineNotifiedAt != nil {
		return n, nil, false
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	// Ordinary response targets accept acknowledgment. Dependency review targets
	// require the refreshed source decision, independently of inbox coordination.
	if (isPlaybookDependencyReview(n) && n.PlaybookStep.DependencyReviewStatus != "awaiting") || (!isPlaybookDependencyReview(n) && (n.Coordination.Status == "acknowledged" || n.Coordination.Completion != nil || n.Coordination.FollowUpAt == nil)) {
		n = readinessEscalationCoordination(n, n.Coordination, n.Coordination.UpdatedBy, now)
		return n, nil, true
	}
	if now.Before(response.DeadlineAt) {
		return n, nil, false
	}
	copied := *response
	copied.Status, copied.DeadlineNotifiedAt = "deadline_missed", &now
	n = withEscalationResponse(n, &copied)
	reassigned := false
	if n.Coordination.Completion == nil && copied.FallbackOwner != "" && copied.FallbackOwner != copied.InitialOwner && n.Coordination.Owner == copied.InitialOwner && len(copied.History) == 0 && !copied.HistoryTruncated && n.Coordination.Revision < api.OperationWorkflowReadinessCoordinationRevisionMax {
		c := *n.Coordination
		c.Owner, c.UpdatedBy, c.UpdatedAt = copied.FallbackOwner, "system:escalation_response", now
		c.Revision++
		appendReadinessAssignment(&copied, api.OperationWorkflowReadinessAssignmentChange{FromOwner: n.Coordination.Owner, ToOwner: c.Owner, At: now, Actor: c.UpdatedBy, Reason: "response_deadline_fallback"})
		if n.PlaybookStep != nil {
			step := *n.PlaybookStep
			step.ManualOwner = true
			appendPlaybookStepAssignmentHistory(&step, api.OperationWorkflowReadinessAssignmentChange{FromOwner: n.Coordination.Owner, ToOwner: c.Owner, At: now, Actor: c.UpdatedBy, Reason: "response_deadline_fallback"})
			n.PlaybookStep = &step
		}
		n.Coordination = &c
		reassigned = true
	}
	summary := "The assigned escalation was not acknowledged by its response deadline. Consult coordination history; business execution remains application-owned."
	if isPlaybookDependencyReview(n) {
		summary = "The dependency validity review was not resolved by its pinned deadline. Confirm the work remains valid or request redo in the workflow case; inbox acknowledgment alone does not resolve the review."
	}
	if n.BusinessImpact != nil {
		summary = "The business-impact alert was not acknowledged by its response deadline. Review assignment history and the matching queue; alert acknowledgment is separate from resolving underlying review obligations."
	}
	notice := readinessSubscriptionNotify(r, "escalation_response_overdue", summary, nil)

	if n.PlaybookEvidence != nil {
		source := *n.PlaybookEvidence
		source.Changes = []api.OperationWorkflowPlaybookEvidenceChange{}
		source.OutcomeChange = nil
		notice.PlaybookEvidence = &source
	}
	if n.BusinessImpact != nil {
		source := *n.BusinessImpact
		source.Response = nil
		notice.BusinessImpact = &source
	}
	notice.EscalationResponse = &api.OperationWorkflowReadinessEscalationResponseEvent{EscalationNotificationID: n.ID, EscalationSequence: n.Sequence, BusinessImpact: notice.BusinessImpact, Source: readinessNotificationBaseline(n), Playbook: notice.PlaybookEvidence, CheckedAt: now, DeadlineAt: copied.DeadlineAt, Owner: n.Coordination.Owner, Reassigned: reassigned}
	return n, []api.OperationWorkflowReadinessNotification{notice}, true
}

func appendReadinessAssignment(response *api.OperationWorkflowReadinessEscalationResponse, change api.OperationWorkflowReadinessAssignmentChange) {
	response.History = append(append([]api.OperationWorkflowReadinessAssignmentChange{}, response.History...), change)
	if len(response.History) > api.OperationWorkflowReadinessAssignmentHistoryMax {
		response.History = response.History[len(response.History)-api.OperationWorkflowReadinessAssignmentHistoryMax:]
		response.HistoryTruncated = true
	}
}

// Human coordination updates record assignment history and fulfill or stop the original response target.
func readinessEscalationCoordination(n api.OperationWorkflowReadinessNotification, c *api.OperationWorkflowReadinessCoordination, actor string, at time.Time) api.OperationWorkflowReadinessNotification {
	_, current := escalationResponse(n)
	if current == nil {
		return n
	}
	response := *current
	if n.Coordination != nil && n.Coordination.Owner != c.Owner {
		appendReadinessAssignment(&response, api.OperationWorkflowReadinessAssignmentChange{FromOwner: n.Coordination.Owner, ToOwner: c.Owner, At: at, Actor: actor, Reason: "manual_assignment"})
	}
	if c.Status == "acknowledged" && response.FirstAcknowledgedAt == nil {
		response.FirstAcknowledgedAt = &at
	}
	if isPlaybookDependencyReview(n) {
		if n.PlaybookStep.DependencyReviewStatus != "awaiting" {
			response.Status = "closed"
		}
	} else if c.Completion != nil || c.FollowUpAt == nil {
		response.Status = "closed"
	} else if response.FirstAcknowledgedAt != nil {
		response.Status = "acknowledged"
	}
	n = withEscalationResponse(n, &response)
	return n
}

// Assigned steps and both escalation families share response semantics without inventing a readiness baseline.
func escalationResponse(n api.OperationWorkflowReadinessNotification) (string, *api.OperationWorkflowReadinessEscalationResponse) {
	if n.BusinessImpact != nil {
		return n.ID, n.BusinessImpact.Response
	}
	if n.PlaybookStep != nil {
		return n.ID, n.PlaybookStep.Response
	}
	if n.PlaybookEscalation != nil {
		return n.PlaybookEscalation.WatchID, n.PlaybookEscalation.Response
	}
	if n.OutcomeEscalation != nil {
		return n.OutcomeEscalation.WatchID, n.OutcomeEscalation.Response
	}
	return "", nil
}
func escalationResponseWatchID(n api.OperationWorkflowReadinessNotification) string {
	id, _ := escalationResponse(n)
	return id
}
func withEscalationResponse(n api.OperationWorkflowReadinessNotification, response *api.OperationWorkflowReadinessEscalationResponse) api.OperationWorkflowReadinessNotification {
	if n.BusinessImpact != nil {
		event := *n.BusinessImpact
		event.Response = response
		n.BusinessImpact = &event
	} else if n.PlaybookStep != nil {
		step := *n.PlaybookStep
		step.Response = response
		n.PlaybookStep = &step
	} else if n.PlaybookEscalation != nil {
		e := *n.PlaybookEscalation
		e.Response = response
		n.PlaybookEscalation = &e
	} else if n.OutcomeEscalation != nil {
		e := *n.OutcomeEscalation
		e.Response = response
		n.OutcomeEscalation = &e
	}
	return n
}

func isPlaybookDependencyReview(n api.OperationWorkflowReadinessNotification) bool {
	return n.PlaybookStep != nil && n.PlaybookStep.DependencyReview != nil
}
