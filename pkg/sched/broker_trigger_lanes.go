// broker_trigger_lanes.go — external broker triggers on lanes of their own
// (ADR-20261027162741).
//
// Kafka, NATS, Redis Streams and SQS-compatible triggers used to be polled
// and delivered one after another on the trigger tick, by every schedd, so
// one tenant's slow function delayed every other tenant's triggers. Each now
// runs on a lane of the bounded loop work pool, keyed by the trigger so one
// batch is in flight per trigger (its partition and stream order kept), and
// only the schedd that owns the trigger's app polls it.

package sched

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// triggerAppOwnerTTL bounds how long a tick trusts an app's cached node; an
// ownership move reaches the trigger tick within it.
const triggerAppOwnerTTL = 30 * time.Second

// brokerTriggerLaneEligible selects the triggers polled from an external
// broker. Cron triggers fire elsewhere; queue and realtime triggers keep
// their own paths (ADR-972, ADR-1322).
func brokerTriggerLaneEligible(t sqlc.Trigger) bool {
	switch api.TriggerKind(t.Kind) {
	case api.TriggerKindCron, api.TriggerKindQueue, api.TriggerKindRealtime:
		return false
	}
	return t.Kind != ""
}

// brokerLanes tracks the broker triggers whose lane is delivering, so poller
// eviction never closes a poller under a running batch.
type brokerLanes struct {
	mu     sync.Mutex
	active map[string]int
	owners map[string]triggerAppOwner
}

type triggerAppOwner struct {
	app state.App
	at  time.Time
}

func (l *Loop) brokerLaneActive(triggerID string) bool {
	l.brokerLanes.mu.Lock()
	defer l.brokerLanes.mu.Unlock()
	return l.brokerLanes.active[triggerID] > 0
}

func (l *Loop) markBrokerLane(triggerID string, delta int) {
	l.brokerLanes.mu.Lock()
	defer l.brokerLanes.mu.Unlock()
	if l.brokerLanes.active == nil {
		l.brokerLanes.active = map[string]int{}
	}
	if l.brokerLanes.active[triggerID] += delta; l.brokerLanes.active[triggerID] <= 0 {
		delete(l.brokerLanes.active, triggerID)
	}
}

// triggerAppOwned reports whether this schedd owns the trigger's app, as the
// invocation drain decides it: an app on this node, or one not yet placed. A
// failed lookup polls, as before, so a store error never stops a trigger.
func (l *Loop) triggerAppOwned(ctx context.Context, t sqlc.Trigger) bool {
	if l.engine == nil || !t.AppID.Valid {
		return true
	}
	appID := t.AppID.String()
	now := time.Now()
	l.brokerLanes.mu.Lock()
	cached, ok := l.brokerLanes.owners[appID]
	l.brokerLanes.mu.Unlock()
	if !ok || now.Sub(cached.at) >= triggerAppOwnerTTL {
		app, err := l.engine.Store().AppByID(ctx, appID)
		if err != nil {
			return true
		}
		cached = triggerAppOwner{app: app, at: now}
		l.brokerLanes.mu.Lock()
		if l.brokerLanes.owners == nil || len(l.brokerLanes.owners) >= 10000 {
			l.brokerLanes.owners = map[string]triggerAppOwner{}
		}
		l.brokerLanes.owners[appID] = cached
		l.brokerLanes.mu.Unlock()
	}
	return l.engine.OwnsApp(cached.app)
}

// scheduleBrokerTriggerLane submits one trigger's batch to its lane. A
// trigger with an exclusive broker binding keeps the serial path, whose
// admission is serial by contract (ADR-972).
func (l *Loop) scheduleBrokerTriggerLane(ctx context.Context, t sqlc.Trigger, store storeLike) {
	triggerID := t.ID.String()
	if exclusive, ok := store.(state.ExclusiveTriggerBindingStore); ok {
		if _, err := exclusive.ExclusiveTriggerBinding(ctx, t.AccountID.String(), "broker", triggerID); !errors.Is(err, state.ErrNotFound) {
			if err := l.dispatchOneTrigger(ctx, t, store, l.triggerPlanResolver(ctx)); err != nil && !errors.Is(err, errTriggerGatewayDispatch) {
				l.log.Warn("sched trigger tick: dispatch", "trigger_id", triggerID, "kind", t.Kind, "err", err)
			}
			return
		}
	}
	l.submitWork(workBrokerTriggerDispatch, triggerID, func() {
		l.markBrokerLane(triggerID, 1)
		defer l.markBrokerLane(triggerID, -1)
		if err := l.dispatchOneTrigger(ctx, t, store, l.triggerPlanResolver(ctx)); err != nil && !errors.Is(err, errTriggerGatewayDispatch) {
			l.log.Warn("sched trigger tick: dispatch lane", "trigger_id", triggerID, "kind", t.Kind, "err", err)
		}
	})
}
