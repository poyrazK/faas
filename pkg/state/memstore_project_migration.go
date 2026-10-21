package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) CreateProjectMigration(_ context.Context, r ProjectMigrationRecord) (ProjectMigrationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.projectMigrations[r.Report.ID]; ok {
		if old.AccountID != r.AccountID {
			return r, ErrNotFound
		}
		if old.RequestDigest != r.RequestDigest {
			return r, ErrConflict
		}
		return cloneProjectMigration(old), nil
	}
	if _, ok := m.accounts[r.AccountID]; !ok {
		return r, ErrNotFound
	}
	count := 0
	for id, old := range m.projectMigrations {
		if old.AccountID != r.AccountID {
			continue
		}
		if old.Report.CreatedAt.Before(time.Now().Add(-api.ProjectMigrationRetention)) && !old.LeaseUntil.After(time.Now()) {
			delete(m.projectMigrations, id)
			continue
		}
		count++
	}
	if count >= api.ProjectMigrationListLimit {
		return r, ErrConflict
	}
	if m.projectMigrations == nil {
		m.projectMigrations = map[string]ProjectMigrationRecord{}
	}
	r.Version = 1
	m.projectMigrations[r.Report.ID] = cloneProjectMigration(r)
	return r, nil
}
func (m *MemStore) GetProjectMigration(_ context.Context, account, id string) (ProjectMigrationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.projectMigrations[id]
	if !ok || r.AccountID != account {
		return ProjectMigrationRecord{}, ErrNotFound
	}
	return cloneProjectMigration(r), nil
}
func (m *MemStore) ListProjectMigrations(_ context.Context, account, project string) ([]ProjectMigrationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []ProjectMigrationRecord{}
	for _, r := range m.projectMigrations {
		if r.AccountID == account && (project == "" || r.Report.Project == project) {
			out = append(out, cloneProjectMigration(r))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Report.CreatedAt.Equal(out[j].Report.CreatedAt) {
			return out[i].Report.ID > out[j].Report.ID
		}
		return out[i].Report.CreatedAt.After(out[j].Report.CreatedAt)
	})
	if len(out) > api.ProjectMigrationListLimit {
		out = out[:api.ProjectMigrationListLimit]
	}
	return out, nil
}
func (m *MemStore) SaveProjectMigration(_ context.Context, r ProjectMigrationRecord, version int64) (ProjectMigrationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.projectMigrations[r.Report.ID]
	if !ok || old.AccountID != r.AccountID {
		return r, ErrNotFound
	}
	if old.Version != version {
		return cloneProjectMigration(old), ErrConflict
	}
	if r.LeaseUntil.After(time.Now()) {
		for id, other := range m.projectMigrations {
			if id != r.Report.ID && other.AccountID == r.AccountID && other.Report.Project == r.Report.Project && other.LeaseUntil.After(time.Now()) {
				return r, ErrConflict
			}
		}
	}
	r.Version = version + 1
	m.projectMigrations[r.Report.ID] = cloneProjectMigration(r)
	return r, nil
}
