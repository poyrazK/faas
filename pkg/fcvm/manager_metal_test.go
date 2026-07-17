//go:build metal

// Metal integration tests: need /dev/kvm, root, firecracker + jailer on PATH,
// and real kernel/base/layer images. Run on the dev EX44 via `make test-metal`.
// Executable acceptance criteria (spec §14):
//
//	M0 — boot a hello-Firecracker VM from the pinned kernel + a busybox rootfs.
//	M1 — boot 50 × 128 MB VMs concurrently and leak zero netns/TAPs/uids on
//	     teardown.
//	M3 — park→wake p50 ≤ 350 ms over 100 cycles restoring from a snapshot each
//	     time.
package fcvm

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/wire"
)

// metalImages resolves the kernel/base/layer paths from the environment so the
// test is portable across boxes. Skips if unset.
func metalImages(t *testing.T) (kernel, base, layer string) {
	t.Helper()
	kernel = os.Getenv("FAAS_TEST_KERNEL")
	base = os.Getenv("FAAS_TEST_BASE_ROOTFS")
	layer = os.Getenv("FAAS_TEST_LAYER_ROOTFS")
	if kernel == "" || base == "" || layer == "" {
		t.Skip("set FAAS_TEST_KERNEL / FAAS_TEST_BASE_ROOTFS / FAAS_TEST_LAYER_ROOTFS to run metal tests")
	}
	return kernel, base, layer
}

func newMetalManager(t *testing.T, kernel string) *Manager {
	t.Helper()
	return NewManager(
		wire.ExecRunner{},
		NewJailerVMM(JailChrootBase, 30*time.Second),
		Paths{Kernel: kernel},
		os.Getenv("FAAS_TEST_FC_VERSION"),
		nil,
	)
}

// withCgroupRootAt points cgroupRoot at the real /sys/fs/cgroup for the
// duration of this metal test. The jailer writes the per-VM scope there
// regardless of what cgroupRoot is set to in this package, so the
// default tempdir (set by TestMain) would make writeMemoryMax probe a
// path the jailer never wrote to, returning IsNotExist.
//
// Lives here (//go:build metal) rather than in cgroup_test.go so it
// isn't flagged as unused by the lint job — which runs without -tags
// metal and therefore can't see the metal-only test files that call it.
func withCgroupRootAt(t *testing.T, path string) {
	t.Helper()
	saved := cgroupRoot
	cgroupRoot = path
	t.Cleanup(func() { cgroupRoot = saved })
}

// TestMetalBoot50Concurrent is the M1 headline acceptance test.
func TestMetalBoot50Concurrent(t *testing.T) {
	kernel, base, layer := metalImages(t)
	m := newMetalManager(t, kernel)
	const n = 50

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("m1-%d", i)
			// Per contextcheck: don't capture the test's ctx into the
			// goroutine — the goroutine's lifetime is bounded by wg.Wait
			// below, so a detached ctx cannot outlive the test. Per-VM
			// boot deadlines live inside ColdBoot itself.
			if _, err := m.ColdBoot(context.Background(), ColdBootRequest{
				Instance: id, BasePath: base, LayerPath: layer, VcpuCount: 2, MemSizeMiB: 128,
			}); err != nil {
				t.Errorf("boot %s: %v", id, err)
			}
		}(i)
	}
	wg.Wait()

	if m.LiveCount() != n {
		t.Fatalf("live=%d, want %d", m.LiveCount(), n)
	}

	// Teardown; after this the box must leak nothing (`make leakcheck`).
	for i := 0; i < n; i++ {
		if err := m.Destroy(ctx, fmt.Sprintf("m1-%d", i)); err != nil {
			t.Errorf("destroy m1-%d: %v", i, err)
		}
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatalf("after teardown live=%d leased=%d, want 0/0", m.LiveCount(), m.LeasedCount())
	}
}

