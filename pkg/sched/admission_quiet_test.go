// adr: 1151 — under quiet pause admission the vCPU and CPU gates count only
// active instances, and every booking stays within the overcommit cap.
package sched_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

const (
	quietVCPUBudget = 8
	quietCPUBudget  = 4000
)

// quietReq books 2 vCPU and 1000 millicores on node n1.
func quietReq(i int) sched.Request {
	return sched.Request{
		Instance: fmt.Sprintf("q%d", i), AppID: fmt.Sprintf("a%d", i), Plan: api.PlanScale,
		RAMMB: 128, VCPU: 2, CPUMillicores: 1000, MaxConcurrency: 20, NodeID: "n1",
		NodeCeilingMB: 1 << 20, VCPUBudget: quietVCPUBudget, CPUBudgetMillicores: quietCPUBudget,
	}
}

// fakeQuietSample is the instance stats poller's latest quiet pause sample.
type fakeQuietSample struct {
	paused map[string]struct{}
	asOf   time.Time
	ok     bool
}

func (f *fakeQuietSample) read(string) (map[string]struct{}, time.Time, bool) {
	return f.paused, f.asOf, f.ok
}

func newQuietLedger(s *fakeQuietSample) (*sched.NodeLedger, *measuredClock) {
	clock := &measuredClock{t: time.Unix(1_800_000_000, 0)}
	l := sched.NewNodeLedger()
	(*l.Now()) = clock.now
	if s != nil {
		l.EnableQuietPauseAdmission(s.read)
	}
	return l, clock
}

func isCapacityRefusal(err error) bool {
	var prob *api.Problem
	return errors.As(err, &prob) && prob.Code == api.CodeCapacity
}

func TestQuietEffective(t *testing.T) {
	for _, tc := range []struct {
		booked, discount, budget, want int
	}{
		{booked: 8, discount: 0, budget: 8, want: 8},
		{booked: 8, discount: 6, budget: 8, want: 2},
		{booked: 20, discount: 18, budget: 8, want: 4}, // the cap binds: 20 - 2*8
		{booked: 24, discount: 24, budget: 8, want: 8}, // all paused, at the cap
		{booked: 10, discount: 4, budget: 0, want: 10}, // no budget: bookings
	} {
		if got := sched.QuietEffective(tc.booked, tc.discount, tc.budget); got != tc.want {
			t.Errorf("quietEffective(%d, %d, %d) = %d, want %d", tc.booked, tc.discount, tc.budget, got, tc.want)
		}
	}
}

func TestQuietPauseAdmission(t *testing.T) {
	sample := &fakeQuietSample{paused: map[string]struct{}{}}
	l, clock := newQuietLedger(sample)
	admitted := 0
	admit := func() error {
		clock.tick()
		err := l.Admit(quietReq(admitted))
		if err == nil {
			admitted++
		}
		return err
	}
	// Without a sample the gates count every booking: 4 x 2 vCPU fill 8.
	for i := 0; i < 4; i++ {
		if err := admit(); err != nil {
			t.Fatalf("admit %d: %v", i, err)
		}
	}
	if err := admit(); !isCapacityRefusal(err) {
		t.Fatalf("fifth admit without a sample: %v, want a capacity refusal", err)
	}
	// The poller sees all four paused: they no longer count.
	clock.tick()
	for i := 0; i < 4; i++ {
		sample.paused[fmt.Sprintf("q%d", i)] = struct{}{}
	}
	sample.asOf, sample.ok = clock.now(), true
	for i := 0; i < 4; i++ {
		if err := admit(); err != nil {
			t.Fatalf("admit with four paused, #%d: %v", i, err)
		}
	}
	// The four new ones were admitted after the sample, so they count.
	if err := admit(); !isCapacityRefusal(err) {
		t.Fatalf("admit beyond the active budget: %v, want a capacity refusal", err)
	}
	// Pause everything: bookings may grow to the 300% cap and no further.
	clock.tick()
	for i := 0; i < admitted; i++ {
		sample.paused[fmt.Sprintf("q%d", i)] = struct{}{}
	}
	sample.asOf = clock.now()
	capInstances := quietVCPUBudget * api.QuietPauseMaxVCPUOvercommitPercent / 100 / 2
	for admitted < capInstances {
		if err := admit(); err != nil {
			t.Fatalf("admit %d below the cap: %v", admitted, err)
		}
		clock.tick()
		sample.paused[fmt.Sprintf("q%d", admitted-1)] = struct{}{}
		sample.asOf = clock.now()
	}
	if err := admit(); !isCapacityRefusal(err) {
		t.Fatalf("admit beyond the %d%% cap: %v, want a capacity refusal", api.QuietPauseMaxVCPUOvercommitPercent, err)
	}
	if got := l.UsedVCPUForNode("n1"); got != capInstances*2 {
		t.Fatalf("booked vCPU = %d, want %d", got, capInstances*2)
	}
	// A stale or missing sample falls back to bookings.
	sample.ok = false
	if got := l.EffectiveVCPUForNode("n1", quietVCPUBudget); got != capInstances*2 {
		t.Fatalf("effective vCPU without a sample = %d, want the bookings %d", got, capInstances*2)
	}
}

