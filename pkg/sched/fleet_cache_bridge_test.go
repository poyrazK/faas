package sched_test

// adr: 20261017 connection survival, §2 memory (the ADR-909 amendment)

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
)

func TestNodeTelemetryCacheInstanceBridge(t *testing.T) {
	c := sched.NewNodeTelemetryCache()
	now := time.Unix(1_800_000_000, 0)
	c.ReplaceInstanceBridge("n1", now, 40<<20)
	c.ReplaceInstanceBridge("n2", now.Add(-2*sched.TelemetryFreshness), 8<<20)
	c.ReplaceInstanceBridge("n3", now, (1<<20)+1)
	c.ReplaceInstanceBridge("", now, 1<<20)
	c.ReplaceInstanceBridge("n4", now, -1)
	got := c.InstanceBridgeMB(now)
	want := map[string]int{"n1": 40, "n3": 2}
	if len(got) != len(want) || got["n1"] != 40 || got["n3"] != 2 {
		t.Fatalf("bridge MB = %v, want %v (stale n2, empty and negative samples dropped)", got, want)
	}
	if sched.BytesToMiBCeil(0) != 0 || sched.BytesToMiBCeil(1) != 1 || sched.BytesToMiBCeil(1<<20) != 1 {
		t.Fatal("BytesToMiBCeil rounds wrong")
	}
	var nilCache *sched.NodeTelemetryCache
	nilCache.ReplaceInstanceBridge("n1", now, 1)
	if nilCache.InstanceBridgeMB(now) != nil {
		t.Fatal("nil cache reported bridges")
	}
}
