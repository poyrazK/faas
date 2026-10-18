package state

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) recoveryQueueEvidenceLocked(ctx context.Context, account, app string, out *api.WorkflowRecoveryAutomaticQueue) error {
	for i := range out.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := &out.Entries[i]
		recoveryQueueWait(out.Policy, entry)
		// Initial recovery identity uses its review UUID, with its own proof scope.
		if sameMemUUID(entry.ReviewID, entry.Action.ActionID) {
			r, ok := m.initialRecoveryReviews[canonicalMemUUID(entry.ReviewID)]
			if !ok || !sameMemUUID(r.AppID, app) || !r.PreparedAt.After(out.ObservedAt.Add(-api.WorkflowRecoveryHistoryRetention)) && !initialReviewUnresolved(r) {
				entry.EvidenceUnavailableReason = "review_not_retained"
				continue
			}
			entry.InitialEvidence = recoveryInitialQueueEvidence(r, m.initialEvidenceLocked(r, out.ObservedAt))
			continue
		}
		_, r, e, frame, err := m.retryEvidenceScopeLocked(account, app, entry.ReviewID, entry.Action.ActionID, out.ObservedAt)
		if errors.Is(err, ErrNotFound) {
			entry.EvidenceUnavailableReason = "review_not_retained"
			continue
		}
		if errors.Is(err, ErrEventRecoveryChanged) {
			entry.EvidenceUnavailableReason = "plan_changed"
			continue
		}
		if err != nil {
			return err
		}
		var d api.WorkflowRetryEvidenceDiagnosis
		if e.Status == "pending" {
			d = m.retryParentEvidenceLocked(r, *e, frame, out.ObservedAt)
			live, ok := m.workflowRuns[frame.Target.RunID]
			if !ok {
				d.Reason, d.Repairable = "run_not_retained", false
			} else if live.ResumeCount != frame.Target.ExpectedResumeCount {
				d.Reason, d.Repairable = "generation_superseded", false
			}
		} else {
			d = retryEvidenceDiagnosis(m.retryEvidenceLocked(r, *e, frame, out.ObservedAt), e.ActionID)
			d.EvidenceKind, d.ItemPosition = "child", 1
		}
		entry.Evidence = recoveryQueueEvidence(r.Plan.App, d)
	}
	return nil
}
