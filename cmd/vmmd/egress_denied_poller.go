package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
)

// popInstanceCountersFunc is the per-netns C1 poll seam. It deliberately
// returns raw named counters; the class roll-up remains testable and shares
// the denylist catalog with the nft renderer.
type popInstanceCountersFunc func(context.Context, string) (map[string]uint64, error)

// runEgressDeniedPoll reads each live namespace's counters and rolls deltas up
// to egress_denied_total{app,class}. The first observation for a namespace is
// only a baseline, matching the existing global egress poller semantics.
func runEgressDeniedPoll(
	ctx context.Context,
	mgr *fcvm.Manager,
	ops *wire.OpsMetrics,
	pop popInstanceCountersFunc,
	interval time.Duration,
	log *slog.Logger,
) {
	if mgr == nil || ops == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	if pop == nil {
		pop = netns.PopCountersInNetns
	}
	if interval <= 0 {
		interval = EgressPollInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	lastSeen := make(map[string]map[string]uint64)
	catalog := netns.NewDefaultDenySet().Entries
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			live := mgr.SnapshotLiveEgress()
			for instance, meta := range live {
				if ctx.Err() != nil {
					return
				}
				if meta.Netns == "" {
					continue
				}
				values, err := pop(ctx, meta.Netns)
				if err != nil {
					log.Warn("per-instance egress poll failed", "instance", instance, "netns", meta.Netns, "err", err)
					continue
				}
				prev, ok := lastSeen[instance]
				if !ok {
					lastSeen[instance] = copyCounterBaseline(values, catalog)
					continue
				}
				deltas := make(map[netns.EgressDenyClass]uint64, 4)
				for _, entry := range catalog {
					if delta, changed := observeCounter(prev, values, entry.CounterName); changed {
						deltas[entry.Class()] += delta
					}
				}
				if delta, changed := observeCounter(prev, values, netns.EgressDenyCounterSMTP); changed {
					deltas[netns.EgressDenyClassSMTP] += delta
				}
				if delta, changed := observeCounter(prev, values, netns.EgressDenyCounterAllowlist); changed {
					deltas[netns.EgressDenyClassAllowlist] += delta
				}
				for class, delta := range deltas {
					if delta == 0 {
						continue
					}
					ops.EgressDenied(meta.AppID, string(class)).Add(float64(delta))
				}
			}
			for instance := range lastSeen {
				if _, ok := live[instance]; !ok {
					delete(lastSeen, instance)
				}
			}
		}
	}
}

func copyCounterBaseline(values map[string]uint64, catalog []netns.DenyEntry) map[string]uint64 {
	baseline := make(map[string]uint64, len(catalog)+2)
	for _, entry := range catalog {
		baseline[entry.CounterName] = values[entry.CounterName]
	}
	baseline[netns.EgressDenyCounterSMTP] = values[netns.EgressDenyCounterSMTP]
	baseline[netns.EgressDenyCounterAllowlist] = values[netns.EgressDenyCounterAllowlist]
	return baseline
}

// observeCounter updates the baseline and returns a non-negative delta. A
// reset (namespace recreation, nft flush, or snapshot restore) re-baselines
// without manufacturing a drop spike.
func observeCounter(prev, current map[string]uint64, name string) (uint64, bool) {
	before := prev[name]
	now := current[name]
	prev[name] = now
	if now < before {
		return 0, false
	}
	return now - before, true
}
