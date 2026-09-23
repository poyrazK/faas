package sched

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// refreshRuntimeConfigRolling is the durable apply-now path for env and secret
// changes. It keeps the app active while creating fresh instances, then
// withdraws stale instances from routing and waits for the route/drain barriers
// before destroying them. StateDraining is non-routable but remains fully
// accounted as resident and concurrent until vmmd confirms destruction; unlike
// Park, this path never asks vmmd to write a snapshot.
func (e *Engine) refreshRuntimeConfigRolling(ctx context.Context, appID, wakeID string) (CoordOutcome, error) {
	release := e.lockApp(appID)
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		release()
		return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: load app %s: %w", appID, err)
	}
	if !e.ownsApp(app) {
		release()
		return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: app %s is owned by node %s", appID, app.NodeID)
	}
	if app.Status == state.AppEvictedCold {
		if _, err := compareAndSetAppStatus(ctx, e.store, appID, state.AppEvictedCold, state.AppActive); err != nil {
			release()
			return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: activate app %s: %w", appID, err)
		}
		app.Status = state.AppActive
	}
	if app.Status != state.AppActive {
		release()
		return CoordOutcome{}, nil
	}
	release()
	account, err := e.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: load account %s: %w", app.AccountID, err)
	}
	limits, ok := api.LimitsFor(account.Plan)
	if !ok {
		return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: unknown account plan %q", account.Plan)
	}
	maxConcurrency := effectiveMaxConcurrency(app, limits)

	changedAt, stamped, err := e.store.AppRuntimeConfigChangedAt(ctx, appID)
	if err != nil {
		return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: read change stamp for %s: %w", appID, err)
	}
	if !stamped {
		// Direct Engine callers and old queued requests may not have passed
		// through apid's config-mutation path. Stamp only when absent so an
		// outbox replay never moves the freshness boundary forward.
		if _, err := state.InvalidateAppSnapshots(ctx, e.store, appID); err != nil {
			return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: invalidate snapshots for %s: %w", appID, err)
		}
		changedAt, stamped, err = e.store.AppRuntimeConfigChangedAt(ctx, appID)
		if err != nil || !stamped {
			return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: load change stamp for %s: %w", appID, err)
		}
	}

	// Clear stale non-serving resident VMs across every scope first. Besides
	// preventing an old warm process from being resumed, this ensures one
	// scope's obsolete boot cannot consume the capacity needed to refresh a
	// different live scope.
	instances, err := e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: list initial instances for %s: %w", appID, err)
	}
	for _, instance := range instances {
		if !instance.StartedAt.IsZero() && !changedAt.After(instance.StartedAt) {
			continue
		}
		switch state.State(instance.State) {
		case state.StateWaking, state.StateColdBooting, state.StateWarm:
			if err := e.destroyStaleRuntimeConfigInstance(ctx, instance); err != nil {
				return CoordOutcome{}, err
			}
		}
	}

	deployments, err := e.store.ListDeploymentsForApp(ctx, appID, 0, 0)
	if err != nil {
		return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: list deployments for %s: %w", appID, err)
	}
	live := make([]state.Deployment, 0, len(deployments))
	liveByID := make(map[string]state.Deployment)
	for _, deployment := range deployments {
		if deployment.Status == state.DeployLive {
			live = append(live, deployment)
			liveByID[deployment.ID] = deployment
		}
	}
	if len(live) == 0 {
		return CoordOutcome{}, errors.New("sched: runtime config restart: app has no live deployment")
	}

	var firstReady *CoordInstance
	for _, deployment := range live {
		target := 1
		if app.Manifest.ExecutionMode == api.ExecutionModeService {
			target = desiredServiceReplicas(app.Manifest)
			if target < 1 {
				target = 1
			}
		}
		for {
			currentApp, appErr := e.store.AppByID(ctx, appID)
			if appErr != nil {
				return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: reload app %s: %w", appID, appErr)
			}
			if currentApp.Status != state.AppActive {
				// A newer park/delete wins over this refresh. The lifecycle event
				// owns any remaining serving rows; do not create more VMs.
				return CoordOutcome{}, nil
			}
			instances, listErr := e.store.ListInstancesForApp(ctx, appID)
			if listErr != nil {
				return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: list instances for %s: %w", appID, listErr)
			}
			var fresh, freshStarting []state.Instance
			var staleServing, staleDraining []state.Instance
			var staleBooting []state.Instance
			for _, instance := range instances {
				stale := instance.StartedAt.IsZero() || changedAt.After(instance.StartedAt)
				if instance.DeploymentID == deployment.ID && !stale {
					switch state.State(instance.State) {
					case state.StateRunning:
						fresh = append(fresh, instance)
					case state.StateWaking, state.StateColdBooting:
						freshStarting = append(freshStarting, instance)
					}
				}
				if !stale {
					continue
				}
				if instance.DeploymentID != deployment.ID {
					continue
				}
				switch state.State(instance.State) {
				case state.StateRunning:
					staleServing = append(staleServing, instance)
				case state.StateDraining:
					staleDraining = append(staleDraining, instance)
				case state.StateWaking, state.StateColdBooting, state.StateWarm:
					staleBooting = append(staleBooting, instance)
				}
			}
			sort.Slice(staleServing, func(i, j int) bool { return staleServing[i].StartedAt.Before(staleServing[j].StartedAt) })
			sort.Slice(staleDraining, func(i, j int) bool { return staleDraining[i].StartedAt.Before(staleDraining[j].StartedAt) })
			sort.Slice(staleBooting, func(i, j int) bool { return staleBooting[i].StartedAt.Before(staleBooting[j].StartedAt) })

			// Old boot attempts and warm VMs are not serving traffic. Remove
			// them before admitting a fresh candidate so they cannot later
			// resume with stale credentials.
			if len(staleBooting) > 0 {
				if err := e.destroyStaleRuntimeConfigInstance(ctx, staleBooting[0]); err != nil {
					return CoordOutcome{}, err
				}
				continue
			}

			if firstReady == nil && len(fresh) > 0 {
				firstReady = runtimeConfigCoordInstance(fresh[0], deployment)
			}
			if len(freshStarting) > 0 {
				if err := e.waitForRuntimeConfigCandidate(ctx, freshStarting[0].ID); err != nil {
					return CoordOutcome{}, err
				}
				continue
			}

			if len(staleDraining) > 0 && len(fresh) > 0 {
				if err := e.retireRuntimeConfigInstance(ctx, appID, deployment.ID, staleDraining[0]); err != nil {
					return CoordOutcome{}, err
				}
				continue
			}

			if len(fresh) >= target {
				if len(staleServing) == 0 {
					break
				}
				if err := e.retireRuntimeConfigInstance(ctx, appID, deployment.ID, staleServing[0]); err != nil {
					return CoordOutcome{}, err
				}
				continue
			}

			// App concurrency is bounded at max+1. Once a fresh candidate
			// reaches that temporary one-slot allowance, retire one predecessor
			// only after the candidate is running; a replay resumes by observing
			// the same durable counts.
			if len(fresh) > 0 && len(staleServing) > 0 && e.ledger.Concurrency(appID) >= maxConcurrency+1 {
				if err := e.retireRuntimeConfigInstance(ctx, appID, deployment.ID, staleServing[0]); err != nil {
					return CoordOutcome{}, err
				}
				continue
			}

			candidateCtx := withRequestedWakeID(WithScope(ctx, deployment.Scope), wakeID)
			result, wakeErr := e.admitAndDispatchWithOptions(candidateCtx, appID, deployment.ID,
				instanceModeForApp(app), TriggerRuntimeConfigRestart, false, true)
			if wakeErr != nil {
				return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: boot replacement for deployment %s: %w", deployment.ID, wakeErr)
			}
			if result.AtCapacity || result.InstanceID == "" {
				return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: replacement for deployment %s was not admitted", deployment.ID)
			}
		}
	}

	// Retire stale instances from superseded/preview deployments only after
	// every live scope has at least its fresh configured serving capacity.
	instances, err = e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: list final stale instances for %s: %w", appID, err)
	}
	for _, instance := range instances {
		if _, isLive := liveByID[instance.DeploymentID]; isLive || (!instance.StartedAt.IsZero() && !changedAt.After(instance.StartedAt)) {
			continue
		}
		switch state.State(instance.State) {
		case state.StateRunning, state.StateDraining:
			if err := e.retireRuntimeConfigInstance(ctx, appID, instance.DeploymentID, instance); err != nil {
				return CoordOutcome{}, err
			}
		case state.StateWaking, state.StateColdBooting, state.StateWarm:
			if err := e.destroyStaleRuntimeConfigInstance(ctx, instance); err != nil {
				return CoordOutcome{}, err
			}
		}
	}
	instances, err = e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: verify refreshed instances for %s: %w", appID, err)
	}
	// Revalidate every scope at the acknowledgement boundary. A candidate
	// may have failed or been removed while a later scope was rolling; never
	// acknowledge the durable request unless all currently live scopes still
	// have their configured fresh serving capacity.
	firstReady = nil
	for _, deployment := range live {
		target := 1
		if app.Manifest.ExecutionMode == api.ExecutionModeService {
			target = desiredServiceReplicas(app.Manifest)
			if target < 1 {
				target = 1
			}
		}
		ready := 0
		for _, instance := range instances {
			stale := instance.StartedAt.IsZero() || changedAt.After(instance.StartedAt)
			if instance.DeploymentID != deployment.ID || stale || state.State(instance.State) != state.StateRunning {
				continue
			}
			ready++
			if firstReady == nil {
				firstReady = runtimeConfigCoordInstance(instance, deployment)
			}
		}
		if ready < target {
			return CoordOutcome{}, fmt.Errorf("sched: runtime config restart: deployment %s has %d/%d fresh serving instances", deployment.ID, ready, target)
		}
	}
	if firstReady == nil {
		return CoordOutcome{}, errors.New("sched: runtime config restart completed without a ready replacement")
	}
	return CoordOutcome{Instance: firstReady}, nil
}

