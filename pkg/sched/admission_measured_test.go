// adr: 909

package sched_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// measuredCeilingMB is the n2 test node's admission ceiling, where 1 GiB
// Node VMs measured ~260 MB each (ADR-909).
const measuredCeilingMB = 8593

func measuredScaleReq(i int) sched.Request {
	return sched.Request{
		Instance: fmt.Sprintf("i%d", i), AppID: fmt.Sprintf("a%d", i), Plan: api.PlanScale,
		RAMMB: 1024, VCPU: 1, MaxConcurrency: 20, NodeID: "n1", NodeCeilingMB: measuredCeilingMB,
	}
}

// fakeMeasurement is a node's latest measured charge, as the instance stats
// poller would publish it.
type fakeMeasurement struct {
	mb   map[string]int
	asOf time.Time
	ok   bool
}

func (f *fakeMeasurement) read(nodeID string) (int, time.Time, bool) {
	mb, found := f.mb[nodeID]
	return mb, f.asOf, f.ok && found
}

type measuredClock struct{ t time.Time }

func (c *measuredClock) now() time.Time { return c.t }
func (c *measuredClock) tick()          { c.t = c.t.Add(time.Second) }

func newMeasuredLedger(m *fakeMeasurement) (*sched.NodeLedger, *measuredClock) {
	clock := &measuredClock{t: time.Unix(1_800_000_000, 0)}
	l := sched.NewNodeLedger()
	(*l.Now()) = clock.now
	if m != nil {
		l.EnableMeasuredAdmission(m.read)
	}
	return l, clock
}

// admitUntilRefused admits Scale instances until the ledger refuses one. Before
// each admission, refresh (when set) publishes a new measurement.
func admitUntilRefused(t *testing.T, l *sched.NodeLedger, clock *measuredClock, refresh func(admitted int)) (int, error) {
	t.Helper()
	for i := 0; i < 100; i++ {
		clock.tick()
		if refresh != nil {
			refresh(i)
			clock.tick()
		}
		if err := l.Admit(measuredScaleReq(i)); err != nil {
			var prob *api.Problem
			if !errors.As(err, &prob) || prob.Code != api.CodeCapacity {
				t.Fatalf("admit %d: want capacity refusal, got %v", i, err)
			}
			return i, err
		}
	}
	t.Fatal("ledger never refused")
	return 0, nil
}

func TestMeasuredAdmission(t *testing.T) {
	cases := []struct {
		name string
		// measurement is nil for a ledger without measured admission.
		measurement *fakeMeasurement
		// perVMMeasuredMB > 0 refreshes the measurement before every
		// admission to perVMMeasuredMB per admitted instance.
		perVMMeasuredMB int
		wantAdmitted    int
		wantErr         string
	}{
		{name: "off keeps reservations", wantAdmitted: 8, wantErr: "resident 8256 MB"},
		{name: "no sample falls back to reservations", measurement: &fakeMeasurement{},
			wantAdmitted: 8, wantErr: "resident 8256 MB"},
		{name: "stale sample falls back to reservations",
			measurement:  &fakeMeasurement{mb: map[string]int{"n1": 0}, ok: false},
			wantAdmitted: 8, wantErr: "resident 8256 MB"},
		{name: "low measured memory admits up to the overcommit cap",
			measurement: &fakeMeasurement{mb: map[string]int{}}, perVMMeasuredMB: 260,
			wantAdmitted: 16, wantErr: "beyond the overcommit allowance"},
		{name: "measured memory near reservations admits like reservations",
			measurement: &fakeMeasurement{mb: map[string]int{}}, perVMMeasuredMB: 1032,
			wantAdmitted: 8, wantErr: "measured 8256 MB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, clock := newMeasuredLedger(tc.measurement)
			var refresh func(int)
			if tc.perVMMeasuredMB > 0 {
				refresh = func(admitted int) {
					tc.measurement.mb["n1"] = admitted * tc.perVMMeasuredMB
					tc.measurement.asOf = clock.now()
					tc.measurement.ok = true
				}
			}
			got, err := admitUntilRefused(t, l, clock, refresh)
			if got != tc.wantAdmitted {
				t.Errorf("admitted %d, want %d", got, tc.wantAdmitted)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("refusal %q does not mention %q", err, tc.wantErr)
			}
			reservationCap := measuredCeilingMB * api.MeasuredAdmissionMaxOvercommitPercent / 100
			if l.ResidentRAMForNode("n1") > reservationCap {
				t.Errorf("reservations %d MB exceed the %d MB overcommit cap", l.ResidentRAMForNode("n1"), reservationCap)
			}
		})
	}
}

