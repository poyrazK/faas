package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) SaveAutomationLiveSuite(ctx context.Context, appID, name, id string, request api.SaveAutomationLiveSuiteRequest) (api.AutomationLiveSuite, error) {
	request = cloneLiveSuiteRequest(request)
	id, err := validateLiveSuite(id, &request)
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.AutomationLiveSuite{}, err
	}
	current, exists := m.automations[appID+"/"+name]
	if !exists {
		return api.AutomationLiveSuite{}, ErrNotFound
	}
	prior, exists := m.automationLiveSuites[id]
	if exists && (prior.AppID != appID || prior.Name != name) {
		return api.AutomationLiveSuite{}, ErrNotFound
	}
	if exists && prior.Managed {
		return api.AutomationLiveSuite{}, ErrWorkflowRunIdempotencyConflict
	}
	if exists {
		request, err = mergeLiveSuitePlan(prior.Plan, request)
	} else {
		err = validateLiveSuiteTarget(request, current, m.automationDeploymentLocked(appID).ID)
		if err == nil {
			err = m.liveSuiteRoomLocked(appID, name)
		}
	}
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	for i, member := range request.Cases {
		if member.RunID == "" || exists && prior.Plan.Cases[i].RunID == member.RunID {
			continue
		}
		live, found := m.automationLiveVerifications[member.RunID]
		if !found || live.AppID != appID || live.Name != name {
			return api.AutomationLiveSuite{}, ErrNotFound
		}
		if err := validateLiveSuiteLink(request, member, live.View); err != nil {
			return api.AutomationLiveSuite{}, err
		}
	}
	now := time.Now().UTC()
	record := automationLiveSuiteRecord{ID: id, AppID: appID, Name: name, Plan: request, CreatedAt: now, UpdatedAt: now}
	if exists {
		record.CreatedAt = prior.CreatedAt
		if liveSuitePlanEqual(prior.Plan, request) {
			record.UpdatedAt = prior.UpdatedAt
		}
	}
	if m.automationLiveSuites == nil {
		m.automationLiveSuites = map[string]automationLiveSuiteRecord{}
	}
	m.automationLiveSuites[id] = record
	m.pruneLiveSuitesLocked(appID, name)
	return m.liveSuiteViewLocked(record)
}

func (m *MemStore) liveSuiteViewLocked(record automationLiveSuiteRecord) (api.AutomationLiveSuite, error) {
	current, exists := m.automations[record.AppID+"/"+record.Name]
	if !exists {
		return api.AutomationLiveSuite{}, ErrNotFound
	}
	return liveSuiteView(record, current, m.automationDeploymentLocked(record.AppID).ID, func(id string) (api.AutomationLiveVerification, error) {
		live, found := m.automationLiveVerifications[id]
		if !found || live.AppID != record.AppID || live.Name != record.Name {
			return api.AutomationLiveVerification{}, ErrNotFound
		}
		return m.liveViewLocked(live)
	})
}

func (m *MemStore) GetAutomationLiveSuite(ctx context.Context, appID, name, id string) (api.AutomationLiveSuite, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return api.AutomationLiveSuite{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.AutomationLiveSuite{}, err
	}
	record, exists := m.automationLiveSuites[parsed.String()]
	if !exists || record.AppID != appID || record.Name != name {
		return api.AutomationLiveSuite{}, ErrNotFound
	}
	return m.liveSuiteViewLocked(record)
}

func (m *MemStore) liveSuitesLocked(appID, name string) []automationLiveSuiteRecord {
	var records []automationLiveSuiteRecord
	for _, record := range m.automationLiveSuites {
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
	cutoff := time.Now().Add(-api.AutomationLiveSuiteStartWindow)
	// Preserve creation receipts throughout the only window in which a missing
	// UUIDv7 could still be admitted. The remaining slots retain recent history.
	sort.SliceStable(records, func(i, j int) bool {
		protectedI := m.liveSuitePinnedLocked(records[i].ID) || records[i].Managed && records[i].CreatedAt.After(cutoff)
		protectedJ := m.liveSuitePinnedLocked(records[j].ID) || records[j].Managed && records[j].CreatedAt.After(cutoff)
		return protectedI && !protectedJ
	})
	if len(records) > api.AutomationLiveSuiteRetainedLimit {
		for _, record := range records[api.AutomationLiveSuiteRetainedLimit:] {
			delete(m.automationLiveSuites, record.ID)
		}
	}
}

func (m *MemStore) liveSuiteRoomLocked(appID, name string) error {
	cutoff := time.Now().Add(-api.AutomationLiveSuiteStartWindow)
	count := 0
	for _, record := range m.automationLiveSuites {
		if record.AppID == appID && record.Name == name && (m.liveSuitePinnedLocked(record.ID) || record.Managed && record.CreatedAt.After(cutoff)) {
			count++
		}
	}
	if count >= api.AutomationLiveSuiteRetainedLimit {
		return api.ErrCapacity("Recent suites still own their recovery records. Retry after the suite creation window expires.")
	}
	return nil
}

func (m *MemStore) ListAutomationLiveSuites(ctx context.Context, appID, name string) (api.ListAutomationLiveSuitesResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := api.ListAutomationLiveSuitesResponse{Suites: []api.AutomationLiveSuiteSummary{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if _, exists := m.automations[appID+"/"+name]; !exists {
		return result, ErrNotFound
	}
	for _, record := range m.liveSuitesLocked(appID, name) {
		result.Suites = append(result.Suites, liveSuiteSummary(record))
		if len(result.Suites) == api.AutomationLiveSuiteHistoryLimit {
			break
		}
	}
	return result, nil
}
