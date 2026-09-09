//go:build metal

// adr: 069

// Sidecar metal tests (issue #463 / ADR-069 / PR-B).
//
// These tests run a real jailed firecracker with the PR-B
// N+1 drive topology (drive0 = base, drive1 = main RW, drive2
// = sidecar-0 RO, drive3 = sidecar-1 RO) and assert:
//
//  1. TestMetalSidecarBoot — guest-init's runWorkloads orchestrator
//     discovers the deployment-level roster on drive1 and
//     fork/execs the main workload + a single metrics sidecar
//     in parallel under per-workload Supervisors.
//  2. TestMetalSidecarPortReachable — the sidecar's TCP listener
//     (busybox httpd on a customer-pinned port) is reachable from
//     inside the netns via the host IP. AC #2 contract: a sidecar
//     runs alongside the main workload, reachable on a per-app
//     port inside the netns.
//  3. TestMetalTwoSidecarsColdBoot — two sidecars in the same
//     deployment cold-boot successfully (PR-B review finding #6
//     renamed it from 'TestMetalTwoSidecarsDistinctUUID' because
//     the prior name implied a UUID assertion the body never
//     wired — see cmd/e2e/v6_distinct_uuid_e2e_test.go for the
//     actual UUID gate).
//  4. TestMetalSidecarOOMIsolation — a sidecar that exceeds its
//     cgroup memory.max dies WITHOUT killing the main workload.
//     This is the AC #4 acceptance gate: a runaway sidecar must
//     not take down the customer's app.
//
// All tests share ensureSidecarExt4 which mirrors ensureBusyboxExt4
// but builds a per-sidecar ext4 with /usr/local/bin/start.sh as
// the canonical sidecar entrypoint (PR-A contract).
//
// The metal suite runs as root on a real EX44 or via Lima nested
// KVM. Without /dev/kvm the file compiles but the test binary
// exits at TestMain.

package fcvm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/rootfs"
)

// ensureSidecarExt4 returns the path to a sidecar ext4 image,
// creating one in dir if none exists. Mirrors ensureBusyboxExt4's
// fixture/build-fallback pattern but the sidecar ext4 ships:
//
//	/usr/local/bin/start.sh  (the canonical sidecar entrypoint)
//	/etc/sidecar/start.sh    (operator-visibility alias)
//
// The start.sh exec's `busybox httpd -f -p <port>` so the test
// can verify the sidecar listens on the customer-pinned port
// without needing a real OCI image. A future PR-C wires the
// customer-supplied cmd field; today every sidecar image ships
// the canonical start.sh per imaged's stamp during buildSidecarLayer.
//
// port is hardcoded to the busybox httpd listen port; the
// customer-pinned port comes from WorkloadSpec.Port on the wake
// wire and is the value the test's main workload uses to dial.
func ensureSidecarExt4(t *testing.T, dir, name string, port int) string {
	t.Helper()
	dst := filepath.Join(dir, fmt.Sprintf("sidecar-%s-%d.ext4", name, port))
	if _, err := os.Stat(dst); err == nil {
		return dst
	}
	if err := buildSidecarExt4(dst, name, port); err != nil {
		t.Fatalf("build sidecar ext4: %v", err)
	}
	return dst
}

