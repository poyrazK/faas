// adr: 20261027090013 §5 — a sandbox session VM is measured like an agent
// run VM, and its quiet-paused and frozen seconds are not billed (D15).
package fcvm

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Frozen time is paused time, reported apart, and never billable.
func TestSandboxUsageMeterExcludesFrozenTime(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var quiet, frozen time.Duration
	read := func(Lease) (int64, error) { return 500 * mib, nil }
	meter := newAgentUsageMeter(read, func(time.Time) time.Duration { return quiet + frozen }, Lease{}, false, now)
	meter.frozen = func(time.Time) time.Duration { return frozen }
	for _, s := range []struct{ quiet, frozen time.Duration }{
		{0, 0},                      // running
		{time.Second, 0},            // quiet-paused
		{0, time.Second},            // frozen
		{0, 500 * time.Millisecond}, // frozen half the interval, then thawed
	} {
		now = now.Add(time.Second)
		quiet += s.quiet
		frozen += s.frozen
		meter.sample(now)
	}
	got := meter.snapshot()
	overhead := int64(api.PerVMOverheadMB)
	want := AgentRunUsage{Method: AgentRunUsageMethod, Samples: 4, ExpectedSamples: 4, MeasuredMillis: 4000,
		PausedMillis: 2500, FrozenMillis: 1500, PrivateMBMillis: 2_000_000, UnpausedPrivateMBMillis: 750_000,
		BillableMBMillis: (500 + overhead) * 1500, PeakPrivateMB: 500}
	if got != want {
		t.Fatalf("usage = %+v\nwant    %+v", got, want)
	}
}

// The frozen ledger sums completed freezes and the current one.
func TestFrozenTimeLedger(t *testing.T) {
	m := &Manager{live: map[string]*Instance{}}
	inst := &Instance{}
	m.live["sandbox-1"] = inst
	t0 := time.Unix(1_800_000_000, 0)
	inst.Frozen, inst.FrozenSince = true, t0
	if got := m.FrozenTime("sandbox-1", t0.Add(3*time.Second)); got != 3*time.Second {
		t.Fatalf("frozen so far = %v", got)
	}
	endFrozenLocked(inst, t0.Add(5*time.Second))
	inst.Frozen, inst.FrozenSince = true, t0.Add(10*time.Second)
	if got := m.FrozenTime("sandbox-1", t0.Add(12*time.Second)); got != 7*time.Second {
		t.Fatalf("frozen total = %v, want 7s", got)
	}
	endFrozenLocked(inst, t0.Add(12*time.Second))
	if got := m.FrozenTime("sandbox-1", t0.Add(60*time.Second)); got != 7*time.Second || inst.Frozen {
		t.Fatalf("after thaw = %v frozen=%v", got, inst.Frozen)
	}
	if got := m.FrozenTime("gone", t0); got != 0 {
		t.Fatalf("unknown instance = %v", got)
	}
}
