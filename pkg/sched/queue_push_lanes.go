// queue_push_lanes.go — parallel push-queue dispatch (ADR-972).
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
	"time"

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

// prune forgets the allowance of triggers this tick no longer polls, which
// otherwise stayed in the map for the scheduler's lifetime.
func (a *queuePushLaneAllowance) prune(polled []sqlc.Trigger) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.allowed) == 0 {
		return
	}
	live := make(map[string]bool, len(polled))
	for i := range polled {
		live[polled[i].ID.String()] = true
	}
	for id := range a.allowed {
		if !live[id] {
			delete(a.allowed, id)
		}
	}
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
		// Lanes never exceed max_concurrency, so depth past it changes nothing.
		if bounded, err := l.engine.Store().QueueDepthForBindingInScope(ctx, appID, bindingID, "", maxConcurrency); err == nil {
			depth = bounded
		}
	}
	// A lane carries one record. The binding projection sizes batches at
	// max_concurrency, and the gateway invokes a batch's records serially,
	// so one lane would otherwise claim the binding's whole in-flight cap and
	// deliver it one record at a time. Handlers receive one record per
	// invocation either way.
	laneTrigger := t
	laneTrigger.BatchSizeMax = 1
	ceiling := min(maxConcurrency, api.QueuePushDispatchSlotsPerNode)
	lanes := queuePushLaneCount(maxConcurrency, l.queuePushLanes.current(triggerID), depth, 1)
	for lane := range lanes {
		l.submitWork(workQueuePushDispatch, fmt.Sprintf("%s#%d", triggerID, lane), func() {
			planFor := l.triggerPlanResolver(ctx)
			err := drainQueuePushLane(ctx, func(ctx context.Context) error {
				return l.dispatchOneTrigger(ctx, laneTrigger, store, planFor)
			})
			gatewayFailed := errors.Is(err, errTriggerGatewayDispatch)
			l.queuePushLanes.observe(triggerID, ceiling, gatewayFailed)
			if err != nil && !gatewayFailed {
				l.log.Warn("sched trigger tick: dispatch lane", "trigger_id", triggerID, "lane", lane, "err", err)
			}
		})
	}
}

// drainQueuePushLane runs one lane: it delivers one record, then the next,
// while each poll returns a record, until the lane's time or record budget
// is spent (ADR-972 amendment 2026-11-19). A lane that delivered one record
// per tick idled between ticks, so a binding's throughput was its lanes times
// the tick rate rather than what its handler could take. An empty poll or an
// error ends the run; the next tick submits the lane again.
func drainQueuePushLane(ctx context.Context, dispatch func(context.Context) error) error {
	start := time.Now()
	for delivered := 1; ; delivered++ {
		pollCtx, report := withTriggerPollReport(ctx)
		if err := dispatch(pollCtx); err != nil {
			return err
		}
		// A cancelled scheduler stops the lane without an error: the
		// dispatch in progress settled its record, and shutdown is not a
		// lane failure.
		more := report.records > 0 && delivered < api.QueuePushLaneDrainMaxRecords &&
			time.Since(start) < api.QueuePushLaneDrainBudget && context.Cause(ctx) == nil
		if !more {
			return nil
		}
	}
}

// triggerPollReport records how many records a dispatch's poll returned, so a
// queue push lane knows whether to claim again.
type triggerPollReport struct{ records int }

type triggerPollReportKey struct{}

func withTriggerPollReport(ctx context.Context) (context.Context, *triggerPollReport) {
	report := &triggerPollReport{}
	return context.WithValue(ctx, triggerPollReportKey{}, report), report
}

// reportTriggerPoll stores a poll's record count in the context's report, if
// the dispatch has one.
func reportTriggerPoll(ctx context.Context, records int) {
	if report, ok := ctx.Value(triggerPollReportKey{}).(*triggerPollReport); ok {
		report.records = records
	}
}
