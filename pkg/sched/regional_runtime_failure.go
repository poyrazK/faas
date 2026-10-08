package sched

// adr: 634
import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state"
)

// FailOriginalRegionalRuntime uses a retained source and never generic Destroy.
// Uncertain native stop leaves the row and local reservation resident. A first
// durable cell fence survives missing configuration, RPC loss and process exit.
func (e *Engine) FailOriginalRegionalRuntime(ctx context.Context, source runtimequota.LaunchSource, reason string) error {
	if source.Validate() != nil {
		return runtimequota.ErrBinding
	}
	p := source.Permit
	retained, err := e.store.RegionalRuntimeLaunchSource(ctx, p.Grant.Binding.RegionalAppID, p.ID)
	if err != nil {
		return fmt.Errorf("sched: retained failed launch source: %w", err)
	}
	if retained.Source != source {
		return runtimequota.ErrBinding
	}
	if ins, err := e.store.InstanceByID(ctx, source.InstanceKey); err == nil && !state.RegionalRuntimeAttemptMatches(source, ins) {
		return state.ErrConflict // A replacement run cannot authorize physical cleanup.
	} else if err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	r, err := e.store.BeginRegionalRuntimeLaunchFailure(ctx, source, uuid.NewString())
	if err != nil {
		return err
	}
	r, err = e.RetireOriginalRegionalRuntime(ctx, source.InstanceKey, p, r.FenceID)
	if err != nil {
		return err
	}
	return e.completeOriginalRegionalFailure(ctx, source, r, reason)
}

func (e *Engine) completeOriginalRegionalFailure(ctx context.Context, source runtimequota.LaunchSource, r state.RegionalRuntimeCapacityReceipt, reason string) error {
	if r.Permit != source.Permit || r.IssuedAt == nil || r.ReleasedAt == nil || r.Stop == nil || r.StopEnvelope == nil ||
		r.Stop.Validate() != nil || r.Stop.Permit != source.Permit || r.Stop.FenceID != r.FenceID {
		return runtimequota.ErrBinding
	}
	// Release only the exact physical origin under the local ledger lock. A
	// later store error cannot turn verified physical absence back into RAM.
	e.ledger.releaseOriginalRegionalRuntime(source)
	ins, err := e.store.InstanceByID(ctx, source.InstanceKey)
	if errors.Is(err, state.ErrNotFound) {
		return nil // Source survives collection; no replacement row is fabricated.
	}
	if err != nil {
		return err
	}
	if !state.RegionalRuntimeAttemptMatches(source, ins) {
		return nil // Retain a newer run, deployment or node unchanged.
	}
	to := state.RegionalRuntimeFailureState(state.State(ins.State))
	if to == "" {
		return nil
	}
	// Accepted original absence can retire an unbound startup shadow after a
	// restart. A later row-write error cannot restore physical residency.
	e.ledger.releaseStoppedRegionalStartupRow(source, ins)
	if err := e.store.FailRegionalRuntimeAttempt(ctx, source, ins.State); err != nil {
		return err
	}
	if state.State(ins.State) != to {
		e.recordCommittedInstanceTransition(ctx, ins, state.State(ins.State), to, ins.AppID, "wake_boot_error", reason)
	}
	return nil
}

// cleanupFailedRegionalLaunch is used by captured wake/prime failure callers.
// It takes over every regional error, including refusals, so callers cannot
// release by terminal state or fall back to an unbound native operation.
func (e *Engine) cleanupFailedRegionalLaunch(ctx context.Context, instance string, spec AppSpec, reason string) bool {
	if spec.RegionalRuntime == nil {
		return false
	}
	source := runtimequota.LaunchSource{Permit: spec.RegionalRuntime.Permit, InstanceKey: instance}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.RegionalRuntimeFailureCleanupTimeout)
	defer cancel()
	if err := e.FailOriginalRegionalRuntime(cleanupCtx, source, reason); err != nil {
		e.log.Warn("sched: failed regional launch remains held", "permit_id", source.Permit.ID, "err", err)
	}
	return true
}

func (e *Engine) cleanupFailedWake(ctx context.Context, input bootInput, mode, reason string, teardown bool) {
	if e.cleanupFailedRegionalLaunch(ctx, input.insID, input.spec, reason) {
		return
	}
	cleanupCtx := ctx
	if mode == string(state.InstanceModeMirror) {
		var cancel context.CancelFunc
		cleanupCtx, cancel = e.cleanupFailedMirrorAdmission(ctx, input)
		defer cancel()
	} else if teardown {
		e.bestEffortDestroy(ctx, input.nodeID, input.insID)
	}
	e.ledger.Release(input.insID)
	e.transitionWithKind(cleanupCtx, input.insID, input.appID, state.StateFailed, "wake_boot_error", reason)
}

func (e *Engine) cleanupFailedPrime(ctx context.Context, ins state.Instance, spec AppSpec, reason string, teardown bool) {
	if spec.RegionalRuntime != nil && state.InstanceMode(ins.Mode) == state.InstanceModeWorker &&
		(reason == "prime_interrupted_by_scheduler_shutdown" || reason == "prime_ready_notify_failed") {
		checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.RegionalRuntimeRetirementTimeout)
		defer cancel()
		dep, err := e.store.DeploymentByID(checkCtx, ins.DeploymentID)
		if err != nil || dep.Status == state.DeployLive {
			// A committed worker handoff belongs to normal serving recovery.
			// An unknown commit reply cannot authorize failed-prime teardown.
			return
		}
	}
	if e.cleanupFailedRegionalLaunch(ctx, ins.ID, spec, reason) {
		return
	}
	if teardown {
		e.bestEffortDestroy(ctx, ins.NodeID, ins.ID)
	}
	e.ledger.Release(ins.ID)
	e.transitionWithKind(ctx, ins.ID, ins.AppID, state.StateFailed, "wake_boot_error", reason)
}
