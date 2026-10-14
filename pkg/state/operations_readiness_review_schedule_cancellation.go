package state

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func validateScheduledReviewCancellation(n api.OperationWorkflowReadinessNotification, input api.OperationWorkflowReviewResponseRequest) error {
	cancellation := input.ScheduledReviewCancellation
	if cancellation == nil {
		return nil
	}
	if input.EscalationActionReview != nil || input.EscalationRevision != nil || input.EscalationAcknowledgment != nil || input.EscalationCompletion != nil || input.ScheduledReviewReschedule != nil || input.Decision != nil || input.HandoffAcceptance != nil || input.HandoffDecline != nil || input.HandoffReassignment != nil || strings.TrimSpace(cancellation.Reason) == "" || len(cancellation.Reason) > 1024 || !utf8.ValidString(cancellation.Reason) || strings.IndexFunc(cancellation.Reason, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0 {
		return ErrInvalidArgument
	}
	if !pendingScheduledActionReview(n) || !n.ReviewResponse.DecisionReview.EscalationAcknowledgment.Completion.ReviewDueAt.Equal(cancellation.DueAt) {
		return ErrConflict
	}
	// Reuse the exact completion identity and unchanged-parent fences without observing sources.
	copy := input
	copy.ScheduledReviewCancellation = nil
	copy.EscalationActionReview = &cancellation.OperationWorkflowReviewResponseEscalationActionReviewRequest
	return validateEscalationActionReview(n, copy)
}
