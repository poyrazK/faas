package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) OperatorTenantApps(ctx context.Context, accountID string) ([]api.ObsTenantApp, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	type summary struct {
		row       api.ObsTenantApp
		createdAt time.Time
	}
	apps := make([]summary, 0)
	counts := map[string]int{}
	for _, app := range m.apps {
		if app.AccountID == accountID && app.Status != AppDeleted {
			apps = append(apps, summary{api.ObsTenantApp{ID: app.ID, Slug: app.Slug, Status: string(app.Status)}, app.CreatedAt})
			counts[app.ID] = 0
		}
	}
	for _, deployment := range m.deployments {
		if _, owned := counts[deployment.AppID]; owned && deployment.Status == DeployLive && deployment.DeletedAt == nil {
			counts[deployment.AppID]++
		}
	}
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].createdAt.Equal(apps[j].createdAt) {
			return apps[i].row.ID > apps[j].row.ID
		}
		return apps[i].createdAt.After(apps[j].createdAt)
	})
	out := make([]api.ObsTenantApp, 0, len(apps))
	for _, app := range apps {
		app.row.Deployments = counts[app.row.ID]
		out = append(out, app.row)
	}
	return out, nil
}

func (m *MemStore) OperatorCapacityProfiles(ctx context.Context) ([]api.ObsCapacityProfile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	profiles := make(map[string]*api.ObsCapacityProfile, len(api.ResourceProfiles)+1)
	for _, app := range m.apps {
		if app.Status == AppDeleted {
			continue
		}
		label := operatorProfileLabel(app)
		row := profiles[label]
		if row == nil {
			row = &api.ObsCapacityProfile{ResourceProfile: label}
			if label != "custom" {
				row.MemoryMB, row.CPUMillicores = app.RAMMB, app.CPUMillicores
			}
			profiles[label] = row
		}
		row.Apps++
		row.ReservedMemoryMB += int64(app.RAMMB)
		row.ReservedCPUMillicores += int64(app.CPUMillicores)
	}
	for _, instance := range m.instances {
		switch State(instance.State) {
		case StateRunning, StateWaking, StateColdBooting:
		default:
			continue
		}
		app, found := m.apps[instance.AppID]
		if found && app.Status != AppDeleted {
			profiles[operatorProfileLabel(app)].LiveInstances++
		}
	}
	out := make([]api.ObsCapacityProfile, 0, len(profiles))
	for _, profile := range api.ResourceProfiles {
		if row := profiles[string(profile.Name)]; row != nil {
			out = append(out, *row)
		}
	}
	if custom := profiles["custom"]; custom != nil {
		out = append(out, *custom)
	}
	return out, nil
}

func operatorProfileLabel(app App) string {
	if profile := api.ResourceProfileForResources(app.RAMMB, app.CPUMillicores); profile != "" {
		return string(profile)
	}
	return "custom"
}
