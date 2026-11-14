package state

import (
	"context"
	"slices"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// EnvironmentOnlyProjectAppReader identifies shared identities that have
// stage settings but have never been admitted to production. They continue
// to consume app quota and cannot be removed by a production omission.
type EnvironmentOnlyProjectAppReader interface {
	EnvironmentOnlyProjectAppIDs(context.Context, string, string) ([]string, error)
}

func EnvironmentOnlyProjectApps(ctx context.Context, store any, accountID, projectID string) (map[string]bool, error) {
	out := make(map[string]bool)
	reader, ok := store.(EnvironmentOnlyProjectAppReader)
	if !ok || projectID == "" {
		return out, nil
	}
	ids, err := reader.EnvironmentOnlyProjectAppIDs(ctx, accountID, projectID)
	for _, id := range ids {
		out[id] = true
	}
	return out, err
}

func (s *PgStore) EnvironmentOnlyProjectAppIDs(ctx context.Context, accountID, projectID string) ([]string, error) {
	return sqlc.New().EnvironmentOnlyProjectAppIDs(ctx, s.pool, sqlc.EnvironmentOnlyProjectAppIDsParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID)})
}

func (m *MemStore) EnvironmentOnlyProjectAppIDs(ctx context.Context, accountID, projectID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return nil, err
	}
	return m.environmentOnlyProjectAppIDsLocked(accountID, projectID), nil
}

func (m *MemStore) environmentOnlyProjectAppIDsLocked(accountID, projectID string) []string {
	stage, production := make(map[string]bool), make(map[string]bool)
	for appID, admittedProject := range m.projectAppStageAdmissions {
		if admittedProject == projectID {
			stage[appID] = true
		}
	}
	for _, specID := range m.projectEnvironmentWorkloadHeads {
		spec := m.projectEnvironmentWorkloadSpecs[specID]
		if spec.AccountID != accountID || spec.ProjectID != projectID {
			continue
		}
		if spec.EnvironmentSlug == "production" {
			production[spec.AppID] = true
		} else {
			stage[spec.AppID] = true
		}
	}
	for _, d := range m.deployments {
		if IsProductionScope(d.Scope) {
			production[d.AppID] = true
		}
	}
	var ids []string
	for id := range stage {
		app := m.apps[id]
		if app.AccountID == accountID && app.ProjectID == projectID && app.PreviewOfSlug == "" &&
			app.Status != AppDeleted && app.Manifest.ProjectSourceSHA256 == "" && !production[id] {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

func (m *MemStore) putWorkloadSpecHeadLocked(spec ProjectEnvironmentWorkloadSpec) {
	m.projectEnvironmentWorkloadHeads[workloadSpecHeadKey(spec.EnvironmentID, spec.AppID)] = spec.ID
	if spec.EnvironmentSlug != "production" {
		if m.projectAppStageAdmissions == nil {
			m.projectAppStageAdmissions = make(map[string]string)
		}
		m.projectAppStageAdmissions[spec.AppID] = spec.ProjectID
	}
}
