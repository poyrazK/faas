package sched_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// RAM-pressure eviction ordered victims by last request only, so an
// instance serving a long request was destroyed while idle ones stayed (bug
// hunt 2026-10-28 B10). Busy instances now go last.
func TestSelectMemoryEvictionsPrefersIdleInstances(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	old := now.Add(-time.Hour)
	vm := func(id string, lastRequest time.Duration) sched.InstanceInfo {
		return sched.InstanceInfo{Instance: id, AppID: "app-" + id, Plan: api.PlanPro, State: state.StateRunning, RAMMB: 1024,
			NodeID: "a", Started: old, LastRequest: now.Add(-lastRequest)}
	}
	streaming := vm("a-streaming", 3*time.Hour) // oldest activity, request still in flight
	streaming.InflightRequests = 1
	tail := vm("a-tail", 2*time.Hour)
	tail.TailCount = 1
	idle := vm("a-idle", time.Minute)
	instances := []sched.InstanceInfo{streaming, tail, idle}
	// 8700 MB must fall to 90% of 8593 (7733): one 1000 MB eviction does it.
	resident := map[string]int{"a-streaming": 1000, "a-tail": 1000, "a-idle": 1000}
	got := sched.SelectMemoryEvictions(now, instances, map[string]int{"a": 8700}, map[string]int{"a": 8593}, resident)
	if want := []string{"a-idle"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("evicted %v, want %v (idle before busy)", got, want)
	}
}
