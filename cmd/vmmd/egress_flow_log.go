package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// listEgressFlowsFunc is the per-netns set listing seam; production uses
// netns.ListEgressFlowsInNetns for allowed flows and
// netns.ListEgressDropsInNetns for blocked attempts.
type listEgressFlowsFunc func(context.Context, string) ([]netns.EgressFlow, error)

// egressFlowLogger turns each instance's egress_flows and egress_blocked nft
// sets into egress flow log rows (ADR-371). A row is written the first poll
// a (destination, port) pair is present; the pair is written again only
// after it expired from the set, i.e. after 10 minutes without a new flow
// (or blocked attempt) to it. Rows from egress_blocked are marked blocked.
type egressFlowLogger struct {
	store     state.EgressFlowLogStore
	list      listEgressFlowsFunc
	listDrops listEgressFlowsFunc
	nodeName  string
	ops       *wire.OpsMetrics
	log       *slog.Logger
	seen      map[string]map[netns.EgressFlow]struct{}
	seenDrops map[string]map[netns.EgressFlow]struct{}
}

func newEgressFlowLogger(store state.EgressFlowLogStore, list, listDrops listEgressFlowsFunc, nodeName string, ops *wire.OpsMetrics, log *slog.Logger) *egressFlowLogger {
	if list == nil {
		list = netns.ListEgressFlowsInNetns
	}
	if listDrops == nil {
		listDrops = netns.ListEgressDropsInNetns
	}
	if log == nil {
		log = slog.Default()
	}
	return &egressFlowLogger{store: store, list: list, listDrops: listDrops, nodeName: nodeName, ops: ops, log: log,
		seen:      make(map[string]map[netns.EgressFlow]struct{}),
		seenDrops: make(map[string]map[netns.EgressFlow]struct{})}
}

// egressBlockedRowsPerInstance caps the blocked rows one instance adds per
// poll. Pairs over the cap are not marked seen, so they are logged on later
// polls while they stay in the set; a scan cannot turn one poll into a huge
// insert.
const egressBlockedRowsPerInstance = 256

// egressSetPoll is one set's pending rows and the state to remember once
// they are stored.
type egressSetPoll struct {
	records []state.EgressFlowRecord
	// next is the seen state after a successful insert: pairs still in the
	// set that were logged before or are logged now.
	next map[string]map[netns.EgressFlow]struct{}
	// kept is the seen state after a failed insert: only pairs still in the
	// set that were logged before, so the new ones are retried next poll.
	kept map[string]map[netns.EgressFlow]struct{}
}

func (l *egressFlowLogger) tick(ctx context.Context, now time.Time, live map[string]fcvm.LiveEgressInstance) {
	allowed := egressSetPoll{next: map[string]map[netns.EgressFlow]struct{}{}, kept: map[string]map[netns.EgressFlow]struct{}{}}
	blocked := egressSetPoll{next: map[string]map[netns.EgressFlow]struct{}{}, kept: map[string]map[netns.EgressFlow]struct{}{}}
	for instance, meta := range live {
		if ctx.Err() != nil {
			return
		}
		if meta.Netns == "" {
			continue
		}
		l.collect(ctx, now, instance, meta, false, &allowed)
		l.collect(ctx, now, instance, meta, true, &blocked)
	}
	// Allowed flows and blocked attempts are separate inserts: a failed
	// blocked batch must not cost the allowed flows abuse reports trace.
	l.seen = l.persist(ctx, "flow", l.seen, allowed, live)
	l.seenDrops = l.persist(ctx, "drop", l.seenDrops, blocked, live)
}

// persist inserts one set's rows and returns the seen state to keep. Seen
// advances only when the insert succeeds; on failure the new pairs stay
// unseen and are retried next poll while they remain in the set.
func (l *egressFlowLogger) persist(ctx context.Context, what string, seen map[string]map[netns.EgressFlow]struct{},
	poll egressSetPoll, live map[string]fcvm.LiveEgressInstance) map[string]map[netns.EgressFlow]struct{} {
	nextState := poll.next
	if len(poll.records) > 0 {
		if err := l.store.InsertEgressFlows(ctx, poll.records); err != nil {
			l.log.Warn("egress flow log: insert failed", "set", what, "rows", len(poll.records), "err", err)
			nextState = poll.kept
		} else if l.ops != nil {
			l.ops.EgressFlowLogRows().Add(float64(len(poll.records)))
		}
	}
	// Instances whose listing failed keep their previous state; gone
	// instances are forgotten.
	for instance, prev := range seen {
		if _, ok := live[instance]; !ok {
			continue
		}
		if _, ok := nextState[instance]; !ok {
			nextState[instance] = prev
		}
	}
	return nextState
}

// collect lists one instance's allowed or blocked set and adds a record for
// every pair not logged before (at most egressBlockedRowsPerInstance for
// blocked attempts). A failed listing leaves the instance out of poll, so it
// keeps its previous state.
func (l *egressFlowLogger) collect(ctx context.Context, now time.Time, instance string, meta fcvm.LiveEgressInstance,
	blocked bool, poll *egressSetPoll) {
	list, seen, what := l.list, l.seen, "flow"
	if blocked {
		list, seen, what = l.listDrops, l.seenDrops, "drop"
	}
	flows, err := list(ctx, meta.Netns)
	if err != nil {
		l.log.Warn("egress flow log: list failed", "set", what, "instance", instance, "netns", meta.Netns, "err", err)
		return
	}
	prev := seen[instance]
	next := make(map[netns.EgressFlow]struct{}, len(flows))
	kept := make(map[netns.EgressFlow]struct{}, len(prev))
	added := 0
	for _, f := range flows {
		if _, ok := prev[f]; ok {
			next[f] = struct{}{}
			kept[f] = struct{}{}
			continue
		}
		if blocked && added >= egressBlockedRowsPerInstance {
			continue
		}
		added++
		next[f] = struct{}{}
		poll.records = append(poll.records, state.EgressFlowRecord{
			ObservedAt: now, NodeName: l.nodeName, AccountID: meta.AccountID, AppID: meta.AppID,
			InstanceID: instance, RemoteIP: f.Addr, RemotePort: f.Port, Protocol: f.Protocol, Blocked: blocked,
		})
	}
	poll.next[instance] = next
	poll.kept[instance] = kept
}

// runEgressFlowLog drives the egress flow logger on the counter poll's
// cadence. A nil store (vmmd without a database) disables it.
func runEgressFlowLog(ctx context.Context, mgr *fcvm.Manager, store state.EgressFlowLogStore, nodeName string,
	list, listDrops listEgressFlowsFunc, ops *wire.OpsMetrics, interval time.Duration, log *slog.Logger) {
	if mgr == nil || store == nil {
		return
	}
	if interval <= 0 {
		interval = EgressPollInterval
	}
	logger := newEgressFlowLogger(store, list, listDrops, nodeName, ops, log)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			logger.tick(ctx, now.UTC(), mgr.SnapshotLiveEgress())
		}
	}
}
