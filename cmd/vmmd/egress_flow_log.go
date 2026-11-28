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

func (l *egressFlowLogger) tick(ctx context.Context, now time.Time, live map[string]fcvm.LiveEgressInstance) {
	var records []state.EgressFlowRecord
	for instance, meta := range live {
		if ctx.Err() != nil {
			return
		}
		if meta.Netns == "" {
			continue
		}
		records = l.collect(ctx, now, instance, meta, false, records)
		records = l.collect(ctx, now, instance, meta, true, records)
	}
	for instance := range l.seen {
		if _, ok := live[instance]; !ok {
			delete(l.seen, instance)
		}
	}
	for instance := range l.seenDrops {
		if _, ok := live[instance]; !ok {
			delete(l.seenDrops, instance)
		}
	}
	if len(records) == 0 {
		return
	}
	if err := l.store.InsertEgressFlows(ctx, records); err != nil {
		// The pairs stay in seen, so a failed batch is not retried: the
		// log is best-effort evidence and must not back up the poll.
		l.log.Warn("egress flow log: insert failed", "rows", len(records), "err", err)
		return
	}
	if l.ops != nil {
		l.ops.EgressFlowLogRows().Add(float64(len(records)))
	}
}

// collect lists one instance's allowed or blocked set, appends a record for
// every pair not present on the previous poll, and remembers the current
// set. A failed listing keeps the previous state.
func (l *egressFlowLogger) collect(ctx context.Context, now time.Time, instance string, meta fcvm.LiveEgressInstance,
	blocked bool, records []state.EgressFlowRecord) []state.EgressFlowRecord {
	list, seen, what := l.list, l.seen, "flow"
	if blocked {
		list, seen, what = l.listDrops, l.seenDrops, "drop"
	}
	flows, err := list(ctx, meta.Netns)
	if err != nil {
		l.log.Warn("egress flow log: list failed", "set", what, "instance", instance, "netns", meta.Netns, "err", err)
		return records
	}
	prev := seen[instance]
	current := make(map[netns.EgressFlow]struct{}, len(flows))
	for _, f := range flows {
		current[f] = struct{}{}
		if _, ok := prev[f]; ok {
			continue
		}
		records = append(records, state.EgressFlowRecord{
			ObservedAt: now, NodeName: l.nodeName, AccountID: meta.AccountID, AppID: meta.AppID,
			InstanceID: instance, RemoteIP: f.Addr, RemotePort: f.Port, Protocol: f.Protocol, Blocked: blocked,
		})
	}
	seen[instance] = current
	return records
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