// TestMetalParkWakeCycle is the M3 latency gate (spec §14, V2): park→wake p50
// ≤ 350 ms over 100 cycles, restoring from a snapshot each wake.
func TestMetalParkWakeCycle(t *testing.T) {
	kernel, base, layer := metalImages(t)

	fcVersion, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	m := NewManager(wire.ExecRunner{}, NewJailerVMM(JailChrootBase, 30*time.Second),
		Paths{Kernel: kernel}, fcVersion, nil)

	ctx := context.Background()
	snapDir := t.TempDir()
	snap := &Snapshot{
		FCVersion:   fcVersion,
		MemPath:     snapDir + "/mem",
		VMStatePath: snapDir + "/vmstate",
	}

	// Prime: cold boot once, then park to produce the first snapshot.
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: "cycle", BasePath: base, LayerPath: layer, VcpuCount: 2, MemSizeMiB: 128,
	}); err != nil {
		t.Fatalf("prime cold boot: %v", err)
	}
	if _, err := m.Park(ctx, "cycle", SnapshotSpec{MemPath: snap.MemPath, VMStatePath: snap.VMStatePath}); err != nil {
		t.Fatalf("prime park: %v", err)
	}

	const cycles = 100
	latencies := make([]time.Duration, 0, cycles)
	for i := 0; i < cycles; i++ {
		start := time.Now()
		inst, err := m.Wake(ctx, WakeRequest{
			Instance: "cycle", BasePath: base, LayerPath: layer, VcpuCount: 2, MemSizeMiB: 128, Snapshot: snap,
		})
		if err != nil {
			t.Fatalf("wake cycle %d: %v", i, err)
		}
		latencies = append(latencies, time.Since(start))
		if inst.Method != WakeRestore {
			t.Errorf("cycle %d fell back to %s — snapshot restore regressed", i, inst.Method)
		}
		if _, err := m.Park(ctx, "cycle", SnapshotSpec{MemPath: snap.MemPath, VMStatePath: snap.VMStatePath}); err != nil {
			t.Fatalf("park cycle %d: %v", i, err)
		}
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[len(latencies)/2]
	p95 := latencies[len(latencies)*95/100]
	t.Logf("wake latency over %d cycles: p50=%s p95=%s", cycles, p50, p95)
	if p50 > 350*time.Millisecond {
		t.Errorf("wake p50 = %s, want ≤ 350 ms (spec §6.3)", p50)
	}
	if p95 > 800*time.Millisecond {
		t.Errorf("wake p95 = %s, want ≤ 800 ms (spec §6.3)", p95)
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leaked leases after cycles: %d", m.LeasedCount())
	}
}

// TestMetalHelloBoot is the M0 acceptance gate (spec §14).
//
// Goal: prove end-to-end that the full jailer + firecracker + tap +
// netns path can boot a real guest and tear down clean. We don't
// measure latency here — M3 owns that — only correctness: VM live,
// VM gone, zero leaks (invariant §6.2-4/5).
//
// M0 exception to the two-drive scheme (spec §4.6): BasePath and
// LayerPath point at the SAME busybox image. There is no per-app
// layer yet (that's M2), but the chroot + driver wiring is identical
// to the two-drive hot path — only the second drive is missing its
// own ext4. We document this in the request via comment so future
// maintainers don't think it's a bug.
func TestMetalHelloBoot(t *testing.T) {
	kernel, _, _ := metalImages(t) // base/layer will be replaced by hello img
	m := newMetalManager(t, kernel)
	// Metal tests run a real jailer, which writes the per-VM scope
	// under /sys/fs/cgroup. Point cgroupRoot there so writeMemoryMax
	// probes the same path the jailer wrote (TestMain defaults to a
	// tempdir to keep the unit-test path isolated).
	withCgroupRootAt(t, "/sys/fs/cgroup")

	tmp := t.TempDir()
	busybox := ensureBusyboxExt4(t, tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const instance = "m0-hello"
	_, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   instance,
		BasePath:   busybox, // M0-only: Base == Layer, see comment above.
		LayerPath:  busybox, // produces a single-drive VM that still hits the chroot path.
		VcpuCount:  2,
		MemSizeMiB: 128,
	})
	if err != nil {
		t.Fatalf("M0 hello cold boot: %v", err)
	}
	if m.LiveCount() != 1 {
		t.Fatalf("live=%d after boot, want 1", m.LiveCount())
	}

	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatalf("destroy M0 hello VM: %v", err)
	}
	if m.LiveCount() != 0 {
		t.Fatalf("live=%d after destroy, want 0", m.LiveCount())
	}
	if m.LeasedCount() != 0 {
		t.Fatalf("leased=%d after destroy, want 0", m.LeasedCount())
	}

	// Spec §6.2-4/5: §6.2-4 = parked = zero RAM (we only had one VM but we
	// still verify nothing leaked), §6.2-5 = no shared state.
	leakcheck.AssertZero(t)
}

// TestMetalDNATPublishedToGuestPort is the #30 acceptance gate: the per-
// instance nft ruleset publishes the guest's :8080 at the host identity, so
// a root-ns HTTP client can reach the guest through the DNAT. Without #30
// (NftCommands loaded by setupNetwork) the request never arrives.
//
// waitReady dials the same address internally — but a true external probe
// proves the DNAT works for ANY caller, not just vmmd self-talk. Slot 0
// maps to 10.100.0.2 (pkg/fcvm/alloc.go hostIPForSlot), so a fresh lease
// for instance "dnat" lands on a directly-probeable IP.
//
// The M0 busybox image (busybox_ext4_metal_test.go:121-186) boots
// `/sbin/init` running `busybox httpd -f -p 8080 -h /`, so a successful
// probe proves the SYN goes through prerouting DNAT and the ACK comes back
// (which exercises the established/related accept rule).
func TestMetalDNATPublishedToGuestPort(t *testing.T) {
	kernel, _, _ := metalImages(t) // base/layer replaced by hello img
	m := newMetalManager(t, kernel)
	busybox := ensureBusyboxExt4(t, t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	inst, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   "dnat",
		BasePath:   busybox,
		LayerPath:  busybox,
		VcpuCount:  2,
		MemSizeMiB: 128,
	})
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	// inst.Lease.HostIP is the veth host-side identity (10.100.0.2 for slot 0).
	// waitReady already proved the kernel booted and the guest is listening;
	// here we re-probe from a root-ns http.Client to confirm the DNAT actually
	// publishes the guest to outside callers (the load-bearing #30 assertion).
	url := "http://" + inst.Lease.HostIP.String() + ":8080/"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s status = %d, want 200", url, resp.StatusCode)
	}

	if err := m.Destroy(ctx, "dnat"); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	leakcheck.AssertZero(t)
}

