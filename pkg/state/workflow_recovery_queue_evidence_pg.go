package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pgRecoveryQueueEvidence(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account, app string, out *api.WorkflowRecoveryAutomaticQueue) error {
	for i := range out.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := &out.Entries[i]
		recoveryQueueWait(out.Policy, entry)
		if sameMemUUID(entry.ReviewID, entry.Action.ActionID) {
			r, err := initialReviewGetTx(ctx, q, tx, account, app, entry.ReviewID, out.ObservedAt)
			if errors.Is(err, ErrNotFound) {
				entry.EvidenceUnavailableReason = "review_not_retained"
				continue
			}
			if err != nil {
				return err
			}
			d, err := initialEvidenceTx(ctx, q, tx, account, r, out.ObservedAt)
			if err != nil {
				return err
			}
			entry.InitialEvidence = recoveryInitialQueueEvidence(r, d)
			continue
		}
		r, e, frame, err := retryEvidenceScopeTx(ctx, q, tx, account, app, entry.ReviewID, entry.Action.ActionID, out.ObservedAt, false)
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
			d, err = retryParentEvidenceTx(ctx, q, tx, account, r, e, frame, out.ObservedAt)
			if err != nil {
				return err
			}
			live, liveErr := q.GetNativeWorkflowRun(ctx, tx, mustPgUUID(frame.Target.RunID))
			if errors.Is(liveErr, pgx.ErrNoRows) {
				d.Reason, d.Repairable = "run_not_retained", false
			} else if liveErr != nil {
				return fmt.Errorf("inspect queue parent generation: %w", liveErr)
			} else if live.ResumeCount != int32(frame.Target.ExpectedResumeCount) {
				d.Reason, d.Repairable = "generation_superseded", false
			}
		} else {
			observed, observeErr := retryEvidenceTx(ctx, q, tx, account, r, e, frame, out.ObservedAt)
			if observeErr != nil {
				return observeErr
			}
			d = retryEvidenceDiagnosis(observed, e.ActionID)
			d.EvidenceKind, d.ItemPosition = "child", 1
		}
		entry.Evidence = recoveryQueueEvidence(r.Plan.App, d)
	}
	return nil
}
