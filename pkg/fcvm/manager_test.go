package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
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

type fakeInputRunner struct {
	fakeRunner
	inputArgv []string
	input     []byte
	inputRuns int
}

func (f *fakeInputRunner) RunInput(_ context.Context, argv []string, input []byte) error {
	f.inputRuns++
	f.inputArgv = append([]string(nil), argv...)
	f.input = append([]byte(nil), input...)
	return nil
}

// fakeHostRenderer (ADR-119 redesign) is the test stub for the
// Manager's HostRenderer seam. It records every Render call and
// the latest StaticEgressRules slice the Manager pushed. The
// real renderer (cmd/vmmd/egress_watcher.go::liveHostPolicy)
// goes through the staging-dir + atomic-rename pipeline; the
// stub short-circuits to a recorded return so the unit test
// can assert the rebuild was attempted without touching the
// host filesystem.
type fakeHostRenderer struct {
	mu          sync.Mutex
	rules       []netns.StaticEgressRule
	renderCalls int
	renderErr   error
}

func (f *fakeHostRenderer) SetStaticEgressRules(rules []netns.StaticEgressRule) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append([]netns.StaticEgressRule(nil), rules...)
}

func (f *fakeHostRenderer) Render(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renderCalls++
	return f.renderErr
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
	// resumeErr (issue #470 / PR #470-FU-A) is returned from
	// ResumeVM when non-nil. The engine's captureWarmSnapshotLocked
	// tests flip this to simulate a Firecracker 5xx after
	// SnapshotKeepAlive — the engine's failure path then
	// transitions to STOPPED instead of PARKED.
	resumeErr error
	// resumed records every instance the engine asked to resume.
	// Tests assert that captureWarmSnapshotLocked fires ResumeVM
	// exactly once per Park cycle on the success path and zero
	// times on the failure path.
	resumed []string
	// keepAliveSnapshotted records every instance the engine's
	// captureWarmSnapshotLocked routed through SnapshotKeepAlive
	// (the warm-tier capture). Distinct from snapshotted because
	// the legacy Park flow uses Snapshot (atomic Snap+Kill) and
	// tests assert both paths are exercised for one Park.
	keepAliveSnapshotted []string
	// bootCgroupFail, when non-nil, causes Boot to return this error after
	// creating the cgroup scope — used to simulate a cgroup write failure
	// (e.g. memory.max WriteFile failing due to permissions) without
	// depending on filesystem permissions that may be bypassed by root.
	bootCgroupFail error
	// postBootCgroupBlock makes the fake jailer scope's memory.max a directory
	// so the Manager's post-boot cgroup fence write fails after bringUp.
	postBootCgroupBlock bool
	// M6 builder-VM path: DestroyWithExport returns this exit code, copies
	// nothing. App VMs just see "destroyed" the same way Kill did.
	destroyWithExportExit int
	// advisoryCalls records every SendStatelessAdvisory the VMM saw
	// (Wave 0 PR-C / ADR-047). Tests assert the wire receiver
	// routed the batch through here when running through the
	// in-process Manager path. Mutex is the existing fakeVMM.mu.
	advisoryCalls        []advisoryCall
	destroyWithExportErr error
	destroyedWithExport  []string
	// G2 secrets staging.
	stagedSecrets   []stagedSecret
	stageSecretsErr error
	// Issue #395 / ADR-045: plaintext api_env staging mirror.
	stagedAPIEnv   []stagedAPIEnvEntry
	stageAPIEnvErr error
	// Issue #463 / ADR-069 / PR-B: per-workload manifest staging
	// on each sidecar drive. Mirrors stagedSecrets / stagedAPIEnv
	// but per-call (one entry per workload, not aggregated).
	stagedWorkloads  []stagedWorkload
	stageWorkloadErr error
	// Issue #463 / ADR-069: per-sidecar deployment env overrides staged on
	// the writable main layer. Mirrors stagedWorkloads but records the JSON
	// payload after Manager.Wake has unsealed it.
	stagedWorkloadEnvs  []stagedWorkloadEnv
	stageWorkloadEnvErr error
	// Issue #463 / ADR-069 / PR-B: deployment-level roster at
	// /etc/faas/workloads.json on drive1. Mirrors stagedWorkloads
	// but the arg shape is (main, sidecars[]) — a single call
	// captures the whole roster, vs. N calls for the per-drive
	// manifests.
	stagedRosters  []stagedRoster
	stageRosterErr error
	// stageCallSeq is a monotonic counter incremented once per call to
	// StageSecretsEnv or StageAPIEnv, captured on each entry's seq
	// field. Used by TestWake_SealedAndAPIEnv_BothStage to assert the
	// call ordering (secrets before api-env) without relying on a
	// shared slice — the secrets and api-env staging live in distinct
	// arrays because their blob shapes differ (sealed JSON envelope
	// vs plaintext JSON map), so a refactor that reorders them inside
	// Manager.Wake wouldn't otherwise trip a count-only test.
	stageCallSeq int
	// pids is the InstancePID source-of-truth for the M8 §11
	// SeccompStatus path. Tests that want the gRPC handler to
	// return a real (pid, true) register one here; the default
	// (empty map) makes the handler return NotFound.
	pids map[string]int
	// ADR-051 PR-D: characterization report observation. The
	// default is a zero report (no observed class) which matches
	// the deploy-time fallback — the engine stamps the scan-hint
	// class and the boot path proceeds. Tests that want to
	// exercise the set-class path use characterizationReport to
	// inject a non-empty result; charReportErr to test the
	// "characterization failed" branch (cold-boot still proceeds
	// because Wake treats the report as best-effort).
	characterizationReport api.CharacterizationReport
	charReportErr          error
	// signalAndKillCalls (M-2 / ADR-138 §Decision 1) records every
	// (instance, signal, grace) the engine's StopInstance dispatch
	// passed through VMM.SignalAndKill. Default behaviour is no-op
	// — the actual destroy chain is delegated to DestroyWithExport
	// once the grace timer fires. Tests assert that the engine's
	// per-mode dispatch (commit 6) routes worker/job to this seam
	// and request/service to the legacy snapshotAndPark path.
	signalAndKillCalls []signalAndKillCall
}

// signalAndKillCall is the per-call record. signal is the POSIX
// signal number the engine translated from manifest.StopSignal
// (0 = use SIGTERM, the manifest default). grace is the
// per-app StopGracePeriod capped at the per-plan tier (commit 10).
type signalAndKillCall struct {
	Instance string
	Signal   int32
	Grace    time.Duration
}

type stagedSecret struct {
	seq  int // capture of stageCallSeq at the moment of call
	blob []byte
}

// stagedAPIEnvEntry is the plaintext sibling of stagedSecret
// (issue #395 / ADR-045). Same shape (instance, blob) but a distinct
// type so a test that asserts "StageSecretsEnv called N times" doesn't
// confuse an api-env write with a secrets write.
type stagedAPIEnvEntry struct {
	instance string
	seq      int // capture of stageCallSeq at the moment of call
	blob     []byte
}

// stagedWorkload (issue #463 / ADR-069 / PR-B) captures one
// StageWorkloadManifest call. driveIdx == -1 is the main workload
// (drive1); 0..N-1 are sidecar drives in the order schedd sent
// them on the wake wire. Tests assert the call ordering (main
// first, then sidecars in stability order) and the spec shape
// (Name, RamMB, Port, Essential).
type stagedWorkload struct {
	instance string
	driveIdx int
	spec     WorkloadSpec
}

type stagedWorkloadEnv struct {
	instance     string
	workloadName string
	blob         []byte
}

