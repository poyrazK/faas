// adr: 20261030100950 — a node whose tenant tasks wait for CPU while its
// CPUs are nearly all busy is held under CPU pressure: placement prefers
// other nodes and admission counts its bookings in full until the hold
// expires.
package sched_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestNodeCPUPressurePressured(t *testing.T) {
	stall, busy := float64(api.NodeCPUPressureStallPercent), float64(api.NodeCPUPressureBusyPercent)
	for _, tc := range []struct {
		name    string
		reading sched.NodeCPUPressure
		want    bool
	}{
		{"waiting on busy CPUs", sched.NodeCPUPressure{SomeAvg10: stall, BusyPercent: busy}, true},
		// One app at its own cpu.max quota stalls on an idle host.
		{"waiting on idle CPUs", sched.NodeCPUPressure{SomeAvg10: 80, BusyPercent: busy - 1}, false},
		// Busy CPUs with nobody waiting are good utilisation.
		{"busy without waiting", sched.NodeCPUPressure{SomeAvg10: stall - 1, BusyPercent: 100}, false},
	} {
		if got := tc.reading.Pressured(); got != tc.want {
			t.Errorf("%s: Pressured() = %t, want %t", tc.name, got, tc.want)
		}
	}
}

func TestNodeTelemetryCacheCPUPressure(t *testing.T) {
	c := sched.NewNodeTelemetryCache()
	at := time.Unix(1_800_000_000, 0)
	c.ReplaceCPUPressure("node-a", at, sched.NodeCPUPressure{SomeAvg10: 30, BusyPercent: 95})
	if got := c.CPUPressure(at.Add(sched.TelemetryFreshness)); got["node-a"].SomeAvg10 != 30 {
		t.Fatalf("fresh readings = %v", got)
	}
	if got := c.CPUPressure(at.Add(sched.TelemetryFreshness + time.Second)); len(got) != 0 {
		t.Fatalf("stale readings = %v, want none", got)
	}
}

// The node's schedd holds a pressured node, rewrites the hold only once
// less than half of it remains, and never holds a node whose readings are
// not pressured or stopped.
func TestObserveCPUPressureHoldsAPressuredNode(t *testing.T) {
	f := newFailoverFleet(t, 2, 4096, func(string) error { return nil })
	pressured := sched.NodeCPUPressure{SomeAvg10: 50, BusyPercent: 99}
	readings := map[string]sched.NodeCPUPressure{f.nodes[0].ID: pressured}
	f.e.EnableCPUPressureBackstop(func(time.Time) map[string]sched.NodeCPUPressure { return readings })
	hold := time.Duration(api.NodeCPUPressureHoldSeconds) * time.Second
	held := func() *time.Time {
		t.Helper()
		return breakerOf(t, f, f.nodes[0].ID).CPUPressureUntil
	}
	holds := func() float64 {
		t.Helper()
		return counterValue(t, f.ops, "schedd_node_cpu_pressure_holds_total", "node", f.nodes[0].ID)
	}

	start := time.Now()
	f.e.ObserveCPUPressure(f.ctx, start)
	if u := held(); u == nil || !u.Equal(start.Add(hold)) {
		t.Fatalf("hold = %v, want %v", u, start.Add(hold))
	}
	if !breakerOf(t, f, f.nodes[0].ID).CPUPressured(start) || breakerOf(t, f, f.nodes[1].ID).CPUPressureUntil != nil {
		t.Fatal("want node 0 held and node 1, which sent no readings, not held")
	}
	// More than half the hold remains: no rewrite.
	f.e.ObserveCPUPressure(f.ctx, start.Add(hold/2-time.Second))
	if u := held(); !u.Equal(start.Add(hold)) {
		t.Fatalf("hold rewritten early to %v", u)
	}
	// Less than half remains: extended, in the same episode.
	extended := start.Add(hold/2 + time.Second)
	f.e.ObserveCPUPressure(f.ctx, extended)
	if u := held(); !u.Equal(extended.Add(hold)) {
		t.Fatalf("hold = %v, want %v", u, extended.Add(hold))
	}
	if got := holds(); got != 1 {
		t.Fatalf("holds = %v, want 1 episode", got)
	}

	// Waiting on idle CPUs is not pressure: the hold is left to expire.
	readings[f.nodes[0].ID] = sched.NodeCPUPressure{SomeAvg10: 90, BusyPercent: 40}
	later := extended.Add(2 * hold)
	f.e.ObserveCPUPressure(f.ctx, later)
	if breakerOf(t, f, f.nodes[0].ID).CPUPressured(later) {
		t.Fatal("a node without pressure stayed held")
	}
	// Pressure again starts a new episode.
	readings[f.nodes[0].ID] = pressured
	f.e.ObserveCPUPressure(f.ctx, later)
	if got := holds(); got != 2 {
		t.Fatalf("holds = %v, want 2 episodes", got)
	}
}

