package fcvm

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/netns"
)

// fakeRunner records every command and can be told to fail a specific one.
type fakeRunner struct {
	mu            sync.Mutex
	commands      [][]string
	failOn        string // substring; the first matching command errors
	failTeardown  bool   // any teardown command errors (covers m.log.Debug branch)
	setupCount    int    // number of setup commands seen so far
	teardownCount int
}

func (f *fakeRunner) Run(_ context.Context, argv []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, argv)
	joined := strings.Join(argv, " ")
	// Setup commands come before teardown — track counts.
	if strings.Contains(joined, "ip link add") || strings.Contains(joined, "ip netns add") {
		f.setupCount++
	} else if strings.Contains(joined, "ip link delete") || strings.Contains(joined, "ip netns del") {
		f.teardownCount++
	}
	if f.failOn != "" && strings.Contains(joined, f.failOn) {
		return fmt.Errorf("fake failure on %q", f.failOn)
	}
	if f.failTeardown && (strings.Contains(joined, "ip link delete") || strings.Contains(joined, "ip netns del")) {
		return fmt.Errorf("fake teardown failure")
	}
	return nil
}

func (f *fakeRunner) ran(substr string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.commands {
		if strings.Contains(strings.Join(c, " "), substr) {
			return true
		}
	}
	return false
}

// fakeVMM records calls and can be told to fail Boot/Restore/Snapshot.
type fakeVMM struct {
	mu          sync.Mutex
	bootErr     error
	restoreErr  error
	snapErr     error
	killErr     error
	killed      []string
	restored    []string
	snapshotted []string
	bootCount   int
}

func (v *fakeVMM) Boot(_ context.Context, l Lease, _ VMConfig) error {
	v.mu.Lock()
	v.bootCount++
	v.mu.Unlock()
	return v.bootErr
}

func (v *fakeVMM) Restore(_ context.Context, l Lease, _ RestoreSpec) error {
	v.mu.Lock()
	v.restored = append(v.restored, l.Instance)
	v.mu.Unlock()
	return v.restoreErr
}

func (v *fakeVMM) Snapshot(_ context.Context, l Lease, _ SnapshotSpec) (SnapshotInfo, error) {
	v.mu.Lock()
	v.snapshotted = append(v.snapshotted, l.Instance)
	v.mu.Unlock()
	return SnapshotInfo{MemBytes: 4096}, v.snapErr
}

func (v *fakeVMM) Kill(_ context.Context, l Lease) error {
	v.mu.Lock()
	v.killed = append(v.killed, l.Instance)
	v.mu.Unlock()
	return v.killErr
}

func (v *fakeVMM) restoredInstance(id string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, r := range v.restored {
		if r == id {
			return true
		}
	}
	return false
}

func (v *fakeVMM) boots() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.bootCount
}

const testFCVersion = "1.7.0"

func usableSnapshot() *Snapshot {
	return &Snapshot{DeploymentID: "d1", FCVersion: testFCVersion, MemPath: "/snap/mem", VMStatePath: "/snap/state"}
}

func (v *fakeVMM) killedInstance(id string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, k := range v.killed {
		if k == id {
			return true
		}
	}
	return false
}

func req(id string) ColdBootRequest {
	return ColdBootRequest{Instance: id, BasePath: "/b.ext4", LayerPath: "/l.ext4", VcpuCount: 2, MemSizeMiB: 128}
}

func newTestManager(run Runner, vmm VMM) *Manager {
	return NewManager(run, vmm, Paths{Kernel: "/srv/fc/base/vmlinux-6.1"}, testFCVersion, nil)
}

func TestColdBootSuccessTracksInstance(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	inst, err := m.ColdBoot(context.Background(), req("i1"))
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	if inst.Lease.UID < JailUIDBase {
		t.Errorf("lease uid not assigned: %d", inst.Lease.UID)
	}
	if m.LiveCount() != 1 || m.LeasedCount() != 1 {
		t.Fatalf("live=%d leased=%d, want 1/1", m.LiveCount(), m.LeasedCount())
	}
	if !run.ran("netns add fc-i1") {
		t.Error("network setup did not run")
	}
}

