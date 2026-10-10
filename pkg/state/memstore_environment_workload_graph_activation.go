package state

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsGraphActivationStore = (*MemStore)(nil)

func (m *MemStore) ActivateEnvironmentGitOpsWorkloadGraph(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) (ProjectReleaseSet, bool, error) {
	if err := ctx.Err(); err != nil {
		return ProjectReleaseSet{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return ProjectReleaseSet{}, false, err
	}
	key := preparationGraphKey(lease.Source.Generation, reviewed.Hash)
	graph, exists := memory.graphs[key]
	if !exists {
		for _, current := range memory.graphs {
			if current.Generation == lease.Source.Generation && current.RevisionID == lease.Revision.ID {
				return ProjectReleaseSet{}, false, ErrConflict
			}
		}
		return ProjectReleaseSet{}, false, ErrNotFound
	}
	if graph.SourceID != memory.source.ID || graph.EnvironmentID != memory.source.EnvironmentID ||
		graph.Generation != memory.source.Generation || graph.IntentVersion != memory.source.IntentVersion ||
		graph.RevisionID != memory.source.ApprovedRevisionID || graph.PlanHash != reviewed.Hash {
		return ProjectReleaseSet{}, false, ErrConflict
	}
	evidence := m.environmentWorkloadActivationEvidenceLocked(memory, graph)
	if evidence.Activated {
		id := m.activeProjectReleaseSets[releaseKey(memory.source.ProjectID, memory.source.EnvironmentSlug)]
		if release, ok := m.projectReleaseSets[id]; ok {
			return cloneProjectReleaseSet(release), true, nil
		}
		return ProjectReleaseSet{}, false, ErrConflict
	}
	if _, _, err := m.environmentCandidateInputsLocked(lease, reviewed); err != nil {
		return ProjectReleaseSet{}, false, err
	}
	if !evidence.Qualified || !environmentGraphSupportsProductionServing(graph) {
		return ProjectReleaseSet{}, false, nil
	}
	if !m.environmentGitOpsScheduledJobsReadyLocked(memory, graph, "paused") {
		return ProjectReleaseSet{}, false, nil
	}

	activeID := m.activeProjectReleaseSets[releaseKey(memory.source.ProjectID, memory.source.EnvironmentSlug)]
	members, _, ttl, err := m.environmentGitOpsReleaseMembersLocked(memory.source, graph, activeID)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return ProjectReleaseSet{}, false, nil
		}
		return ProjectReleaseSet{}, false, err
	}
	previous := make(map[string]Deployment)
	previousJobs := make(map[string]Job)
	for _, member := range graph.Members {
		if member.CandidateDeploymentID == "" {
			continue
		}
		deployment, ok := m.deployments[member.CandidateDeploymentID]
		if !ok || deployment.AppID != member.AppID || !deployment.EnvironmentWorkloadHeld() ||
			deployment.Status != DeploySnapshotting || deployment.EnvironmentWorkloadRuntime == "" ||
			normalizedDeploymentScope(deployment.Scope) != normalizedDeploymentScope(memory.source.EnvironmentSlug) {
			return ProjectReleaseSet{}, false, ErrConflict
		}
		if err := m.checkDeploymentAutomationsLocked(deployment); err != nil {
			return ProjectReleaseSet{}, false, err
		}
		previous[deployment.ID] = deployment
		deployment.EnvironmentWorkloadHeldValue = environmentWorkloadHeldFlag(false)
		deployment.Status = DeployLive
		deployment.TrafficPercent = 0
		deployment.TrafficPercentExplicit = true
		deployment.Error = ""
		deployment.RolloutState = "complete"
		if deployment.RolloutCompletedAt == nil {
			now := time.Now().UTC()
			deployment.RolloutCompletedAt = &now
		}
		m.deployments[deployment.ID] = deployment
	}
	for _, member := range graph.Members {
		if member.ExecutionMode != api.ExecutionModeJob || !member.ScheduleConfigured {
			continue
		}
		job := m.jobs[member.JobID]
		previousJobs[job.ID] = job
		job.Status = "active"
		job.UpdatedAt = time.Now().UTC()
		m.jobs[job.ID] = job
	}
	release, err := m.publishProjectReleaseSetLocked(memory.source.AccountID, memory.source.ProjectID, memory.source.EnvironmentSlug, ttl, members, true)
	if err != nil {
		for id, deployment := range previous {
			m.deployments[id] = deployment
		}
		for id, job := range previousJobs {
			m.jobs[id] = job
		}
		if errors.Is(err, ErrConflict) {
			return ProjectReleaseSet{}, false, nil
		}
		return ProjectReleaseSet{}, false, err
	}
	for id, before := range previous {
		if current, ok := m.deployments[id]; ok {
			m.enqueueRolloutOutcomeWebhooksLocked(before, current)
			if before.Status != DeployLive {
				m.enqueueDeploymentLifecycleWebhooksLocked(current)
			}
		}
	}
	return release, true, nil
}

