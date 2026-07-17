package fcvm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/onebox-faas/faas/pkg/netns"
)

// Manager is vmmd's core: it owns the whole per-instance resource lifecycle —
// lease → network → jailed firecracker → teardown. Its central guarantee is that
// EVERY failure path fully unwinds (network torn down, VM killed, lease released),
// so the box never leaks netns/TAPs/uids/cgroups (invariant §6.2-4/5,
// `make leakcheck`). The side effects are injected so this guarantee is proven by
// unit tests without KVM; the metal implementations live behind //go:build metal.

// Runner executes one host command (ip/nft/sysctl) to completion.
type Runner interface {
	Run(ctx context.Context, argv []string) error
}

// VMM starts, snapshots, restores, and stops the jailed firecracker process for
// an instance.
type VMM interface {
	// Boot spawns jailer→firecracker with cfg and returns once the guest passes
	// readiness. It must clean up its own chroot/process if it returns an error.
	Boot(ctx context.Context, l Lease, cfg VMConfig) error
	// Restore loads a snapshot into a fresh jailed firecracker and resumes it,
	// returning once the guest is ready. On error it cleans up its own process.
	Restore(ctx context.Context, l Lease, spec RestoreSpec) error
	// Snapshot pauses the running VM, writes a full snapshot to spec's paths, and
	// destroys the VM (spec §4.4). The instance is gone when this returns.
	Snapshot(ctx context.Context, l Lease, spec SnapshotSpec) (SnapshotInfo, error)
	// Kill stops the firecracker process and removes the jail chroot. It is
	// best-effort and idempotent — safe to call on an instance that never fully
	// booted.
	Kill(ctx context.Context, l Lease) error
}

// Paths locates the kernel and base images on disk (spec §8). Injected so tests
// don't touch the filesystem.
type Paths struct {
	Kernel string // /srv/fc/base/vmlinux-6.1.x
}

// Instance is a live (or booting) microVM tracked by the Manager.
type Instance struct {
	Lease  Lease
	Net    netns.Config
	Method WakeMethod // how it came up; a restore that fell back reads WakeColdBoot
}

// Manager tracks live instances and serialises nothing on the hot path beyond a
// short-held map lock. Safe for concurrent Wake/Destroy.
type Manager struct {
	alloc     *Allocator
	run       Runner
	vmm       VMM
	paths     Paths
	fcVersion string // the running Firecracker version; snapshots load only on a match
	log       *slog.Logger

	mu   sync.Mutex
	live map[string]*Instance
}

// NewManager wires a Manager. fcVersion is the running Firecracker version (used
// to decide snapshot usability, ADR-005). log may be nil (a discard logger).
func NewManager(run Runner, vmm VMM, paths Paths, fcVersion string, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.New(slog.NewTextHandler(discard{}, nil))
	}
	return &Manager{
		alloc:     NewAllocator(),
		run:       run,
		vmm:       vmm,
		paths:     paths,
		fcVersion: fcVersion,
		log:       log,
		live:      make(map[string]*Instance),
	}
}

// WakeRequest brings an app up for a request or cron (spec §6.1). If Snapshot is
// usable on the running Firecracker version it is restored (fast path); otherwise,
// or if restore fails, the instance cold boots from rootfs (ADR-005: cold boot
// always works). BasePath/LayerPath are required for the cold path.
type WakeRequest struct {
	Instance   string
	BasePath   string // drive0 shared ro base rootfs for the app's runtime
	LayerPath  string // drive1 per-app layer
	VcpuCount  int
	MemSizeMiB int
	EgressMbit int       // per-plan tc cap (pkg/api/limits.EgressMbit); 0 = no cap
	Snapshot   *Snapshot // nil => cold boot
}

// ColdBootRequest is the deploy-pipeline prime path: a first boot with no
// snapshot yet (spec §9.6).
type ColdBootRequest struct {
	Instance   string
	BasePath   string
	LayerPath  string
	VcpuCount  int
	MemSizeMiB int
	EgressMbit int // per-plan tc cap; 0 = no cap (legacy / disabled)
}

// ColdBoot boots an instance from rootfs with no snapshot. It is Wake with a nil
// snapshot.
func (m *Manager) ColdBoot(ctx context.Context, req ColdBootRequest) (*Instance, error) {
	return m.Wake(ctx, WakeRequest{
		Instance: req.Instance, BasePath: req.BasePath, LayerPath: req.LayerPath,
		VcpuCount: req.VcpuCount, MemSizeMiB: req.MemSizeMiB,
		EgressMbit: req.EgressMbit, Snapshot: nil,
	})
}

