package state

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ExecutionPipelineHistoryEntry is a metadata-only projection for the dashboard.
type ExecutionPipelineHistoryEntry struct {
	ID, WorkflowID, Outcome, ParentID string
	StepCount                         int
	CreatedAt                         time.Time
	FinishedAt                        *time.Time
}

type ExecutionPipelineHistoryStore interface {
	ListExecutionPipelineHistory(context.Context, string, cursor.Key, int32) ([]ExecutionPipelineHistoryEntry, error)
}

func (s *PgStore) ListExecutionPipelineHistory(ctx context.Context, account string, before cursor.Key, limit int32) ([]ExecutionPipelineHistoryEntry, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	acct, err := parsePgUUID(account)
	if err != nil {
		return nil, ErrNotFound
	}
	params := sqlc.PipelineHistoryPageParams{AccountID: acct, PageLimit: limit}
	if !before.IsZero() {
		params.BeforeID, err = parsePgUUID(before.ID)
		if err != nil {
			return nil, ErrInvalidArgument
		}
		params.BeforeAt = executionTime(before.CreatedAt)
	}
	rows, err := sqlc.New().PipelineHistoryPage(ctx, s.pool, params)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ExecutionPipelineHistoryEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, ExecutionPipelineHistoryEntry{
			ID: pgUUIDString(row.ID), WorkflowID: row.WorkflowID, Outcome: row.Outcome, ParentID: row.ParentID,
			StepCount: int(row.StepCount), CreatedAt: row.CreatedAt.Time, FinishedAt: timestamptzToTimePtr(row.FinishedAt),
		})
	}
	return out, nil
}

func (m *MemStore) ListExecutionPipelineHistory(_ context.Context, account string, before cursor.Key, limit int32) ([]ExecutionPipelineHistoryEntry, error) {
	if limit < 1 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ExecutionPipelineHistoryEntry
	for id, pipeline := range m.executionPipelines {
		job := m.executionWorkflowJobs[id]
		if job.AccountID != account || (!before.IsZero() && (job.CreatedAt.After(before.CreatedAt) ||
			job.CreatedAt.Equal(before.CreatedAt) && job.ID >= before.ID)) {
			continue
		}
		item := ExecutionPipelineHistoryEntry{ID: id, WorkflowID: job.WorkflowID, Outcome: string(job.Status),
			StepCount: job.StepCount, CreatedAt: job.CreatedAt}
		if job.FinishedAt != nil {
			finished := *job.FinishedAt
			item.FinishedAt = &finished
		}
		if pipeline.Completion != nil {
			item.Outcome = pipeline.Completion.Outcome
		}
		if pipeline.Policy.Rerun != nil {
			item.ParentID = pipeline.Policy.Rerun.ParentID
		}
		out = append(out, item)
	}
	slices.SortFunc(out, func(a, b ExecutionPipelineHistoryEntry) int {
		if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
			return order
		}
		return cmp.Compare(b.ID, a.ID)
	})
	return out[:min(len(out), int(limit))], nil
}
