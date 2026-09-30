package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// EnvironmentRuntimeFreshnessStore distinguishes scoped variables from
// changes to shared credentials. Its result includes the shared stamp and
// default-scope overlays, so those still invalidate every affected guest.
type EnvironmentRuntimeFreshnessStore interface {
	AppRuntimeConfigChangedAtInScope(context.Context, string, string) (time.Time, bool, error)
}

type EnvironmentRuntimeInvalidationStore interface {
	InvalidateAppSnapshotsInScope(context.Context, string, string) (int, error)
}

// A default-scope value overlays every environment and retains the existing
// application-wide boundary. Named environments invalidate only their cache.
func InvalidateAppSnapshotsInScope(ctx context.Context, store Store, appID, scope string) (int, error) {
	scope = normalizedDeploymentScope(scope)
	if api.ValidateScope(scope) != nil {
		return 0, ErrInvalidArgument
	}
	if scope == "default" {
		return InvalidateAppSnapshots(ctx, store, appID)
	}
	if scoped, ok := store.(EnvironmentRuntimeInvalidationStore); ok {
		return scoped.InvalidateAppSnapshotsInScope(ctx, appID, scope)
	}
	return InvalidateAppSnapshots(ctx, store, appID)
}

func RuntimeConfigChangedAtForScope(ctx context.Context, store Store, appID, scope string) (time.Time, bool, error) {
	if scoped, ok := store.(EnvironmentRuntimeFreshnessStore); ok {
		return scoped.AppRuntimeConfigChangedAtInScope(ctx, appID, normalizedDeploymentScope(scope))
	}
	return store.AppRuntimeConfigChangedAt(ctx, appID)
}

type environmentRuntimeKey struct{ AppID, Scope string }

func (m *MemStore) environmentRuntimeChangedAtLocked(appID, scope string) (time.Time, bool) {
	boundary, exists := m.runtimeConfigChangedAt[appID]
	for _, candidate := range []string{"default", normalizedDeploymentScope(scope)} {
		if at, present := m.environmentRuntimeConfigChangedAt[environmentRuntimeKey{AppID: appID, Scope: candidate}]; present {
			exists = true
			if at.After(boundary) {
				boundary = at
			}
		}
	}
	return boundary, exists
}

func (m *MemStore) AppRuntimeConfigChangedAtInScope(_ context.Context, appID, scope string) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	at, exists := m.environmentRuntimeChangedAtLocked(appID, scope)
	return at, exists, nil
}

func (m *MemStore) markEnvironmentRuntimeChangedAndSnapshotsLocked(appID, scope string, at time.Time) int {
	scope = normalizedDeploymentScope(scope)
	if m.environmentRuntimeConfigChangedAt == nil {
		m.environmentRuntimeConfigChangedAt = map[environmentRuntimeKey]time.Time{}
	}
	key := environmentRuntimeKey{AppID: appID, Scope: scope}
	if prior := m.environmentRuntimeConfigChangedAt[key]; prior.After(at) {
		at = prior
	}
	m.environmentRuntimeConfigChangedAt[key] = at
	invalidated := 0
	for id, snapshot := range m.snapshots {
		deployment := m.deployments[snapshot.DeploymentID]
		if deployment.AppID == appID && normalizedDeploymentScope(deployment.Scope) == scope && !snapshot.Stale && !snapshot.CreatedAt.After(at) {
			snapshot.Stale = true
			m.snapshots[id] = snapshot
			invalidated++
		}
	}
	return invalidated
}

func (m *MemStore) InvalidateAppSnapshotsInScope(ctx context.Context, appID, scope string) (int, error) {
	scope = normalizedDeploymentScope(scope)
	if api.ValidateScope(scope) != nil {
		return 0, ErrInvalidArgument
	}
	if scope == "default" {
		return InvalidateAppSnapshots(ctx, m, appID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.apps[appID]; !exists {
		return 0, ErrNotFound
	}
	return m.markEnvironmentRuntimeChangedAndSnapshotsLocked(appID, scope, time.Now()), nil
}