func (e *Engine) destroyStaleRuntimeConfigInstance(ctx context.Context, instance state.Instance) error {
	release := e.lockApp(instance.AppID)
	defer release()
	fresh, err := e.store.InstanceByID(ctx, instance.ID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("sched: runtime config restart: reload stale instance %s: %w", instance.ID, err)
	}
	switch state.State(fresh.State) {
	case state.StateWaking, state.StateColdBooting, state.StateWarm:
		if err := e.destroyForRuntimeConfigRestart(ctx, fresh); err != nil {
			return fmt.Errorf("sched: runtime config restart: destroy stale instance %s: %w", fresh.ID, err)
		}
	}
	return nil
}

func runtimeConfigCoordInstance(instance state.Instance, deployment state.Deployment) *CoordInstance {
	return &CoordInstance{
		InstanceID: instance.ID, NodeID: instance.NodeID, DeploymentID: instance.DeploymentID,
		WakeID: instance.WakeID, Port: int32(deploymentRuntimePort(deployment)), ColdBoot: true,
	}
}

func (e *Engine) waitForRuntimeConfigCandidate(ctx context.Context, instanceID string) error {
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		instance, err := e.store.InstanceByID(ctx, instanceID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return nil
			}
			return fmt.Errorf("sched: runtime config restart: read in-flight replacement %s: %w", instanceID, err)
		}
		switch state.State(instance.State) {
		case state.StateRunning:
			return nil
		case state.StateFailed, state.StateStopped, state.StateParked:
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("sched: runtime config restart: replacement %s is still %s", instanceID, instance.State)
		case <-ticker.C:
		}
	}
}