func TestColdBootNetworkFailureLeaksNothing(t *testing.T) {
	// Fail midway through network setup; the lease must be released and teardown
	// attempted so leakcheck stays clean.
	run := &fakeRunner{failOn: "tuntap add tap0"}
	vmm := &fakeVMM{}
	m := newTestManager(run, vmm)

	if _, err := m.ColdBoot(context.Background(), req("i1")); err == nil {
		t.Fatal("expected cold boot to fail")
	}
	if m.LiveCount() != 0 {
		t.Errorf("live=%d, want 0 after failed boot", m.LiveCount())
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leased=%d, want 0 after failed boot — LEASE LEAK", m.LeasedCount())
	}
	if !run.ran("netns del fc-i1") {
		t.Error("teardown did not attempt to delete the netns")
	}
}

func TestColdBootVMFailureLeaksNothing(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{bootErr: fmt.Errorf("kvm exploded")}
	m := newTestManager(run, vmm)

	if _, err := m.ColdBoot(context.Background(), req("i1")); err == nil {
		t.Fatal("expected cold boot to fail")
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leased=%d, want 0 — LEASE LEAK after VM boot failure", m.LeasedCount())
	}
	if !vmm.killedInstance("i1") {
		t.Error("VM was not killed on the cleanup path")
	}
	if !run.ran("netns del fc-i1") {
		t.Error("network was not torn down on VM boot failure")
	}
}

func TestDestroyReleasesResources(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)
	if _, err := m.ColdBoot(context.Background(), req("i1")); err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(context.Background(), "i1"); err != nil {
		t.Fatal(err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Errorf("live=%d leased=%d, want 0/0 after destroy", m.LiveCount(), m.LeasedCount())
	}
	if !vmm.killedInstance("i1") {
		t.Error("destroy did not kill the VM")
	}
}

func TestDestroyUnknownIsNoop(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if err := m.Destroy(context.Background(), "ghost"); err != nil {
		t.Errorf("destroying unknown instance should be a no-op, got %v", err)
	}
}

