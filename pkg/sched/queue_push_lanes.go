// queue_push_lanes.go — parallel push-queue dispatch (ADR-829).
//
// An in-platform queue push trigger no longer dispatches inline on the
// trigger tick. The tick submits up to N lanes to the bounded loop work pool;
// each lane runs dispatchOneTrigger once. Claims are atomic leases and
// ClaimQueueTriggerInvocation enforces the binding's max_concurrency across
// every concurrent claimer, so lanes add throughput without exceeding it.

package sched

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// errTriggerGatewayDispatch marks a batch the gateway could not take. The
// batch is already settled for retry; lanes use it to back off.
var errTriggerGatewayDispatch = errors.New("sched: trigger gateway dispatch failed")

// queuePushLaneEligible selects the in-platform named queue push consumers.
// External brokers keep the serial path: their ordering and offsets are per
// partition or stream, and unbound legacy queue triggers have no binding cap.
func queuePushLaneEligible(t sqlc.Trigger) bool {
	return t.Kind == string(api.TriggerKindQueue) && t.Source.Valid &&
		t.Source.String == string(state.InvocationQueue) && t.Slug != "" && t.QueueBindingID.Valid
}

// queuePushLaneAllowance is each trigger's lane allowance: one lane to start,
// one more after every lane that reaches the gateway, halved after a gateway
// transport error so a failing handler is backed off rather than flooded.
type queuePushLaneAllowance struct {
	mu      sync.Mutex
	allowed map[string]int
}

func (a *queuePushLaneAllowance) current(triggerID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n := a.allowed[triggerID]; n > 0 {
		return n
	}
	return 1
}

func (a *queuePushLaneAllowance) observe(triggerID string, ceiling int, gatewayFailed bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.allowed == nil {
		a.allowed = map[string]int{}
	}
	n := max(a.allowed[triggerID], 1)
	if gatewayFailed {
		n /= 2
	} else {
		n++
	}
	a.allowed[triggerID] = max(min(n, ceiling), 1)
}

// queuePushLaneCount is the lanes one tick submits: enough to cover the
// backlog at one batch per lane, never more than the binding's concurrency,
// the trigger's allowance or the node's slots, and at least one so an idle
// binding is still polled.
func queuePushLaneCount(maxConcurrency, allowed, depth, batchSize int) int {
	batchSize = max(batchSize, 1)
	lanes := 1
	if depth > 0 {
		lanes = (depth + batchSize - 1) / batchSize
	}
	return max(min(lanes, allowed, maxConcurrency, api.QueuePushDispatchSlotsPerNode), 1)
}

// scheduleQueuePushLanes submits this tick's lanes for one eligible trigger.
// Missing binding or depth data falls back to one lane, which is exactly the
// serial behavior, so a read failure never stops delivery.
func (l *Loop) scheduleQueuePushLanes(ctx context.Context, t sqlc.Trigger, store storeLike) {
	triggerID := t.ID.String()
	if exclusive, ok := store.(state.ExclusiveTriggerBindingStore); ok {
		_, err := exclusive.ExclusiveTriggerBinding(ctx, t.AccountID.String(), "broker", triggerID)
		if !errors.Is(err, state.ErrNotFound) {
			// Exclusive broker admission is serial by contract, and a
			// failed lookup must surface through the serial path's error.
			if err := l.dispatchOneTrigger(ctx, t, store, l.triggerPlanResolver(ctx)); err != nil && !errors.Is(err, errTriggerGatewayDispatch) {
				l.log.Warn("sched trigger tick: dispatch", "trigger_id", triggerID, "kind", t.Kind, "err", err)
			}
			return
		}
	}
	maxConcurrency, depth := 1, 0
	appID, bindingID := t.AppID.String(), t.QueueBindingID.String()
	if binding, err := l.engine.Store().QueueBindingByID(ctx, t.AccountID.String(), appID, bindingID); err == nil {
		maxConcurrency = max(binding.MaxConcurrency, 1)
		if stats, err := l.engine.Store().QueueStateForBinding(ctx, appID, bindingID); err == nil {
			depth = stats.Depth
		}
	}
	ceiling := min(maxConcurrency, api.QueuePushDispatchSlotsPerNode)
	lanes := queuePushLaneCount(maxConcurrency, l.queuePushLanes.current(triggerID), depth, int(t.BatchSizeMax))
	for lane := range lanes {
		l.submitWork(workQueuePushDispatch, fmt.Sprintf("%s#%d", triggerID, lane), func() {
			err := l.dispatchOneTrigger(ctx, t, store, l.triggerPlanResolver(ctx))
			gatewayFailed := errors.Is(err, errTriggerGatewayDispatch)
			l.queuePushLanes.observe(triggerID, ceiling, gatewayFailed)
			if err != nil && !gatewayFailed {
				l.log.Warn("sched trigger tick: dispatch lane", "trigger_id", triggerID, "lane", lane, "err", err)
			}
		})
	}
}
