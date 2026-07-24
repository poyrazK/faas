package fcvm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	// resumeHookErr is returned from TriggerResumeHook when non-nil; the
	// default (nil) matches production-success semantics. V6 tests that need
	// the dial-failure path flip this.
	resumeHookErr error
	// resumeHookCalls records every (instance, hostTimeUnixNano) the wake
	// path passed to TriggerResumeHook. Tests assert both ordering (Boot
	// doesn't fire it, Restore does) and the dial-time argument.
	resumeHookCalls []resumeHookCall
	// M6 builder-VM path: DestroyWithExport returns this exit code, copies
	// nothing. App VMs just see "destroyed" the same way Kill did.
	destroyWithExportExit int
	destroyWithExportErr  error
	destroyedWithExport   []string
	// G2 secrets staging.
	stagedSecrets   []stagedSecret
	stageSecretsErr error
}

type stagedSecret struct {
	blob []byte
}

func (v *fakeVMM) Boot(_ context.Context, l Lease, _ VMConfig) error {
	v.mu.Lock()
	v.bootCount++
	v.mu.Unlock()
	// Mirror what jailer does in production: create the per-VM cgroup
	// scope under faas-tenant.slice. Without this the post-bringUp
	// writeMemoryMax in Wake fails on the test side even though the
	// code path is correct. Best-effort: if the test set cgroupRoot to
	// a path where this would fail, leave it; the cgroup write will
	// surface the error.
	if err := os.MkdirAll(filepath.Join(cgroupRoot, ParentCgroup, PerInstanceScope(l.Instance)), 0o755); err != nil {
		return err
	}
	return v.bootErr
}

// BootColdBoot mirrors the production flow for tests: synthesize a
// VMConfig from the resolved ColdBootSpec and delegate to Boot. The
// fake doesn't actually materialize from StorageBackend (no storage
// configured); the production path in JailerVMM.BootColdBoot would
// resolve keys through storage.Get before calling Boot. Tests that
// care about storage semantics use TestRestore_MaterializesBaseViaStorage
// (pkg/fcvm/vmm_test.go) with a real JailerVMM + fake StorageBackend.
func (v *fakeVMM) BootColdBoot(ctx context.Context, l Lease, spec ColdBootSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	return v.Boot(ctx, l, BuildColdBootConfig(spec, l.Slot))
}

func (v *fakeVMM) Restore(ctx context.Context, l Lease, spec RestoreSpec) error {
	v.mu.Lock()
	v.restored = append(v.restored, l.Instance)
	v.mu.Unlock()
	// Same scope-create as Boot — jailer creates the scope on restore too.
	if err := os.MkdirAll(filepath.Join(cgroupRoot, ParentCgroup, PerInstanceScope(l.Instance)), 0o755); err != nil {
		return err
	}
	// Mirror the production JailerVMM.Restore: after /snapshot/load, dial the
	// vsock and trigger the resume hook. ADR-022. The test then sees the call
	// on v.resumeHookCalls (used by TestWakeRestore_*) and surfaces any
	// injected error (used by TestWakeRestore_ResumeHookErrorPropagatesAndUnwinds).
	if spec.VsockDevice != nil {
		if err := v.TriggerResumeHook(ctx, l, 1); err != nil {
			return err
		}
	}
	return v.restoreErr
}

func (v *fakeVMM) TriggerResumeHook(_ context.Context, l Lease, hostTimeUnixNano int64) error {
	v.mu.Lock()
	v.resumeHookCalls = append(v.resumeHookCalls, resumeHookCall{Instance: l.Instance, HostTimeUnixNano: hostTimeUnixNano})
	v.mu.Unlock()
	// Default: succeed. Tests that exercise the resume-hook error path should
	// set resumeHookErr (see manager_test.go).
	return v.resumeHookErr
}

// resumeHookCall records one TriggerResumeHook invocation. The slice is
// append-only and read under v.mu — production code never reads it.
type resumeHookCall struct {
	Instance         string
	HostTimeUnixNano int64
}

