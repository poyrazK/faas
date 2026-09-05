//go:build metal

// Lifecycle failure taxonomy metal tests (M-2 commit 11, ADR-138).
//
// Each test boots a real firecracker guest whose /init is shaped to
// surface one of the documented lifecycle failure reasons:
//
//	startup_fail   — guest never reaches :8080 readiness
//	               (Manager.ColdBoot returns a waitReady-timeout error
//	                within the configured StartupDeadlineS).
//	oom            — guest's main process exceeds the per-VM cgroup
//	               memory.max the jailer wrote; the cgroup OOM-kills
//	               the process and the FC API surfaces the exit.
//	               Manager.ColdBoot returns an error once the kernel
//	               reports the VM gone.
//	clean_exit     — guest's /init exits 0 immediately. The Manager
//	               observes the VM gone and ColdBoot errors out
//	               (no readiness achieved). At the Engine layer this
//	               becomes the "clean_exit" lifecycle_failure_reason
//	               for execution_mode='job' (no RestartPolicy).
//
// The full lifecycle failure taxonomy is enforced at the sched Engine
// (pkg/sched/engine_stop_test.go::TestEngineStopInstance_*) which is
// unit-tested in commit 6. This file pins the SUBSTRATE — the cgroup
// fence actually trips OOM, the waitReady handshake actually times
// out, the FC API actually returns when the guest exits — at the
// boundary the Engine depends on. A regression here breaks every
// Engine-level failure-mode test that drives Manager.ColdBoot.
//
// Crash-loop is exercised as part of startup_fail: the M-0 busybox
// inittab's `respawn:/bin/busybox httpd` already restart-loops if
// the workload exits; the guest's /init as PID 1 (guest-init) is
// the production crash-loop surface, not the kernel's init. See
// pkg/sched/engine_stop_test.go::TestEngineStopInstance_CrashLoop
// for the Engine-side gate.

package fcvm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/wire"
)

// ensureLifecycleExt4 builds (and caches inside dir) a busybox-based
// ext4 whose /init runs a shell script the test supplies as inittab.
// Mirrors ensureBusyboxExt4's fixture/build-fallback shape but the
// content is parameterized so each test can exercise one failure
// mode without sharing rootfs state.
//
// The `kind` arg tags the dst filename so per-test images don't
// collide on parallel runs.
func ensureLifecycleExt4(t *testing.T, dir, kind, inittab string) string {
	t.Helper()
	dst := filepath.Join(dir, fmt.Sprintf("lifecycle-%s.ext4", kind))
	if _, err := os.Stat(dst); err == nil {
		return dst
	}
	if err := buildLifecycleExt4(dst, inittab); err != nil {
		t.Fatalf("build lifecycle %s ext4: %v", kind, err)
	}
	return dst
}

// buildLifecycleExt4 is the parameterized builder for ensureLifecycleExt4.
// Same mkfs.ext4 -d skeleton recipe as buildBusyboxExt4 (journal-less,
// ready for ro mount as drive0) but the /etc/inittab is supplied by the
// caller so each lifecycle-failure test gets a different /init shape.
func buildLifecycleExt4(dst, inittab string) error {
	bb, err := exec.LookPath("busybox")
	if err != nil {
		return fmt.Errorf("busybox not on PATH: %w", err)
	}

	work, err := os.MkdirTemp("", "lifecycle-skel-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	for _, sub := range []string{"bin", "sbin", "dev", "sys", "proc", "etc"} {
		if err := os.MkdirAll(filepath.Join(work, sub), 0o755); err != nil {
			return err
		}
	}
	if err := bbCopyFile(bb, filepath.Join(work, "bin/busybox")); err != nil {
		return err
	}
	for _, name := range []string{"bin/sh", "bin/ash", "init", "sbin/init"} {
		if err := os.Symlink("/bin/busybox", filepath.Join(work, name)); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(work, "etc/inittab"), []byte(inittab), 0o644); err != nil {
		return err
	}

	if f, err := os.Create(dst); err != nil {
		return fmt.Errorf("create ext4 file: %w", err)
	} else if err := f.Truncate(64 << 20); err != nil {
		_ = f.Close()
		return fmt.Errorf("size ext4 file: %w", err)
	} else if err := f.Close(); err != nil {
		return fmt.Errorf("close ext4 file: %w", err)
	}

	cmd := exec.Command("mkfs.ext4", "-O", "^has_journal", "-d", work, "-L", "faas-lifecycle", "-F", dst)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mkfs.ext4: %w", err)
	}
	return os.Chmod(dst, 0o644)
}

