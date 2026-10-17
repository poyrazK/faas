package sched

// adr: 1575. schedd owns the rollback fence and final instance transition.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type functionMigrationCanceller interface {
	CancelFunctionMigrationSource(context.Context, string, string, string) (bool, error)
}

// A qualified source never falls back to the legacy cancel, which cannot
// distinguish fresh pool recovery from confirmed retirement.
func rollbackFunctionMigrationSource(ctx context.Context, store state.Store, vmm RoutedVMM, id, token string) (handled, retired bool, retErr error) {
	sources, ok := store.(state.FunctionMigrationSourceStore)
	if !ok {
		return false, false, nil
	}
	source, err := sources.GetFunctionMigrationSource(ctx, id, token)
	if errors.Is(err, state.ErrNotFound) {
		return false, false, nil
	}
	if err != nil {
		return true, false, err
	}
	client, ok := vmm.(functionMigrationCanceller)
	if !ok {
		return true, false, state.ErrMigrationCommitUnresolved
	}
	if source.Phase == state.FunctionMigrationRecovered || source.Phase == state.FunctionMigrationRetired {
		_, err := client.CancelFunctionMigrationSource(ctx, source.SourceNodeID, id, token)
		return true, source.Phase == state.FunctionMigrationRetired, err
	}
	resolution, err := sources.FenceFunctionMigrationSource(ctx, id, token)
	if err != nil {
		return true, false, err
	}
	if resolution == state.MigrationCommitObsolete {
		if destinations, ok := store.(state.FunctionMigrationDestinationStore); ok {
			d, err := destinations.GetFunctionMigrationDestination(ctx, id, token)
			if err == nil && d.Phase == state.FunctionDestinationCommitted {
				if err := activateCommittedFunctionDestination(ctx, store, vmm, id, token); err != nil {
					return true, false, err
				}
			} else if err == nil && d.Phase != state.FunctionDestinationRetired {
				if err := retireFunctionMigrationDestination(ctx, store, vmm, id, token); err != nil {
					return true, false, err
				}
			} else if err != nil && !errors.Is(err, state.ErrNotFound) {
				return true, false, err
			}
		}
		// The owner already moved or became terminal. Only the lease-bound
		// source acknowledgement may retire that original physical process.
		err := vmm.AcknowledgeMigration(ctx, source.SourceNodeID, id, token)
		return true, err == nil, err
	}
	if resolution != state.MigrationCommitAborted {
		return true, false, state.ErrMigrationCommitUnresolved
	}
	if err := retireFunctionMigrationDestination(ctx, store, vmm, id, token); err != nil {
		return true, false, err
	}
	source, err = sources.GetFunctionMigrationSource(ctx, id, token)
	if err != nil {
		return true, false, err
	}
	if source.Phase == state.FunctionMigrationRecovered || source.Phase == state.FunctionMigrationRetired {
		_, err := client.CancelFunctionMigrationSource(ctx, source.SourceNodeID, id, token)
		return true, source.Phase == state.FunctionMigrationRetired, err
	}
	retired, err = client.CancelFunctionMigrationSource(ctx, source.SourceNodeID, id, token)
	if err != nil {
		return true, false, fmt.Errorf("function migration source recovery: %w", err)
	}
	if err := sources.CompleteFunctionMigrationSource(ctx, id, token, retired); err != nil {
		return true, false, fmt.Errorf("function migration ownership completion: %w", err)
	}
	// The physical result and its ownership commit are now confirmed. Object
	// cleanup failure retains the durable retry record, not serving capacity.
	_, err = client.CancelFunctionMigrationSource(ctx, source.SourceNodeID, id, token)
	return true, retired, err
}

func retireFunctionMigrationDestination(ctx context.Context, store state.Store, vmm RoutedVMM, id, token string) error {
	destinations, ok := store.(state.FunctionMigrationDestinationStore)
	if !ok {
		return nil
	}
	d, err := destinations.GetFunctionMigrationDestination(ctx, id, token)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if d.Phase == state.FunctionDestinationRetired {
		return nil
	}
	if d.Phase == state.FunctionDestinationCommitted {
		return state.ErrMigrationCommitUnresolved
	}
	if err := vmm.CancelLiveMigration(ctx, d.Grant.Binding.NodeID, id, token); err != nil {
		return err
	}
	d, err = destinations.GetFunctionMigrationDestination(ctx, id, token)
	if err != nil {
		return err
	}
	if d.Phase != state.FunctionDestinationRetired {
		return state.ErrMigrationCommitUnresolved
	}
	return nil
}

func (e *Engine) reconcileExpiredFunctionMigrationSources(ctx context.Context, limit int) (attempted, completed int, retErr error) {
	sources, ok := e.store.(state.FunctionMigrationSourceStore)
	if !ok {
		return 0, 0, nil
	}
	entries, err := sources.ListExpiredFunctionMigrationSources(ctx, time.Now().UTC(), limit)
	if err != nil {
		return 0, 0, err
	}
	for _, source := range entries {
		attempted++
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.MigrateLiveCommitRecoveryTimeout)
		_, retired, err := rollbackFunctionMigrationSource(recoveryCtx, e.store, e.vmm, source.InstanceID, source.LeaseToken)
		cancel()
		if d, ok := e.store.(state.FunctionMigrationDestinationStore); ok {
			if destination, readErr := d.GetFunctionMigrationDestination(ctx, source.InstanceID, source.LeaseToken); readErr == nil && destination.Phase == state.FunctionDestinationRetired && destination.Grant.Binding.NodeID == e.ownerNodeID {
				e.ledger.Release(source.InstanceID)
			}
		}
		if err != nil {
			e.log.Warn("sched: function migration source recovery retained", "instance_id", source.InstanceID, "err", err)
			continue
		}
		if retired && source.SourceNodeID == e.ownerNodeID {
			e.ledger.Release(source.InstanceID)
		}
		completed++
	}
	return attempted, completed, nil
}

// Exclude qualification records from the legacy watchdog even while a
// recovery or durable completion is unavailable. It cannot publish RUNNING
// before pool probes, or release capacity before physical retirement.
func functionMigrationSourceTracked(ctx context.Context, store state.Store, ins state.Instance) (bool, error) {
	sources, ok := store.(state.FunctionMigrationSourceStore)
	if !ok {
		return false, nil
	}
	_, err := sources.GetFunctionMigrationSourceByInstance(ctx, ins.ID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}
