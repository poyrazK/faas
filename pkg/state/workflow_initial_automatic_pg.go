package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func initialAutomaticSubmissionReasonTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, app string, p api.WorkflowRecoveryPolicyResponse, now time.Time) (string, error) {
	b, err := q.WorkflowRecoveryAutomaticBudget(ctx, tx, sqlc.WorkflowRecoveryAutomaticBudgetParams{AppID: mustPgUUID(app), SinceAt: pgtypeFromTime(now.Add(-time.Hour))})
	if err != nil {
		return "", fmt.Errorf("read initial execution budget: %w", err)
	}
	unresolved, err := q.WorkflowInitialAutomaticUnresolved(ctx, tx, mustPgUUID(app))
	if err != nil {
		return "", fmt.Errorf("read initial execution slot: %w", err)
	}
	return initialAutomaticPressureReason(p, int(b.HourlyExecutions), timestamptzToTimePtr(b.LastExecutionAt), !unresolved.Valid || unresolved.Bool, now), nil
}
func observeInitialAutomaticTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, r api.WorkflowInitialRecoveryReview, account string, now time.Time) (*api.WorkflowRecoveryReviewActionStatus, string, error) {
	job, err := getEventRecoveryMetadata(ctx, q, tx, account, r.JobID)
	if errors.Is(err, ErrNotFound) {
		return nil, "evidence_unknown", nil
	}
	if err != nil {
		return nil, "", err
	}
	if job.SelectedCount != 1 || job.AppID != r.AppID {
		return nil, "evidence_unknown", nil
	}
	rows, err := q.EventRecoveryItems(ctx, tx, sqlc.EventRecoveryItemsParams{JobID: mustPgUUID(r.JobID), PageLimit: 1})
	if err != nil {
		return nil, "", fmt.Errorf("read initial execution item: %w", err)
	}
	if len(rows) != 1 {
		return nil, "evidence_unknown", nil
	}
	item := recoveryStoredItem(rows[0])
	if item.WorkflowRunID != r.Target.RunID || item.ExpectedResumeCount == nil || *item.ExpectedResumeCount != 0 {
		return nil, "evidence_unknown", nil
	}
	observations, err := observeWorkflowRecoveryExecutions(ctx, q, tx, account, r.JobID, 0, 1, now)
	if err != nil {
		return nil, "", err
	}
	result, reason := initialAutomaticOutcome(r, job, item, observations[1], now)
	if result == nil && job.State == "paused" {
		reason = "job_paused"
	}
	return result, reason, nil
}
func processInitialAutomaticResultTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, p *api.WorkflowRecoveryPolicyResponse, account, slug string, raw []byte, now time.Time) error {
	r, err := decodeInitialReview(raw)
	if err != nil {
		return err
	}
	if err = validateAutomaticInitialIdentity(r); err != nil {
		return err
	}
	result, reason, err := observeInitialAutomaticTx(ctx, q, tx, r, account, now)
	if err != nil {
		return err
	}
	initialAutomaticEvaluation(&r, reason, now)
	if result != nil {
		r.AutomaticResult = result
		initialReviewTransition(ctx, &r, "submitted", "result_recorded", now)
		if err = appendRecoveryHistoryEvent(ctx, q, tx, r.AppID, initialLifecycleHistory(r)); err != nil {
			return err
		}
		if r.PolicyVersion == p.Version && p.Policy.Mode == "automatic_initial" && recoveryAutomaticRecordFailure(p, *result, now) {
			if err = enqueueAutomaticRecoveryPause(ctx, q, tx, mustPgUUID(account), *p, slug); err != nil {
				return err
			}
		}
	}
	if err := captureInitialAutomaticBlockTx(ctx, q, tx, account, &r, reason, now); err != nil {
		return err
	}
	return initialReviewSaveTx(ctx, q, tx, r)
}
func executeInitialAutomaticPendingTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, p api.WorkflowRecoveryPolicyResponse, account string, now time.Time) error {
	raw, err := q.WorkflowInitialAutomaticPending(ctx, tx, sqlc.WorkflowInitialAutomaticPendingParams{AppID: mustPgUUID(p.AppID), PolicyVersion: p.Version})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read pending initial recovery: %w", err)
	}
	r, err := decodeInitialReview(raw)
	if err != nil {
		return err
	}
	if err = validateAutomaticInitialIdentity(r); err != nil {
		return err
	}
	submitted, err := decideInitialReviewTx(ctx, q, tx, account, p.AppID, r.ID, p, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: p.Policy.Reason}, now)
	reason := "child_created"
	if err != nil {
		var blocked bool
		reason, blocked = initialAutomaticBlockedReason(err)
		if !blocked {
			return err
		}
	} else {
		r = submitted
		if r.Status != "submitted" {
			reason = r.Reason
		}
	}
	initialAutomaticEvaluation(&r, reason, now)
	if err := captureInitialAutomaticBlockTx(ctx, q, tx, account, &r, reason, now); err != nil {
		return err
	}
	return initialReviewSaveTx(ctx, q, tx, r)
}
