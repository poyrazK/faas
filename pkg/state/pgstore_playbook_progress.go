package state

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) StartWorkflowPlaybookProgress(ctx context.Context, a, t string, o api.OperationWorkflowCoordinationTimelineOptions, actor string, r api.OperationWorkflowPlaybookProgressStartRequest, b bool) (api.OperationWorkflowPlaybookProgress, error) {
	return startWorkflowPlaybookProgress(ctx, s, a, t, o, actor, r, b)
}
func (s *PgStore) UpdateWorkflowPlaybookProgress(ctx context.Context, a, t string, o api.OperationWorkflowCoordinationTimelineOptions, id, actor string, r api.OperationWorkflowPlaybookProgressUpdateRequest, b bool) (api.OperationWorkflowPlaybookProgress, error) {
	return updateWorkflowPlaybookProgress(ctx, s, a, t, o, id, actor, r, b)
}
func playbookProgressQuery(r workflowPlaybookProgressRecord) sqlc.ListWorkflowPlaybookProgressParams {
	a, _ := operationUUID(r.Account)
	p, _ := operationUUID(r.App)
	t, _ := operationUUID(r.Tenant)
	return sqlc.ListWorkflowPlaybookProgressParams{AccountID: a, AppID: p, TenantID: t, CaseKey: r.key(), Limit: api.OperationWorkflowPlaybookProgressPerCaseMax}
}
func readPGPlaybookProgress(ctx context.Context, db sqlc.DBTX, r workflowPlaybookProgressRecord) ([]api.OperationWorkflowPlaybookProgress, error) {
	rows, err := sqlc.New().ListWorkflowPlaybookProgress(ctx, db, playbookProgressQuery(r))
	if err != nil {
		return nil, mapErr(err)
	}
	out := []api.OperationWorkflowPlaybookProgress{}
	for _, raw := range rows {
		var p api.OperationWorkflowPlaybookProgress
		if err = json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
func (s *PgStore) readPlaybookProgress(ctx context.Context, r workflowPlaybookProgressRecord) ([]api.OperationWorkflowPlaybookProgress, error) {
	return readPGPlaybookProgress(ctx, s.pool, r)
}
func (s *PgStore) savePlaybookProgress(ctx context.Context, r workflowPlaybookProgressRecord, revision int64) (api.OperationWorkflowPlaybookProgress, error) {
	return s.savePlaybookProgressReview(ctx, r, revision, "", 0)
}
func (s *PgStore) savePlaybookProgressReview(ctx context.Context, r workflowPlaybookProgressRecord, revision int64, targetID string, targetRevision int64) (api.OperationWorkflowPlaybookProgress, error) {
	r.Progress = clonePlaybookProgress(r.Progress)
	r.Progress.Applicability = nil
	r.Progress.ReplacesProgressIDs = nil
	q := sqlc.New()
	params := playbookProgressQuery(r)
	id, _ := operationUUID(r.Progress.ID)
	raw, err := json.Marshal(r.Progress)
	if err != nil {
		return r.Progress, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return r.Progress, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize starts and quota checks across all writers; updates are also revision fenced.
	if _, err = q.LockCustomerOperationAccount(ctx, tx, params.AccountID); err != nil {
		return r.Progress, mapErr(err)
	}

	rows, err := readPGPlaybookProgress(ctx, tx, r)
	if err != nil {
		return r.Progress, err
	}
	if err = validatePlaybookReplacement(rows, targetID, targetRevision); err != nil {
		return r.Progress, err
	}
	if revision > 0 {
		found := false
		for _, old := range rows {
			if old.ID == r.Progress.ID {
				found = true
				if old.Revision != revision || !workflowPlaybookProgressActive(old) {
					return r.Progress, ErrConflict
				}
				break
			}
		}
		if !found {
			return r.Progress, ErrNotFound
		}
	}
	if revision == 0 {
		for _, old := range rows {
			if old.ID == r.Progress.ID {
				return old, nil
			}
		}
		if len(rows) >= api.OperationWorkflowPlaybookProgressPerCaseMax {
			return r.Progress, NewOperationLimitError("playbook_progress_per_case", api.OperationWorkflowPlaybookProgressPerCaseMax, int64(len(rows)+1))
		}
		n, err := q.InsertWorkflowPlaybookProgress(ctx, tx, sqlc.InsertWorkflowPlaybookProgressParams{ID: id, AccountID: params.AccountID, AppID: params.AppID, TenantID: params.TenantID, CaseKey: params.CaseKey, Revision: r.Progress.Revision, Payload: raw})
		if err != nil {
			return r.Progress, mapErr(err)
		}
		if n != 1 {
			return r.Progress, ErrConflict
		}
	} else {
		n, err := q.UpdateWorkflowPlaybookProgress(ctx, tx, sqlc.UpdateWorkflowPlaybookProgressParams{ID: id, AccountID: params.AccountID, AppID: params.AppID, TenantID: params.TenantID, CaseKey: params.CaseKey, Revision: revision, NextRevision: r.Progress.Revision, Payload: raw})
		if err != nil {
			return r.Progress, mapErr(err)
		}
		if n != 1 {
			return r.Progress, ErrConflict
		}
	}
	return r.Progress, tx.Commit(ctx)
}

func (s *PgStore) ReviewWorkflowPlaybookProgress(ctx context.Context, a, t string, o api.OperationWorkflowCoordinationTimelineOptions, id, actor string, r api.OperationWorkflowPlaybookReviewRequest, b bool) (api.OperationWorkflowPlaybookProgress, error) {
	return reviewWorkflowPlaybookProgress(ctx, s, a, t, o, id, actor, r, b)
}
