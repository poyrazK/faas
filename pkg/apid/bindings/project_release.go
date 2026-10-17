package bindings

import (
	"context"
	"errors"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type ProjectReleaseCatalog interface {
	AppsForProject(context.Context, string, string) ([]state.App, error)
	DeploymentByID(context.Context, string) (state.Deployment, error)
	LiveDeployments(context.Context, string) ([]state.Deployment, error)
	GetBindingReleasePolicy(context.Context, string, string, string) (api.BindingReleasePolicy, error)
	ActiveProjectReleaseSet(context.Context, string, string, string) (state.ProjectReleaseSet, error)
}

func ProjectReleaseMembers(apps []state.App, deployments map[string]string, ttl int) ([]state.ProjectReleaseMember, *api.Problem) {
	if ttl <= 0 || ttl > api.RevisionPinMaxTTLSeconds || len(deployments) == 0 || len(deployments) > api.ProjectReleaseSetMaxMembers || len(apps) != len(deployments) {
		return nil, api.ErrValidation("release requires every project workload and a valid revision retention TTL")
	}
	members := make([]state.ProjectReleaseMember, 0, len(apps))
	for _, app := range apps {
		id, err := uuid.Parse(deployments[app.Slug])
		if err != nil || id == uuid.Nil || app.Manifest.RevisionPinTTLSeconds < ttl {
			return nil, api.ErrValidation("every workload requires a deployment UUID and sufficient revision retention")
		}
		members = append(members, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: id.String()})
	}
	return members, nil
}

func ProjectServiceTargets(app state.App, deployments map[string]string) map[string]string {
	targets := map[string]string{}
	for _, binding := range app.Manifest.ServiceBindings {
		if id := deployments[binding.Service]; id != "" {
			targets[binding.Service] = id
		}
	}
	return targets
}

func ValidateCheckedProjectRelease(manifest api.CheckedProjectReleaseManifest, project, environment string, apps []state.App) ([]state.ProjectReleaseMember, *api.Problem) {
	if manifest.Project != project || manifest.Environment != environment || manifest.ExpectedActiveReleaseID == nil || manifest.ExpectedFallbackDeployments == nil || len(manifest.ExpectedPolicyRevisions) != len(apps) || len(manifest.ServiceTargets) != len(apps) {
		return nil, api.ErrValidation("activation requires the complete prepared manifest and explicit expected routing state")
	}
	if id := *manifest.ExpectedActiveReleaseID; id != "" {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return nil, api.ErrValidation("invalid expected active release")
		}
	}
	var members []state.ProjectReleaseMember
	var problem *api.Problem
	if manifest.Deactivate {
		members, problem = ProjectDeactivationMembers(apps, manifest)
	} else {
		members, problem = ProjectReleaseMembers(apps, manifest.Deployments, manifest.TTLSeconds)
	}
	if problem != nil {
		return nil, problem
	}
	for _, app := range apps {
		revision, ok := manifest.ExpectedPolicyRevisions[app.Slug]
		if !ok || revision < 1 || manifest.ServiceTargets[app.Slug] == nil || !maps.Equal(manifest.ServiceTargets[app.Slug], ProjectServiceTargets(app, manifest.Deployments)) {
			return nil, api.ErrValidation("prepared policy or service graph changed; prepare and qualify again")
		}
	}
	return members, nil
}

