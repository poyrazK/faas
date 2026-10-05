package sched

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *MigrationHarness) resolveMigrationCommit(ctx context.Context, id, from, lease string, spec AppSpec) (state.MigrationCommitResolution, error) {
	resolver, ok := h.store.(state.MigrationCommitRecoveryStore)
	if !ok {
		return state.MigrationCommitRetained, state.ErrMigrationCommitUnresolved
	}
	attempt := state.MigrationCommitAttempt{InstanceID: id, SourceNodeID: from, DestinationNodeID: h.newOwnerNodeID, LeaseToken: lease}
	if spec.migrationRuntime != nil {
		attempt.SourceWakeID, attempt.DestinationWakeID = spec.migrationRuntime.ExpectedWakeID, spec.migrationRuntime.WakeID
	}
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.MigrateLiveCommitRecoveryTimeout)
	defer cancel()
	resolution, err := resolver.ResolveInstanceMigrationCommit(recoveryCtx, attempt)
	if err != nil {
		return state.MigrationCommitRetained, errors.Join(state.ErrMigrationCommitUnresolved, err)
	}
	switch resolution {
	case state.MigrationCommitRecovered:
		return resolution, nil
	case state.MigrationCommitAborted:
		h.cleanupAdopted(ctx, id)
		h.ledger.Release(id)
		h.cancelSource(ctx, from, id, lease)
		return resolution, nil
	case state.MigrationCommitObsolete:
		h.cleanupAdopted(ctx, id)
		h.ledger.Release(id)
		// A lease-bound acknowledgement removes the paused source. A generic
		// Destroy(instanceID) could target a newer source process.
		h.acknowledgeSource(ctx, from, id, lease)
		return resolution, nil
	default:
		// Keep the destination reservation and both VMs. Ordinary reconciliation
		// and durable lease expiry can resolve ownership after the DB recovers.
		return state.MigrationCommitRetained, state.ErrMigrationCommitUnresolved
	}
}

func (h *MigrationHarness) acknowledgeSource(ctx context.Context, from, id, lease string) {
	ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.MigrateLiveCommitRecoveryTimeout)
	defer cancel()
	if err := h.vmm.AcknowledgeMigration(ackCtx, from, id, lease); err != nil {
		h.log.Debug("sched: migrate one: source acknowledgement failed; retaining lease",
			"instance_id", id, "from_node_id", from, "err", err)
	}
}

func migrationCommitFailureOutcome(err, leaseErr error) string {
	switch {
	case errors.Is(err, state.ErrConflict):
		return "conflict"
	case errors.Is(err, state.ErrNotFound):
		return ""
	case errors.Is(err, state.ErrNodeCapacity):
		return "no_headroom"
	case errors.Is(err, leaseErr), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "lease_expired"
	default:
		return "peer_failure"
	}
}
