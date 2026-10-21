// adr: 909 — under measured admission, RAM-pressure eviction follows a
// node's measured memory, not its reservations.

package sched_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSelectMemoryEvictions(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	old := now.Add(-time.Hour)
	vm := func(id, node string, lastRequest time.Duration, plan api.Plan) sched.InstanceInfo {
		return sched.InstanceInfo{Instance: id, AppID: "app-" + id, Plan: plan, State: state.StateRunning, RAMMB: 1024,
			NodeID: node, Started: old, LastRequest: now.Add(-lastRequest)}
	}
	instances := []sched.InstanceInfo{
		vm("a-busy", "a", time.Second, api.PlanPro),
		vm("a-idle", "a", time.Hour, api.PlanPro),
		vm("a-idler", "a", 2*time.Hour, api.PlanPro),
		vm("a-scale", "a", 3*time.Hour, api.PlanScale),
		vm("b-idle", "b", time.Hour, api.PlanPro),
	}
	young := vm("a-young", "a", 3*time.Hour, api.PlanPro)
	young.Started = now.Add(-time.Second)
	service := vm("a-service", "a", 3*time.Hour, api.PlanPro)
	service.Mode = string(state.InstanceModeService)
	instances = append(instances, young, service)
	resident := map[string]int{"a-busy": 300, "a-idle": 300, "a-idler": 400, "a-scale": 900, "b-idle": 300}
	ceiling := map[string]int{"a": 8593, "b": 8593}

	cases := []struct {
		name     string
		used     map[string]int
		resident map[string]int
		want     []string
	}{
		{name: "under the ceiling", used: map[string]int{"a": 8593, "b": 100}, resident: resident},
		// 9000 MB must fall to 90% (7733): LRU non-Scale first, the
		// youngest instance and the service replica never.
		{name: "over the ceiling", used: map[string]int{"a": 9000, "b": 100}, resident: resident,
			want: []string{"a-idler", "a-idle", "a-busy", "a-scale"}},
		{name: "a little over", used: map[string]int{"a": 8600}, resident: resident,
			want: []string{"a-idler", "a-idle", "a-busy"}},
		{name: "unmeasured instances free their reservation", used: map[string]int{"a": 9000},
			resident: map[string]int{}, want: []string{"a-idler", "a-idle"}},
		{name: "only pressured nodes", used: map[string]int{"a": 100, "b": 9000}, resident: resident,
			want: []string{"b-idle"}},
		{name: "node without a report", used: map[string]int{}, resident: resident},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sched.SelectMemoryEvictions(now, instances, tc.used, ceiling, tc.resident)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("evict %v, want %v", got, tc.want)
			}
		})
	}
}