// Wake brings an instance up, preferring snapshot restore and falling back to
// cold boot. On any terminal error it unwinds every resource it acquired — the
// caller sees no half-built instance and the box leaks nothing (§6.2-4/5).
func (m *Manager) Wake(ctx context.Context, req WakeRequest) (_ *Instance, err error) {
	lease, err := m.alloc.Acquire(req.Instance)
	if err != nil {
		return nil, fmt.Errorf("wake %s: acquire lease: %w", req.Instance, err)
	}
	nc := netns.NewConfig(lease.Instance, lease.Netns, lease.VethHost, lease.VethPeer, lease.HostIP)
	nc.EgressMbit = req.EgressMbit

	// Any failure past this point must fully clean up.
	defer func() {
		if err != nil {
			m.cleanup(context.WithoutCancel(ctx), lease, nc)
		}
	}()

	if err = m.setupNetwork(ctx, nc); err != nil {
		return nil, fmt.Errorf("wake %s: network setup: %w", req.Instance, err)
	}

	var method WakeMethod
	method, err = m.bringUp(ctx, lease, nc, req)
	if err != nil {
		return nil, err
	}

	// memory.max fence (spec §4.4) — written AFTER bringUp returns because
	// the scope is created by jailer during Boot/Restore and does not exist
	// before then. writeMemoryMax is naturally idempotent (cgroupv2 accepts
	// an identical-value write as a no-op), so snapshot-restore Wake does
	// not need a reset. Failure routes through the deferred cleanup path —
	// the VM is already up, but teardown kills it and releases the lease.
	if err = writeMemoryMax(req.Instance, req.MemSizeMiB); err != nil {
		return nil, fmt.Errorf("wake %s: cgroup fence: %w", req.Instance, err)
	}

	inst := &Instance{Lease: lease, Net: nc, Method: method}
	m.mu.Lock()
	m.live[req.Instance] = inst
	m.mu.Unlock()
	m.log.Info("wake ok", "instance", req.Instance, "method", method.String(),
		"uid", lease.UID, "host_ip", lease.HostIP.String())
	return inst, nil
}

// bringUp performs restore-or-cold-boot into an already-networked netns. A
// restore miss or failure is NOT terminal — it falls back to cold boot (ADR-005).
// The returned method is what actually happened: a restore that fell back reads
// WakeColdBoot, so schedd can mark the snapshot stale and schedule a re-snapshot.
// A non-nil error means even cold boot failed (a real wake failure).
func (m *Manager) bringUp(ctx context.Context, lease Lease, nc netns.Config, req WakeRequest) (WakeMethod, error) {
	if PlanWake(req.Snapshot, m.fcVersion) == WakeRestore {
		rs := RestoreSpec{
			MemPath: req.Snapshot.MemPath, VMStatePath: req.Snapshot.VMStatePath,
			Tap: nc.Tap,
			// The restored VM re-reads kernel + drives under the chroot
			// basenames; Park→Kill erased the previous chroot, so hand the
			// Manager.ColdBoot equivalents back to the VMM to re-stage.
			KernelPath: m.paths.Kernel,
			BasePath:   req.BasePath,
			LayerPath:  req.LayerPath,
		}
		if rErr := m.vmm.Restore(ctx, lease, rs); rErr == nil {
			return WakeRestore, nil
		} else {
			// Fall back to cold boot into the same netns; kill any half-restored VM.
			m.log.Warn("restore failed; cold-boot fallback", "instance", req.Instance, "err", rErr)
			_ = m.vmm.Kill(ctx, lease)
		}
	}

	spec := ColdBootSpec{
		KernelPath: m.paths.Kernel,
		BasePath:   req.BasePath,
		LayerPath:  req.LayerPath,
		VcpuCount:  req.VcpuCount,
		MemSizeMiB: req.MemSizeMiB,
		Tap:        nc.Tap,
	}
	if err := spec.Validate(); err != nil {
		return WakeColdBoot, fmt.Errorf("wake %s: %w", req.Instance, err)
	}
	if err := m.vmm.Boot(ctx, lease, BuildColdBootConfig(spec)); err != nil {
		return WakeColdBoot, fmt.Errorf("wake %s: cold boot: %w", req.Instance, err)
	}
	return WakeColdBoot, nil
}

// Park snapshots a running instance then destroys it, freeing all resident RAM
// (invariant §6.2-4: a parked app's cgroup is gone). The snapshot files are
// written to spec's paths. Returns the snapshot info for schedd/imaged to record.
func (m *Manager) Park(ctx context.Context, instance string, spec SnapshotSpec) (SnapshotInfo, error) {
	m.mu.Lock()
	inst, ok := m.live[instance]
	m.mu.Unlock()
	if !ok {
		return SnapshotInfo{}, fmt.Errorf("park %s: not live", instance)
	}

	info, err := m.vmm.Snapshot(ctx, inst.Lease, spec)
	if err != nil {
		// The VM may be in an unknown state; destroy it so nothing leaks. The
		// caller keeps the app cold-bootable (its rootfs is intact).
		_ = m.Destroy(ctx, instance)
		return SnapshotInfo{}, fmt.Errorf("park %s: snapshot: %w", instance, err)
	}
	// Snapshot already destroyed the VM process; release network + lease. cleanup
	// also calls Kill, which is an idempotent no-op on the already-gone VM.
	m.mu.Lock()
	delete(m.live, instance)
	m.mu.Unlock()
	m.cleanup(ctx, inst.Lease, inst.Net)
	m.log.Info("parked", "instance", instance, "mem_bytes", info.MemBytes)
	return info, nil
}

