package state

import (
	"context"
)

func (m *MemStore) projectServiceMemberLocked(appID, deploymentID string) projectServiceMember {
	app, ok := m.apps[appID]
	d, found := m.deployments[deploymentID]
	if !ok || !found || d.AppID != appID || app.Status == AppDeleted || app.PreviewOfSlug != "" || d.EnvironmentWorkloadManaged() {
		return projectServiceMember{}
	}
	spec, pinned := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadDeploymentSpecs[deploymentID]]
	if !pinned || spec.AppID != appID || spec.EnvironmentSlug != d.Scope {
		return projectServiceMember{}
	}
	name := app.WorkloadName
	if name == "" {
		name = app.Slug
	}
	return projectServiceMember{AppID: appID, AccountID: app.AccountID, ProjectID: app.ProjectID, Name: name, Slug: app.Slug,
		DeploymentID: deploymentID, Scope: d.Scope, Live: d.Status == DeployLive && !d.EnvironmentWorkloadHeld(), Settings: spec.Settings, Hash: spec.Hash}
}

func (m *MemStore) ResolveProjectServiceBinding(ctx context.Context, callerAppID, callerDeploymentID, service string, requested ...string) (EnvironmentGitOpsServiceBindingRoute, bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return EnvironmentGitOpsServiceBindingRoute{}, false, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	requestedID := ""
	if len(requested) > 0 {
		requestedID = requested[0]
	}
	input := projectServiceGraphInput{Caller: m.projectServiceMemberLocked(callerAppID, callerDeploymentID)}
	input.Managed = m.promotionServiceMemberLocked(callerAppID, callerDeploymentID)
	if !input.Managed && len(input.Caller.Settings.Manifest.ServiceBindings) == 0 {
		return EnvironmentGitOpsServiceBindingRoute{}, false, false, nil
	}
	for _, release := range m.projectReleaseSets {
		if requestedID != "" && requestedID != release.ID {
			continue
		}
		if release.AccountID != input.Caller.AccountID || release.ProjectID != input.Caller.ProjectID || release.EnvironmentSlug != input.Caller.Scope ||
			!m.releaseUsableLocked(release) || releaseMemberForApp(release, callerAppID) != callerDeploymentID {
			continue
		}
		graph := projectServiceGraph{ID: release.ID}
		for _, member := range release.Members {
			graph.Members = append(graph.Members, m.projectServiceMemberLocked(member.AppID, member.DeploymentID))
		}
		input.Graphs = append(input.Graphs, graph)
	}
	for _, graph := range input.Graphs {
		if m.projectReleaseSets[graph.ID].Active {
			input.Graphs = []projectServiceGraph{graph}
			break
		}
	}
	if len(input.Graphs) == 0 {
		for id, p := range m.projectEnvironmentPromotions {
			h := m.promotionHealth[id]
			if requestedID != "" && requestedID != id {
				continue
			}
			if p.AccountID != input.Caller.AccountID || p.ProjectID != input.Caller.ProjectID || p.ToEnvironment != input.Caller.Scope || p.Status != "running" || p.TargetReleaseSetID != "" || h == nil || !m.clock().Before(h.Deadline) || (h.Phase != "preflight" && h.Phase != "cutover") || m.promotionReleaseConfigurations[id].DestinationConfig == nil {
				continue
			}
			graph, matched := projectServiceGraph{ID: id}, false
			for _, pin := range m.projectEnvironmentPromotionWorkloadSpecs {
				if pin.PromotionID != id {
					continue
				}
				matched = matched || pin.AppID == callerAppID && pin.DeploymentID == callerDeploymentID
				member := m.projectServiceMemberLocked(pin.AppID, pin.DeploymentID)
				d := m.deployments[pin.DeploymentID]
				member.Live = member.Live && d.TrafficPercentExplicit && d.TrafficPercent == 0
				graph.Members = append(graph.Members, member)
			}
			if matched {
				if len(graph.Members) != len(m.promotionReleaseConfigurations[id].Workloads) {
					return EnvironmentGitOpsServiceBindingRoute{}, true, false, ErrConflict
				}
				input.Graphs = append(input.Graphs, graph)
			}
		}
	}
	return resolveProjectServiceGraph(input, normalizeProjectServiceName(service))
}

// Members without outbound declarations must also fail closed. Otherwise an
// undeclared call from a candidate could fall through to ordinary app routing.
func (m *MemStore) promotionServiceMemberLocked(appID, deploymentID string) bool {
	for id, capture := range m.promotionReleaseConfigurations {
		if capture.DestinationConfig == nil {
			continue
		}
		for _, w := range capture.Workloads {
			if w.AppID != appID {
				continue
			}
			if w.Previous.ID == deploymentID {
				return true
			}
			if pin := m.projectEnvironmentPromotionWorkloadSpecs[workloadSpecHeadKey(id, appID)]; pin.DeploymentID == deploymentID && deploymentID != "" {
				return true
			}
		}
	}
	return false
}