func TestQuietPauseAdmissionWithoutSwitch(t *testing.T) {
	l, clock := newQuietLedger(nil)
	for i := 0; i < 4; i++ {
		clock.tick()
		if err := l.Admit(quietReq(i)); err != nil {
			t.Fatalf("admit %d: %v", i, err)
		}
	}
	if err := l.Admit(quietReq(4)); !isCapacityRefusal(err) {
		t.Fatalf("admit beyond the budget with quiet pause admission off: %v", err)
	}
}

// Placement and Admit must agree on what a node has used.
func TestEffectiveForNodeMatchesAdmit(t *testing.T) {
	sample := &fakeQuietSample{paused: map[string]struct{}{}}
	l, clock := newQuietLedger(sample)
	for i := 0; i < 4; i++ {
		clock.tick()
		if err := l.Admit(quietReq(i)); err != nil {
			t.Fatal(err)
		}
	}
	clock.tick()
	sample.paused["q0"], sample.paused["q1"] = struct{}{}, struct{}{}
	sample.asOf, sample.ok = clock.now(), true
	if got := l.EffectiveVCPUForNode("n1", quietVCPUBudget); got != 4 {
		t.Fatalf("effective vCPU = %d, want 4 (8 booked, 4 paused)", got)
	}
	if got := l.EffectiveCPUMillicoresForNode("n1", 4000, quietCPUBudget); got != 2000 {
		t.Fatalf("effective CPU = %d, want 2000", got)
	}
	// Placement may read a larger fleet-wide booking than this ledger's.
	if got := l.EffectiveCPUMillicoresForNode("n1", 9000, quietCPUBudget); got != 7000 {
		t.Fatalf("effective CPU from a fleet view = %d, want 7000", got)
	}
}

// Random admissions, releases, pauses, resumes and samples. At every
// admission the node's active vCPU, computed independently from what the
// sample saw, must fit the budget, and its bookings must fit the cap.
func TestQuietPauseAdmissionRandomTraffic(t *testing.T) {
	rng := rand.New(rand.NewSource(957))
	sample := &fakeQuietSample{paused: map[string]struct{}{}}
	l, clock := newQuietLedger(sample)
	live := []string{}
	admittedAt := map[string]time.Time{}
	paused := map[string]bool{}
	vcpuCap := quietVCPUBudget * api.QuietPauseMaxVCPUOvercommitPercent / 100
	maxLive := 0
	for step := 0; step < 20000; step++ {
		clock.tick()
		switch op := rng.Intn(10); {
		case op < 2: // the poller takes a sample
			sample.paused = map[string]struct{}{}
			for _, id := range live {
				if paused[id] {
					sample.paused[id] = struct{}{}
				}
			}
			sample.asOf, sample.ok = clock.now(), true
		case op < 4 && len(live) > 0: // an instance pauses or resumes
			id := live[rng.Intn(len(live))]
			paused[id] = !paused[id]
		case op < 5 && len(live) > 0:
			k := rng.Intn(len(live))
			l.Release(live[k])
			delete(paused, live[k])
			live = append(live[:k], live[k+1:]...)
		default:
			r := quietReq(step)
			booked, active := 2*len(live), 2*len(live)
			if sample.ok {
				for id := range sample.paused {
					if !admittedAt[id].IsZero() && !admittedAt[id].After(sample.asOf) {
						active -= 2
					}
				}
			}
			if err := l.Admit(r); err != nil {
				if !isCapacityRefusal(err) {
					t.Fatalf("step %d: %v", step, err)
				}
				continue
			}
			if active+2 > quietVCPUBudget {
				t.Fatalf("step %d: admitted with %d active vCPU + 2 over the %d budget", step, active, quietVCPUBudget)
			}
			if booked+2 > vcpuCap {
				t.Fatalf("step %d: bookings %d + 2 exceed the %d vCPU cap", step, booked, vcpuCap)
			}
			live = append(live, r.Instance)
			admittedAt[r.Instance] = clock.now()
		}
		maxLive = max(maxLive, len(live))
	}
	if maxLive <= quietVCPUBudget/2 {
		t.Fatalf("peak %d live instances, want more than the %d the budget alone allows", maxLive, quietVCPUBudget/2)
	}
}
