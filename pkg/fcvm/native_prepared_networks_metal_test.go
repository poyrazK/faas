//go:build linux && metal

// adr: 149 — amendment 2026-11-07: prepared networks under native recovery.
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
	"golang.org/x/sys/unix"
)

const (
	nativePreparedIsolatedEnv = "GREGALE_NATIVE_PREPARED_ISOLATED"
	nativePreparedDaemonEnv   = "GREGALE_NATIVE_PREPARED_DAEMON_ROOT"
	nativePreparedStopEnv     = "GREGALE_NATIVE_PREPARED_STOP"
	nativePreparedTarget      = "native-prepared-target"
)

// The claim steps in order; a stand-in vmmd exits right after the named one.
var nativePreparedStops = []string{"ready", "claimed", "linked", "moved", "finished"}

// A vmmd stand-in builds a real spare under native recovery, then exits at
// each claim step without cleaning up. A fresh vmmd on the same journal must
// remove every namespace and veth, archive the spare and free every slot.
func TestMetalNativePreparedNetworkRecovery(t *testing.T) {
	ctx := nativeMetalPreparedIsolated(t, 4*time.Minute)
	if ctx == nil {
		return
	}
	if root := os.Getenv(nativePreparedDaemonEnv); root != "" {
		nativeMetalPreparedDaemon(t, ctx, root, os.Getenv(nativePreparedStopEnv))
		return
	}
	for _, stop := range nativePreparedStops {
		t.Run(stop, func(t *testing.T) { nativeMetalPreparedRecover(t, ctx, stop) })
	}
}

// The native network cost a wake avoids: building a spare (what a miss pays
// in setup_network) against claiming one (what a hit adds). Opt-in.
func TestMetalNativePreparedNetworkClaimBench(t *testing.T) {
	cycles, _ := strconv.Atoi(os.Getenv("FAAS_NATIVE_PREPARED_BENCH_CYCLES"))
	if cycles <= 0 {
		t.Skip("set FAAS_NATIVE_PREPARED_BENCH_CYCLES")
	}
	ctx := nativeMetalPreparedIsolated(t, 10*time.Minute)
	if ctx == nil {
		return
	}
	root := nativeMetalPreparedRoot(t)
	m, v, p, legacy := nativeMetalPreparedPool(t, ctx, root)
	var build, claim, link, move, finish []time.Duration
	for i := range cycles {
		start := time.Now()
		policy := nativeMetalPreparedFill(t, ctx, m, p)
		build = append(build, time.Since(start))
		target := fmt.Sprintf("native-prepared-bench-%d", i)
		start = time.Now()
		entry := p.claim(target, policy)
		claim = append(claim, time.Since(start))
		if entry == nil || entry.nativeClaim == nil {
			t.Fatalf("cycle %d: native claim did not commit", i)
		}
		lease := entry.lease
		lease.Plan = api.PlanScale
		start = time.Now()
		if err := m.prepareNativeLease(withNativePreparedClaim(ctx, *entry.nativeClaim), lease); err != nil {
			t.Fatal(err)
		}
		link = append(link, time.Since(start))
		spares, _ := m.nativeSpares()
		oldNS := entry.config.Netns
		start = time.Now()
		if err := spares.moveNativeSpare(ctx, lease.Instance, func() error { return p.move(oldNS, lease.Netns) }); err != nil {
			t.Fatal(err)
		}
		move = append(move, time.Since(start))
		start = time.Now()
		if err := spares.finishNativeClaim(ctx, *entry.nativeClaim); err != nil {
			t.Fatal(err)
		}
		finish = append(finish, time.Since(start))
		if err := m.cleanup(ctx, lease, nativeLeaseNetwork(lease), nil); err != nil {
			t.Fatalf("cycle %d cleanup: %v", i, err)
		}
	}
	hit := make([]time.Duration, cycles)
	for i := range cycles {
		hit[i] = claim[i] + move[i] + finish[i]
	}
	for _, row := range []struct {
		name string
		d    []time.Duration
	}{{"build_spare (miss setup_network)", build}, {"claim", claim}, {"prepare_linked (both paths prepare)", link},
		{"move", move}, {"finish", finish}, {"hit_extra = claim+move+finish", hit}} {
		t.Logf("NATIVE_PREPARED_BENCH %-38s n=%d p50=%s p95=%s max=%s", row.name, len(row.d),
			nativeBenchPercentile(row.d, 50), nativeBenchPercentile(row.d, 95), nativeBenchPercentile(row.d, 100))
	}
	nativeMetalPreparedAssertClean(t, m, v, root, legacy, cycles)
}