func (v *fakeVMM) Boot(_ context.Context, l Lease, _ VMConfig, _ string) error {
	v.mu.Lock()
	v.bootCount++
	v.mu.Unlock()
	// Mirror what jailer does in production: create the per-VM cgroup
	// scope under faas-tenant.slice, then write memory.max to set the
	// RAM cap. Both operations must succeed for Boot to be considered
	// successful — a missing scope or unwritable memory.max means the
	// VM is not properly constrained and we must fail.
	scopePath := filepath.Join(cgroupRoot, ParentCgroupFor(l.Plan), PerInstanceScope(l.Instance))
	if err := os.MkdirAll(scopePath, 0o755); err != nil {
		return err
	}
	// Injectable cgroup failure — used to simulate memory.max write failure
	// (CAP_SYS_ADMIN not granted, cgroup namespace isolation, etc.) without
	// depending on filesystem permissions that root can bypass.
	if v.bootCgroupFail != nil {
		return v.bootCgroupFail
	}
	if v.postBootCgroupBlock {
		if err := os.Mkdir(filepath.Join(scopePath, "memory.max"), 0o755); err != nil {
			return err
		}
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
	// Mirror production: thread the per-deployment override
	// readiness probe path through Boot. The fakeVMM's Boot
	// discards the parameter (the test doesn't go through
	// waitReady), but the call signature is the contract.
	return v.Boot(ctx, l, BuildColdBootConfig(spec, l.Slot), spec.HealthcheckPath)
}

// BootColdBootForJob (issue #1184 Workstream A / ADR-099) is the
// job-task sibling of BootColdBoot. The fake delegates to Boot
// (SkipReady semantics are already on the VMConfig — see
// BuildJobColdBootConfig setting EphemeralWritable=true). Tests
// that exercise the job boot path don't go through waitReady;
// the supervisor exit DGRAM is faked at the schedd layer.
func (v *fakeVMM) BootColdBootForJob(ctx context.Context, l Lease, spec JobColdBootSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	return v.Boot(ctx, l, BuildJobColdBootConfig(spec, l.Slot), "")
}

// WaitJobExit (issue #1184 Workstream A / ADR-099) returns a
// canned "succeeded" envelope so Engine.HandleJobExit unit tests
// can drive the terminal transition without a real vsock UDS.
// Production path waits for the guest-init supervisor's DGRAM
// (port 1026, msg_type 4); the fake is a no-op pass-through.
func (v *fakeVMM) WaitJobExit(_ context.Context, l Lease, _ time.Duration) (JobExitPayload, error) {
	return JobExitPayload{ExitCode: 0, ErrorClass: "succeeded", LeaseToken: l.Instance}, nil
}

func (v *fakeVMM) Restore(ctx context.Context, l Lease, spec RestoreSpec) error {
	v.mu.Lock()
	v.restored = append(v.restored, l.Instance)
	v.mu.Unlock()
	// Same scope-create as Boot — jailer creates the scope on restore too.
	if err := os.MkdirAll(filepath.Join(cgroupRoot, ParentCgroupFor(l.Plan), PerInstanceScope(l.Instance)), 0o755); err != nil {
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
		Plan:       api.PlanHobby,
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

// TestWakeRejectsEmptyPlan pins the up-front Plan validation
// (issue #301 / ADR-043). A wire call with req.Plan = "" must fail
// BEFORE bringUp runs — otherwise the VM would boot under the
// legacy 2-level path (ParentCgroupFor("") = defaultParentCgroup),
// silently disabling cpu.weight + cpu.max enforcement for that
// lease, and the cleanup defer would only fire after the VM was
// up. PR #390 review finding #1 (ship-blocker).
func TestWakeRejectsEmptyPlan(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{}
	m := newTestManager(run, vmm)

	req := WakeRequest{
		Instance:   "ghost",
		BaseKey:    "/b.ext4",
		LayerKey:   "/l.ext4",
		VcpuCount:  2,
		MemSizeMiB: 128,
		Plan:       "", // empty — fail-closed contract
	}
	if _, err := m.Wake(context.Background(), req); err == nil {
		t.Fatal("Wake with empty plan: expected error, got nil")
	} else if !strings.Contains(err.Error(), "invalid plan") {
		t.Errorf("Wake with empty plan: error %q does not mention invalid plan", err)
	}
	// The VM MUST NOT have booted.
	if vmm.boots() != 0 {
		t.Errorf("VM booted %d times despite invalid plan; want 0 (validation must run before bringUp)", vmm.boots())
	}
	// And the lease MUST have been released — a fail-closed plan
	// rejection must not leak the alloc slot.
	if m.LeasedCount() != 0 {
		t.Errorf("LeasedCount = %d after empty-plan rejection, want 0 (lease leak)", m.LeasedCount())
	}
	// Network must not have been set up either — same rationale
	// (rejection is up-front, no I/O side effects).
	if run.ran("netns add fc-ghost") || run.ran("ip netns add fc-ghost") {
		t.Error("netns was created despite invalid plan; want no I/O before validation")
	}
}

// TestWakeRejectsUnknownPlan pins the same fail-closed contract for
// a plan string that's not in api.Plans (e.g. a future plan that
// hasn't been wired into limits.go yet, or a typo'd wire call). Same
// shape as TestWakeRejectsEmptyPlan — must reject, must not boot,
// must not leak.
func TestWakeRejectsUnknownPlan(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{}
	m := newTestManager(run, vmm)

	req := WakeRequest{
		Instance:   "typo",
		BaseKey:    "/b.ext4",
		LayerKey:   "/l.ext4",
		VcpuCount:  2,
		MemSizeMiB: 128,
		Plan:       api.Plan("enterprise"), // not in api.Plans
	}
	if _, err := m.Wake(context.Background(), req); err == nil {
		t.Fatal("Wake with unknown plan: expected error, got nil")
	} else if !strings.Contains(err.Error(), "invalid plan") {
		t.Errorf("Wake with unknown plan: error %q does not mention invalid plan", err)
	}
	if vmm.boots() != 0 {
		t.Errorf("VM booted %d times despite unknown plan; want 0", vmm.boots())
	}
	if m.LeasedCount() != 0 {
		t.Errorf("LeasedCount = %d after unknown-plan rejection, want 0 (lease leak)", m.LeasedCount())
	}
	if run.ran("netns add fc-typo") || run.ran("ip netns add fc-typo") {
		t.Error("netns was created despite unknown plan; want no I/O before validation")
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
		Plan:       api.PlanHobby,
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
		Plan:       api.PlanHobby,
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

// SnapshotKeepAlive (issue #470 / PR #470-FU-A) is the fake hot
// path the engine's captureWarmSnapshotLocked exercises. The
// fake records the call + args and returns the configured
// snapErr on demand. The fakeVMM does NOT touch v.killed because
// the warm path explicitly keeps the VM paused (the engine's
// legacy snapshotAndPark → Kill cycle is still the one that
// releases the chroot).
func (v *fakeVMM) SnapshotKeepAlive(_ context.Context, l Lease, _ SnapshotSpec) (SnapshotInfo, error) {
	v.mu.Lock()
	v.keepAliveSnapshotted = append(v.keepAliveSnapshotted, l.Instance)
	err := v.snapErr
	v.mu.Unlock()
	return SnapshotInfo{MemBytes: 4096}, err
}

// ResumeVM (issue #470 / PR #470-FU-A) is the fake counterpart to
// the host-side Resume: it records the call and returns the
// configured resumeErr. The engine's captureWarmSnapshotLocked
// sets resumeErr to simulate a Firecracker 5xx during resume.
func (v *fakeVMM) ResumeVM(_ context.Context, l Lease) error {
	v.mu.Lock()
	v.resumed = append(v.resumed, l.Instance)
	err := v.resumeErr
	v.mu.Unlock()
	return err
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

// SignalAndKill (M-2 / ADR-138 §Decision 1) records the
// (signal, grace) the engine passed so tests can assert on the
// mode-aware dispatch. Default behaviour is no-op: the
// DestroyWithExport path is not invoked because the engine's
// worker/job dispatch lives in pkg/sched/engine_stop_pgtest_test.go
// (commit 6) — this fake only satisfies the VMM interface so
// bringUp tests continue to compile.
func (v *fakeVMM) SignalAndKill(_ context.Context, l Lease, signal syscall.Signal, grace time.Duration) (bool, int32, error) {
	v.mu.Lock()
	v.signalAndKillCalls = append(v.signalAndKillCalls, signalAndKillCall{Instance: l.Instance, Signal: int32(signal), Grace: grace})
	v.mu.Unlock()
	return false, 0, nil
}

// WaitCharacterizationReport (ADR-051 PR-D) is the host-side
// mirror of the guest-init characterize probe. The fake returns
// the configured characterizationReport (or zero if unset) and
// charReportErr. Tests assert the Wake path calls this on cold
// boots only and that the report lands on the Instance. The
// error branch is wired but Wake treats both branches as
// best-effort (the deploy does not fail on characterize errors).
func (v *fakeVMM) WaitCharacterizationReport(_ context.Context, l Lease, _ time.Duration) (api.CharacterizationReport, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.charReportErr != nil {
		return api.CharacterizationReport{}, v.charReportErr
	}
	return v.characterizationReport, nil
}

func (v *fakeVMM) StageSecretsEnv(_ string, jsonBlob []byte) error {
	v.mu.Lock()
	v.stageCallSeq++
	seq := v.stageCallSeq
	v.stagedSecrets = append(v.stagedSecrets, stagedSecret{seq: seq, blob: append([]byte(nil), jsonBlob...)})
	v.mu.Unlock()
	return v.stageSecretsErr
}

// StageAPIEnv is the plaintext sibling of StageSecretsEnv
// (issue #395 / ADR-045). The fake records the call so tests can
// assert the merged map shape without a real loopback mount.
func (v *fakeVMM) StageAPIEnv(instance string, jsonBlob []byte) error {
	v.mu.Lock()
	v.stageCallSeq++
	seq := v.stageCallSeq
	v.stagedAPIEnv = append(v.stagedAPIEnv, stagedAPIEnvEntry{
		instance: instance,
		seq:      seq,
		blob:     append([]byte(nil), jsonBlob...),
	})
	v.mu.Unlock()
	return v.stageAPIEnvErr
}

// StageWorkloadEnv is the fakeVMM stub for per-sidecar env staging. The
// production VMM writes this JSON to the main workload's instance-scoped
// upper; the fake records the already-unsealed payload for contract tests.
func (v *fakeVMM) StageWorkloadEnv(instance, workloadName string, jsonBlob []byte) error {
	v.mu.Lock()
	v.stagedWorkloadEnvs = append(v.stagedWorkloadEnvs, stagedWorkloadEnv{
		instance:     instance,
		workloadName: workloadName,
		blob:         append([]byte(nil), jsonBlob...),
	})
	v.mu.Unlock()
	return v.stageWorkloadEnvErr
}

// StageWorkloadManifest is the fakeVMM stub for the per-workload
// drive-side staging (issue #463 / ADR-069 / PR-B). Records the
// call so tests can assert the workload spec shape and the
// drive index order without a real loopback mount. The fake
// does NOT validate the manifest blob — the production
// writeWorkloadManifest writes JSON, but the test surface
// only checks call ordering + arg shape.
func (v *fakeVMM) StageWorkloadManifest(instance string, driveIdx int, w WorkloadSpec) error {
	v.mu.Lock()
	v.stagedWorkloads = append(v.stagedWorkloads, stagedWorkload{
		instance: instance,
		driveIdx: driveIdx,
		spec:     w,
	})
	v.mu.Unlock()
	return v.stageWorkloadErr
}

// stagedRoster (issue #463 / ADR-069 / PR-B) captures one
// StageWorkloadRoster call. main is the main workload's spec;
// sidecars is the per-sidecar array (possibly nil/empty).
// Tests assert the call shape (single call, main spec carries
// the plan RAM, sidecars preserve stability order).
type stagedRoster struct {
	instance string
	main     WorkloadSpec
	sidecars []WorkloadSpec
}

// stagedRosters + stageRosterErr mirror the stagedWorkloads shape
// for the roster write. The fake never validates the JSON the
// production writeWorkloadRoster emits — the test surface only
// checks call ordering + arg shape (same posture as
// stagedWorkloads above).
func (v *fakeVMM) StageWorkloadRoster(instance string, main WorkloadSpec, sidecars []WorkloadSpec) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.stagedRosters = append(v.stagedRosters, stagedRoster{
		instance: instance,
		main:     main,
		sidecars: append([]WorkloadSpec(nil), sidecars...),
	})
	return v.stageRosterErr
}

// InstancePID is the in-process fake for the M8 §11 SeccompStatus
// path. fakeVMM never spawns a real process, so the canonical
// "test a real jailing" path is the cmd/e2e sec11_seccomp test
// (which boots vmmd as a subprocess and reads /proc/<pid>/status
// back). The fake answers (0, false) for unknown instances and
// (pids[instance], true) for instances the test has registered
// via boot — tests that want to drive the gRPC handler through
// the fake should set pids before invoking the handler.
func (v *fakeVMM) InstancePID(instance string) (int, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	p, ok := v.pids[instance]
	return p, ok
}

// LogRing (issue #254 / Move 4) is the in-process fake for the
// vmmdgrpc.Logs handler's VMM seam. fakeVMM never spawns a real
// firecracker process, so the canonical "test a real log stream"
// path is cmd/e2e/logs_e2e_test.go (//go:build metal). The fake
// returns nil; tests that want to drive the Logs handler through
// the fake should embed a real *logbuf.Ring and override LogRing.
func (v *fakeVMM) LogRing(_ string) *logbuf.Ring { return nil }

// SendStatelessAdvisory is the VMM-interface hook the guest-init
// fanotify path uses (Wave 0 PR-C / ADR-047). The default no-op
// matches production-stub behaviour: the real wire receiver lives
// in cmd/vmmd and dials the manager, not the VMM. Tests that want
// to drive the VMM-seam path can override.
func (v *fakeVMM) SendStatelessAdvisory(_ context.Context, l Lease, appID string, batch []AdvisoryEvent) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.advisoryCalls == nil {
		v.advisoryCalls = []advisoryCall{}
	}
	v.advisoryCalls = append(v.advisoryCalls, advisoryCall{
		Instance: l.Instance,
		AppID:    appID,
		Batch:    batch,
	})
	return nil
}

// WithEvents (issue #517 / PR-C / ADR-064) is the no-op stub for
// the VMM interface's events hook. The test corpus never reads
// the events fan-out (the production cmd/vmmd wires a real
// Platform); the stub keeps the interface satisfied without
// wiring a Platform through every test seam.
func (v *fakeVMM) WithEvents(_ *events.Platform) VMM { return v }

// advisoryCall mirrors the VMM.SendStatelessAdvisory arguments so
// the test can assert what the VMM saw.
type advisoryCall struct {
	Instance string
	AppID    string
	Batch    []AdvisoryEvent
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
	// Issue #301 / ADR-044 — Manager.Wake validates req.Plan against
	// api.Plan.Valid(). PlanHobby is the cheapest paid tier; tests
	// that don't care which plan they exercise use it.
	return ColdBootRequest{Instance: id, BaseKey: "/b.ext4", LayerKey: "/l.ext4", VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby}
}

// reqWithPort mirrors req() but stamps a per-deployment override port
// (issue #460 / ADR-053, PR-C). Used by the port-propagation test that
// exercises ColdBoot → WakeRequest → Instance.Port.
func reqWithPort(id string, port int) ColdBootRequest {
	r := req(id)
	r.Port = port
	return r
}

// reqWithHealthcheck mirrors req() but stamps a per-deployment override
// readiness probe path (issue #460 / ADR-053, ADR-057 / PR-D). Used by
// the healthcheck-propagation test that exercises ColdBoot →
// WakeRequest → Instance.HealthcheckPath.
func reqWithHealthcheck(id, path string) ColdBootRequest {
	r := req(id)
	r.HealthcheckPath = path
	return r
}

// reqWithDeploymentID mirrors req() but stamps a deployment_id on
// the WakeRequest (issue #463 / ADR-069 / PR-B AC #1). Used by the
// deployment-id-propagation test that exercises ColdBoot →
// WakeRequest → Instance.DeploymentID.
func reqWithDeploymentID(id, depID string) ColdBootRequest {
	r := req(id)
	r.DeploymentID = depID
	return r
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

// TestColdBootSuccessStampsInstancePort pins issue #460 / ADR-053
// (PR-C): when ColdBootRequest carries a per-deployment override
// port, the live Instance must carry that port so the vmmdgrpc
// forwarder (or any other server-side reader that walks m.live) can
// resolve the per-instance dial port without a second lookup. A
// regression that drops the propagation forces the forwarder to
// re-resolve from the deployment row on every request — breaking
// the "in-memory cache" invariant vmmdgrpc.forward.go relies on.
func TestColdBootSuccessStampsInstancePort(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	inst, err := m.ColdBoot(context.Background(), reqWithPort("i1", 9090))
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	if inst.Port != 9090 {
		t.Errorf("Instance.Port = %d, want 9090 (inst=%+v)", inst.Port, inst)
	}
	if m.LiveCount() != 1 {
		t.Errorf("LiveCount = %d, want 1 (the live map should hold the stamped instance)", m.LiveCount())
	}
}

// TestColdBootSuccessStampsInstanceHealthcheckPath pins issue #460 /
// ADR-053, ADR-057 (PR-D): when ColdBootRequest carries a per-deployment
// override readiness probe path, the live Instance must carry that path
// so server-side readers (e.g. a future observability probe) can
// resolve the per-instance probe target without a second lookup. A
// regression that drops the propagation forces the waiter to re-read
// from the deployment row on every request — breaking the same
// in-memory-cache invariant that TestColdBootSuccessStampsInstancePort
// (PR-C) covers for the port side.
//
// Mirror of the PR-C port test, which established the pattern.
func TestColdBootSuccessStampsInstanceHealthcheckPath(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	inst, err := m.ColdBoot(context.Background(), reqWithHealthcheck("i1", "/healthz"))
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	if inst.HealthcheckPath != "/healthz" {
		t.Errorf("Instance.HealthcheckPath = %q, want %q (inst=%+v)", inst.HealthcheckPath, "/healthz", inst)
	}
	if m.LiveCount() != 1 {
		t.Errorf("LiveCount = %d, want 1 (the live map should hold the stamped instance)", m.LiveCount())
	}
}

// TestColdBootSuccessStampsInstanceDeploymentID pins issue #463 /
// ADR-069 / PR-B AC #1: when ColdBootRequest carries a deployment_id,
// the live Instance must carry it so the vsock DGRAM
// sidecar-init-failed dispatch (cmd/vmmd/framework_ready_recv.go)
// can flip the deployments row back to status='failed' on init
// exit without a separate apid pg_notify bridge. Mirror of the
// PR-C port / PR-D healthcheck propagation tests.
//
// A regression that drops the propagation breaks the AC #1
// end-to-end contract: the dispatch resolves the deployment_id from
// the live Instance, and a missing stamp leaves "" — the dispatch
// silently no-ops, the deploy row stays in its current status, and
// the customer's "deploy failed" UI shows nothing.
func TestColdBootSuccessStampsInstanceDeploymentID(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	inst, err := m.ColdBoot(context.Background(), reqWithDeploymentID("i1", "dep-1"))
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	if inst.DeploymentID != "dep-1" {
		t.Errorf("Instance.DeploymentID = %q, want %q (inst=%+v)", inst.DeploymentID, "dep-1", inst)
	}
	if m.LiveCount() != 1 {
		t.Errorf("LiveCount = %d, want 1 (the live map should hold the stamped instance)", m.LiveCount())
	}
}

// TestColdBootEmptyHealthcheckPathIsEmpty verifies the legacy / no-override
// path: when ColdBootRequest carries no HealthcheckPath, the live Instance
// must carry the empty string so the waitReady HTTP probe branch is
// skipped (it falls through to the legacy TCP-accept on :8080). The
// negation of TestColdBootSuccessStampsInstanceHealthcheckPath — both
// together pin the propagation contract.
func TestColdBootEmptyHealthcheckPathIsEmpty(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	inst, err := m.ColdBoot(context.Background(), req("i1"))
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	if inst.HealthcheckPath != "" {
		t.Errorf("Instance.HealthcheckPath = %q, want empty (legacy TCP-accept on :8080)", inst.HealthcheckPath)
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

func TestDestroyCancelsLivenessLoop(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	registry := NewLivenessRegistry()
	cancelled := make(chan struct{})
	m.WithLivenessProbes(registry, LivenessProbeConfig{
		PeriodSeconds:       5,
		ConsecutiveFailures: 3,
	}).WithLivenessProbeStarter(func(context.Context, string, int, string, LivenessProbeConfig) context.CancelFunc {
		return func() {
			select {
			case <-cancelled:
			default:
				close(cancelled)
			}
		}
	})
	m.mu.Lock()
	m.live["i-live"] = &Instance{Lease: Lease{Instance: "i-live", Slot: 1}}
	m.mu.Unlock()
	m.startLivenessLoop(context.Background(), "i-live", 1, nil)

	if err := m.Destroy(context.Background(), "i-live"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("destroy did not cancel the liveness loop")
	}
	registry.mu.Lock()
	remaining := len(registry.loops)
	registry.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("liveness registry retains %d loop(s) after destroy", remaining)
	}
}

func TestParkCancelsLivenessLoop(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)
	registry := NewLivenessRegistry()
	cancelled := make(chan struct{})
	m.WithLivenessProbes(registry, LivenessProbeConfig{
		PeriodSeconds:       5,
		ConsecutiveFailures: 3,
	}).WithLivenessProbeStarter(func(context.Context, string, int, string, LivenessProbeConfig) context.CancelFunc {
		return func() {
			select {
			case <-cancelled:
			default:
				close(cancelled)
			}
		}
	})
	if _, err := m.ColdBoot(context.Background(), req("i-park")); err != nil {
		t.Fatal(err)
	}
	m.startLivenessLoop(context.Background(), "i-park", 1, nil)

	if _, err := m.Park(context.Background(), "i-park", SnapshotSpec{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("park did not cancel the liveness loop")
	}
	registry.mu.Lock()
	remaining := len(registry.loops)
	registry.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("liveness registry retains %d loop(s) after park", remaining)
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type bootResult struct {
		instance string
		err      error
	}
	bootResults := make(chan bootResult, n)
	for i := 0; i < n; i++ {
		bootCtx, bootCancel := context.WithTimeout(ctx, time.Second)
		go func(i int, bootCtx context.Context, bootCancel context.CancelFunc) {
			defer bootCancel()
			instance := fmt.Sprintf("i%d", i)
			_, err := m.ColdBoot(bootCtx, req(instance))
			bootResults <- bootResult{instance: instance, err: err}
		}(i, bootCtx, bootCancel)
	}
	for i := 0; i < n; i++ {
		select {
		case result := <-bootResults:
			if result.err != nil {
				t.Errorf("boot %s: %v", result.instance, result.err)
			}
		case <-ctx.Done():
			t.Fatalf("concurrent boot batch exceeded deadline: %v", ctx.Err())
		}
	}
	if m.LiveCount() != n || m.LeasedCount() != n {
		t.Fatalf("after boot: live=%d leased=%d, want %d/%d", m.LiveCount(), m.LeasedCount(), n, n)
	}

	destroyResults := make(chan bootResult, n)
	for i := 0; i < n; i++ {
		destroyCtx, destroyCancel := context.WithTimeout(ctx, time.Second)
		go func(i int, destroyCtx context.Context, destroyCancel context.CancelFunc) {
			defer destroyCancel()
			instance := fmt.Sprintf("i%d", i)
			err := m.Destroy(destroyCtx, instance)
			destroyResults <- bootResult{instance: instance, err: err}
		}(i, destroyCtx, destroyCancel)
	}
	for i := 0; i < n; i++ {
		select {
		case result := <-destroyResults:
			if result.err != nil {
				t.Errorf("destroy %s: %v", result.instance, result.err)
			}
		case <-ctx.Done():
			t.Fatalf("concurrent destroy batch exceeded deadline: %v", ctx.Err())
		}
	}
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
		Plan:     api.PlanHobby,
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

// TestWakeRejectsEgressAllowlist_v6Accepted: ADR-032 v6 mirror. v6
// entries must pass the wire-side parse + family gate (the v4-only
// reject from PR #159 is gone). The /0 reject (Bits()==0) is the
// only remaining per-entry guard at the wire; everything else is
// the DB trigger's job. This test pins that a v6 entry ADVANCES
// past the parse loop — it either succeeds (preferred) or fails
// for an unrelated reason further down the path (e.g. the fakeVMM
// stub doesn't implement every step). The key assertion is that
// the error, if any, does NOT say "v4 only".
func TestWakeRejectsEgressAllowlist_v6Accepted(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{}
	m := newTestManager(run, vmm)

	_, err := m.Wake(context.Background(), WakeRequest{
		Instance:   "vw6",
		BaseKey:    "/b.ext4",
		LayerKey:   "/l.ext4",
		VcpuCount:  2,
		MemSizeMiB: 128,
		Plan:       api.PlanHobby,
		// v6 prefix: ADR-032 accepts this; renderer partitions
		// into a separate ip6 faas forward rule.
		EgressAllowlist: []string{"fe80::/10"},
	})
	// The test does not assert err == nil — fakeVMM may short-
	// circuit at any step (StartInstance, etc.). What we DO
	// assert: the parse gate didn't trip on the v6 entry, so the
	// error does not name the v6 entry as the offender.
	if err != nil && strings.Contains(err.Error(), "fe80::/10") {
		t.Fatalf("Wake with v6 EgressAllowlist entry: error names the CIDR — parse gate regressed: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "v4 only") {
		t.Fatalf("Wake with v6 EgressAllowlist entry: error says 'v4 only' — ADR-032 wire gate regressed: %v", err)
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
		Plan:            api.PlanHobby,
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

// TestWakeRejectsEgressAllowlist_V6SlashZeroClosed: ADR-032 v6 mirror
// of the non-/0 contract. `::/0` would unblock the entire IPv6
// internet and make the v6 allowlist a no-op, so the wire-side
// Bits()==0 reject still trips regardless of family. The DB trigger
// also rejects it (migration 00033), but the wire gate is the
// defence-in-depth layer if the DB is bypassed (e.g. a future
// migration that loosens the trigger).
func TestWakeRejectsEgressAllowlist_V6SlashZeroClosed(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{}
	m := newTestManager(run, vmm)

	_, err := m.Wake(context.Background(), WakeRequest{
		Instance:        "w6z",
		BaseKey:         "/b.ext4",
		LayerKey:        "/l.ext4",
		VcpuCount:       2,
		MemSizeMiB:      128,
		Plan:            api.PlanHobby,
		EgressAllowlist: []string{"::/0"},
	})
	if err == nil {
		t.Fatal("Wake with v6 /0 EgressAllowlist entry: expected fail-closed, got success")
	}
	if !strings.Contains(err.Error(), "::/0") {
		t.Errorf("error should name the offending CIDR; got: %v", err)
	}
	if !strings.Contains(err.Error(), "masklen") && !strings.Contains(err.Error(), "/0") {
		t.Errorf("error should mention the non-/0 invariant; got: %v", err)
	}
	if run.ran("nft") {
		t.Error("nft commands ran before v6 /0 rejection — render order regressed")
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
		Plan:     api.PlanHobby,
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

// TestInstanceDeploymentIDAndAppID_KnownInstanceReturnsBoth pins the
// helper added for issue #463 / ADR-069 / PR-B AC #1: the sidecar-
// init-failed dispatch in vmmd resolves both IDs in a single
// lock-held read so a Park racing the DGRAM recv returns a
// consistent pair. The legacy InstanceAppID helper would require
// two lock acquisitions; the new helper collapses both into one.
func TestInstanceDeploymentIDAndAppID_KnownInstanceReturnsBoth(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.live["inst-x"] = &Instance{
		AppID:        "app-x",
		DeploymentID: "dep-x",
	}
	depID, appID, err := m.InstanceDeploymentIDAndAppID("inst-x")
	if err != nil {
		t.Fatalf("InstanceDeploymentIDAndAppID: %v", err)
	}
	if depID != "dep-x" {
		t.Errorf("deployment_id: got %q, want %q", depID, "dep-x")
	}
	if appID != "app-x" {
		t.Errorf("app_id: got %q, want %q", appID, "app-x")
	}
}

// TestInstanceDeploymentIDAndAppID_EmptyDeploymentIDForLegacy
// pins the empty-deployment-id outcome for pre-PR-B callers. The
// dispatch path tolerates "" by skipping the deploy-row flip on
// init_failed — the audit row still lands so the dispatch is
// observable.
func TestInstanceDeploymentIDAndAppID_EmptyDeploymentIDForLegacy(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.live["inst-y"] = &Instance{
		AppID:        "app-y",
		DeploymentID: "",
	}
	depID, appID, err := m.InstanceDeploymentIDAndAppID("inst-y")
	if err != nil {
		t.Fatalf("InstanceDeploymentIDAndAppID: %v", err)
	}
	if depID != "" {
		t.Errorf("deployment_id: got %q, want empty (legacy)", depID)
	}
	if appID != "app-y" {
		t.Errorf("app_id: got %q, want %q", appID, "app-y")
	}
}

// TestInstanceDeploymentIDAndAppID_UnknownReturnsError pins the
// missing-instance branch: the helper must surface a "not live"
// error so the dispatch path can log + drop the DGRAM instead of
// silently no-op'ing (which would mask a real Park race).
func TestInstanceDeploymentIDAndAppID_UnknownReturnsError(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	_, _, err := m.InstanceDeploymentIDAndAppID("ghost")
	if err == nil {
		t.Fatal("expected error on unknown instance")
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
	m.cleanup(context.Background(), lease, nc, nil)
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

func TestRunNftCommandsUsesSingleAtomicBatch(t *testing.T) {
	run := &fakeInputRunner{}
	m := newTestManager(run, &fakeVMM{})
	nc := netns.NewConfig("i-1", "fc-i-1", "vh1", "vp1", netip.MustParseAddr("10.100.0.2"))
	cmds := nc.NftCommands()

	if err := m.runNftCommands(context.Background(), nc.Netns, cmds); err != nil {
		t.Fatalf("runNftCommands: %v", err)
	}
	if run.inputRuns != 1 {
		t.Fatalf("batch runs = %d, want 1", run.inputRuns)
	}
	if got := strings.Join(run.inputArgv, " "); got != "ip netns exec fc-i-1 nft -f -" {
		t.Fatalf("batch argv = %q", got)
	}
	for _, want := range []string{
		"add table ip faas\n",
		"dnat to 10.0.0.2:8080\n",
		"add table ip6 faas\n",
	} {
		if !bytes.Contains(run.input, []byte(want)) {
			t.Errorf("batch missing %q in:\n%s", want, run.input)
		}
	}
	if len(run.commands) != 0 {
		t.Fatalf("batched rules also ran individually: %v", run.commands)
	}
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
	// PR-E: per-CIDR deny lines. The cap-rule-precedes-daddr-drop
	// ordering invariant still holds — pick the FIRST per-CIDR rule
	// in each family. Catalog sort order (v4 prefix asc, v6 prefix
	// asc — see NewDefaultDenySet) is the same as before, so the
	// pre-PR-E v4 first entry (10.0.0.0/8) and v6 first entry
	// (::/128) are still the leading per-CIDR rules.
	daddrDropV4 := indexOfArgv(run.commands, "ip daddr 10.0.0.0/8 counter name")
	daddrDropV6 := indexOfArgv(run.commands, "ip6 daddr ::/128 counter name")
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
//
// Sweeps the four deployed plan RAMs (128/256/512/1024 MB per
// pkg/api/limits.go). A regression in the (plan+PerVMOverheadMB)
// arithmetic that happens to satisfy plan=128 still passes a
// single-value test but trips here. The cross-process e2e
// (cmd/e2e/sec11_memory_max_e2e_test.go, //go:build metal) is the
// authoritative gate that asserts the same fence against a real
// jailer's /sys/fs/cgroup; this is the layer-down pin that runs
// on every-PR CI.
func TestWakeWritesMemoryMaxAfterBringUp(t *testing.T) {
	for _, planMB := range []int{128, 256, 512, 1024} {
		t.Run(fmt.Sprintf("%dMB", planMB), func(t *testing.T) {
			run, vmm := &fakeRunner{}, &fakeVMM{}
			m := newTestManager(run, vmm)

			instID := fmt.Sprintf("cgroup-order-%d", planMB)
			r := req(instID)
			r.MemSizeMiB = planMB

			if _, err := m.ColdBoot(context.Background(), r); err != nil {
				t.Fatalf("cold boot (plan=%d): %v", planMB, err)
			}
			if vmm.boots() != 1 {
				t.Fatalf("expected 1 boot, got %d", vmm.boots())
			}
			// Issue #301 / ADR-044: 3-level hierarchy
			//   <cgroupRoot>/faas-tenant.slice/<plan-slice>/<instance>/
			// ParentCgroupFor(plan) already includes the
			// "faas-tenant.slice/" prefix, so the path is just
			// cgroupRoot + ParentCgroupFor(plan) + PerInstanceScope(instID).
			memPath := filepath.Join(cgroupRoot, ParentCgroupFor(r.Plan), PerInstanceScope(instID), "memory.max")
			body, err := os.ReadFile(memPath)
			if err != nil {
				t.Fatalf("memory.max not written at %s: %v", memPath, err)
			}
			want := int64(planMB+api.PerVMOverheadMB) << 20
			got := strings.TrimSpace(string(body))
			if got != itoa(int(want)) {
				t.Errorf("memory.max = %q, want %d", got, want)
			}
		})
	}
}

// TestWakeCgroupWriteFailureUnwindsNetns covers the leak invariant
// when the post-bringUp cgroup write itself fails. The cleanup
// defer in Wake must still tear down the netns and release the lease
// so a transient cgroup permission issue doesn't leak. We inject a
// cgroup failure via fakeVMM.bootCgroupFail so the test works
// regardless of whether it runs as root (root can bypass fs permissions).
func TestWakeCgroupWriteFailureUnwindsNetns(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	// Inject a synthetic cgroup write failure — same shape as what
	// writeMemoryMax would return if the cgroup scope was unwritable.
	vmm.bootCgroupFail = errors.New("cgroup write: open /sys/fs/cgroup/faas-tenant.slice/vm-cgroup-fail/cgroup.controller: permission denied")
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

// TestWakePostBootCgroupFenceFailsClosed covers the production fence write
// itself. fakeVMM.Boot creates the jailer scope but intentionally leaves the
// memory.max/cpu.max files absent, so Manager.Wake reaches writePlanCgroup and
// must reject the otherwise-ready VM instead of exposing it uncapped.
func TestWakePostBootCgroupFenceFailsClosed(t *testing.T) {
	withFakeCgroupRoot(t)
	run, vmm := &fakeRunner{}, &fakeVMM{postBootCgroupBlock: true}
	m := newTestManager(run, vmm)

	_, err := m.ColdBoot(context.Background(), req("post-cgroup-fail"))
	if err == nil {
		t.Fatal("expected post-boot cgroup fence failure")
	}
	if m.LeasedCount() != 0 {
		t.Errorf("lease leaked after post-boot cgroup failure: leased=%d", m.LeasedCount())
	}
	if m.LiveCount() != 0 {
		t.Errorf("VM remained live after post-boot cgroup failure: live=%d", m.LiveCount())
	}
	if !run.ran("netns del fc-post-cgroup-fail") {
		t.Error("cleanup did not delete netns after post-boot cgroup failure")
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

// fakeCaptureRunner is the stdout-aware runner stub used by the
// UpdateEgressAllowlist unit tests. The real nft tool prints
// `chain forward { ... iifname "tap0" ip daddr { 1.2.3.0/24 } accept # handle 7 }`
// on success; the fake synthesises that output with a configurable
// handle so the wake path's handle capture can be exercised.
type fakeCaptureRunner struct {
	mu sync.Mutex
	// listChainOutput is the bytes the next `nft -a list chain` call
	// returns. Tests set it to a synthesised nft ruleset so
	// captureAllowlistHandles resolves a known handle.
	listChainOutput []byte
	// listChainErr, when non-nil, is returned by the next
	// RunCapture call (the test exercises the failure path).
	listChainErr error
	// commands records every argv the runner saw (parallels
	// fakeRunner.commands so the test can assert what
	// captureAllowlistHandles actually invoked).
	commands [][]string
}

func (f *fakeCaptureRunner) RunCapture(_ context.Context, argv []string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, argv)
	if f.listChainErr != nil {
		return nil, f.listChainErr
	}
	return f.listChainOutput, nil
}

// TestUpdateEgressAllowlist_NoLiveInstancesIsNoop — the empty
// app is the redelivery / no-live-targets path. No nft commands
// should fire, no error.
func TestUpdateEgressAllowlist_NoLiveInstancesIsNoop(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	if err := m.UpdateEgressAllowlist(context.Background(), "app-orphan", []netip.Prefix{
		netip.MustParsePrefix("1.2.3.0/24"),
	}); err != nil {
		t.Fatalf("UpdateEgressAllowlist: %v", err)
	}
	if run.ran("nft") {
		t.Error("nft should not run when no live instances match the app")
	}
}

// TestUpdateEgressAllowlist_AppliesV4Patch — a fresh netns
// (bootstrapped via a direct setupNetwork call) plus a single
// in-place patch must emit exactly one delete-by-handle (the
// prior handle captured at wake time) plus one add rule.
func TestUpdateEgressAllowlist_AppliesV4Patch(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	nc := netns.NewConfig("i-1", "fc-i-1", "vh1", "vp1", netip.MustParseAddr("10.100.0.2"))
	// Seed the live map with a synthetic instance whose
	// prior allowlist has a v4 entry; the renderer
	// already ran at wake time (captured by captureAllowlistHandles
	// in production; here we just hand-craft the prior state so
	// the patch path has something to delete).
	inst := &Instance{
		Lease:             Lease{Instance: "i-1", UID: 20001},
		Net:               nc,
		Method:            WakeColdBoot,
		AppID:             "app-1",
		AllowlistHandleV4: 7,
	}
	inst.Net.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24")}
	m.mu.Lock()
	m.live["i-1"] = inst
	m.mu.Unlock()

	// Patch to a different v4 prefix.
	if err := m.UpdateEgressAllowlist(context.Background(), "app-1", []netip.Prefix{
		netip.MustParsePrefix("8.8.8.0/24"),
	}); err != nil {
		t.Fatalf("UpdateEgressAllowlist: %v", err)
	}
	// The patch sequence must include: delete-by-handle 7 on the
	// v4 chain, then add the new 8.8.8.0/24 rule. Each argv is
	// per-netns: `ip netns exec fc-i-1 nft …`. Note: the tap
	// name is "tap0" verbatim in argv (no quotes) — nft's
	// output printer adds quotes when listing rules; the
	// argv-side tokenisation is the literal string.
	wantDelete := `ip netns exec fc-i-1 nft delete rule ip faas forward handle 7`
	if !run.ran(wantDelete) {
		t.Errorf("missing %q in command stream", wantDelete)
	}
	wantAdd := `ip netns exec fc-i-1 nft add rule ip faas forward iifname tap0 ip daddr { 8.8.8.0/24 } accept`
	if !run.ran(wantAdd) {
		t.Errorf("missing %q in command stream", wantAdd)
	}
	// Cached state refreshed: the next patch's fast-path
	// compares against the new baseline.
	m.mu.Lock()
	defer m.mu.Unlock()
	if got := m.live["i-1"].Net.EgressAllowlist[0].String(); got != "8.8.8.0/24" {
		t.Errorf("cached allowlist = %q, want 8.8.8.0/24", got)
	}
	if m.live["i-1"].AllowlistHandleV4 != 7 {
		t.Errorf("cached v4 handle = %d, want 7 (capture is best-effort, no -a reader in tests)", m.live["i-1"].AllowlistHandleV4)
	}
}

// TestUpdateEgressAllowlist_SameAllowlistNoOp — redelivery.
// The same allowlist twice should not run nft at all.
func TestUpdateEgressAllowlist_SameAllowlistNoOp(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	nc := netns.NewConfig("i-2", "fc-i-2", "vh2", "vp2", netip.MustParseAddr("10.100.0.3"))
	inst := &Instance{
		Lease: Lease{Instance: "i-2", UID: 20002},
		Net:   nc,
		AppID: "app-2",
	}
	inst.Net.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24")}
	m.mu.Lock()
	m.live["i-2"] = inst
	m.mu.Unlock()

	// First push — a different allowlist, should run nft
	// (prior handle is 0 so no delete, just the add).
	if err := m.UpdateEgressAllowlist(context.Background(), "app-2", []netip.Prefix{
		netip.MustParsePrefix("8.8.8.0/24"),
	}); err != nil {
		t.Fatalf("first push: %v", err)
	}
	if !run.ran("nft add rule") {
		t.Fatal("first push should have run nft")
	}
	// Second push — same allowlist as the cached baseline
	// (8.8.8.0/24 after the first push). Idempotent fast-path
	// (samePrefixSet) should short-circuit before any nft exec.
	preCount := len(run.commands)
	if err := m.UpdateEgressAllowlist(context.Background(), "app-2", []netip.Prefix{
		netip.MustParsePrefix("8.8.8.0/24"),
	}); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if got := len(run.commands); got != preCount {
		t.Errorf("redelivery ran %d new commands, want 0 (samePrefixSet no-op)", got-preCount)
	}
}

// TestUpdateEgressAllowlist_NftErrorReverts — when the add
// step fails, the prior allowlist argv is re-emitted (best
// effort) so the live netns returns to the pre-patch state.
func TestUpdateEgressAllowlist_NftErrorReverts(t *testing.T) {
	run := &fakeRunner{failOn: "8.8.8.0/24"}
	m := newTestManager(run, &fakeVMM{})
	nc := netns.NewConfig("i-3", "fc-i-3", "vh3", "vp3", netip.MustParseAddr("10.100.0.4"))
	inst := &Instance{
		Lease: Lease{Instance: "i-3", UID: 20003},
		Net:   nc,
		AppID: "app-3",
	}
	inst.Net.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24")}
	m.mu.Lock()
	m.live["i-3"] = inst
	m.mu.Unlock()

	err := m.UpdateEgressAllowlist(context.Background(), "app-3", []netip.Prefix{
		netip.MustParsePrefix("8.8.8.0/24"),
	})
	if err == nil {
		t.Fatal("UpdateEgressAllowlist should have failed when the add step errors")
	}
	// Revert path: the prior v4 rule was re-emitted.
	if !run.ran("1.2.3.0/24") {
		t.Error("revert did not re-emit the prior v4 rule")
	}
}

// TestUpdateEgressAllowlist_FansOutAcrossLiveInstances —
// 2 live instances of the same app, distinct v4 prefixes;
// both receive the new rule.
func TestUpdateEgressAllowlist_FansOutAcrossLiveInstances(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	for i, id := range []string{"a", "b"} {
		nc := netns.NewConfig("i-"+id, "fc-i-"+id, "vh-"+id, "vp-"+id, netip.MustParseAddr(fmt.Sprintf("10.100.0.%d", 10+i)))
		inst := &Instance{
			Lease: Lease{Instance: "i-" + id, UID: 20010 + i},
			Net:   nc,
			AppID: "app-shared",
		}
		inst.Net.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix(fmt.Sprintf("1.1.%d.0/24", i+1))}
		m.mu.Lock()
		m.live["i-"+id] = inst
		m.mu.Unlock()
	}
	if err := m.UpdateEgressAllowlist(context.Background(), "app-shared", []netip.Prefix{
		netip.MustParsePrefix("8.8.8.0/24"),
	}); err != nil {
		t.Fatalf("UpdateEgressAllowlist: %v", err)
	}
	// Both live netns got the new rule.
	count := 0
	for _, c := range run.commands {
		if strings.Contains(strings.Join(c, " "), "8.8.8.0/24") {
			count++
		}
	}
	if count != 2 {
		t.Errorf("new rule emitted %d times, want 2 (one per live netns)", count)
	}
}

// TestUpdateEgressAllowlist_RejectsEmptyAppID — defensive.
func TestUpdateEgressAllowlist_RejectsEmptyAppID(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	if err := m.UpdateEgressAllowlist(context.Background(), "", nil); err == nil {
		t.Fatal("expected error for empty app_id")
	}
	if run.ran("nft") {
		t.Error("nft must not run on empty app_id")
	}
}

// TestCaptureAllowlistHandles — listChainHandles parses a
// synthesised nft -a list chain output and returns the right
// handle for both v4 and v6.
func TestCaptureAllowlistHandles(t *testing.T) {
	out := []byte(`table ip faas {
	chain forward {
	 type filter hook forward priority 0; policy accept;
	 iifname "tap0" ip daddr { 1.2.3.0/24 } accept # handle 42
	}
}
table ip6 faas {
	chain forward {
	 type filter hook forward priority 0; policy accept;
	 iifname "tap0" ip6 daddr { 2001:db8::/32 } accept # handle 99
	}
}
`)
	cap := &fakeCaptureRunner{listChainOutput: out}
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithCaptureRunner(cap)
	hV4, hV6, err := m.captureAllowlistHandles(context.Background(), "fc-i-1")
	if err != nil {
		t.Fatalf("captureAllowlistHandles: %v", err)
	}
	if hV4 != 42 {
		t.Errorf("hV4 = %d, want 42", hV4)
	}
	if hV6 != 99 {
		t.Errorf("hV6 = %d, want 99", hV6)
	}
}

// TestCaptureAllowlistHandles_NilRunnerLeavesHandlesZero —
// the optional seam: nil capture runner means we leave
// AllowlistHandle{V4,V6} at 0 (the next patch picks them up
// via the chain list).
func TestCaptureAllowlistHandles_NilRunnerLeavesHandlesZero(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	hV4, hV6, err := m.captureAllowlistHandles(context.Background(), "fc-i-1")
	if err != nil {
		t.Fatalf("captureAllowlistHandles: %v", err)
	}
	if hV4 != 0 || hV6 != 0 {
		t.Errorf("nil runner should return 0,0; got %d,%d", hV4, hV6)
	}
}

// TestCaptureAllowlistHandlesForWake_SkipsEmptyAllowlist ensures a default
// wake does not pay for nft chain listings when the renderer emitted no
// allowlist rule.
func TestCaptureAllowlistHandlesForWake_SkipsEmptyAllowlist(t *testing.T) {
	cap := &fakeCaptureRunner{listChainOutput: []byte(`chain forward {}`)}
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithCaptureRunner(cap)
	hV4, hV6, err := m.captureAllowlistHandlesForWake(context.Background(), "fc-i-1", nil)
	if err != nil {
		t.Fatalf("captureAllowlistHandlesForWake: %v", err)
	}
	if hV4 != 0 || hV6 != 0 {
		t.Fatalf("empty allowlist handles = (%d, %d), want (0, 0)", hV4, hV6)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.commands) != 0 {
		t.Fatalf("empty allowlist made %d nft capture calls, want 0", len(cap.commands))
	}
}

func TestCaptureAllowlistHandlesForWake_CapturesConfiguredAllowlist(t *testing.T) {
	out := []byte(`
 iifname "tap0" ip daddr { 1.2.3.0/24 } accept # handle 42
 iifname "tap0" ip6 daddr { 2001:db8::/32 } accept # handle 99
`)
	cap := &fakeCaptureRunner{listChainOutput: out}
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithCaptureRunner(cap)
	allowlist := []netip.Prefix{
		netip.MustParsePrefix("1.2.3.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	hV4, hV6, err := m.captureAllowlistHandlesForWake(context.Background(), "fc-i-1", allowlist)
	if err != nil {
		t.Fatalf("captureAllowlistHandlesForWake: %v", err)
	}
	if hV4 != 42 || hV6 != 99 {
		t.Fatalf("configured allowlist handles = (%d, %d), want (42, 99)", hV4, hV6)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.commands) != 2 {
		t.Fatalf("configured allowlist made %d nft capture calls, want 2", len(cap.commands))
	}
}

func TestCaptureAllowlistHandlesForWake_SkipsUnrepresentedFamily(t *testing.T) {
	out := []byte(`
 iifname "tap0" ip daddr { 1.2.3.0/24 } accept # handle 42
`)
	cap := &fakeCaptureRunner{listChainOutput: out}
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithCaptureRunner(cap)
	hV4, hV6, err := m.captureAllowlistHandlesForWake(context.Background(), "fc-i-1", []netip.Prefix{
		netip.MustParsePrefix("1.2.3.0/24"),
	})
	if err != nil {
		t.Fatalf("captureAllowlistHandlesForWake: %v", err)
	}
	if hV4 != 42 || hV6 != 0 {
		t.Fatalf("v4-only allowlist handles = (%d, %d), want (42, 0)", hV4, hV6)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.commands) != 1 {
		t.Fatalf("v4-only allowlist made %d nft capture calls, want 1", len(cap.commands))
	}
	if !strings.Contains(strings.Join(cap.commands[0], " "), " nft -a list chain ip faas forward") {
		t.Fatalf("v4-only capture used unexpected command: %v", cap.commands[0])
	}
}

// TestUpdateEgressAllowlist_TwoPatchesInRow — the regression
// the review called out: a second UpdateEgressAllowlist call on
// the same app, with a DIFFERENT allowlist, must succeed. Before
// the fix, the second patch's "delete by handle" targeted the
// original handle (which was already deleted by the first patch's
// delete step). The fix is to call listChainHandles after each
// successful add and update the cached AllowlistHandleV4/V6
// before the write-back.
//
// With a nil captureRunner we can't observe the kernel-assigned
// handle, so the cached handle stays at the prior value. The
// test sets the prior handle to 0 (the fresh-Wake state) and
// asserts that two back-to-back patches BOTH succeed: the first
// patch sees handleV4=0 → no delete step (just add); the second
// patch sees handleV4=0 in the snapshot (because the unit suite
// doesn't surface the kernel-assigned handle), emits no delete
// step, and just adds the new rule. The live netns ends up with
// the most recent allowlist argv.
func TestUpdateEgressAllowlist_TwoPatchesInRow(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	nc := netns.NewConfig("i-2p", "fc-i-2p", "vh-2p", "vp-2p", netip.MustParseAddr("10.100.0.20"))
	inst := &Instance{
		Lease: Lease{Instance: "i-2p", UID: 20020},
		Net:   nc,
		AppID: "app-2p",
		// No handle captured — fresh Wake simulation.
	}
	inst.Net.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24")}
	m.mu.Lock()
	m.live["i-2p"] = inst
	m.mu.Unlock()

	// First patch: different allowlist.
	if err := m.UpdateEgressAllowlist(context.Background(), "app-2p", []netip.Prefix{
		netip.MustParsePrefix("8.8.8.0/24"),
	}); err != nil {
		t.Fatalf("patch 1: %v", err)
	}
	// Second patch: another different allowlist. The cached
	// handle is still 0 (no capture runner), so the delete
	// step is skipped and the add succeeds. The cached
	// allowlist is the most recent.
	if err := m.UpdateEgressAllowlist(context.Background(), "app-2p", []netip.Prefix{
		netip.MustParsePrefix("9.9.9.0/24"),
	}); err != nil {
		t.Fatalf("patch 2: %v", err)
	}
	// Both new rules must have been emitted. The 1.2.3.0/24
	// prior should NEVER appear (no delete step runs because
	// handleV4=0 throughout).
	if run.ran("1.2.3.0/24") {
		t.Errorf("patch should not have re-emitted the prior CGI: 1.2.3.0/24")
	}
	if !run.ran("8.8.8.0/24") {
		t.Errorf("patch 1's add argv missing: 8.8.8.0/24")
	}
	if !run.ran("9.9.9.0/24") {
		t.Errorf("patch 2's add argv missing: 9.9.9.0/24")
	}
	// Cached state matches the most recent patch.
	m.mu.Lock()
	defer m.mu.Unlock()
	if got := m.live["i-2p"].Net.EgressAllowlist[0].String(); got != "9.9.9.0/24" {
		t.Errorf("cached allowlist = %q, want 9.9.9.0/24", got)
	}
}

// TestUpdateEgressAllowlist_TwoPatchesInRow_WithCaptureRunner —
// the load-bearing pair to TestUpdateEgressAllowlist_TwoPatchesInRow:
// when the captureRunner is wired, the second patch must observe
// the post-first-patch handle (a fresh kernel-assigned integer)
// and use it for the delete-by-handle call. This is the path the
// metal test exercises on the EX44.
//
// fakeCaptureRunner returns a consecutive handle sequence
// (1, 2, 3, ...) so the test can assert the second patch's
// delete-by-handle call targets the latest handle.
func TestUpdateEgressAllowlist_TwoPatchesInRow_WithCaptureRunner(t *testing.T) {
	run := &fakeRunner{}
	cap := &handleSeqCaptureRunner{}
	m := newTestManager(run, &fakeVMM{}).WithCaptureRunner(cap)
	nc := netns.NewConfig("i-2pc", "fc-i-2pc", "vh-2pc", "vp-2pc", netip.MustParseAddr("10.100.0.21"))
	inst := &Instance{
		Lease:             Lease{Instance: "i-2pc", UID: 20021},
		Net:               nc,
		AppID:             "app-2pc",
		AllowlistHandleV4: 9, // wake-time capture
	}
	inst.Net.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24")}
	m.mu.Lock()
	m.live["i-2pc"] = inst
	m.mu.Unlock()

	// Patch 1.
	if err := m.UpdateEgressAllowlist(context.Background(), "app-2pc", []netip.Prefix{
		netip.MustParsePrefix("8.8.8.0/24"),
	}); err != nil {
		t.Fatalf("patch 1: %v", err)
	}
	// Patch 2.
	if err := m.UpdateEgressAllowlist(context.Background(), "app-2pc", []netip.Prefix{
		netip.MustParsePrefix("9.9.9.0/24"),
	}); err != nil {
		t.Fatalf("patch 2: %v", err)
	}
	// The captures must have produced non-zero handles.
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.live["i-2pc"].AllowlistHandleV4 == 0 {
		t.Errorf("after patch 2 with captureRunner wired, AllowlistHandleV4 should be non-zero")
	}
	// The first patch's path: delete-by-handle 9 + add.
	// The second patch's path: delete-by-handle <new-from-patch-1>
	// + add.
	wantDeletePatch1 := `delete rule ip faas forward handle 9`
	if !run.ran(wantDeletePatch1) {
		t.Errorf("patch 1 delete-by-handle 9 missing")
	}
	// The second patch's delete-by-handle must NOT be 9 (the
	// original handle) — it must be the handle captured after
	// patch 1.
	var sawDeletePatch2 bool
	run.mu.Lock()
	for _, c := range run.commands {
		join := strings.Join(c, " ")
		if strings.Contains(join, "delete rule ip faas forward handle") &&
			!strings.Contains(join, "handle 9 ") {
			sawDeletePatch2 = true
			t.Logf("patch 2 delete argv: %s", join)
		}
	}
	run.mu.Unlock()
	if !sawDeletePatch2 {
		t.Errorf("patch 2 must delete by the post-patch-1 handle, not by handle 9")
	}
}

// handleSeqCaptureRunner returns a sequence of distinct
// handles on each listChainHandles call. The first capture
// returns 100, the next 200, then 300, etc. The synth nft
// output uses the same `iifname "tap0" ip daddr { … } accept #
// handle N` shape the real kernel emits.
type handleSeqCaptureRunner struct {
	mu    sync.Mutex
	calls int
}

func (f *handleSeqCaptureRunner) RunCapture(_ context.Context, argv []string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	handle := f.calls * 100
	// Build the synthetic ruleset that matches what the renderer
	// just emitted. Pick the family from the argv.
	family := "ip"
	for _, a := range argv {
		if a == "ip6" {
			family = "ip6"
		}
	}
	// Use the cached prior baseline the test seeded.
	cidr := "8.8.8.0/24"
	if family == "ip6" {
		cidr = "2001:db8::/32"
	}
	return []byte(fmt.Sprintf(`table %s faas {
	chain forward {
	 type filter hook forward priority 0; policy accept;
	 iifname "tap0" %s daddr { %s } accept # handle %d
	}
}`, family, family, cidr, handle)), nil
}

// TestUpdateEgressAllowlist_V6FailureLeavesV4Untouched — the
// per-family revert path. A v4 + v6 allowlist patch where the
// v6 add step fails: the v4 patch should still have landed
// (its add rule is in the command stream), and the v6 revert
// should re-emit the prior v6 rule. The pre-fix code did the
// revert for both families, which would have undone the v4
// success.
func TestUpdateEgressAllowlist_V6FailureLeavesV4Untouched(t *testing.T) {
	// failOn matches the v6 add argv (the new v6 prefix
	// "fe80::/10"). The fakeRunner fails on the FIRST matching
	// command in command order; the patch sequence is v4 first
	// then v6, so the v4 add succeeds and the v6 add fails.
	run := &fakeRunner{failOn: "fe80::/10"}
	m := newTestManager(run, &fakeVMM{})
	nc := netns.NewConfig("i-pf", "fc-i-pf", "vh-pf", "vp-pf", netip.MustParseAddr("10.100.0.30"))
	inst := &Instance{
		Lease:             Lease{Instance: "i-pf", UID: 20030},
		Net:               nc,
		AppID:             "app-pf",
		AllowlistHandleV4: 11,
		AllowlistHandleV6: 22,
	}
	inst.Net.EgressAllowlist = []netip.Prefix{
		netip.MustParsePrefix("1.2.3.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	m.mu.Lock()
	m.live["i-pf"] = inst
	m.mu.Unlock()

	err := m.UpdateEgressAllowlist(context.Background(), "app-pf", []netip.Prefix{
		netip.MustParsePrefix("8.8.8.0/24"),
		netip.MustParsePrefix("fe80::/10"),
	})
	if err == nil {
		t.Fatal("expected error from v6 add failure")
	}
	// The v4 patch should have run: delete-by-handle 11 + add 8.8.8.0/24.
	if !run.ran("delete rule ip faas forward handle 11") {
		t.Error("v4 delete-by-handle 11 missing")
	}
	if !run.ran("8.8.8.0/24") {
		t.Error("v4 add 8.8.8.0/24 missing")
	}
	// The v6 revert should re-emit the prior v6 rule
	// (2001:db8::/32). The pre-fix code would have
	// re-emitted prior v4 too (1.2.3.0/24), which would have
	// undone the v4 success.
	if !run.ran("2001:db8::/32") {
		t.Error("v6 revert did not re-emit the prior v6 rule (2001:db8::/32)")
	}
	// The prior v4 should NOT have been re-emitted by the
	// revert (per-family revert preserves the v4 success).
	// Count v4 add invocations: 1 from the patch path (the
	// new 8.8.8.0/24 rule), 0 from the revert path.
	v4AddCount := 0
	run.mu.Lock()
	for _, c := range run.commands {
		join := strings.Join(c, " ")
		if strings.Contains(join, "ip daddr") && strings.Contains(join, "accept") {
			v4AddCount++
		}
	}
	run.mu.Unlock()
	if v4AddCount != 1 {
		t.Errorf("v4 add ran %d times; want 1 (no revert of v4 success); commands: %v",
			v4AddCount, run.commands)
	}
}

// PR scale-out readiness (4-PR #2) — Warn field tests.
//
// newTestManagerWithLog mirrors newTestManager but threads a
// caller-supplied *slog.Logger into NewManager so the test can
// capture the JSON-encoded Warn lines and assert on structured
// fields. newTestManager still passes nil log (NewManager falls back
// to a discard handler), so existing tests are unaffected.
func newTestManagerWithLog(run Runner, vmm VMM, log *slog.Logger) *Manager {
	return NewManager(run, vmm, Paths{Kernel: "/srv/fc/base/vmlinux-6.1"}, testFCVersion, log, nil)
}

// captureWarningLog returns a slog JSON handler bound to the supplied
// bytes.Buffer. Mirrors the inline slog-capture idiom used in
// pkg/gateway/synth_integration_test.go:38-41,
// pkg/middleware/middleware_test.go:314, pkg/mail/mail_test.go:27.
// LevelDebug is used so a future Debug-level addition doesn't cause
// a test to silently miscount records.
func captureWarningLog(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// findRecord returns the FIRST slog record whose "msg" field equals
// msg. Returns ok=false when the buffer contains no matching record.
// Anchors are bracket-exact to avoid accidental substring matches in
// field values (e.g. "restore failed" inside an error message).
func findRecord(t *testing.T, buf *bytes.Buffer, msg string) (map[string]any, bool) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	for {
		var rec map[string]any
		if err := dec.Decode(&rec); err != nil {
			return nil, false
		}
		if got, _ := rec["msg"].(string); got == msg {
			return rec, true
		}
	}
}

// TestManagerRestoreFallbackLogIncludesFCVersions (PR scale-out
// readiness, 4-PR #2) — the post-`PlanWake==WakeRestore` + failed
// `Restore` Warn at manager.go:709 carries the desired_fc_version
// (= m.fcVersion) and snapshot_fc_version (= req.Snapshot.FCVersion)
// fields. Both fields must equal testFCVersion because the
// usableSnapshot() helper matches the Manager version.
func TestManagerRestoreFallbackLogIncludesFCVersions(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{restoreErr: fmt.Errorf("snapshot corrupt")}
	var buf bytes.Buffer
	m := newTestManagerWithLog(run, vmm, captureWarningLog(&buf))

	inst, err := m.Wake(context.Background(), WakeRequest{
		Instance: "fb", BaseKey: "/b.ext4", LayerKey: "/l.ext4",
		VcpuCount: 2, MemSizeMiB: 128,
		Plan:     api.PlanHobby,
		Snapshot: usableSnapshot(),
	})
	if err != nil {
		t.Fatalf("Wake after restore-fail: %v", err)
	}
	if inst.Method != WakeColdBoot {
		t.Errorf("method = %v, want WakeColdBoot (fallback)", inst.Method)
	}

	rec, ok := findRecord(t, &buf, "restore failed, falling back to cold boot")
	if !ok {
		t.Fatalf("restore-fallback Warn not in log:\n%s", buf.String())
	}
	for _, field := range []string{"desired_fc_version", "snapshot_fc_version"} {
		got, _ := rec[field].(string)
		if got != testFCVersion {
			t.Errorf("%s = %q, want %q (Manager fcVersion / snapshot FCVersion)", field, got, testFCVersion)
		}
	}
	if got, _ := rec["instance"].(string); got != "fb" {
		t.Errorf("instance = %q, want fb", got)
	}
}

// TestManagerVersionMismatchSkipsFallbackLog (PR scale-out readiness,
// 4-PR #2) — when req.Snapshot has FCVersion != m.fcVersion,
// PlanWake() returns WakeColdBoot directly via Usable's
// snap.FCVersion != currentFCVersion check (snapshot.go:81-86 +
// snapshot.go:58); Restore is never called and the fallback Warn
// never fires. This pins the invariant the new fields rely on:
// the warn only reaches operators on a "restore failed after
// version matched", not on a version mismatch (which is silent and
// is the intended cold-boot behavior).
//
// Inline snapshot construction here only — no mismatchedFCSnapshot()
// helper exists because the helper would have to mutate the snapshot
// to force the Warn path, which would require weakening PlanWake
// (anti-goal in the plan).
func TestManagerVersionMismatchSkipsFallbackLog(t *testing.T) {
	run := &fakeRunner{}
	vmm := &fakeVMM{restoreErr: fmt.Errorf("snapshot corrupt")}
	var buf bytes.Buffer
	m := newTestManagerWithLog(run, vmm, captureWarningLog(&buf))

	// Inline mismatched snapshot. DeploymentID + paths are irrelevant
	// — PlanWake bails on the FC version check before any io
	// operation. usableSnapshot would set FCVersion = testFCVersion
	// which doesn't exercise the gate; deliberately diverge here.
	mismatchSnap := &Snapshot{
		DeploymentID: "d1",
		FCVersion:    testFCVersion + "-other",
		StorageKey:   "snap/d1/mem",
		VMStatePath:  "/snap/state",
	}

	inst, err := m.Wake(context.Background(), WakeRequest{
		Instance: "fb", BaseKey: "/b.ext4", LayerKey: "/l.ext4",
		VcpuCount: 2, MemSizeMiB: 128,
		Plan:     api.PlanHobby,
		Snapshot: mismatchSnap,
	})
	if err != nil {
		t.Fatalf("Wake on version-mismatch: %v", err)
	}
	if inst.Method != WakeColdBoot {
		t.Errorf("method = %v, want WakeColdBoot (PlanWake took the cold-boot path)", inst.Method)
	}
	if n := len(vmm.restored); n != 0 {
		t.Errorf("Restore invoked on version-mismatch snapshot: count=%d (PlanWake must skip)", n)
	}

	// The Warn must NOT fire. Scan the entire buffer; any
	// "restore failed, falling back to cold boot" record fails the
	// test.
	if _, found := findRecord(t, &buf, "restore failed, falling back to cold boot"); found {
		t.Errorf("restore-fallback Warn fired on version-mismatch path (must be PlanWake-only):\n%s", buf.String())
	}
}

// TestMarkInstanceFrameworkReady (issue #470 / PR #470-FU-B)
// exercises the vmmd-side receipt of the guest-init
// "framework ready" DGRAM. Stamps the per-instance
// framework_ready_at clock and observes the warmup histogram
// when wired.
func TestMarkInstanceFrameworkReady(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm).WithFrameworkReady(NewFrameworkReadyMetrics())

	// Cold-boot an instance to populate the live map. The
	// req() helper doesn't stamp AppID / Runtime by default;
	// the framework-ready receipt path reads both from the
	// live Instance, so we thread them in via a direct map
	// patch under the manager's mutex. The patch mirrors the
	// production wake path where the schedd's WakeRequest
	// sources both values from the apps row.
	inst, err := m.ColdBoot(context.Background(), req("i-fr"))
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	if inst.FrameworkReadyAt != nil {
		t.Errorf("FrameworkReadyAt pre-stamp = %v, want nil", inst.FrameworkReadyAt)
	}
	m.mu.Lock()
	m.live["i-fr"].AppID = "app-test-1"
	m.live["i-fr"].Runtime = "node22"
	m.mu.Unlock()

	// Stamp and verify the live Instance is updated.
	before := time.Now()
	stamped, appID, runtime, err := m.MarkInstanceFrameworkReady(context.Background(), "i-fr", 250)
	if err != nil {
		t.Fatalf("MarkInstanceFrameworkReady: %v", err)
	}
	if !stamped {
		t.Fatal("stamped = false, want true")
	}
	if appID == "" {
		t.Errorf("appID empty after stamp")
	}
	if runtime == "" {
		t.Errorf("runtime empty after stamp")
	}
	// Re-read the live Instance via the manager (ColdBoot
	// returns a snapshot copy; the live map is the source of
	// truth for the stamp).
	m.mu.Lock()
	got := m.live["i-fr"]
	m.mu.Unlock()
	if got.FrameworkReadyAt.Before(before) {
		t.Errorf("FrameworkReadyAt = %v, want >= %v", got.FrameworkReadyAt, before)
	}
}

// TestMarkInstanceFrameworkReady_UnknownInstance asserts the
// receipt of a DGRAM for an instance that has already been
// torn down (a stale receipt racing the wake-park cycle)
// returns (false, "", "", nil) — the gRPC handler translates
// that to a NotFound code rather than silently swallowing.
func TestMarkInstanceFrameworkReady_UnknownInstance(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	stamped, appID, runtime, err := m.MarkInstanceFrameworkReady(context.Background(), "i-does-not-exist", 100)
	if err != nil {
		t.Fatalf("MarkInstanceFrameworkReady: %v", err)
	}
	if stamped {
		t.Error("stamped = true, want false (unknown instance)")
	}
	if appID != "" || runtime != "" {
		t.Errorf("appID=%q runtime=%q, want both empty on unknown", appID, runtime)
	}
}

// TestMarkInstanceTailTerminal (issue #667 / ADR-078) pins the
// host-side decrement path that backs the type=0x04 tail_event
// DGRAM receipt. Mirrors TestMarkInstanceFrameworkReady's
// shape: cold-boot an instance, stamp TailCount via the
// live-map patch (the runner-side WaitGroup is the canonical
// owner of the counter; this test patches the in-memory mirror
// to simulate the runner's pre-decrement state), call
// MarkInstanceTailTerminal, observe the decrement in the live
// map.
//
// Mirrors the new field on state.Instance.TailCount (migration
// 00151). The TailTerminalStamper is wired to a fakeTailStamper
// that records every call so the test asserts the SQL seam is
// hit on the receipt path.
func TestMarkInstanceTailTerminal(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	stamper := &fakeTailStamper{}
	m := newTestManager(run, vmm).WithTailTerminalStamper(stamper)

	inst, err := m.ColdBoot(context.Background(), req("i-tail"))
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	// Seed: simulate the runner having 2 in-flight tail tasks.
	m.mu.Lock()
	m.live["i-tail"].AppID = "app-tail-test"
	m.live["i-tail"].TailCount = 2
	m.mu.Unlock()

	// Receipt: a tail task reached terminal (completed).
	stamped, appID, err := m.MarkInstanceTailTerminal(
		context.Background(), "i-tail", TailOutcomeCompleted, 1500)
	if err != nil {
		t.Fatalf("MarkInstanceTailTerminal: %v", err)
	}
	if !stamped {
		t.Fatal("stamped = false, want true")
	}
	if appID != "app-tail-test" {
		t.Errorf("appID = %q, want app-tail-test", appID)
	}

	// In-memory TailCount decremented.
	m.mu.Lock()
	got := m.live["i-tail"].TailCount
	m.mu.Unlock()
	if got != 1 {
		t.Errorf("TailCount = %d, want 1 (decrement from 2)", got)
	}

	// Stamper was hit once.
	if len(stamper.calls) != 1 {
		t.Errorf("stamper calls = %d, want 1", len(stamper.calls))
	}
	if len(stamper.calls) >= 1 && stamper.calls[0] != "i-tail" {
		t.Errorf("stamper calls[0] = %q, want i-tail", stamper.calls[0])
	}

	_ = inst // silence unused
}

// TestMarkInstanceTailTerminal_FloorsAtZero pins the in-memory
// floor (the SQL GREATEST(…, 0) guard has its own test in
// memstore_tail_count_test.go). A stray decrement on a
// counter at 0 must leave the in-memory mirror at 0; the
// snapshotAndPark 5s watchdog force-parks regardless (PR 4).
func TestMarkInstanceTailTerminal_FloorsAtZero(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	stamper := &fakeTailStamper{}
	m := newTestManager(run, vmm).WithTailTerminalStamper(stamper)

	if _, err := m.ColdBoot(context.Background(), req("i-tail-floor")); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	// Counter starts at 0 (cold-boot doesn't seed it). Three
	// stray receipts — each must leave the counter at 0.
	for i := 0; i < 3; i++ {
		stamped, _, err := m.MarkInstanceTailTerminal(
			context.Background(), "i-tail-floor", TailOutcomeTimeout, 5000)
		if err != nil {
			t.Fatalf("receipt %d: %v", i, err)
		}
		if !stamped {
			t.Errorf("receipt %d: stamped=false, want true", i)
		}
	}
	m.mu.Lock()
	got := m.live["i-tail-floor"].TailCount
	m.mu.Unlock()
	if got != 0 {
		t.Errorf("TailCount = %d, want 0 (floor)", got)
	}
}

// TestMarkInstanceTailTerminal_UnknownInstance asserts the
// receipt of a DGRAM for an instance that has already been
// torn down (a stale receipt racing the wake-park cycle)
// returns (false, "", nil) — the host's dispatchTailEvent
// translates that to a Debug log + drop, same convention as
// MarkInstanceFrameworkReady.
func TestMarkInstanceTailTerminal_UnknownInstance(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	stamper := &fakeTailStamper{}
	m := newTestManager(run, vmm).WithTailTerminalStamper(stamper)

	stamped, appID, err := m.MarkInstanceTailTerminal(
		context.Background(), "i-tail-missing", TailOutcomeCompleted, 100)
	if err != nil {
		t.Fatalf("MarkInstanceTailTerminal: %v", err)
	}
	if stamped {
		t.Error("stamped = true, want false (unknown instance)")
	}
	if appID != "" {
		t.Errorf("appID = %q, want empty on unknown", appID)
	}
	if len(stamper.calls) != 0 {
		t.Errorf("stamper calls = %d, want 0 (no live instance)", len(stamper.calls))
	}
}

// TestMarkInstanceTailTerminal_NilStamperDoesNotPanic asserts
// the nil-safe receipt path: a Manager constructed without
// WithTailTerminalStamper must not panic when a 0x04 DGRAM
// arrives. Mirrors the same nil-safe pattern as
// MarkInstanceFrameworkReady / FrameworkReadyMetrics.
func TestMarkInstanceTailTerminal_NilStamperDoesNotPanic(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm) // no WithTailTerminalStamper

	if _, err := m.ColdBoot(context.Background(), req("i-tail-nilstamp")); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	m.mu.Lock()
	m.live["i-tail-nilstamp"].TailCount = 1
	m.mu.Unlock()

	stamped, _, err := m.MarkInstanceTailTerminal(
		context.Background(), "i-tail-nilstamp", TailOutcomeFailed, 200)
	if err != nil {
		t.Fatalf("MarkInstanceTailTerminal: %v", err)
	}
	if !stamped {
		t.Error("stamped = false, want true (in-memory mirror still decrements)")
	}
	m.mu.Lock()
	got := m.live["i-tail-nilstamp"].TailCount
	m.mu.Unlock()
	if got != 0 {
		t.Errorf("TailCount = %d, want 0 (in-memory mirror decrement)", got)
	}
}

// fakeTailStamper is the TailTerminalStamper test double. It
// records every DecrementInstanceTailCount call so the test
// can assert the SQL seam was hit (or not) on the receipt
// path. Mirrors the recording pattern in framework_ready's
// test helpers.
type fakeTailStamper struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeTailStamper) DecrementInstanceTailCount(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, id)
	return nil
}

// TestInstanceByCID (issue #470 / PR #470-FU-B) is the
// reverse lookup the host's DGRAM recv loop uses to map
// a peer AF_VSOCK CID back to the live Instance id.
func TestInstanceByCID(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	inst, err := m.ColdBoot(context.Background(), req("i-cid"))
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	cid := GuestVsockCID(inst.Lease.Slot)
	got, err := m.InstanceByCID(cid)
	if err != nil {
		t.Fatalf("InstanceByCID: %v", err)
	}
	if got != "i-cid" {
		t.Errorf("InstanceByCID(%d) = %q, want %q", cid, got, "i-cid")
	}
}

// TestInstanceByCID_UnknownCID asserts a stale CID (one
// that no live instance owns) returns the documented error.
func TestInstanceByCID_UnknownCID(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	if _, err := m.InstanceByCID(0xDEADBEEF); err == nil {
		t.Error("InstanceByCID(unknown) = nil, want error")
	}
}

// ADR-119: per-app static egress IP path. The full Wake side is
// covered indirectly by the renderer tests in pkg/netns (the
// renderer emits the SNAT rule when AccountStaticIP != nil); the
// tests below pin the vmmd-side Manager surface so a future
// regression that drops the validation or breaks the live-patch
// surfaces here without needing a metal box.

// TestWakeRejectsStaticEgressIP_NotV4 pins the IPv6-deferred
// behaviour. apid gates on family=4 upstream; vmmd defends in
// depth so a future schema relaxation can't sneak a v6 string
// into the Wake path. The parse gate trips BEFORE the vmm
// path so fakeVMM doesn't matter — the request errors at the
// static-egress-IP block in Wake.
func TestWakeRejectsStaticEgressIP_NotV4(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	_, err := m.Wake(context.Background(), WakeRequest{
		Instance:       "vw-static-v6",
		BaseKey:        "/b.ext4",
		LayerKey:       "/l.ext4",
		VcpuCount:      2,
		MemSizeMiB:     128,
		Plan:           api.PlanScale,
		StaticEgressIP: "::1",
	})
	if err == nil {
		t.Fatal("Wake with IPv6 static IP: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "IPv6 deferred") {
		t.Errorf("err = %v, want substring `IPv6 deferred`", err)
	}
}

// TestWakeRejectsStaticEgressIP_Reserved pins the deny-set gate.
// The same deny set the apid handler enforces (RFC1918, CGN,
// link-local, multicast, loopback) is mirrored here so a Wake
// from a non-apid caller (eg. a future bulk-import path) can't
// pin a reserved IP.
func TestWakeRejectsStaticEgressIP_Reserved(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	_, err := m.Wake(context.Background(), WakeRequest{
		Instance:       "vw-static-rfc1918",
		BaseKey:        "/b.ext4",
		LayerKey:       "/l.ext4",
		VcpuCount:      2,
		MemSizeMiB:     128,
		Plan:           api.PlanScale,
		StaticEgressIP: "10.1.2.3",
	})
	if err == nil {
		t.Fatal("Wake with RFC1918 static IP: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "in deny set") {
		t.Errorf("err = %v, want substring `in deny set` (canonical ValidateStaticEgressIP)", err)
	}
}

// TestWakeRejectsStaticEgressIP_Malformed pins the parse gate.
func TestWakeRejectsStaticEgressIP_Malformed(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	_, err := m.Wake(context.Background(), WakeRequest{
		Instance:       "vw-static-malformed",
		BaseKey:        "/b.ext4",
		LayerKey:       "/l.ext4",
		VcpuCount:      2,
		MemSizeMiB:     128,
		Plan:           api.PlanScale,
		StaticEgressIP: "not-an-ip",
	})
	if err == nil {
		t.Fatal("Wake with malformed static IP: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid dotted-quad") {
		t.Errorf("err = %v, want substring `invalid dotted-quad`", err)
	}
}

// TestUpdateStaticEgressIP_NoLiveInstancesIsNoop — the empty
// app is the redelivery / no-live-targets path. No nft commands
// should fire, no error. The per-app cache is still written so
// a future Wake for this app picks up the new IP.
func TestUpdateStaticEgressIP_NoLiveInstancesIsNoop(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	if err := m.UpdateStaticEgressIP(context.Background(), "acct-orphan", "app-orphan", "203.0.113.42"); err != nil {
		t.Fatalf("UpdateStaticEgressIP: %v", err)
	}
	if run.ran("nft") {
		t.Error("nft should not run when no live instances match the app")
	}
	m.perAppStaticIPMu.RLock()
	defer m.perAppStaticIPMu.RUnlock()
	if got := m.perAppStaticIP["app-orphan"]; got == nil || got.String() != "203.0.113.42" {
		t.Errorf("perAppStaticIP[app-orphan] = %v, want 203.0.113.42", got)
	}
}

// ADR-119 redesign: the per-netns SNAT path was deleted.
// The per-VM live-patch tests (AppliesPatch, SameIPNoOp,
// ReplacesIP, ClearPath, FansOutAcrossLiveInstances) all
// exercised `inst.Net.AccountStaticIP` and `inst.StaticIPHandle`,
// which are deleted fields. The new model is the host renderer
// (pkg/netns/policy.go::StaticEgressRules) with the per-VM host
// IP allocated by pkg/fcvm.AcquireStaticEgressIP. New tests
// below cover the redesign: rebuildHostStaticEgressRules
// populates the host policy, the per-app cache invariants hold,
// and the redelivery fast-path is byte-identical to the
// previous tests' intent.

// TestUpdateStaticEgressIP_RejectsEmptyAppID — defensive. Empty
// appID is a programmer error, not a customer-facing failure.
func TestUpdateStaticEgressIP_RejectsEmptyAppID(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	err := m.UpdateStaticEgressIP(context.Background(), "acct-test", "", "203.0.113.42")
	if err == nil {
		t.Fatal("expected error for empty app_id")
	}
	if !strings.Contains(err.Error(), "empty app_id") {
		t.Errorf("err = %v, want substring `empty app_id`", err)
	}
}

// TestUpdateStaticEgressIP_RejectsReservedIP — same deny set as
// Wake. A misconfigured upstream caller (eg. a test fixture or
// a future bulk-import path) cannot pin a reserved IP.
func TestUpdateStaticEgressIP_RejectsReservedIP(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	err := m.UpdateStaticEgressIP(context.Background(), "acct-1", "app-1", "192.168.1.1")
	if err == nil {
		t.Fatal("expected error for reserved IP")
	}
	if !strings.Contains(err.Error(), "in deny set") {
		t.Errorf("err = %v, want substring `in deny set` (canonical ValidateStaticEgressIP)", err)
	}
}

// TestUpdateStaticEgressIP_BuildsHostPolicy (ADR-119 redesign)
// is the new regression net for the host-renderer rebuild path.
// Seeding a live instance with the per-VM host IP and the
// per-app customer IP, then calling UpdateStaticEgressIP, must
// surface the (perVMHostIP, customerIP) tuple to the host
// renderer (via netns.ActiveHostPolicy.StaticEgressRules).
func TestUpdateStaticEgressIP_BuildsHostPolicy(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	renderer := &fakeHostRenderer{}
	m.SetHostRenderer(renderer)
	perVMHostIP := netip.MustParseAddr("10.200.0.1")
	customerIP := netip.MustParseAddr("203.0.113.42")
	nc := netns.NewConfig("i-1", "fc-i-1", "vh1", "vp1", netip.MustParseAddr("10.100.0.2"))
	inst := &Instance{
		Lease:     Lease{Instance: "i-1", UID: 20001},
		Net:       nc,
		Method:    WakeColdBoot,
		AppID:     "app-1",
		AccountID: "acct-1",
	}
	m.mu.Lock()
	m.live["i-1"] = inst
	m.mu.Unlock()
	// Register the per-VM host IP (the Wake path does this on
	// a successful AcquireStaticEgressIP).
	m.RegisterStaticEgressIPForVM("app-1", perVMHostIP)

	if err := m.UpdateStaticEgressIP(context.Background(), "acct-1", "app-1", "203.0.113.42"); err != nil {
		t.Fatalf("UpdateStaticEgressIP: %v", err)
	}
	// The host renderer is rebuilt via the atomic-swap path
	// (netns.SwapActiveHostPolicy) — the renderer stub's
	// Render() is the call-site. The active host policy's
	// StaticEgressRules is the canonical evidence the
	// rebuild ran.
	pol := netns.ActiveHostPolicyForRender()
	if pol == nil {
		t.Fatal("ActiveHostPolicyForRender returned nil")
	}
	if len(pol.StaticEgressRules) != 1 {
		t.Fatalf("StaticEgressRules len = %d, want 1", len(pol.StaticEgressRules))
	}
	r := pol.StaticEgressRules[0]
	if r.PerVMHostIP != perVMHostIP {
		t.Errorf("PerVMHostIP = %s, want %s", r.PerVMHostIP, perVMHostIP)
	}
	if r.CustomerIP != customerIP {
		t.Errorf("CustomerIP = %s, want %s", r.CustomerIP, customerIP)
	}
	if r.AppID != "app-1" {
		t.Errorf("AppID = %q, want app-1", r.AppID)
	}
	if r.AccountID != "acct-1" {
		t.Errorf("AccountID = %q, want acct-1", r.AccountID)
	}
	renderer.mu.Lock()
	if renderer.renderCalls != 1 {
		renderer.mu.Unlock()
		t.Errorf("fakeHostRenderer.renderCalls = %d, want 1", renderer.renderCalls)
		return
	}
	renderer.mu.Unlock()
	// No per-netns nft exec was emitted.
	if run.ran("nft") {
		t.Errorf("renderer should not run nft directly; commands: %v", run.commands)
	}
}

// TestUpdateStaticEgressIP_ClearPathRemovesHostRule is the
// clear-path analogue. Setting the IP then clearing must drop
// the tuple from the host renderer's StaticEgressRules list.
func TestUpdateStaticEgressIP_ClearPathRemovesHostRule(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	renderer := &fakeHostRenderer{}
	m.SetHostRenderer(renderer)
	perVMHostIP := netip.MustParseAddr("10.200.0.1")
	nc := netns.NewConfig("i-1", "fc-i-1", "vh1", "vp1", netip.MustParseAddr("10.100.0.2"))
	inst := &Instance{
		Lease:     Lease{Instance: "i-1", UID: 20001},
		Net:       nc,
		Method:    WakeColdBoot,
		AppID:     "app-1",
		AccountID: "acct-1",
	}
	m.mu.Lock()
	m.live["i-1"] = inst
	m.mu.Unlock()
	m.RegisterStaticEgressIPForVM("app-1", perVMHostIP)
	if err := m.UpdateStaticEgressIP(context.Background(), "acct-1", "app-1", "203.0.113.42"); err != nil {
		t.Fatalf("UpdateStaticEgressIP set: %v", err)
	}
	if err := m.UpdateStaticEgressIP(context.Background(), "acct-1", "app-1", ""); err != nil {
		t.Fatalf("UpdateStaticEgressIP clear: %v", err)
	}
	pol := netns.ActiveHostPolicyForRender()
	if pol == nil {
		t.Fatal("ActiveHostPolicyForRender returned nil")
	}
	if len(pol.StaticEgressRules) != 0 {
		t.Errorf("StaticEgressRules len = %d after clear, want 0", len(pol.StaticEgressRules))
	}
}

// TestUpdateStaticEgressIP_SameIPNoOp covers the redelivery
// path. The same IP twice should not rebuild the host renderer
// (the second call's idempotent fast-path returns early).
func TestUpdateStaticEgressIP_SameIPNoOp(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	renderer := &fakeHostRenderer{}
	m.SetHostRenderer(renderer)
	perVMHostIP := netip.MustParseAddr("10.200.0.1")
	nc := netns.NewConfig("i-1", "fc-i-1", "vh1", "vp1", netip.MustParseAddr("10.100.0.2"))
	inst := &Instance{
		Lease:  Lease{Instance: "i-1", UID: 20001},
		Net:    nc,
		Method: WakeColdBoot,
		AppID:  "app-1",
	}
	m.mu.Lock()
	m.live["i-1"] = inst
	m.mu.Unlock()
	m.RegisterStaticEgressIPForVM("app-1", perVMHostIP)
	// First call: set the IP, rebuild the host policy.
	if err := m.UpdateStaticEgressIP(context.Background(), "acct-1", "app-1", "203.0.113.42"); err != nil {
		t.Fatalf("first UpdateStaticEgressIP: %v", err)
	}
	// Second call: same IP, no rebuild expected.
	if err := m.UpdateStaticEgressIP(context.Background(), "acct-1", "app-1", "203.0.113.42"); err != nil {
		t.Fatalf("second UpdateStaticEgressIP: %v", err)
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	if renderer.renderCalls != 1 {
		t.Errorf("fakeHostRenderer.renderCalls = %d, want 1 (redelivery must be a no-op)", renderer.renderCalls)
	}
}

// TestManager_ReportWorkloadOOM_CallsRelay (Cluster C / ADR-121)
// asserts the Manager.ReportWorkloadOOM path invokes the
// WorkloadOOMSink with the observed (peakMB, planMB) payload
// captured at the guest-init cgroup.events listener. The
// cmd/vmmd wires the relay via WithWorkloadOOMSink; the framework
// _ready recv dispatcher calls ReportWorkloadOOM when a DGRAM
// type 0x05 arrives on port 1027.
//
// The test is intent-on: a stub relay captures the call and the
// assertions inspect the captured values without booting a guest
// VM. The Manager's live map is not consulted (the relay is
// forwarded to schedd before the destroy path runs).
func TestManager_ReportWorkloadOOM_CallsRelay(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)

	var (
		mu          sync.Mutex
		called      bool
		gotInstance string
		gotPeak     int
		gotPlan     int
	)
	m.WithWorkloadOOMSink(func(_ context.Context, instanceID string, peakMB, planMB int) {
		mu.Lock()
		defer mu.Unlock()
		called = true
		gotInstance = instanceID
		gotPeak = peakMB
		gotPlan = planMB
	})

	m.ReportWorkloadOOM(context.Background(), "inst-1", 384, 256)

	mu.Lock()
	defer mu.Unlock()
	if !called {
		t.Fatal("WorkloadOOMSink was not called")
	}
	if gotInstance != "inst-1" {
		t.Errorf("instanceID = %q, want %q", gotInstance, "inst-1")
	}
	if gotPeak != 384 {
		t.Errorf("peakMB = %d, want 384", gotPeak)
	}
	if gotPlan != 256 {
		t.Errorf("planMB = %d, want 256", gotPlan)
	}
}

// TestManager_ReportWorkloadOOM_NilRelayNoOp (Cluster C / ADR-121)
// asserts that ReportWorkloadOOM is a no-op when no relay is wired
// (a unit test that doesn't construct a relay). The Manager must
// guard on nil so the missing-wire path doesn't panic — this is
// the project convention (LivenessFailedSink nil-safe, see
// cmd/vmmd/liveness_recv_test for the parallel).
func TestManager_ReportWorkloadOOM_NilRelayNoOp(t *testing.T) {
	run, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(run, vmm)
	// Deliberately do NOT call WithWorkloadOOMSink.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("ReportWorkloadOOM with nil relay panicked: %v", r)
		}
	}()
	m.ReportWorkloadOOM(context.Background(), "inst-1", 384, 256)

}
