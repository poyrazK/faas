package state

import (
	"context"
	"slices"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type ProjectEnvironmentAppReader interface {
	ProjectEnvironmentAppIDs(context.Context, string, string, string) ([]string, error)
}

// ProjectAppsForEnvironment keeps legacy production membership, excluding
// identities configured only in stages. Named stages own their desired heads
// and deployments; an unrelated environment cannot expand their release set.
func ProjectAppsForEnvironment(ctx context.Context, store any, accountID, projectID, environment string, apps []App) ([]App, error) {
	reader, ok := store.(ProjectEnvironmentAppReader)
	if !ok {
		return apps, nil
	}
	ids, err := reader.ProjectEnvironmentAppIDs(ctx, accountID, projectID, environment)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(slices.Clone(apps), func(app App) bool { return !slices.Contains(ids, app.ID) }), nil
}

func (s *PgStore) ProjectEnvironmentAppIDs(ctx context.Context, accountID, projectID, environment string) ([]string, error) {
	return projectEnvironmentAppIDsDB(ctx, s.pool, accountID, projectID, environment)
}

func projectEnvironmentAppIDsDB(ctx context.Context, db sqlc.DBTX, accountID, projectID, environment string) ([]string, error) {
	if IsProductionScope(environment) {
		environment = "production"
	}
	return sqlc.New().ProjectEnvironmentAppIDs(ctx, db, sqlc.ProjectEnvironmentAppIDsParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), Environment: environment})
}

func (m *MemStore) ProjectEnvironmentAppIDs(ctx context.Context, accountID, projectID, environment string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return nil, err
	}
	return m.projectEnvironmentAppIDsLocked(accountID, projectID, environment), nil
}

func (m *MemStore) projectEnvironmentAppIDsLocked(accountID, projectID, environment string) []string {
	included := make(map[string]bool)
	if IsProductionScope(environment) {
		omitted := m.environmentOnlyProjectAppIDsLocked(accountID, projectID)
		for id, app := range m.apps {
			if !slices.Contains(omitted, id) && app.AccountID == accountID && app.ProjectID == projectID && app.Status != AppDeleted && app.PreviewOfSlug == "" {
				included[id] = true
			}
		}
	} else {
		for _, specID := range m.projectEnvironmentWorkloadHeads {
			spec := m.projectEnvironmentWorkloadSpecs[specID]
			if spec.EnvironmentSlug == environment {
				included[spec.AppID] = true
			}
		}
		for _, d := range m.deployments {
			if d.Scope == environment && d.DeletedAt == nil {
				included[d.AppID] = true
			}
		}
	}
	var ids []string
	for id := range included {
		app := m.apps[id]
		if app.AccountID == accountID && app.ProjectID == projectID && app.Status != AppDeleted && app.PreviewOfSlug == "" {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}