// Destroy stops an instance and releases all its resources. Idempotent: an
// unknown instance is a no-op (already gone).
func (m *Manager) Destroy(ctx context.Context, instance string) error {
	m.mu.Lock()
	inst, ok := m.live[instance]
	if ok {
		delete(m.live, instance)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	m.cleanup(ctx, inst.Lease, inst.Net)
	m.log.Info("destroyed", "instance", instance)
	return nil
}

// LiveCount reports how many instances the Manager currently tracks.
func (m *Manager) LiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.live)
}

// LeasedCount reports how many allocator slots are held. After a clean teardown
// of everything, LiveCount and LeasedCount must both be zero — the leak check.
func (m *Manager) LeasedCount() int { return m.alloc.InUse() }

// setupNetwork realises the per-instance topology (veth/tap/addressing), applies
// the per-plan tc egress cap on the host-side veth, and then loads the
// nftables ruleset that publishes the guest and enforces the egress policy
// (§7/§11). Commands run in order, stopping at the first error; a failure
// leaves the caller's deferred cleanup to unwind everything (invariant §6.2-5).
// The DNAT rules must land before readiness is probed, so they run here, inside
// the setup phase, rather than after bringUp.
//
// Ordering matters on snapshot-restore Wake (the netns outlives the VM):
// each ruleset's reset (`tc qdisc del`, `nft delete table`) runs best-effort
// BEFORE its strict add, so the second `add` does not collide. Both resets
// exit non-zero on a fresh netns / brand-new veth; those failures are
// expected and logged at Debug.
func (m *Manager) setupNetwork(ctx context.Context, nc netns.Config) error {
	if err := m.runCommands(ctx, nc.SetupCommands()); err != nil {
		return err
	}

	// tc egress cap. Best-effort reset (errors expected on fresh veth);
	// strict add runs only when the plan carries a cap. EgressMbit == 0
	// keeps legacy callers (existing fakeRunner tests, debug paths)
	// working without forcing every caller to set a non-zero rate.
	for _, argv := range nc.TcResetCommands() {
		if err := m.run.Run(ctx, argv); err != nil {
			m.log.Debug("tc reset (best-effort, expected on fresh veth)",
				"instance", nc.Instance, "argv", argv, "err", err)
		}
	}
	if nc.EgressMbit > 0 {
		if err := m.runCommands(ctx, nc.TcCommands()); err != nil {
			return fmt.Errorf("tc egress cap: %w", err)
		}
	}

	// nft ruleset reset + strict add. See NftCommands / NftResetCommands
	// doc comments for the established/related ordering that makes
	// published replies survive the lateral-movement deny.
	for _, argv := range nc.NftResetCommands() {
		if err := m.run.Run(ctx, argv); err != nil {
			m.log.Debug("nft reset (best-effort, expected on fresh netns)",
				"instance", nc.Instance, "argv", argv, "err", err)
		}
	}
	return m.runCommands(ctx, nc.NftCommands())
}

// runCommands runs each argv in order, stopping at the first error. The argv
// is included in the wrapped error so the failure is identifiable in logs.
func (m *Manager) runCommands(ctx context.Context, cmds [][]string) error {
	for _, argv := range cmds {
		if err := m.run.Run(ctx, argv); err != nil {
			return fmt.Errorf("%v: %w", argv, err)
		}
	}
	return nil
}

// cleanup is the unwind path: best-effort kill the VM, best-effort tear down the
// network, and always release the lease. Errors are logged, never returned — a
// cleanup that gives up would leak.
func (m *Manager) cleanup(ctx context.Context, lease Lease, nc netns.Config) {
	if err := m.vmm.Kill(ctx, lease); err != nil {
		m.log.Warn("cleanup: kill vm", "instance", lease.Instance, "err", err)
	}
	for _, argv := range nc.TeardownCommands() {
		if err := m.run.Run(ctx, argv); err != nil {
			// Teardown commands are expected to fail if the resource was never
			// created (e.g. netns del on a boot that failed before netns add).
			m.log.Debug("cleanup: teardown cmd", "cmd", argv, "err", err)
		}
	}
	// cleanup runs exactly once per lease (failed boot OR Destroy, never both),
	// so Release should succeed; a failure here is a real leak signal, not noise.
	if err := m.alloc.Release(lease.Instance); err != nil {
		m.log.Warn("cleanup: release lease", "instance", lease.Instance, "err", err)
	}
}

// discard is an io.Writer sink for the nil-logger fallback.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
