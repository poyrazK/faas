package state

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type scheduledActionReviewUpdate struct {
	Revision int64
	Identity api.OperationWorkflowReviewResponseEscalationActionReviewRequest
	DueAt    time.Time
	Review   api.OperationWorkflowReviewResponseEscalationActionReview
}

func pendingScheduledActionReview(n api.OperationWorkflowReadinessNotification) bool {
	response := n.ReviewResponse
	if n.Kind != "business_impact_changed" || response == nil || response.Status != "active" || response.Decision == nil || response.DecisionReview == nil || response.CheckInAt == nil || n.Coordination == nil || n.Coordination.Completion != nil {
		return false
	}
	review := response.DecisionReview
	ack := review.EscalationAcknowledgment
	return ack != nil && ack.Completion != nil && ack.Completion.ReviewDueAt != nil && ack.Completion.ScheduledReviewAt == nil && ack.Completion.ReviewCancellation == nil && len(ack.Completion.Evidence) > 0 && review.AcceptanceEscalatedAt != nil && reviewHandoffTime(review) != nil && response.Owner == review.Owner && n.Coordination.Owner == response.Owner && response.Decision.At.Equal(review.DecisionAt)
}

func evaluateScheduledActionReviews(ctx context.Context, store readinessSubscriptionBackend, r *readinessSubscriptionRecord) ([]readinessWatchUpdate, error) {
	page, err := store.readReadinessInbox(ctx, r.Account, r.Tenant, r.Subscription.AppID, r.Subscription.Scope, r.Subscription.ID, 0, api.OperationWorkflowReadinessInboxMax, r.Operator)
	if err != nil {
		return nil, err
	}
	candidates := []api.OperationWorkflowReadinessNotification{}
	now := time.Now().UTC()
	for _, n := range page.Items {
		if pendingScheduledActionReview(n) && !now.Before(*n.ReviewResponse.DecisionReview.EscalationAcknowledgment.Completion.ReviewDueAt) {
			candidates = append(candidates, n)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].Sequence > r.ScheduledActionReviewCursor, candidates[j].Sequence > r.ScheduledActionReviewCursor
		if a != b {
			return a
		}
		return candidates[i].Sequence < candidates[j].Sequence
	})
	if len(candidates) > api.OperationWorkflowReadinessEscalationResponsesPerPassMax {
		candidates = candidates[:api.OperationWorkflowReadinessEscalationResponsesPerPassMax]
	}
	updates := []readinessWatchUpdate{}
	var firstErr error
	for _, n := range candidates {
		review := n.ReviewResponse.DecisionReview
		ack := review.EscalationAcknowledgment
		completion := ack.Completion
		identity := api.OperationWorkflowReviewResponseEscalationActionReviewRequest{DecisionAt: review.DecisionAt, RequiredAt: review.RequiredAt, HandedOffAt: *reviewHandoffTime(review), EscalatedAt: *review.AcceptanceEscalatedAt, AcknowledgedAt: ack.At, CompletedAt: completion.At}
		input := api.OperationWorkflowReviewResponseRequest{Owner: n.ReviewResponse.Owner, Plan: n.ReviewResponse.Plan, Status: "active", CheckInAt: n.ReviewResponse.CheckInAt, EscalationActionReview: &identity}
		captured, e := prepareEscalationActionReview(ctx, store, r.Account, r.Tenant, r.Subscription.AppID, r.Subscription.Scope, r.Subscription.ID, n.Sequence, api.OperationWorkflowReadinessCoordinationRequest{Revision: n.Coordination.Revision, ReviewResponse: &input}, r.Operator)
		r.ScheduledActionReviewCursor = n.Sequence
		if errors.Is(e, context.DeadlineExceeded) {
			at := time.Now().UTC().Truncate(time.Microsecond)
			health := api.OperationWorkflowReviewResponseHealth{Status: "unavailable", CheckedAt: at, RemainingBlockers: []string{}, RecoveredBlockers: []string{}, Sources: []api.OperationWorkflowReviewResponseSourceHealth{}}
			for i := range completion.Evidence {
				health.Sources = append(health.Sources, api.OperationWorkflowReviewResponseSourceHealth{ReviewIndex: i, Status: "unavailable", CheckedAt: at, Blockers: []string{}, Explanation: "Scheduled source observation exceeded its time budget."})
			}
			health.Fingerprint = reviewResponseHealthFingerprint(health)
			captured = &api.OperationWorkflowReviewResponseEscalationActionReview{CompletedAt: completion.At, Health: health}
		} else if e != nil {
			if firstErr == nil && !errors.Is(e, ErrConflict) && !errors.Is(e, ErrNotFound) {
				firstErr = e
			}
			if ctx.Err() != nil {
				return updates, e
			}
			continue
		}
		if captured == nil {
			continue
		}
		updates = append(updates, readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, ScheduledActionReview: &scheduledActionReviewUpdate{Revision: n.Coordination.Revision, Identity: identity, DueAt: *completion.ReviewDueAt, Review: *captured}})
		if ctx.Err() != nil {
			break
		}
	}
	return updates, firstErr
}

