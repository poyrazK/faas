package state

import (
	"context"
	"time"
)

var _ EnvironmentWorkloadIntentStore = (*MemStore)(nil)

func (m *MemStore) deleteEnvironmentWorkloadIntentsLocked(appID, environmentID string) {
	for key := range m.appEnvironmentWorkloadIntents {
		if (appID == "" || key.AppID == appID) && (environmentID == "" || key.EnvironmentID == environmentID) {
			delete(m.appEnvironmentWorkloadIntents, key)
		}
	}
}

func (m *MemStore) workloadIntentEnvironmentLocked(accountID, appID, environmentID string) (App, ProjectEnvironment, error) {
	app, exists := m.apps[appID]
	if !exists || app.AccountID != accountID || app.Status == AppDeleted {
		return App{}, ProjectEnvironment{}, ErrNotFound
	}
	for _, env := range m.projectEnvironments {
		if env.ID == environmentID && env.AccountID == accountID && env.ProjectID == app.ProjectID {
			return app, env, nil
		}
	}
	return app, ProjectEnvironment{}, ErrNotFound
}

func (m *MemStore) EnvironmentWorkloadIntent(_ context.Context, accountID, appID, environmentID string) (EnvironmentWorkloadIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, _, err := m.workloadIntentEnvironmentLocked(accountID, appID, environmentID); err != nil {
		return EnvironmentWorkloadIntent{}, err
	}
	row, exists := m.appEnvironmentWorkloadIntents[environmentWorkloadIntentKey{appID, environmentID}]
	if !exists {
		return row, ErrNotFound
	}
	return cloneWorkloadIntent(row), nil
}

func (m *MemStore) putWorkloadIntentLocked(row EnvironmentWorkloadIntent) EnvironmentWorkloadIntent {
	if m.appEnvironmentWorkloadIntents == nil {
		m.appEnvironmentWorkloadIntents = map[environmentWorkloadIntentKey]EnvironmentWorkloadIntent{}
	}
	key := environmentWorkloadIntentKey{row.AppID, row.EnvironmentID}
	previous := m.appEnvironmentWorkloadIntents[key]
	row.CreatedAt = previous.CreatedAt
	row.UpdatedAt = time.Now().UTC()
	if row.CreatedAt.IsZero() {
		row.CreatedAt = row.UpdatedAt
	}
	row = cloneWorkloadIntent(row)
	m.appEnvironmentWorkloadIntents[key] = row
	return cloneWorkloadIntent(row)
}

func (m *MemStore) PutEnvironmentWorkloadIntent(_ context.Context, row EnvironmentWorkloadIntent) (EnvironmentWorkloadIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, env, err := m.workloadIntentEnvironmentLocked(row.AccountID, row.AppID, row.EnvironmentID)
	if err != nil {
		return row, err
	}
	previous := m.appEnvironmentWorkloadIntents[environmentWorkloadIntentKey{row.AppID, row.EnvironmentID}]
	row, err = validateWorkloadIntentWrite(row, previous, app, env.Slug, m.accounts[row.AccountID].Plan)
	if err != nil {
		return row, err
	}
	paths := workloadIntentChangedPaths(previous, row)
	managed, err := m.gitOpsGuardScopedWriteLocked(row.AccountID, row.AppID, env.Slug, paths)
	if err != nil {
		return row, err
	}
	row = m.putWorkloadIntentLocked(row)
	if len(paths) > 0 {
		touchGitOpsMemoryIntent(managed)
	}
	return row, nil
}
