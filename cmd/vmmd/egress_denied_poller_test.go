package main

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
)

type recordedFanout map[string][]int64

func (r recordedFanout) RecordEgressFanout(instance string, perMinute int64) {
	r[instance] = append(r[instance], perMinute)
}

// adr: 361 — the per-namespace poll rolls the ADR-361 policy counters into
// their deny classes and turns faas_egress_new_dst into a trailing
// one-minute fan-out per instance.
func TestEgressNetnsPollerFanoutAndClasses(t *testing.T) {
	readings := []map[string]uint64{
		{netns.EgressNewDstCounter: 5, netns.EgressDenyCounterPolicy: 2},                                    // baseline
		{netns.EgressNewDstCounter: 45, netns.EgressDenyCounterPolicy: 10, netns.EgressDenyCounterRate: 3},  // +40
		{netns.EgressNewDstCounter: 125, netns.EgressDenyCounterPolicy: 10, netns.EgressDenyCounterRate: 3}, // +80
		{netns.EgressNewDstCounter: 125, netns.EgressDenyCounterPolicy: 10, netns.EgressDenyCounterRate: 3}, // +0
		{netns.EgressNewDstCounter: 126, netns.EgressDenyCounterPolicy: 10, netns.EgressDenyCounterRate: 3}, // +1
		{netns.EgressNewDstCounter: 0}, // reset
	}
	tick := 0
	pop := func(context.Context, string) (map[string]uint64, error) { return readings[tick], nil }
	sink := recordedFanout{}
	ops := wire.NewOpsMetrics("vmmd")
	p := newEgressNetnsPoller(sink, ops, pop, nil)
	live := map[string]fcvm.LiveEgressInstance{"i-1": {AppID: "app-1", Netns: "fc-i-1"}}
	start := time.Unix(1_700_000_000, 0)
	for tick = range readings {
		p.tick(context.Background(), start.Add(time.Duration(tick)*15*time.Second), live)
	}
	// The window keeps samples newer than now-60s: t=15s 40, t=30s 120,
	// t=45s 120, t=60s 121; at t=75s the reset adds nothing and the t=15s
	// sample ages out, leaving 81.
	want := []int64{40, 120, 120, 121, 81}
	got := sink["i-1"]
	if len(got) != len(want) {
		t.Fatalf("fan-out samples = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fan-out samples = %v, want %v", got, want)
		}
	}
	if n := testutil.ToFloat64(ops.EgressNewDestinations("app-1")); n != 121 {
		t.Errorf("egress_new_destinations_total{app-1} = %v, want 121", n)
	}
	if n := testutil.ToFloat64(ops.EgressDenied("app-1", string(netns.EgressDenyClassPortPolicy))); n != 8 {
		t.Errorf("port_policy drops = %v, want 8", n)
	}
	if n := testutil.ToFloat64(ops.EgressDenied("app-1", string(netns.EgressDenyClassRateLimit))); n != 3 {
		t.Errorf("rate_limit drops = %v, want 3", n)
	}

	// A torn-down instance loses its baseline and window.
	p.tick(context.Background(), start.Add(2*time.Minute), map[string]fcvm.LiveEgressInstance{})
	if len(p.lastSeen) != 0 || len(p.fanout) != 0 {
		t.Fatalf("state kept for a gone instance: lastSeen=%v fanout=%v", p.lastSeen, p.fanout)
	}
}
