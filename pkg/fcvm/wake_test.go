package fcvm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

func wakeReq(id string, snap *Snapshot) WakeRequest {
	// Issue #301 / ADR-044 — Manager.Wake validates req.Plan
	// against api.Plan.Valid() and rejects empty/unknown plans
	// (the wire-side seam at pkg/vmmdgrpc/proto.go::toWakeRequest
	// populates it from CreateFromSnapshotRequest.plan). Tests
	// that don't care which plan tier they exercise use Hobby
	// (the cheapest paid tier — cpu.max = 200ms/100ms).
	return WakeRequest{Instance: id, BaseKey: "/b.ext4", LayerKey: "/l.ext4", VcpuCount: 2, MemSizeMiB: 128, Snapshot: snap, Plan: api.PlanHobby}
}

func TestWakeRestoresUsableSnapshot(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	inst, err := m.Wake(context.Background(), wakeReq("i1", usableSnapshot()))
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if inst.Method != WakeRestore {
		t.Errorf("method = %s, want restore", inst.Method)
	}
	if !vmm.restoredInstance("i1") {
		t.Error("restore was not called")
	}
	if vmm.boots() != 0 {
		t.Error("cold boot should not run when restore succeeds")
	}
}

func TestWakeStaleSnapshotColdBoots(t *testing.T) {
	snap := usableSnapshot()
	snap.Stale = true
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	inst, err := m.Wake(context.Background(), wakeReq("i1", snap))
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if inst.Method != WakeColdBoot {
		t.Errorf("stale snapshot should cold boot, got %s", inst.Method)
	}
	if vmm.restoredInstance("i1") {
		t.Error("stale snapshot must not be restored")
	}
	if vmm.boots() != 1 {
		t.Errorf("expected exactly 1 cold boot, got %d", vmm.boots())
	}
}

func TestWakeVersionMismatchColdBoots(t *testing.T) {
	snap := usableSnapshot()
	snap.FCVersion = "0.0.1" // manager runs testFCVersion
	vmm := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, vmm)

	inst, err := m.Wake(context.Background(), wakeReq("i1", snap))
	if err != nil {
		t.Fatal(err)
	}
	if inst.Method != WakeColdBoot || vmm.restoredInstance("i1") {
		t.Error("version-mismatched snapshot must cold boot, not restore")
	}
}

// TestWakeRestoreFailureFallsBackToColdBoot is the ADR-005 guarantee: a usable
// snapshot that fails to restore must still bring the app up via cold boot, and
// leak nothing.
func TestWakeRestoreFailureFallsBackToColdBoot(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{restoreErr: fmt.Errorf("corrupt mem file")}
	m := newTestManager(run, vmm)

	inst, err := m.Wake(context.Background(), wakeReq("i1", usableSnapshot()))
	if err != nil {
		t.Fatalf("wake should recover via cold boot, got %v", err)
	}
	if inst.Method != WakeColdBoot {
		t.Errorf("fallback method should read cold_boot (so schedd re-snapshots), got %s", inst.Method)
	}
	if !vmm.restoredInstance("i1") {
		t.Error("restore should have been attempted first")
	}
	if vmm.boots() != 1 {
		t.Errorf("expected 1 cold boot after restore failure, got %d", vmm.boots())
	}
	// The half-restored VM must have been killed before cold boot.
	if len(vmm.killed) == 0 {
		t.Error("half-restored VM should be killed before fallback")
	}
	if m.LiveCount() != 1 || m.LeasedCount() != 1 {
		t.Errorf("live=%d leased=%d, want 1/1", m.LiveCount(), m.LeasedCount())
	}
}

// adr: 350 — only an application hook failure receives a customer reason.
func TestWakeRestoreFallbackReasonOnlyForApplicationHook(t *testing.T) {
	tests := []struct {
		name       string
		snapshot   *Snapshot
		restoreErr error
		want       string
	}{
		{name: "hook failure", snapshot: usableSnapshot(), restoreErr: fmt.Errorf("callback /private failed: %w", ErrAfterRestoreHook), want: WakeReasonAfterRestoreFailed},
		{name: "other restore failure", snapshot: usableSnapshot(), restoreErr: fmt.Errorf("corrupt snapshot")},
		{name: "successful restore", snapshot: usableSnapshot()},
		{name: "planned cold boot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestManager(&fakeRunner{}, &fakeVMM{restoreErr: tt.restoreErr})
			inst, err := m.Wake(context.Background(), wakeReq("i1", tt.snapshot))
			if err != nil {
				t.Fatalf("Wake: %v", err)
			}
			if inst.RestoreFallbackReason != tt.want {
				t.Errorf("RestoreFallbackReason = %q, want %q", inst.RestoreFallbackReason, tt.want)
			}
		})
	}
}

