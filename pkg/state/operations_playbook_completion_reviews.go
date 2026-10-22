package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func observePlaybookCompletionReview(ctx context.Context, store readinessSubscriptionBackend, r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification) (readinessWatchUpdate, error) {
	w := *n.PlaybookReview
	u := readinessWatchUpdate{SourceID: n.ID, Sequence: n.Sequence, WatchID: w.ID, Revision: w.Revision}
	if !time.Now().UTC().Before(w.ExpiresAt) {
		w.Status = "expired"
	} else {
		// The completion's frozen comparison floor governs review, not later escalation evidence.
		if n.PlaybookEscalation == nil || n.PlaybookEvidence == nil || n.Coordination == nil || n.Coordination.Completion == nil || n.Coordination.Completion.PlaybookOutcome == nil {
			w.Status = "stopped"
		} else {
			e := *n.PlaybookEscalation
			e.Current = w.Current
			e.Current.Evidence.StateRevision = w.StateRevision
			n.PlaybookEscalation = &e
			result, err := readPlaybookCompletionOutcome(ctx, store, r.Account, r.Tenant, r.Subscription.AppID, r.Subscription.Scope, n, n.Coordination.Completion.PlaybookOutcome.Resolution, r.Operator)
			if err != nil {
				return u, err
			}
			w.Current = result.Observation
			if w.Current.Evidence.ContractVersion == n.PlaybookEvidence.ContractVersion && w.Current.Evidence.StateRevision > w.StateRevision {
				w.StateRevision = w.Current.Evidence.StateRevision
			}
		}
	}
	w.Revision++
	u.CompletionReview = &w
	return u, nil
}

// Lease and exact source-row locks fence observations and notices together.
func applyPlaybookCompletionReview(r *readinessSubscriptionRecord, n api.OperationWorkflowReadinessNotification, u readinessWatchUpdate) (api.OperationWorkflowReadinessNotification, []api.OperationWorkflowReadinessNotification, bool) {
	old := n.PlaybookReview
	if n.ID != u.SourceID || old == nil || old.ID != u.WatchID || old.Revision != u.Revision || old.Status != "active" {
		return n, nil, false
	}
	w := *u.CompletionReview
	if !time.Now().UTC().Before(old.ExpiresAt) {
		w.Status = "expired"
	}
	n.PlaybookReview = &w
	if w.Status != "active" || playbookBusinessOutcomeKey(&old.Current) == playbookBusinessOutcomeKey(&w.Current) {
		return n, nil, true
	}
	if n.Coordination == nil || n.Coordination.Completion == nil || n.Coordination.Completion.PlaybookOutcome == nil || n.PlaybookEvidence == nil {
		return n, nil, true
	}
	notice := readinessSubscriptionNotify(r, "playbook_outcome_changed", "Application evidence changed during a post-completion review window. The original human closure and its evidence are preserved; review the linked completion.", nil)
	source := *n.PlaybookEvidence
	source.Changes = []api.OperationWorkflowPlaybookEvidenceChange{}
	source.OutcomeChange = nil
	source.BusinessOutcome = &w.Current
	source.CheckedAt = w.Current.Evidence.CheckedAt
	notice.PlaybookEvidence = &source
	notice.PlaybookReviewEvent = &api.OperationWorkflowPlaybookCompletionReviewEvent{ReviewID: w.ID, SourceNotificationID: n.ID, SourceSequence: n.Sequence, Resolution: n.Coordination.Completion.PlaybookOutcome.Resolution, Previous: old.Current, Current: w.Current}
	notice.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: w.Owner, UpdatedAt: notice.CreatedAt, UpdatedBy: "system:playbook-completion-review"}
	return n, []api.OperationWorkflowReadinessNotification{notice}, true
}
