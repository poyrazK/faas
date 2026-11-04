// adr: 20261029151111 — under measured CPU admission the CPU gate charges a
// measured instance its measured CPU, the vCPU gate stops counting its guest
// vCPUs, and every booking stays within the overcommit cap.
package sched_test

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// fakeCPUSample is the instance stats poller's latest measured CPU sample.
type fakeCPUSample struct {
	millicores map[string]int
	asOf       time.Time
	ok         bool
}

func (f *fakeCPUSample) read(string) (map[string]int, time.Time, bool) {
	return f.millicores, f.asOf, f.ok
}

func newMeasuredCPULedger(s *fakeCPUSample) (*sched.NodeLedger, *measuredClock) {
	clock := &measuredClock{t: time.Unix(1_800_000_000, 0)}
	l := sched.NewNodeLedger()
	(*l.Now()) = clock.now
	l.EnableMeasuredCPUAdmission(s.read)
	return l, clock
}

// measureAll records every admitted instance at real millicores each, as of
// now.
func (f *fakeCPUSample) measureAll(admitted, millicores int, now time.Time) {
	f.millicores = map[string]int{}
	for i := range admitted {
		f.millicores[fmt.Sprintf("q%d", i)] = millicores
	}
	f.asOf, f.ok = now, true
}

func TestMeasuredCPUAdmission(t *testing.T) {
	for _, tc := range []struct {
		name string
		// used is each measured instance's host CPU millicores.
		used int
		// want is how many 2 vCPU, 1000 millicore instances fit an 8 vCPU,
		// 4000 millicore node when every admitted one is measured.
		want int
	}{
		// 20 real millicores charge 160, so the CPU gate fits 19: a 20th
		// would need 19 × 160 + 1000 > 4000.
		{name: "idle", used: 20, want: 19},
		// Below the floor an instance is charged 10% of its booking, so
		// only the 700% cap binds: 28 × 2 vCPU = 56.
		{name: "below the floor", used: 0, want: 28},
		// 125 real millicores is the booking model's assumption: the CPU
		// gate keeps the booking, though guest vCPU no longer counts.
		{name: "as booked", used: 125, want: 4},
		// A busy instance is never charged more than its booking.
		{name: "busy", used: 1000, want: 4},
		// 60 real millicores charge 480, so the CPU gate fits 7: an 8th
		// would need 7 × 480 + 1000 > 4000.
		{name: "half booked", used: 60, want: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sample := &fakeCPUSample{}
			l, clock := newMeasuredCPULedger(sample)
			admitted := 0
			for {
				clock.tick()
				sample.measureAll(admitted, tc.used, clock.now())
				clock.tick()
				err := l.Admit(quietReq(admitted))
				if err != nil {
					if !isCapacityRefusal(err) {
						t.Fatalf("admit %d: %v, want a capacity refusal", admitted, err)
					}
					break
				}
				admitted++
				if admitted > 100 {
					t.Fatal("admission never refused")
				}
			}
			if admitted != tc.want {
				t.Fatalf("admitted %d instances, want %d", admitted, tc.want)
			}
		})
	}
}

// Only a fresh sample that saw an instance discounts it.
func TestMeasuredCPUAdmissionNeedsTheSample(t *testing.T) {
	sample := &fakeCPUSample{}
	l, clock := newMeasuredCPULedger(sample)
	for i := range 4 {
		clock.tick()
		if err := l.Admit(quietReq(i)); err != nil {
			t.Fatalf("admit %d: %v", i, err)
		}
	}
	// No sample: bookings fill the 8 vCPU budget.
	if err := l.Admit(quietReq(4)); !isCapacityRefusal(err) {
		t.Fatalf("admit without a sample: %v, want a capacity refusal", err)
	}
	// A sample taken before the instances were admitted cannot discount them.
	sample.measureAll(4, 0, clock.now().Add(-time.Hour))
	if err := l.Admit(quietReq(4)); !isCapacityRefusal(err) {
		t.Fatalf("admit on a sample older than the bookings: %v, want a capacity refusal", err)
	}
	sample.measureAll(4, 0, clock.now())
	sample.ok = false
	if err := l.Admit(quietReq(4)); !isCapacityRefusal(err) {
		t.Fatalf("admit on a stale sample: %v, want a capacity refusal", err)
	}
	sample.ok = true
	if err := l.Admit(quietReq(4)); err != nil {
		t.Fatalf("admit on a fresh sample: %v", err)
	}
	// Measured CPU never touches RAM: a 4 GiB request on a 1 GiB node fails.
	big := quietReq(5)
	big.RAMMB, big.NodeCeilingMB = 4096, 1024
	if err := l.Admit(big); !isCapacityRefusal(err) {
		t.Fatalf("admit beyond the RAM ceiling: %v, want a capacity refusal", err)
	}
}

