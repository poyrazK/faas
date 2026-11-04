// adr: 20261031193635
package sched

import (
	"context"
	"errors"
	"fmt"
	"sync"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Contained environment branches (ADR-20261031193635 section 7, slice 6a).
// schedd asks vmmd for the contained profile and destroys a runtime that
// does not acknowledge it. vmmd reads the same durable policy on its own, so
// this check catches an older vmmd, which ignores the request.

// environmentBranchEgressCacheMax bounds the uncontained-deployment cache.
const environmentBranchEgressCacheMax = 4096

// environmentBranchEgressCache remembers deployments read as outside any
// contained branch. A deployment's scope and its branch's egress never
// change, so the answer holds for the deployment's life.
type environmentBranchEgressCache struct {
	mu          sync.Mutex
	uncontained map[string]bool
}

func (c *environmentBranchEgressCache) known(deploymentID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.uncontained[deploymentID]
}

func (c *environmentBranchEgressCache) remember(deploymentID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.uncontained == nil || len(c.uncontained) >= environmentBranchEgressCacheMax {
		c.uncontained = make(map[string]bool)
	}
	c.uncontained[deploymentID] = true
}

// environmentBranchContained reports whether the deployment runs in a
// contained (or simulated) branch. A deleted branch's deployment is refused.
func (e *Engine) environmentBranchContained(ctx context.Context, deploymentID string) (bool, error) {
	store, ok := e.store.(state.EnvironmentBranchEgressStore)
	if !ok || deploymentID == "" || e.environmentBranchEgress.known(deploymentID) {
		return false, nil
	}
	policy, err := store.EnvironmentBranchEgress(ctx, deploymentID)
	switch {
	case errors.Is(err, state.ErrEnvironmentBranchGone):
		return false, fmt.Errorf("sched: boot %s: %w", deploymentID, err)
	case errors.Is(err, state.ErrNotFound):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("sched: read environment branch egress: %w", err)
	}
	if !policy.Contained() {
		e.environmentBranchEgress.remember(deploymentID)
		return false, nil
	}
	return true, nil
}

// errEnvironmentBranchContainmentUnacknowledged: the runtime did not install
// the contained profile it was asked for.
var errEnvironmentBranchContainmentUnacknowledged = errors.New("runtime did not acknowledge environment branch containment")

func (c *VMMClient) confirmEnvironmentBranchContainment(ctx context.Context, app AppSpec, instance string, resp *vmmdpb.WakeResponse) error {
	if !app.EnvironmentBranchContained || (resp.GetEnvironmentBranchContained() && resp.GetInstance() == instance && resp.GetNetns() != "") {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
	defer cancel()
	_ = c.Destroy(cleanupCtx, instance)
	return errEnvironmentBranchContainmentUnacknowledged
}