// TestWakeTotalFailureNoLeak: restore fails AND cold boot fails => terminal error
// and zero leaked resources.
func TestWakeTotalFailureNoLeak(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{restoreErr: fmt.Errorf("bad snapshot"), bootErr: fmt.Errorf("no kvm")}
	m := newTestManager(run, vmm)
	// Issue #1059 / ADR-127: wire the wake-failure counter so the
	// test can assert the metric surfaces on the operator-facing
	// surface. The fixture below never reaches the
	// setupNetwork / cgroup hook sites — restore fails first, so
	// netns_fail and cgroup_fail fire 0; the cold-boot terminal
	// + restore-fallback both classify as snapshot_restore_err
	// (default branch — neither error carries a typed sentinel
	// nor matches ENOSPC). Expectation: the counter increments
	// TWICE for snapshot_restore_err (once at restore-fallback,
	// once at cold-boot terminal).
	wireOps := wire.NewOpsMetrics("vmmd")
	m.SetWakeFailureMetrics(wireOps)

	if _, err := m.Wake(context.Background(), wakeReq("i1", usableSnapshot())); err == nil {
		t.Fatal("expected terminal error when both restore and cold boot fail")
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leased=%d, want 0 — LEASE LEAK", m.LeasedCount())
	}
	if !run.ran("netns del fc-i1") {
		t.Error("network should be torn down on total wake failure")
	}
	// Scrape the handler and assert the closed-vocabulary reason
	// is on the wire. Per ADR-127 §3.1 the label value MUST be one
	// of the 8 closed reasons; "snapshot_restore_err" is the
	// fallback bucket for any non-sentinel / non-substring error
	// chain, which matches the fakeVMM fixtures above.
	srv := httptest.NewServer(wireOps.Handler())
	defer srv.Close()
	body := getScrapeBody(t, srv.URL)
	want := fmt.Sprintf(`vmmd_wake_failure_total{app="",box=%q,reason="snapshot_restore_err"} 2`, wire.BoxHostname())
	if !strings.Contains(body, want) {
		t.Errorf("missing %q in metrics scrape body:\n%s", want, body)
	}
}

func TestParkSnapshotsAndReleases(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)
	if _, err := m.Wake(context.Background(), wakeReq("i1", nil)); err != nil {
		t.Fatal(err)
	}

	info, err := m.Park(context.Background(), "i1", SnapshotSpec{VMStatePath: "/snap/state", StorageKey: "snap/i1/mem"})
	if err != nil {
		t.Fatalf("park: %v", err)
	}
	if info.MemBytes == 0 {
		t.Error("park should report snapshot size")
	}
	if len(vmm.snapshotted) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(vmm.snapshotted))
	}
	// Invariant §6.2-4: parked app holds zero resident resources.
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Errorf("after park live=%d leased=%d, want 0/0", m.LiveCount(), m.LeasedCount())
	}
	if !run.ran("netns del fc-i1") {
		t.Error("park should tear down the network")
	}
}

func TestParkSnapshotFailureDestroysAndReleases(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{snapErr: fmt.Errorf("disk full")}
	m := newTestManager(run, vmm)
	if _, err := m.Wake(context.Background(), wakeReq("i1", nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Park(context.Background(), "i1", SnapshotSpec{VMStatePath: "/s", StorageKey: "snap/i1/mem"}); err == nil {
		t.Fatal("park should surface the snapshot error")
	}
	// Even on snapshot failure the instance is destroyed (rootfs keeps it
	// cold-bootable) and nothing leaks.
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Errorf("after failed park live=%d leased=%d, want 0/0", m.LiveCount(), m.LeasedCount())
	}
}

func TestParkUnknownInstanceErrors(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if _, err := m.Park(context.Background(), "ghost", SnapshotSpec{VMStatePath: "/s", StorageKey: "snap/ghost/mem"}); err == nil {
		t.Error("parking an unknown instance should error")
	}
}

// getScrapeBody (issue #1059 / ADR-127) fetches the /metrics
// endpoint body via httptest the same way pkg/wire/metrics_test.go's
// render helper does — but kept as a local in-package helper because
// the wire package's render is package-private. Uses
// http.Get on the httptest URL because the underlying promhttp
// handler is a real http.Handler; httptest.NewRecorder would
// short-circuit the wire shape and a regression there would let the
// test pass while prod emits something different.
func getScrapeBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("scrape: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

func TestWakeCompanionSnapshotMemoryFallback(t *testing.T) {
	for _, mb := range []int{0, 128, 192} {
		t.Run(fmt.Sprint(mb), func(t *testing.T) {
			vmm := &fakeVMM{}
			m := newTestManager(&fakeRunner{}, vmm)
			snap := usableSnapshot()
			snap.MemBytes = int64(mb) << 20
			req := wakeReq("companion-memory", snap)
			req.Sidecars = []WorkloadSpec{{Name: "metrics", Type: "sidecar", StorageKey: "metrics.ext4", DriveID: "layer-sidecar-0", RamMB: 64}}
			inst, err := m.Wake(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			want := WakeColdBoot
			if mb == 192 {
				want = WakeRestore
			}
			if inst.Method != want {
				t.Fatalf("wake method = %s, want %s", inst.Method, want)
			}
			if want == WakeColdBoot && vmm.coldBootSpecs[0].MemSizeMiB != 192 {
				t.Fatalf("fallback memory = %d, want 192", vmm.coldBootSpecs[0].MemSizeMiB)
			}
		})
	}
}
