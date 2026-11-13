package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) CompareStoredWorkflowLiveSuites(ctx context.Context, appID, name, candidateID, baselineID string) (api.StoredWorkflowLiveSuiteComparison, error) {
	candidateID, baselineID, err := liveSuiteComparisonIDs(candidateID, baselineID)
	if err != nil {
		return api.StoredWorkflowLiveSuiteComparison{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.StoredWorkflowLiveSuiteComparison{}, err
	}
	views := make([]api.StoredWorkflowLiveSuite, 0, 2)
	for _, id := range []string{baselineID, candidateID} {
		record, exists := m.storedWorkflowLiveSuites[id]
		if !exists || record.AppID != appID || record.Name != name {
			return api.StoredWorkflowLiveSuiteComparison{}, ErrNotFound
		}
		view, err := m.liveSuiteViewLocked(record)
		if err != nil {
			return api.StoredWorkflowLiveSuiteComparison{}, err
		}
		views = append(views, view)
	}
	return compareLiveSuites(views[0], views[1]), nil
}
