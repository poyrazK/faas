package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) StartWorkflowPlaybookProgress(ctx context.Context, a, t string, o api.OperationWorkflowCoordinationTimelineOptions, actor string, r api.OperationWorkflowPlaybookProgressStartRequest, b bool) (api.OperationWorkflowPlaybookProgress, error) {
	return startWorkflowPlaybookProgress(ctx, m, a, t, o, actor, r, b)
}
func (m *MemStore) UpdateWorkflowPlaybookProgress(ctx context.Context, a, t string, o api.OperationWorkflowCoordinationTimelineOptions, id, actor string, r api.OperationWorkflowPlaybookProgressUpdateRequest, b bool) (api.OperationWorkflowPlaybookProgress, error) {
	return updateWorkflowPlaybookProgress(ctx, m, a, t, o, id, actor, r, b)
}
func (m *MemStore) readPlaybookProgress(ctx context.Context, r workflowPlaybookProgressRecord) ([]api.OperationWorkflowPlaybookProgress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []api.OperationWorkflowPlaybookProgress{}
	for _, row := range m.operationMemoryLocked().playbookProgress {
		if row.key() == r.key() {
			out = append(out, clonePlaybookProgress(row.Progress))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, ctx.Err()
}
func (m *MemStore) savePlaybookProgress(ctx context.Context, r workflowPlaybookProgressRecord, revision int64) (api.OperationWorkflowPlaybookProgress, error) {
	return m.savePlaybookProgressReview(ctx, r, revision, "", 0)
}
func (m *MemStore) savePlaybookProgressReview(ctx context.Context, r workflowPlaybookProgressRecord, revision int64, targetID string, targetRevision int64) (api.OperationWorkflowPlaybookProgress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return r.Progress, err
	}
	data := m.operationMemoryLocked()
	if data.playbookProgress == nil {
		data.playbookProgress = map[string]workflowPlaybookProgressRecord{}
	}
	old, exists := data.playbookProgress[r.Progress.ID]
	if exists && old.key() != r.key() {
		return r.Progress, ErrNotFound
	}
	if revision == 0 && exists {
		return clonePlaybookProgress(old.Progress), nil
	}
	if revision > 0 && (!exists || old.Progress.Revision != revision || !workflowPlaybookProgressActive(old.Progress)) {
		return r.Progress, ErrConflict
	}
	if revision == 0 {
		count := 0
		for _, row := range data.playbookProgress {
			if row.key() == r.key() {
				count++
			}
		}
		if count >= api.OperationWorkflowPlaybookProgressPerCaseMax {
			return r.Progress, NewOperationLimitError("playbook_progress_per_case", api.OperationWorkflowPlaybookProgressPerCaseMax, int64(count+1))
		}
	}

	if targetID != "" {
		rows := []api.OperationWorkflowPlaybookProgress{}
		for _, row := range data.playbookProgress {
			if row.key() == r.key() {
				rows = append(rows, row.Progress)
			}
		}
		if err := validatePlaybookReplacement(rows, targetID, targetRevision); err != nil {
			return r.Progress, err
		}
	}
	r.Progress.Applicability = nil
	r.Progress.ReplacesProgressIDs = nil
	r.Progress = clonePlaybookProgress(r.Progress)
	data.playbookProgress[r.Progress.ID] = r
	return clonePlaybookProgress(r.Progress), nil
}

func (s *MemStore) ReviewWorkflowPlaybookProgress(ctx context.Context, a, t string, o api.OperationWorkflowCoordinationTimelineOptions, id, actor string, r api.OperationWorkflowPlaybookReviewRequest, b bool) (api.OperationWorkflowPlaybookProgress, error) {
	return reviewWorkflowPlaybookProgress(ctx, s, a, t, o, id, actor, r, b)
}
