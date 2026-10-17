package sched

// adr: 1578

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type functionMigrationRestartState interface {
	state.FunctionMigrationRestartStore
	GetFunctionMigrationDestination(context.Context, string, string) (state.FunctionMigrationDestination, error)
	InstanceRuntimeConfigReceipt(context.Context, string) (state.RuntimeConfigInputs, bool, error)
	MigrateInstanceOwnerWithRuntimeConfig(context.Context, string, string, string, string, state.RuntimeConfigMigration) error
	ResolveInstanceMigrationCommit(context.Context, state.MigrationCommitAttempt) (state.MigrationCommitResolution, error)
	CheckFunctionMigrationDestinationCommitted(context.Context, runtimeadmission.FunctionMigrationDestinationGrant) error
}

// RecoverFunctionMigrations runs once before SeedLedger and admission start.
// It reconstructs only this node's durable ready/committed destinations. No
// original AppSpec, generated wake, in-memory reservation or client reply is
// reused. Issued/stale work remains fenced for ordinary expiry recovery.
func (e *Engine) RecoverFunctionMigrations(ctx context.Context) (int, error) {
	s, ok := e.store.(functionMigrationRestartState)
	if !ok || e.ownerNodeID == "" {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, api.MigrateLiveCommitRecoveryTimeout)
	defer cancel()
	return recoverFunctionMigrations(ctx, s, e.vmm, e.ownerNodeID, e.log)
}

func recoverFunctionMigrations(ctx context.Context, s functionMigrationRestartState, vmm RoutedVMM, node string, log *slog.Logger) (int, error) {
	completed, after := 0, ""
	var failures []error
	for {
		entries, err := s.ListRestartFunctionMigrationDestinations(ctx, node, after, api.MigratingWatchdogTickLimit)
		if err != nil {
			return completed, errors.Join(append(failures, fmt.Errorf("list restart function migrations: %w", err))...)
		}
		for _, d := range entries {
			id := d.Grant.Binding.InstanceID
			if id <= after || d.Grant.Binding.NodeID != node {
				return completed, errors.Join(append(failures, state.ErrConflict)...)
			}
			after = id
			recoveryCtx, cancel := context.WithTimeout(ctx, api.MigrateLiveCommitRecoveryTimeout)
			err := recoverFunctionMigration(recoveryCtx, s, vmm, d)
			cancel()
			if err != nil {
				log.Warn("sched: restart function migration retained", "instance_id", id, "err", err)
				failures = append(failures, fmt.Errorf("restart function migration %s: %w", id, err))
				continue
			}
			completed++
		}
		if len(entries) < api.MigratingWatchdogTickLimit {
			return completed, errors.Join(failures...)
		}
	}
}

func recoverFunctionMigration(ctx context.Context, s functionMigrationRestartState, vmm RoutedVMM, listed state.FunctionMigrationDestination) error {
	g := listed.Grant
	d, err := s.GetFunctionMigrationDestination(ctx, g.Binding.InstanceID, g.Source.LeaseToken)
	if err != nil {
		return err
	}
	if !d.Grant.Equal(g) || d.Receipt == nil {
		return state.ErrMigrationCommitUnresolved
	}
	if d.Phase == state.FunctionDestinationReady {
		if err := commitRestartFunctionDestination(ctx, s, d); err != nil {
			return err
		}
	} else if d.Phase != state.FunctionDestinationCommitted {
		return state.ErrMigrationCommitUnresolved
	}
	if err := s.CheckFunctionMigrationDestinationCommitted(ctx, g); err != nil {
		return err
	}
	if err := activateRecordedFunctionDestination(ctx, s, vmm, g.Binding.InstanceID, g.Source.LeaseToken); err != nil {
		return err
	}
	return vmm.AcknowledgeMigration(ctx, g.Source.Parent.Binding.NodeID, g.Binding.InstanceID, g.Source.LeaseToken)
}

func commitRestartFunctionDestination(ctx context.Context, s functionMigrationRestartState, d state.FunctionMigrationDestination) error {
	g := d.Grant
	if g.CheckReceipt(*d.Receipt, time.Now()) != nil {
		return state.ErrApplicationStandardRuntimeStale
	}
	inputs, found, err := s.InstanceRuntimeConfigReceipt(ctx, g.Binding.InstanceID)
	if err != nil {
		return err
	}
	if !found {
		return state.ErrMigrationCommitUnresolved
	}
	err = s.MigrateInstanceOwnerWithRuntimeConfig(ctx, g.Binding.InstanceID, g.Source.Parent.Binding.NodeID, g.Binding.NodeID, g.Source.LeaseToken, state.RuntimeConfigMigration{
		ExpectedWakeID: g.Source.SourceWakeID, WakeID: g.WakeID, Inputs: &inputs,
		Netns: d.Receipt.Netns, HostIP: d.Receipt.HostIP, GuestUID: int(d.Receipt.LeaseUID),
	})
	if err == nil {
		return nil
	}
	resolution, resolveErr := s.ResolveInstanceMigrationCommit(ctx, state.MigrationCommitAttempt{
		InstanceID: g.Binding.InstanceID, SourceNodeID: g.Source.Parent.Binding.NodeID, DestinationNodeID: g.Binding.NodeID,
		SourceWakeID: g.Source.SourceWakeID, DestinationWakeID: g.WakeID, LeaseToken: g.Source.LeaseToken,
	})
	if resolveErr == nil && resolution == state.MigrationCommitRecovered {
		return nil
	}
	return errors.Join(err, resolveErr, state.ErrMigrationCommitUnresolved)
}
