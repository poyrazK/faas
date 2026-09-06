// capacity_publisher_test.go — vmmd's capacity-publisher unit
// tests (ADR-025 axis 5).
//
// Scope. The proto-field + control-flow tests here pin the
// pure pieces of the publisher:
//
//  1. buildCapacityReport: the proto field contract.
//  2. buildCapacityReport with non-Linux resident: ok=false
//     emits used_mb=0 (ADR-005 cold-boot fallback).
//  3. buildCapacityReport with over-commit: ram_headroom_mb
//     clamped at 0 (no negative wire value).
//  4. buildCapacityReport vCPU placeholder contract.
//  5. buildCapacityReport sampled_at monotonic within
//     [before, after] window.
//  6. runCapacityPublish with empty target: returns immediately
//     and never dials.
//  7. runCapacityPublish with cancelled ctx: returns within
//     2 s of cancel (no infinite loop).
//
// End-to-end bufconn tests (ticker fires, reconnect after
// server error) live in capacity_publisher_e2e_test.go
// (PR-1 review fix: introduced the countReader seam so the
// end-to-end tests can inject live/leased counts without
// a real *fcvm.Manager).

package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// silentLogger is the slog.Logger used by tests that don't care
// about log output. Discarded via io.Writer sink.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestBuildCapacityReport_NodeIDPropagated asserts the proto's
// node_id matches the publisher's parameter. The publisher
// receives nodeID from registerComputeNode's
// state.ComputeNode.ID (a server-assigned UUID). A regression
// that hard-coded "node-local" or dropped the field would
// break the chooser's per-node table.
func TestBuildCapacityReport_NodeIDPropagated(t *testing.T) {
	t.Parallel()
	var counts countReader // nil — buildCapacityReport reads via interfaces; nil is treated as zero
	resident := func() (map[string]int64, bool) {
		return map[string]int64{
			"i-1": 100 * 1024 * 1024,
			"i-2": 200 * 1024 * 1024,
		}, true
	}
	cfg := ComputeNodeConfig{MemMB: 1000}
	got := buildCapacityReport(context.Background(), nil, counts, "0193f7c0-uuid-7bbb-9def-0123456789ab", cfg, resident, silentLogger())
	if got.GetNodeId() != "0193f7c0-uuid-7bbb-9def-0123456789ab" {
		t.Errorf("node_id = %q, want 0193f7c0-uuid-7bbb-9def-0123456789ab", got.GetNodeId())
	}
}

func TestBuildCapacityReport_BatchesLocalTelemetry(t *testing.T) {
	resident := func() (map[string]int64, bool) {
		return nil, false
	}
	stats := telemetryReader(func(context.Context) (*vmmdpb.StatsResponse, error) {
		return &vmmdpb.StatsResponse{
			TotalResidentBytes: wrapperspb.Int64(256 << 20),
			Instances: []*vmmdpb.InstanceStats{{
				Instance:          "vm-1",
				ResidentBytes:     wrapperspb.Int64(256 << 20),
				CpuPct:            wrapperspb.Double(12.5),
				InflightRequests:  3,
				RequestCountTotal: wrapperspb.Int64(123),
				OpenConns:         5,
				DiskUsedBytes:     wrapperspb.Int64(80),
				DiskCapacityBytes: wrapperspb.Int64(100),
				LastRequestAt:     timestamppb.New(time.Unix(123, 0)),
			}},
		}, nil
	})

	got := buildCapacityReport(context.Background(), nil, nil, "node-a", ComputeNodeConfig{MemMB: 1024}, resident, silentLogger(), stats)
	if got.GetUsedMb() != 256 {
		t.Fatalf("used_mb = %d, want 256 from Stats snapshot", got.GetUsedMb())
	}
	if len(got.GetInstances()) != 1 {
		t.Fatalf("telemetry rows = %d, want 1", len(got.GetInstances()))
	}
	row := got.GetInstances()[0]
	if row.GetInstanceId() != "vm-1" || row.GetInflightRequests() != 3 || row.GetOpenConns() != 5 || row.GetRequestCountTotal() == nil || row.GetRequestCountTotal().GetValue() != 123 || row.GetCpuPct().GetValue() != 12.5 || row.GetDiskUsedBytes().GetValue() != 80 || row.GetDiskCapacityBytes().GetValue() != 100 {
		t.Fatalf("telemetry row = %+v, want vm-1 cpu=12.5 inflight=3 open_conns=5", row)
	}
}

// TestBuildCapacityReport_ResidentBytesSummed asserts the
// publisher sums the per-instance cgroup memory.current
// values across all instances. 2 instances × 100 MiB = 200 MiB.
func TestBuildCapacityReport_ResidentBytesSummed(t *testing.T) {
	t.Parallel()
	resident := func() (map[string]int64, bool) {
		return map[string]int64{
			"i-1": 100 * 1024 * 1024,
			"i-2": 100 * 1024 * 1024,
		}, true
	}
	cfg := ComputeNodeConfig{MemMB: 1000}
	got := buildCapacityReport(context.Background(), nil, nil, "node-1", cfg, resident, silentLogger())
	if got.GetUsedMb() != 200 {
		t.Errorf("used_mb = %d, want 200", got.GetUsedMb())
	}
	if got.GetRamHeadroomMb() != 800 {
		t.Errorf("ram_headroom_mb = %d, want 800", got.GetRamHeadroomMb())
	}
}

