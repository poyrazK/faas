package sched

// Managed caches keep their memory across park (ADR-20261116192126).
//
// Every other app's snapshot is reusable deployment cache: park reuses a
// healthy init capture, or retires a VM that served requests and asks for a
// clean recapture (ADR-20261115113000), so no request state reaches a later
// wake. A managed Valkey cache is the opposite case: its memory IS the
// customer's data, and a wake that restores the deployment's clean init
// capture hands back an empty cache. Native run redis-20261118T060901 showed
// exactly that: SET, park, wake from the primed init capture, GET nil.
//
// So a cache's park always captures the running VM. imaged publishes that
// capture in place of the deployment's live row (isManagedCacheDeployment),
// and the next wake restores it. Nothing here applies to any other app.

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// isManagedCacheHandoff reports whether deploymentID is a managed cache's
// resize/upgrade handoff deployment. Its prime VM holds the data it copied
// from the running cache, and its prime capture is the only way that data
// reaches a later wake: a cold boot of a handoff deployment runs the copy
// again, from the cache's own address, which after cutover is itself, so it
// never opens 6379 (native soak redissoak-20261121T120457). Such a prime
// must not go live without its snapshot; failing it aborts the rollout and
// the running VM keeps serving with its data (ADR-20261116192126: a failed
// handoff keeps the old VM). A lookup error keeps the ordinary rule.
func (e *Engine) isManagedCacheHandoff(ctx context.Context, deploymentID string) bool {
	if deploymentID == "" {
		return false
	}
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return false
	}
	return runsManagedValkey(dep) && api.IsManagedValkeyHandoffCommand(dep.OverrideEntrypoint)
}

// keepsMemoryAtPark reports whether app is a managed cache, whose park must
// capture its memory instead of reusing or discarding it. A lookup error
// other than not-found keeps the cache rule: capturing an ordinary app once
// is cheaper than losing a cache's data.
func (e *Engine) keepsMemoryAtPark(ctx context.Context, appID string) bool {
	store, ok := e.store.(state.ManagedValkeyStore)
	if !ok || appID == "" {
		return false
	}
	_, err := store.GetManagedValkeyCache(ctx, appID)
	if errors.Is(err, state.ErrNotFound) {
		return false
	}
	if err != nil {
		e.log.Warn("sched: managed cache lookup failed; capturing at park", "app", appID, "err", err)
	}
	return true
}
