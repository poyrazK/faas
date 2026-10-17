package state

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// ProjectReleaseStagingStore owns temporary customer intent. It never writes instances or routes.
type ProjectReleaseStagingStore interface {
	StageProjectRelease(context.Context, string, string, api.StageProjectReleaseRequest) (api.ProjectReleaseStage, error)
	ProjectReleaseStage(context.Context, string, string, string, string) (api.ProjectReleaseStage, error)
	CancelProjectReleaseStage(context.Context, string, string, string, string) (api.ProjectReleaseStage, error)
	ServiceReleaseReadinessTargets(context.Context, string) (map[string]int, error)
}

type projectReleaseStage struct {
	ID, AccountID, ProjectID string
	Manifest                 api.CheckedProjectReleaseManifest
	Targets                  map[string]int
	CreatedAt, ExpiresAt     time.Time
	Cancelled                bool
}

type releaseStageSnapshot struct {
	Project     Project
	Apps        []App
	Deployments map[string]Deployment
	Policies    map[string]api.BindingReleasePolicy
	Managed     map[string]bool
	Mirrors     map[string]bool
	Active      string
	Source      *ProjectReleaseSet
}

func stageCanonicalID(id string) string {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return id
	}
	return parsed.String()
}

func validateReleaseStage(snapshot releaseStageSnapshot, manifest api.CheckedProjectReleaseManifest) (map[string]int, error) {
	if manifest.Deactivate || manifest.ExpectedActiveReleaseID == nil || manifest.ExpectedFallbackDeployments == nil ||
		manifest.Project != snapshot.Project.Slug || !api.ValidProjectEnvironmentSlug(manifest.Environment) ||
		manifest.TTLSeconds <= 0 || manifest.TTLSeconds > api.RevisionPinMaxTTLSeconds || len(snapshot.Apps) == 0 ||
		len(snapshot.Apps) > api.ProjectReleaseSetMaxMembers || len(manifest.Deployments) != len(snapshot.Apps) ||
		len(manifest.ServiceTargets) != len(snapshot.Apps) || len(manifest.ExpectedPolicyRevisions) != len(snapshot.Apps) {
		return nil, ErrInvalidArgument
	}
	if snapshot.Active != "" && len(manifest.ExpectedFallbackDeployments) != 0 {
		return nil, ErrInvalidArgument
	}
	if stageCanonicalID(snapshot.Active) != *manifest.ExpectedActiveReleaseID {
		return nil, ErrConflict
	}
	targets := map[string]int{}
	members := map[string]string{}
	fallback := map[string]string{}
	for _, app := range snapshot.Apps {
		id, err := uuid.Parse(manifest.Deployments[app.Slug])
		if err != nil || id == uuid.Nil || id.String() != manifest.Deployments[app.Slug] {
			return nil, ErrInvalidArgument
		}
		if app.AccountID != snapshot.Project.AccountID {
			return nil, ErrConflict
		}
		dep, ok := snapshot.Deployments[id.String()]
		if !ok || dep.AppID != app.ID || dep.Scope != manifest.Environment || dep.Status != DeployLive || dep.EnvironmentWorkloadHeld() || dep.RootfsKey == "" || dep.ImageDigest == "" || snapshot.Managed[app.ID] || snapshot.Mirrors[id.String()] || app.Status != AppActive || app.Manifest.RevisionPinTTLSeconds < manifest.TTLSeconds {
			return nil, fmt.Errorf("staging candidate %s in %s: status=%s materialized=%t active_app=%t retained=%t held=%t managed=%t mirror=%t: %w", app.Slug, dep.Scope, dep.Status, dep.RootfsKey != "" && dep.ImageDigest != "", app.Status == AppActive, app.Manifest.RevisionPinTTLSeconds >= manifest.TTLSeconds, dep.EnvironmentWorkloadHeld(), snapshot.Managed[app.ID], snapshot.Mirrors[id.String()], ErrConflict)
		}
		for _, live := range snapshot.Deployments {
			if live.AppID == app.ID && live.Scope == manifest.Environment && live.Status == DeployLive && IsServiceRollout(live) {
				return nil, ErrConflict
			}
		}
		policy := snapshot.Policies[app.ID]
		if policy.Mode != "enforce" || policy.Revision != manifest.ExpectedPolicyRevisions[app.Slug] {
			return nil, ErrBindingReleasePolicyRevision
		}
		expectedTargets := map[string]string{}
		for _, binding := range app.Manifest.ServiceBindings {
			if selected := manifest.Deployments[binding.Service]; selected != "" {
				expectedTargets[binding.Service] = selected
			}
		}
		if manifest.ServiceTargets[app.Slug] == nil || !maps.Equal(expectedTargets, manifest.ServiceTargets[app.Slug]) {
			return nil, ErrConflict
		}
		members[app.ID] = dep.ID
		if app.Manifest.ExecutionMode == api.ExecutionModeService {
			desired := 1
			if app.Manifest.ServiceReplicas != nil && app.Manifest.ServiceReplicas.Desired > desired {
				desired = app.Manifest.ServiceReplicas.Desired
			}
			targets[dep.ID] = desired
		}
		if snapshot.Active == "" {
			for _, live := range snapshot.Deployments {
				if live.AppID == app.ID && live.Scope == manifest.Environment && live.Status == DeployLive && live.TrafficPercent > 0 {
					if fallback[app.Slug] != "" || live.TrafficPercent != 100 {
						return nil, ErrConflict
					}
					fallback[app.Slug] = stageCanonicalID(live.ID)
				}
			}
			if fallback[app.Slug] == "" {
				return nil, ErrConflict
			}
		}
	}
	if snapshot.Active == "" && !maps.Equal(fallback, manifest.ExpectedFallbackDeployments) {
		return nil, ErrConflict
	}
	if len(targets) == 0 {
		return nil, ErrInvalidArgument
	}
	if manifest.RollbackReleaseID != "" {
		if snapshot.Source == nil {
			return nil, ErrConflict
		}
		if err := validateCheckedRollbackSource(*snapshot.Source, snapshot.Project.AccountID, snapshot.Project.ID, manifest.Environment, members, time.Now()); err != nil {
			return nil, err
		}
	}
	return targets, nil
}

