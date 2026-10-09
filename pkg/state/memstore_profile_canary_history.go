package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) ListProfileCanaryChecks(_ context.Context, accountID, appID, deploymentID string, limit int, before string) (api.ProfileCanaryHistoryPage, error) {
	page := api.ProfileCanaryHistoryPage{AppID: appID, DeploymentID: deploymentID, Entries: []api.CanaryProfileSignal{}}
	cursor, err := validateProfileCanaryHistoryPage(limit, before, deploymentID)
	if err != nil {
		return page, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dep, ok := m.deployments[deploymentID]
	if !ok || dep.AppID != appID || dep.DeletedAt != nil || dep.CanaryTotalSteps <= 0 || !m.profileInvestigationOwnedLocked(accountID, appID) {
		return page, ErrNotFound
	}
	entries := make([]api.CanaryProfileSignal, 0)
	for _, row := range m.profileCanaryChecks {
		if row.appID != appID || row.accountID != accountID || row.check.Candidate == nil || row.check.Candidate.DeploymentID != deploymentID {
			continue
		}
		if err := validateProfileCanaryHistorySignal(row.check, deploymentID); err != nil {
			return page, err
		}
		entries = append(entries, copyProfileCanarySignal(row.check))
	}
	sort.Slice(entries, func(i, j int) bool { return profileCanaryHistorySignalBefore(entries[i], entries[j]) })
	start := 0
	if cursor != nil {
		found := false
		for i := range entries {
			if profileCanaryCursorMatches(*cursor, deploymentID, entries[i]) {
				start, found = i+1, true
				break
			}
		}
		if !found {
			return page, ErrNotFound
		}
	}
	for i := start; i < len(entries) && len(page.Entries) < limit+1; i++ {
		page.Entries = append(page.Entries, entries[i])
	}
	if len(page.Entries) > limit {
		page.NextCursor = encodeProfileCanaryHistoryCursor(deploymentID, page.Entries[limit-1])
		page.Entries = page.Entries[:limit]
	}
	return page, nil
}
