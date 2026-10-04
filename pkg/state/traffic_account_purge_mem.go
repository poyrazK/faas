// adr: 531
package state

import "context"

// Account retirement projects one final cascade, including foreign redirect
// domains and surfaces whose app FK points into the retiring account.
func (m *MemStore) checkMemTrafficAccountPurgeLocked(ctx context.Context, account string) (memTrafficPolicyChange, error) {
	apps := make(map[string]App)
	for id, app := range m.apps {
		if err := ctx.Err(); err != nil {
			return memTrafficPolicyChange{}, err
		}
		if app.AccountID == account {
			apps[id] = app
		}
	}
	change, err := m.memTrafficAppPurgeChangeLocked(ctx, apps)
	if err != nil {
		return change, err
	}
	change.Environments = make(map[string]ProjectEnvironment)
	change.PlatformTenants = make(map[string]PlatformTenant)
	change.TenantSurfaceLinks = make(map[string]string)
	if err := visitMemTrafficRows(ctx, m.corsPresets, nil, func(row CorsPreset) error {
		if row.AccountID == account {
			change.Presets[row.ID] = CorsPreset{}
		}
		return nil
	}); err != nil {
		return change, err
	}
	if err := visitMemTrafficRows(ctx, m.edgeRules, nil, func(row EdgeRule) error {
		if row.AccountID == account {
			change.Rules[row.ID] = EdgeRule{}
		} else if row.CorsPresetID != nil {
			if _, deleted := change.Presets[*row.CorsPresetID]; deleted {
				row.CorsPresetID = nil
				change.Rules[row.ID] = row
			}
		}
		return nil
	}); err != nil {
		return change, err
	}
	for key, row := range m.projectEnvironments {
		if err := ctx.Err(); err != nil {
			return change, err
		}
		if row.AccountID == account {
			change.Environments[key] = ProjectEnvironment{}
		}
	}
	for key, row := range m.projectEnvironmentEdgePolicies {
		if err := ctx.Err(); err != nil {
			return change, err
		}
		if row.AccountID == account {
			change.Policies[key] = ProjectEnvironmentEdgePolicy{}
		}
	}
	for id, row := range m.platformTenants {
		if err := ctx.Err(); err != nil {
			return change, err
		}
		if row.AccountID == account {
			change.PlatformTenants[id] = PlatformTenant{}
		}
	}
	for id, row := range m.tenantSurfaces {
		if err := ctx.Err(); err != nil {
			return change, err
		}
		if row.AccountID == account {
			change.TenantSurfaces[id] = TenantSurface{}
		}
	}
	for surface, tenant := range m.platformTenantBySurface {
		if err := ctx.Err(); err != nil {
			return change, err
		}
		_, surfaceDeleted := change.TenantSurfaces[surface]
		_, tenantDeleted := change.PlatformTenants[tenant]
		if surfaceDeleted || tenantDeleted {
			change.TenantSurfaceLinks[surface] = ""
		}
	}
	if err := visitMemTrafficTenantHostnames(ctx, m.tenantHostnames, nil, func(row TenantHostname) error {
		if _, deleted := change.TenantSurfaces[row.SurfaceID]; deleted {
			change.TenantHostnames[trafficTenantProposalKey(row)] = TenantHostname{}
		}
		return nil
	}); err != nil {
		return change, err
	}
	err = appTrafficBindingError(m.checkMemTrafficBindingLocked(ctx, account, nil, "", change))
	return change, err
}

func (m *MemStore) publishMemTrafficAccountPurgeLocked(account string, change memTrafficPolicyChange) {
	for id := range change.Apps {
		m.publishMemTrafficAppPurgeLocked(id)
	}
	for id, row := range change.Rules {
		if row.ID == "" {
			delete(m.edgeRules, id)
		} else {
			m.edgeRules[id] = row
		}
	}
	for id := range change.Presets {
		delete(m.corsPresets, id)
	}
	for id := range change.Policies {
		delete(m.projectEnvironmentEdgePolicies, id)
	}
	for id := range change.Environments {
		delete(m.projectEnvironments, id)
	}
	for id, row := range m.tenantHostnames {
		if _, deleted := change.TenantSurfaces[row.SurfaceID]; deleted {
			delete(m.tenantHostnames, id)
		}
	}
	for id := range change.TenantSurfaces {
		delete(m.tenantSurfaces, id)
		delete(m.platformTenantBySurface, id)
	}
	for id := range change.TenantSurfaceLinks {
		delete(m.platformTenantBySurface, id)
	}
	for id, row := range m.projects {
		if row.AccountID == account {
			delete(m.projects, id)
		}
	}
	for id, row := range m.invocations {
		_, deletedApp := change.Apps[row.AppID]
		if row.AccountID == account || deletedApp {
			if row.QuotaReserved {
				m.decrementAccountAsyncInflightLocked(row.AccountID)
			}
			delete(m.invocations, id)
		}
	}
	delete(m.accountAsyncQuota, account)
}