func releaseStageView(stage projectReleaseStage, snapshot releaseStageSnapshot, instances []Instance, recovery map[string]ServiceRecovery) api.ProjectReleaseStage {
	out := api.ProjectReleaseStage{ID: stage.ID, Project: stage.Manifest.Project, Environment: stage.Manifest.Environment, CreatedAt: stage.CreatedAt, ExpiresAt: stage.ExpiresAt, Status: "ready", Members: []api.ProjectReleaseStageMember{}}
	targets, err := validateReleaseStage(snapshot, stage.Manifest)
	if err != nil {
		out.Status = "blocked"
		out.Reason = "manifest_changed"
		targets = stage.Targets
	}
	for _, app := range snapshot.Apps {
		for depID, desired := range targets {
			dep := snapshot.Deployments[stageCanonicalID(depID)]
			if dep.AppID != app.ID {
				continue
			}
			ready := 0
			for _, ins := range instances {
				if ins.AppID == app.ID && ins.DeploymentID == depID && ins.Mode == string(InstanceModeService) && State(ins.State) == StateRunning {
					ready++
				}
			}
			out.Members = append(out.Members, api.ProjectReleaseStageMember{App: app.Slug, DeploymentID: stageCanonicalID(depID), Desired: desired, Ready: ready})
			if ready < desired && out.Status != "blocked" {
				out.Status = "waiting"
				out.Reason = "service_readiness"
			}
			if ready < desired && out.Reason != "manifest_changed" {
				switch recovery[app.ID].Status {
				case "waiting_capacity":
					out.Status = "blocked"
					out.Reason = "capacity"
				case "retrying_startup":
					out.Status = "blocked"
					out.Reason = "startup"
				case "waiting_dependency":
					out.Status = "blocked"
					out.Reason = "dependency"
				}
			}
		}
	}
	sort.Slice(out.Members, func(i, j int) bool { return out.Members[i].App < out.Members[j].App })
	if !time.Now().Before(stage.ExpiresAt) {
		out.Status = "expired"
		out.Reason = "lease_expired"
	}
	if stage.Cancelled {
		out.Status = "cancelled"
		out.Reason = ""
	}
	return out
}

func cloneReleaseStage(stage projectReleaseStage) projectReleaseStage {
	raw, _ := json.Marshal(stage)
	var out projectReleaseStage
	_ = json.Unmarshal(raw, &out)
	return out
}
