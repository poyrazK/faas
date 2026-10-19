package state

import (
	"context"
	"reflect"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) CreateMigrationVerification(_ context.Context, r MigrationVerificationRecord) (MigrationVerificationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.migrationVerifications[r.Report.ID]; ok {
		if old.AccountID != r.AccountID || old.AppID != r.AppID {
			return r, ErrNotFound
		}
		if !reflect.DeepEqual(old.Report.Request, r.Report.Request) {
			return r, ErrConflict
		}
		return cloneMigrationVerification(old), nil
	}
	app, ok := m.apps[r.AppID]
	if !ok || app.AccountID != r.AccountID || app.Status == AppDeleted {
		return r, ErrNotFound
	}
	active := 0
	for id, old := range m.migrationVerifications {
		if old.Report.Status != "pending" && old.Report.CreatedAt.Before(time.Now().Add(-api.MigrationVerificationRetention)) {
			delete(m.migrationVerifications, id)
			continue
		}
		if old.AppID == r.AppID && old.Report.Status == "pending" {
			active++
		}
	}
	if active >= api.MigrationVerificationMaxActive {
		return r, ErrConflict
	}
	if m.migrationVerifications == nil {
		m.migrationVerifications = map[string]MigrationVerificationRecord{}
	}
	r.Version = 1
	m.migrationVerifications[r.Report.ID] = cloneMigrationVerification(r)
	return r, nil
}

func (m *MemStore) ListDueMigrationVerifications(_ context.Context, now time.Time, limit int32) ([]MigrationVerificationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > api.MigrationVerificationWorkerBatch {
		limit = api.MigrationVerificationWorkerBatch
	}
	out := []MigrationVerificationRecord{}
	for _, r := range m.migrationVerifications {
		due := migrationVerificationDue(r)
		if !due.IsZero() && !due.After(now) {
			out = append(out, cloneMigrationVerification(r))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := migrationVerificationDue(out[i]), migrationVerificationDue(out[j])
		if a.Equal(b) {
			return out[i].Report.ID < out[j].Report.ID
		}
		return a.Before(b)
	})
	if len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) GetMigrationVerification(_ context.Context, account, app, id string) (MigrationVerificationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.migrationVerifications[id]
	if !ok || r.AccountID != account || r.AppID != app {
		return r, ErrNotFound
	}
	return cloneMigrationVerification(r), nil
}

func (m *MemStore) ListMigrationVerifications(_ context.Context, account, app string) ([]MigrationVerificationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []MigrationVerificationRecord{}
	for _, r := range m.migrationVerifications {
		if r.AccountID == account && r.AppID == app {
			out = append(out, cloneMigrationVerification(r))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Report.CreatedAt.After(out[j].Report.CreatedAt) })
	if len(out) > api.MigrationVerificationListLimit {
		out = out[:api.MigrationVerificationListLimit]
	}
	return out, nil
}

func (m *MemStore) SaveMigrationVerification(_ context.Context, r MigrationVerificationRecord, version int64, task *CreateAppTaskParams) (MigrationVerificationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.migrationVerifications[r.Report.ID]
	if !ok || old.AccountID != r.AccountID || old.AppID != r.AppID {
		return r, ErrNotFound
	}
	if old.Version != version {
		return cloneMigrationVerification(old), ErrConflict
	}
	if task != nil && *migrationTaskSlot(&r) == "" {
		resolved, err := resolveCreateAppTask(*task)
		if err != nil {
			return r, err
		}
		row, err := m.createAppTaskLocked(resolved)
		if err != nil {
			return r, err
		}
		deadline := r.Report.ExpiresAt
		row.StartDeadlineAt = &deadline
		m.appTasks[row.ID] = row
		*migrationTaskSlot(&r) = row.ID
	}
	r.Version = version + 1
	m.migrationVerifications[r.Report.ID] = cloneMigrationVerification(r)
	return r, nil
}