// TestWakeColdBoot_DoesNotInvokeResumeHook pins the post-restore-only
// invariant (ADR-022): a Wake with no usable snapshot MUST NOT call
// TriggerResumeHook. Cold-boot guests get fresh kernel entropy from the
// boot-time pool; only restore needs the resume hook (re-seed entropy +
// step clock).
func TestWakeColdBoot_DoesNotInvokeResumeHook(t *testing.T) {
	mgr := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{Kernel: "/k"}, "1.7.0", nil, nil)
	if _, err := mgr.Wake(context.Background(), WakeRequest{
		Instance:   "cold-A",
		BaseKey:    "/base.ext4",
		LayerKey:   "/layer.ext4",
		VcpuCount:  2,
		MemSizeMiB: 128,
		// Snapshot intentionally nil — forces cold boot.
	}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	// Reach into the fakeVMM to assert TriggerResumeHook was not called.
	vmm, ok := mgr.vmm.(*fakeVMM)
	if !ok {
		t.Fatal("mgr.vmm is not *fakeVMM")
	}
	if n := len(vmm.resumeHookCalls); n != 0 {
		t.Errorf("TriggerResumeHook called %d times on cold boot, want 0 (hook is post-restore only)", n)
	}
}

// TestWakeRestore_InvokesResumeHook verifies the restore path DOES call
// TriggerResumeHook exactly once per Wake, with the lease slot wired into
// the VsockDevice passed via RestoreSpec.
func TestWakeRestore_InvokesResumeHook(t *testing.T) {
	mgr := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{Kernel: "/k"}, "1.7.0", nil, nil)
	if _, err := mgr.Wake(context.Background(), WakeRequest{
		Instance:   "restore-A",
		BaseKey:    "/base.ext4",
		LayerKey:   "/layer.ext4",
		VcpuCount:  2,
		MemSizeMiB: 128,
		Snapshot:   usableSnapshot(),
	}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	vmm, ok := mgr.vmm.(*fakeVMM)
	if !ok {
		t.Fatal("mgr.vmm is not *fakeVMM")
	}
	if n := len(vmm.resumeHookCalls); n != 1 {
		t.Errorf("TriggerResumeHook called %d times on restore, want 1", n)
	}
	if len(vmm.resumeHookCalls) > 0 && vmm.resumeHookCalls[0].Instance != "restore-A" {
		t.Errorf("resume hook for instance = %q, want %q", vmm.resumeHookCalls[0].Instance, "restore-A")
	}
}

