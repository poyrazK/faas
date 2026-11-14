// adr: 20261027090015 — under measured charge an agent run VM books a
// reservation from its version's peak and counts against the measured
// ceiling at its memory or that peak; invariant 2's bounds still hold.
package sched_test

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// fakeAgentMemory is every VM's latest memory sample on a node.
type fakeAgentMemory struct {
	mb   map[string]float64
	asOf time.Time
	ok   bool
}

func (f *fakeAgentMemory) read(string) (map[string]float64, time.Time, bool) {
	return f.mb, f.asOf, f.ok
}

func agentChargeReq(i, ramMB, peakMB int) sched.Request {
	return sched.Request{Instance: fmt.Sprintf("agent-%d", i), Plan: api.PlanScale, RAMMB: ramMB, VCPU: 1, VCPUBudget: 100_000,
		NodeID: "n1", NodeCeilingMB: measuredCeilingMB, Kind: sched.KindAgentRun, AgentPeakMB: peakMB}
}

// admitAgentRuns admits runs until the ledger refuses one and returns how
// many it took.
func admitAgentRuns(t *testing.T, l *sched.NodeLedger, clock *measuredClock, from, ramMB, peakMB int) int {
	t.Helper()
	for i := 0; i < 500; i++ {
		clock.tick()
		if err := l.Admit(agentChargeReq(from+i, ramMB, peakMB)); err != nil {
			return i
		}
	}
	t.Fatal("the ledger never refused an agent run")
	return 0
}

func TestAgentMeasuredCharge(t *testing.T) {
	for _, tc := range []struct {
		name       string
		charge     bool
		fresh      bool
		ram, peak  int
		want       int
		wantBooked int
	}{
		// 27 x 308 MB charged <= 8,593; 27 x 458 MB booked.
		{name: "history charges the peak", charge: true, fresh: true, ram: 2048, peak: 300, want: 27, wantBooked: 27 * 458},
		{name: "no history books the plan", charge: true, fresh: true, ram: 2048, want: 4, wantBooked: 4 * 2056},
		{name: "switch off books the plan", fresh: true, ram: 2048, peak: 300, want: 4, wantBooked: 4 * 2056},
		{name: "no fresh measurement counts the plan", charge: true, ram: 2048, peak: 300, want: 4, wantBooked: 4 * 458},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &fakeMeasurement{mb: map[string]int{"n1": 0}, ok: tc.fresh}
			l, clock := newMeasuredLedger(m)
			m.asOf = clock.now()
			if tc.charge {
				l.EnableAgentMeasuredCharge((&fakeAgentMemory{}).read)
			}
			if got := admitAgentRuns(t, l, clock, 0, tc.ram, tc.peak); got != tc.want {
				t.Fatalf("admitted %d runs, want %d", got, tc.want)
			}
			if got := l.ResidentRAMForNode("n1"); got != tc.wantBooked {
				t.Fatalf("booked %d MB, want %d", got, tc.wantBooked)
			}
		})
	}
}

// A memory sample above a run's peak raises its charge, and a sample that
// cannot have seen a run leaves it at its peak.
func TestAgentMeasuredChargeFollowsSamples(t *testing.T) {
	m := &fakeMeasurement{mb: map[string]int{"n1": 0}, ok: true}
	l, clock := newMeasuredLedger(m)
	m.asOf = clock.now()
	samples := &fakeAgentMemory{mb: map[string]float64{}, ok: true}
	l.EnableAgentMeasuredCharge(samples.read)
	for i := 0; i < 10; i++ {
		clock.tick()
		if err := l.Admit(agentChargeReq(i, 2048, 300)); err != nil {
			t.Fatal(err)
		}
	}
	if used := l.AdmissionUsedMB("n1"); used != 10*308 {
		t.Fatalf("used %d MB, want ten peaks of 308", used)
	}
	clock.tick()
	samples.asOf = clock.now()
	for i := 0; i < 10; i++ {
		samples.mb[fmt.Sprintf("agent-%d", i)] = 700
	}
	if used := l.AdmissionUsedMB("n1"); used != 10*708 {
		t.Fatalf("used %d MB after the samples, want ten of 708", used)
	}
	// Four more fit: 7,080 + 4 x 308 <= 8,593 < 7,080 + 5 x 308.
	if got := admitAgentRuns(t, l, clock, 100, 2048, 300); got != 4 {
		t.Fatalf("admitted %d more runs, want 4", got)
	}
}

// Invariant 2 on an agent node (ADR-20261027090015 §3) over random
// admissions, releases and samples: an admission leaves the measured figure
// within the ceiling, reservations within the overcommit bound, and each
// run books at most ram_mb + 8.
func TestPropertyAgentMeasuredChargeBounds(t *testing.T) {
	r := rand.New(rand.NewSource(20261027090015))
	for trial := 0; trial < 40; trial++ {
		m := &fakeMeasurement{mb: map[string]int{"n1": r.Intn(2000)}, ok: true}
		l, clock := newMeasuredLedger(m)
		m.asOf = clock.now()
		samples := &fakeAgentMemory{mb: map[string]float64{}, ok: true}
		l.EnableAgentMeasuredCharge(samples.read)
		live := map[string]int{}
		for step := 0; step < 200; step++ {
			clock.tick()
			switch op := r.Intn(10); {
			case op < 6:
				ram := []int{512, 1024, 2048, 4096}[r.Intn(4)]
				peak := 0
				if r.Intn(4) > 0 {
					peak = 1 + r.Intn(ram)
				}
				req := agentChargeReq(trial*1000+step, ram, peak)
				before := l.ResidentRAMForNode("n1")
				if err := l.Admit(req); err != nil {
					continue
				}
				live[req.Instance] = ram
				if booked := l.ResidentRAMForNode("n1") - before; booked > api.BillableRAMMB(ram) {
					t.Fatalf("trial %d: a %d MB run booked %d MB", trial, ram, booked)
				}
				if used := l.AdmissionUsedMB("n1"); used > measuredCeilingMB {
					t.Fatalf("trial %d: an admission left %d MB measured over the %d MB ceiling", trial, used, measuredCeilingMB)
				}
			case op < 8:
				for id := range live {
					l.Release(id)
					delete(live, id)
					break
				}
			default:
				samples.asOf = clock.now()
				for id, ram := range live {
					samples.mb[id] = float64(r.Intn(ram + 1))
				}
			}
			if booked := l.ResidentRAMForNode("n1"); booked > 2*measuredCeilingMB {
				t.Fatalf("trial %d: %d MB booked over twice the ceiling", trial, booked)
			}
		}
	}
}
