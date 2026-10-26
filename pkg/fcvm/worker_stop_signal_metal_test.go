//go:build linux && metal

package fcvm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
)

// workerDrainDelay is how long the fixture worker spends draining after its
// stop signal before it exits cleanly.
const workerDrainDelay = 2 * time.Second

// TestMetalWorkerStopSignalReachesTheWorkload is the Hunt 14 H14-59 metal
// gate. A worker's stop used to signal the host Firecracker process, which
// exits at once: production audited worker_drain_completed 142 ms after
// worker_drain_started and the worker never logged its signal. Now the
// signal travels over the guest's job-control vsock to guest-init, which
// forwards it to the workload. The fixture worker traps SIGTERM, drains for
// workerDrainDelay, then exits 0, so a stop that takes at least that long,
// ends within the grace period and needs no SIGKILL proves the workload saw
// its signal.
func TestMetalWorkerStopSignalReachesTheWorkload(t *testing.T) {
	kernel, _, _ := metalImages(t)
	guestInit := os.Getenv("FAAS_TEST_GUEST_INIT_BIN")
	if guestInit == "" {
		t.Skip("set FAAS_TEST_GUEST_INIT_BIN to a guest-init built from this tree")
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	dir := t.TempDir()
	base := filepath.Join(dir, "worker-base.ext4")
	if err := buildStopSignalWorkerBase(base, guestInit); err != nil {
		t.Fatalf("build worker base: %v", err)
	}
	layer := filepath.Join(dir, "worker-layer.ext4")
	if err := buildV6LayerExt4(layer); err != nil {
		t.Fatalf("build worker layer: %v", err)
	}

	m := newMetalManagerWithReadyTimeout(t, kernel, 90*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	instance := uuid.NewString()
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, Plan: "hobby", BaseKey: base, LayerKey: layer,
		VcpuCount: 1, MemSizeMiB: 128, ExecutionMode: api.ExecutionModeWorker,
	}); err != nil {
		t.Fatalf("worker cold boot: %v", err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		_ = m.Destroy(cleanup, instance)
		leakcheck.AssertZero(t)
	})
	// Let the workload install its trap before it is asked to stop.
	time.Sleep(time.Second)

	const grace = 10 * time.Second
	started := time.Now()
	killed, exitCode, err := m.SignalAndKill(ctx, instance, syscall.SIGTERM, grace)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("stop worker: %v", err)
	}
	t.Logf("worker stop: elapsed=%s killSignalSent=%v exitCode=%d", elapsed, killed, exitCode)
	if killed {
		t.Fatalf("stop escalated to SIGKILL after %s; the workload never finished its drain", elapsed)
	}
	if elapsed < workerDrainDelay-200*time.Millisecond {
		t.Fatalf("worker stopped after %s, before its %s drain; the signal reached the VMM, not the workload", elapsed, workerDrainDelay)
	}
	if elapsed >= grace {
		t.Fatalf("worker stop took %s, the whole %s grace period", elapsed, grace)
	}
	if m.LiveCount() != 0 {
		t.Fatalf("LiveCount = %d after the stop, want 0", m.LiveCount())
	}
}

// buildStopSignalWorkerBase builds drive0 for a guest-init worker whose
// workload drains for workerDrainDelay on SIGTERM and then exits 0.
func buildStopSignalWorkerBase(dst, guestInit string) error {
	bb, err := exec.LookPath("busybox")
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "worker-stop-skel-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	for _, sub := range []string{"bin", "sbin", "dev", "sys", "proc", "etc/faas", "usr/local/bin", "tmp", "var/tmp", "overlay"} {
		if err := os.MkdirAll(filepath.Join(work, sub), 0o755); err != nil {
			return err
		}
	}
	if err := bbCopyFile(guestInit, filepath.Join(work, "sbin", "init")); err != nil {
		return err
	}
	if err := bbCopyFile(bb, filepath.Join(work, "bin", "busybox")); err != nil {
		return err
	}
	for _, name := range []string{"bin/sh", "bin/ash", "bin/sleep"} {
		if err := os.Symlink("/bin/busybox", filepath.Join(work, name)); err != nil {
			return err
		}
	}
	worker := fmt.Sprintf("#!/bin/sh\ntrap 'sleep %d; exit 0' TERM\nwhile :; do sleep 1; done\n", int(workerDrainDelay/time.Second))
	if err := os.WriteFile(filepath.Join(work, "usr", "local", "bin", "worker"), []byte(worker), 0o755); err != nil {
		return err
	}
	appJSON := `{"entrypoint":["/usr/local/bin/worker"],"execution_mode":"worker","stop_signal":"SIGTERM"}` + "\n"
	if err := os.WriteFile(filepath.Join(work, "etc", "faas", "app.json"), []byte(appJSON), 0o644); err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if err := f.Truncate(128 << 20); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	mkfs := exec.Command("mkfs.ext4", "-q", "-O", "^has_journal", "-d", work, "-L", "faas-worker-stop", "-F", dst)
	mkfs.Stderr = os.Stderr
	if err := mkfs.Run(); err != nil {
		return err
	}
	return os.Chmod(dst, 0o644)
}