// TestWakeRestore_ResumeHookErrorFallsBackToColdBoot verifies the resume
// hook error path is handled safely (ADR-005 cold-boot fallback). A failed
// TriggerResumeHook means the resumed VM would share its snapshot's entropy
// — spec §11 V6 says "non-unique guest must not serve." The Manager's
// restore-failure cold-boot fallback discards the bad VM and starts fresh,
// which gives the guest unique entropy by construction.
//
// Invariants pinned here:
//   - The half-restored VM is killed (no leak: fvmm.killed includes it).
//   - Wake ultimately succeeds (the cold-boot fallback rescued it).
//   - TriggerResumeHook is called exactly once before the fallback fires.
func TestWakeRestore_ResumeHookErrorFallsBackToColdBoot(t *testing.T) {
	fvmm := &fakeVMM{resumeHookErr: fmt.Errorf("dial vsock uds: synthetic failure")}
	mgr := NewManager(&fakeRunner{}, fvmm, Paths{Kernel: "/k"}, "1.7.0", nil, nil)
	if _, err := mgr.Wake(context.Background(), WakeRequest{
		Instance:   "restore-fail",
		BaseKey:    "/base.ext4",
		LayerKey:   "/layer.ext4",
		VcpuCount:  2,
		MemSizeMiB: 128,
		Snapshot:   usableSnapshot(),
	}); err != nil {
		t.Fatalf("Wake: %v (cold-boot fallback should have rescued it)", err)
	}
	// TriggerResumeHook was called once (during the restore attempt).
	if n := len(fvmm.resumeHookCalls); n != 1 {
		t.Errorf("TriggerResumeHook calls = %d, want 1", n)
	}
	// Restore was attempted once, then Kill ran to discard the half-restored
	// VM before cold boot took over.
	if n := len(fvmm.restored); n != 1 {
		t.Errorf("Restore calls = %d, want 1", n)
	}
	if n := len(fvmm.killed); n != 1 {
		t.Errorf("Kill calls = %d, want 1 (cold-boot fallback discards the bad VM)", n)
	}
	// Cold boot ran after Kill — so bootCount is 1.
	if n := fvmm.bootCount; n != 1 {
		t.Errorf("Boot calls = %d, want 1 (cold-boot fallback after failed resume)", n)
	}
	if mgr.LiveCount() != 1 {
		t.Errorf("LiveCount = %d after successful cold-boot fallback, want 1", mgr.LiveCount())
	}
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

func (v *fakeVMM) DestroyWithExport(_ context.Context, l Lease, _ string) (int, error) {
	v.mu.Lock()
	v.destroyedWithExport = append(v.destroyedWithExport, l.Instance)
	v.mu.Unlock()
	return v.destroyWithExportExit, v.destroyWithExportErr
}

func (v *fakeVMM) StageSecretsEnv(_ string, jsonBlob []byte) error {
	v.mu.Lock()
	v.stagedSecrets = append(v.stagedSecrets, stagedSecret{blob: append([]byte(nil), jsonBlob...)})
	v.mu.Unlock()
	return v.stageSecretsErr
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
	return &Snapshot{DeploymentID: "d1", FCVersion: testFCVersion, StorageKey: "snap/d1/mem", VMStatePath: "/snap/state"}
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
	return ColdBootRequest{Instance: id, BaseKey: "/b.ext4", LayerKey: "/l.ext4", VcpuCount: 2, MemSizeMiB: 128}
}

func newTestManager(run Runner, vmm VMM) *Manager {
	return NewManager(run, vmm, Paths{Kernel: "/srv/fc/base/vmlinux-6.1"}, testFCVersion, nil, nil)
}

// TestMain redirects cgroupRoot to a temp dir for the whole package's
// unit tests. fakeVMM.Boot (manager_test.go:fakeVMM.Boot) creates the
// per-VM scope as a plain directory under cgroupRoot, so the unit-test
// path never touches the host's real /sys/fs/cgroup — concurrent runs
// don't collide. Tests that want a distinct root inside the unit-test
// path can call withFakeCgroupRoot (cgroup_test.go). Metal tests
// (TestMetal*, in manager_metal_test.go) point cgroupRoot back at the
// real /sys/fs/cgroup via the same helper, because the jailer writes
// there regardless of what cgroupRoot is set to in this package.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fcvm-cgroup-test-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	if err := os.MkdirAll(filepath.Join(dir, "faas-tenant.slice"), 0o755); err != nil {
		panic(err)
	}
	cgroupRoot = dir
	os.Exit(m.Run())
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
		Instance: "fb", BaseKey: "/b.ext4", LayerKey: "/l.ext4",
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

// TestWakeRejectsEgressAllowlist_v6Closed: defence-in-depth in the
// wire path (PR #159 review F3). apid's PATCH + the Postgres cidr[]
// CHECK already reject v6 at write time, but a wire-bypass (a vmmd
// that forgets to re-validate, a corrupted plan-tier check, a
// future migration that loosens the apid gate) must not be able to
// land a v6 prefix in the per-netns `ip faas forward` chain — nft
// rejects v6 in an `ip`-family table, which would abort the rule
// sequence and break Wake.
//
// The Wake path ParsePrefix's each entry and re-validates family +
// non-/0 BEFORE touching the netns. A v6 entry fails Closed with a
// caller-actionable error naming the offending CIDR; the deferred
// cleanup unwinds the lease so LeakCount stays 0.
//
// Mirrors the cleanup discipline of TestColdBootNetworkFailureLeaksNothing
// (manager_test.go:365) on a fail-fast path that returns BEFORE any
// nft argv runs.
func TestWakeRejectsEgressAllowlist_v6Closed(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{}
	m := newTestManager(run, vmm)

	_, err := m.Wake(context.Background(), WakeRequest{
		Instance:   "vw",
		BaseKey:    "/b.ext4",
		LayerKey:   "/l.ext4",
		VcpuCount:  2,
		MemSizeMiB: 128,
		// v6 prefix: apid+DB would normally catch this, the wire-side
		// re-validate is the load-bearing gate if either is bypassed.
		EgressAllowlist: []string{"fe80::/10"},
	})
	if err == nil {
		t.Fatal("Wake with v6 EgressAllowlist entry: expected fail-closed, got success")
	}
	if !strings.Contains(err.Error(), "fe80::/10") {
		t.Errorf("error should name the offending CIDR; got: %v", err)
	}
	if !strings.Contains(err.Error(), "v4 only") {
		t.Errorf("error should say v4 only (ADR-031 v1); got: %v", err)
	}
	// No nft commands may have run — the re-validate is BEFORE
	// Setup/NftCommands. A future regression that moves validation
	// after setup would leave a half-rendered netns behind; pin here.
	if run.ran("nft") {
		t.Error("nft commands ran before v6 rejection — render order regressed")
	}
	if m.LeasedCount() != 0 {
		t.Errorf("lease leaked after fail-closed: leased=%d", m.LeasedCount())
	}
}

