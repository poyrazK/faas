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

func validateWorkflowRecoveryRetryParent(parent sqlc.EventRecoveryJob, app, name string) error {
	if uuidString(parent.AppID) != canonicalMemUUID(app) {
		return ErrNotFound
	}
	var selection api.EventRecoveryRequest
	if err := json.Unmarshal(parent.Selection, &selection); err != nil {
		return fmt.Errorf("decode retry parent: %w", err)
	}
	if selection.Mode != "workflow" || selection.WorkflowRuns == nil || selection.WorkflowRuns.Name != name || !workflowRecoveryRetryParentTerminal(parent.State) {
		return ErrEventRecoveryRetryParent
	}
	return nil
}

func workflowRecoveryRetryParentTerminal(state string) bool {
	return state == "completed" || state == "cancelled" || state == "expired"
}

func workflowRunRetryCandidates(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app string, req api.EventRecoveryRequest) ([]sqlc.EventRecoveryCandidatesRow, error) {
	parent, err := q.EventRecoveryPreflightJob(ctx, db, sqlc.EventRecoveryPreflightJobParams{AccountID: mustPgUUID(account), JobID: mustPgUUID(req.ParentJobID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read workflow retry parent: %w", err)
	}
	if err = validateWorkflowRecoveryRetryParent(parent, app, req.WorkflowRuns.Name); err != nil {
		return nil, err
	}
	rows := make([]sqlc.EventRecoveryCandidatesRow, 0, len(req.WorkflowRuns.Runs))
	now := time.Now().UTC()
	for _, target := range req.WorkflowRuns.Runs {
		row, err := q.WorkflowRunRecoveryRetryCandidate(ctx, db, sqlc.WorkflowRunRecoveryRetryCandidateParams{ParentJobID: mustPgUUID(req.ParentJobID), ParentPosition: target.ParentPosition, AccountID: mustPgUUID(account), AppID: mustPgUUID(app), WorkflowName: req.WorkflowRuns.Name, RunID: mustPgUUID(target.RunID), ExpectedResumeCount: int32(target.ExpectedResumeCount), NowAt: pgtypeFromTime(now)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEventRecoveryChanged
		}
		if err != nil {
			return nil, fmt.Errorf("select reviewed workflow retry: %w", err)
		}
		rows = append(rows, sqlc.EventRecoveryCandidatesRow(row))
	}
	return rows, nil
}

func (m *MemStore) workflowRecoveryRetryParentLocked(account, app string, req api.EventRecoveryRequest) (*memEventRecoveryJob, error) {
	parent, err := m.eventRecoveryJobLocked(account, req.ParentJobID)
	if err != nil {
		return nil, err
	}
	if !sameMemUUID(parent.Job.AppID, app) {
		return nil, ErrNotFound
	}
	if parent.Job.Selection.Mode != "workflow" || parent.Job.Selection.WorkflowRuns == nil || parent.Job.Selection.WorkflowRuns.Name != req.WorkflowRuns.Name || !workflowRecoveryRetryParentTerminal(parent.Job.State) {
		return nil, ErrEventRecoveryRetryParent
	}
	return parent, nil
}

func workflowRecoveryRetryTargetMatches(parent *memEventRecoveryJob, target api.WorkflowRunRecoveryTarget, now time.Time) bool {
	for _, item := range parent.Items {
		if item.Position != target.ParentPosition || item.State != "queued" || item.WorkflowRunID != target.RunID || item.ExpectedResumeCount == nil || *item.ExpectedResumeCount+1 != target.ExpectedResumeCount {
			continue
		}
		saved, ok := parent.WorkflowResults[item.Position]
		return ok && saved.RunID == target.RunID && saved.ResumeNumber == target.ExpectedResumeCount && saved.Execution.State == "failed" && saved.Execution.RecordedAt != nil && !saved.Execution.RecordedAt.After(now)
	}
	return false
}