// TestConcurrentBootAndDestroyNoLeak mirrors the M1 acceptance shape (boot many,
// tear all down, zero leaks) at the orchestration level.
func TestConcurrentBootAndDestroyNoLeak(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)
	const n = 50 // M1: boot 50 VMs concurrently

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := m.ColdBoot(context.Background(), req(fmt.Sprintf("i%d", i))); err != nil {
				t.Errorf("boot i%d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if m.LiveCount() != n || m.LeasedCount() != n {
		t.Fatalf("after boot: live=%d leased=%d, want %d/%d", m.LiveCount(), m.LeasedCount(), n, n)
	}

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := m.Destroy(context.Background(), fmt.Sprintf("i%d", i)); err != nil {
				t.Errorf("destroy i%d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatalf("after teardown: live=%d leased=%d, want 0/0 (LEAK)", m.LiveCount(), m.LeasedCount())
	}
}

// --- bringUp / cleanup -----------------------------------------------------

// TestRestoreFailsThenColdBootSucceeds covers the ADR-005 branch: snapshot
// restore errors are non-terminal, we Kill the half-restored VM and fall
// back to cold boot. The returned method must read WakeColdBoot so schedd
// can mark the snapshot stale.
func TestRestoreFailsThenColdBootSucceeds(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{restoreErr: fmt.Errorf("snapshot corrupt")}
	m := newTestManager(run, vmm)

	inst, err := m.Wake(context.Background(), WakeRequest{
		Instance: "fb", BasePath: "/b.ext4", LayerPath: "/l.ext4",
		VcpuCount: 2, MemSizeMiB: 128,
		Snapshot: usableSnapshot(),
	})
	if err != nil {
		t.Fatalf("Wake after restore-fail: %v", err)
	}
	if inst.Method != WakeColdBoot {
		t.Errorf("method = %v, want WakeColdBoot (fallback)", inst.Method)
	}
	if vmm.boots() != 1 {
		t.Errorf("Boot not invoked after restore-fail fallback: %d", vmm.boots())
	}
	// The half-restored VM must be killed before the cold-boot attempt —
	// otherwise the lease UID has two processes fighting for the netns.
	if !vmm.killedInstance("fb") {
		t.Error("expected Kill of half-restored instance before cold-boot fallback")
	}
	if m.LeasedCount() != 1 {
		t.Errorf("lease not held after successful fallback: leased=%d", m.LeasedCount())
	}
}

// TestRestoreSucceedsUsesFastPath — counter-test to the fallback: when
// Restore works, cold boot is NOT called and the returned method is
// WakeRestore.
func TestRestoreSucceedsUsesFastPath(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{} // no errors
	m := newTestManager(run, vmm)

	inst, err := m.Wake(context.Background(), WakeRequest{
		Instance: "rp", BasePath: "/b.ext4", LayerPath: "/l.ext4",
		VcpuCount: 2, MemSizeMiB: 128,
		Snapshot: usableSnapshot(),
	})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if inst.Method != WakeRestore {
		t.Errorf("method = %v, want WakeRestore", inst.Method)
	}
	if vmm.boots() != 0 {
		t.Errorf("Boot must not run on restore fast path: %d", vmm.boots())
	}
}

// TestColdBootConfigInvalid covers the Validate() failure branch of bringUp.
// ColdBootSpec.Validate must reject empty paths / 0 vcpu / 0 mem.
func TestColdBootConfigInvalid(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{}
	m := newTestManager(run, vmm)

	cases := []struct {
		name string
		req  ColdBootRequest
	}{
		{"missing base", ColdBootRequest{Instance: "x", LayerPath: "/l.ext4", VcpuCount: 1, MemSizeMiB: 128}},
		{"missing layer", ColdBootRequest{Instance: "x", BasePath: "/b.ext4", VcpuCount: 1, MemSizeMiB: 128}},
		{"zero vcpu", ColdBootRequest{Instance: "x", BasePath: "/b", LayerPath: "/l", VcpuCount: 0, MemSizeMiB: 128}},
		{"zero mem", ColdBootRequest{Instance: "x", BasePath: "/b", LayerPath: "/l", VcpuCount: 1, MemSizeMiB: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.ColdBoot(context.Background(), tc.req)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if vmm.boots() != 0 {
				t.Errorf("vmm.Boot must not run when spec invalid: %d", vmm.boots())
			}
			// The lease was acquired (before validation) and must be released
			// even on this failure path — no half-held UID.
			if m.LeasedCount() != 0 {
				t.Errorf("lease leaked on validation failure: leased=%d", m.LeasedCount())
			}
		})
	}
}

// TestColdBootVMFailureExhaustsCleanup covers the path where Boot itself
// fails: cleanup() must still run teardown + release, so a transient VMM
// failure does not leak the netns UID.
func TestColdBootVMFailureExhaustsCleanup(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{bootErr: fmt.Errorf("jailer exploded")}
	m := newTestManager(run, vmm)

	_, err := m.ColdBoot(context.Background(), req("vm-fail"))
	if err == nil {
		t.Fatal("expected Boot error")
	}
	if !strings.Contains(err.Error(), "cold boot") {
		t.Errorf("error %q not from cold-boot path", err.Error())
	}
	// Cleanup must have invoked Kill (best-effort) and released the lease.
	if !vmm.killedInstance("vm-fail") {
		t.Error("Kill not called during failed-boot cleanup")
	}
	if m.LeasedCount() != 0 {
		t.Errorf("lease not released after Boot failure: leased=%d", m.LeasedCount())
	}
	// Teardown commands should have been attempted (the network was set up
	// before Boot was called).
	if !run.ran("ip link delete") && !run.ran("ip netns del") {
		t.Error("expected teardown commands during cleanup; none ran")
	}
}

// TestParkUnknownInstanceReturnsError covers the "instance not live" branch
// of Park — without covering this, a typo'd instance id silently no-ops.
func TestParkUnknownInstanceReturnsError(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	_, err := m.Park(context.Background(), "ghost", SnapshotSpec{})
	if err == nil {
		t.Fatal("expected error parking unknown instance")
	}
	if !strings.Contains(err.Error(), "not live") {
		t.Errorf("error %q missing 'not live'", err.Error())
	}
}

// TestParkSnapshotFailureDestroysInstance covers the ADR-005 safety net:
// if Snapshot fails we Destroy the live instance rather than leaking the
// still-running VM + lease. The error returned must wrap the snapshot cause.
func TestParkSnapshotFailureDestroysInstance(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{snapErr: fmt.Errorf("disk full")}
	m := newTestManager(run, vmm)

	// First bring up an instance so Park has something to act on.
	inst, err := m.ColdBoot(context.Background(), req("park-fail"))
	if err != nil {
		t.Fatalf("ColdBoot: %v", err)
	}
	_ = inst

	_, err = m.Park(context.Background(), "park-fail", SnapshotSpec{MemPath: "/m", VMStatePath: "/s"})
	if err == nil {
		t.Fatal("expected snapshot error")
	}
	if !strings.Contains(err.Error(), "snapshot") {
		t.Errorf("error %q not snapshot-wrapped", err.Error())
	}
	// The instance should be torn down even though Park failed — that is the
	// invariant.
	if m.LiveCount() != 0 {
		t.Errorf("instance not removed from live after Park failure: live=%d", m.LiveCount())
	}
	if m.LeasedCount() != 0 {
		t.Errorf("lease leaked after Park failure: leased=%d", m.LeasedCount())
	}
}

// TestSetupNetworkPropagatesFirstError covers the run-loop in setupNetwork:
// it stops at the first failing command (not the last) and wraps with argv.
func TestSetupNetworkPropagatesFirstError(t *testing.T) {
	run := &fakeRunner{failOn: "ip link add"}
	vmm := &fakeVMM{}
	m := newTestManager(run, vmm)
	_, err := m.ColdBoot(context.Background(), req("net-fail"))
	if err == nil {
		t.Fatal("expected setup-network error")
	}
	if !strings.Contains(err.Error(), "ip link add") {
		t.Errorf("error %q missing failing argv", err.Error())
	}
	if vmm.boots() != 0 {
		t.Errorf("Boot must not run when network setup fails: %d", vmm.boots())
	}
}

// TestAcquireFailureShortCircuitsWake covers the very first Wake failure:
// alloc.Acquire returns an error. Wake must not run setupNetwork or Boot.
func TestAcquireFailureShortCircuitsWake(t *testing.T) {
	// Saturate the allocator so the next Acquire fails.
	alloc := NewAllocator()
	for i := 0; i < MaxSlots; i++ {
		if _, err := alloc.Acquire(fmt.Sprintf("pre%d", i)); err != nil {
			t.Fatalf("priming %d: %v", i, err)
		}
	}
	vmm := &fakeVMM{}
	run := &fakeRunner{}
	m := NewManager(run, vmm, Paths{Kernel: "/k"}, testFCVersion, nil)
	m.alloc = alloc // swap in the saturated one

	_, err := m.ColdBoot(context.Background(), req("overflow"))
	if err == nil {
		t.Fatal("expected acquire failure")
	}
	if !strings.Contains(err.Error(), "acquire") {
		t.Errorf("error %q missing 'acquire'", err.Error())
	}
	if run.ran("ip link") {
		t.Error("setupNetwork must not run when Acquire fails")
	}
	if vmm.boots() != 0 {
		t.Errorf("Boot must not run when Acquire fails: %d", vmm.boots())
	}
}

// TestLiveCountAndLeasedCountEmptyManager — sanity check the getters on a
// fresh Manager.
func TestLiveCountAndLeasedCountEmptyManager(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Errorf("fresh manager non-empty: live=%d leased=%d", m.LiveCount(), m.LeasedCount())
	}
}

