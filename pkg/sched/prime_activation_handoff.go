package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

// An activation handoff is infrastructure work, not a failed customer boot.
// Recovery retains its instance/capture and retries without executing again.
var errPrimeActivationPending = errors.New("sched: prime activation handoff remains recoverable")

func primeActivationPending(err error) error {
	return errors.Join(errPrimeActivationPending, err)
}

// Caller holds the app and prime locks. Only the exact preparation can reuse
// a wake; old rollback captures and changed runtime inputs remain retryable.
func (e *Engine) replayPrimeActivation(ctx context.Context, app state.App, dep state.Deployment) (bool, error) {
	store, ok := e.store.(state.SnapshotPrimeHandoffStore)
	if !ok || dep.Status != state.DeploySnapshotting || len(dep.StageState) == 0 {
		return false, nil
	}
	var stage state.StageState
	if err := json.Unmarshal(dep.StageState, &stage); err != nil {
		return false, err
	}
	if stage.Current == state.StageReadiness {
		return true, nil
	}
	if stage.Current == "" {
		for _, done := range stage.History {
			if done.Name == state.StageReadiness && done.EndedAt != nil {
				return true, nil
			}
		}
	}
	if stage.Current != state.StageSnapshotPrepare || stage.CurrentStartedAt == nil {
		return false, nil
	}
	instances, err := store.ListSnapshotPrimeInstances(ctx, app.ID, dep.ID, *stage.CurrentStartedAt)
	if err != nil {
		return true, primeActivationPending(err)
	}
	if len(instances) == 0 {
		return false, nil
	}
	values, err := e.store.RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, dep.ID)
	if err != nil {
		return true, primeActivationPending(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(values)
	if err != nil {
		return true, primeActivationPending(err)
	}
	changedAt, changed, err := state.RuntimeConfigChangedAtForScope(ctx, e.store, app.ID, dep.Scope)
	if err != nil {
		return true, primeActivationPending(err)
	}
	if !changed {
		changedAt = time.Unix(0, 0).UTC()
	}
	for _, ins := range instances {
		if state.State(ins.State).CountsForRAM() && ins.State != string(state.StateRunning) {
			return true, nil // An unfinished boot/capture belongs to its lifecycle owner.
		}
		proof, err := e.store.(state.RuntimeInstancePublicationStore).InstanceRuntimeConfigFence(ctx, app.AccountID, app.ID, ins.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return true, primeActivationPending(err)
		}
		fresh := err == nil && proof == fence
		if fresh {
			stale, err := e.runtimeConfigReceiptStaleness(ctx, ins, changedAt, dep.Scope)
			if err != nil {
				return true, primeActivationPending(err)
			}
			fresh = !stale
		}
		if ins.State == string(state.StateRunning) && ins.Mode == string(state.InstanceModeWorker) {
			if fresh && executionModeForApp(app) == api.ExecutionModeWorker {
				return true, e.notifyPrimeActivation(ctx, func() error { return e.emitDeploymentReady(ctx, dep.ID, api.ExecutionModeWorker, ins.ID, "") })
			}
			// The recovery sweeper owns stale-worker withdrawal and teardown.
			return true, nil
		}
		if ins.State == string(state.StateRunning) {
			return true, e.resumePrimeCapture(ctx, app, ins, fresh)
		}
		if !fresh {
			continue
		}
		handoff, err := store.SnapshotPrimeHandoff(ctx, app.AccountID, app.ID, ins.ID)
		if errors.Is(err, state.ErrNotFound) {
			continue
		} // A pre-upgrade capture has no recoverable message.
		if err != nil {
			return true, primeActivationPending(err)
		}
		if handoff.Fence != fence {
			continue
		}
		if ins.State == string(state.StateParked) || ins.State == string(state.StateStopped) {
			// Also repairs a lost acknowledgment of the atomic completion write.
			e.ledger.Release(ins.ID)
			return true, e.sendPrimeHandoff(ctx, dep.ID, handoff.Channel, handoff.Payload)
		}
	}
	return false, nil
}

// The app and deployment-prime locks prove that no Prime still owns this
// RUNNING guest. Its boot committed before a previous scheduler died; resume
// capture instead of waiting for a lifecycle owner that no longer exists.
func (e *Engine) resumePrimeCapture(ctx context.Context, app state.App, ins state.Instance, fresh bool) error {
	if (ins.Mode != string(state.InstanceModeNormal) && ins.Mode != string(state.InstanceModeService)) || ins.Mode != instanceModeForApp(app) {
		return primeActivationPending(errors.New("running prime mode does not match deployment"))
	}
	if !fresh && !e.runtimeConfigStale(ctx, ins) {
		return primeActivationPending(errors.New("running prime has no current runtime proof"))
	}
	if err := state.CheckSnapshotPrimeAttempt(ctx, e.store); err != nil {
		return err
	}
	// A crash can also leave the capture barrier committed while the guest
	// is still RUNNING. Both locks exclude the previous capture owner. Clear
	// only that barrier; BeginExclusiveSnapshot still checks active exclusive
	// operation leases and will refuse capture while one owns the guest.
	if barrier, ok := e.store.(state.ExclusiveSnapshotStore); ok {
		if err := barrier.EndExclusiveSnapshot(ctx, ins.ID); err != nil {
			return primeActivationPending(err)
		}
	}
	// The existing park path retires a guest with changed runtime inputs or
	// RAM before capture, and fences the completed handoff to this attempt.
	// A busy exclusive operation retains its guest until it releases ownership.
	// Capture failures keep Prime's normal failure classification; queue and
	// ambiguous completion failures already carry errPrimeActivationPending.
	if err := e.snapshotAndParkPrime(ctx, ins); err != nil {
		if errors.Is(err, exclusivework.ErrBusy) {
			return primeActivationPending(err)
		}
		return err
	}
	return nil
}

func (e *Engine) notifyPrimeActivation(ctx context.Context, send func() error) error {
	if e.notif == nil {
		return primeActivationPending(errors.New("activation notifier unavailable"))
	}
	if err := send(); err != nil {
		return primeActivationPending(err)
	}
	return nil
}

func (e *Engine) sendPrimeHandoff(ctx context.Context, deployment, channel string, payload []byte) error {
	if err := e.notifyPrimeActivation(ctx, func() error { return e.notif.Notify(ctx, channel, string(payload)) }); err != nil {
		return err
	}
	if channel == db.NotifySnapshotWritten {
		if err := e.ClearSnapshotBackoff(ctx, deployment); err != nil {
			e.log.Warn("prime handoff: clear snapshot backoff", "deployment", deployment, "err", err)
		}
	}
	return nil
}

func (e *Engine) completePrimeHandoff(ctx context.Context, ins state.Instance, next state.State, channel string, payload []byte) error {
	store, ok := e.store.(state.SnapshotPrimeHandoffStore)
	if !ok {
		return primeActivationPending(errors.New("atomic prime handoff store unavailable"))
	}
	if err := store.CompleteSnapshotPrimeHandoff(ctx, ins, string(next), channel, payload); err != nil {
		return primeActivationPending(fmt.Errorf("record prime activation handoff: %w", err))
	}
	e.ledger.Release(ins.ID)
	e.releaseHostPortLeases(ctx, ins.NodeID, ins.ID)
	kind, reason := "state_transition", ""
	if next == state.StateStopped {
		kind, reason = "park_ephemeral_secret", "ephemeral_secret"
	}
	e.recordCommittedInstanceTransition(ctx, ins, state.State(ins.State), next, ins.AppID, kind, reason)
	return nil
}
