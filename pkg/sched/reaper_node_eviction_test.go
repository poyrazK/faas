// spec: §4.3 — RAM-pressure eviction starts above 80% of a node's own
// admission ceiling (per-node ceilings: ADR-203).
// adr: 909 — under measured admission, eviction follows measured memory, so
// reservation pressure must not evict.

package sched

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSelectNodeEvictions(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	old := now.Add(-time.Hour)
	vm := func(id, node string, lastRequest time.Duration, plan api.Plan) InstanceInfo {
		return InstanceInfo{Instance: id, AppID: "app-" + id, Plan: plan, State: state.StateRunning, RAMMB: 1024,
			NodeID: node, Started: old, LastRequest: now.Add(-lastRequest)}
	}
	instances := []InstanceInfo{
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

	cases := []struct {
		name     string
		reserved map[string]int
		ceiling  map[string]int
		want     []string
	}{
		// A 16 GB compute node: the old fixed 38,080 MB threshold never
		// fired here. 9,100 MB must fall to 80% of 10,000 (8,000), which
		// takes two 1,032 MB evictions: LRU non-Scale first, the youngest
		// instance and the service never.
		{name: "small node over 80% of its ceiling",
			reserved: map[string]int{"a": 9100}, ceiling: map[string]int{"a": 10000},
			want: []string{"a-idler", "a-idle"}},
		{name: "small node at exactly 80%",
			reserved: map[string]int{"a": 8000}, ceiling: map[string]int{"a": 10000}},
		// The reference box keeps the spec's 38,080 MB threshold.
		{name: "reference node just over 38,080 MB",
			reserved: map[string]int{"a": api.RAMAdmissionCeilingMB*80/100 + 1}, ceiling: map[string]int{"a": api.RAMAdmissionCeilingMB},
			want: []string{"a-idler"}},
		{name: "reference node at 38,080 MB",
			reserved: map[string]int{"a": api.RAMAdmissionCeilingMB * 80 / 100}, ceiling: map[string]int{"a": api.RAMAdmissionCeilingMB}},
		// Pressure on one node never evicts another node's instances.
		{name: "only pressured nodes",
			reserved: map[string]int{"a": 1000, "b": 9000}, ceiling: map[string]int{"a": 10000, "b": 10000},
			want: []string{"b-idle"}},
		{name: "node without a ceiling",
			reserved: map[string]int{"a": 9000}, ceiling: map[string]int{}},
		{name: "evicts every candidate when still over",
			reserved: map[string]int{"a": 20000}, ceiling: map[string]int{"a": 10000},
			want: []string{"a-idler", "a-idle", "a-busy", "a-scale"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectNodeEvictions(now, instances, tc.reserved, tc.ceiling)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("evict %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSelectNodeEvictionsMatchesReferenceBox pins that a node with the
// reference 47,600 MB ceiling evicts exactly as the single-box
// SelectEvictions always has.
func TestSelectNodeEvictionsMatchesReferenceBox(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var instances []InstanceInfo
	for i := 0; i < 40; i++ {
		instances = append(instances, InstanceInfo{
			Instance: fmt.Sprintf("i%02d", i), AppID: fmt.Sprintf("a%02d", i), Plan: api.PlanPro,
			State: state.StateRunning, RAMMB: 1024, NodeID: "ref",
			Started: now.Add(-time.Hour), LastRequest: now.Add(-time.Duration(i) * time.Minute),
		})
	}
	// ADR-1151: a frozen VM goes first and frees only its frozen floor.
	instances = append(instances, InstanceInfo{
		Instance: "frozen", AppID: "a-frozen", Plan: api.PlanPro, State: state.StateFrozen,
		RAMMB: 1024, NodeID: "ref", Started: now.Add(-time.Hour), LastRequest: now.Add(-time.Hour),
	})
	for _, reserved := range []int{30000, 38080, 38081, 40000, 47600} {
		want := SelectEvictions(reserved, now, instances)
		got := SelectNodeEvictions(now, instances,
			map[string]int{"ref": reserved}, map[string]int{"ref": api.RAMAdmissionCeilingMB})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("reserved %d MB: per-node evict %v, single-box evict %v", reserved, got, want)
		}
	}
}

// TestReservationEvictionsFollowAdmissionMode drives the reaper's decision
// from a real ledger. Nine 1 GiB reservations on a node with a 10,000 MB
// ceiling sit above its 8,000 MB eviction threshold.
func TestReservationEvictionsFollowAdmissionMode(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	const node, ceiling = "small", 10000
	admit := func(t *testing.T, l *NodeLedger) []InstanceInfo {
		t.Helper()
		var snapshot []InstanceInfo
		for i := 0; i < 9; i++ {
			req := Request{
				Instance: fmt.Sprintf("i%d", i), AppID: fmt.Sprintf("a%d", i), Plan: api.PlanScale,
				RAMMB: 1024, VCPU: 1, MaxConcurrency: 20, NodeID: node, NodeCeilingMB: ceiling,
			}
			if err := l.Admit(req); err != nil {
				t.Fatalf("admit %s: %v", req.Instance, err)
			}
			snapshot = append(snapshot, InstanceInfo{
				Instance: req.Instance, AppID: req.AppID, Plan: req.Plan, State: state.StateRunning,
				RAMMB: req.RAMMB, NodeID: node, Started: now.Add(-time.Hour),
				LastRequest: now.Add(-time.Duration(i) * time.Minute),
			})
		}
		return snapshot
	}

	t.Run("reservation admission evicts on the node's own ceiling", func(t *testing.T) {
		l := NewNodeLedger()
		snapshot := admit(t, l)
		if got := l.ResidentRAMForNode(node); got != 9*1032 {
			t.Fatalf("reserved = %d MB, want %d", got, 9*1032)
		}
		// The fixed 38,080 MB threshold never fires on this node.
		if got := SelectEvictions(l.ResidentRAM(), now, snapshot); len(got) != 0 {
			t.Fatalf("fixed threshold evicted %v; the test node is too small for it", got)
		}
		// 9,288 MB must fall to 8,000 MB: the two least recently used go.
		got := reservationEvictions(l, now, snapshot)
		if want := []string{"i8", "i7"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("evict %v, want %v", got, want)
		}
	})

	t.Run("measured admission leaves pressure to measured memory", func(t *testing.T) {
		l := NewNodeLedger()
		l.now = func() time.Time { return now.Add(time.Second) }
		// A fresh report: the node's VMs measure next to nothing.
		l.EnableMeasuredAdmission(func(string) (int, time.Time, bool) { return 0, now, true })
		snapshot := admit(t, l)
		if got := reservationEvictions(l, now, snapshot); len(got) != 0 {
			t.Fatalf("reservation pressure evicted %v under measured admission", got)
		}
	})
}
