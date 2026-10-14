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

func workflowRunRecoveryItem(item *api.EventRecoveryItem, encoded []byte) {
	var target api.WorkflowRunRecoveryTarget
	if json.Unmarshal(encoded, &target) == nil && target.RunID != "" {
		item.WorkflowRunID = target.RunID
		count := target.ExpectedResumeCount
		item.ExpectedResumeCount = &count
	}
}
func workflowRunRecoveryCandidates(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app string, req api.EventRecoveryRequest) ([]sqlc.EventRecoveryCandidatesRow, error) {
	if req.ParentJobID != "" {
		return workflowRunRetryCandidates(ctx, q, db, account, app, req)
	}
	rows := make([]sqlc.EventRecoveryCandidatesRow, 0, len(req.WorkflowRuns.Runs))
	for _, t := range req.WorkflowRuns.Runs {
		row, err := q.WorkflowRunRecoveryCandidate(ctx, db, sqlc.WorkflowRunRecoveryCandidateParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), RunID: mustPgUUID(t.RunID), WorkflowName: req.WorkflowRuns.Name, FailureEvidenceID: t.FailureEvidenceID, ObservedAt: pgtypeFromTime(time.Now().UTC()), RetainedAfter: pgtypeFromTime(time.Now().UTC().Add(-api.WorkflowRecoveryFailureRetention)), ExpectedResumeCount: int32(t.ExpectedResumeCount)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEventRecoveryChanged
		}
		if err != nil {
			return nil, fmt.Errorf("select workflow recovery run: %w", err)
		}
		rows = append(rows, sqlc.EventRecoveryCandidatesRow(row))
	}
	return rows, nil
}
func (m *MemStore) workflowRunRecoveryCandidatesLocked(account, app string, req api.EventRecoveryRequest) ([]memEventRecoveryItem, error) {
	owned, ok := m.eventSubscriptionAppLocked(app)
	if !ok || !sameMemUUID(owned.AccountID, account) {
		return nil, ErrNotFound
	}
	var parent *memEventRecoveryJob
	if req.ParentJobID != "" {
		var err error
		parent, err = m.workflowRecoveryRetryParentLocked(account, app, req)
		if err != nil {
			return nil, err
		}
	}
	items := make([]memEventRecoveryItem, 0, len(req.WorkflowRuns.Runs))
	for _, t := range req.WorkflowRuns.Runs {
		run, ok := m.workflowRuns[t.RunID]
		if !ok || !sameMemUUID(run.AppID, app) || run.WorkflowName != req.WorkflowRuns.Name || run.ResumeCount != t.ExpectedResumeCount || run.Status != WorkflowRunStatusFailed && run.Status != WorkflowRunStatusDead {
			return nil, ErrEventRecoveryChanged
		}
		if t.FailureEvidenceID != "" && !m.initialFailureMatchesLocked(app, req.WorkflowRuns.Name, t, run.Status, time.Now().UTC()) {
			return nil, ErrEventRecoveryChanged
		}
		if parent != nil && !workflowRecoveryRetryTargetMatches(parent, t, time.Now().UTC()) {
			return nil, ErrEventRecoveryChanged
		}
		encoded, err := json.Marshal(struct {
			api.WorkflowRunRecoveryTarget
			ParentJobID string `json:"parent_job_id,omitempty"`
		}{t, req.ParentJobID})
		if err != nil {
			return nil, err
		}
		count := t.ExpectedResumeCount
		item := memEventRecoveryItem{ExpectedProgress: encoded, EventRecoveryItem: api.EventRecoveryItem{WorkflowRunID: t.RunID, ExpectedResumeCount: &count, Position: int64(len(items) + 1), FailedAt: run.UpdatedAt, FailureCode: run.Status, State: "pending"}}
		recoveryItemLineage(&item.EventRecoveryItem, encoded)
		items = append(items, item)
	}
	return items, nil
}
func workflowRunRecoveryOutcome(err error) (string, string, error) {
	switch {
	case err == nil:
		return "queued", "", nil
	case errors.Is(err, ErrWorkflowRunQuotaExceeded):
		return "pending", "app", nil
	case errors.Is(err, ErrWorkflowResumeConflict):
		return "skipped", "changed", nil
	case errors.Is(err, ErrWorkflowRunNotFound), errors.Is(err, ErrWorkflowResumeUnavailable), errors.Is(err, ErrWorkflowResumeUnsafe), errors.Is(err, ErrWorkflowResumeLimit):
		return "skipped", "target_unavailable", nil
	default:
		return "", "", err
	}
}
func processWorkflowRunRecoveryTx(ctx context.Context, tx pgx.Tx, job sqlc.EventRecoveryJob, item sqlc.EventRecoveryItem, selection api.EventRecoveryRequest) (string, string, error) {
	if reason, err := initialReviewJobAdmissionTx(ctx, sqlc.New(), tx, uuidString(job.ID), time.Now().UTC()); err != nil {
		return "", "", err
	} else if reason != "" {
		return "skipped", initialReviewSkipReason(reason), nil
	}
	var target api.WorkflowRunRecoveryTarget
	if err := json.Unmarshal(item.ExpectedProgress, &target); err != nil {
		return "", "", fmt.Errorf("decode workflow recovery target: %w", err)
	}
	found := false
	for _, t := range selection.WorkflowRuns.Runs {
		if t == target {
			found = true
			break
		}
	}
	if !found {
		return "skipped", "changed", nil
	}
	_, err := workflowRunRecoveryCandidates(ctx, sqlc.New(), tx, uuidString(job.AccountID), uuidString(job.AppID), api.EventRecoveryRequest{WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: selection.WorkflowRuns.Name, Runs: []api.WorkflowRunRecoveryTarget{target}}})
	if errors.Is(err, ErrEventRecoveryChanged) {
		return "skipped", "changed", nil
	}
	if err != nil {
		return "", "", err
	}
	_, _, _, err = resumeWorkflowRunTx(ctx, tx, WorkflowResumeOptions{RunID: target.RunID, AppID: uuidString(job.AppID), AccountID: uuidString(job.AccountID), ExpectedResumeCount: target.ExpectedResumeCount}, false)
	return workflowRunRecoveryOutcome(err)
}
func (m *MemStore) processWorkflowRunRecoveryLocked(job *memEventRecoveryJob, item *memEventRecoveryItem) (string, string, error) {
	if reason, err := m.initialReviewJobAdmissionLocked(job, time.Now().UTC()); err != nil {
		return "", "", err
	} else if reason != "" {
		return "skipped", initialReviewSkipReason(reason), nil
	}
	owned, ok := m.eventSubscriptionAppLocked(job.Job.AppID)
	if !ok || !sameMemUUID(owned.AccountID, job.AccountID) {
		return "skipped", "target_unavailable", nil
	}
	run, ok := m.workflowRuns[item.WorkflowRunID]
	if !ok || !sameMemUUID(run.AppID, owned.ID) || run.WorkflowName != job.Job.Selection.WorkflowRuns.Name {
		return "skipped", "changed", nil
	}
	if item.ExpectedResumeCount == nil {
		return "skipped", "changed", nil
	}
	var target api.WorkflowRunRecoveryTarget
	if err := json.Unmarshal(item.ExpectedProgress, &target); err != nil {
		return "", "", fmt.Errorf("decode initial recovery evidence: %w", err)
	}
	if target.FailureEvidenceID != "" && !m.initialFailureMatchesLocked(owned.ID, run.WorkflowName, target, run.Status, time.Now().UTC()) {
		return "skipped", "changed", nil
	}
	_, _, _, err := m.resumeWorkflowRunLocked(WorkflowResumeOptions{RunID: run.ID, AppID: owned.ID, AccountID: owned.AccountID, ExpectedResumeCount: *item.ExpectedResumeCount}, false)
	return workflowRunRecoveryOutcome(err)
}
