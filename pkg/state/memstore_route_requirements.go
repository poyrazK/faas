package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) savedRouteRequirementsLocked(accountID, appID string) (api.SavedRouteRequirements, error) {
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return api.SavedRouteRequirements{}, ErrNotFound
	}
	saved, ok := m.savedRouteRequirements[appID]
	if !ok {
		return saved, ErrNotFound
	}
	if err := validateSavedRouteRequirements(saved, appID); err != nil {
		return saved, err
	}
	return copySavedRouteRequirements(saved)
}

func (m *MemStore) GetSavedRouteRequirements(_ context.Context, accountID, appID string) (api.SavedRouteRequirements, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.savedRouteRequirementsLocked(accountID, appID)
}

func (m *MemStore) SaveRouteRequirements(_ context.Context, accountID, appID string, request api.SaveRouteRequirementsRequest) (api.SavedRouteRequirements, error) {
	config, digest, err := NormalizeSavedRouteRequirements(request)
	if err != nil {
		return api.SavedRouteRequirements{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return api.SavedRouteRequirements{}, ErrNotFound
	}
	current := m.savedRouteRequirements[appID]
	if current.Revision != *request.ExpectedRevision {
		return api.SavedRouteRequirements{}, ErrRouteRequirementsRevision
	}
	if current.SHA256 == digest {
		return copySavedRouteRequirements(current)
	}
	if current.Revision >= api.RouteRequirementsMaxRevision {
		return api.SavedRouteRequirements{}, ErrRouteRequirementsRevision
	}
	saved := api.SavedRouteRequirements{AppID: appID, Revision: current.Revision + 1, SHA256: digest, Requirements: config, UpdatedAt: time.Now().UTC()}
	if m.savedRouteRequirements == nil {
		m.savedRouteRequirements = map[string]api.SavedRouteRequirements{}
	}
	m.savedRouteRequirements[appID] = saved
	m.enqueueAutomaticRouteCheckLocked(appID, "", true)
	return copySavedRouteRequirements(saved)
}

func (m *MemStore) CheckRouteRequirements(_ context.Context, accountID, appID string, request api.CheckRouteRequirementsRequest, checker RouteRequirementsChecker) (api.RouteRequirementsCheck, error) {
	if err := ValidateCheckRouteRequirements(request); err != nil {
		return api.RouteRequirementsCheck{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot, err := m.routePolicySnapshotLocked(accountID, appID)
	if err != nil {
		return api.RouteRequirementsCheck{}, err
	}
	saved, err := m.savedRouteRequirementsLocked(accountID, appID)
	if err != nil {
		return api.RouteRequirementsCheck{}, err
	}
	if err := checkSavedRevision(saved, request.ExpectedRevision); err != nil {
		return api.RouteRequirementsCheck{}, err
	}
	if err := m.routePolicyContractLocked(&snapshot, request.DeploymentID); err != nil {
		return api.RouteRequirementsCheck{}, err
	}
	return checker(snapshot, saved)
}
