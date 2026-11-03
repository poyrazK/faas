// broker_trigger_lanes.go — external broker triggers on lanes of their own
// (ADR-20261027162741), shared fairly between accounts (ADR-20261101093015).
//
// Kafka, NATS, Redis Streams and SQS-compatible triggers used to be polled
// and delivered one after another on the trigger tick, by every schedd, so
// one tenant's slow function delayed every other tenant's triggers. Each now
// runs on a lane of the bounded loop work pool, keyed by the trigger so one
// batch is in flight per trigger (its partition and stream order kept), and
// only the schedd that owns the trigger's app polls it.
//
// The node's lanes are few and a batch can hold one for up to 16.5 minutes,
// so they are shared: every account with broker triggers on the node is owed
// an even share, accounts and their triggers take turns round-robin, and a
// lane nobody is owed may be lent out but is never lent again while an
// account waits for its share.

package sched

import (
	"context"
	"errors"
	"sort"
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

// brokerLanes tracks the broker triggers holding a lane, so poller eviction
// never closes a poller under a running batch, and shares the lanes between
// accounts.
type brokerLanes struct {
	mu sync.Mutex
	// active counts each trigger's lanes, held from submit to the end of
	// its batch.
	active map[string]int
	// held counts each account's lanes; total is their sum.
	held  map[string]int
	total int
	// lastAccount and lastTrigger are the round-robin cursors: the account
	// last given a lane and, per account, its trigger last given one.
	lastAccount string
	lastTrigger map[string]string
	owners      map[string]triggerAppOwner
}

type triggerAppOwner struct {
	app state.App
	// consumes is false for a deleted app and for one whose billing account
	// is suspended or pending deletion (triggerAppConsumes).
	consumes bool
	// plan is the app's billing plan, empty when its account read failed.
	plan api.Plan
	at   time.Time
}

// cachedTriggerAppPlan returns the plan the trigger tick cached for appID.
func (l *Loop) cachedTriggerAppPlan(appID string) (api.Plan, bool) {
	l.brokerLanes.mu.Lock()
	defer l.brokerLanes.mu.Unlock()
	owner, ok := l.brokerLanes.owners[appID]
	if !ok || owner.plan == "" || time.Since(owner.at) >= triggerAppOwnerTTL {
		return "", false
	}
	return owner.plan, true
}

func (l *Loop) brokerLaneActive(triggerID string) bool {
	l.brokerLanes.mu.Lock()
	defer l.brokerLanes.mu.Unlock()
	return l.brokerLanes.active[triggerID] > 0
}

// brokerLaneQueues orders one tick's lane triggers for the round-robin.
// accounts lists every account with a lane trigger, starting after the
// account last served; queues holds each account's triggers that hold no
// lane, starting after its trigger last served; guarantee is the lanes each
// account is owed: the node's lanes split evenly between the accounts, at
// least one each and no more than the account has triggers.
type brokerLaneQueues struct {
	accounts  []string
	queues    map[string][]sqlc.Trigger
	guarantee map[string]int
}

func (b *brokerLanes) queues(triggers []sqlc.Trigger, slots int) brokerLaneQueues {
	byAccount := map[string][]sqlc.Trigger{}
	for _, t := range triggers {
		acct := t.AccountID.String()
		byAccount[acct] = append(byAccount[acct], t)
	}
	out := brokerLaneQueues{
		accounts:  make([]string, 0, len(byAccount)),
		queues:    make(map[string][]sqlc.Trigger, len(byAccount)),
		guarantee: make(map[string]int, len(byAccount)),
	}
	for acct := range byAccount {
		out.accounts = append(out.accounts, acct)
	}
	sort.Strings(out.accounts)
	share := 1
	if len(out.accounts) > 0 && slots/len(out.accounts) > 1 {
		share = slots / len(out.accounts)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out.accounts = rotateAfter(out.accounts, b.lastAccount, func(acct string) string { return acct })
	cursors := make(map[string]string, len(out.accounts))
	for _, acct := range out.accounts {
		owned := byAccount[acct]
		sort.Slice(owned, func(i, j int) bool { return owned[i].ID.String() < owned[j].ID.String() })
		owned = rotateAfter(owned, b.lastTrigger[acct], func(t sqlc.Trigger) string { return t.ID.String() })
		idle := make([]sqlc.Trigger, 0, len(owned))
		for _, t := range owned {
			if b.active[t.ID.String()] == 0 {
				idle = append(idle, t)
			}
		}
		out.queues[acct] = idle
		out.guarantee[acct] = min(share, len(owned))
		if last, ok := b.lastTrigger[acct]; ok {
			cursors[acct] = last
		}
	}
	// Forget the cursors of accounts with no broker trigger left here.
	b.lastTrigger = cursors
	return out
}

// rotateAfter returns sorted rotated to start at its first element whose key
// sorts after last. An empty last leaves it as it is.
func rotateAfter[T any](sorted []T, last string, key func(T) string) []T {
	if last == "" || len(sorted) == 0 {
		return sorted
	}
	i := sort.Search(len(sorted), func(i int) bool { return key(sorted[i]) > last })
	return append(append(make([]T, 0, len(sorted)), sorted[i:]...), sorted[:i]...)
}

// reserve takes a lane for one of acct's triggers. Without borrowing, the
// account must be short of its guarantee. Borrowing takes a lane only while
// a free one would still be left for every lane an account is owed and does
// not hold, so a lane an account is owed is never lent out.
func (b *brokerLanes) reserve(acct, triggerID string, guarantee map[string]int, slots int, borrowing bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.total >= slots {
		return false
	}
	if !borrowing {
		if b.held[acct] >= guarantee[acct] {
			return false
		}
	} else {
		owed := 0
		for other, owe := range guarantee {
			if short := owe - b.held[other]; short > 0 {
				owed += short
			}
		}
		if slots-b.total-owed <= 0 {
			return false
		}
	}
	if b.active == nil {
		b.active = map[string]int{}
	}
	if b.held == nil {
		b.held = map[string]int{}
	}
	b.active[triggerID]++
	b.held[acct]++
	b.total++
	return true
}

// release returns a lane reserve took.
func (b *brokerLanes) release(acct, triggerID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active[triggerID]--; b.active[triggerID] <= 0 {
		delete(b.active, triggerID)
	}
	if b.held[acct]--; b.held[acct] <= 0 {
		delete(b.held, acct)
	}
	if b.total--; b.total < 0 {
		b.total = 0
	}
}

// served moves the round-robin cursors past a trigger given a lane.
func (b *brokerLanes) served(acct, triggerID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastAccount = acct
	if b.lastTrigger == nil {
		b.lastTrigger = map[string]string{}
	}
	b.lastTrigger[acct] = triggerID
}

// triggerAppOwned reports whether this schedd should poll the trigger's
// external broker: it owns the trigger's app, as the invocation drain decides
// it (an app on this node, or one not yet placed), the app can run, and the
// trigger's capability is on for the account (dispatchOneTrigger would skip
// it otherwise, and its lane would be owed to an account that cannot use
// it). A failed lookup polls, as before, so a store error never stops a
// trigger.
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
		consumes, plan := triggerAppAccount(ctx, l.engine.Store(), app)
		cached = triggerAppOwner{app: app, consumes: consumes, plan: plan, at: now}
		l.brokerLanes.mu.Lock()
		if l.brokerLanes.owners == nil || len(l.brokerLanes.owners) >= 10000 {
			l.brokerLanes.owners = map[string]triggerAppOwner{}
		}
		l.brokerLanes.owners[appID] = cached
		l.brokerLanes.mu.Unlock()
	}
	if !cached.consumes || !l.engine.OwnsApp(cached.app) {
		return false
	}
	if !l.engine.appCapabilityAllowed(ctx, cached.app, triggerCapability(t.Kind)) {
		return false
	}
	// An agent-target trigger waits while agents cannot run here
	// (ADR-20261027090010 §9).
	return !t.Agent.Valid || (l.triggerAgents != nil && l.engine.appCapabilityAllowed(ctx, cached.app, "agents"))
}

