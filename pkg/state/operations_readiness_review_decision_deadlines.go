package state

import (
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type reviewDecisionDeadlineUpdate struct{ DecisionAt, RequiredAt, DueAt time.Time }

func effectiveDecisionReviewWindow(response *api.OperationWorkflowReviewResponse) int64 {
	if response.DecisionReviewAfterSeconds >= 60 && response.DecisionReviewAfterSeconds <= 604800 {
		return response.DecisionReviewAfterSeconds
	}
	return api.OperationWorkflowReviewResponseDecisionReviewDefaultSeconds
}
func pinnedDecisionReview(response *api.OperationWorkflowReviewResponse) *api.OperationWorkflowReviewResponseDecisionReview {
	if response.DecisionReview == nil {
		return nil
	}
	review := *response.DecisionReview
	if review.DueAt == nil {
		due := review.RequiredAt.Add(time.Duration(effectiveDecisionReviewWindow(response)) * time.Second)
		review.DueAt = &due
	}
	if review.Reassigned && review.AcceptanceDueAt == nil && review.DeadlineNotifiedAt != nil {
		due := reviewHandoffTime(&review).Add(time.Duration(effectiveHandoffAcceptanceWindow(response)) * time.Second)
		review.AcceptanceDueAt = &due
	}
	return &review
}

// Independent rotation keeps deadlines observable even when scoped case reads fail.
func reviewDecisionDeadlineUpdates(items []api.OperationWorkflowReadinessNotification, r *readinessSubscriptionRecord) []readinessWatchUpdate {
	candidates := []api.OperationWorkflowReadinessNotification{}
	for _, n := range items {
		if n.Kind == "business_impact_changed" && n.ReviewResponse != nil && n.ReviewResponse.Status == "active" && n.ReviewResponse.Decision != nil && n.ReviewResponse.DecisionReview != nil && n.ReviewResponse.DecisionReview.DeadlineNotifiedAt == nil {
			candidates = append(candidates, n)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].Sequence > r.ReviewDecisionCursor, candidates[j].Sequence > r.ReviewDecisionCursor
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
		updates = append(updates, readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, DecisionDeadline: &reviewDecisionDeadlineUpdate{DecisionAt: review.DecisionAt, RequiredAt: review.RequiredAt, DueAt: *review.DueAt}})
		r.ReviewDecisionCursor = n.Sequence
	}
	return updates
}

// Publish once under the source and subscription locks. Inbox acknowledgment is irrelevant.
func applyReviewDecisionDeadline(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	response := n.ReviewResponse
	if n.ID != u.SourceID || response == nil || response.Status != "active" || response.Decision == nil || response.DecisionReview == nil || response.DecisionReview.DeadlineNotifiedAt != nil {
		return n, nil, false
	}
	review := pinnedDecisionReview(response)
	target := u.DecisionDeadline
	if !review.DecisionAt.Equal(target.DecisionAt) || !response.Decision.At.Equal(target.DecisionAt) || !review.RequiredAt.Equal(target.RequiredAt) || !review.DueAt.Equal(target.DueAt) {
		return n, nil, false
	}
	updated := *response
	updated.DecisionReview = review
	n.ReviewResponse = &updated
	now := time.Now().UTC().Truncate(time.Microsecond)
	if now.Before(*review.DueAt) {
		return n, nil, true
	}
	review.DeadlineNotifiedAt = &now
	review.PreviousOwner = updated.Owner
	review.Owner = updated.Owner
	if review.FallbackOwner != "" && review.FallbackOwner != updated.Owner && review.InitialOwner != "" && review.InitialOwner == updated.Owner && !review.OwnerChanged && n.Coordination != nil && n.Coordination.Owner == updated.Owner && n.Coordination.Completion == nil && n.Coordination.Revision < api.OperationWorkflowReadinessCoordinationRevisionMax {
		coordination := *n.Coordination
		coordination.Owner = review.FallbackOwner
		coordination.Revision++
		coordination.UpdatedAt = now
		coordination.UpdatedBy = "system:review-decision-deadline"
		n.Coordination = &coordination
		updated.Owner = review.FallbackOwner
		updated.CheckInOwnerChanged = true
		updated.UpdatedAt = now
		updated.UpdatedBy = coordination.UpdatedBy
		review.Owner = updated.Owner
		review.OwnerChanged = true
		review.Reassigned = true
		review.HandedOffAt = &now
		review.AcceptanceEscalationOwner = updated.HandoffAcceptanceEscalationOwner
		acceptanceDue := now.Add(time.Duration(effectiveHandoffAcceptanceWindow(&updated)) * time.Second)
		review.AcceptanceDueAt = &acceptanceDue
		updated.History = append(slices.Clone(updated.History), api.OperationWorkflowReviewResponseChange{HandoffAcceptanceEscalationOwner: updated.HandoffAcceptanceEscalationOwner, HandoffAcceptAfterSeconds: updated.HandoffAcceptAfterSeconds, Reason: "decision_review_overdue", DecisionReviewFallbackOwner: updated.DecisionReviewFallbackOwner, DecisionReviewAfterSeconds: updated.DecisionReviewAfterSeconds, FallbackOwner: updated.FallbackOwner, Owner: updated.Owner, Plan: updated.Plan, Status: updated.Status, CheckInAt: updated.CheckInAt, At: now, Actor: updated.UpdatedBy})
		if len(updated.History) > 16 {
			updated.History = updated.History[len(updated.History)-16:]
			updated.HistoryTruncated = true
		}
	}
	n.ReviewResponse = &updated
	notice := readinessSubscriptionNotify(r, "review_response_decision_review_overdue", "The decision revalidation deadline passed without a new explicit decision. Review current plan health and record continue, revise or pause; acknowledgment does not satisfy this obligation.", nil)
	if n.BusinessImpact != nil {
		impact := *n.BusinessImpact
		impact.Response = nil
		notice.BusinessImpact = &impact
	}
	notice.DecisionReview = &api.OperationWorkflowReviewResponseDecisionReviewEvent{SourceNotificationID: n.ID, SourceSequence: n.Sequence, Decision: *response.Decision, Review: *review}
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: updated.Owner, UpdatedAt: now, UpdatedBy: "system:review-decision-deadline"}
	return n, []api.OperationWorkflowReadinessNotification{notice}, true
}
