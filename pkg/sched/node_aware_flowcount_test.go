// adr: 127 — persistent node telemetry provides bounded flow detail with a
// freshness-bounded fallback to local observation.

package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched/flowcount"
	"github.com/onebox-faas/faas/pkg/state"
)

type nodeAwareFlowFallback struct {
	warmed    int
	warmCalls int
	warmedIDs []string
	count     int64
	flows     []flowcount.FlowSummary
}

func (f *nodeAwareFlowFallback) Warm(_ context.Context, instances []state.Instance) error {
	f.warmed = len(instances)
	f.warmCalls++
	f.warmedIDs = f.warmedIDs[:0]
	for _, instance := range instances {
		f.warmedIDs = append(f.warmedIDs, instance.ID)
	}
	return nil
}

func (f *nodeAwareFlowFallback) Open(_ context.Context, _ string) (int64, error) {
	return f.count, nil
}

func (f *nodeAwareFlowFallback) Snapshot(_ context.Context, _ string) ([]flowcount.FlowSummary, error) {
	return append([]flowcount.FlowSummary(nil), f.flows...), nil
}

func TestNodeAwareFlowCounterPrefersFreshRemoteTelemetry(t *testing.T) {
	cache := NewNodeTelemetryCache()
	now := time.Unix(300, 0)
	remote := int64(4)
	cache.Replace("node-a", now, now, []NodeTelemetry{{InstanceID: "vm-1", OpenConns: remote}})
	fallback := &nodeAwareFlowFallback{count: 99}
	counter := NewNodeAwareFlowCounter(cache, fallback)
	counter.now = func() time.Time { return now }

	got, err := counter.Open(context.Background(), "vm-1")
	if err != nil || got != remote {
		t.Fatalf("Open = (%d, %v), want (%d, nil)", got, err, remote)
	}
}

func TestNodeAwareFlowCounterSnapshotPrefersFreshRemoteTelemetry(t *testing.T) {
	cache := NewNodeTelemetryCache()
	now := time.Unix(350, 0)
	cache.Replace("node-a", now, now, []NodeTelemetry{{
		InstanceID: "vm-1",
		FlowSummaries: []flowcount.FlowSummary{{
			InstanceID: "vm-1", Protocol: "tcp", RemoteIP: "203.0.113.10", RemotePort: 443, Count: 2,
		}},
	}})
	fallback := &nodeAwareFlowFallback{flows: []flowcount.FlowSummary{{RemoteIP: "192.0.2.1"}}}
	counter := NewNodeAwareFlowCounter(cache, fallback)
	counter.now = func() time.Time { return now }

	got, err := counter.Snapshot(context.Background(), "vm-1")
	if err != nil || len(got) != 1 || got[0].RemoteIP != "203.0.113.10" {
		t.Fatalf("Snapshot = (%+v, %v), want fresh remote summary", got, err)
	}
}

func TestNodeAwareFlowCounterSnapshotFallsBackWhenRemoteIsStale(t *testing.T) {
	cache := NewNodeTelemetryCache()
	now := time.Unix(450, 0)
	cache.Replace("node-a", now, now, []NodeTelemetry{{InstanceID: "vm-1"}})
	fallback := &nodeAwareFlowFallback{flows: []flowcount.FlowSummary{{RemoteIP: "192.0.2.1", Count: 1}}}
	counter := NewNodeAwareFlowCounter(cache, fallback)
	counter.now = func() time.Time { return now.Add(TelemetryFreshness + time.Nanosecond) }

	got, err := counter.Snapshot(context.Background(), "vm-1")
	if err != nil || len(got) != 1 || got[0].RemoteIP != "192.0.2.1" {
		t.Fatalf("stale Snapshot = (%+v, %v), want local fallback summary", got, err)
	}
}

func TestNodeAwareFlowCounterFallsBackWhenRemoteIsMissingOrStale(t *testing.T) {
	cache := NewNodeTelemetryCache()
	now := time.Unix(400, 0)
	remote := int64(4)
	cache.Replace("node-a", now, now, []NodeTelemetry{{InstanceID: "vm-1", OpenConns: remote}})
	fallback := &nodeAwareFlowFallback{count: 7}
	counter := NewNodeAwareFlowCounter(cache, fallback)
	counter.now = func() time.Time { return now.Add(TelemetryFreshness + time.Nanosecond) }

	got, err := counter.Open(context.Background(), "vm-1")
	if err != nil || got != fallback.count {
		t.Fatalf("stale Open = (%d, %v), want (%d, nil)", got, err, fallback.count)
	}
	if got, err := counter.Open(context.Background(), "unknown"); err != nil || got != fallback.count {
		t.Fatalf("missing Open = (%d, %v), want (%d, nil)", got, err, fallback.count)
	}
}

func TestNodeAwareFlowCounterWarmForwardsLocalReader(t *testing.T) {
	fallback := &nodeAwareFlowFallback{}
	counter := NewNodeAwareFlowCounter(nil, fallback)
	instances := []state.Instance{{ID: "vm-1"}, {ID: "vm-2"}}
	if err := counter.Warm(context.Background(), instances); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	if fallback.warmed != len(instances) {
		t.Fatalf("fallback warmed %d instances, want %d", fallback.warmed, len(instances))
	}
}

func TestNodeAwareFlowCounterWarmOnlyForMissingRemoteTelemetry(t *testing.T) {
	cache := NewNodeTelemetryCache()
	now := time.Unix(500, 0)
	cache.Replace("node-a", now, now, []NodeTelemetry{{InstanceID: "vm-remote"}})
	fallback := &nodeAwareFlowFallback{}
	counter := NewNodeAwareFlowCounter(cache, fallback)
	counter.now = func() time.Time { return now }

	instances := []state.Instance{{ID: "vm-remote"}, {ID: "vm-missing"}}
	if err := counter.Warm(context.Background(), instances); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	if fallback.warmCalls != 1 || fallback.warmed != 1 || len(fallback.warmedIDs) != 1 || fallback.warmedIDs[0] != "vm-missing" {
		t.Fatalf("fallback warm = calls:%d count:%d ids:%v, want one call for vm-missing", fallback.warmCalls, fallback.warmed, fallback.warmedIDs)
	}

	cache.Replace("node-a", now, now, []NodeTelemetry{{InstanceID: "vm-remote"}, {InstanceID: "vm-missing"}})
	if err := counter.Warm(context.Background(), instances); err != nil {
		t.Fatalf("Warm with complete telemetry: %v", err)
	}
	if fallback.warmCalls != 1 {
		t.Fatalf("complete remote telemetry invoked fallback; calls=%d, want 1", fallback.warmCalls)
	}
}

func TestNodeAwareFlowCounterWarmEmptyFleetSkipsFallback(t *testing.T) {
	fallback := &nodeAwareFlowFallback{}
	counter := NewNodeAwareFlowCounter(nil, fallback)
	if err := counter.Warm(context.Background(), nil); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	if fallback.warmCalls != 0 {
		t.Fatalf("empty fleet invoked fallback %d times, want 0", fallback.warmCalls)
	}
}
