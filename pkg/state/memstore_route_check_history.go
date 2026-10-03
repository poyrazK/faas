package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

func pruneMemoryRouteHistory(entries [][]byte) [][]byte {
	bytes, start := 0, len(entries)
	for i := len(entries) - 1; i >= 0; i-- {
		if len(entries)-i > api.RouteCheckHistoryMaxEntries || bytes+len(entries[i]) > api.RouteCheckHistoryMaxBytes {
			break
		}
		bytes += len(entries[i])
		start = i
	}
	return append([][]byte(nil), entries[start:]...)
}

func (m *MemStore) routeHistoryLocked(accountID, appID, deploymentID string) ([][]byte, error) {
	snapshot, err := m.routePolicySnapshotLocked(accountID, appID)
	if err != nil {
		return nil, err
	}
	deployment, ok := m.deployments[deploymentID]
	if !ok || deployment.AppID != appID {
		return nil, ErrNotFound
	}
	if snapshot.Account.Plan.OpenAPIDocsPerDeployment() <= 0 {
		return nil, ErrAutomaticRouteCheckPlan
	}
	return m.automaticRouteChecks[deploymentID].History, nil
}

func (m *MemStore) GetRouteCheckHistoryEntry(_ context.Context, accountID, appID, deploymentID, id string) (api.RouteCheckHistoryEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, err := m.routeHistoryLocked(accountID, appID, deploymentID)
	if err != nil {
		return api.RouteCheckHistoryEntry{}, err
	}
	for _, body := range entries {
		entry, err := decodeRouteHistoryEntry(body, appID, deploymentID)
		if err != nil || entry.ID == id {
			return entry, err
		}
	}
	return api.RouteCheckHistoryEntry{}, ErrNotFound
}

func (m *MemStore) ListRouteCheckHistory(_ context.Context, accountID, appID, deploymentID string, limit int, before string) (api.RouteCheckHistoryPage, error) {
	page := api.RouteCheckHistoryPage{AppID: appID, DeploymentID: deploymentID, Entries: []api.RouteCheckHistorySummary{}}
	if err := validateRouteHistoryPage(limit, before); err != nil {
		return page, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, err := m.routeHistoryLocked(accountID, appID, deploymentID)
	if err != nil {
		return page, err
	}
	eligible := before == ""
	for i := len(entries) - 1; i >= 0; i-- {
		entry, err := decodeRouteHistoryEntry(entries[i], appID, deploymentID)
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
		page.Entries = append(page.Entries, routeHistorySummary(entry))
	}
	if !eligible {
		return page, ErrNotFound
	}
	return page, nil
}
