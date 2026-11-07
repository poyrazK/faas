package sched

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// advanceServiceRolloutStep publishes only the share backed by ready candidate
// replicas. The store rechecks both cohorts before withdrawing excess old
// replicas. Their reservations survive every route/drain/destroy failure.
func (e *Engine) advanceServiceRolloutStep(ctx context.Context, app state.App, rollout state.Deployment, ready, desired int) bool {
	steps, ok := e.store.(state.ServiceRolloutStepStore)
	if !ok || ready <= 0 || ready >= desired {
		return false
	}
	percent := ready * 100 / desired
	if percent <= 0 || percent < rollout.TrafficPercent {
		return false
	}
	var err error
	if percent > rollout.TrafficPercent {
		rollout, err = steps.BeginServiceRolloutStep(ctx, rollout.ID, percent)
		if err != nil {
			return false
		}
	}
	rollout, err = steps.ClaimServiceRolloutReplicaDrains(ctx, rollout.ID, percent)
	if err != nil {
		return false
	}
	if len(rollout.ServiceRolloutHandoff.RetiringInstanceIDs) == 0 {
		return false
	}
	if !e.finishServiceRolloutReplicaDrains(ctx, app.ID, rollout, state.ServiceRolloutActionPromote) {
		e.recoverServiceRolloutReadiness(ctx, app, rollout)
		return false
	}
	e.scheduleServiceReconcile(ctx, rollout.ID)
	return true
}

// Retiring identities persist on the rollout so a replacement scheduler repeats
// the route acknowledgement and fresh per-instance drain before destroying VMs.
func (e *Engine) finishServiceRolloutReplicaDrains(ctx context.Context, appID string, rollout state.Deployment, action string) bool {
	ids := rollout.ServiceRolloutHandoff.RetiringInstanceIDs
	if len(ids) == 0 {
		return true
	}
	remaining := make([]string, 0, len(ids))
	for _, id := range ids {
		ins, err := e.store.InstanceByID(ctx, id)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err == nil && state.State(ins.State) == state.StateStopped {
			e.ledger.Release(id)
			continue
		}
		if err != nil || ins.AppID != appID || ins.DeploymentID != rollout.ServiceRolloutHandoff.PredecessorDeploymentID || state.State(ins.State) != state.StateDraining {
			return false
		}
		remaining = append(remaining, id)
	}
	if len(remaining) > 0 {
		recipientID := rollout.ID
		if action == state.ServiceRolloutActionAbort {
			recipientID = rollout.ServiceRolloutHandoff.PredecessorDeploymentID
		}
		ack, fleet, converged := e.waitForServiceRouteConvergenceForRollout(ctx, appID, rollout.ID, recipientID, action)
		if !converged {
			return false
		}
		if fleet && !e.waitForServiceReplicaDrain(ctx, appID, rollout.ID, rollout.ServiceRolloutHandoff.PredecessorDeploymentID, action, ack, remaining) {
			return false
		}
		for _, id := range remaining {
			if err := e.stopServiceRolloutDrainingReplica(ctx, id); err != nil {
				e.log.Warn("sched: stop retired service rollout replica", "instance", id, "err", err)
				return false
			}
		}
	}
	steps, ok := e.store.(state.ServiceRolloutStepStore)
	return ok && steps.CompleteServiceRolloutReplicaDrains(ctx, rollout.ID, ids) == nil
}

func (e *Engine) stopServiceRolloutDrainingReplica(ctx context.Context, id string) error {
	ins, err := e.lockedInStates(ctx, id, state.StateDraining)
	if err != nil || ins == nil {
		return err
	}
	defer e.unlockApp(ins.AppID)
	if err := e.timedDestroy(ctx, ins.NodeID, ins.ID, DestroyTimeout); err != nil {
		return err
	}
	changed, err := e.transitionWithKindCAS(ctx, ins.ID, ins.AppID, state.StateStopped, "service_rollout", "replica_replaced")
	if err != nil {
		return err
	}
	if !changed {
		current, err := e.store.InstanceByID(ctx, id)
		if err != nil {
			return err
		}
		if state.State(current.State) != state.StateStopped {
			return state.ErrConflict
		}
	}
	e.ledger.Release(ins.ID)
	return nil
}

func (e *Engine) requestServiceRolloutTimeoutAbort(ctx context.Context, app state.App, rollout state.Deployment) {
	h := rollout.ServiceRolloutHandoff
	now := time.Now().UTC()
	h.Action, h.Phase, h.Reason = state.ServiceRolloutActionAbort, state.ServiceRolloutPhasePending, "readiness timeout"
	h.Generation, h.AcknowledgedAt, h.CompletedAt = 0, nil, nil
	h.UpdatedAt = &now
	updated, err := e.store.UpdateServiceRolloutHandoff(ctx, rollout.ID, h)
	if err == nil {
		e.reverseServiceRollout(ctx, app, updated)
	}
}