// A quiet-paused instance keeps its whole discount when it is also measured.
func TestMeasuredCPUWithQuietPause(t *testing.T) {
	cpu := &fakeCPUSample{}
	quiet := &fakeQuietSample{paused: map[string]struct{}{}}
	l, clock := newMeasuredCPULedger(cpu)
	l.EnableQuietPauseAdmission(quiet.read)
	for i := range 4 {
		clock.tick()
		if err := l.Admit(quietReq(i)); err != nil {
			t.Fatal(err)
		}
	}
	clock.tick()
	// q0 is paused and measured busy; q1 is measured at 60 real millicores.
	cpu.millicores = map[string]int{"q0": 1000, "q1": 60}
	cpu.asOf, cpu.ok = clock.now(), true
	quiet.paused["q0"] = struct{}{}
	quiet.asOf, quiet.ok = clock.now(), true
	vcpu, millicores := l.CPUDiscount("n1")
	if vcpu != 4 || millicores != 1000+520 {
		t.Fatalf("discount = %d vCPU, %d millicores; want 4 and %d", vcpu, millicores, 1000+520)
	}
	// Placement reads the same discount.
	if got := l.EffectiveCPUMillicoresForNode("n1", 4000, quietCPUBudget); got != 4000-1520 {
		t.Fatalf("effective CPU = %d, want %d", got, 4000-1520)
	}
	if got := l.EffectiveVCPUForNode("n1", quietVCPUBudget); got != 4 {
		t.Fatalf("effective vCPU = %d, want 4", got)
	}
}