// Placement prefers a node without CPU pressure and still uses a pressured
// node when nothing else fits.
func TestCPUPressurePlacementPrefersAnotherNode(t *testing.T) {
	f := newFailoverFleet(t, 2, 4096, func(string) error { return nil })
	app := f.app(t, api.PlanScale, 256, 20)
	until := time.Now().Add(time.Minute)
	if err := f.store.HoldComputeNodeCPUPressure(f.ctx, f.nodes[0].ID, until); err != nil {
		t.Fatal(err)
	}
	if res, err := f.e.AdmitInstance(f.ctx, app.ID, "", "", sched.TriggerGateway); err != nil || res.NodeID != f.nodes[1].ID {
		t.Fatalf("wake = %+v, %v; want node 1 while node 0 is under pressure", res, err)
	}
	if err := f.store.HoldComputeNodeCPUPressure(f.ctx, f.nodes[1].ID, until); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.AdmitInstance(f.ctx, app.ID, "", "", sched.TriggerGateway); err != nil {
		t.Fatalf("wake with every node under pressure: %v, want it placed anyway", err)
	}
}

// On a pressured node the ledger withholds the measured CPU discount, so
// the gates count every booking and the refusal says why.
func TestCPUPressureAdmissionCountsEveryBooking(t *testing.T) {
	fill := func(pressured bool) (int, error) {
		sample := &fakeCPUSample{}
		l, clock := newMeasuredCPULedger(sample)
		*l.CPUPressured() = func(string, time.Time) bool { return pressured }
		admitted := 0
		for {
			clock.tick()
			sample.measureAll(admitted, 0, clock.now())
			clock.tick()
			if err := l.Admit(quietReq(admitted)); err != nil {
				return admitted, err
			}
			admitted++
			if admitted > 100 {
				t.Fatal("admission never refused")
			}
		}
	}
	// Idle measured instances: without pressure only the 700% cap binds.
	if got, _ := fill(false); got != 28 {
		t.Fatalf("admitted %d without pressure, want 28", got)
	}
	// Under pressure every 2 vCPU booking counts against the 8 vCPU budget.
	got, err := fill(true)
	if got != 4 || !isCapacityRefusal(err) || !strings.Contains(err.Error(), "CPU pressure") {
		t.Fatalf("admitted %d under pressure, refused with %v; want 4 and a refusal naming CPU pressure", got, err)
	}
	sample := &fakeCPUSample{}
	l, clock := newMeasuredCPULedger(sample)
	*l.CPUPressured() = func(string, time.Time) bool { return true }
	clock.tick()
	if err := l.Admit(quietReq(0)); err != nil {
		t.Fatal(err)
	}
	clock.tick()
	sample.measureAll(1, 0, clock.now())
	if vcpu, cpu := l.CPUDiscount(quietReq(0).NodeID); vcpu != 0 || cpu != 0 {
		t.Fatalf("discount on a pressured node = %d vCPU, %d mc; want none for the durable guard", vcpu, cpu)
	}
}
