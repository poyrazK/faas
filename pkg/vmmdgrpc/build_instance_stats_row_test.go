// PR-414 I3 (review): whitebox unit test for buildInstanceStatsRow.
//
// The gRPC round-trip test (TestStats_NetTxBytesPopulatedFromCache)
// cannot pin the per-row population on a non-Linux CI box because
// leakcheck.ResidentBytes returns no rows there, so the handler
// returns early before iterating any cache. To pin the wire shape
// "the cache value reaches the wire" on every machine, the per-row
// construction in pkg/vmmdgrpc/server.go:332-380 (before this PR's
// extraction) was hoisted into buildInstanceStatsRow, and this
// file exercises it directly. Whitebox shape (package vmmdgrpc) so
// the helper is reachable without exporting it.
//
// Mirrors pkg/fcvm/cpustats/cache_test.go's "verify the cache
// value reaches the wire" coverage shape.

package vmmdgrpc

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/activity"
	"github.com/onebox-faas/faas/pkg/fcvm/cpustats"
	"github.com/onebox-faas/faas/pkg/fcvm/netstats"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestBuildInstanceStatsRow_PopulatesNetTxBytes drives a Cache
// with two observations (baseline + 4096-byte delta) and asserts
// the assembled InstanceStats row carries net_tx_bytes=4096 via
// the Int64Value wrapper. Pins the wire contract:
//
//   - net_tx_bytes is non-nil when the cache has a Valid reading
//   - the wrapper holds int64(nReading.DeltaBytes), preserving
//     "absent = no data, 0 = real zero-byte delta" semantics
//   - the resident bytes from the caller are always populated
func TestBuildInstanceStatsRow_PopulatesNetTxBytes(t *testing.T) {
	netCache := netstats.New(func() time.Time { return time.Unix(0, 0) })
	netCache.Observe(netstats.Observation{InstanceID: "vm-A", RXBytes: 0, TXBytes: 0, At: time.Unix(0, 0)})
	netCache.Observe(netstats.Observation{InstanceID: "vm-A", RXBytes: 4096, TXBytes: 2048, At: time.Unix(1, 0)})
	netCache.Observe(netstats.Observation{InstanceID: "vm-A", RXBytes: 8192, TXBytes: 3072, At: time.Unix(2, 0)})

	row := buildInstanceStatsRow("vm-A", 8192, nil, netCache, nil, wire.NewOpsMetrics("vmmd_test"))

	if row.GetInstance() != "vm-A" {
		t.Errorf("Instance = %q, want vm-A", row.GetInstance())
	}
	if got := row.GetResidentBytes().Value; got != 8192 {
		t.Errorf("ResidentBytes = %d, want 8192", got)
	}
	if row.NetTxBytes == nil {
		t.Fatalf("NetTxBytes nil, want non-nil wrapper (cache had Valid reading)")
	}
	if got := row.NetTxBytes.Value; got != 8192 {
		t.Errorf("NetTxBytes.Value = %d, want cumulative 8192", got)
	}
	if row.NetRxBytes == nil || row.NetRxBytes.Value != 3072 {
		t.Errorf("NetRxBytes = %v, want cumulative 3072", row.NetRxBytes)
	}
	// CPU fields: nil cache → absent wrappers (mirrors the legacy
	// behaviour: a test that doesn't wire the CPU cache gets
	// baseline/regression semantics on the wire).
	if row.CpuPct != nil {
		t.Errorf("CpuPct = %v, want nil (no cpu cache wired)", row.CpuPct)
	}
	if row.CpuSeconds != nil {
		t.Errorf("CpuSeconds = %v, want nil (no cpu cache wired)", row.CpuSeconds)
	}
}

// TestBuildInstanceStatsRow_AbsentWhenNetCacheEmpty pins the
// absent branch: a freshly-instantiated Cache has no baseline
// for any instance, so buildInstanceStatsRow must leave
// net_tx_bytes unset. The wrapper-nil vs value-0 distinction is
// load-bearing — schedd stamps Unknown on absent and writes 0
// on the wrapped-zero path (matches cpu_pct semantics).
func TestBuildInstanceStatsRow_AbsentWhenNetCacheEmpty(t *testing.T) {
	netCache := netstats.New(func() time.Time { return time.Unix(0, 0) })

	row := buildInstanceStatsRow("vm-A", 4096, nil, netCache, nil, wire.NewOpsMetrics("vmmd_test"))

	if row.NetTxBytes != nil {
		t.Errorf("NetTxBytes = %v, want nil (cache has no baseline for vm-A)", row.NetTxBytes)
	}
}

// TestBuildInstanceStatsRow_NilCachesDoesNotPanic pins the
// "no caches wired" path: both cpuCache and netCache nil must
// produce a row with resident_bytes only, no panic. This is the
// shape tests that don't exercise the caches fall through to.
func TestBuildInstanceStatsRow_NilCachesDoesNotPanic(t *testing.T) {
	row := buildInstanceStatsRow("vm-A", 4096, nil, nil, nil, wire.NewOpsMetrics("vmmd_test"))
	if row.GetInstance() != "vm-A" {
		t.Errorf("Instance = %q, want vm-A", row.GetInstance())
	}
	if got := row.GetResidentBytes().Value; got != 4096 {
		t.Errorf("ResidentBytes = %d, want 4096", got)
	}
	if row.NetTxBytes != nil {
		t.Errorf("NetTxBytes = %v, want nil (no net cache wired)", row.NetTxBytes)
	}
	if row.CpuPct != nil {
		t.Errorf("CpuPct = %v, want nil (no cpu cache wired)", row.CpuPct)
	}
}

