package state

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// Rechecked under the notification lock; acknowledgment alone never accepts a handoff.
func validateReviewHandoffAcceptance(n api.OperationWorkflowReadinessNotification, input api.OperationWorkflowReviewResponseRequest) error {
	acceptance := input.HandoffAcceptance
	if acceptance == nil {
		return nil
	}
	if input.HandoffReassignment != nil || input.HandoffDecline != nil || input.EscalationAcknowledgment != nil || input.EscalationCompletion != nil || input.EscalationActionReview != nil || input.EscalationRevision != nil || input.ScheduledReviewCancellation != nil || input.ScheduledReviewReschedule != nil || input.Decision != nil || input.Status != "active" || input.CheckInAt == nil || !input.CheckInAt.After(time.Now().UTC()) || strings.TrimSpace(acceptance.Note) == "" || len(acceptance.Note) > 1024 || !utf8.ValidString(acceptance.Note) || strings.IndexFunc(acceptance.Note, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0 {
		return ErrInvalidArgument
	}
	response := n.ReviewResponse
	if response == nil || response.Status != "active" || response.Decision == nil || response.DecisionReview == nil || n.Coordination == nil || n.Coordination.Completion != nil {
		return ErrConflict
	}
	review := response.DecisionReview
	if !matchesReviewHandoffTime(review, acceptance.HandedOffAt) || !review.Reassigned || review.DeadlineNotifiedAt == nil || review.Acceptance != nil || review.Decline != nil || review.Owner == "" || input.Owner != review.Owner || response.Owner != review.Owner || n.Coordination.Owner != review.Owner || !review.DecisionAt.Equal(acceptance.DecisionAt) || !response.Decision.At.Equal(acceptance.DecisionAt) || !review.RequiredAt.Equal(acceptance.RequiredAt) {
		return ErrConflict
	}
	return nil
}
