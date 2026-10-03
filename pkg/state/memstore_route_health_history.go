package state

import (
	"context"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ RouteHealthHistoryStore = (*MemStore)(nil)

func (m *MemStore) appendRouteHealthHistoryLocked(deploymentID string, record routeHealthStoredDecision) {
	if len(record.Body) == 0 {
		return
	}
	if m.routeHealthHistory == nil {
		m.routeHealthHistory = map[string][]routeHealthStoredDecision{}
	}
	entries := m.routeHealthHistory[deploymentID]
	for _, previous := range entries {
		if previous.Key == record.Key {
			return
		}
	}
	entries = append(entries, record)
	slices.SortFunc(entries, func(a, b routeHealthStoredDecision) int {
		if compared := a.CheckedAt.Compare(b.CheckedAt); compared != 0 {
			return compared
		}
		return strings.Compare(a.ID, b.ID)
	})
	bytes, start := 0, len(entries)
	for i := len(entries) - 1; i >= 0; i-- {
		if len(entries)-i > api.RouteHealthHistoryMaxEntries || bytes+len(entries[i].Body) > api.RouteHealthHistoryMaxBytes {
			break
		}
		bytes += len(entries[i].Body)
		start = i
	}
	m.routeHealthHistory[deploymentID] = append([]routeHealthStoredDecision(nil), entries[start:]...)
}

func (m *MemStore) routeHealthHistoryLocked(accountID, appID, deploymentID string) ([]routeHealthStoredDecision, error) {
	if _, err := m.routeHealthGateLocked(accountID, appID); err != nil {
		return nil, err
	}
	if deployment, ok := m.deployments[deploymentID]; !ok || deployment.AppID != appID {
		return nil, ErrNotFound
	}
	return m.routeHealthHistory[deploymentID], nil
}

func (m *MemStore) GetRouteHealthHistoryEntry(_ context.Context, accountID, appID, deploymentID, id string) (api.RouteHealthHistoryEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, err := m.routeHealthHistoryLocked(accountID, appID, deploymentID)
	if err != nil {
		return api.RouteHealthHistoryEntry{}, err
	}
	for _, record := range entries {
		entry, err := decodeRouteHealthHistory(record.Body, appID, deploymentID)
		if err != nil || entry.ID == id {
			return entry, err
		}
	}
	return api.RouteHealthHistoryEntry{}, ErrNotFound
}

func (m *MemStore) ListRouteHealthHistory(_ context.Context, accountID, appID, deploymentID string, limit int, before string) (api.RouteHealthHistoryPage, error) {
	page := api.RouteHealthHistoryPage{AppID: appID, DeploymentID: deploymentID, Entries: []api.RouteHealthHistoryEntry{}}
	if err := validateRouteHealthHistoryPage(limit, before); err != nil {
		return page, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, err := m.routeHealthHistoryLocked(accountID, appID, deploymentID)
	if err != nil {
		return page, err
	}
	eligible := before == ""
	for i := len(entries) - 1; i >= 0; i-- {
		entry, err := decodeRouteHealthHistory(entries[i].Body, appID, deploymentID)
		if err != nil {
			return page, err
		}
		if !eligible {
			eligible = entry.ID == before
			continue
		}
		if len(page.Entries) == limit {
			page.NextCursor = page.Entries[len(page.Entries)-1].ID
			break
		}
		page.Entries = append(page.Entries, entry)
	}
	if !eligible {
		return page, ErrNotFound
	}
	return page, nil
}
