package meter

// Test fakes shared across the meter package's tests
// (sampler_test.go, sampler_cpu_test.go, …). The package
// declaration matches the internal tests in the same directory so
// the fakes can be referenced without exporting them as public
// types — they're test-internal only.
//
// New shared fakes go here; per-test scaffolding (seed
// functions, table data) stays in the test file that uses it.

import (
	"sync"
)

// fakeCPUSource is a programmatic CPUSource for sampler tests. The
// reader is keyed by instanceID; per instance, the source returns
// the curr reading on every call. Tests advance curr between
// SampleAndRoll calls to drive the delta.
//
// Missing is a separate set of instanceIDs the source returns
// ok=false for, used to exercise the "no row" branch in
// cpuDeltaForMinute (issue #279 / PR-B / ADR-039 §3.1).
type fakeCPUSource struct {
	mu      sync.Mutex
	values  map[string]uint64
	missing map[string]struct{}
}

func newFakeCPUSource() *fakeCPUSource {
	return &fakeCPUSource{
		values:  map[string]uint64{},
		missing: map[string]struct{}{},
	}
}

// Set programs the source to return (curr, true) for instanceID.
func (f *fakeCPUSource) Set(instanceID string, curr uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[instanceID] = curr
	delete(f.missing, instanceID)
}

// SetMissing programs the source to return (0, false) for
// instanceID. Used to exercise the "schedd reader has no row"
// branch in cpuDeltaForMinute.
func (f *fakeCPUSource) SetMissing(instanceID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.values, instanceID)
	f.missing[instanceID] = struct{}{}
}

// CPUUsageUsec satisfies the meter.CPUSource interface.
func (f *fakeCPUSource) CPUUsageUsec(instanceID string) (uint64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.missing[instanceID]; ok {
		return 0, false
	}
	v, ok := f.values[instanceID]
	return v, ok
}

// fakeEgressSource is a programmatic EgressSource for sampler
// tests (ADR-046, step 11). Reader is keyed by instanceID;
// Set programs the per-tick (txBytes, netTxBytes) values and
// SetMissing exercises the "no source wired" branch (which
// returns 0, 0, false — the legacy PR-1 contract).
type fakeEgressSource struct {
	mu      sync.Mutex
	values  map[string]UsageDeltas
	missing map[string]struct{}
}

func newFakeEgressSource() *fakeEgressSource {
	return &fakeEgressSource{
		values:  map[string]UsageDeltas{},
		missing: map[string]struct{}{},
	}
}

// Set programs the source to return (tx, net, true) for
// instanceID. tx is the gateway response byte count for this
// tick; net is the root-side vethHost.rx_bytes delta.
func (f *fakeEgressSource) Set(instanceID string, tx, net uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[instanceID] = UsageDeltas{TXBytes: tx, NetTXBytes: net}
	delete(f.missing, instanceID)
}

func (f *fakeEgressSource) SetUsage(instanceID string, usage UsageDeltas) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[instanceID] = usage
	delete(f.missing, instanceID)
}

// SetMissing programs the source to return (0, 0, false) for
// instanceID. Used to exercise the "no row for this tick"
// branch — the sampler writes 0 to BOTH egress columns in
// that case (mirrors the cpu path's contract).
func (f *fakeEgressSource) SetMissing(instanceID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.values, instanceID)
	f.missing[instanceID] = struct{}{}
}

// ReadUsageDeltas satisfies the meter.EgressSource interface.
func (f *fakeEgressSource) ReadUsageDeltas(instanceID string) (UsageDeltas, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.missing[instanceID]; ok {
		return UsageDeltas{}, false
	}
	v, ok := f.values[instanceID]
	if !ok {
		return UsageDeltas{}, false
	}
	return v, true
}