func (e *Engine) retireRuntimeConfigInstance(ctx context.Context, appID, deploymentID string, instance state.Instance) error {
	// Serialize against park/snapshot so we never destroy a VM while another
	// lifecycle operation is capturing or resuming it.
	release := e.lockApp(appID)
	fresh, err := e.store.InstanceByID(ctx, instance.ID)
	if err != nil {
		release()
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("sched: runtime config restart: reload retiring instance %s: %w", instance.ID, err)
	}
	if state.State(fresh.State) == state.StateRunning {
		changed, transitionErr := e.transitionWithKindCAS(ctx, fresh.ID, appID, state.StateDraining,
			"runtime_config_restart", "fresh_replacement_ready")
		if transitionErr != nil {
			release()
			return fmt.Errorf("sched: runtime config restart: withdraw instance %s: %w", fresh.ID, transitionErr)
		}
		_ = changed // the row remains fully accounted while route/drain retries resume
	}
	fresh, err = e.store.InstanceByID(ctx, instance.ID)
	release()
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("sched: runtime config restart: verify withdrawn instance %s: %w", instance.ID, err)
	}
	if state.State(fresh.State) == state.StateStopped {
		return nil
	}
	if state.State(fresh.State) != state.StateDraining {
		return fmt.Errorf("sched: runtime config restart: instance %s changed to unexpected state %s", fresh.ID, fresh.State)
	}

	acknowledgedAt, fleetBarrier, converged := e.waitForServiceRouteConvergenceForRollout(
		ctx, appID, "", deploymentID, "runtime_config_restart")
	if !converged {
		return fmt.Errorf("sched: runtime config restart: route convergence failed for instance %s", fresh.ID)
	}
	if fleetBarrier && !e.waitForRuntimeConfigInstanceDrain(ctx, appID, fresh.ID, acknowledgedAt) {
		return fmt.Errorf("sched: runtime config restart: in-flight requests did not drain for instance %s", fresh.ID)
	}
	if err := e.timedDestroy(context.WithoutCancel(ctx), fresh.NodeID, fresh.ID, DestroyTimeout); err != nil {
		return fmt.Errorf("sched: runtime config restart: destroy withdrawn instance %s: %w", fresh.ID, err)
	}
	changed, err := e.transitionWithKindCAS(ctx, fresh.ID, appID, state.StateStopped,
		"runtime_config_restart", "runtime_configuration_changed")
	if err != nil {
		return fmt.Errorf("sched: runtime config restart: stop withdrawn instance %s: %w", fresh.ID, err)
	}
	if !changed {
		current, readErr := e.store.InstanceByID(ctx, fresh.ID)
		if readErr != nil {
			return readErr
		}
		if state.State(current.State) != state.StateStopped {
			return fmt.Errorf("sched: runtime config restart: instance %s remained %s", fresh.ID, current.State)
		}
	}
	e.ledger.Release(fresh.ID)
	return nil
}

