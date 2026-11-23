package memorypressure_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/memorypressure"
	"github.com/onebox-faas/faas/pkg/state"
)

var now = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

func node(id string, booked int64) state.OperatorCapacityNode {
	return state.OperatorCapacityNode{ID: id, Name: "fsn-" + id, Active: true, MemMB: 65536, AdmissionCeilingMB: 47600,
		RAMUsedMB: booked, InstancesRunning: 3}
}

func report(id string, measured int, age time.Duration, frozen int) state.NodeMemoryReport {
	return state.NodeMemoryReport{NodeID: id, MeasuredMB: measured, FrozenInstances: frozen, MeasuredAt: now.Add(-age)}
}

func withUsed(r state.NodeMemoryReport, used int) state.NodeMemoryReport {
	r.UsedMB = &used
	return r
}

func TestNodeStates(t *testing.T) {
	out := memorypressure.Build(memorypressure.Input{Now: now, Window: 24 * time.Hour,
		Nodes: []state.OperatorCapacityNode{node("1", 60000), node("2", 30000), node("3", 119000), node("4", 1000), node("5", 1000), node("6", 1000),
			node("7", 1000), node("8", 1000)},
		Reports: []state.NodeMemoryReport{
			report("1", 20000, time.Second, 4), // ok
			report("2", 48000, time.Second, 0), // over the ceiling
			report("3", 20000, time.Second, 0), // bookings at the 250% cap
			report("4", 43000, time.Second, 0), // inside the 90% eviction band
			report("5", 50000, time.Minute, 7), // stale: not counted as over, its frozen VMs not totalled
			// vmmd's used memory is what measured eviction compares, so it
			// wins over admission's charge both ways.
			withUsed(report("7", 40000, time.Second, 0), 49000), // over
			withUsed(report("8", 48000, time.Second, 0), 20000), // ok
		}})
	byID := map[string]api.ObsMemoryNode{}
	for _, n := range out.Nodes {
		byID[n.NodeID] = n
	}
	want := map[string]string{"1": "ok", "2": "over", "3": "near", "4": "near", "5": "stale", "6": "unmeasured", "7": "over", "8": "ok"}
	for id, st := range want {
		if byID[id].State != st {
			t.Errorf("node %s state = %q, want %q (%+v)", id, byID[id].State, st, byID[id])
		}
	}
	if out.Nodes[0].NodeID != "2" || out.Nodes[len(out.Nodes)-1].State != "ok" {
		t.Fatalf("order = %+v", out.Nodes)
	}
	if byID["3"].BookedLimitMB != 119000 || byID["1"].Frozen != 4 || byID["6"].MeasuredMB != nil || byID["6"].MeasurementAgeSeconds != nil {
		t.Fatalf("node fields = %+v %+v %+v", byID["3"], byID["1"], byID["6"])
	}
	if age := byID["5"].MeasurementAgeSeconds; age == nil || *age != 60 {
		t.Fatalf("stale age = %v", age)
	}
	tot := out.Totals
	if tot.Nodes != 8 || tot.NodesOver != 2 || tot.NodesNear != 2 || tot.NodesStale != 1 || tot.MeasuredMB != 219000 || tot.UsedMB != 200000 ||
		tot.Frozen != 4 || tot.Running != 24 {
		t.Fatalf("totals = %+v", tot)
	}
}

func TestEventsByNodeReasonAndApp(t *testing.T) {
	ev := func(kind, reason, app, node string, at time.Duration, peak int) state.MemoryPressureEvent {
		return state.MemoryPressureEvent{At: now.Add(-at), Kind: kind, Reason: reason, AppID: app, AppSlug: "slug-" + app,
			AccountID: "acct", Plan: "hobby", NodeID: node, InstanceID: "ins", PeakMB: peak, PlanMB: 512}
	}
	evs := []state.MemoryPressureEvent{
		ev(state.MemoryPressureKindOOMKill, "", "api", "1", time.Minute, 601),
		ev(state.MemoryPressureKindOOMKill, "", "api", "1", 2*time.Minute, 700),
		ev(state.MemoryPressureKindEviction, "measured_memory", "blog", "1", 3*time.Minute, 0),
		ev(state.MemoryPressureKindFrozenPark, "memory_pressure", "blog", "2", 4*time.Minute, 0),
		// The instance row is gone: no node, still counted for the app.
		ev(state.MemoryPressureKindEviction, "ram_pressure", "docs", "", 5*time.Minute, 0),
		// No app row either: counted, no app.
		ev(state.MemoryPressureKindEviction, "ram_pressure", "", "", 6*time.Minute, 0),
	}
	out := memorypressure.Build(memorypressure.Input{Now: now, Window: time.Hour, Nodes: []state.OperatorCapacityNode{node("1", 0), node("2", 0)}, Events: evs})
	tot := out.Totals
	if tot.Evictions != 3 || tot.FrozenParks != 1 || tot.OOMKills != 2 || tot.AppsAffected != 3 {
		t.Fatalf("totals = %+v", tot)
	}
	byID := map[string]api.ObsMemoryNode{}
	for _, n := range out.Nodes {
		byID[n.NodeID] = n
	}
	if byID["1"].OOMKills != 2 || byID["1"].Evictions != 1 || byID["2"].FrozenParks != 1 {
		t.Fatalf("nodes = %+v", out.Nodes)
	}
	if out.Apps[0].AppID != "api" || out.Apps[0].OOMKills != 2 || out.Apps[0].MaxPeakMB != 700 || out.Apps[0].PlanMB != 512 || !out.Apps[0].LastAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("apps = %+v", out.Apps)
	}
	if out.Apps[1].AppID != "blog" || out.Apps[1].Evictions != 1 || out.Apps[1].FrozenParks != 1 {
		t.Fatalf("apps = %+v", out.Apps)
	}
	if out.Reasons[0] != (api.ObsMemoryReasonCount{Kind: "eviction", Reason: "ram_pressure", Count: 2}) || out.Reasons[1].Kind != "oom_kill" {
		t.Fatalf("reasons = %+v", out.Reasons)
	}
	if len(out.Events) != 6 || out.Events[0].Kind != "oom_kill" || out.Events[0].PeakMB != 601 || out.Events[2].Reason != "measured_memory" {
		t.Fatalf("events = %+v", out.Events)
	}
}

func TestListCapsAndEmptyShape(t *testing.T) {
	var evs []state.MemoryPressureEvent
	for i := range api.ObsMemoryPressureListMax + 10 {
		evs = append(evs, state.MemoryPressureEvent{At: now.Add(-time.Duration(i) * time.Second), Kind: state.MemoryPressureKindEviction,
			AppID: fmt.Sprint("app-", i)})
	}
	out := memorypressure.Build(memorypressure.Input{Now: now, Window: time.Hour, Events: evs, Truncated: true})
	if len(out.Events) != api.ObsMemoryPressureListMax || len(out.Apps) != api.ObsMemoryPressureAppsMax || !out.Truncated || out.Totals.Evictions != len(evs) {
		t.Fatalf("caps: events=%d apps=%d totals=%+v", len(out.Events), len(out.Apps), out.Totals)
	}
	empty := memorypressure.Build(memorypressure.Input{Now: now, Window: time.Hour})
	if empty.Nodes == nil || empty.Reasons == nil || empty.Apps == nil || empty.Events == nil || empty.OvercommitPercent != 250 || empty.EvictionTargetPercent != 90 {
		t.Fatalf("empty = %+v", empty)
	}
}