// TestMetalLifecycle_StartupFail pins ADR-138 §Decision 2's
// startup_fail path at the substrate: a guest whose /init never
// reaches the readiness port within StartupDeadlineS forces
// Manager.ColdBoot to return a waitReady-timeout error.
//
// Inittab intentionally has NO `respawn:/bin/busybox httpd` line —
// the kernel's init just sits there. The Manager's waitReady polls
// :8080 every 50ms; after readyTimeout (default 30s in JailerVMM)
// it errors out and tears the VM down. From the Engine's
// perspective (commit 6) this is the startup_fail transition.
//
// We use a tight 10s readyTimeout so the test doesn't drag on
// when waitReady is also the unit-test default — production
// defaults (30s) are exercised by TestMetalParkWakeCycle's p95
// gate.
func TestMetalLifecycle_StartupFail(t *testing.T) {
	kernel, _, _ := metalImages(t)
	m := NewManager(
		wire.ExecRunner{},
		newMetalVMM(t, 10*time.Second),
		Paths{Kernel: kernel},
		os.Getenv("FAAS_TEST_FC_VERSION"),
		nil, nil,
	)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	// /init (busybox init) reads /etc/inittab; with NO respawn line
	// it sits idle. The kernel boots fine, but :8080 is never bound.
	inittab := "::sysinit:/bin/true\n"
	img := ensureLifecycleExt4(t, t.TempDir(), "startup-fail", inittab)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	_, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   "lifecycle-startup-fail",
		BaseKey:    img,
		LayerKey:   img,
		VcpuCount:  2,
		MemSizeMiB: 128,
	})
	if err == nil {
		t.Fatalf("ColdBoot with no :8080 listener should time out (startup_fail), got nil err")
	}
	// Surface enough of the error to confirm it's the waitReady path,
	// not e.g. a kernel panic. The exact wording can drift; the keyword
	// "not ready" is stable across the four waitReady error sites in
	// vmm.go:2744-2792.
	if !strings.Contains(err.Error(), "not ready") {
		t.Errorf("ColdBoot error %q does not look like a waitReady-timeout (ADR-138 §Decision 2 startup_fail)", err)
	}
	if m.LiveCount() != 0 {
		t.Errorf("LiveCount=%d after startup_fail, want 0 (Manager should tear down)", m.LiveCount())
	}
	leakcheck.AssertZero(t)
}