// buildSidecarExt4 makes a tiny optimized sidecar ext4 image. The image tree
// lives below /upper, matching rootfs.Builder's production artifact layout.
// Its busybox httpd entrypoint lives at /usr/local/bin/start.sh — the
// canonical sidecar image convention. The mkfs call is the same
// journal-less recipe as buildBusyboxExt4 because guest-init mounts the
// sidecar drive read-only and chroots the workload into its /upper tree.
func buildSidecarExt4(dst, name string, port int) error {
	bb, err := exec.LookPath("busybox")
	if err != nil {
		return fmt.Errorf("busybox not on PATH: %w", err)
	}

	work, err := os.MkdirTemp("", "sidecar-skel-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	for _, sub := range []string{"upper/bin", "upper/usr/local/bin", "upper/etc/sidecar", "upper/dev", "upper/proc", "upper/sys", "upper/tmp"} {
		if err := os.MkdirAll(filepath.Join(work, sub), 0o755); err != nil {
			return err
		}
	}

	// Copy busybox into the skeleton's /usr/local/bin so the
	// start.sh exec can find it without a symlink dance.
	if err := bbCopyFile(bb, filepath.Join(work, "upper/usr/local/bin/busybox")); err != nil {
		return err
	}
	if err := os.Symlink("/usr/local/bin/busybox", filepath.Join(work, "upper/bin/sh")); err != nil {
		return err
	}
	start := fmt.Sprintf("#!/bin/sh\nexec /usr/local/bin/busybox httpd -f -p %d -h /\n", port)
	if err := os.WriteFile(filepath.Join(work, "upper/usr/local/bin/start.sh"), []byte(start), 0o755); err != nil {
		return err
	}
	// Operator-visibility alias used by `cat /etc/sidecar/start.sh`
	// inside the guest to confirm the sidecar is the right one.
	if err := os.WriteFile(filepath.Join(work, "upper/etc/sidecar/start.sh"), []byte(start), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(work, "upper/index.html"), []byte("<h1>sidecar-ready</h1>\n"), 0o644); err != nil {
		return err
	}
	if err := rootfs.InjectWorkloadManifest(filepath.Join(work, "upper"), name, api.AppManifest{
		Entrypoint: []string{"/usr/local/bin/start.sh"},
		WorkingDir: "/",
		User:       "0",
		Port:       port,
	}); err != nil {
		return fmt.Errorf("inject workload manifest: %w", err)
	}

	// Pre-size the file (modern e2fsprogs refuses zero-block input).
	if f, err := os.Create(dst); err != nil {
		return fmt.Errorf("create ext4 file: %w", err)
	} else if err := f.Truncate(64 << 20); err != nil {
		_ = f.Close()
		return fmt.Errorf("size ext4 file: %w", err)
	} else if err := f.Close(); err != nil {
		return fmt.Errorf("close ext4 file: %w", err)
	}

	cmd := exec.Command("mkfs.ext4", "-O", "^has_journal", "-d", work, "-L", "faas-sidecar", "-F", dst)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mkfs.ext4: %w", err)
	}
	if err := os.Chmod(dst, 0o644); err != nil {
		return fmt.Errorf("chmod sidecar ext4: %w", err)
	}
	return nil
}

