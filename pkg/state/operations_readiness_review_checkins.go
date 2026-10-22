package state

import (
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func reviewResponseCheckInUpdates(items []api.OperationWorkflowReadinessNotification, r *readinessSubscriptionRecord) []readinessWatchUpdate {
	candidates := []api.OperationWorkflowReadinessNotification{}
	for _, n := range items {
		if n.Kind == "business_impact_changed" && n.ReviewResponse != nil && n.ReviewResponse.Status == "active" && n.ReviewResponse.CheckInAt != nil && n.ReviewResponse.CheckInNotifiedAt == nil {
			candidates = append(candidates, n)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].Sequence > r.ReviewResponseCursor, candidates[j].Sequence > r.ReviewResponseCursor
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
		deadline := *n.ReviewResponse.CheckInAt
		updates = append(updates, readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, ReviewCheckInAt: &deadline})
		r.ReviewResponseCursor = n.Sequence
	}
	return updates
}

// Subscription lease and notification locks fence the marker, handoff and notice.
// A resolution or a newly scheduled check-in wins against a stale read phase.
func applyReviewResponseCheckIn(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	if n.ID != u.SourceID || n.Kind != "business_impact_changed" || n.BusinessImpact == nil || n.ReviewResponse == nil || n.Coordination == nil || n.ReviewResponse.Status != "active" || n.ReviewResponse.CheckInAt == nil || u.ReviewCheckInAt == nil || !n.ReviewResponse.CheckInAt.Equal(*u.ReviewCheckInAt) || n.ReviewResponse.CheckInNotifiedAt != nil {
		return n, nil, false
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if now.Before(*n.ReviewResponse.CheckInAt) {
		return n, nil, false
	}
	response := *n.ReviewResponse
	previousOwner := response.Owner
	reassigned := false
	if response.FallbackOwner != "" && response.FallbackOwner != response.Owner && response.CheckInInitialOwner != "" && response.Owner == response.CheckInInitialOwner && !response.CheckInOwnerChanged && n.Coordination.Owner == response.Owner && n.Coordination.Completion == nil && n.Coordination.Revision < api.OperationWorkflowReadinessCoordinationRevisionMax {
		c := *n.Coordination
		c.Owner = response.FallbackOwner
		c.Revision++
		c.UpdatedAt = now
		c.UpdatedBy = "system:review-response-check-in"
		n.Coordination = &c
		response.Owner = c.Owner
		response.CheckInOwnerChanged = true
		if response.DecisionReview != nil {
			review := *response.DecisionReview
			review.OwnerChanged = true
			response.DecisionReview = &review
		}
		reassigned = true
	}
	response.CheckInNotifiedAt = &now
	response.UpdatedAt = now
	response.UpdatedBy = "system:review-response-check-in"
	response.History = append(slices.Clone(response.History), api.OperationWorkflowReviewResponseChange{Reason: "check_in_overdue", FallbackOwner: response.FallbackOwner, Owner: response.Owner, Plan: response.Plan, Status: response.Status, CheckInAt: response.CheckInAt, At: now, Actor: response.UpdatedBy})
	if len(response.History) > 16 {
		response.History = response.History[len(response.History)-16:]
		response.HistoryTruncated = true
	}
	n.ReviewResponse = &response
	notice := readinessSubscriptionNotify(r, "review_response_check_in_overdue", "The active review response missed its scheduled check-in. Inspect the response plan and linked reviews, then explicitly reschedule or resolve it; application business execution remains application-owned.", nil)
	context := *n.BusinessImpact
	context.Response = nil
	notice.BusinessImpact = &context
	notice.ReviewCheckIn = &api.OperationWorkflowReviewResponseCheckInEvent{SourceNotificationID: n.ID, SourceSequence: n.Sequence, CheckedAt: now, CheckInAt: *response.CheckInAt, PreviousOwner: previousOwner, Owner: response.Owner, Reassigned: reassigned, Response: response}
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: response.Owner, UpdatedAt: now, UpdatedBy: response.UpdatedBy}
	return n, []api.OperationWorkflowReadinessNotification{notice}, true
}