// triggerAppConsumes reports whether a broker trigger's app may take records
// from the customer's broker. A deleted app, or one whose billing account is
// suspended or pending deletion, cannot run them: each record retried into
// its dead-letter store and, on Kafka, was committed past, so the customer's
// queue drained into an app they could no longer open (bug hunt 2026-10-28
// B32). The inbound webhook receiver refuses the same two cases. A failed
// account read polls, as a failed app read does.
func triggerAppConsumes(ctx context.Context, store state.AppOwnerReader, app state.App) bool {
	consumes, _ := triggerAppAccount(ctx, store, app)
	return consumes
}

// triggerAppAccount is triggerAppConsumes plus the app's plan, which is
// empty when the account read failed.
func triggerAppAccount(ctx context.Context, store state.AppOwnerReader, app state.App) (bool, api.Plan) {
	if app.Status == state.AppDeleted {
		return false, ""
	}
	acct, err := state.AppResourceAccount(ctx, store, app)
	if err != nil {
		return true, ""
	}
	return acct.Active(), acct.Plan
}

// scheduleBrokerTriggerLanes submits one tick's broker triggers to their
// lanes, fairly between accounts. Accounts take turns, round-robin from
// where the last tick stopped, and so do the triggers of one account. A
// first pass gives each account the lanes it is owed; a second lends out
// the lanes nobody is owed. A trigger with an exclusive broker binding keeps
// the serial path, whose admission is serial by contract (ADR-972); it runs
// once the lanes are submitted.
func (l *Loop) scheduleBrokerTriggerLanes(ctx context.Context, triggers []sqlc.Trigger, store storeLike) {
	laned := make([]sqlc.Trigger, 0, len(triggers))
	var serial []sqlc.Trigger
	for _, t := range triggers {
		if brokerTriggerExclusive(ctx, t, store) {
			serial = append(serial, t)
			continue
		}
		laned = append(laned, t)
	}
	slots := workSpecs[workBrokerTriggerDispatch].slots
	plan := l.brokerLanes.queues(laned, slots)
	saturated := false
	for _, borrowing := range []bool{false, true} {
		for progress := true; progress && !saturated; {
			progress = false
			for _, acct := range plan.accounts {
				queue := plan.queues[acct]
				if len(queue) == 0 || !l.brokerLanes.reserve(acct, queue[0].ID.String(), plan.guarantee, slots, borrowing) {
					continue
				}
				plan.queues[acct] = queue[1:]
				progress = true
				switch l.submitBrokerTriggerLane(ctx, queue[0], acct, store) {
				case "queued", "inline":
					l.brokerLanes.served(acct, queue[0].ID.String())
				case "dropped":
					// The pool is full: every later submit would drop too.
					saturated = true
				}
				if saturated {
					break
				}
			}
		}
	}
	for _, t := range serial {
		if err := l.dispatchOneTrigger(ctx, t, store, l.triggerPlanResolver(ctx)); err != nil && !errors.Is(err, errTriggerGatewayDispatch) {
			l.log.Warn("sched trigger tick: dispatch", "trigger_id", t.ID.String(), "kind", t.Kind, "err", err)
		}
	}
}

