package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) SaveStoredWorkflowLiveSuite(ctx context.Context, appID, name, id string, request api.SaveStoredWorkflowLiveSuiteRequest) (api.StoredWorkflowLiveSuite, error) {
	request = cloneLiveSuiteRequest(request)
	id, err := validateLiveSuite(id, &request)
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	current, exists := m.storedWorkflows[appID+"/"+name]
	if !exists {
		return api.StoredWorkflowLiveSuite{}, ErrNotFound
	}
	prior, exists := m.storedWorkflowLiveSuites[id]
	if exists && (prior.AppID != appID || prior.Name != name) {
		return api.StoredWorkflowLiveSuite{}, ErrNotFound
	}
	if exists && prior.Managed {
		return api.StoredWorkflowLiveSuite{}, ErrWorkflowRunIdempotencyConflict
	}
	if exists {
		request, err = mergeLiveSuitePlan(prior.Plan, request)
	} else {
		err = validateLiveSuiteTarget(request, current, m.storedWorkflowDeploymentLocked(appID).ID)
		if err == nil {
			err = m.liveSuiteRoomLocked(appID, name)
		}
	}
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	for i, member := range request.Cases {
		if member.RunID == "" || exists && prior.Plan.Cases[i].RunID == member.RunID {
			continue
		}
		live, found := m.storedWorkflowLiveVerifications[member.RunID]
		if !found || live.AppID != appID || live.Name != name {
			return api.StoredWorkflowLiveSuite{}, ErrNotFound
		}
		if err := validateLiveSuiteLink(request, member, live.View); err != nil {
			return api.StoredWorkflowLiveSuite{}, err
		}
	}
	now := time.Now().UTC()
	record := storedWorkflowLiveSuiteRecord{ID: id, AppID: appID, Name: name, Plan: request, CreatedAt: now, UpdatedAt: now}
	if exists {
		record.CreatedAt = prior.CreatedAt
		if liveSuitePlanEqual(prior.Plan, request) {
			record.UpdatedAt = prior.UpdatedAt
		}
	}
	if m.storedWorkflowLiveSuites == nil {
		m.storedWorkflowLiveSuites = map[string]storedWorkflowLiveSuiteRecord{}
	}
	m.storedWorkflowLiveSuites[id] = record
	m.pruneLiveSuitesLocked(appID, name)
	return m.liveSuiteViewLocked(record)
}

func (m *MemStore) liveSuiteViewLocked(record storedWorkflowLiveSuiteRecord) (api.StoredWorkflowLiveSuite, error) {
	current, exists := m.storedWorkflows[record.AppID+"/"+record.Name]
	if !exists {
		return api.StoredWorkflowLiveSuite{}, ErrNotFound
	}
	return liveSuiteView(record, current, m.storedWorkflowDeploymentLocked(record.AppID).ID, func(id string) (api.StoredWorkflowLiveVerification, error) {
		live, found := m.storedWorkflowLiveVerifications[id]
		if !found || live.AppID != record.AppID || live.Name != record.Name {
			return api.StoredWorkflowLiveVerification{}, ErrNotFound
		}
		return m.liveViewLocked(live)
	})
}

func (m *MemStore) GetStoredWorkflowLiveSuite(ctx context.Context, appID, name, id string) (api.StoredWorkflowLiveSuite, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	record, exists := m.storedWorkflowLiveSuites[parsed.String()]
	if !exists || record.AppID != appID || record.Name != name {
		return api.StoredWorkflowLiveSuite{}, ErrNotFound
	}
	return m.liveSuiteViewLocked(record)
}

func (m *MemStore) liveSuitesLocked(appID, name string) []storedWorkflowLiveSuiteRecord {
	var records []storedWorkflowLiveSuiteRecord
	for _, record := range m.storedWorkflowLiveSuites {
		if record.AppID == appID && record.Name == name {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	return records
}

func (m *MemStore) pruneLiveSuitesLocked(appID, name string) {
	records := m.liveSuitesLocked(appID, name)
	cutoff := time.Now().Add(-api.StoredWorkflowLiveSuiteProtectedWindow)
	// Preserve creation receipts throughout the only window in which a missing
	// UUIDv7 could still be admitted. The remaining slots retain recent history.
	sort.SliceStable(records, func(i, j int) bool {
		protectedI := m.liveSuitePinnedLocked(records[i].ID) || records[i].Managed && records[i].CreatedAt.After(cutoff)
		protectedJ := m.liveSuitePinnedLocked(records[j].ID) || records[j].Managed && records[j].CreatedAt.After(cutoff)
		return protectedI && !protectedJ
	})
	if len(records) > api.StoredWorkflowLiveSuiteRetainedLimit {
		for _, record := range records[api.StoredWorkflowLiveSuiteRetainedLimit:] {
			delete(m.storedWorkflowLiveSuites, record.ID)
		}
	}
}

func (m *MemStore) liveSuiteRoomLocked(appID, name string) error {
	cutoff := time.Now().Add(-api.StoredWorkflowLiveSuiteProtectedWindow)
	count := 0
	for _, record := range m.storedWorkflowLiveSuites {
		if record.AppID == appID && record.Name == name && (m.liveSuitePinnedLocked(record.ID) || record.Managed && record.CreatedAt.After(cutoff)) {
			count++
		}
	}
	if count >= api.StoredWorkflowLiveSuiteRetainedLimit {
		return api.ErrCapacity("Recent suites still own their recovery records. Retry after the suite creation window expires.")
	}
	return nil
}

func (m *MemStore) ListStoredWorkflowLiveSuites(ctx context.Context, appID, name string) (api.ListStoredWorkflowLiveSuitesResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := api.ListStoredWorkflowLiveSuitesResponse{Suites: []api.StoredWorkflowLiveSuiteSummary{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if _, exists := m.storedWorkflows[appID+"/"+name]; !exists {
		return result, ErrNotFound
	}
	for _, record := range m.liveSuitesLocked(appID, name) {
		result.Suites = append(result.Suites, liveSuiteSummary(record))
		if len(result.Suites) == api.StoredWorkflowLiveSuiteHistoryLimit {
			break
		}
	}
	return result, nil
}