// TestBuildCapacityReport_NonLinuxHostEmitsZero asserts that
// when resident() returns ok=false (non-Linux), the report
// carries used_mb=0 and ram_headroom_mb=cfg.MemMB. The
// chooser (PR-2) detects the zero and falls back to the
// store sum — ADR-005.
func TestBuildCapacityReport_NonLinuxHostEmitsZero(t *testing.T) {
	t.Parallel()
	resident := func() (map[string]int64, bool) {
		return nil, false // non-Linux: cgroup read failed
	}
	cfg := ComputeNodeConfig{MemMB: 47600}
	got := buildCapacityReport(context.Background(), nil, nil, "node-1", cfg, resident, silentLogger())
	if got.GetUsedMb() != 0 {
		t.Errorf("used_mb = %d on non-Linux; want 0", got.GetUsedMb())
	}
	if got.GetRamHeadroomMb() != 47600 {
		t.Errorf("ram_headroom_mb = %d on non-Linux; want %d", got.GetRamHeadroomMb(), 47600)
	}
}

// TestBuildCapacityReport_OverCommitClampsAtZero asserts that
// a used_mb exceeding cfg.MemMB clamps ram_headroom_mb at 0
// (rather than emitting a negative value). The chooser treats
// headroom=0 as "saturated" — a useful signal — without
// having to parse a negative int.
func TestBuildCapacityReport_OverCommitClampsAtZero(t *testing.T) {
	t.Parallel()
	resident := func() (map[string]int64, bool) {
		return map[string]int64{"i-1": 2000 * 1024 * 1024}, true // 2000 MiB
	}
	cfg := ComputeNodeConfig{MemMB: 1000} // 1000 MiB cap
	got := buildCapacityReport(context.Background(), nil, nil, "node-1", cfg, resident, silentLogger())
	if got.GetUsedMb() != 2000 {
		t.Errorf("used_mb = %d, want 2000", got.GetUsedMb())
	}
	if got.GetRamHeadroomMb() != 0 {
		t.Errorf("ram_headroom_mb = %d, want 0 (clamped)", got.GetRamHeadroomMb())
	}
}

// TestBuildCapacityReport_VCPUScaledByLiveCount asserts the
// vCPU placeholder is live_count * 2. The chooser ignores
// vcpu_busy today (PR-2); this pin documents the contract so
// a future upgrade to per-cgroup-weight sum is a deliberate
// change.
func TestBuildCapacityReport_VCPUScaledByLiveCount(t *testing.T) {
	t.Parallel()
	// We can't easily inject a non-zero LiveCount without
	// a real manager; verify the formula on the wire
	// field's zero case (live=0 → vcpu_busy=0). The
	// placeholder is a no-op for live=0; the load-bearing
	// path is exercised in the metal suite.
	resident := func() (map[string]int64, bool) { return nil, true }
	got := buildCapacityReport(context.Background(), nil, nil, "node-1", ComputeNodeConfig{MemMB: 1000}, resident, silentLogger())
	if got.GetVcpuBusy() != 0 {
		t.Errorf("vcpu_busy = %d, want 0 (live=0)", got.GetVcpuBusy())
	}
}

// TestBuildCapacityReport_TimeStampMonotonic asserts the
// proto's sampled_at_unix_ms is reasonably close to time.Now
// (within 1 second). The receiving schedd uses the engines
// lastSeen stamp (set on Replace) for freshness, not the
// proto's timestamp, so this is a sanity pin only.
func TestBuildCapacityReport_TimeStampMonotonic(t *testing.T) {
	t.Parallel()
	before := time.Now().UnixMilli()
	got := buildCapacityReport(context.Background(), nil, nil, "node-1", ComputeNodeConfig{MemMB: 1000}, noResident, silentLogger())
	after := time.Now().UnixMilli()
	if got.GetSampledAtUnixMs() < before || got.GetSampledAtUnixMs() > after {
		t.Errorf("sampled_at_unix_ms = %d, want in [%d, %d]", got.GetSampledAtUnixMs(), before, after)
	}
}

// TestRunCapacityPublish_EmptyTargetReturnsImmediately asserts
// the publisher's early-out: empty scheddTarget returns nil
// without dialing or spawning a goroutine. main.go gates this
// on NodeName, but the publisher is defensive — a misconfigured
// environment that injects an empty target must not panic.
func TestRunCapacityPublish_EmptyTargetReturnsImmediately(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	done := make(chan struct{})
	go func() {
		defer close(done)
		runCapacityPublish(context.Background(), nil, "node-1",
			ComputeNodeConfig{MemMB: 1000}, "", nil, 1*time.Second, noResident, nil, "", logger)
	}()
	select {
	case <-done:
		// ok
	case <-time.After(500 * time.Millisecond):
		t.Fatal("empty target did not return within 500ms")
	}
}

// TestRunCapacityPublish_CtxCancelExitsPromptly asserts the
// outer reconnect loop returns within 100ms of ctx cancel.
// Without this guard, a graceful shutdown would block on the
// dialer's 30s timeout.
func TestRunCapacityPublish_CtxCancelExitsPromptly(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		// An invalid target will fail to dial; the loop
		// must surface the cancel promptly without
		// blocking on the dialer's full timeout.
		runCapacityPublish(ctx, nil, "node-1",
			ComputeNodeConfig{MemMB: 1000}, "unix:///nonexistent.sock", nil,
			1*time.Second, noResident, nil, "", logger)
	}()
	// Give the publisher a tick to enter the dial path.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("publisher did not exit within 2s of ctx cancel")
	}
}

// noResident is a residentBytesFn that reports empty/non-Linux.
func noResident() (map[string]int64, bool) { return nil, false }
