// adr: 375
package state

import "context"

// App purge cascades reservations and app-scoped routing intent. Project the
// complete cascade before deleting any jobs, artifacts, or deletion claims.
func (m *MemStore) checkMemTrafficAppPurgeLocked(ctx context.Context, app App) error {
	change := memTrafficPolicyChange{Apps: map[string]App{app.ID: {}}, Domains: make(map[string]CustomDomain),
		TenantSurfaces: make(map[string]TenantSurface), TenantHostnames: make(map[string]TenantHostname),
		Rules: make(map[string]EdgeRule), Presets: make(map[string]CorsPreset), Policies: make(map[string]ProjectEnvironmentEdgePolicy),
		Aliases: make(map[string]DeploymentAlias), Deployments: make(map[string]Deployment)}
	for id, domain := range m.domains {
		if err := ctx.Err(); err != nil {
			return err
		}
		if domain.AppID == app.ID || domain.RedirectAppID == app.ID {
			change.Domains[id] = CustomDomain{}
		}
	}
	for id, surface := range m.tenantSurfaces {
		if err := ctx.Err(); err != nil {
			return err
		}
		if surface.AppID == app.ID {
			change.TenantSurfaces[id] = TenantSurface{}
		}
	}
	if err := visitMemTrafficTenantHostnames(ctx, m.tenantHostnames, nil, func(host TenantHostname) error {
		if _, found := change.TenantSurfaces[host.SurfaceID]; found {
			change.TenantHostnames[trafficTenantProposalKey(host)] = TenantHostname{}
		}
		return nil
	}); err != nil {
		return err
	}
	for id, rule := range m.edgeRules {
		if err := ctx.Err(); err != nil {
			return err
		}
		if rule.AppID == app.ID {
			change.Rules[id] = EdgeRule{}
		}
	}
	for id, preset := range m.corsPresets {
		if err := ctx.Err(); err != nil {
			return err
		}
		if preset.AppID == app.ID {
			change.Presets[id] = CorsPreset{}
		}
	}
	for id, policy := range m.projectEnvironmentEdgePolicies {
		if err := ctx.Err(); err != nil {
			return err
		}
		if policy.AppID == app.ID {
			change.Policies[id] = ProjectEnvironmentEdgePolicy{}
		}
	}
	for id, alias := range m.deploymentAliases {
		if err := ctx.Err(); err != nil {
			return err
		}
		if alias.AppID == app.ID {
			change.Aliases[id] = DeploymentAlias{}
		}
	}
	for id, deployment := range m.deployments {
		if err := ctx.Err(); err != nil {
			return err
		}
		if deployment.AppID == app.ID {
			change.Deployments[id] = Deployment{}
		}
	}
	return appTrafficBindingError(m.checkMemTrafficBindingLocked(ctx, app.AccountID, nil, app.ID, change))
}

func (m *MemStore) publishMemTrafficAppPurgeLocked(appID string) {
	delete(m.defaultDomains, appID)
	for id, domain := range m.domains {
		if domain.AppID == appID || domain.RedirectAppID == appID {
			_ = m.deleteCustomDomainLocked(id)
		}
	}
	for id, host := range m.tenantHostnames {
		if m.tenantSurfaces[host.SurfaceID].AppID == appID {
			delete(m.tenantHostnames, id)
		}
	}
	for id, surface := range m.tenantSurfaces {
		if surface.AppID == appID {
			delete(m.tenantSurfaces, id)
			delete(m.platformTenantBySurface, id)
		}
	}
	for id, rule := range m.edgeRules {
		if rule.AppID == appID {
			delete(m.edgeRules, id)
		}
	}
	for id, preset := range m.corsPresets {
		if preset.AppID == appID {
			delete(m.corsPresets, id)
		}
	}
	for id, policy := range m.projectEnvironmentEdgePolicies {
		if policy.AppID == appID {
			delete(m.projectEnvironmentEdgePolicies, id)
		}
	}
	for id, alias := range m.deploymentAliases {
		if alias.AppID == appID {
			delete(m.deploymentAliases, id)
		}
	}
}