func (m *MemStore) environmentGitOpsReleaseMembersLocked(source EnvironmentGitSource, graph EnvironmentWorkloadGraph, activeID string) ([]ProjectReleaseMember, []ProjectReleaseMember, int, error) {
	activeTargets := map[string]string{}
	if activeID != "" {
		active, ok := m.projectReleaseSets[activeID]
		if !ok || !active.Active || active.ProjectID != source.ProjectID ||
			normalizedDeploymentScope(active.EnvironmentSlug) != normalizedDeploymentScope(source.EnvironmentSlug) {
			return nil, nil, 0, ErrConflict
		}
		for _, member := range active.Members {
			if _, duplicate := activeTargets[member.AppID]; duplicate {
				return nil, nil, 0, ErrConflict
			}
			activeTargets[member.AppID] = member.DeploymentID
		}
	}
	candidateTargets := map[string]string{}
	for _, member := range graph.Members {
		if member.CandidateDeploymentID == "" {
			continue
		}
		if _, duplicate := candidateTargets[member.AppID]; duplicate {
			return nil, nil, 0, ErrConflict
		}
		candidateTargets[member.AppID] = member.CandidateDeploymentID
	}
	appIDs := make([]string, 0)
	for appID, app := range m.apps {
		if app.AccountID == source.AccountID && app.ProjectID == source.ProjectID && app.Status != AppDeleted && app.PreviewOfSlug == "" {
			appIDs = append(appIDs, appID)
		}
	}
	slices.Sort(appIDs)
	if len(appIDs) == 0 || len(appIDs) > api.ProjectReleaseSetMaxMembers {
		return nil, nil, 0, ErrConflict
	}
	minimumTTL := int(^uint(0) >> 1)
	members := make([]ProjectReleaseMember, 0, len(appIDs))
	fallback := make([]ProjectReleaseMember, 0, len(appIDs))
	for _, appID := range appIDs {
		app := m.apps[appID]
		if app.Manifest.RevisionPinTTLSeconds <= 0 {
			return nil, nil, 0, ErrConflict
		}
		minimumTTL = min(minimumTTL, app.Manifest.RevisionPinTTLSeconds)
		target := candidateTargets[appID]
		if target == "" {
			target = activeTargets[appID]
		}
		if target == "" && activeID == "" {
			live := []Deployment{}
			for _, deployment := range m.deployments {
				if deployment.AppID == appID && normalizedDeploymentScope(deployment.Scope) == normalizedDeploymentScope(source.EnvironmentSlug) &&
					deployment.Status == DeployLive && deployment.TrafficPercent > 0 {
					live = append(live, deployment)
				}
			}
			if len(live) > 1 || len(live) == 1 && live[0].TrafficPercent != 100 {
				return nil, nil, 0, ErrConflict
			}
			if len(live) == 1 {
				target = live[0].ID
				fallback = append(fallback, ProjectReleaseMember{AppID: appID, DeploymentID: target})
			} else {
				eligible := []Deployment{}
				for _, deployment := range m.deployments {
					if deployment.AppID == appID && normalizedDeploymentScope(deployment.Scope) == normalizedDeploymentScope(source.EnvironmentSlug) &&
						deployment.Status == DeployLive && !deployment.EnvironmentWorkloadHeld() &&
						(deployment.TrafficPercentExplicit || m.validRetainedRevisionLocked(deployment.ID) || m.deploymentInUsableReleaseLocked(deployment.ID)) {
						eligible = append(eligible, deployment)
					}
				}
				sort.Slice(eligible, func(i, j int) bool { return eligible[i].Revision > eligible[j].Revision })
				if len(eligible) == 0 {
					return nil, nil, 0, ErrConflict
				}
				target = eligible[0].ID
			}
		}
		if target == "" {
			return nil, nil, 0, ErrConflict
		}
		members = append(members, ProjectReleaseMember{AppID: appID, DeploymentID: target})
	}
	if minimumTTL <= 0 || minimumTTL > api.RevisionPinMaxTTLSeconds {
		return nil, nil, 0, ErrConflict
	}
	if activeID == "" {
		fallbackAppIDs := make([]string, 0, len(appIDs))
		for _, appID := range appIDs {
			if candidateTargets[appID] == "" {
				fallbackAppIDs = append(fallbackAppIDs, appID)
			}
		}
		if err := m.validateProjectReleaseFallbackForAppsLocked(fallbackAppIDs, source.EnvironmentSlug, fallback); err != nil {
			return nil, nil, 0, err
		}
	}
	return members, fallback, minimumTTL, nil
}