// TestBuildInstanceStatsRow_CPUCachePopulatesAllThreeFields
// pins the CPU branch in the helper: when cpuCache is wired
// and the per-instance reading is Valid, all three CPU fields
// (CpuPct, CpuSeconds, CpuThrottledSeconds) are populated.
// This is a regression guard for the existing CPU wire path
// (issue #279 / PR-B) — if anyone breaks the helper and drops
// one of the three fields, this fires.
//
// Two monotonic observations on the same instance, with a
// wall-clock progression of one second, produce a deterministic
// CPUPct and accumulated CPUSeconds / ThrottledSeconds. The
// expected values are computed inline from the same constants
// the cache uses (see pkg/fcvm/cpustats/cache.go:200).
//
// 12.5% over 1s = 125_000 usec (the cache multiplies by 100 to
// get pct, and divides usec by 1e6 to get cpu-seconds: 100 *
// (125_000/1e6) / 1 = 12.5%).
func TestBuildInstanceStatsRow_CPUCachePopulatesAllThreeFields(t *testing.T) {
	now := time.Unix(0, 0)
	cpuCache := cpustats.New(func() time.Time { return now })
	// Baseline: 0 CPU usage, 0 throttle. Subsequent: 12.5%
	// over 1 second → 125_000 usec usage, 0 throttle.
	cpuCache.Observe(cpustats.Observation{
		InstanceID:    "vm-A",
		CPUUsageUsec:  0,
		ThrottledUsec: 0,
		At:            time.Unix(0, 0),
	})
	cpuCache.Observe(cpustats.Observation{
		InstanceID:    "vm-A",
		CPUUsageUsec:  125_000,
		ThrottledUsec: 0,
		At:            time.Unix(1, 0),
	})

	row := buildInstanceStatsRow("vm-A", 4096, cpuCache, nil, nil, wire.NewOpsMetrics("vmmd_test"))

	if row.CpuPct == nil || row.CpuPct.Value != 12.5 {
		t.Errorf("CpuPct = %v, want 12.5", row.CpuPct)
	}
	if row.CpuSeconds == nil || row.CpuSeconds.Value != 0.125 {
		t.Errorf("CpuSeconds = %v, want 0.125", row.CpuSeconds)
	}
	if row.CpuThrottledSeconds == nil || row.CpuThrottledSeconds.Value != 0 {
		t.Errorf("CpuThrottledSeconds = %v, want 0", row.CpuThrottledSeconds)
	}
}

// TestBuildInstanceStatsRow_ActivityPopulatesInflightAndLastAt
// pins the PR-B (issue #462) wire shape: the
// inflight_requests and last_request_at fields reach the wire
// when the activityCache has a row for the instance. Five
// unmatched Begins produce InflightRequests=5 and a
// LastRequestAt equal to the injected clock moment. Schedd
// poller (pkg/sched/instancestats/poller.go:218-219) already
// decodes this shape — the contract here is that buildInstanceStatsRow
// populates it from the cache and that schedd's reader can
// ask MaxInflightForApp for the same shape.
func TestBuildInstanceStatsRow_ActivityPopulatesInflightAndLastAt(t *testing.T) {
	tracker := activity.New(func() time.Time { return time.Unix(1_700_000_000, 0) })
	for i := 0; i < 5; i++ {
		tracker.Begin("vm-A")
	}

	row := buildInstanceStatsRow("vm-A", 8192, nil, nil, tracker, wire.NewOpsMetrics("vmmd_test"))

	if row.GetInstance() != "vm-A" {
		t.Errorf("Instance = %q, want vm-A", row.GetInstance())
	}
	if got := row.GetResidentBytes().Value; got != 8192 {
		t.Errorf("ResidentBytes = %d, want 8192", got)
	}
	if got := row.GetInflightRequests(); got != 5 {
		t.Errorf("InflightRequests = %d, want 5 (5 unmatched Begins)", got)
	}
	if got := row.GetRequestCountTotal().Value; got != 5 {
		t.Errorf("RequestCountTotal = %d, want 5", got)
	}
	gotTime := row.GetLastRequestAt().AsTime()
	if !gotTime.Equal(time.Unix(1_700_000_000, 0)) {
		t.Errorf("LastRequestAt = %v, want %v (the injected clock at Begin time)", gotTime, time.Unix(1_700_000_000, 0))
	}
}

// TestBuildInstanceStatsRow_ActivityAbsentWhenNoBegins pins
// the "never observed" wire shape: an instance the activity
// cache has not seen leaves InflightRequests=0 (bare int64)
// and LastRequestAt=nil. This matches the pre-PR-B wire
// shape exactly — the additive-merge the schedd poller
// relies on. schedd stamps Unknown on absent LastRequestAt
// and treats InflightRequests=0 as a valid "no observation
// yet" reading.
func TestBuildInstanceStatsRow_ActivityAbsentWhenNoBegins(t *testing.T) {
	tracker := activity.New(nil)

	row := buildInstanceStatsRow("vm-A", 4096, nil, nil, tracker, wire.NewOpsMetrics("vmmd_test"))

	if row.GetInflightRequests() != 0 {
		t.Errorf("InflightRequests = %d, want 0 (never observed)", row.GetInflightRequests())
	}
	if row.LastRequestAt != nil {
		t.Errorf("LastRequestAt = %v, want nil (never observed)", row.LastRequestAt)
	}
}
