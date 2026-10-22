package state

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type fulfillmentReviewEscalationUpdate struct {
	ReviewID               string
	Fingerprint            string
	DueAt                  time.Time
	ReminderNotificationID string
	EscalateAt             time.Time
}

func fulfillmentReviewEscalationDelivered(coord *api.OperationWorkflowReadinessCoordination, id string, due time.Time) bool {
	return slices.ContainsFunc(coord.FulfillmentReviewEscalations, func(e api.OperationWorkflowFulfillmentReviewEscalation) bool {
		return e.ReviewID == id && e.DueAt.Equal(due)
	})
}

// Grace starts at actual reminder delivery; manual routing after that delivery cancels the window.
func fulfillmentReviewEscalationSchedule(coord *api.OperationWorkflowReadinessCoordination, review api.OperationWorkflowObligationFulfillmentReview, id string) (*api.OperationWorkflowFulfillmentReviewReminder, time.Time, bool) {
	if review.DueAt == nil || review.EscalationOwner == "" || review.EscalationOwner == review.Owner || review.EscalateAfterSeconds < 1 || review.EscalateAfterSeconds > 604800 {
		return nil, time.Time{}, false
	}
	for i := range coord.FulfillmentReviewReminders {
		reminder := &coord.FulfillmentReviewReminders[i]
		if reminder.ReviewID != id || !reminder.DueAt.Equal(*review.DueAt) || reminder.Review.Owner != review.Owner {
			continue
		}
		if slices.ContainsFunc(coord.ReviewCoordinationChanges, func(change api.OperationWorkflowFulfillmentReviewCoordinationChange) bool {
			return change.ReviewID == id && change.FromOwner != change.Owner && !change.At.Before(reminder.NotifiedAt)
		}) {
			return nil, time.Time{}, false
		}
		return reminder, reminder.NotifiedAt.Add(time.Duration(review.EscalateAfterSeconds) * time.Second), true
	}
	return nil, time.Time{}, false
}

// Independent cursor keeps evidence failures and large plans from starving escalation delivery.
func evaluateFulfillmentReviewEscalations(ctx context.Context, store readinessSubscriptionBackend, r *readinessSubscriptionRecord) ([]readinessWatchUpdate, error) {
	page, err := store.readReadinessInbox(ctx, r.Account, r.Tenant, r.Subscription.AppID, r.Subscription.Scope, r.Subscription.ID, 0, api.OperationWorkflowReadinessInboxMax, r.Operator)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	type candidate struct {
		key    string
		update readinessWatchUpdate
	}
	candidates := []candidate{}
	for _, n := range page.Items {
		if n.Coordination == nil || len(n.Coordination.FulfillmentReviewEscalations) >= 128 {
			continue
		}
		for _, review := range n.Coordination.FulfillmentReviews {
			obligation := fulfillmentReviewActionObligation(r.Subscription.ID, n.Sequence, review)
			if !review.ReviewRequired || obligation == nil || review.DueAt == nil || review.DueAt.After(now) || activeFulfillment(n.Coordination, review.Binding, review.FulfilledAt) == nil || fulfillmentReviewEscalationDelivered(n.Coordination, obligation.ID, *review.DueAt) {
				continue
			}
			reminder, at, ok := fulfillmentReviewEscalationSchedule(n.Coordination, review, obligation.ID)
			if !ok || at.After(now) {
				continue
			}
			candidates = append(candidates, candidate{n.ID + "/" + obligation.ID, readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, Revision: n.Coordination.Revision, FulfillmentReviewEscalation: &fulfillmentReviewEscalationUpdate{ReviewID: obligation.ID, Fingerprint: review.Fingerprint, DueAt: *review.DueAt, ReminderNotificationID: reminder.NotificationID, EscalateAt: at}}})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].key > r.FulfillmentReviewEscalationCursor, candidates[j].key > r.FulfillmentReviewEscalationCursor
		if a != b {
			return a
		}
		return candidates[i].key < candidates[j].key
	})
	if len(candidates) > 5 {
		candidates = candidates[:5]
	}
	updates := []readinessWatchUpdate{}
	for _, c := range candidates {
		r.FulfillmentReviewEscalationCursor = c.key
		updates = append(updates, c.update)
	}
	return updates, nil
}

// Receipt and destination notice publish atomically under the source and subscription locks.
func applyFulfillmentReviewEscalation(record *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	input := u.FulfillmentReviewEscalation
	if input == nil || n.ID != u.SourceID || n.Coordination == nil || n.Coordination.Revision != u.Revision || len(n.Coordination.FulfillmentReviewEscalations) >= 128 {
		return n, nil, false
	}
	review, _, err := pendingReviewByID(n, record.Subscription.ID, input.ReviewID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err != nil || review.Fingerprint != input.Fingerprint || review.DueAt == nil || !review.DueAt.Equal(input.DueAt) || review.DueAt.After(now) || fulfillmentReviewEscalationDelivered(n.Coordination, input.ReviewID, input.DueAt) {
		return n, nil, false
	}
	reminder, at, ok := fulfillmentReviewEscalationSchedule(n.Coordination, review, input.ReviewID)
	if !ok || reminder.NotificationID != input.ReminderNotificationID || !at.Equal(input.EscalateAt) || at.After(now) {
		return n, nil, false
	}
	notice := readinessSubscriptionNotify(record, "fulfillment_review_escalated", "Fulfillment review remains pending after its overdue reminder and grace period. Escalated to "+review.EscalationOwner+". Review evidence issues ("+strings.Join(review.Reasons, ", ")+") and explicitly reaffirm fulfillment or reopen the case. Review ownership and original fulfillment remain recorded.", nil)
	notice.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(n.ID+"\x1f"+input.ReviewID+"\x1f"+input.DueAt.UTC().Format(time.RFC3339Nano)+"\x1fescalation")).String()
	receipt := api.OperationWorkflowFulfillmentReviewEscalation{ReviewID: input.ReviewID, NotificationID: notice.ID, ReminderNotificationID: reminder.NotificationID, DueAt: input.DueAt, EscalateAt: at, NotifiedAt: notice.CreatedAt, EscalationOwner: review.EscalationOwner, Review: review}
	notice.FulfillmentReview = &review
	notice.FulfillmentReviewEscalation = &receipt
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Status: "unread", Owner: review.EscalationOwner, Revision: 1, UpdatedAt: notice.CreatedAt, UpdatedBy: "system:fulfillment-review-escalation"}
	coord := *n.Coordination
	coord.FulfillmentReviewEscalations = append(slices.Clone(coord.FulfillmentReviewEscalations), receipt)
	n.Coordination = &coord
	return n, []api.OperationWorkflowReadinessNotification{notice}, true
}

func fulfillmentReviewEscalationEvent(escalation api.OperationWorkflowFulfillmentReviewEscalation) api.OperationWorkflowObligationEvent {
	value := escalation
	return api.OperationWorkflowObligationEvent{FulfillmentReviewEscalation: &value, ObligationID: escalation.ReviewID, ActionKind: "fulfillment_review", Cases: []api.OperationWorkflowNextActionCase{escalation.Review.Binding.Case}, Kind: "fulfillment_review_escalated", At: escalation.NotifiedAt, Actor: "system:fulfillment-review-escalation", Explanation: "Pending fulfillment review escalated to " + escalation.EscalationOwner + " after its overdue reminder and configured grace period."}
}
