package state

import (
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

type reviewEscalationCommitmentUpdate struct {
	CoordinationRevision                                                    int64
	DecisionAt, RequiredAt, HandedOffAt, EscalatedAt, AcknowledgedAt, DueAt time.Time
	Coordinator                                                             string
}

func currentReviewEscalation(n api.OperationWorkflowReadinessNotification) bool {
	response := n.ReviewResponse
	if n.Kind != "business_impact_changed" || response == nil || response.Status != "active" || response.Decision == nil || response.DecisionReview == nil || n.Coordination == nil || n.Coordination.Completion != nil {
		return false
	}
	review := response.DecisionReview
	return review.Reassigned && reviewHandoffTime(review) != nil && review.AcceptanceEscalatedAt != nil && review.AcceptanceEscalationOwner != "" && review.Acceptance == nil && review.Decline == nil && review.Owner != "" && response.Owner == review.Owner && n.Coordination.Owner == review.Owner && response.Decision.At.Equal(review.DecisionAt)
}

func validateReviewEscalationAcknowledgment(n api.OperationWorkflowReadinessNotification, input api.OperationWorkflowReviewResponseRequest) error {
	ack := input.EscalationAcknowledgment
	if ack == nil {
		return nil
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	label := ack.Coordinator
	if input.EscalationCompletion != nil || input.EscalationActionReview != nil || input.EscalationRevision != nil || input.ScheduledReviewCancellation != nil || input.ScheduledReviewReschedule != nil || input.Decision != nil || input.HandoffAcceptance != nil || input.HandoffDecline != nil || input.HandoffReassignment != nil || input.Status != "active" || label == "" || strings.TrimSpace(label) != label || len(label) > 256 || !utf8.ValidString(label) || strings.IndexFunc(label, unicode.IsControl) >= 0 || strings.TrimSpace(ack.Plan) == "" || len(ack.Plan) > 1024 || !utf8.ValidString(ack.Plan) || strings.IndexFunc(ack.Plan, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0 || !ack.DueAt.After(now) || ack.DueAt.After(now.Add(7*24*time.Hour)) {
		return ErrInvalidArgument
	}
	if !currentReviewEscalation(n) {
		return ErrConflict
	}
	response := n.ReviewResponse
	review := response.DecisionReview
	if review.EscalationAcknowledgment != nil || !review.DecisionAt.Equal(ack.DecisionAt) || !review.RequiredAt.Equal(ack.RequiredAt) || !reviewHandoffTime(review).Equal(ack.HandedOffAt) || !review.AcceptanceEscalatedAt.Equal(ack.EscalatedAt) || input.Owner != response.Owner || input.Plan != response.Plan || input.CheckInAt == nil || response.CheckInAt == nil || !input.CheckInAt.Equal(*response.CheckInAt) {
		return ErrConflict
	}
	return nil
}

func pendingReviewEscalationCommitment(n api.OperationWorkflowReadinessNotification) bool {
	return currentReviewEscalation(n) && n.ReviewResponse.DecisionReview.EscalationAcknowledgment != nil && n.ReviewResponse.DecisionReview.EscalationAcknowledgment.Completion == nil && n.ReviewResponse.DecisionReview.EscalationAcknowledgment.OverdueNotifiedAt == nil
}

// Independent bounded rotation works even when application-state reads fail.
func reviewEscalationCommitmentUpdates(items []api.OperationWorkflowReadinessNotification, r *readinessSubscriptionRecord) []readinessWatchUpdate {
	candidates := []api.OperationWorkflowReadinessNotification{}
	for _, n := range items {
		if pendingReviewEscalationCommitment(n) {
			candidates = append(candidates, n)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].Sequence > r.EscalationCommitmentCursor, candidates[j].Sequence > r.EscalationCommitmentCursor
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
		review := n.ReviewResponse.DecisionReview
		ack := review.EscalationAcknowledgment
		updates = append(updates, readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, EscalationCommitment: &reviewEscalationCommitmentUpdate{CoordinationRevision: n.Coordination.Revision, DecisionAt: review.DecisionAt, RequiredAt: review.RequiredAt, HandedOffAt: *reviewHandoffTime(review), EscalatedAt: *review.AcceptanceEscalatedAt, AcknowledgedAt: ack.At, DueAt: ack.DueAt, Coordinator: ack.Coordinator}})
		r.EscalationCommitmentCursor = n.Sequence
	}
	return updates
}

func applyReviewEscalationCommitmentDeadline(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	if n.ID != u.SourceID || !pendingReviewEscalationCommitment(n) {
		return n, nil, false
	}
	review := n.ReviewResponse.DecisionReview
	ack := review.EscalationAcknowledgment
	target := u.EscalationCommitment
	if n.Coordination.Revision != target.CoordinationRevision || !review.DecisionAt.Equal(target.DecisionAt) || !review.RequiredAt.Equal(target.RequiredAt) || !reviewHandoffTime(review).Equal(target.HandedOffAt) || !review.AcceptanceEscalatedAt.Equal(target.EscalatedAt) || !ack.At.Equal(target.AcknowledgedAt) || !ack.DueAt.Equal(target.DueAt) || ack.Coordinator != target.Coordinator {
		return n, nil, false
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if now.Before(ack.DueAt) {
		return n, nil, false
	}
	updated := *n.ReviewResponse
	updatedReview := *review
	updatedAck := *ack
	updatedAck.OverdueNotifiedAt = &now
	updatedReview.EscalationAcknowledgment = &updatedAck
	updated.DecisionReview = &updatedReview
	updated.History = slices.Clone(updated.History)
	for i, change := range updated.History {
		if prior := change.EscalationAcknowledgment; prior != nil && prior.At.Equal(ack.At) && prior.Actor == ack.Actor && prior.DueAt.Equal(ack.DueAt) {
			updated.History[i].EscalationAcknowledgment = &updatedAck
		}
	}
	n.ReviewResponse = &updated
	notice := readinessSubscriptionNotify(r, "review_response_escalation_commitment_overdue", "The escalation coordinator's next-action deadline passed while the current handoff remains unaccepted. Review the recorded action plan and coordinate responsibility; this alert changes neither ownership nor the original review deadline.", nil)
	if n.BusinessImpact != nil {
		impact := *n.BusinessImpact
		impact.Response = nil
		notice.BusinessImpact = &impact
	}
	notice.DecisionReview = &api.OperationWorkflowReviewResponseDecisionReviewEvent{SourceNotificationID: n.ID, SourceSequence: n.Sequence, Decision: *updated.Decision, Review: updatedReview}
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: ack.Coordinator, UpdatedAt: now, UpdatedBy: "system:escalation-commitment-deadline"}
	return n, []api.OperationWorkflowReadinessNotification{notice}, true
}
