package state

import (
	"context"
	"sort"
	"time"
)

// DeletedAppRecord is a narrow customer inventory, not the sweeper's full App.
type DeletedAppRecord struct {
	ID, Slug                    string
	DeletedAt, DeleteGraceUntil *time.Time
	SortAt                      time.Time
	PurgeStarted                bool
}

type AccountDeletedAppsStore interface {
	ListAccountDeletedApps(context.Context, string, time.Time, string, int) ([]DeletedAppRecord, error)
	AccountDeletedAppByID(context.Context, string, string) (DeletedAppRecord, error)
}

var _ AccountDeletedAppsStore = (*MemStore)(nil)

func validDeletedAppPage(accountID string, beforeAt time.Time, beforeID string, limit int) bool {
	return accountID != "" && limit >= 1 && limit <= 51 && (beforeID == "") == beforeAt.IsZero()
}

func (m *MemStore) deletedAppRecordLocked(a App) DeletedAppRecord {
	at := a.CreatedAt
	if a.DeletedAt != nil {
		at = *a.DeletedAt
	}
	_, claimed := m.appDeletionClaims[a.ID]
	return DeletedAppRecord{ID: a.ID, Slug: a.Slug, DeletedAt: a.DeletedAt, DeleteGraceUntil: a.DeleteGraceUntil, SortAt: at, PurgeStarted: claimed}
}

func (m *MemStore) ListAccountDeletedApps(_ context.Context, accountID string, beforeAt time.Time, beforeID string, limit int) ([]DeletedAppRecord, error) {
	if !validDeletedAppPage(accountID, beforeAt, beforeID, limit) {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]DeletedAppRecord, 0)
	for _, app := range m.apps {
		if app.AccountID != accountID || app.Status != AppDeleted {
			continue
		}
		r := m.deletedAppRecordLocked(app)
		if beforeID != "" && (r.SortAt.After(beforeAt) || (r.SortAt.Equal(beforeAt) && r.ID >= beforeID)) {
			continue
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].SortAt.Equal(rows[j].SortAt) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].SortAt.After(rows[j].SortAt)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (m *MemStore) AccountDeletedAppByID(_ context.Context, accountID, id string) (DeletedAppRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[id]
	if !ok || a.AccountID != accountID || a.Status != AppDeleted {
		return DeletedAppRecord{}, ErrNotFound
	}
	return m.deletedAppRecordLocked(a), nil
}