func (e *Engine) waitForRuntimeConfigInstanceDrain(ctx context.Context, appID, instanceID string, acknowledgedAt time.Time) bool {
	drainCtx, cancel := context.WithTimeout(ctx, time.Duration(api.ServiceReplicaDrainTimeoutSeconds)*time.Second)
	defer cancel()
	quietFor := time.Duration(api.ServiceReplicaDrainQuietSeconds) * time.Second
	ticker := time.NewTicker(time.Duration(api.ServiceReplicaDrainPollMilliseconds) * time.Millisecond)
	defer ticker.Stop()
	var zeroSince time.Time
	for {
		instance, err := e.store.InstanceByID(drainCtx, instanceID)
		if errors.Is(err, state.ErrNotFound) || (err == nil && state.State(instance.State) != state.StateDraining) {
			return true
		}
		now := time.Now()
		inflight, receivedAt, observed := e.telemetryCache.LookupInflightRequests(instanceID, now)
		if err == nil && observed && receivedAt.After(acknowledgedAt) && inflight == 0 {
			if zeroSince.IsZero() {
				zeroSince = receivedAt
			} else if receivedAt.Sub(zeroSince) >= quietFor {
				return true
			}
		} else {
			zeroSince = time.Time{}
		}
		select {
		case <-drainCtx.Done():
			e.log.Warn("sched: runtime config request drain incomplete", "app", appID, "instance", instanceID)
			return false
		case <-ticker.C:
		}
	}
}