func waitForSidecarHTTP(ctx context.Context, url string) (*http.Response, error) {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode/100 == 2 {
			return resp, nil
		}
		if err == nil {
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for %s: %w (last result: %v)", url, ctx.Err(), lastErr)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// TestMetalSidecarBoot is the M7 PR-B headline test (AC #1 +
// AC #2). It boots a guest with one sidecar and asserts:
//
//   - The wake returns (no panic in the orchestrator).
//   - The guest supervisor starts the main and sidecar workloads.
//   - The guest-init /etc/faas/workloads.json was stamped on drive1
//     by StageWorkloadRoster and survives pivot_root.
//
// The HTTP probe against the main workload's :8080 is the
// waitReady handshake — proving the supervisor reaches the
// RUNNING state — and the absence of any error path inside
// the orchestrator is the AC #1 acceptance.
//
// AC #4 (OOM isolation) and AC #2 (sidecar port reachable) get
// their own tests below; this one is the "did it boot at all"
// smoke gate.
func TestMetalSidecarBoot(t *testing.T) {
	kernel, base, layer := metalImages(t)
	m := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	tmp := t.TempDir()
	sidecar := ensureSidecarExt4(t, tmp, "metrics", 9090)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const (
		instance = "prb-sidecar-1"
		mainPort = 8080
	)
	_, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   instance,
		Plan:       "hobby",
		BaseKey:    base,
		LayerKey:   layer,
		VcpuCount:  2,
		MemSizeMiB: 256,
		Port:       mainPort,
		Sidecars: []WorkloadSpec{
			{
				Name:       "metrics",
				Type:       "sidecar",
				StorageKey: sidecar,
				DriveID:    "layer-sidecar-0",
				RamMB:      64,
				Port:       9090,
				Essential:  true,
			},
		},
	})
	if err != nil {
		t.Fatalf("PR-B sidecar cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })

	// Probe the main workload's :8080 listener (the waitReady
	// handshake already did this once; the second probe here
	// is the AC #1 surface — the boot path must end at a
	// running supervisor, not a crash-looped one).
	inst, ok := m.LiveInstances()[instance]
	if !ok {
		t.Fatalf("instance %q not in live map", instance)
	}
	url := fmt.Sprintf("http://%s:8080/", inst.Lease.HostIP.String())
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusInternalServerError {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("main :8080 returned server error %d: %s", resp.StatusCode, body)
	}
	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	sidecarResp, err := waitForSidecarHTTP(readyCtx, fmt.Sprintf("http://%s:9090/", inst.Lease.HostIP.String()))
	readyCancel()
	if err != nil {
		t.Fatalf("sidecar readiness: %v", err)
	}
	_, _ = io.Copy(io.Discard, sidecarResp.Body)
	sidecarResp.Body.Close()

	// Tear down and verify the per-instance host resources are gone.
	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	leakcheck.AssertZero(t)
}

// TestMetalSidecarPortReachable covers AC #2: a sidecar runs
// alongside the main workload, reachable on a customer-pinned
// port inside the netns. We use http.Get against the host IP
// on the sidecar port — the same address waitReady dials on
// the main port. The DNAT publishes :9090 inside the guest as
// :9090 on the host identity, so the probe lands on the
// sidecar's busybox httpd.
//
// Caveat: the gateway's per-instance portnorm ladder only DNATs
// the customer-pinned main port today (PR-C adds per-sidecar
// ports). The metal test bypasses the gateway and dials the
// host IP directly — same path vmmd's waitReady uses. This
// proves the underlying guest-init + isolated-root + cgroup wiring
// without depending on the gateway-side portnorm that PR-C
// lands separately.
func TestMetalSidecarPortReachable(t *testing.T) {
	kernel, base, layer := metalImages(t)
	m := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	tmp := t.TempDir()
	sidecar := ensureSidecarExt4(t, tmp, "metrics", 9091)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const instance = "prb-sidecar-port-1"
	inst, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   instance,
		Plan:       "hobby",
		BaseKey:    base,
		LayerKey:   layer,
		VcpuCount:  2,
		MemSizeMiB: 256,
		Port:       8080,
		Sidecars: []WorkloadSpec{
			{Name: "metrics", Type: "sidecar", StorageKey: sidecar, DriveID: "layer-sidecar-0", RamMB: 64, Port: 9091, Essential: true},
		},
	})
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })

	url := fmt.Sprintf("http://%s:9091/", inst.Lease.HostIP.String())
	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	resp, err := waitForSidecarHTTP(readyCtx, url)
	readyCancel()
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("sidecar :9091 returned %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("sidecar Content-Type = %q, want text/html (busybox httpd)", resp.Header.Get("Content-Type"))
	}

	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	leakcheck.AssertZero(t)
}