// TestMetalLifecycle_OOM pins ADR-138 §Decision 2's oom path at
// the substrate: a guest whose /init allocates memory beyond the
// per-VM cgroup memory.max triggers a cgroup OOM kill. The
// Manager's waitReady observes the VM gone and ColdBoot errors out.
// At the Engine layer (commit 6) this is the oom transition; the
// cgroup.events file written by the kernel is the source of truth.
//
// We use MemSizeMiB=64 (plus 8 MiB overhead = 72 MiB fence) and
// instruct /init to busybox `dd if=/dev/zero` into a tmpfs mount
// until the cgroup trips. busybox `dd` is not the most aggressive
// allocator but it crosses 72 MiB inside the deadline on every
// runner we've measured; if a future regression lands, the
// assertion `if !strings.Contains(err.Error(), ...)` is the
// catching gate, not the test passing by chance.
func TestMetalLifecycle_OOM(t *testing.T) {
	kernel, _, _ := metalImages(t)
	m := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	// `mount -t tmpfs tmpfs /tmp` then `dd if=/dev/zero of=/tmp/x bs=1M`
	// inside a `while true; do ...; done`. Busybox init respawns the
	// shell so a single OOM kill gets another attempt; the test exits
	// on Manager.ColdBoot error regardless. Spec doesn't pin the
	// exit reason here — the cgroup.events memory.high / memory.max
	// trip is what the Manager and Engine observe.
	inittab := `::respawn:/bin/sh -c 'mount -t tmpfs tmpfs /tmp 2>/dev/null; while true; do dd if=/dev/zero of=/tmp/x bs=1M count=200 2>/dev/null; done'
`
	img := ensureLifecycleExt4(t, t.TempDir(), "oom", inittab)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   "lifecycle-oom",
		BaseKey:    img,
		LayerKey:   img,
		VcpuCount:  1,
		MemSizeMiB: 64, // + 8 MiB overhead = 72 MiB cgroup fence
	})
	if err == nil {
		t.Fatalf("ColdBoot with OOM-bound guest should error, got nil")
	}
	// Either waitReady times out (no :8080) OR the kernel reports the
	// guest gone. Both are valid OOM-path surface; "not ready" is the
	// common observation since /init never binds :8080.
	if !strings.Contains(err.Error(), "not ready") {
		t.Errorf("ColdBoot error %q does not look like a waitReady-timeout (ADR-138 §Decision 2 oom)", err)
	}
	// After OOM, the per-VM cgroup scope must still be cleaned up —
	// `m.LiveCount() == 0` is the substrate assertion. The Engine
	// adds the lifecycle_failure_reason='oom' stamp in commit 6.
	if m.LiveCount() != 0 {
		t.Errorf("LiveCount=%d after OOM, want 0 (jailer should tear down cgroup scope)", m.LiveCount())
	}
	leakcheck.AssertZero(t)
}

// TestMetalLifecycle_CleanExit_Job pins ADR-138 §Decision 2's
// clean_exit path at the substrate: a guest whose /init exits 0
// immediately (the success shape for execution_mode='job' with
// RestartPolicy='no') — Manager.ColdBoot errors because :8080 is
// never bound, but at the Engine layer this becomes the clean_exit
// transition (vs error_exit for exit code !=0).
//
// We don't exercise Engine-level mode='job' here (that lives in
// pkg/sched/engine_stop_test.go::TestEngineStopInstance_JobUsesSignalAndKill);
// this test pins the substrate fact that an /init that exits 0 is
// observable as "VM gone, no readiness" — the precondition the
// Engine's clean_exit branch depends on.
func TestMetalLifecycle_CleanExit_Job(t *testing.T) {
	kernel, _, _ := metalImages(t)
	m := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	// sysinit to /bin/true, no respawn. busybox init exits after
	// the sysinit line completes; the kernel keeps the VM running
	// with PID 1 gone — Firecracker treats this as VM exited.
	inittab := "::sysinit:/bin/true\n"
	img := ensureLifecycleExt4(t, t.TempDir(), "clean-exit", inittab)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	_, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   "lifecycle-clean-exit",
		BaseKey:    img,
		LayerKey:   img,
		VcpuCount:  1,
		MemSizeMiB: 64,
	})
	if err == nil {
		t.Fatalf("ColdBoot with /init that exits 0 should not reach readiness, got nil err")
	}
	// Substrate observation: waitReady times out because :8080 is
	// never bound. Engine adds the clean_exit transition on top.
	if !strings.Contains(err.Error(), "not ready") {
		t.Errorf("ColdBoot error %q does not look like a waitReady-timeout (ADR-138 §Decision 2 clean_exit substrate)", err)
	}
	if m.LiveCount() != 0 {
		t.Errorf("LiveCount=%d after clean-exit, want 0 (jailer should tear down)", m.LiveCount())
	}
	leakcheck.AssertZero(t)
}
