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

// VMM starts and stops the jailed firecracker process for an instance.
type VMM interface {
	// Boot spawns jailer→firecracker with cfg and returns once the guest passes
	// readiness. It must clean up its own chroot/process if it returns an error.
	Boot(ctx context.Context, l Lease, cfg VMConfig) error
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
	Lease Lease
	Net   netns.Config
}

// Manager tracks live instances and serialises nothing on the hot path beyond a
// short-held map lock. Safe for concurrent ColdBoot/Destroy.
type Manager struct {
	alloc *Allocator
	run   Runner
	vmm   VMM
	paths Paths
	log   *slog.Logger

	mu   sync.Mutex
	live map[string]*Instance
}

// NewManager wires a Manager. log may be nil (a discard logger is used).
func NewManager(run Runner, vmm VMM, paths Paths, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.New(slog.NewTextHandler(discard{}, nil))
	}
	return &Manager{
		alloc: NewAllocator(),
		run:   run,
		vmm:   vmm,
		paths: paths,
		log:   log,
		live:  make(map[string]*Instance),
	}
}

// ColdBootRequest is a cold-boot (fallback path / first boot, spec §4.4).
type ColdBootRequest struct {
	Instance   string
	BasePath   string // drive0 shared ro base rootfs for the app's runtime
	LayerPath  string // drive1 per-app layer
	VcpuCount  int
	MemSizeMiB int
}

// ColdBoot brings an instance up from rootfs. On any error it unwinds every
// resource it acquired and returns the original error — the caller sees no
// half-built instance and the box leaks nothing.
func (m *Manager) ColdBoot(ctx context.Context, req ColdBootRequest) (_ *Instance, err error) {
	lease, err := m.alloc.Acquire(req.Instance)
	if err != nil {
		return nil, fmt.Errorf("cold boot %s: acquire lease: %w", req.Instance, err)
	}
	nc := netns.NewConfig(lease.Instance, lease.Netns, lease.VethHost, lease.VethPeer, lease.HostIP)

	// From here on, any failure must fully clean up.
	defer func() {
		if err != nil {
			m.cleanup(context.WithoutCancel(ctx), lease, nc)
		}
	}()

	if err = m.setupNetwork(ctx, nc); err != nil {
		return nil, fmt.Errorf("cold boot %s: network setup: %w", req.Instance, err)
	}

	spec := ColdBootSpec{
		KernelPath: m.paths.Kernel,
		BasePath:   req.BasePath,
		LayerPath:  req.LayerPath,
		VcpuCount:  req.VcpuCount,
		MemSizeMiB: req.MemSizeMiB,
		Tap:        nc.Tap,
	}
	if err = spec.Validate(); err != nil {
		return nil, fmt.Errorf("cold boot %s: %w", req.Instance, err)
	}
	if err = m.vmm.Boot(ctx, lease, BuildColdBootConfig(spec)); err != nil {
		return nil, fmt.Errorf("cold boot %s: boot vm: %w", req.Instance, err)
	}

	inst := &Instance{Lease: lease, Net: nc}
	m.mu.Lock()
	m.live[req.Instance] = inst
	m.mu.Unlock()
	m.log.Info("cold boot ok", "instance", req.Instance, "uid", lease.UID, "host_ip", lease.HostIP.String())
	return inst, nil
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

// setupNetwork runs each setup command in order, stopping at the first error.
func (m *Manager) setupNetwork(ctx context.Context, nc netns.Config) error {
	for _, argv := range nc.SetupCommands() {
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