// TestMetalMemoryMaxFenceEnforced is the #33 acceptance gate. After a
// cold boot, Manager.Wake writes memory.max = (plan_mb + 8) MB to the
// per-VM cgroup scope jailer creates (spec §4.4). The test reads
// that file back from /sys/fs/cgroup and asserts the value matches.
//
// The probe is a true external read (not internal vmmd self-talk):
// even if the Wake code path silently swallows a write error and
// returns success, this test catches it — the kernel state is the
// only thing that actually protects the host. Skips when /sys/fs/
// cgroup is not mounted (Lima without nested cgroup passthrough,
// macOS dev).
func TestMetalMemoryMaxFenceEnforced(t *testing.T) {
	kernel, _, _ := metalImages(t)
	m := newMetalManager(t, kernel)
	busybox := ensureBusyboxExt4(t, t.TempDir())

	// Pre-flight: the cgroup fs must be reachable. Skipping (not
	// failing) is the right behaviour on a dev box that can't mount
	// cgroupv2 — the production EX44 always has it.
	const scopeBase = "/sys/fs/cgroup/faas-tenant.slice/vm-mem.scope"
	if _, err := os.Stat("/sys/fs/cgroup"); err != nil {
		t.Skipf("/sys/fs/cgroup not mounted (Lima/macOS dev): %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const memMB = 128
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   "mem",
		BasePath:   busybox,
		LayerPath:  busybox,
		VcpuCount:  2,
		MemSizeMiB: memMB,
	}); err != nil {
		t.Fatalf("cold boot: %v", err)
	}

	// The fence must equal (plan_mb + PerVMOverheadMB) << 20.
	wantBytes := (memMB + 8) << 20 // 8 = pkg/api.PerVMOverheadMB
	body, err := os.ReadFile(filepath.Join(scopeBase, "memory.max"))
	if err != nil {
		t.Fatalf("read %s/memory.max: %v", scopeBase, err)
	}
	got, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
	if err != nil {
		t.Fatalf("parse memory.max %q: %v", body, err)
	}
	if got != int64(wantBytes) {
		t.Errorf("memory.max = %d, want %d (=%d MiB plan + %d MiB overhead)",
			got, wantBytes, memMB, 8)
	}

	if err := m.Destroy(ctx, "mem"); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	leakcheck.AssertZero(t)
}

// TestMetalEgressCapEnforced is the #31 acceptance gate. After a cold
// boot, Manager.setupNetwork applies a tbf qdisc on the host-side veth
// with the plan's EgressMbit (spec §7: 10/25/100/250 Mbit). The test
// runs `tc -s qdisc show dev <vethHost>` and asserts the live qdisc
// carries the requested rate. Mirrors TestMetalDNATPublishedToGuestPort's
// external-probe design: any code path that swallows a tc error and
// returns success still gets caught here, because the live kernel
// state is what enforces the cap.
//
// Skips when `tc` is not on PATH or when /sys/class/net/<vethHost>
// is not visible (Lima without nested netns passthrough, macOS dev).
func TestMetalEgressCapEnforced(t *testing.T) {
	kernel, _, _ := metalImages(t)
	m := newMetalManager(t, kernel)
	busybox := ensureBusyboxExt4(t, t.TempDir())

	if _, err := exec.LookPath("tc"); err != nil {
		t.Skipf("`tc` not on PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const rateMbit = 25 // Hobby plan
	inst, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance:   "egress",
		BasePath:   busybox,
		LayerPath:  busybox,
		VcpuCount:  2,
		MemSizeMiB: 128,
		EgressMbit: rateMbit,
	})
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}

	vethHost := inst.Net.VethHost
	// Probe live kernel state. `tc -s qdisc show dev <name>` exits 0
	// even if the dev has no qdisc (it prints "qdisc noqueue"), so
	// match on the tbf keyword AND the rate.
	out, err := exec.Command("tc", "-s", "qdisc", "show", "dev", vethHost).CombinedOutput()
	if err != nil {
		t.Fatalf("tc qdisc show dev %s: %v\n%s", vethHost, err, out)
	}
	text := string(out)
	if !strings.Contains(text, "tbf") {
		t.Errorf("no tbf qdisc on %s; got:\n%s", vethHost, text)
	}
	wantRate := fmt.Sprintf("rate %dMbit", rateMbit)
	if !strings.Contains(text, wantRate) {
		t.Errorf("tbf rate not %q on %s; got:\n%s", wantRate, vethHost, text)
	}

	if err := m.Destroy(ctx, "egress"); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	leakcheck.AssertZero(t)
}
