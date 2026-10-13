package sched

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// DrainManagedPostgresCutoverInstance is internal scheduler plumbing. It needs
// an already installed fence and explicit instance selection. Prepare/Verify
// do not invoke it; a member receipt grants no cutover activation authority.
func (e *Engine) DrainManagedPostgresCutoverInstance(ctx context.Context, cutoverID, instanceID string) (state.ManagedPostgresDrain, error) {
	store, ok := e.store.(state.ManagedPostgresDrainStore)
	if !ok {
		return state.ManagedPostgresDrain{}, state.ErrConflict
	}
	if _, ok := e.vmm.(ManagedPostgresDrainVMM); !ok {
		return state.ManagedPostgresDrain{}, state.ErrConflict
	}
	ctx, cancel := context.WithTimeout(ctx, api.ManagedPostgresCutoverDrainTimeout)
	defer cancel()
	owner := e.ownerNodeID
	if owner == "" {
		owner = e.defaultLocalNodeID
	}
	drain, err := store.ReserveManagedPostgresDrain(ctx, cutoverID, instanceID, owner)
	if err != nil {
		return drain, err
	}
	if err := e.retireManagedPostgresDrain(ctx, store, drain); err != nil {
		return drain, err
	}
	return store.ManagedPostgresDrain(ctx, drain.Frame.ID)
}

func (e *Engine) retireManagedPostgresDrain(ctx context.Context, store state.ManagedPostgresDrainStore, drain state.ManagedPostgresDrain) error {
	f := drain.Frame
	owner := e.ownerNodeID
	if owner == "" {
		owner = e.defaultLocalNodeID
	}
	if !f.Valid() || f.OwnerNodeID != owner {
		return state.ErrConflict
	}
	release, err := e.lockQualificationApp(ctx, f.AppID)
	if err != nil {
		return err
	}
	defer release()
	ins, err := e.store.InstanceByID(ctx, f.InstanceID)
	if err != nil {
		return err
	}
	if ins.AppID != f.AppID || ins.DeploymentID != f.DeploymentID || ins.NodeID != f.NodeID || ins.WakeID != f.WakeID {
		return state.ErrConflict
	}
	if drain.Proof == nil {
		vmm, ok := e.vmm.(ManagedPostgresDrainVMM)
		if !ok {
			return state.ErrConflict
		}
		evidence, err := vmm.RetireManagedPostgresDrain(ctx, f)
		if err != nil {
			return err
		}
		if !evidence.Frame.Equal(f) || !evidence.Proof.ValidFor(f) {
			return state.ErrConflict
		}
		if err := store.CompleteManagedPostgresDrain(ctx, f, evidence.Proof); err != nil {
			return err
		}
		if ins.State != string(state.StateStopped) {
			e.recordCommittedInstanceTransition(ctx, ins, state.State(ins.State), state.StateStopped, f.AppID, "state_transition", "managed_postgres_cutover_retired")
		}
	} else if err := store.CompleteManagedPostgresDrain(ctx, f, *drain.Proof); err != nil {
		return err
	}
	// Host-port release belongs to the store's locked receipt transaction.
	// Releasing again after commit could erase a replacement incarnation's lease.
	e.ledger.Release(f.InstanceID)
	return nil
}

type ManagedPostgresDrainRecoveryPage struct {
	Examined, Retired int
	NextCursor        string
}

// Retry only durable, explicitly selected members. Advancing past a failed
// member prevents an unreachable node starving its neighbors; the next pass
// retries it. A restart never infers receipts from terminal rows or inventory.
func (e *Engine) RecoverManagedPostgresDrains(ctx context.Context, ownerNodeID, afterID string, limit int) (page ManagedPostgresDrainRecoveryPage, result error) {
	store, ok := e.store.(state.ManagedPostgresDrainStore)
	if !ok {
		return page, state.ErrConflict
	}
	owner := e.ownerNodeID
	if owner == "" {
		owner = e.defaultLocalNodeID
	}
	if ownerNodeID != owner {
		return page, state.ErrConflict
	}
	ctx, cancel := context.WithTimeout(ctx, api.ManagedPostgresCutoverDrainTimeout)
	defer cancel()
	rows, err := store.ListPendingManagedPostgresDrains(ctx, ownerNodeID, afterID, limit)
	if err != nil {
		return page, err
	}
	page.NextCursor = afterID
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return page, errors.Join(result, err)
		}
		page.Examined++
		page.NextCursor = row.Frame.ID
		if err := e.retireManagedPostgresDrain(ctx, store, row); err != nil {
			result = errors.Join(result, fmt.Errorf("cutover drain %s: %w", row.Frame.ID, err))
			continue
		}
		page.Retired++
	}
	if len(rows) < limit {
		page.NextCursor = ""
	}
	return page, result
}
