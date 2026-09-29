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

// listEgressFlowsFunc is the per-netns flow listing seam; production uses
// netns.ListEgressFlowsInNetns.
type listEgressFlowsFunc func(context.Context, string) ([]netns.EgressFlow, error)

// egressFlowLogger turns each instance's egress_flows nft set into egress
// flow log rows (ADR-369). A row is written the first poll a (destination,
// port) pair is present; the pair is written again only after it expired
// from the set, i.e. after 10 minutes without a new flow to it.
type egressFlowLogger struct {
	store    state.EgressFlowLogStore
	list     listEgressFlowsFunc
	nodeName string
	ops      *wire.OpsMetrics
	log      *slog.Logger
	seen     map[string]map[netns.EgressFlow]struct{}
}

func newEgressFlowLogger(store state.EgressFlowLogStore, list listEgressFlowsFunc, nodeName string, ops *wire.OpsMetrics, log *slog.Logger) *egressFlowLogger {
	if list == nil {
		list = netns.ListEgressFlowsInNetns
	}
	if log == nil {
		log = slog.Default()
	}
	return &egressFlowLogger{store: store, list: list, nodeName: nodeName, ops: ops, log: log,
		seen: make(map[string]map[netns.EgressFlow]struct{})}
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
		flows, err := l.list(ctx, meta.Netns)
		if err != nil {
			l.log.Warn("egress flow log: list failed", "instance", instance, "netns", meta.Netns, "err", err)
			continue
		}
		prev := l.seen[instance]
		current := make(map[netns.EgressFlow]struct{}, len(flows))
		for _, f := range flows {
			current[f] = struct{}{}
			if _, ok := prev[f]; ok {
				continue
			}
			records = append(records, state.EgressFlowRecord{
				ObservedAt: now, NodeName: l.nodeName, AccountID: meta.AccountID, AppID: meta.AppID,
				InstanceID: instance, RemoteIP: f.Addr, RemotePort: f.Port,
			})
		}
		l.seen[instance] = current
	}
	for instance := range l.seen {
		if _, ok := live[instance]; !ok {
			delete(l.seen, instance)
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

// runEgressFlowLog drives the egress flow logger on the counter poll's
// cadence. A nil store (vmmd without a database) disables it.
func runEgressFlowLog(ctx context.Context, mgr *fcvm.Manager, store state.EgressFlowLogStore, nodeName string,
	list listEgressFlowsFunc, ops *wire.OpsMetrics, interval time.Duration, log *slog.Logger) {
	if mgr == nil || store == nil {
		return
	}
	if interval <= 0 {
		interval = EgressPollInterval
	}
	logger := newEgressFlowLogger(store, list, nodeName, ops, log)
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
