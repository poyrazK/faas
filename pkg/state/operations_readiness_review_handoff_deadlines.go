package state

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type reviewHandoffDeadlineUpdate struct {
	CoordinationRevision                       int64
	DecisionAt, RequiredAt, HandedOffAt, DueAt time.Time
	Owner                                      string
}

func effectiveHandoffAcceptanceWindow(response *api.OperationWorkflowReviewResponse) int64 {
	if response.HandoffAcceptAfterSeconds >= 60 && response.HandoffAcceptAfterSeconds <= 604800 {
		return response.HandoffAcceptAfterSeconds
	}
	return api.OperationWorkflowReviewResponseHandoffAcceptDefaultSeconds
}
func pendingReviewHandoff(n api.OperationWorkflowReadinessNotification) bool {
	response := n.ReviewResponse
	if n.Kind != "business_impact_changed" || response == nil || response.Status != "active" || response.Decision == nil || response.DecisionReview == nil || n.Coordination == nil || n.Coordination.Completion != nil {
		return false
	}
	review := response.DecisionReview
	return review.Reassigned && review.DeadlineNotifiedAt != nil && review.Acceptance == nil && review.Decline == nil && review.AcceptanceDeadlineNotifiedAt == nil && review.Owner != "" && response.Owner == review.Owner && n.Coordination.Owner == review.Owner
}

// Deadline checks rotate independently of application-state reads and other targets.
func reviewHandoffDeadlineUpdates(items []api.OperationWorkflowReadinessNotification, r *readinessSubscriptionRecord) []readinessWatchUpdate {
	candidates := []api.OperationWorkflowReadinessNotification{}
	for _, n := range items {
		if pendingReviewHandoff(n) {
			candidates = append(candidates, n)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].Sequence > r.HandoffDeadlineCursor, candidates[j].Sequence > r.HandoffDeadlineCursor
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
		review := pinnedDecisionReview(n.ReviewResponse)
		updates = append(updates, readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, HandoffDeadline: &reviewHandoffDeadlineUpdate{CoordinationRevision: n.Coordination.Revision, DecisionAt: review.DecisionAt, RequiredAt: review.RequiredAt, HandedOffAt: *reviewHandoffTime(review), DueAt: *review.AcceptanceDueAt, Owner: review.Owner}})
		r.HandoffDeadlineCursor = n.Sequence
	}
	return updates
}

// Acceptance and superseding human changes are checked again under source/subscription locks.
func applyReviewHandoffDeadline(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	if n.ID != u.SourceID || !pendingReviewHandoff(n) {
		return n, nil, false
	}
	response := n.ReviewResponse
	review := pinnedDecisionReview(response)
	target := u.HandoffDeadline
	if n.Coordination.Revision != target.CoordinationRevision || !response.Decision.At.Equal(target.DecisionAt) || !review.DecisionAt.Equal(target.DecisionAt) || !review.RequiredAt.Equal(target.RequiredAt) || !reviewHandoffTime(review).Equal(target.HandedOffAt) || !review.AcceptanceDueAt.Equal(target.DueAt) || review.Owner != target.Owner {
		return n, nil, false
	}
	updated := *response
	updated.DecisionReview = review
	n.ReviewResponse = &updated
	now := time.Now().UTC().Truncate(time.Microsecond)
	if now.Before(*review.AcceptanceDueAt) {
		return n, nil, true
	}
	review.AcceptanceDeadlineNotifiedAt = &now
	escalate := review.AcceptanceEscalationOwner != "" && review.AcceptanceEscalationOwner != response.Owner
	if escalate {
		review.AcceptanceEscalatedAt = &now
	}
	notice := readinessSubscriptionNotify(r, "review_response_handoff_acceptance_overdue", "The handed-off review was not explicitly accepted within its acceptance window. Record acceptance with a note and future check-in, or review responsibility; acknowledgment does not accept the handoff and the original decision review deadline remains in force.", nil)
	if n.BusinessImpact != nil {
		impact := *n.BusinessImpact
		impact.Response = nil
		notice.BusinessImpact = &impact
	}
	notice.DecisionReview = &api.OperationWorkflowReviewResponseDecisionReviewEvent{SourceNotificationID: n.ID, SourceSequence: n.Sequence, Decision: *response.Decision, Review: *review}
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: response.Owner, UpdatedAt: now, UpdatedBy: "system:handoff-acceptance-deadline"}
	notices := []api.OperationWorkflowReadinessNotification{notice}
	if escalate {
		escalation := readinessSubscriptionNotify(r, "review_response_handoff_acceptance_escalated", "The assigned team missed its handoff acceptance deadline. Review the routing and decline history and explicitly coordinate the next action; ownership and the original decision review deadline remain unchanged.", nil)
		escalation.BusinessImpact = notice.BusinessImpact
		escalation.DecisionReview = notice.DecisionReview
		escalation.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: review.AcceptanceEscalationOwner, UpdatedAt: now, UpdatedBy: "system:handoff-acceptance-escalation"}
		notices = append(notices, escalation)
	}
	return n, notices, true
}

func reviewHandoffTime(review *api.OperationWorkflowReviewResponseDecisionReview) *time.Time {
	if review.HandedOffAt != nil {
		return review.HandedOffAt
	}
	return review.DeadlineNotifiedAt
}
func matchesReviewHandoffTime(review *api.OperationWorkflowReviewResponseDecisionReview, supplied *time.Time) bool {
	if supplied == nil {
		return review.Reassignment == nil
	}
	current := reviewHandoffTime(review)
	return current != nil && current.Equal(*supplied)
}