// brokerTriggerExclusive reports whether t has an exclusive broker binding,
// or whether the lookup failed, which dispatchOneTrigger then reports.
func brokerTriggerExclusive(ctx context.Context, t sqlc.Trigger, store storeLike) bool {
	exclusive, ok := store.(state.ExclusiveTriggerBindingStore)
	if !ok {
		return false
	}
	_, err := exclusive.ExclusiveTriggerBinding(ctx, t.AccountID.String(), "broker", t.ID.String())
	return !errors.Is(err, state.ErrNotFound)
}

// submitBrokerTriggerLane runs one trigger's batch on its lane, which holds
// the lane reserve took until the batch ends. A submit that never runs (the
// pool is full, or the trigger's last batch is still finishing) returns it.
func (l *Loop) submitBrokerTriggerLane(ctx context.Context, t sqlc.Trigger, acct string, store storeLike) string {
	triggerID := t.ID.String()
	outcome := l.workPool().submit(workBrokerTriggerDispatch, triggerID, func() {
		defer l.brokerLanes.release(acct, triggerID)
		if err := l.dispatchOneTrigger(ctx, t, store, l.triggerPlanResolver(ctx)); err != nil && !errors.Is(err, errTriggerGatewayDispatch) {
			l.log.Warn("sched trigger tick: dispatch lane", "trigger_id", triggerID, "kind", t.Kind, "err", err)
		}
	})
	if outcome == "dropped" || outcome == "coalesced" {
		l.brokerLanes.release(acct, triggerID)
	}
	return outcome
}
