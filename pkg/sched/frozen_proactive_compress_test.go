// adr: 20261031143521 — on a node near its RAM ceiling the reaper compresses
// frozen VMs, longest frozen first and only those frozen long enough, a few
// per tick and each once, until the node is back under the low-water mark;
// a node that cannot compress backs off.
package sched_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// compressFleet is an engine whose ledger holds frozen reservations on one
// node with a 1,000 MB ceiling, and whose compression call is recorded.
type compressFleet struct {
	e       *sched.Engine
	now     time.Time
	calls   []string
	outcome string
	// shrinkTo is the charge a compressed VM books.
	shrinkTo int
}

func newCompressFleet(t *testing.T) *compressFleet {
	t.Helper()
	f := &compressFleet{
		e:        sched.XNewEngine(t, state.NewMemStore(), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0"),
		now:      time.Unix(1_800_000_000, 0),
		outcome:  "compressed",
		shrinkTo: api.FrozenChargeFloorMB,
	}
	*f.e.Ledger().Now() = func() time.Time { return f.now }
	*f.e.FrozenCompressOne() = func(_ context.Context, id string) string {
		f.calls = append(f.calls, id)
		if f.outcome == "compressed" {
			f.e.Ledger().ShrinkFrozenCharge(id, f.shrinkTo)
		}
		return f.outcome
	}
	f.e.EnableFrozenProactiveCompress()
	return f
}

// freeze books a frozen reservation of chargeMB, frozen at the fleet's
// current time.
func (f *compressFleet) freeze(t *testing.T, id string, chargeMB int) {
	t.Helper()
	if err := f.e.Ledger().Admit(sched.Request{Instance: id, AppID: "app-" + id, Plan: api.PlanScale, RAMMB: 1024, VCPU: 2,
		CPUMillicores: 1000, MaxConcurrency: 4, NodeID: "n1", NodeCeilingMB: 1000, Kind: sched.KindFrozen, FrozenChargeMB: chargeMB}); err != nil {
		t.Fatal(err)
	}
}

func (f *compressFleet) tick(d time.Duration) {
	f.now = f.now.Add(d)
	f.e.CompressFrozenForDensity(context.Background(), f.now)
}

func TestProactiveCompressStopsUnderTheLowWaterMark(t *testing.T) {
	f := newCompressFleet(t)
	for i := range 6 { // 6 x 150 MB = 900 MB, 90% of the ceiling
		f.freeze(t, fmt.Sprintf("v%d", i), 150)
		f.now = f.now.Add(time.Second)
	}
	f.tick(2 * time.Minute)
	// 900 -> 758 after v0 (still >= 750), -> 616 after v1: under 75%, stop.
	if len(f.calls) != 2 || f.calls[0] != "v0" || f.calls[1] != "v1" {
		t.Fatalf("compressed %v, want the two longest frozen, v0 then v1", f.calls)
	}
}

func TestProactiveCompressWaitsForTheHighWaterMark(t *testing.T) {
	f := newCompressFleet(t)
	for i := range 5 { // 5 x 160 MB = 800 MB, 80% of the ceiling
		f.freeze(t, fmt.Sprintf("v%d", i), 160)
	}
	f.tick(2 * time.Minute)
	if len(f.calls) != 0 {
		t.Fatalf("compressed %v under the high-water mark", f.calls)
	}
}

func TestProactiveCompressSkipsRecentlyFrozenVMs(t *testing.T) {
	f := newCompressFleet(t)
	for i := range 6 {
		f.freeze(t, fmt.Sprintf("v%d", i), 150)
	}
	f.tick(time.Duration(api.FrozenCompressMinAgeSeconds-1) * time.Second)
	if len(f.calls) != 0 {
		t.Fatalf("compressed %v, all frozen under %d s ago", f.calls, api.FrozenCompressMinAgeSeconds)
	}
	f.tick(time.Second)
	if len(f.calls) == 0 {
		t.Fatal("nothing compressed once the VMs were old enough")
	}
}

func TestProactiveCompressLimitsEachTickAndAsksEachVMOnce(t *testing.T) {
	f := newCompressFleet(t)
	f.shrinkTo = 90    // each compression frees only 10 MB
	for i := range 9 { // 9 x 100 MB = 900 MB
		f.freeze(t, fmt.Sprintf("v%d", i), 100)
		f.now = f.now.Add(time.Second)
	}
	f.tick(2 * time.Minute)
	if len(f.calls) != api.FrozenCompressPerNodePerTick {
		t.Fatalf("compressed %d VMs in one tick, want at most %d", len(f.calls), api.FrozenCompressPerNodePerTick)
	}
	f.tick(time.Second)
	seen := map[string]bool{}
	for _, id := range f.calls {
		if seen[id] {
			t.Fatalf("asked about %s twice: %v", id, f.calls)
		}
		seen[id] = true
	}
	if len(f.calls) != 2*api.FrozenCompressPerNodePerTick {
		t.Fatalf("compressed %v over two ticks", f.calls)
	}
}

func TestProactiveCompressBacksOffANodeThatCannotCompress(t *testing.T) {
	f := newCompressFleet(t)
	f.outcome = "unavailable"
	for i := range 6 {
		f.freeze(t, fmt.Sprintf("v%d", i), 150)
	}
	f.tick(2 * time.Minute)
	if len(f.calls) != 1 {
		t.Fatalf("asked %v; want one attempt, then the node backs off", f.calls)
	}
	f.tick(time.Duration(api.FrozenCompressUnavailableBackoffSeconds-1) * time.Second)
	if len(f.calls) != 1 {
		t.Fatalf("asked %v during the backoff", f.calls)
	}
	f.tick(time.Second)
	if len(f.calls) != 2 || f.calls[1] != "v0" {
		t.Fatalf("asked %v after the backoff; want v0 again, it was never compressed", f.calls)
	}
}
