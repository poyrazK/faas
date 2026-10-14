package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

func retryParentEvidenceTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account string, r api.WorkflowRecoveryPolicyReview, e api.WorkflowRecoveryAutomaticAction, frame api.WorkflowInitialRecoveryReview, now time.Time) (api.WorkflowRetryEvidenceDiagnosis, error) {
	out := retryParentEvidenceBase(r, e, frame, now)
	if out.Reason == "retry_not_pending" {
		return out, nil
	}
	job, err := getEventRecoveryMetadata(ctx, q, tx, account, r.JobID)
	if errors.Is(err, ErrNotFound) {
		out.Reason = "parent_job_not_retained"
		return out, nil
	}
	if err != nil {
		return out, err
	}
	items, err := q.EventRecoveryItems(ctx, tx, sqlc.EventRecoveryItemsParams{JobID: mustPgUUID(r.JobID), AfterPosition: e.Position - 1, PageLimit: 1})
	if err != nil {
		return out, fmt.Errorf("inspect retry parent item: %w", err)
	}
	if len(items) != 1 || items[0].Position != e.Position {
		out.Reason = "parent_item_not_retained"
		return out, nil
	}
	item := recoveryStoredItem(items[0])
	if !retryParentItemMatches(job, item, r, e, frame) {
		out.Reason = "parent_lineage_changed"
		return out, nil
	}
	if item.State != "queued" {
		out.Reason = "parent_admission_not_verified"
		return out, nil
	}
	observed, err := observeWorkflowRecoveryExecutions(ctx, q, tx, account, r.JobID, e.Position-1, e.Position, now)
	if err != nil {
		return out, err
	}
	existing, err := q.WorkflowRetryParentEvidenceResultExists(ctx, tx, sqlc.WorkflowRetryParentEvidenceResultExistsParams{JobID: mustPgUUID(r.JobID), Position: e.Position})
	if err != nil {
		return out, fmt.Errorf("inspect existing parent proof: %w", err)
	}
	return retryParentExecutionDiagnosis(out, observed[e.Position], existing), nil
}
func (s *PgStore) DiagnoseWorkflowRetryParentEvidence(ctx context.Context, account, app, review, action string) (api.WorkflowRetryEvidenceDiagnosis, error) {
	var out api.WorkflowRetryEvidenceDiagnosis
	if err := validateRetryEvidenceIDs(account, app, review, action); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.initialFailureReadTx(ctx, account, app)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	r, e, frame, err := retryEvidenceScopeTx(ctx, q, tx, account, app, review, action, now, false)
	if err != nil {
		return out, err
	}
	out, err = retryParentEvidenceTx(ctx, q, tx, account, r, e, frame, now)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) RepairWorkflowRetryParentEvidence(ctx context.Context, account, app, review, action string, req api.WorkflowInitialEvidenceRepairRequest) (api.WorkflowRetryEvidenceDiagnosis, error) {
	var out api.WorkflowRetryEvidenceDiagnosis
	if err := validateRetryEvidenceIDs(account, app, review, action); err != nil {
		return out, err
	}
	if err := req.Validate(); err != nil {
		return out, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, _, _, err := s.initialReviewWriteTx(ctx, account, app)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	r, e, frame, err := retryEvidenceScopeTx(ctx, q, tx, account, app, review, action, now, true)
	if err != nil {
		return out, err
	}
	out, err = retryParentEvidenceTx(ctx, q, tx, account, r, e, frame, now)
	if err != nil {
		return out, err
	}
	replay, err := retryParentEvidenceReplay(out, req)
	if err != nil {
		return out, err
	}
	if replay {
		return out, tx.Commit(ctx)
	}
	if !out.Repairable {
		return out, ErrWorkflowRecoveryEvidenceBlocked
	}
	planned, err := recoveryAutomaticAction(r, e, api.WorkflowRecoveryPolicyResponse{AppID: r.AppID, Version: r.PolicyVersion, Policy: r.Policy})
	if err != nil {
		return out, ErrEventRecoveryChanged
	}
	count, err := q.WorkflowRetryParentEvidenceRepair(ctx, tx, sqlc.WorkflowRetryParentEvidenceRepairParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), JobID: mustPgUUID(r.JobID), RunID: mustPgUUID(frame.Target.RunID), ExpectedCount: int32(frame.Target.ExpectedResumeCount - 1), ParentPosition: e.Position, WorkflowName: planned.Retry.WorkflowRuns.Name, NowAt: pgtypeFromTime(now)})
	if err != nil {
		return out, fmt.Errorf("save verified parent proof: %w", err)
	}
	if count != 1 {
		return out, ErrWorkflowRecoveryEvidenceBlocked
	}
	out, err = retryParentEvidenceTx(ctx, q, tx, account, r, e, frame, now)
	if err != nil {
		return out, err
	}
	if out.Execution == nil || out.Execution.Source != "recovery_result" {
		return out, ErrWorkflowRecoveryEvidenceBlocked
	}
	receipt := initialEvidenceReceipt(ctx, req, out.Execution.State, now)
	raw, err := json.Marshal(receipt)
	if err != nil {
		return out, fmt.Errorf("encode parent repair receipt: %w", err)
	}
	count, err = q.WorkflowRetryParentEvidenceReceipt(ctx, tx, sqlc.WorkflowRetryParentEvidenceReceiptParams{AppID: mustPgUUID(app), ReviewID: mustPgUUID(review), ActionID: mustPgUUID(action), Receipt: raw})
	if err != nil {
		return out, fmt.Errorf("save parent repair audit: %w", err)
	}
	if count != 1 {
		return out, ErrEventRecoveryChanged
	}
	if err = appendRecoveryHistoryEvent(ctx, q, tx, app, retryRepairHistory(r, e, "parent", *receipt)); err != nil {
		return out, err
	}
	if err = q.WorkflowInitialEvidenceRepairWake(ctx, tx, sqlc.WorkflowInitialEvidenceRepairWakeParams{AppID: mustPgUUID(app), NowAt: pgtypeFromTime(now)}); err != nil {
		return out, fmt.Errorf("schedule repaired parent recheck: %w", err)
	}
	out.Repair = receipt
	return out, tx.Commit(ctx)
}
