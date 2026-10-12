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
		if n.OutcomeEscalation != nil && n.OutcomeEscalation.Response != nil && n.OutcomeEscalation.Response.Status == "awaiting" && readinessNotificationBaseline(n) != nil {
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
		updates = append(updates, readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, ResponseWatchID: n.OutcomeEscalation.WatchID})
		r.ResponseCursor = n.Sequence
	}
	return updates
}

// Called under the subscription and exact notification lock; recheck live coordination before taking action.
func applyReadinessEscalationResponse(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	if n.ID != u.SourceID || n.OutcomeEscalation == nil || n.OutcomeEscalation.WatchID != u.ResponseWatchID || n.OutcomeEscalation.Response == nil || n.Coordination == nil {
		return n, nil, false
	}
	response := n.OutcomeEscalation.Response
	if response.Status != "awaiting" || response.DeadlineNotifiedAt != nil {
		return n, nil, false
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	// An acknowledgment, completion or cleared follow-up wins even when it arrived after the read phase.
	if n.Coordination.Status == "acknowledged" || n.Coordination.Completion != nil || n.Coordination.FollowUpAt == nil {
		n = readinessEscalationCoordination(n, n.Coordination, n.Coordination.UpdatedBy, now)
		return n, nil, true
	}
	if now.Before(response.DeadlineAt) {
		return n, nil, false
	}
	e := *n.OutcomeEscalation
	copied := *response
	copied.Status, copied.DeadlineNotifiedAt = "deadline_missed", &now
	e.Response = &copied
	n.OutcomeEscalation = &e
	reassigned := false
	if copied.FallbackOwner != "" && n.Coordination.Owner == copied.InitialOwner && len(copied.History) == 0 && !copied.HistoryTruncated && n.Coordination.Revision < api.OperationWorkflowReadinessCoordinationRevisionMax {
		c := *n.Coordination
		c.Owner, c.UpdatedBy, c.UpdatedAt = copied.FallbackOwner, "system:escalation_response", now
		c.Revision++
		appendReadinessAssignment(&copied, api.OperationWorkflowReadinessAssignmentChange{FromOwner: n.Coordination.Owner, ToOwner: c.Owner, At: now, Actor: c.UpdatedBy, Reason: "response_deadline_fallback"})
		n.Coordination = &c
		reassigned = true
	}
	notice := readinessSubscriptionNotify(r, "escalation_response_overdue", "The assigned escalation was not acknowledged by its response deadline. Consult coordination history; business execution remains application-owned.", nil)
	notice.EscalationResponse = &api.OperationWorkflowReadinessEscalationResponseEvent{EscalationNotificationID: n.ID, EscalationSequence: n.Sequence, Source: *readinessNotificationBaseline(n), CheckedAt: now, DeadlineAt: copied.DeadlineAt, Owner: n.Coordination.Owner, Reassigned: reassigned}
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
	if n.OutcomeEscalation == nil || n.OutcomeEscalation.Response == nil {
		return n
	}
	e := *n.OutcomeEscalation
	response := *e.Response
	if n.Coordination != nil && n.Coordination.Owner != c.Owner {
		appendReadinessAssignment(&response, api.OperationWorkflowReadinessAssignmentChange{FromOwner: n.Coordination.Owner, ToOwner: c.Owner, At: at, Actor: actor, Reason: "manual_assignment"})
	}
	if c.Status == "acknowledged" && response.FirstAcknowledgedAt == nil {
		response.FirstAcknowledgedAt = &at
	}
	if c.Completion != nil || c.FollowUpAt == nil {
		response.Status = "closed"
	} else if response.FirstAcknowledgedAt != nil {
		response.Status = "acknowledged"
	}
	e.Response = &response
	n.OutcomeEscalation = &e
	return n
}
