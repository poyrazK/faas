package state

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type ProjectRollbackHistoryStore interface {
	ListProjectRollbacks(context.Context, string, string) ([]api.ProjectRollbackSummary, error)
	ActiveProjectRollbackID(context.Context, string, string) (string, error)
}

func summarizeProjectRollback(op api.ProjectRollbackOperation) api.ProjectRollbackSummary {
	summary := api.ProjectRollbackSummary{
		ID: op.ID, Status: op.Status, Phase: op.Phase, UndoOf: op.UndoOf, UndoOperationID: op.UndoOperationID,
		PauseRequestedAt: op.PauseRequestedAt, CreatedAt: op.CreatedAt, UpdatedAt: op.UpdatedAt,
	}
	for _, step := range op.Steps {
		if step.Status != "skipped" {
			summary.CutoversTotal++
		}
		if step.Status == "rolled_back" {
			summary.CutoversCompleted++
		}
	}
	return summary
}

func (s *PgStore) ListProjectRollbacks(ctx context.Context, account, project string) ([]api.ProjectRollbackSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.receipt FROM project_rollback_operations o
		JOIN projects p ON p.id = o.project_id
		WHERE o.project_id = $1 AND p.account_id = $2
		ORDER BY (o.status = 'running') DESC,
		    NULLIF(o.receipt->>'created_at', '')::timestamptz DESC NULLS LAST, o.id DESC
		LIMIT $3`, project, account, api.ProjectRollbackHistoryLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]api.ProjectRollbackSummary, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var op api.ProjectRollbackOperation
		if err := json.Unmarshal(raw, &op); err != nil {
			return nil, err
		}
		out = append(out, summarizeProjectRollback(op))
	}
	return out, rows.Err()
}

func (s *PgStore) ActiveProjectRollbackID(ctx context.Context, account, project string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		SELECT o.id::text FROM project_rollback_operations o
		JOIN projects p ON p.id = o.project_id
		WHERE o.project_id = $1 AND p.account_id = $2 AND o.status = 'running'`, project, account).Scan(&id)
	return id, mapErr(err)
}

func (m *MemStore) ListProjectRollbacks(_ context.Context, account, project string) ([]api.ProjectRollbackSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]api.ProjectRollbackSummary, 0)
	if p, ok := m.projects[project]; !ok || p.AccountID != account {
		return out, nil
	}
	for _, op := range m.projectRollbackOperations {
		if op.ProjectID == project {
			out = append(out, summarizeProjectRollback(op.ProjectRollbackOperation))
		}
	}
	slices.SortFunc(out, compareProjectRollbackHistory)
	return out[:min(len(out), api.ProjectRollbackHistoryLimit)], nil
}

func compareProjectRollbackHistory(a, b api.ProjectRollbackSummary) int {
	if (a.Status == "running") != (b.Status == "running") {
		if a.Status == "running" {
			return -1
		}
		return 1
	}
	if (a.CreatedAt == nil) != (b.CreatedAt == nil) {
		if a.CreatedAt == nil {
			return 1
		}
		return -1
	}
	if a.CreatedAt != nil {
		// PostgreSQL timestamps compare at microsecond precision.
		order := b.CreatedAt.Round(time.Microsecond).Compare(a.CreatedAt.Round(time.Microsecond))
		if order != 0 {
			return order
		}
	}
	return cmp.Compare(b.ID, a.ID)
}

func (m *MemStore) ActiveProjectRollbackID(_ context.Context, account, project string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.projects[project]; ok && p.AccountID == account {
		for _, op := range m.projectRollbackOperations {
			if op.ProjectID == project && op.Status == "running" {
				return op.ID, nil
			}
		}
	}
	return "", ErrNotFound
}

var _ ProjectRollbackHistoryStore = (*PgStore)(nil)
var _ ProjectRollbackHistoryStore = (*MemStore)(nil)
