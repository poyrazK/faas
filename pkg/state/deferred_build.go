package state

import (
	"context"
	"sort"
	"time"
)

// DeferredBuildStore queues builds that no claim path takes before their
// not_before (ADR-20261021221906). apid defers the convergence build of a developer
// sync that already reached the running instance as a live patch (ADR-740);
// a newer sync supersedes the queued row, so a run of saves builds once.
// It is an optional Store capability so older test doubles keep working.
type DeferredBuildStore interface {
	// CreateDeferredBuildWithID is CreateBuildWithIDAndActivity (activity may
	// be nil) with not_before written by the same insert, so a claim can
	// never take the build early.
	CreateDeferredBuildWithID(ctx context.Context, id, deploymentID string, kind DeploymentKind, sourceBytes int64, logPath string, notBefore time.Time, activity *OrgActivity) (Build, int64, error)
	// StartDeferredBuild makes the deployment's queued, deferred build
	// claimable now. found is false when there is no such build.
	StartDeferredBuild(ctx context.Context, deploymentID string) (Build, bool, error)
	// DeferredDeployments lists the app's deployments whose build is still
	// queued and deferred. A newer developer sync carries their source, so
	// apid cancels them instead of letting each one build: deployments with
	// a build already in flight are not superseded by CreateDeployment.
	DeferredDeployments(ctx context.Context, appID string) ([]string, error)
}

var (
	_ DeferredBuildStore = (*PgStore)(nil)
	_ DeferredBuildStore = (*MemStore)(nil)
)

// CreateDeferredBuildWithID mirrors PgStore.CreateDeferredBuildWithID.
func (m *MemStore) CreateDeferredBuildWithID(_ context.Context, id, deploymentID string, kind DeploymentKind, sourceBytes int64, logPath string, notBefore time.Time, activity *OrgActivity) (Build, int64, error) {
	return m.createBuildWithID(id, deploymentID, kind, sourceBytes, logPath, activity, &notBefore)
}

// StartDeferredBuild mirrors PgStore.StartDeferredBuild.
func (m *MemStore) StartDeferredBuild(_ context.Context, deploymentID string) (Build, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, b := range m.builds {
		if b.DeploymentID != deploymentID || b.Status != BuildQueued {
			continue
		}
		if _, deferred := m.buildNotBefore[id]; !deferred {
			continue
		}
		delete(m.buildNotBefore, id)
		return b, true, nil
	}
	return Build{}, false, nil
}

// DeferredDeployments mirrors PgStore.DeferredDeployments.
func (m *MemStore) DeferredDeployments(_ context.Context, appID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id := range m.buildNotBefore {
		b, ok := m.builds[id]
		if !ok || b.Status != BuildQueued {
			continue
		}
		if dep, ok := m.deployments[b.DeploymentID]; ok && dep.AppID == appID {
			ids = append(ids, dep.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// buildDueLocked reports whether a queued build may be claimed at now.
func (m *MemStore) buildDueLocked(id string, now time.Time) bool {
	notBefore, deferred := m.buildNotBefore[id]
	return !deferred || !now.Before(notBefore)
}
