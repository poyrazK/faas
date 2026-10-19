package state

import (
	"context"
	"reflect"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) CreateMigrationCutover(_ context.Context, r MigrationCutoverRecord) (MigrationCutoverRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.migrationCutovers[r.Report.ID]; ok {
		if old.AccountID != r.AccountID || old.AppID != r.AppID {
			return r, ErrNotFound
		}
		if !reflect.DeepEqual(old.Report.Request, r.Report.Request) {
			return r, ErrConflict
		}
		return cloneMigrationCutover(old), nil
	}
	app, ok := m.apps[r.AppID]
	if !ok || app.AccountID != r.AccountID || app.Status == AppDeleted {
		return r, ErrNotFound
	}
	count := 0
	for id, old := range m.migrationCutovers {
		if old.AppID != r.AppID {
			continue
		}
		switch old.Report.Status {
		case "completed", "cancelled", "restored", "blocked":
			if old.Report.CreatedAt.Before(time.Now().Add(-api.MigrationCutoverRetention)) {
				delete(m.migrationCutovers, id)
				continue
			}
		}
		count++
	}
	if count >= api.MigrationCutoverListLimit {
		return r, ErrConflict
	}
	if m.migrationCutovers == nil {
		m.migrationCutovers = map[string]MigrationCutoverRecord{}
	}
	r.Version = 1
	m.migrationCutovers[r.Report.ID] = cloneMigrationCutover(r)
	return r, nil
}
func (m *MemStore) GetMigrationCutover(_ context.Context, account, app, id string) (MigrationCutoverRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.migrationCutovers[id]
	if !ok || r.AccountID != account || r.AppID != app {
		return MigrationCutoverRecord{}, ErrNotFound
	}
	return cloneMigrationCutover(r), nil
}
func (m *MemStore) ListMigrationCutovers(_ context.Context, account, app string) ([]MigrationCutoverRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []MigrationCutoverRecord{}
	for _, r := range m.migrationCutovers {
		if r.AccountID == account && r.AppID == app {
			out = append(out, cloneMigrationCutover(r))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Report.CreatedAt.Equal(out[j].Report.CreatedAt) {
			return out[i].Report.ID > out[j].Report.ID
		}
		return out[i].Report.CreatedAt.After(out[j].Report.CreatedAt)
	})
	return out, nil
}
func (m *MemStore) SaveMigrationCutover(_ context.Context, r MigrationCutoverRecord, version int64) (MigrationCutoverRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.migrationCutovers[r.Report.ID]
	if !ok || old.AccountID != r.AccountID || old.AppID != r.AppID {
		return r, ErrNotFound
	}
	if old.Version != version {
		return cloneMigrationCutover(old), ErrConflict
	}
	r.Version = version + 1
	m.migrationCutovers[r.Report.ID] = cloneMigrationCutover(r)
	return r, nil
}
func (m *MemStore) ListDueMigrationCutovers(_ context.Context, now time.Time, limit int32) ([]MigrationCutoverRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > api.MigrationCutoverWorkerBatch {
		limit = api.MigrationCutoverWorkerBatch
	}
	out := []MigrationCutoverRecord{}
	for _, r := range m.migrationCutovers {
		due := migrationCutoverDue(r)
		if !due.IsZero() && !due.After(now) {
			out = append(out, cloneMigrationCutover(r))
		}
	}
	sort.Slice(out, func(i, j int) bool { return migrationCutoverDue(out[i]).Before(migrationCutoverDue(out[j])) })
	if len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}