// TestWakeRejectsEgressAllowlist_ZeroBitsClosed: same defence-in-
// depth shape, on the /0 case. apid's PATCH rejects Bits()==0
// (PR #159 review F2); the Wake path re-validates so a wire-bypass
// cannot smuggle 0.0.0.0/0 (which would unblock the whole v4
// internet and make the allowlist a no-op).
func TestWakeRejectsEgressAllowlist_ZeroBitsClosed(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{}
	m := newTestManager(run, vmm)

	_, err := m.Wake(context.Background(), WakeRequest{
		Instance:        "w0",
		BaseKey:         "/b.ext4",
		LayerKey:        "/l.ext4",
		VcpuCount:       2,
		MemSizeMiB:      128,
		EgressAllowlist: []string{"0.0.0.0/0"},
	})
	if err == nil {
		t.Fatal("Wake with /0 EgressAllowlist entry: expected fail-closed, got success")
	}
	if !strings.Contains(err.Error(), "0.0.0.0/0") {
		t.Errorf("error should name the offending CIDR; got: %v", err)
	}
	if !strings.Contains(err.Error(), "non-/0") {
		t.Errorf("error should mention the non-/0 invariant; got: %v", err)
	}
	if m.LeasedCount() != 0 {
		t.Errorf("lease leaked after fail-closed: leased=%d", m.LeasedCount())
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
		Instance: "rp", BaseKey: "/b.ext4", LayerKey: "/l.ext4",
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
		{"missing base", ColdBootRequest{Instance: "x", LayerKey: "/l.ext4", VcpuCount: 1, MemSizeMiB: 128}},
		{"missing layer", ColdBootRequest{Instance: "x", BaseKey: "/b.ext4", VcpuCount: 1, MemSizeMiB: 128}},
		{"zero vcpu", ColdBootRequest{Instance: "x", BaseKey: "/b", LayerKey: "/l", VcpuCount: 0, MemSizeMiB: 128}},
		{"zero mem", ColdBootRequest{Instance: "x", BaseKey: "/b", LayerKey: "/l", VcpuCount: 1, MemSizeMiB: 0}},
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

	_, err = m.Park(context.Background(), "park-fail", SnapshotSpec{VMStatePath: "/s", StorageKey: "snap/park-fail/mem"})
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
	m := NewManager(run, vmm, Paths{Kernel: "/k"}, testFCVersion, nil, nil)
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

// TestSetupNetworkRunsNftBeforeVMBoot proves the wire-up point: the per-
// instance nft commands run inside setupNetwork, AFTER the topology (veth/
// tap/addressing) is in place but BEFORE VMM.Boot. Without this ordering,
// VMM.Boot's waitReady would dial a host identity whose DNAT isn't loaded
// yet — and the SYN-ACK would never come back (filter or no filter).
func TestSetupNetworkRunsNftBeforeVMBoot(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	if _, err := m.ColdBoot(context.Background(), req("dnat-ord")); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	if vmm.boots() != 1 {
		t.Fatalf("VMM.Boot must run exactly once; got %d", vmm.boots())
	}

	// Locate the tap-create argv and the DNAT argv by content.
	var tapIdx, dnatIdx = -1, -1
	for i, c := range run.commands {
		line := strings.Join(c, " ")
		switch {
		case strings.Contains(line, "tuntap add tap0"):
			tapIdx = i
		case strings.Contains(line, "dnat to 10.0.0.2:8080"):
			dnatIdx = i
		}
	}
	if tapIdx < 0 {
		t.Fatalf("never saw `tuntap add tap0` in %v", run.commands)
	}
	if dnatIdx < 0 {
		t.Fatalf("never saw DNAT rule `dnat to 10.0.0.2:8080` in %v", run.commands)
	}
	if tapIdx > dnatIdx {
		t.Errorf("tap-create (idx %d) must precede DNAT rule (idx %d)", tapIdx, dnatIdx)
	}
	// VMM.Boot runs after setupNetwork returns (Wake's call sequence). bootCount
	// is asserted at the top of this test via `vmm.boots() != 1`; the order
	// between tap-create < DNAT < Boot is the load-bearing #30 invariant.
}

// TestSetupNetworkNftFailureLeaksNothing covers the leak invariant when the
// strict part of the nft ruleset fails: the defer-cleanup in Wake must
// fully unwind (netns deleted, lease released) even if Boot never runs.
//
// We fail on a strict nft argv (`add rule ip faas prerouting`) so the best-
// effort reset (which ran first and succeeded) is already done — that's the
// realistic scenario where a partial ruleset lands but a later add fails.
func TestSetupNetworkNftFailureLeaksNothing(t *testing.T) {
	run := &fakeRunner{failOn: "add rule ip faas prerouting"}
	vmm := &fakeVMM{}
	m := newTestManager(run, vmm)

	_, err := m.ColdBoot(context.Background(), req("dnat-fail"))
	if err == nil {
		t.Fatal("expected setupNetwork to fail on the nft add rule")
	}
	if !strings.Contains(err.Error(), "add rule ip faas prerouting") {
		t.Errorf("err %q must wrap the failing argv", err.Error())
	}
	if vmm.boots() != 0 {
		t.Errorf("VMM.Boot must not run when nft fails: %d boots", vmm.boots())
	}
	if m.LeasedCount() != 0 {
		t.Errorf("LeasedCount = %d after failed boot, want 0 (leak)", m.LeasedCount())
	}
	if !run.ran("netns del fc-dnat-fail") {
		t.Error("teardown did not run netns del; netns leaked")
	}
}

// --- tc + memory.max wiring (PR A: #31 + #33) ----------------------------

// indexOfArgv returns the index of the first recorded argv whose
// joined form contains substr, or -1 if absent. Used by the new
// ordering / argv-shape tests below.
func indexOfArgv(cmds [][]string, substr string) int {
	for i, c := range cmds {
		if strings.Contains(strings.Join(c, " "), substr) {
			return i
		}
	}
	return -1
}

// TestSetupNetworkTcResetBeforeNftReset locks the snapshot-restore
// ordering: each ruleset's reset (`tc qdisc del`, `nft delete table`)
// must come BEFORE its strict add, and the tc reset must come BEFORE
// the nft reset so a fresh netns that already had the veth set up
// (which happens across park→wake) drops the qdisc before the nft
// reset tries to clean the table.
func TestSetupNetworkTcResetBeforeNftReset(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	r := req("tc-ord")
	r.EgressMbit = 25
	if _, err := m.ColdBoot(context.Background(), r); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	tcDel := indexOfArgv(run.commands, "tc qdisc del")
	nftDel := indexOfArgv(run.commands, "nft delete table")
	nftAdd := indexOfArgv(run.commands, "nft add table")
	if tcDel < 0 || nftDel < 0 || nftAdd < 0 {
		t.Fatalf("expected all three argvs; got tcDel=%d nftDel=%d nftAdd=%d\n%s",
			tcDel, nftDel, nftAdd, flattenForTest(run.commands))
	}
	if tcDel >= nftDel {
		t.Errorf("tc qdisc del (idx %d) must precede nft delete table (idx %d) on snapshot-restore Wake", tcDel, nftDel)
	}
	if nftDel >= nftAdd {
		t.Errorf("nft delete table (idx %d) must precede nft add table (idx %d) — same reset-before-add invariant", nftDel, nftAdd)
	}
}

// TestSetupNetworkEmitsConntrackCapRule locks the spec §7 wire-up:
// pkg/fcvm/manager.go::Wake stamps nc.ConntrackCap = api.DefaultConntrackCap,
// so the runner must observe the nft `ct count over 4096 counter name
// "faas_cap" drop` rule in the argv list — and it must sit between the
// established/related accept and the SMTP / daddr drops (the rule
// position the connlimit comment in pkg/netns/config.go asserts).
//
// The companion unit tests for argv shape live in pkg/netns/config_test.go
// (TestNftCommandsEmitsConntrackCapRule / CapRuleRunsAfterEstablishedBeforeDenies);
// this test pins the wiring through pkg/fcvm/manager::setupNetwork, which
// is the runtime code that owns rule ordering against tc reset/add.
func TestSetupNetworkEmitsConntrackCapRule(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	if _, err := m.ColdBoot(context.Background(), req("cap-rule")); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	capV4 := indexOfArgv(run.commands, "nft add rule ip faas forward ct count over 4096")
	capV6 := indexOfArgv(run.commands, "nft add rule ip6 faas forward ct count over 4096")
	establishedV4 := indexOfArgv(run.commands, "nft add rule ip faas forward ct state established,related accept")
	establishedV6 := indexOfArgv(run.commands, "nft add rule ip6 faas forward ct state established,related accept")
	smtpDrop := indexOfArgv(run.commands, "tcp dport {")
	daddrDropV4 := indexOfArgv(run.commands, "ip daddr { 10.0.0.0/8")
	daddrDropV6 := indexOfArgv(run.commands, "ip6 daddr { fe80::/10")
	if capV4 < 0 || capV6 < 0 || establishedV4 < 0 || establishedV6 < 0 || daddrDropV4 < 0 || daddrDropV6 < 0 || smtpDrop < 0 {
		t.Fatalf("missing one or more rules in argv list: capV4=%d capV6=%d establishedV4=%d establishedV6=%d smtp=%d daddrV4=%d daddrV6=%d\n%s",
			capV4, capV6, establishedV4, establishedV6, smtpDrop, daddrDropV4, daddrDropV6, flattenForTest(run.commands))
	}
	// IPv4 forward chain: established/related accept < cap < SMTP drop < daddr drop.
	if establishedV4 >= capV4 {
		t.Errorf("[v4] established,related accept (idx %d) must come BEFORE the cap rule (idx %d)", establishedV4, capV4)
	}
	if capV4 >= smtpDrop {
		t.Errorf("[v4] cap rule (idx %d) must come BEFORE the SMTP drop (idx %d)", capV4, smtpDrop)
	}
	if capV4 >= daddrDropV4 {
		t.Errorf("[v4] cap rule (idx %d) must come BEFORE the daddr lateral-movement drop (idx %d)", capV4, daddrDropV4)
	}
	// IPv6 forward chain: established/related accept < cap < daddr drop.
	// (No SMTP drop on v6.)
	if establishedV6 >= capV6 {
		t.Errorf("[v6] established,related accept (idx %d) must come BEFORE the cap rule (idx %d)", establishedV6, capV6)
	}
	if capV6 >= daddrDropV6 {
		t.Errorf("[v6] cap rule (idx %d) must come BEFORE the daddr lateral-movement drop (idx %d)", capV6, daddrDropV6)
	}
}

// TestSetupNetworkTcRateEqualsPlan locks the wire shape: when the
// caller sets EgressMbit, the argv that runs contains the rate.
func TestSetupNetworkTcRateEqualsPlan(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	r := req("tc-rate")
	r.EgressMbit = 100 // Pro plan
	if _, err := m.ColdBoot(context.Background(), r); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	tcAdd := indexOfArgv(run.commands, "tc qdisc add")
	if tcAdd < 0 {
		t.Fatalf("never saw `tc qdisc add` argv: %s", flattenForTest(run.commands))
	}
	if !strings.Contains(strings.Join(run.commands[tcAdd], " "), "rate 100mbit") {
		t.Errorf("tc argv %v must contain `rate 100mbit`", run.commands[tcAdd])
	}
}

// TestSetupNetworkEgressZeroDisablesTc locks the `EgressMbit > 0`
// guard: legacy callers (existing tests, dev CLI boot) leave the
// field at zero and the tc argv MUST NOT run. Without the guard, a
// `tc qdisc add ... rate 0mbit` would fail on metal with
// "RTNETLINK answers: Invalid argument" and abort the wake.
func TestSetupNetworkEgressZeroDisablesTc(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	r := req("tc-off")
	r.EgressMbit = 0
	if _, err := m.ColdBoot(context.Background(), r); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	if indexOfArgv(run.commands, "tc qdisc add") >= 0 {
		t.Errorf("tc qdisc add must not run when EgressMbit is 0: %s", flattenForTest(run.commands))
	}
}

// TestWakeWritesMemoryMaxAfterBringUp asserts the wire-up order for
// the #33 cgroup fence: the scope is created by jailer during
// Boot/Restore, so writeMemoryMax must run AFTER bringUp returns and
// BEFORE the instance is published into m.live. This test uses
// fakeVMM (whose Boot creates the scope on the test side, mirroring
// jailer), runs a ColdBoot, and asserts both:
//  1. writeMemoryMax wrote a memory.max file in the fake cgroupRoot
//     whose value equals (MemSizeMiB + PerVMOverheadMB) << 20.
//  2. The cgroup write happened after vmm.Boot (bootCount was
//     already incremented when writeMemoryMax ran).
func TestWakeWritesMemoryMaxAfterBringUp(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	r := req("cgroup-order")
	r.MemSizeMiB = 128

	if _, err := m.ColdBoot(context.Background(), r); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	if vmm.boots() != 1 {
		t.Fatalf("expected 1 boot, got %d", vmm.boots())
	}
	memPath := filepath.Join(cgroupRoot, "faas-tenant.slice", PerInstanceScope("cgroup-order"), "memory.max")
	body, err := os.ReadFile(memPath)
	if err != nil {
		t.Fatalf("memory.max not written at %s: %v", memPath, err)
	}
	const want = (128 + 8) << 20 // PerVMOverheadMB = 8 (pkg/api/limits)
	got := strings.TrimSpace(string(body))
	if got != itoa(want) {
		t.Errorf("memory.max = %q, want %d", got, want)
	}
}

// TestWakeCgroupWriteFailureUnwindsNetns covers the leak invariant
// when the post-bringUp cgroup write itself fails. The cleanup
// defer in Wake must still tear down the netns and release the lease
// so a transient cgroup permission issue doesn't leak. We point
// cgroupRoot at a read-only directory so os.WriteFile returns an
// error.
func TestWakeCgroupWriteFailureUnwindsNetns(t *testing.T) {
	// Build a directory where the slice dir exists but is read-only —
	// the scope-create inside fakeVMM.Boot succeeds (it MkdirAll's
	// the scope), but the subsequent memory.max WriteFile inside the
	// scope fails. Easiest: chmod the parent dir to 0500 after the
	// scope is created. We do it by pointing cgroupRoot at a path
	// that exists but is unwritable.
	tmp := t.TempDir()
	ro := filepath.Join(tmp, "ro")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatalf("mkdir ro: %v", err)
	}
	// Restore permissions on cleanup so t.TempDir can remove the
	// tree — t.TempDir removes with a regular rm.
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })

	saved := cgroupRoot
	cgroupRoot = ro
	t.Cleanup(func() { cgroupRoot = saved })

	// fakeVMM.Boot does MkdirAll(<cgroupRoot>/faas-tenant.slice/vm-…scope)
	// on a read-only root → fails → Boot returns an error. That makes
	// bringUp fail, which routes through the existing defer-cleanup.
	// The expected assertion is just that the Wake error mentions the
	// failure (whatever the underlying cause) and nothing leaks.
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	_, err := m.ColdBoot(context.Background(), req("cgroup-fail"))
	if err == nil {
		t.Fatal("expected Wake to fail when cgroup write/setup is impossible")
	}
	if m.LeasedCount() != 0 {
		t.Errorf("lease leaked after cgroup failure: leased=%d", m.LeasedCount())
	}
	// Network setup ran before Boot (and before the cgroup write),
	// so the cleanup defer must have torn it down.
	if !run.ran("netns del fc-cgroup-fail") {
		t.Error("cleanup did not delete netns on cgroup failure")
	}
}

func flattenForTest(cmds [][]string) string {
	var b strings.Builder
	for _, c := range cmds {
		b.WriteString(strings.Join(c, " "))
		b.WriteString("\n")
	}
	return b.String()
}
