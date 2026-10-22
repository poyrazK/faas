package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

var _ LogSavedSearchStore = (*MemStore)(nil)

// PutLogSavedSearch mirrors the PostgreSQL upsert and per-app cap.
func (m *MemStore) PutLogSavedSearch(_ context.Context, search LogSavedSearch, limit int) (LogSavedSearch, error) {
	if err := validateLogSavedSearch(search); err != nil {
		return LogSavedSearch{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.logSavedSearches == nil {
		m.logSavedSearches = map[string]LogSavedSearch{}
	}
	now := time.Now().UTC()
	count := 0
	for id, existing := range m.logSavedSearches {
		if existing.AppID != search.AppID {
			continue
		}
		if existing.Name == search.Name {
			existing.Query, existing.UpdatedAt = search.Query, now
			m.logSavedSearches[id] = existing
			return existing, nil
		}
		count++
	}
	if count >= limit {
		return LogSavedSearch{}, &LogSavedSearchQuotaError{Limit: limit, Observed: count}
	}
	search.ID, search.CreatedAt, search.UpdatedAt = uuid.NewString(), now, now
	m.logSavedSearches[search.ID] = search
	return search, nil
}

// ListLogSavedSearches mirrors the PostgreSQL read, ordered by name.
func (m *MemStore) ListLogSavedSearches(_ context.Context, accountID, appID string) ([]LogSavedSearch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []LogSavedSearch
	for _, search := range m.logSavedSearches {
		if search.AccountID == accountID && search.AppID == appID {
			out = append(out, search)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// LogSavedSearchByName mirrors the PostgreSQL read.
func (m *MemStore) LogSavedSearchByName(_ context.Context, accountID, appID, name string) (LogSavedSearch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, search := range m.logSavedSearches {
		if search.AccountID == accountID && search.AppID == appID && search.Name == name {
			return search, nil
		}
	}
	return LogSavedSearch{}, ErrNotFound
}

// DeleteLogSavedSearch mirrors the PostgreSQL delete.
func (m *MemStore) DeleteLogSavedSearch(_ context.Context, accountID, appID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, search := range m.logSavedSearches {
		if search.AccountID == accountID && search.AppID == appID && search.Name == name {
			delete(m.logSavedSearches, id)
			return nil
		}
	}
	return ErrNotFound
}
