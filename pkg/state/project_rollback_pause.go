package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrProjectRollbackPaused = errors.New("state: project rollback paused")

type ProjectRollbackPauseStore interface {
	PauseProjectRollback(context.Context, string, string, string) (ProjectRollbackRecord, bool, error)
}

func requestProjectRollbackPause(op *ProjectRollbackRecord, now time.Time) (bool, error) {
	if op.Status == "blocked" || op.PauseRequestedAt != nil {
		return false, nil
	}
	if op.Status != "running" {
		return false, ErrConflict
	}
	op.Phase = api.ProjectRollbackReleasing
	op.PauseRequestedAt, op.UpdatedAt = &now, &now
	return true, nil
}

func (s *PgStore) PauseProjectRollback(ctx context.Context, account, project, id string) (ProjectRollbackRecord, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectRollbackRecord{}, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// The checked cutover holds this row through its traffic write.
	op, err := scanProjectRollback(tx.QueryRow(ctx, `
		SELECT o.version, o.next_run_at, o.receipt FROM project_rollback_operations o
		JOIN projects p ON p.id = o.project_id
		WHERE o.id = $1 AND o.project_id = $2 AND p.account_id = $3 FOR UPDATE OF o`, id, project, account))
	if err != nil {
		return op, false, err
	}
	changed, err := requestProjectRollbackPause(&op, time.Now().UTC())
	if err != nil || !changed {
		return op, false, err
	}
	raw, err := json.Marshal(op)
	if err != nil {
		return op, false, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE project_rollback_operations SET receipt = $2,
		    version = version + 1, next_run_at = clock_timestamp()
		WHERE id = $1`, id, raw)
	if err != nil {
		return op, false, mapErr(err)
	}
	if tag.RowsAffected() != 1 {
		return op, false, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return op, false, err
	}
	return op, true, nil
}

func (m *MemStore) PauseProjectRollback(ctx context.Context, account, project, id string) (ProjectRollbackRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return ProjectRollbackRecord{}, false, err
	}
	op, ok := m.projectRollbackOperations[id]
	if !ok || op.ProjectID != project || m.projects[project].AccountID != account {
		return ProjectRollbackRecord{}, false, ErrNotFound
	}
	now := time.Now()
	changed, err := requestProjectRollbackPause(&op, now.UTC())
	if changed {
		op.Version++
		op.nextRun = now
		m.projectRollbackOperations[id] = op
	}
	return cloneProjectRollback(op), changed, err
}

// Early project rollbacks used the planned checked attempt ID in place of
// an explicit child-to-project link.
func (m *MemStore) legacyProjectRollbackPausedLocked(child api.RollbackOperation) bool {
	for _, op := range m.projectRollbackOperations {
		if op.PauseRequestedAt == nil {
			continue
		}
		for _, step := range op.Steps {
			if step.AttemptID == child.ID && step.AppID == child.AppID &&
				step.TargetID == child.TargetDeploymentID && step.CurrentID == child.CurrentDeploymentID {
				return true
			}
		}
	}
	return false
}

var _ ProjectRollbackPauseStore = (*PgStore)(nil)
var _ ProjectRollbackPauseStore = (*MemStore)(nil)
