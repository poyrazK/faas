//go:build metal

// Worker-idle-exemption metal test (M-2 commit 11, ADR-137 §Decision 1).
//
// The reaper exemption for execution_mode='worker' lives at the sched
// Engine layer (pkg/sched/reaper.go widened in commit 6 to honour
// ExecutionMode='worker' alongside the existing WorkloadClass=worker
// branch). What this file pins is the SUBSTRATE fact that the
// fcvm Manager itself has no idle-reap path — a "worker" guest is
// just a VM with no readiness port bound, and the Manager has no
// view of "this VM hasn't served a request in N minutes". The
// Engine's reaper runs OUTSIDE the Manager (it reads instances
// rows from state and issues Destroy calls); the Manager's only
// job is to keep the VM alive until Destroy is called.
//
// If this test ever fails (the VM gets torn down without an explicit
// Destroy), the Manager has regressed into an idle-reap mode it
// must not own — the Engine's reaper is the single source of truth
// for that decision.
//
// To avoid consuming a real 10-minute wall-clock window per run, we
// shorten the assertion window to 90s. The Engine's default
// IdleTimeoutS for plan 'request' is 30s; if a regression lands and
// the Manager somehow inherited a 30s reap, a 90s window catches
// it. The full 10-min window is the production shape, exercised by
// pkg/sched/reaper_worker_exempt_test.go at the Engine layer.

package fcvm

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
)

// TestMetalWorker_NotReapedAfterIdleWindow pins ADR-137 §Decision 1's
// worker-idle-exemption at the substrate: a Manager.ColdBoot that
// produces a guest with no :8080 listener (the canonical "worker"
// shape — long-lived, no public port, no request-driven lifecycle)
// must NOT be torn down by the Manager over the idle window. The
// Engine's reaper is the only path that issues Destroy for a worker;
// if the Manager grows a private reap, this test catches it.
func TestMetalWorker_NotReapedAfterIdleWindow(t *testing.T) {
	kernel, _, _ := metalImages(t)
	m := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	// A worker doesn't bind :8080 — the canonical shape is a
	// process that consumes CPU/RAM but doesn't serve HTTP. We
	// reuse the startup-fail inittab (busybox init with no
	// respawn line) — but to keep the guest ALIVE without readiness,
	// we use a sysinit line that busybox `sleep infinity`s in the
	// background. waitReady will time out, but that's fine: the
	// Engine doesn't gate a worker on waitReady success — workers
	// are admitted by plan tier + quota, not by per-wake readiness.
	//
	// Wait — that contradicts the substrate assertion. A real worker
	// wake would go through a different path. For THIS test, we want
	// a VM that the Manager believes is alive. The simplest shape:
	// use the standard httpd :8080 rootfs (so waitReady's TCP-accept
	// succeeds) but NEVER issue a request. The VM stays RUNNING
	// inside the Manager indefinitely; only an explicit Destroy (or
	// an Engine reaper) takes it down.
	busybox := ensureBusyboxExt4(t, t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	const instance = "worker-idle"
	inst, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   instance,
		BaseKey:    busybox,
		LayerKey:   busybox,
		VcpuCount:  1,
		MemSizeMiB: 128,
	})
	if err != nil {
		t.Fatalf("worker cold boot: %v", err)
	}
	t.Cleanup(func() {
		// Use a detached context so a t.Fatalf in the idle loop
		// still tears down the netns/cgroup/jail.
		teardownCtx, teardownCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer teardownCancel()
		_ = m.Destroy(teardownCtx, instance)
		leakcheck.AssertZero(t)
	})

	// Snapshot the host IP — if the Manager were to silently
	// re-allocate slots during the idle window, this would change
	// and the Engine's idle-tracking would break.
	origIP := inst.Lease.HostIP.String()

	// Idle window. 90s is the substrate ceiling: if any future
	// regression introduces a Manager-side reap under 90s, this
	// catches it. The Engine's full 10-min window is exercised by
	// pkg/sched/reaper_worker_exempt_test.go.
	const idleWindow = 90 * time.Second
	t.Logf("worker idle window: %s (instance=%s hostIP=%s)", idleWindow, instance, origIP)
	deadline := time.Now().Add(idleWindow)
	for time.Now().Before(deadline) {
		if m.LiveCount() != 1 {
			t.Fatalf("LiveCount=%d mid-idle-window, want 1 — Manager tore down the worker without an explicit Destroy (ADR-137 §Decision 1 violation)",
				m.LiveCount())
		}
		live := m.LiveInstances()
		worker, ok := live[instance]
		if !ok {
			t.Fatalf("%s missing from live map mid-idle-window — Manager tore down without Destroy", instance)
		}
		// Host IP stability: a re-allocation on idle would
		// silently break every Engine-level metric + replica
		// scaffold that keys on the IP.
		if worker.Lease.HostIP.String() != origIP {
			t.Errorf("%s host IP changed during idle: %q → %q",
				instance, origIP, worker.Lease.HostIP)
		}
		time.Sleep(5 * time.Second)
	}

	// Final assertion: still alive at the deadline.
	if m.LiveCount() != 1 {
		t.Errorf("LiveCount=%d at end of idle window, want 1", m.LiveCount())
	}
}