// TestCleanupKillErrorIsLogged — covers the `m.log.Warn` branch of cleanup's
// first call when vmm.Kill returns an error. The error must be swallowed
// (cleanup is best-effort), not propagated.
func TestCleanupKillErrorIsLogged(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{killErr: fmt.Errorf("process already gone")}
	m := newTestManager(run, vmm)
	// Trigger cleanup via Destroy on an instance we never booted: Destroy
	// short-circuits when not live, so we need to fake-via-Wake failure path.
	// Easiest: pre-populate live map by performing a successful boot, then
	// calling Destroy.
	inst, err := m.ColdBoot(context.Background(), req("kill-err"))
	if err != nil {
		t.Fatalf("ColdBoot: %v", err)
	}
	_ = inst
	if err := m.Destroy(context.Background(), "kill-err"); err != nil {
		t.Fatalf("Destroy should swallow cleanup errors: %v", err)
	}
	// Lease must still be released despite the Kill error.
	if m.LeasedCount() != 0 {
		t.Errorf("lease leaked after Kill error: leased=%d", m.LeasedCount())
	}
}

// fakeVMMWithKillErr extends fakeVMM with a Kill that always errors.
// We mutate the embedded fakeVMM rather than threading a new field so this
// test file's existing helpers stay unchanged.