// A measurement cannot see instances admitted after it, so they count at
// their full reservation until the next one.
func TestMeasuredAdmissionCountsAdmissionsAfterTheSample(t *testing.T) {
	m := &fakeMeasurement{mb: map[string]int{"n1": 0, "n2": 0}, ok: true}
	l, clock := newMeasuredLedger(m)
	m.asOf = clock.now()
	// Another node's admissions after the sample do not count against n1.
	clock.tick()
	other := measuredScaleReq(99)
	other.NodeID = "n2"
	if err := l.Admit(other); err != nil {
		t.Fatalf("admit on n2: %v", err)
	}
	if got, _ := admitUntilRefused(t, l, clock, nil); got != 8 {
		t.Fatalf("admitted %d before a new sample, want 8 (8 x 1032 MB under %d)", got, measuredCeilingMB)
	}
	// The next sample sees the eight at 260 MB each: six more fit.
	clock.tick()
	m.mb["n1"] = 8 * 260
	m.asOf = clock.now()
	more := 0
	for i := 100; ; i++ {
		clock.tick()
		if err := l.Admit(measuredScaleReq(i)); err != nil {
			break
		}
		more++
	}
	if more != 6 {
		t.Errorf("admitted %d after the sample, want 6 (2080 + 6 x 1032 <= %d)", more, measuredCeilingMB)
	}
}

func TestMeasuredAdmissionRefusesWhenMeasuredExceedsReservations(t *testing.T) {
	// The measurement covers every instance on the node, including ones
	// another schedd placed there, so it can exceed this ledger's view.
	m := &fakeMeasurement{mb: map[string]int{"n1": 8000}, ok: true}
	l, clock := newMeasuredLedger(m)
	m.asOf = clock.now()
	clock.tick()
	err := l.Admit(measuredScaleReq(0))
	if err == nil || !strings.Contains(err.Error(), "measured 8000 MB") {
		t.Fatalf("admit with 8000 MB measured: got %v, want a measured capacity refusal", err)
	}
}

func TestAdmissionUsedMB(t *testing.T) {
	l, clock := newMeasuredLedger(nil)
	for i := 0; i < 3; i++ {
		clock.tick()
		if err := l.Admit(measuredScaleReq(i)); err != nil {
			t.Fatalf("admit: %v", err)
		}
	}
	if got, want := l.AdmissionUsedMB("n1"), l.ResidentRAMForNode("n1"); got != want {
		t.Fatalf("off: AdmissionUsedMB = %d, want reservations %d", got, want)
	}
	m := &fakeMeasurement{mb: map[string]int{"n1": 780}, ok: true, asOf: clock.now()}
	l.EnableMeasuredAdmission(m.read)
	if got := l.AdmissionUsedMB("n1"); got != 780 {
		t.Fatalf("on: AdmissionUsedMB = %d, want measured 780", got)
	}
	clock.tick()
	if err := l.Admit(measuredScaleReq(3)); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if got := l.AdmissionUsedMB("n1"); got != 780+1032 {
		t.Fatalf("on, one admitted after the sample: AdmissionUsedMB = %d, want %d", got, 780+1032)
	}
	if got := l.AdmissionUsedMB("unknown"); got != 0 {
		t.Fatalf("unknown node: AdmissionUsedMB = %d, want 0", got)
	}
}

// Random admit/release traffic against a node whose instances each measure
// 200-400 MB. At every admission the measured charge, plus reservations
// admitted after the sample, stays under the ceiling, and reservations stay
// under the overcommit cap.
func TestMeasuredAdmissionRandomTraffic(t *testing.T) {
	rng := rand.New(rand.NewSource(909))
	m := &fakeMeasurement{mb: map[string]int{}}
	l, clock := newMeasuredLedger(m)
	measuredMB := map[string]int{}
	admittedAt := map[string]time.Time{}
	live := []string{}
	reservationCap := measuredCeilingMB * api.MeasuredAdmissionMaxOvercommitPercent / 100
	sample := func() {
		total := 0
		for _, id := range live {
			total += measuredMB[id]
		}
		m.mb["n1"], m.asOf, m.ok = total, clock.now(), true
	}
	maxLive := 0
	for step := 0; step < 5000; step++ {
		clock.tick()
		switch op := rng.Intn(10); {
		case op < 2:
			sample()
		case op < 4 && len(live) > 0:
			k := rng.Intn(len(live))
			l.Release(live[k])
			live = append(live[:k], live[k+1:]...)
		default:
			r := measuredScaleReq(step)
			// The bound Admit must hold, computed independently.
			used, _, ok := m.read("n1")
			for _, id := range live {
				if !ok {
					used += 1032
				} else if admittedAt[id].After(m.asOf) {
					used += 1032
				}
			}
			if err := l.Admit(r); err != nil {
				continue
			}
			if used+1032 > measuredCeilingMB {
				t.Fatalf("step %d: admitted with %d MB used + 1032 MB over the %d MB ceiling", step, used, measuredCeilingMB)
			}
			live = append(live, r.Instance)
			admittedAt[r.Instance] = clock.now()
			measuredMB[r.Instance] = 200 + rng.Intn(201)
			if got := l.ResidentRAMForNode("n1"); got > reservationCap {
				t.Fatalf("step %d: reservations %d MB exceed the %d MB cap", step, got, reservationCap)
			}
		}
		if len(live) > maxLive {
			maxLive = len(live)
		}
	}
	if maxLive <= measuredCeilingMB/1032 {
		t.Fatalf("peak %d live instances, want more than the %d reservations allow", maxLive, measuredCeilingMB/1032)
	}
}
