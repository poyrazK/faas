package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectPinnedWorkerStore reports retained generations with outstanding owned
// work. A read failure cannot authorize draining a resident or inventing demand.
type ProjectPinnedWorkerStore interface {
	PinnedProjectReleaseWorkers(context.Context, string) ([]string, error)
}

func (s *PgStore) PinnedProjectReleaseWorkers(ctx context.Context, appID string) ([]string, error) {
	id, err := operationUUID(appID)
	if err != nil {
		return nil, err
	}
	jobs, err := sqlc.New().PinnedProjectReleaseWorkers(ctx, s.pool, id)
	if err != nil {
		return nil, err
	}
	cohorts, err := sqlc.New().ListProjectCanaryWorkerTargets(ctx, s.pool, id)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, d := range append(jobs, cohorts...) {
		set[d] = true
	}
	ids := make([]string, 0, len(set))
	for d := range set {
		ids = append(ids, d)
	}
	sort.Strings(ids)
	return ids, nil
}

func (m *MemStore) PinnedProjectReleaseWorkers(_ context.Context, appID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[appID]; !ok {
		return nil, ErrNotFound
	}
	return m.pinnedProjectReleaseWorkersLocked(appID, time.Now()), nil
}

func (m *MemStore) pinnedProjectReleaseWorkersLocked(appID string, now time.Time) []string {
	app := m.apps[appID]
	if app.Status != AppActive || app.ProjectID == "" {
		return nil
	}
	targets := map[string]struct{}{}
	for _, id := range m.canaryWorkerTargetsLocked(appID) {
		targets[id] = struct{}{}
	}
	liveRoots := map[string]bool{}
	for _, inv := range m.invocations {
		if inv.AccountID != app.AccountID || inv.AppID != appID || inv.DeploymentScope != "production" || inv.EnvironmentID != "" || (inv.State != InvocationPending && inv.State != InvocationDispatching) {
			continue
		}
		rootID := inv.ReplayRootInvocationID
		if rootID == "" {
			rootID = inv.ID
		}
		liveRoots[rootID+"\x00"+inv.QueueBindingID] = true
	}
	for rootID, pin := range m.workerReleasePins {
		if pin.AppID != appID {
			continue
		}
		root, rootOK := m.invocations[rootID]
		binding, bindingOK := m.queueBindings[pin.BindingID]
		dep, depOK := m.deployments[pin.DeploymentID]
		graph, graphOK := m.projectReleaseSets[pin.ReleaseID]
		if !rootOK || root.AppID != appID || root.AccountID != app.AccountID || root.QueueBindingID != pin.BindingID || !bindingOK || binding.AppID != appID || binding.AccountID != app.AccountID || binding.DeploymentScope != "production" || binding.Mode != "push" || binding.WorkloadClass != WorkloadClassWorker || !binding.Enabled || binding.RetiredAt != nil || !depOK || dep.AppID != appID || dep.Status != DeployLive || dep.Scope != "production" || !graphOK || graph.ProjectID != app.ProjectID || graph.AccountID != app.AccountID || graph.EnvironmentSlug != "production" || !releasePubliclyUsable(graph, now) || releaseMemberForApp(graph, appID) != dep.ID {
			continue
		}
		if liveRoots[rootID+"\x00"+pin.BindingID] {
			targets[dep.ID] = struct{}{}
		}
	}
	ids := make([]string, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *MemStore) pinnedProjectWorkerLocked(appID, deploymentID string) bool {
	for _, id := range m.pinnedProjectReleaseWorkersLocked(appID, time.Now()) {
		if id == deploymentID {
			return true
		}
	}
	return false
}
