package sched

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// A live zero-traffic prime is qualification capacity, not a serving worker.
// Use the same graph that synthetic queue delivery resolves. No graph permits
// only the legacy weighted target; an incomplete graph never falls back.
func (e *Engine) projectWorkerTargets(ctx context.Context, app state.App, deployments []state.Deployment) (map[string]struct{}, map[string]struct{}, error) {
	targets := workerDeploymentTargets(deployments)
	held := map[string]struct{}{}
	if app.ProjectID == "" || app.PreviewOfSlug != "" {
		return targets, held, nil
	}
	reader, ok := e.store.(state.ProjectReleaseSetReader)
	if !ok {
		return nil, nil, state.ErrConflict
	}
	byScope := map[string][]state.Deployment{}
	for _, dep := range deployments {
		byScope[normalizedDeploymentScope(dep.Scope)] = append(byScope[normalizedDeploymentScope(dep.Scope)], dep)
	}
	targets = map[string]struct{}{}
	for scope, candidates := range byScope {
		graph, err := reader.ActiveProjectReleaseSet(ctx, app.AccountID, app.ProjectID, scope)
		if errors.Is(err, state.ErrNotFound) {
			weighted := []state.Deployment{}
			for _, dep := range candidates {
				if dep.TrafficPercent > 0 {
					weighted = append(weighted, dep)
				}
			}
			for id := range workerDeploymentTargets(weighted) {
				targets[id] = struct{}{}
			}
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		selected := ""
		for _, member := range graph.Members {
			if member.AppID == app.ID {
				selected = member.DeploymentID
			}
		}
		found := false
		for _, dep := range candidates {
			if dep.ID == selected && dep.Status == state.DeployLive {
				targets[dep.ID] = struct{}{}
				found = true
			}
		}
		if !found {
			return nil, nil, state.ErrConflict
		}
	}
	if store, ok := e.store.(state.ProjectReleaseWorkerStore); ok {
		candidates, err := store.HeldProjectReleaseWorkers(ctx, app.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, id := range candidates {
			held[id] = struct{}{}
		}
	}
	return targets, held, nil
}