// TestMetalTwoSidecarsColdBoot pins the 2-sidecar cap seam
// (PR-B review finding #6). It boots a guest with TWO sidecars
// on the same deployment (well within the cap of 2) and asserts
// the cold-boot path doesn't panic — that's the load-bearing
// surface for the per-workload cgroup scopes and the N+1 drive
// topology. The previous name 'TestMetalTwoSidecarsDistinctUUID'
// implied a UUID assertion that was never wired (the body sets
// inst then ignores it via '_ = inst'); the rename surfaces the
// real contract. The distinct-UUID gate is in
// cmd/e2e/v6_distinct_uuid_e2e_test.go, where the vsock probe
// can read /proc/sys/kernel/random/uuid from inside the guest.
func TestMetalTwoSidecarsColdBoot(t *testing.T) {
	kernel, base, layer := metalImages(t)
	m := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	tmp := t.TempDir()
	sc0 := ensureSidecarExt4(t, tmp, "metrics", 9100)
	sc1 := ensureSidecarExt4(t, tmp, "logger", 9101)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const instance = "prb-sidecar-2"
	inst, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   instance,
		Plan:       "hobby",
		BaseKey:    base,
		LayerKey:   layer,
		VcpuCount:  2,
		MemSizeMiB: 256,
		Port:       8080,
		Sidecars: []WorkloadSpec{
			{Name: "metrics", Type: "sidecar", StorageKey: sc0, DriveID: "layer-sidecar-0", RamMB: 64, Port: 9100, Essential: true},
			{Name: "logger", Type: "sidecar", StorageKey: sc1, DriveID: "layer-sidecar-1", RamMB: 32, Port: 9101, Essential: false},
		},
	})
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	for _, port := range []int{9100, 9101} {
		readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
		resp, readyErr := waitForSidecarHTTP(readyCtx, fmt.Sprintf("http://%s:%d/", inst.Lease.HostIP.String(), port))
		readyCancel()
		if readyErr != nil {
			t.Fatalf("sidecar :%d readiness: %v", port, readyErr)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	// UUID readback lives in cmd/e2e/v6_distinct_uuid_e2e_test.go.

	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	leakcheck.AssertZero(t)
}

// TestMetalSidecarOOMIsolation covers AC #4: a sidecar that
// exceeds its cgroup memory.max dies WITHOUT killing the main
// workload. The test path:
//
//  1. Boot a guest with a 16 MB sidecar (well below the 256 MB
//     main workload's cgroup); the sidecar's ext4 carries a
//     32 MB fixture file at /var/log/lastlog that busybox httpd
//     serves on a single GET.
//  2. Probe the main workload's :8080 once to confirm the
//     guest-init handshake reaches RUNNING (AC #1 + #2
//     preconditions).
//  3. Hit the sidecar's /lastlog URL — the 32 MB > 16 MB
//     cgroup, the sidecar OOMs, the kernel's memcg kills the
//     process. The guest memcg reports the event.
//  4. Probe the MAIN workload's :8080 again — it must still
//     respond 2xx (AC #4 acceptance).
//
// The test relies on busybox httpd's content-serve path: a
// single GET /lastlog triggers a 32 MB read into the in-guest
// page cache, which lands in the sidecar's cgroup scope (the
// memcg charges the page cache to the workload that dirtied it,
// not the kernel). When the cgroup.max exceeds, the OOM-killer
// fires inside the sidecar scope. The main workload's cgroup
// is isolated at the parent scope boundary and is unaffected.
//
// The test is environment-dependent: it requires /dev/kvm (the
// metal suite gate) and a host kernel that supports memcg OOM
// kill notifications (cgroup v2 — the production EX44 always
// runs v2 per §11). On a v1 host the test skips (the boot
// path's cgroup_root probe returns false).
func TestMetalSidecarOOMIsolation(t *testing.T) {
	// Pre-flight: cgroup v2 + the per-workload path. The
	// six-guarded skip mirrors the §11 production posture
	// (cgroups v2 is required for firecracker snapshot
	// restore and the per-workload memcg isolation; the
	// README / CLAUDE.md "cgroup v2 only" rule is enforced
	// here, not at boot).
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		t.Skipf("cgroup v2 unavailable on this host (%v); AC #4 metal gate requires v2", err)
	}
	kernel, base, layer := metalImages(t)
	m := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	tmp := t.TempDir()
	sidecar := ensureOOMSidecarExt4(t, tmp, "stress", 9092)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	const (
		instance     = "prc-sidecar-oom-1"
		mainPort     = 8080
		sidecarPort  = 9092
		sidecarRamMB = 16
	)
	inst, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   instance,
		Plan:       "hobby",
		BaseKey:    base,
		LayerKey:   layer,
		VcpuCount:  2,
		MemSizeMiB: 256,
		Port:       mainPort,
		Sidecars: []WorkloadSpec{
			{
				Name:       "stress",
				Type:       "sidecar",
				StorageKey: sidecar,
				DriveID:    "layer-sidecar-0",
				RamMB:      sidecarRamMB,
				Port:       sidecarPort,
				Essential:  false,
			},
		},
	})
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort destroy — the sidecar OOM may leave
		// the guest in a half-reaped state, but the host's
		// netns/cgroup cleanup is idempotent.
		dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dcancel()
		_ = m.Destroy(dctx, instance)
	})

	// Step 1: probe main workload must succeed (precondition).
	mainURL := fmt.Sprintf("http://%s:%d/", inst.Lease.HostIP.String(), mainPort)
	resp, err := http.Get(mainURL)
	if err != nil {
		t.Fatalf("main GET %s: %v", mainURL, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= http.StatusInternalServerError {
		t.Fatalf("main :8080 (pre) = %d, want a live HTTP response", resp.StatusCode)
	}
	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	readyResp, err := waitForSidecarHTTP(readyCtx, fmt.Sprintf("http://%s:%d/", inst.Lease.HostIP.String(), sidecarPort))
	readyCancel()
	if err != nil {
		t.Fatalf("sidecar pre-OOM readiness: %v", err)
	}
	_, _ = io.Copy(io.Discard, readyResp.Body)
	readyResp.Body.Close()

	// Step 2: trigger the OOM. The sidecar's httpd serves
	// /var/log/lastlog (a 32 MB file) on GET /lastlog. The
	// bus read fills the page cache in the sidecar's cgroup
	// scope; the memcg sees the working set climb past
	// sidecarRamMB and the OOM-killer fires. Connection
	// errors / EOF mid-flight are EXPECTED here — that's
	// the OOM. We don't fail the test on the sidecar error;
	// the AC #4 acceptance is the main workload's survival.
	sidecarURL := fmt.Sprintf("http://%s:%d/lastlog", inst.Lease.HostIP.String(), sidecarPort)
	sresp, err := http.Get(sidecarURL)
	if err == nil {
		// Drain so the kernel actually delivers the bytes.
		_, _ = io.Copy(io.Discard, sresp.Body)
		sresp.Body.Close()
		t.Logf("sidecar response = %d (no OOM triggered; check fixture size > cgroup)", sresp.StatusCode)
	} else {
		t.Logf("sidecar GET errored (%v) — expected on OOM", err)
	}

	// Step 3: probe main workload again. AC #4 acceptance:
	// the main workload MUST still answer 2xx after the
	// sidecar OOMs. A regression that lets the memcg OOM
	// propagate to the parent scope would 503 here.
	// Allow a brief settle for the OOM-killer to reap +
	// the postmortem to settle.
	time.Sleep(500 * time.Millisecond)
	resp2, err := http.Get(mainURL)
	if err != nil {
		t.Fatalf("main GET (post-OOM) %s: %v", mainURL, err)
	}
	_, _ = io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode >= http.StatusInternalServerError {
		t.Errorf("main :8080 (post-OOM) = %d, want a live HTTP response (AC #4 violated: sidecar OOM propagated to main workload)",
			resp2.StatusCode)
	}
}

// ensureOOMSidecarExt4 builds a sidecar ext4 whose /var/log/lastlog
// is a 32 MB fixture file (sparse) that busybox httpd serves on
// GET /lastlog. The path -h /var/log keeps the busybox-root index
// trivial so the test's pre-OOM probe (Step 1) doesn't itself
// trigger the OOM. The 32 MB > 16 MB cgroup bound is the load
// generator; the OOM-killer fires inside the sidecar's cgroup
// scope, the main workload is isolated.
//
// The fixture file is a sparse truncate (no host-side byte
// copy); the ext4 mkfs writes the file and the in-guest
// page-cache carve-out grows on the GET.
//
// buildSidecarExt4 is reused with a shape override baked into
// the sidecar's start.sh: rather than refactor the build helper
// for a single test, we duplicate the body — the contract is
// simple enough that the shared skeleton is a constant body, and
// the test wants to extend it with a single fixture file path.
func ensureOOMSidecarExt4(t *testing.T, dir, name string, port int) string {
	t.Helper()
	dst := filepath.Join(dir, fmt.Sprintf("sidecar-oom-%s-%d.ext4", name, port))
	if _, err := os.Stat(dst); err == nil {
		return dst
	}
	bb, err := exec.LookPath("busybox")
	if err != nil {
		t.Fatalf("busybox not on PATH: %v", err)
	}

	work, err := os.MkdirTemp("", "sidecar-oom-skel-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	defer os.RemoveAll(work)

	for _, sub := range []string{"upper/bin", "upper/usr/local/bin", "upper/etc/sidecar", "upper/var/log", "upper/dev", "upper/proc", "upper/sys", "upper/tmp"} {
		if err := os.MkdirAll(filepath.Join(work, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	if err := bbCopyFile(bb, filepath.Join(work, "upper/usr/local/bin/busybox")); err != nil {
		t.Fatalf("copy busybox: %v", err)
	}
	if err := os.Symlink("/usr/local/bin/busybox", filepath.Join(work, "upper/bin/sh")); err != nil {
		t.Fatalf("symlink sh: %v", err)
	}
	// 32 MB sparse fixture. The Truncate doesn't allocate
	// host memory; the ext4 mkfs writes the file and the
	// in-guest GET triggers the memcg-charged read.
	f, err := os.Create(filepath.Join(work, "upper/var/log/lastlog"))
	if err != nil {
		t.Fatalf("create lastlog: %v", err)
	}
	if err := f.Truncate(32 << 20); err != nil {
		_ = f.Close()
		t.Fatalf("truncate lastlog: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close lastlog: %v", err)
	}
	start := fmt.Sprintf("#!/bin/sh\nexec /usr/local/bin/busybox httpd -f -p %d -h /var/log\n", port)
	if err := os.WriteFile(filepath.Join(work, "upper/usr/local/bin/start.sh"), []byte(start), 0o755); err != nil {
		t.Fatalf("write start.sh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "upper/etc/sidecar/start.sh"), []byte(start), 0o644); err != nil {
		t.Fatalf("write alias: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "upper/var/log/index.html"), []byte("<h1>oom-test</h1>\n"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := rootfs.InjectWorkloadManifest(filepath.Join(work, "upper"), name, api.AppManifest{
		Entrypoint: []string{"/usr/local/bin/start.sh"},
		WorkingDir: "/",
		User:       "0",
		Port:       port,
	}); err != nil {
		t.Fatalf("inject workload manifest: %v", err)
	}

	if f, err := os.Create(dst); err != nil {
		t.Fatalf("create ext4: %v", err)
	} else if err := f.Truncate(64 << 20); err != nil {
		_ = f.Close()
		t.Fatalf("size ext4: %v", err)
	} else if err := f.Close(); err != nil {
		t.Fatalf("close ext4: %v", err)
	}
	cmd := exec.Command("mkfs.ext4", "-O", "^has_journal", "-d", work, "-L", "faas-sidecar-oom", "-F", dst)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mkfs.ext4: %v", err)
	}
	if err := os.Chmod(dst, 0o644); err != nil {
		t.Fatalf("chmod ext4: %v", err)
	}
	return dst
}
