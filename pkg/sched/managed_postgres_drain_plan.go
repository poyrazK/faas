package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// PlanManagedPostgresCutoverDrain explicitly queues every supported member of
// an already fenced app in one durable transaction. Unsupported members remain
// in the plan as blockers. The existing recovery worker performs native stops;
// reservation itself never invokes a VM or publishes customer credentials.
func (e *Engine) PlanManagedPostgresCutoverDrain(ctx context.Context, cutoverID string) (state.ManagedPostgresDrainPlan, error) {
	store, ok := e.store.(state.ManagedPostgresDrainPlanStore)
	if !ok {
		return state.ManagedPostgresDrainPlan{}, state.ErrConflict
	}
	if _, ok := e.store.(state.ManagedPostgresDrainStore); !ok {
		return state.ManagedPostgresDrainPlan{}, state.ErrConflict
	}
	if _, ok := e.vmm.(ManagedPostgresDrainVMM); !ok {
		return state.ManagedPostgresDrainPlan{}, state.ErrConflict
	}
	ctx, cancel := context.WithTimeout(ctx, api.ManagedPostgresCutoverDrainTimeout)
	defer cancel()
	owner := e.ownerNodeID
	if owner == "" {
		owner = e.defaultLocalNodeID
	}
	return store.ReserveManagedPostgresDrainPlan(ctx, cutoverID, owner)
}

// This is an inspection surface for internal controllers. Catalog retirement
// alone cannot authorize a database cutover or external SQL-session drain.
func (e *Engine) ManagedPostgresCutoverDrainPlan(ctx context.Context, id string) (state.ManagedPostgresDrainPlan, error) {
	store, ok := e.store.(state.ManagedPostgresDrainPlanStore)
	if !ok {
		return state.ManagedPostgresDrainPlan{}, state.ErrConflict
	}
	return store.ManagedPostgresDrainPlan(ctx, id)
}
