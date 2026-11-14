package sched

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// A RUNNING prime has finished booting, but its environment can change before
// imaged accepts readiness. Withdraw only a stale recipient in this attempt;
// fresh workers and other lifecycle owners keep their capacity.
func (e *Engine) retireStalePrimeWorker(ctx context.Context, app state.App, dep state.Deployment, expected state.Instance) (bool, error) {
	owner, ok := e.store.(state.SnapshotPrimeWorkerRecoveryStore)
	if !ok {
		return false, errors.New("sched: prime recovery requires guarded worker retirement")
	}
	values, err := e.store.RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, dep.ID)
	if err != nil {
		return false, fmt.Errorf("sched: prime recovery runtime inputs: %w", err)
	}
	guarded, err := state.WithDeploymentActivationAttempt(ctx, dep, values)
	if err != nil {
		return false, err
	}
	claimed, err := owner.ClaimStalePrimeWorker(guarded, app, expected)
	if err != nil || !claimed {
		return false, err
	}
	// The durable DRAINING claim excludes this worker from readiness before any
	// VM effects. Keep it resident until destruction and the terminal write
	// succeed; the next recovery pass can resume a failed teardown.
	release := e.lockApp(app.ID)
	defer release()
	fresh, err := e.store.InstanceByID(ctx, expected.ID)
	if errors.Is(err, state.ErrNotFound) {
		e.ledger.Release(expected.ID)
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if fresh.State == string(state.StateStopped) {
		e.ledger.Release(expected.ID)
		return true, nil
	}
	if fresh.State != string(state.StateDraining) || fresh.NodeID != expected.NodeID || fresh.WakeID != expected.WakeID {
		return false, nil
	}
	if err := e.destroyForRuntimeConfigRestart(ctx, fresh); err != nil {
		return false, fmt.Errorf("sched: retire stale prime worker: %w", err)
	}
	return true, nil
}
