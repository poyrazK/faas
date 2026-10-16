package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type WorkflowRecoveryRetryPreflightStore interface {
	GetWorkflowRecoveryRetryPreflight(context.Context, string, string, time.Time) (api.EventRecoveryPreflight, error)
}

func workflowPreflightIdentity(item api.EventRecoveryItem, retry bool) api.EventRecoveryPreflightItem {
	count := item.ExpectedResumeCount
	if count != nil {
		n := *count
		if retry {
			n++
		}
		count = &n
	}
	return api.EventRecoveryPreflightItem{Position: item.Position, WorkflowRunID: item.WorkflowRunID, ExpectedResumeCount: count}
}
func workflowPreflightSaved(row *api.EventRecoveryPreflightItem, item api.EventRecoveryItem) bool {
	if item.State != "queued" {
		row.Reason = "not_admitted"
		return false
	}
	if item.Execution == nil || item.Execution.Source != "recovery_result" || item.Execution.EvidenceSource != "workflow_run" || item.Execution.RecordedAt == nil {
		row.Reason = "outcome_not_saved"
		return false
	}
	row.SavedOutcome = item.Execution.State
	if row.SavedOutcome != "failed" {
		row.Reason = "outcome_not_failed"
		return false
	}
	return true
}
func workflowPreflightDiagnostics(row *api.EventRecoveryPreflightItem, name string, d api.WorkflowRunDiagnosticsResponse, err error, operation bool) error {
	if errors.Is(err, ErrWorkflowRunNotFound) {
		row.Reason = "run_not_retained"
		return nil
	}
	if err != nil {
		return fmt.Errorf("read recovery resume eligibility: %w", err)
	}
	if row.ExpectedResumeCount == nil || d.RunID != row.WorkflowRunID || d.WorkflowName != name || d.Resume.ExpectedResumeCount != *row.ExpectedResumeCount || d.Status != "failed" && d.Status != "dead" {
		row.Reason = "changed"
		return nil
	}
	for _, block := range d.Resume.Blockers {
		row.Blockers = append(row.Blockers, block.Code)
	}
	if operation {
		row.Blockers = append(row.Blockers, "operation_owned")
	}
	row.Reason = "eligible"
	if len(row.Blockers) > 0 {
		row.Reason = "capacity"
		row.CapacityScope = "app"
		for _, code := range row.Blockers {
			if code != "active_run_quota" {
				row.Reason = "resume_ineligible"
				row.CapacityScope = ""
				if code == "resume_limit_reached" {
					row.Reason = "resume_limit_reached"
				}
				break
			}
		}
	}
	return nil
}
func appendWorkflowRecoveryPreflight(out *api.EventRecoveryPreflight, row api.EventRecoveryPreflightItem, retry bool) {
	addRecoveryPreflight(out, row.Position, row.Reason, row.CapacityScope)
	i := len(out.Sample) - 1
	row.Status = out.Sample[i].Status
	if row.Reason == "outcome_not_saved" {
		row.Status = "unknown"
		out.LikelySkippedCount--
		out.UnknownCount++
		out.Sample[i].Status = row.Status
	}
	out.Sample[i] = row
	if retry {
		out.PendingCount--
		out.ReviewedCount++
	}
}
func workflowRecoveryPreflightRead(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account string, job api.EventRecoveryJob, now time.Time, retry bool, out *api.EventRecoveryPreflight) error {
	out.AppID, out.WorkflowName = job.AppID, job.Selection.WorkflowRuns.Name
	rows, err := q.EventRecoveryItems(ctx, db, sqlc.EventRecoveryItemsParams{JobID: mustPgUUID(job.ID), PageLimit: api.AutomationRecoveryBatchMaxRuns + 1})
	if err != nil {
		return err
	}
	if len(rows) > api.AutomationRecoveryBatchMaxRuns || int64(len(rows)) != job.SelectedCount {
		return ErrEventRecoveryQuery
	}
	var observations map[int64]api.EventRecoveryExecution
	if retry {
		observations, err = observeWorkflowRecoveryExecutions(ctx, q, db, account, job.ID, 0, job.SelectedCount, now)
		if err != nil {
			return err
		}
	}
	for _, stored := range rows {
		item := recoveryStoredItem(stored)
		if !retry && item.State != "pending" {
			continue
		}
		if !retry {
			reason, err := initialReviewJobAuthority(ctx, q, db, job.ID, now)
			if err != nil {
				return err
			}
			if reason != "" {
				row := workflowPreflightIdentity(item, false)
				row.Reason = reason
				appendWorkflowRecoveryPreflight(out, row, false)
				continue
			}
		}
		if !retry {
			var target api.WorkflowRunRecoveryTarget
			if err := json.Unmarshal(stored.ExpectedProgress, &target); err != nil {
				return fmt.Errorf("decode recovery preflight evidence: %w", err)
			}
			if target.FailureEvidenceID != "" {
				_, err := workflowRunRecoveryCandidates(ctx, q, db, account, job.AppID, api.EventRecoveryRequest{WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: job.Selection.WorkflowRuns.Name, Runs: []api.WorkflowRunRecoveryTarget{target}}})
				if errors.Is(err, ErrEventRecoveryChanged) {
					row := workflowPreflightIdentity(item, false)
					row.Reason = "failure_evidence_unavailable"
					appendWorkflowRecoveryPreflight(out, row, false)
					continue
				}
				if err != nil {
					return err
				}
			}
		}
		row := workflowPreflightIdentity(item, retry)
		if execution, ok := observations[item.Position]; ok {
			item.Execution = &execution
		}
		if !retry || workflowPreflightSaved(&row, item) {
			d, e := workflowRunDiagnosticsRead(ctx, q, db, WorkflowDiagnosticsOptions{RunID: row.WorkflowRunID, AccountID: account, AppID: job.AppID})
			_, ownerErr := q.GetCustomerOperationForWorkflow(ctx, db, mustPgUUID(row.WorkflowRunID))
			if ownerErr != nil && !errors.Is(ownerErr, pgx.ErrNoRows) {
				return ownerErr
			}
			if err := workflowPreflightDiagnostics(&row, job.Selection.WorkflowRuns.Name, d, e, ownerErr == nil); err != nil {
				return err
			}
		}
		appendWorkflowRecoveryPreflight(out, row, retry)
	}
	return nil
}
func (m *MemStore) workflowRecoveryPreflightItemLocked(ctx context.Context, job *memEventRecoveryJob, item memEventRecoveryItem, now time.Time, retry bool) (api.EventRecoveryPreflightItem, error) {
	row := workflowPreflightIdentity(item.EventRecoveryItem, retry)
	if !retry {
		reason, err := m.initialReviewJobAdmissionLocked(job, now)
		if err != nil {
			return row, err
		}
		if reason != "" {
			row.Reason = reason
			return row, nil
		}
	}
	if !retry {
		var target api.WorkflowRunRecoveryTarget
		if err := json.Unmarshal(item.ExpectedProgress, &target); err != nil {
			return row, fmt.Errorf("decode recovery preflight evidence: %w", err)
		}
		if target.FailureEvidenceID != "" {
			run, exists := m.workflowRuns[target.RunID]
			if !exists || !m.initialFailureMatchesLocked(job.Job.AppID, job.Job.Selection.WorkflowRuns.Name, target, run.Status, now) {
				row.Reason = "failure_evidence_unavailable"
				return row, nil
			}
		}
	}
	if retry {
		execution := m.workflowRecoveryExecutionLocked(job, item, now)
		item.Execution = &execution
		if !workflowPreflightSaved(&row, item.EventRecoveryItem) {
			return row, nil
		}
	}
	d, err := m.workflowRunDiagnosticsLocked(ctx, WorkflowDiagnosticsOptions{RunID: row.WorkflowRunID, AccountID: job.AccountID, AppID: job.Job.AppID})
	_, owned := m.operationForWorkflowLocked(row.WorkflowRunID)
	err = workflowPreflightDiagnostics(&row, job.Job.Selection.WorkflowRuns.Name, d, err, owned)
	return row, err
}
func (s *PgStore) GetWorkflowRecoveryRetryPreflight(ctx context.Context, account, id string, now time.Time) (api.EventRecoveryPreflight, error) {
	if err := eventRecoveryIDs(account, id); err != nil {
		return api.EventRecoveryPreflight{}, err
	}
	if now.IsZero() {
		return api.EventRecoveryPreflight{}, ErrEventRecoveryQuery
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.EventRecoveryPreflight{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	job, err := getEventRecoveryMetadata(ctx, q, tx, account, id)
	if err != nil {
		return api.EventRecoveryPreflight{}, err
	}
	if job.Selection.WorkflowRuns == nil || !workflowRecoveryRetryParentTerminal(job.State) {
		return api.EventRecoveryPreflight{}, ErrEventRecoveryRetryParent
	}
	out := newRecoveryPreflight(job, now, now, now, 0)
	out.ReviewMode = "workflow_retry"
	if err = workflowRecoveryPreflightRead(ctx, q, tx, account, job, now, true, &out); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (m *MemStore) GetWorkflowRecoveryRetryPreflight(ctx context.Context, account, id string, now time.Time) (api.EventRecoveryPreflight, error) {
	if err := eventRecoveryIDs(account, id); err != nil {
		return api.EventRecoveryPreflight{}, err
	}
	if now.IsZero() {
		return api.EventRecoveryPreflight{}, ErrEventRecoveryQuery
	}
	if err := ctx.Err(); err != nil {
		return api.EventRecoveryPreflight{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job, err := m.eventRecoveryJobLocked(account, id)
	if err != nil {
		return api.EventRecoveryPreflight{}, err
	}
	if job.Job.Selection.WorkflowRuns == nil || !workflowRecoveryRetryParentTerminal(job.Job.State) {
		return api.EventRecoveryPreflight{}, ErrEventRecoveryRetryParent
	}
	if len(job.Items) > api.AutomationRecoveryBatchMaxRuns {
		return api.EventRecoveryPreflight{}, ErrEventRecoveryQuery
	}
	out := newRecoveryPreflight(job.Job, now, now, now, 0)
	out.ReviewMode = "workflow_retry"
	out.AppID, out.WorkflowName = job.Job.AppID, job.Job.Selection.WorkflowRuns.Name
	for _, item := range job.Items {
		row, err := m.workflowRecoveryPreflightItemLocked(ctx, job, item, now, true)
		if err != nil {
			return out, err
		}
		appendWorkflowRecoveryPreflight(&out, row, true)
	}
	return out, nil
}