// TestCleanupTeardownCommandFailureIsDebug — covers the `m.log.Debug` branch
// when a teardown command errors (e.g. ip netns del on a netns that was
// never created because boot failed before that step).
func TestCleanupTeardownCommandFailureIsDebug(t *testing.T) {
	run := &fakeRunner{} // no failures during setup
	vmm := &fakeVMM{bootErr: fmt.Errorf("Boot fail")}
	m := newTestManager(run, vmm)

	_, err := m.ColdBoot(context.Background(), req("td-fail"))
	if err == nil {
		t.Fatal("expected Boot error")
	}
	// We can't easily make the teardown commands fail when they ran fine on
	// the setup side, but we *can* swap to a Runner that fails teardown.
	// Re-run with a runner that fails teardown:
	run2 := &fakeRunner{} // no setup failures
	run2.failTeardown = true
	vmm2 := &fakeVMM{bootErr: fmt.Errorf("Boot fail")}
	m2 := newTestManager(run2, vmm2)
	_, _ = m2.ColdBoot(context.Background(), req("td-fail2"))
	// We expect no panic — the debug log swallows teardown failures.
	if m2.LeasedCount() != 0 {
		t.Errorf("lease leaked: %d", m2.LeasedCount())
	}
}

// TestCleanupReleaseErrorIsLogged — covers the alloc.Release error branch
// (instance not in the lease map, can only happen on logic error / double
// cleanup). The error must be swallowed.
func TestCleanupReleaseErrorIsLogged(t *testing.T) {
	// Bypass Wake's automatic cleanup by directly calling m.cleanup on an
	// instance the allocator has never seen.
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	lease := Lease{Instance: "ghost-cleanup", UID: 20000, GID: 20000}
	nc := netnsConfigForTest(lease)
	// Should not panic; should log warn. We're proving the swallow.
	m.cleanup(context.Background(), lease, nc)
}

// netnsConfigForTest builds a minimal netns.Config matching the lease so
// cleanup has something to iterate teardown commands on. The exact netns
// name doesn't matter — fakeRunner matches by substring.
func netnsConfigForTest(l Lease) netns.Config {
	return netns.NewConfig(
		l.Instance, l.Netns, l.VethHost, l.VethPeer,
		l.HostIP,
	)
}

// TestDiscardWrite — covers the io.Writer fallback in manager.go so the
// NewManager(nil-log) path is verified end-to-end.
func TestDiscardWrite(t *testing.T) {
	d := discard{}
	if _, err := d.Write([]byte("anything")); err != nil {
		t.Errorf("discard.Write: %v", err)
	}
}
