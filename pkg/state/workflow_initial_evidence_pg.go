package state

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

func initialEvidenceTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account string, r api.WorkflowInitialRecoveryReview, now time.Time) (api.WorkflowInitialEvidenceDiagnosis, error) {
	if !recoveryEvidenceSupported(r) || r.AutomaticResult != nil || r.Result != nil {
		return initialEvidenceDiagnosis(r, nil, "not_submitted_automatic_initial", now), nil
	}
	job, err := getEventRecoveryMetadata(ctx, q, tx, account, r.JobID)
	if errors.Is(err, ErrNotFound) {
		return initialEvidenceDiagnosis(r, nil, "job_not_retained", now), nil
	}
	if err != nil {
		return api.WorkflowInitialEvidenceDiagnosis{}, err
	}
	if job.AppID != r.AppID || job.SelectedCount != 1 {
		return initialEvidenceDiagnosis(r, nil, "job_target_changed", now), nil
	}
	rows, err := q.EventRecoveryItems(ctx, tx, sqlc.EventRecoveryItemsParams{JobID: mustPgUUID(job.ID), PageLimit: 1})
	if err != nil {
		return api.WorkflowInitialEvidenceDiagnosis{}, fmt.Errorf("inspect evidence repair item: %w", err)
	}
	if len(rows) != 1 {
		return initialEvidenceDiagnosis(r, nil, "job_target_changed", now), nil
	}
	item := recoveryStoredItem(rows[0])
	if item.WorkflowRunID != r.Target.RunID || item.ExpectedResumeCount == nil || *item.ExpectedResumeCount != r.Target.ExpectedResumeCount {
		return initialEvidenceDiagnosis(r, nil, "job_target_changed", now), nil
	}
	if item.State != "queued" {
		return initialEvidenceDiagnosis(r, nil, "admission_not_verified", now), nil
	}
	observed, err := observeWorkflowRecoveryExecutions(ctx, q, tx, account, r.JobID, 0, 1, now)
	if err != nil {
		return api.WorkflowInitialEvidenceDiagnosis{}, err
	}
	e := observed[1]
	out := initialEvidenceDiagnosis(r, &e, "exact_generation_unverified", now)
	if out.Repairable {
		exists, err := q.WorkflowInitialEvidenceResultExists(ctx, tx, mustPgUUID(r.JobID))
		if err != nil {
			return out, fmt.Errorf("inspect existing repair evidence: %w", err)
		}
		if exists {
			out.Repairable = false
			out.Reason = "existing_result_conflict"
		}
	}
	return out, nil
}
func (s *PgStore) DiagnoseWorkflowInitialEvidence(ctx context.Context, account, app, id string) (api.WorkflowInitialEvidenceDiagnosis, error) {
	var out api.WorkflowInitialEvidenceDiagnosis
	if err := validateInitialReviewIDs(account, app, id); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.initialFailureReadTx(ctx, account, app)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC().Truncate(time.Microsecond)
	q := sqlc.New()
	r, err := initialReviewGetTx(ctx, q, tx, account, app, id, now)
	if err != nil {
		return out, err
	}
	out, err = initialEvidenceTx(ctx, q, tx, account, r, now)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) RepairWorkflowInitialEvidence(ctx context.Context, account, app, id string, req api.WorkflowInitialEvidenceRepairRequest) (api.WorkflowInitialEvidenceDiagnosis, error) {
	var out api.WorkflowInitialEvidenceDiagnosis
	if err := validateInitialReviewIDs(account, app, id); err != nil {
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
	r, err := initialReviewGetTx(ctx, q, tx, account, app, id, now)
	if err != nil {
		return out, err
	}
	replay, err := initialEvidenceReplay(r, req)
	if err != nil {
		return out, err
	}
	out, err = initialEvidenceTx(ctx, q, tx, account, r, now)
	if err != nil {
		return out, err
	}
	if replay {
		return out, tx.Commit(ctx)
	}
	if !out.Repairable {
		return out, ErrWorkflowInitialReviewBlocked
	}
	count, err := q.WorkflowInitialEvidenceRepair(ctx, tx, sqlc.WorkflowInitialEvidenceRepairParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), JobID: mustPgUUID(r.JobID), RunID: mustPgUUID(r.Target.RunID), NowAt: pgtypeFromTime(now)})
	if err != nil {
		return out, fmt.Errorf("save verified initial recovery evidence: %w", err)
	}
	if count != 1 {
		return out, ErrWorkflowInitialReviewBlocked
	}
	// Read the row actually inserted; the live run may have changed since diagnosis.
	out, err = initialEvidenceTx(ctx, q, tx, account, r, now)
	if err != nil {
		return out, err
	}
	if out.Execution == nil || out.Execution.Source != "recovery_result" {
		return out, ErrWorkflowInitialReviewBlocked
	}
	r.EvidenceRepair = initialEvidenceReceipt(ctx, req, out.Execution.State, now)
	initialReviewTransition(ctx, &r, r.Status, "evidence_repaired", now)
	if err = initialReviewSaveTx(ctx, q, tx, r); err != nil {
		return out, err
	}
	if err = appendRecoveryHistoryEvent(ctx, q, tx, app, initialRepairHistory(r)); err != nil {
		return out, err
	}
	if r.Policy.Mode == "preview" {
		err = q.WorkflowInitialManualCompletionAdvance(ctx, tx, sqlc.WorkflowInitialManualCompletionAdvanceParams{ID: mustPgUUID(r.ID), NextAt: pgtypeFromTime(now)})
	} else {
		err = q.WorkflowInitialEvidenceRepairWake(ctx, tx, sqlc.WorkflowInitialEvidenceRepairWakeParams{AppID: mustPgUUID(app), NowAt: pgtypeFromTime(now)})
	}
	if err != nil {
		return out, fmt.Errorf("schedule repaired evidence observation: %w", err)
	}
	out.Repair = r.EvidenceRepair
	return out, tx.Commit(ctx)
}
