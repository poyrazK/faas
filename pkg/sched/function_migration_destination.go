package sched

// adr: 1352. Resolve ambiguity before either physical owner can serve again.

import (
	"context"
	"errors"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func functionDestinationCleanupConfirmed(ctx context.Context, store state.Store, id, lease string) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.MigrateLiveCommitRecoveryTimeout)
	defer cancel()
	s, ok := store.(state.FunctionMigrationDestinationStore)
	if !ok {
		return false
	}
	d, err := s.GetFunctionMigrationDestination(ctx, id, lease)
	return errors.Is(err, state.ErrNotFound) || err == nil && d.Phase == state.FunctionDestinationRetired
}
func activateCommittedFunctionDestination(ctx context.Context, store state.Store, vmm RoutedVMM, id, lease string) error {
	s, ok := store.(state.FunctionMigrationDestinationStore)
	if !ok {
		return state.ErrMigrationCommitUnresolved
	}
	return activateRecordedFunctionDestination(ctx, s, vmm, id, lease)
}

func activateRecordedFunctionDestination(ctx context.Context, s interface {
	GetFunctionMigrationDestination(context.Context, string, string) (state.FunctionMigrationDestination, error)
}, vmm RoutedVMM, id, lease string) error {
	d, err := s.GetFunctionMigrationDestination(ctx, id, lease)
	if err != nil {
		return err
	}
	if d.Phase != state.FunctionDestinationCommitted || d.Receipt == nil {
		return state.ErrMigrationCommitUnresolved
	}
	spec := AppSpec{AppID: d.Grant.Binding.AppID, AccountID: d.Grant.Binding.AccountID, DeploymentID: d.Grant.Binding.DeploymentID, FCVersion: d.Grant.Restore.FCVersion,
		migrationRuntime: &migrationRuntimeInputs{ExpectedWakeID: d.Grant.Source.SourceWakeID, WakeID: d.Grant.WakeID}}
	adopted, err := vmm.AdoptMigratedInstance(ctx, d.Grant.Binding.NodeID, id, spec, d.Grant.Source.MemoryKey, d.Grant.Source.VMStateKey, lease)
	if err != nil {
		return err
	}
	if adopted.WakeID != d.Grant.WakeID || adopted.Method != vmmdpb.WakeMethod_WAKE_RESTORE || adopted.Netns != d.Receipt.Netns || adopted.HostIP != d.Receipt.HostIP || adopted.GuestUID != int(d.Receipt.LeaseUID) {
		return state.ErrMigrationCommitUnresolved
	}
	return nil
}