// End to end on one node: placement, the ledger and the durable guard agree
// on the measured discount, so a wake the vCPU bookings would refuse lands.
func TestMeasuredCPUAdmissionReachesTheDurableGuard(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	existing, err := mem.ListComputeNodes(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range existing {
		if err := mem.MarkComputeNodeInactive(ctx, n.ID); err != nil {
			t.Fatal(err)
		}
	}
	vcpus := api.MustLimitsFor(api.PlanPro).VCPU
	f := &failoverFleet{store: mem, ctx: ctx, vmm: newRefusingVMM(nil), ops: wire.NewOpsMetrics("schedd")}
	node, err := mem.CreateComputeNode(ctx, state.ComputeNode{
		Name: "measured-cpu", TargetURL: "tcp://10.0.5.2:7000",
		VPCPUs: 80, MemMB: 16384, MaxConcurrency: 50, AdmissionCeilingMB: 8192, VCPUBudget: 2 * vcpus, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.nodes = append(f.nodes, node)
	f.e = sched.XNewEngine(t, mem, f.vmm, &sched.FakeNotifier{}, "1.10.0").
		WithOwnerNodeID(node.ID).WithOpsMetrics(f.ops)
	sample := &fakeCPUSample{millicores: map[string]int{}}
	f.e.Ledger().EnableMeasuredCPUAdmission(sample.read)

	for i := range 2 {
		res, err := f.e.Wake(ctx, f.app(t, api.PlanPro, 256, 1).ID, "", "", sched.TriggerCronSched)
		if err != nil {
			t.Fatalf("wake %d: %v", i, err)
		}
		sample.millicores[res.InstanceID] = 20
	}
	third := f.app(t, api.PlanPro, 256, 1)
	if _, err := f.e.Wake(ctx, third.ID, "", "", sched.TriggerCronSched); !isCapacityRefusal(err) {
		t.Fatalf("wake with the vCPU budget booked and no sample: %v, want a capacity refusal", err)
	}
	sample.asOf, sample.ok = time.Now(), true
	res, err := f.e.Wake(ctx, third.ID, "", "", sched.TriggerCronSched)
	if err != nil {
		t.Fatalf("wake with both booked instances measured idle: %v", err)
	}
	if res.NodeID != node.ID {
		t.Fatalf("wake placed on %s, want %s", res.NodeID, node.ID)
	}
}

// Random admissions, releases, CPU changes and samples. At every admission
// the node's CPU, computed independently from what the sample saw (measured
// charges for instances it measured, bookings for the rest), must fit the
// budget, and the bookings must fit the cap.
func TestMeasuredCPUAdmissionRandomTraffic(t *testing.T) {
	rng := rand.New(rand.NewSource(1029))
	sample := &fakeCPUSample{millicores: map[string]int{}}
	l, clock := newMeasuredCPULedger(sample)
	live := []string{}
	admittedAt := map[string]time.Time{}
	actual := map[string]int{}
	cpuCap := quietCPUBudget * api.MeasuredCPUMaxOvercommitPercent / 100
	vcpuCap := quietVCPUBudget * api.MeasuredCPUMaxOvercommitPercent / 100
	maxLive := 0
	for step := 0; step < 20000; step++ {
		clock.tick()
		switch op := rng.Intn(10); {
		case op < 2: // the poller takes a sample
			sample.millicores = map[string]int{}
			for _, id := range live {
				sample.millicores[id] = actual[id]
			}
			sample.asOf, sample.ok = clock.now(), true
		case op < 3: // the sample goes stale
			sample.ok = false
		case op < 5 && len(live) > 0: // an instance's CPU changes
			actual[live[rng.Intn(len(live))]] = rng.Intn(200)
		case op < 6 && len(live) > 0:
			k := rng.Intn(len(live))
			l.Release(live[k])
			delete(actual, live[k])
			live = append(live[:k], live[k+1:]...)
		default:
			r := quietReq(step)
			used, unmeasuredVCPU := 0, 0
			for _, id := range live {
				m, seen := sample.millicores[id]
				if sample.ok && seen && !admittedAt[id].After(sample.asOf) {
					used += min(max(m*api.CPUOvercommit, 1000*api.MeasuredCPUMinChargePercent/100), 1000)
					continue
				}
				used += 1000
				unmeasuredVCPU += 2
			}
			if err := l.Admit(r); err != nil {
				if !isCapacityRefusal(err) {
					t.Fatalf("step %d: %v", step, err)
				}
				continue
			}
			if used+1000 > quietCPUBudget {
				t.Fatalf("step %d: admitted with %d CPU in use + 1000 over the %d budget", step, used, quietCPUBudget)
			}
			if unmeasuredVCPU+2 > quietVCPUBudget {
				t.Fatalf("step %d: admitted with %d unmeasured vCPU + 2 over the %d budget", step, unmeasuredVCPU, quietVCPUBudget)
			}
			if 1000*(len(live)+1) > cpuCap || 2*(len(live)+1) > vcpuCap {
				t.Fatalf("step %d: %d bookings exceed the %d%% cap", step, len(live)+1, api.MeasuredCPUMaxOvercommitPercent)
			}
			live = append(live, r.Instance)
			admittedAt[r.Instance] = clock.now()
			actual[r.Instance] = rng.Intn(200)
		}
		maxLive = max(maxLive, len(live))
	}
	if maxLive <= quietCPUBudget/1000 {
		t.Fatalf("peak %d live instances, want more than the %d the bookings alone allow", maxLive, quietCPUBudget/1000)
	}
}
