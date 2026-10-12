package state

import (
	"github.com/onebox-faas/faas/pkg/api"
)

// The caller holds the subscription and target row locks. Only live evidence changes.
func syncPlaybookOutcomeEscalation(n api.OperationWorkflowReadinessNotification, w api.OperationWorkflowPlaybookEvidenceWatch) (api.OperationWorkflowReadinessNotification, bool) {
	if w.Status != "active" || w.Escalation == nil || w.BusinessOutcome == nil || w.DeadlineAt == nil || n.ID != w.Escalation.NotificationID || n.Sequence != w.Escalation.Sequence || n.PlaybookEscalation == nil || n.PlaybookEscalation.WatchID != w.ID || n.PlaybookEscalation.ProgressID != w.ProgressID || !n.PlaybookEscalation.DeadlineAt.Equal(*w.DeadlineAt) {
		return n, false
	}
	e := *n.PlaybookEscalation
	e.Current = *w.BusinessOutcome
	if w.BusinessOutcome.Status == "achieved" && e.SuccessObservedAt == nil {
		at := w.BusinessOutcome.Evidence.CheckedAt
		e.SuccessObservedAt = &at
	}
	n.PlaybookEscalation = &e
	return n, true
}
