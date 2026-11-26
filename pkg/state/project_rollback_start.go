package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrProjectRollbackRequestReused = errors.New("state: project rollback request ID reused with different options")

type ProjectRollbackStartStore interface {
	GetProjectRollbackStart(context.Context, string, string, api.ProjectRollbackRequest) (ProjectRollbackRecord, error)
	CreateProjectRollbackStart(context.Context, string, ProjectRollbackRecord, api.ProjectRollbackRequest) (ProjectRollbackRecord, bool, error)
}

func (s *PgStore) GetProjectRollbackStart(ctx context.Context, account, project string, req api.ProjectRollbackRequest) (ProjectRollbackRecord, error) {
	var op ProjectRollbackRecord
	var receipt, options []byte
	err := s.pool.QueryRow(ctx, `
		SELECT o.version, o.next_run_at, o.receipt, o.start_request_options
		FROM project_rollback_operations o
		JOIN projects p ON p.id = o.project_id
		WHERE o.project_id = $1 AND p.account_id = $2 AND o.start_request_id = $3`,
		project, account, req.RequestID).Scan(&op.Version, &op.nextRun, &receipt, &options)
	if err != nil {
		return op, mapErr(err)
	}
	if err := json.Unmarshal(options, &op.startRequest); err != nil {
		return op, err
	}
	if op.startRequest != req {
		return op, ErrProjectRollbackRequestReused
	}
	if err := json.Unmarshal(receipt, &op); err != nil {
		return op, err
	}
	return op, nil
}

// CreateProjectRollbackStart saves the exact plan and its request key in one
// insert. A concurrent retry reads the winner after a uniqueness conflict.
func (s *PgStore) CreateProjectRollbackStart(ctx context.Context, account string, op ProjectRollbackRecord, req api.ProjectRollbackRequest) (ProjectRollbackRecord, bool, error) {
	if req.RequestID == "" {
		return op, false, ErrInvalidArgument
	}
	op.RequestID, op.startRequest = req.RequestID, req
	err := createProjectRollback(ctx, s.pool, account, op)
	if !errors.Is(err, ErrConflict) {
		return op, err == nil, err
	}
	saved, lookupErr := s.GetProjectRollbackStart(ctx, account, op.ProjectID, req)
	if errors.Is(lookupErr, ErrNotFound) {
		return op, false, err
	}
	return saved, false, lookupErr
}

func (m *MemStore) GetProjectRollbackStart(_ context.Context, account, project string, req api.ProjectRollbackRequest) (ProjectRollbackRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getProjectRollbackStartLocked(account, project, req)
}

func (m *MemStore) getProjectRollbackStartLocked(account, project string, req api.ProjectRollbackRequest) (ProjectRollbackRecord, error) {
	p, exists := m.projects[project]
	if !exists || p.AccountID != account || req.RequestID == "" {
		return ProjectRollbackRecord{}, ErrNotFound
	}
	for _, op := range m.projectRollbackOperations {
		if op.ProjectID == project && op.startRequest.RequestID == req.RequestID {
			if op.startRequest != req {
				return ProjectRollbackRecord{}, ErrProjectRollbackRequestReused
			}
			return cloneProjectRollback(op), nil
		}
	}
	return ProjectRollbackRecord{}, ErrNotFound
}

func (m *MemStore) CreateProjectRollbackStart(ctx context.Context, account string, op ProjectRollbackRecord, req api.ProjectRollbackRequest) (ProjectRollbackRecord, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if req.RequestID == "" {
		return op, false, ErrInvalidArgument
	}
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return op, false, err
	}
	if saved, err := m.getProjectRollbackStartLocked(account, op.ProjectID, req); !errors.Is(err, ErrNotFound) {
		return saved, false, err
	}
	op.RequestID, op.startRequest = req.RequestID, req
	if err := m.createProjectRollbackLocked(account, op); err != nil {
		return op, false, err
	}
	return cloneProjectRollback(op), true, nil
}

var _ ProjectRollbackStartStore = (*PgStore)(nil)
var _ ProjectRollbackStartStore = (*MemStore)(nil)