func applyScheduledActionReview(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	if n.ID != u.SourceID || !pendingScheduledActionReview(n) {
		return n, nil, false
	}
	target := u.ScheduledActionReview
	response := n.ReviewResponse
	input := api.OperationWorkflowReviewResponseRequest{Owner: response.Owner, Plan: response.Plan, Status: "active", CheckInAt: response.CheckInAt, EscalationActionReview: &target.Identity}
	completion := response.DecisionReview.EscalationAcknowledgment.Completion
	now := time.Now().UTC().Truncate(time.Microsecond)
	if n.Coordination.Revision != target.Revision || validateEscalationActionReview(n, input) != nil || !completion.ReviewDueAt.Equal(target.DueAt) || !target.Review.CompletedAt.Equal(completion.At) || now.Before(target.DueAt) {
		return n, nil, false
	}
	updated := *response
	review := *response.DecisionReview
	ack := *review.EscalationAcknowledgment
	done := *completion
	captured := target.Review
	captured.At, captured.Actor = now, "system:scheduled-action-review"
	done.Review = &captured
	done.ScheduledReviewAt = &now
	ack.Completion = &done
	review.EscalationAcknowledgment = &ack
	updated.DecisionReview = &review
	updated.History = slices.Clone(updated.History)
	for i, change := range updated.History {
		if previous := change.EscalationCompletion; previous != nil && previous.At.Equal(done.At) {
			updated.History[i].EscalationCompletion = &done
		}
		if previous := change.EscalationAcknowledgment; previous != nil && previous.At.Equal(ack.At) {
			updated.History[i].EscalationAcknowledgment = &ack
		}
	}
	updated.History = append(updated.History, api.OperationWorkflowReviewResponseChange{Reason: "escalation_action_reviewed", EscalationActionReview: &captured, Owner: updated.Owner, Plan: updated.Plan, Status: updated.Status, CheckInAt: updated.CheckInAt, At: now, Actor: captured.Actor})
	if len(updated.History) > 16 {
		updated.History = updated.History[len(updated.History)-16:]
		updated.HistoryTruncated = true
	}
	n.ReviewResponse = &updated
	notice := readinessSubscriptionNotify(r, "review_response_escalation_action_checked", "The scheduled evidence check for completed coordinator work has run. Inspect clear, blocked, review-required or unavailable observations; any revised commitment requires an explicit operator action and business success remains application-owned.", nil)
	if n.BusinessImpact != nil {
		impact := *n.BusinessImpact
		impact.Response = nil
		notice.BusinessImpact = &impact
	}
	notice.DecisionReview = &api.OperationWorkflowReviewResponseDecisionReviewEvent{SourceNotificationID: n.ID, SourceSequence: n.Sequence, Decision: *updated.Decision, Review: review}
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: ack.Coordinator, UpdatedAt: now, UpdatedBy: captured.Actor}
	return n, []api.OperationWorkflowReadinessNotification{notice}, true
}
