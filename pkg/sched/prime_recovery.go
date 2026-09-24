package sched

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// A notification is only a wakeup hint: another schedd can acknowledge the
// shared snapshot_prime outbox row before the owning schedd completes Prime.
// Recheck old snapshot_prepare rows so a missed or prematurely acknowledged
// hint cannot leave a deployment pending indefinitely. A grace period avoids
// racing ordinary primes; the instance and work-pool checks protect longer
// captures and retries.
const (
	primeRecoveryInterval = time.Minute
	primeRecoveryDelay    = 2 * time.Minute
	primeRecoveryPageSize = 64
	primeRecoveryMaxPages = 4
)

func (l *Loop) dispatchPrimeRecovery(ctx context.Context) {
	l.submitWork(workPrimeRecovery, "fleet", func() { l.runPrimeRecovery(ctx) })
}

func (l *Loop) runPrimeRecovery(ctx context.Context) {
	cutoff := l.now().UTC().Add(-primeRecoveryDelay)
	for page := 0; page < primeRecoveryMaxPages && ctx.Err() == nil; page++ {
		deps, err := l.engine.store.ListDeploymentsForOperator(ctx, state.OperatorDeploymentFilter{
			Statuses:      []state.DeploymentStatus{state.DeploySnapshotting},
			CreatedBefore: cutoff,
			OldestFirst:   true,
			Limit:         primeRecoveryPageSize,
			Offset:        page * primeRecoveryPageSize,
		})
		if err != nil {
			l.log.Warn("sched: list snapshot prime recovery candidates", "err", err)
			return
		}
		for _, dep := range deps {
			if ctx.Err() != nil {
				return
			}
			if err := l.recoverPrimeCandidate(ctx, dep.ID, cutoff); err != nil {
				l.log.Warn("sched: inspect snapshot prime recovery candidate", "deployment", dep.ID, "err", err)
			}
		}
		if len(deps) < primeRecoveryPageSize {
			return
		}
	}
}

func (l *Loop) recoverPrimeCandidate(ctx context.Context, deploymentID string, cutoff time.Time) error {
	// The fleet listing is a candidate index, not the decision: re-read the
	// full row because rollback cancellation or a normal prime may have won.
	dep, err := l.engine.store.DeploymentByID(ctx, deploymentID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if dep.Status != state.DeploySnapshotting || dep.RootfsKey == "" || len(dep.StageState) == 0 {
		return nil
	}
	var stage state.StageState
	if err := json.Unmarshal(dep.StageState, &stage); err != nil {
		return err
	}
	if stage.Current != state.StageSnapshotPrepare || stage.CurrentStartedAt == nil || !stage.CurrentStartedAt.Before(cutoff) {
		return nil
	}
	app, err := l.engine.store.AppByID(ctx, dep.AppID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if app.Status == state.AppDeleted || !l.engine.ownsAppDecision(app) {
		return nil
	}
	if snap, err := l.engine.store.LatestSnapshot(ctx, dep.ID); err == nil {
		// Rollback requeues an old deployment. Its earlier snapshot may
		// still exist, but only a capture after this snapshot_prepare
		// attempt proves the new handoff completed.
		if snap.CreatedAt.After(*stage.CurrentStartedAt) {
			return nil
		}
	} else if !errors.Is(err, state.ErrNotFound) {
		return err
	}
	instances, err := l.engine.store.ListLatestInstancesForApp(ctx, app.ID, 64)
	if err != nil {
		return err
	}
	for _, ins := range instances {
		// A PARKED instance means capture finished and imaged may still be
		// handling snapshot_written. Never start a second VM during that
		// handoff. Older parked instances from before a rollback requeue do
		// not protect the new attempt.
		if ins.DeploymentID == dep.ID && ins.StartedAt.After(*stage.CurrentStartedAt) &&
			(state.State(ins.State).CountsForRAM() || state.State(ins.State) == state.StateParked) {
			return nil
		}
	}
	l.log.Warn("sched: recovering lost snapshot prime handoff", "app", app.ID, "deployment", dep.ID)
	l.dispatchPrime(ctx, app.ID, dep.ID)
	return nil
}
