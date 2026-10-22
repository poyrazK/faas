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

func retryEvidenceScopeTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account, app, review, action string, now time.Time, write bool) (api.WorkflowRecoveryPolicyReview, api.WorkflowRecoveryAutomaticAction, api.WorkflowInitialRecoveryReview, error) {
	var r api.WorkflowRecoveryPolicyReview
	var e api.WorkflowRecoveryAutomaticAction
	var frame api.WorkflowInitialRecoveryReview
	raw, err := q.WorkflowRecoveryPolicyReviewGet(ctx, tx, sqlc.WorkflowRecoveryPolicyReviewGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ID: mustPgUUID(review)})
	if errors.Is(err, pgx.ErrNoRows) {
		return r, e, frame, ErrNotFound
	}
	if err != nil {
		return r, e, frame, fmt.Errorf("read retry evidence review: %w", err)
	}
	if err = json.Unmarshal(raw, &r); err != nil {
		return r, e, frame, fmt.Errorf("decode retry evidence review: %w", err)
	}
	var row sqlc.WorkflowRecoveryPolicyExecution
	if write {
		row, err = q.WorkflowRecoveryAutomaticOperatorAction(ctx, tx, sqlc.WorkflowRecoveryAutomaticOperatorActionParams{AppID: mustPgUUID(app), ReviewID: mustPgUUID(review), ActionID: mustPgUUID(action)})
	} else {
		row, err = q.WorkflowRetryEvidenceAction(ctx, tx, sqlc.WorkflowRetryEvidenceActionParams{AppID: mustPgUUID(app), ReviewID: mustPgUUID(review), ActionID: mustPgUUID(action)})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return r, e, frame, ErrNotFound
	}
	if err != nil {
		return r, e, frame, fmt.Errorf("read retry evidence action: %w", err)
	}
	if row.PolicyVersion != r.PolicyVersion {
		return r, e, frame, ErrEventRecoveryChanged
	}
	e, err = pgAutomaticAction(row)
	if err != nil {
		return r, e, frame, err
	}
	frame, err = retryEvidenceFrame(r, e, now)
	return r, e, frame, err
}
func retryEvidenceTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account string, r api.WorkflowRecoveryPolicyReview, e api.WorkflowRecoveryAutomaticAction, frame api.WorkflowInitialRecoveryReview, now time.Time) (api.WorkflowInitialEvidenceDiagnosis, error) {
	if !recoveryEvidenceSupported(frame) || frame.AutomaticResult != nil {
		return initialEvidenceDiagnosis(frame, nil, "retry_not_applied", now), nil
	}
	job, err := getEventRecoveryMetadata(ctx, q, tx, account, e.ChildJobID)
	if errors.Is(err, ErrNotFound) {
		return initialEvidenceDiagnosis(frame, nil, "job_not_retained", now), nil
	}
	if err != nil {
		return api.WorkflowInitialEvidenceDiagnosis{}, err
	}
	items, err := q.EventRecoveryItems(ctx, tx, sqlc.EventRecoveryItemsParams{JobID: mustPgUUID(e.ChildJobID), PageLimit: 1})
	if err != nil {
		return api.WorkflowInitialEvidenceDiagnosis{}, fmt.Errorf("read retry lineage: %w", err)
	}
	if len(items) != 1 || !retryEvidenceChildMatches(job, recoveryStoredItem(items[0]), r, e) {
		return initialEvidenceDiagnosis(frame, nil, "child_lineage_changed", now), nil
	}
	return initialEvidenceTx(ctx, q, tx, account, frame, now)
}
func (s *PgStore) DiagnoseWorkflowRetryEvidence(ctx context.Context, account, app, review, action string) (api.WorkflowRetryEvidenceDiagnosis, error) {
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
	diagnosed, err := retryEvidenceTx(ctx, q, tx, account, r, e, frame, now)
	if err != nil {
		return out, err
	}
	return retryEvidenceDiagnosis(diagnosed, e.ActionID), tx.Commit(ctx)
}
func (s *PgStore) RepairWorkflowRetryEvidence(ctx context.Context, account, app, review, action string, req api.WorkflowInitialEvidenceRepairRequest) (api.WorkflowRetryEvidenceDiagnosis, error) {
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
	replay, err := initialEvidenceReplay(frame, req)
	if err != nil {
		return out, err
	}
	diagnosed, err := retryEvidenceTx(ctx, q, tx, account, r, e, frame, now)
	if err != nil {
		return out, err
	}
	out = retryEvidenceDiagnosis(diagnosed, e.ActionID)
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
	count, err := q.WorkflowRetryEvidenceRepair(ctx, tx, sqlc.WorkflowRetryEvidenceRepairParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), JobID: mustPgUUID(e.ChildJobID), RunID: mustPgUUID(frame.Target.RunID), ExpectedCount: int32(frame.Target.ExpectedResumeCount), WorkflowName: planned.Retry.WorkflowRuns.Name, ParentJobID: mustPgUUID(r.JobID), ParentPosition: e.Position, ActionID: mustPgUUID(e.ActionID), NowAt: pgtypeFromTime(now)})
	if err != nil {
		return out, fmt.Errorf("save verified retry evidence: %w", err)
	}
	if count != 1 {
		return out, ErrWorkflowRecoveryEvidenceBlocked
	}
	diagnosed, err = retryEvidenceTx(ctx, q, tx, account, r, e, frame, now)
	if err != nil {
		return out, err
	}
	if diagnosed.Execution == nil || diagnosed.Execution.Source != "recovery_result" {
		return out, ErrWorkflowRecoveryEvidenceBlocked
	}
	receipt := initialEvidenceReceipt(ctx, req, diagnosed.Execution.State, now)
	raw, err := json.Marshal(receipt)
	if err != nil {
		return out, fmt.Errorf("encode retry evidence receipt: %w", err)
	}
	count, err = q.WorkflowRetryEvidenceReceipt(ctx, tx, sqlc.WorkflowRetryEvidenceReceiptParams{AppID: mustPgUUID(app), ReviewID: mustPgUUID(review), ActionID: mustPgUUID(action), Receipt: raw})
	if err != nil {
		return out, fmt.Errorf("save retry evidence receipt: %w", err)
	}
	if count != 1 {
		return out, ErrEventRecoveryChanged
	}
	if err = appendRecoveryHistoryEvent(ctx, q, tx, app, retryRepairHistory(r, e, "child", *receipt)); err != nil {
		return out, err
	}
	if err = q.WorkflowInitialEvidenceRepairWake(ctx, tx, sqlc.WorkflowInitialEvidenceRepairWakeParams{AppID: mustPgUUID(app), NowAt: pgtypeFromTime(now)}); err != nil {
		return out, fmt.Errorf("schedule retry evidence observation: %w", err)
	}
	out = retryEvidenceDiagnosis(diagnosed, e.ActionID)
	out.Repair = receipt
	return out, tx.Commit(ctx)
}
