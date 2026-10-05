package state

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ AppHealthHistoryStore = (*MemStore)(nil)

func (m *MemStore) PruneExpiredAppHealthHistory(_ context.Context, now time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var removed int64
	for id, row := range m.appHealthHistory {
		start := 0
		for start < len(row.Entries) && removed < api.AppHealthHistoryPruneBatch {
			at, _ := time.Parse(time.RFC3339Nano, row.Entries[start].ObservedAt)
			if !at.Before(now.Add(-api.AppHealthHistoryMaxAge)) {
				break
			}
			start++
			removed++
		}
		row.Entries = slices.Clone(row.Entries[start:])
		m.appHealthHistory[id] = row
		if removed == api.AppHealthHistoryPruneBatch {
			break
		}
	}
	return removed, nil
}

func (m *MemStore) ClaimAppHealth(_ context.Context, token string, now time.Time) (AppHealthClaim, error) {
	if token == "" || now.IsZero() {
		return AppHealthClaim{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.appHealthHistory == nil {
		m.appHealthHistory = make(map[string]appHealthRecord)
	}
	var selected App
	var earliest time.Time
	for _, app := range m.apps {
		if !appHealthEligible(app) {
			continue
		}
		row := m.appHealthHistory[app.ID]
		if row.NextCheckAt.After(now) || row.Claim.LeaseUntil.After(now) {
			continue
		}
		if selected.ID == "" || row.NextCheckAt.Before(earliest) || row.NextCheckAt.Equal(earliest) && app.ID < selected.ID {
			selected, earliest = app, row.NextCheckAt
		}
	}
	if selected.ID == "" {
		return AppHealthClaim{}, ErrNotFound
	}
	claim := AppHealthClaim{AppID: selected.ID, AccountID: selected.AccountID, Token: token, StartedAt: now, LeaseUntil: now.Add(api.AppHealthCollectorLease)}
	row := m.appHealthHistory[selected.ID]
	row.Claim = claim
	m.appHealthHistory[selected.ID] = row
	return claim, nil
}

func (m *MemStore) FinishAppHealth(_ context.Context, claim AppHealthClaim, a api.AppHealthResponse, now time.Time) error {
	key, _, err := prepareAppHealth(claim, a, now)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[claim.AppID]
	if !ok || app.AccountID != claim.AccountID || !appHealthEligible(app) {
		return ErrNotFound
	}
	row := m.appHealthHistory[claim.AppID]
	if claim.Token == "" || row.Claim.Token != claim.Token || !row.Claim.LeaseUntil.After(now) || !row.Claim.StartedAt.Equal(claim.StartedAt) {
		return ErrConflict
	}
	if row.Latest != nil {
		oldAt, _ := time.Parse(time.RFC3339Nano, row.Latest.EvaluatedAt)
		at, _ := time.Parse(time.RFC3339Nano, a.EvaluatedAt)
		if !at.After(oldAt) {
			return ErrConflict
		}
	}
	row.Entries = append(row.Entries, appHealthEntries(row.Latest, row.Key, key, a)...)
	row.Latest, row.Key, row.NextCheckAt = cloneAppHealth(&a), key, now.Add(api.AppHealthCollectorInterval)
	row.Claim = AppHealthClaim{}
	bytes := 0
	start := len(row.Entries)
	for start > 0 {
		body, _ := json.Marshal(row.Entries[start-1])
		at, _ := time.Parse(time.RFC3339Nano, row.Entries[start-1].ObservedAt)
		if len(row.Entries)-start >= api.AppHealthHistoryMaxEntries || bytes+len(body) > api.AppHealthHistoryMaxBytes || at.Before(now.Add(-api.AppHealthHistoryMaxAge)) {
			break
		}
		bytes += len(body)
		start--
	}
	row.Entries = cloneAppHealth(row.Entries[start:])
	m.appHealthHistory[claim.AppID] = row
	return nil
}

func (m *MemStore) ListAppHealthHistory(_ context.Context, accountID, appID string, limit int, before string, now time.Time) (api.AppHealthHistoryPage, error) {
	if err := validateAppHealthPage(limit, before); err != nil {
		return api.AppHealthHistoryPage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return api.AppHealthHistoryPage{}, ErrNotFound
	}
	row := m.appHealthHistory[appID]
	page := appHealthPage(appID, cloneAppHealth(row.Latest), now)
	entries := slices.Clone(row.Entries)
	slices.Reverse(entries)
	if before != "" {
		i := slices.IndexFunc(entries, func(e api.AppHealthHistoryEntry) bool { return e.ID == before })
		if i < 0 {
			return page, ErrNotFound
		}
		at, _ := time.Parse(time.RFC3339Nano, entries[i].ObservedAt)
		if at.Before(now.Add(-api.AppHealthHistoryMaxAge)) {
			return page, ErrNotFound
		}
		entries = entries[i+1:]
	}
	for _, e := range entries {
		at, _ := time.Parse(time.RFC3339Nano, e.ObservedAt)
		if at.Before(now.Add(-api.AppHealthHistoryMaxAge)) {
			continue
		}
		if len(page.Entries) == limit {
			page.NextCursor = page.Entries[limit-1].ID
			break
		}
		page.Entries = append(page.Entries, cloneAppHealth(e))
	}
	return page, nil
}