func PrepareProjectRelease(ctx context.Context, catalog ProjectReleaseCatalog, accountID string, project state.Project, environment string, request api.PublishProjectReleaseSetRequest) (api.CheckedProjectReleaseManifest, *api.Problem) {
	result := api.CheckedProjectReleaseManifest{Project: project.Slug, Environment: environment, TTLSeconds: request.TTLSeconds, Deployments: maps.Clone(request.Deployments), ExpectedFallbackDeployments: map[string]string{}, ExpectedPolicyRevisions: map[string]int64{}, ServiceTargets: map[string]map[string]string{}}
	apps, err := catalog.AppsForProject(ctx, accountID, project.ID)
	if err != nil {
		return result, api.ErrCapacity("could not read project workloads")
	}
	if _, problem := ProjectReleaseMembers(apps, request.Deployments, request.TTLSeconds); problem != nil {
		return result, problem
	}
	for slug, id := range result.Deployments {
		parsed, _ := uuid.Parse(id)
		result.Deployments[slug] = parsed.String()
	}
	active, err := catalog.ActiveProjectReleaseSet(ctx, accountID, project.ID, environment)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return result, api.ErrCapacity("could not read current release graph")
	}
	expected := active.ID
	result.ExpectedActiveReleaseID = &expected
	for _, app := range apps {
		deployment, err := ProjectReleaseDeployment(ctx, catalog, result.Deployments[app.Slug])
		if err != nil || deployment.AppID != app.ID || deployment.Scope != environment || deployment.Status != state.DeployLive || deployment.RootfsKey == "" || deployment.ImageDigest == "" || deployment.EnvironmentWorkloadHeld() {
			return result, api.ErrValidation("every candidate must be an eligible materialized live deployment in the graph environment")
		}
		policy, err := catalog.GetBindingReleasePolicy(ctx, accountID, app.ID, environment)
		if err != nil || policy.Mode != "enforce" {
			return result, api.ErrValidation("checked graph activation requires an enforced binding release policy on every member scope")
		}
		result.ExpectedPolicyRevisions[app.Slug] = policy.Revision
		result.ServiceTargets[app.Slug] = ProjectServiceTargets(app, result.Deployments)
		if expected != "" {
			continue
		}
		deployments, err := catalog.LiveDeployments(ctx, app.ID)
		if err != nil {
			return result, api.ErrCapacity("could not read current weighted routing")
		}
		for _, deployment := range deployments {
			if deployment.Scope != environment || deployment.TrafficPercent == 0 {
				continue
			}
			if deployment.TrafficPercent != 100 || result.ExpectedFallbackDeployments[app.Slug] != "" {
				return result, api.ErrValidation("preparing the first checked graph requires unambiguous weighted routing")
			}
			id, err := uuid.Parse(deployment.ID)
			if err != nil {
				return result, api.ErrValidation("invalid current deployment identity")
			}
			result.ExpectedFallbackDeployments[app.Slug] = id.String()
		}
	}
	return result, nil
}

// MemStore retains compact UUID keys from the legacy API; public manifests
// always use canonical UUIDs, independently of backend representation.
func ProjectReleaseDeployment(ctx context.Context, catalog interface {
	DeploymentByID(context.Context, string) (state.Deployment, error)
}, id string) (state.Deployment, error) {
	deployment, err := catalog.DeploymentByID(ctx, id)
	if errors.Is(err, state.ErrNotFound) {
		deployment, err = catalog.DeploymentByID(ctx, strings.ReplaceAll(id, "-", ""))
	}
	return deployment, err
}

type ProjectRollbackCatalog interface {
	ProjectReleaseCatalog
	state.ProjectReleaseSetReader
}

func PrepareProjectRollback(ctx context.Context, catalog ProjectRollbackCatalog, accountID string, project state.Project, environment string, request api.PrepareProjectRollbackRequest) (api.CheckedProjectReleaseManifest, *api.Problem) {
	var result api.CheckedProjectReleaseManifest
	id, err := uuid.Parse(request.ReleaseID)
	if err != nil || id == uuid.Nil || id.String() != request.ReleaseID {
		return result, api.ErrValidation("rollback requires a canonical historical release UUID")
	}
	source, err := catalog.ProjectReleaseSetByID(ctx, accountID, project.ID, environment, request.ReleaseID)
	if errors.Is(err, state.ErrNotFound) {
		return result, api.NewProblem(404, "not_found", "Not found", "Historical release not found.")
	}
	if err != nil {
		return result, api.ErrCapacity("could not read historical release")
	}
	if source.Active || source.ExpiresAt == nil || !time.Now().Before(*source.ExpiresAt) {
		return result, api.NewProblem(409, "release_graph_changed", "History unavailable", "Rollback requires a retained inactive release.")
	}
	apps, err := catalog.AppsForProject(ctx, accountID, project.ID)
	if err != nil {
		return result, api.ErrCapacity("could not read project workloads")
	}
	byID := map[string]string{}
	for _, member := range source.Members {
		byID[member.AppID] = member.DeploymentID
	}
	deployments := map[string]string{}
	for _, app := range apps {
		deployments[app.Slug] = byID[app.ID]
	}
	if len(source.Members) != len(apps) {
		return result, api.ErrValidation("historical membership must match the current project")
	}
	result, problem := PrepareProjectRelease(ctx, catalog, accountID, project, environment, api.PublishProjectReleaseSetRequest{TTLSeconds: request.TTLSeconds, Deployments: deployments})
	if problem != nil {
		return result, problem
	}
	if result.ExpectedActiveReleaseID == nil || *result.ExpectedActiveReleaseID == "" || *result.ExpectedActiveReleaseID == source.ID {
		return result, api.NewProblem(409, "release_graph_changed", "Active graph required", "Rollback requires a different active release graph.")
	}
	result.RollbackReleaseID = source.ID
	return result, nil
}