// nativeMetalPreparedIsolated re-runs the test in private mount and network
// namespaces with a tenant bridge and an empty /run/netns. It returns nil in
// the outer process.
func nativeMetalPreparedIsolated(t *testing.T, budget time.Duration) context.Context {
	t.Helper()
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Skip("requires x86_64 Linux and root")
	}
	ctx, cancel := context.WithTimeout(t.Context(), budget)
	t.Cleanup(cancel)
	if os.Getenv(nativePreparedIsolatedEnv) != "1" {
		cmd := exec.CommandContext(ctx, "unshare", "--mount", "--net", "--propagation", "private", os.Args[0],
			"-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.timeout="+budget.String(), "-test.v")
		cmd.Env = append(os.Environ(), nativePreparedIsolatedEnv+"=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated native prepared networks: %v\n%s", err, out)
		}
		t.Logf("%s", out)
		return nil
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	if os.Getenv(nativePreparedDaemonEnv) != "" {
		return ctx // the stand-in shares its parent's namespaces
	}
	if err := os.MkdirAll("/run/netns", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount("tmpfs", "/run/netns", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "mode=0755,size=4m"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Unmount("/run/netns", unix.MNT_DETACH) })
	run := wire.ExecRunner{}
	for _, argv := range [][]string{
		{"ip", "link", "set", "lo", "up"},
		{"ip", "link", "add", netns.TenantBridge, "type", "bridge"},
		{"ip", "addr", "add", "10.100.0.1/16", "dev", netns.TenantBridge},
		{"ip", "link", "set", netns.TenantBridge, "up"},
	} {
		if err := run.Run(ctx, argv); err != nil {
			t.Fatal(err)
		}
	}
	return ctx
}

func nativeMetalPreparedRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("", "gregale-native-prepared-")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// nativeMetalPreparedPool is a native vmmd's Manager with
// native_prepared_networks on and a one-spare pool on the real host
// namespace operations. Native work must never reach the legacy runner.
func nativeMetalPreparedPool(t *testing.T, ctx context.Context, root string) (*Manager, *JailerVMM, *preparedNetworkPool, *fakeRunner) {
	t.Helper()
	v := NewJailerVMM(root, 10*time.Second).WithNativeProcessRecovery().WithNativeLaunchOwners().WithNativePreparedNetworks()
	// Native helpers re-exec a vmmd helper; this test binary is not one.
	if binary := os.Getenv("FAAS_TEST_VMMD_BINARY"); binary != "" {
		v.mountHelperPath = binary
	} else if exe, err := os.Executable(); err != nil {
		t.Fatal(err)
	} else if helper, err := resolveMountHelper(exe); err != nil || helper == exe {
		t.Fatalf("set FAAS_TEST_VMMD_BINARY or put a static vmmd-jail-helper beside %s (%v)", exe, err)
	}
	r := v.nativeRecovery
	t.Cleanup(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.daemonLock != nil {
			_ = r.daemonLock.Close()
		}
	})
	legacy := &fakeRunner{failOn: "ip"}
	m := NewManager(legacy, v, Paths{}, "1.7.0", slog.New(slog.NewTextHandler(os.Stderr, nil)), nil)
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		t.Fatal(err)
	}
	pool, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	p := &preparedNetworkPool{m: m, capacity: 1, ctx: pool, cancel: cancel, notify: make(chan struct{}, 1),
		move: movePreparedNetns, removed: preparedNetworkRemoved}
	m.preparedNetworks = p
	return m, v, p, legacy
}

// nativeMetalPreparedDaemon is the vmmd stand-in. It exits with status 3
// right after stop, so nothing it holds is cleaned up.
func nativeMetalPreparedDaemon(t *testing.T, ctx context.Context, root, stop string) {
	m, _, p, _ := nativeMetalPreparedPool(t, ctx, root)
	exit := func(step string) {
		if step == stop {
			fmt.Printf("native prepared daemon stopped at %s\n", stop)
			os.Exit(3)
		}
	}
	policy := nativeMetalPreparedFill(t, ctx, m, p)
	exit("ready")
	entry := p.claim(nativePreparedTarget, policy)
	if entry == nil || entry.nativeClaim == nil {
		t.Fatal("native claim did not commit")
	}
	exit("claimed")
	lease := entry.lease
	lease.Plan = api.PlanScale
	if err := m.prepareNativeLease(withNativePreparedClaim(ctx, *entry.nativeClaim), lease); err != nil {
		t.Fatal(err)
	}
	exit("linked")
	spares, _ := m.nativeSpares()
	oldNS := entry.config.Netns
	if err := spares.moveNativeSpare(ctx, lease.Instance, func() error { return p.move(oldNS, lease.Netns) }); err != nil {
		t.Fatal(err)
	}
	exit("moved")
	if err := spares.finishNativeClaim(ctx, *entry.nativeClaim); err != nil {
		t.Fatal(err)
	}
	exit("finished")
	t.Fatalf("unknown stop %q", stop)
}

