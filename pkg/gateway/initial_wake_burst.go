package gateway

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

type capacityWarmEnsurer interface {
	EnsureWarmCapacity(ctx context.Context, appID, scope, trigger string, desired int) (string, WakeMethod, bool, error)
}

type capacityWakeScheduler interface {
	EnsureWakeCapacity(ctx context.Context, appID, trigger string, desired int, report func(instanceID, nodeID, deploymentID, wakeID string, method int32, port int)) error
}

func (h *Handler) initialWakeDemand(appID string, maxConcurrency int, plan api.Plan) int {
	if h == nil || h.burstPressure == nil || appID == "" {
		return 1
	}
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return 1
	}
	desired := desiredBurstInstances(h.burstPressure.state(appID).inflight.Load(), limits.ConcurrencyPerVMBound, maxConcurrency)
	if desired < 1 {
		return 1
	}
	if desired > api.ScaleUpMaxBurstPerTick {
		return api.ScaleUpMaxBurstPerTick
	}
	return desired
}

// EnsureWarmCapacity keeps the cross-producer wake coordinator while passing
// pressure already accumulated behind this gateway's single cold-start gate.
// Older scheduler adapters/servers return one target; the existing burst
// reconciliation then fills any remaining demand after that target is ready.
func (b *PGBackend) EnsureWarmCapacity(ctx context.Context, appID, scope, trigger string, desired int) (string, WakeMethod, bool, error) {
	if b == nil || scope != "" || desired <= 1 {
		return b.EnsureWarm(ctx, appID, scope, trigger)
	}
	scheduler, err := b.resolveSched(ctx, appID)
	if err != nil {
		return "", WakeMethodUnspecified, false, err
	}
	capacity, ok := scheduler.(capacityWakeScheduler)
	if !ok {
		return b.EnsureWarm(ctx, appID, scope, trigger)
	}
	var firstWake string
	method := WakeMethodUnspecified
	found := false
	err = capacity.EnsureWakeCapacity(ctx, appID, trigger, desired, func(instanceID, nodeID, deploymentID, wakeID string, rawMethod int32, port int) {
		if instanceID == "" || nodeID == "" {
			return
		}
		b.RecordTarget(appID, Target{InstanceID: instanceID, NodeID: nodeID, DeploymentID: deploymentID, WakeID: wakeID, Port: port})
		if !found {
			firstWake, method, found = wakeID, scheddWakeMethodToGateway(rawMethod), true
		}
	})
	return firstWake, method, !found && err == nil, err
}

// Publish the initial batch as the app's active capacity generation so a
// RUNNING notification for its first VM cannot launch duplicate expansion
// while its siblings are still restoring.
func (h *Handler) ensureInitialWarm(ctx context.Context, ensurer capacityWarmEnsurer, appID, scope, trigger string, desired int) (id string, method WakeMethod, atCapacity bool, err error) {
	if desired <= 1 {
		return ensurer.EnsureWarmCapacity(ctx, appID, scope, trigger, desired)
	}
	state := h.burstPressure.state(appID)
	state.mu.Lock()
	generation := state.worker
	owned := generation == nil
	if owned {
		generation = &burstGeneration{done: make(chan struct{})}
		state.worker = generation
	}
	state.mu.Unlock()
	if owned {
		defer func() {
			state.mu.Lock()
			generation.err = err
			if state.worker == generation {
				state.worker = nil
			}
			close(generation.done)
			state.mu.Unlock()
		}()
	}
	return ensurer.EnsureWarmCapacity(ctx, appID, scope, trigger, desired)
}