func ProjectDeactivationMembers(apps []state.App, manifest api.CheckedProjectReleaseManifest) ([]state.ProjectReleaseMember, *api.Problem) {
	if manifest.TTLSeconds != 0 || manifest.RollbackReleaseID != "" || manifest.ExpectedActiveReleaseID == nil || *manifest.ExpectedActiveReleaseID == "" || len(apps) == 0 || len(apps) > api.ProjectReleaseSetMaxMembers || len(apps) != len(manifest.Deployments) || !maps.Equal(manifest.Deployments, manifest.ExpectedFallbackDeployments) {
		return nil, api.ErrValidation("deactivation requires a complete exact fallback graph, active release and zero ttl_seconds")
	}
	members := make([]state.ProjectReleaseMember, 0, len(apps))
	for _, app := range apps {
		id, err := uuid.Parse(manifest.Deployments[app.Slug])
		if err != nil || id == uuid.Nil || id.String() != manifest.Deployments[app.Slug] {
			return nil, api.ErrValidation("fallback deployments require canonical UUIDs")
		}
		members = append(members, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: id.String()})
	}
	return members, nil
}

func PrepareProjectDeactivation(ctx context.Context, catalog ProjectReleaseCatalog, accountID string, project state.Project, environment string) (api.CheckedProjectReleaseManifest, *api.Problem) {
	result := api.CheckedProjectReleaseManifest{Deactivate: true, Project: project.Slug, Environment: environment, Deployments: map[string]string{}, ExpectedFallbackDeployments: map[string]string{}, ExpectedPolicyRevisions: map[string]int64{}, ServiceTargets: map[string]map[string]string{}}
	active, err := catalog.ActiveProjectReleaseSet(ctx, accountID, project.ID, environment)
	if errors.Is(err, state.ErrNotFound) {
		return result, api.NewProblem(409, "release_graph_changed", "Active graph required", "There is no active graph to deactivate.")
	}
	if err != nil {
		return result, api.ErrCapacity("could not read active graph")
	}
	result.ExpectedActiveReleaseID = &active.ID
	apps, err := catalog.AppsForProject(ctx, accountID, project.ID)
	if err != nil {
		return result, api.ErrCapacity("could not read project workloads")
	}
	if len(apps) == 0 || len(apps) > api.ProjectReleaseSetMaxMembers {
		return result, api.ErrValidation("deactivation requires a complete nonempty project fallback")
	}
	for _, app := range apps {
		deployments, err := catalog.LiveDeployments(ctx, app.ID)
		if err != nil {
			return result, api.ErrCapacity("could not read weighted fallback")
		}
		var selected *state.Deployment
		for _, dep := range deployments {
			if dep.Scope != environment || dep.TrafficPercent <= 0 {
				continue
			}
			if selected != nil || dep.TrafficPercent != 100 {
				return result, api.ErrValidation("deactivation requires one 100% fallback per workload; traffic splits are unsupported")
			}
			copy := dep
			selected = &copy
		}
		if selected == nil || selected.Status != state.DeployLive || selected.EnvironmentWorkloadHeld() || selected.RootfsKey == "" || selected.ImageDigest == "" {
			return result, api.ErrValidation("every workload needs an eligible materialized 100% fallback deployment")
		}
		id, err := uuid.Parse(selected.ID)
		if err != nil || id == uuid.Nil {
			return result, api.ErrValidation("invalid fallback deployment identity")
		}
		policy, err := catalog.GetBindingReleasePolicy(ctx, accountID, app.ID, environment)
		if err != nil || policy.Mode != "enforce" {
			return result, api.ErrValidation("every fallback scope needs an enforced binding release policy")
		}
		result.Deployments[app.Slug] = id.String()
		result.ExpectedPolicyRevisions[app.Slug] = policy.Revision
	}
	result.ExpectedFallbackDeployments = maps.Clone(result.Deployments)
	for _, app := range apps {
		result.ServiceTargets[app.Slug] = ProjectServiceTargets(app, result.Deployments)
	}
	return result, nil
}
