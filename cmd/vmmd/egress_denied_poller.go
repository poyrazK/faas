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

// egressFanoutWindow is the span over which new destinations are summed
// into the per-minute fan-out signal (ADR-361 decision 6).
const egressFanoutWindow = time.Minute

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
	if interval <= 0 {
		interval = EgressPollInterval
	}
	poller := newEgressNetnsPoller(mgr, ops, pop, log)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			poller.tick(ctx, now, mgr.SnapshotLiveEgress())
		}
	}
}

// egressFanoutSink receives each instance's per-minute egress abuse
// samples: new destinations (fan-out) and per-destination flood drops.
// *fcvm.Manager implements it; Stats serves the stored values to schedd.
type egressFanoutSink interface {
	RecordEgressFanout(instance string, perMinute int64)
	RecordEgressFlood(instance string, dropsPerMinute int64)
}

type fanoutSample struct {
	at  time.Time
	new uint64
}

// egressNetnsPoller holds the per-instance counter baselines and fan-out
// windows between ticks. It is owned by one goroutine.
type egressNetnsPoller struct {
	sink     egressFanoutSink
	ops      *wire.OpsMetrics
	pop      popInstanceCountersFunc
	log      *slog.Logger
	catalog  []netns.DenyEntry
	lastSeen map[string]map[string]uint64
	fanout   map[string][]fanoutSample
	flood    map[string][]fanoutSample
}

func newEgressNetnsPoller(sink egressFanoutSink, ops *wire.OpsMetrics, pop popInstanceCountersFunc, log *slog.Logger) *egressNetnsPoller {
	if pop == nil {
		pop = netns.PopCountersInNetns
	}
	if log == nil {
		log = slog.Default()
	}
	return &egressNetnsPoller{
		sink:     sink,
		ops:      ops,
		pop:      pop,
		log:      log,
		catalog:  netns.NewDefaultDenySet().Entries,
		lastSeen: make(map[string]map[string]uint64),
		fanout:   make(map[string][]fanoutSample),
		flood:    make(map[string][]fanoutSample),
	}
}

// aggregateDenyCounters maps the named aggregate counters to their class;
// the per-CIDR catalog entries roll up by DenyEntry.Class.
var aggregateDenyCounters = []struct {
	name  string
	class netns.EgressDenyClass
}{
	{netns.EgressDenyCounterSMTP, netns.EgressDenyClassSMTP},
	{netns.EgressFloodCounter, netns.EgressDenyClassFlood},
	{netns.EgressDenyCounterAllowlist, netns.EgressDenyClassAllowlist},
	{netns.EgressDenyCounterPolicy, netns.EgressDenyClassPortPolicy},
	{netns.EgressDenyCounterRate, netns.EgressDenyClassRateLimit},
}

func (p *egressNetnsPoller) tick(ctx context.Context, now time.Time, live map[string]fcvm.LiveEgressInstance) {
	for instance, meta := range live {
		if ctx.Err() != nil {
			return
		}
		if meta.Netns == "" {
			continue
		}
		values, err := p.pop(ctx, meta.Netns)
		if err != nil {
			p.log.Warn("per-instance egress poll failed", "instance", instance, "netns", meta.Netns, "err", err)
			continue
		}
		prev, ok := p.lastSeen[instance]
		if !ok {
			p.lastSeen[instance] = p.baseline(values)
			continue
		}
		deltas := make(map[netns.EgressDenyClass]uint64, 6)
		for _, entry := range p.catalog {
			if delta, changed := observeCounter(prev, values, entry.CounterName); changed {
				deltas[entry.Class()] += delta
			}
		}
		for _, agg := range aggregateDenyCounters {
			if delta, changed := observeCounter(prev, values, agg.name); changed {
				deltas[agg.class] += delta
			}
		}
		for class, delta := range deltas {
			if delta == 0 {
				continue
			}
			p.ops.EgressDenied(meta.AppID, string(class)).Add(float64(delta))
		}
		newDst, _ := observeCounter(prev, values, netns.EgressNewDstCounter)
		if newDst > 0 {
			p.ops.EgressNewDestinations(meta.AppID).Add(float64(newDst))
		}
		p.sink.RecordEgressFanout(instance, observeWindow(p.fanout, instance, now, newDst))
		p.sink.RecordEgressFlood(instance, observeWindow(p.flood, instance, now, deltas[netns.EgressDenyClassFlood]))
	}
	for instance := range p.lastSeen {
		if _, ok := live[instance]; !ok {
			delete(p.lastSeen, instance)
			delete(p.fanout, instance)
			delete(p.flood, instance)
		}
	}
}

func (p *egressNetnsPoller) baseline(values map[string]uint64) map[string]uint64 {
	baseline := make(map[string]uint64, len(p.catalog)+len(aggregateDenyCounters)+1)
	for _, entry := range p.catalog {
		baseline[entry.CounterName] = values[entry.CounterName]
	}
	for _, agg := range aggregateDenyCounters {
		baseline[agg.name] = values[agg.name]
	}
	baseline[netns.EgressNewDstCounter] = values[netns.EgressNewDstCounter]
	return baseline
}

// observeWindow appends one tick's count for instance and returns the sum
// over the trailing egressFanoutWindow.
func observeWindow(windows map[string][]fanoutSample, instance string, now time.Time, n uint64) int64 {
	samples := append(windows[instance], fanoutSample{at: now, new: n})
	cutoff := now.Add(-egressFanoutWindow)
	kept := samples[:0]
	var sum uint64
	for _, s := range samples {
		if s.at.After(cutoff) {
			kept = append(kept, s)
			sum += s.new
		}
	}
	windows[instance] = kept
	return int64(min(sum, uint64(1<<62)))
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