func nativeMetalPreparedRecover(t *testing.T, ctx context.Context, stop string) {
	root := nativeMetalPreparedRoot(t)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMetalNativePreparedNetworkRecovery$", "-test.timeout=2m", "-test.v")
	cmd.Env = append(os.Environ(), nativePreparedDaemonEnv+"="+root, nativePreparedStopEnv+"="+stop)
	out, _ := cmd.CombinedOutput() // the stand-in always exits non-zero
	if !strings.Contains(string(out), "native prepared daemon stopped at "+stop) {
		t.Fatalf("stand-in did not stop at %s:\n%s", stop, out)
	}
	spares, err := filepath.Glob("/run/netns/fc-prepared-*")
	if err != nil {
		t.Fatal(err)
	}
	targets, _ := filepath.Glob("/run/netns/fc-" + nativePreparedTarget)
	t.Logf("after stop %s: spare namespaces=%d target namespaces=%d", stop, len(spares), len(targets))
	if len(spares)+len(targets) != 1 {
		t.Fatalf("stop %s left %d spare and %d target namespaces, want exactly one network", stop, len(spares), len(targets))
	}
	m, v, _, legacy := nativeMetalPreparedPool(t, ctx, root)
	// Startup retires unclaimed and abandoned spares before any pool opens.
	if err := m.EnablePreparedNetworks(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := v.nativeRecovery.journal.read(nativePreparedTarget); err == nil {
		// A target that linked the spare owns it; its stop removes both names.
		if err := m.Destroy(ctx, nativePreparedTarget); err != nil {
			t.Fatalf("stop recovered target: %v", err)
		}
		owner, err := v.nativeRecovery.journal.read(nativePreparedTarget)
		if err != nil || !owner.ResourcesRemoved || owner.Prepared != (nativePreparedLink{}) {
			t.Fatalf("recovered target after stop = %+v, %v", owner, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	nativeMetalPreparedAssertClean(t, m, v, root, legacy, 1)
}

func nativeMetalPreparedAssertClean(t *testing.T, m *Manager, v *JailerVMM, root string, legacy *fakeRunner, archivedSpares int) {
	t.Helper()
	names, err := os.ReadDir("/run/netns")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Fatalf("namespaces left: %v", names)
	}
	veths, err := exec.Command("ip", "-o", "link", "show", "type", "veth").CombinedOutput()
	if err != nil || len(strings.TrimSpace(string(veths))) != 0 {
		t.Fatalf("veths left: %v\n%s", err, veths)
	}
	entries, err := os.ReadDir(v.nativeRecovery.journal.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".json"); ok && nativePreparedSpareName(id) {
			t.Fatalf("spare record left in the record root: %s", id)
		}
	}
	archived, err := os.ReadDir(filepath.Join(v.nativeRecovery.journal.root, "retired"))
	if err != nil || len(archived) != archivedSpares {
		t.Fatalf("archived spares = %d (%v), want %d", len(archived), err, archivedSpares)
	}
	m.mu.Lock()
	held := len(m.nativeRecovered)
	m.mu.Unlock()
	if held != 0 || m.alloc.InUse() != 0 {
		t.Fatalf("slots held: recovered=%d in use=%d", held, m.alloc.InUse())
	}
	if len(legacy.commands) != 0 {
		t.Fatalf("native work used the legacy runner: %v", legacy.commands)
	}
	t.Logf("clean: root %s", root)
}

// nativeMetalPreparedFill builds one spare; when the pool builds none it
// repeats the steps by hand so the failure names its step.
func nativeMetalPreparedFill(t *testing.T, ctx context.Context, m *Manager, p *preparedNetworkPool) preparedNetworkPolicy {
	t.Helper()
	policy := fillTestPreparedPool(t, m, p, 250)
	if len(p.ready) == 1 {
		return policy
	}
	lease, err := m.alloc.reserveNetwork("prepared-diagnosis")
	if err != nil {
		t.Fatalf("no spare was built; reserve: %v", err)
	}
	spares, ok := m.nativeSpares()
	if !ok || !spares.nativePreparedSparesEnabled() {
		t.Fatalf("no spare was built; native spares enabled=%v", ok)
	}
	lease.Instance, lease.Netns = "prepared-00000000-0000-4000-8000-000000000001", "fc-prepared-00000000-0000-4000-8000-000000000001"
	if _, err := spares.prepareNativeSpare(ctx, lease); err != nil {
		t.Fatalf("no spare was built; prepare spare record: %v", err)
	}
	nc := netns.NewConfig(lease.Instance, lease.Netns, lease.VethHost, lease.VethPeer, lease.HostIP)
	nc.TapUID, nc.EgressMbit, nc.PublicForwarding = lease.UID, 250, true
	nc.EgressPorts = api.TenantEgressBasePorts()
	t.Fatalf("no spare was built; setup network: %v", m.setupNetwork(ctx, nc))
	return policy
}

func nativeBenchPercentile(d []time.Duration, p int) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := slices.Clone(d)
	slices.Sort(s)
	i := (len(s)*p + 99) / 100
	return s[max(0, min(len(s)-1, i-1))].Round(100 * time.Microsecond)
}
