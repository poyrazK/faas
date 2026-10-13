package state

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

func validateScheduledReviewReschedule(n api.OperationWorkflowReadinessNotification, input api.OperationWorkflowReviewResponseRequest) error {
	reschedule := input.ScheduledReviewReschedule
	if reschedule == nil {
		return nil
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	due := reschedule.NewDueAt.UTC().Truncate(time.Microsecond)
	if input.ScheduledReviewCancellation != nil || input.EscalationActionReview != nil || input.EscalationRevision != nil || input.EscalationAcknowledgment != nil || input.EscalationCompletion != nil || input.Decision != nil || input.HandoffAcceptance != nil || input.HandoffDecline != nil || input.HandoffReassignment != nil || strings.TrimSpace(reschedule.Reason) == "" || len(reschedule.Reason) > 1024 || !utf8.ValidString(reschedule.Reason) || strings.IndexFunc(reschedule.Reason, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0 || !due.After(now) || due.After(now.Add(7*24*time.Hour)) || due.Equal(reschedule.DueAt) {
		return ErrInvalidArgument
	}
	if !pendingScheduledActionReview(n) || !n.ReviewResponse.DecisionReview.EscalationAcknowledgment.Completion.ReviewDueAt.Equal(reschedule.DueAt) {
		return ErrConflict
	}
	copy := input
	copy.ScheduledReviewReschedule = nil
	copy.EscalationActionReview = &reschedule.OperationWorkflowReviewResponseEscalationActionReviewRequest
	return validateEscalationActionReview(n, copy)
}
