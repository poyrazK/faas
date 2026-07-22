package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/secretbox"
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
	// TriggerResumeHook dials the guest's vsock UDS and asks it to run its
	// post-restore side effects (re-seed entropy + step clock, guest/init/resume.go).
	// Must be called from Restore after /snapshot/load and before waitReady so
	// the app cannot accept on :8080 with a stale RNG stream (spec §11 V6).
	// ADR-022 records the wire format (4-byte msg type + JSON body, port 1024
	// on the fixed host CID 3).
	TriggerResumeHook(ctx context.Context, l Lease, hostTimeUnixNano int64) error
	// Snapshot pauses the running VM, writes a full snapshot to spec's paths, and
	// destroys the VM (spec §4.4). The instance is gone when this returns.
	Snapshot(ctx context.Context, l Lease, spec SnapshotSpec) (SnapshotInfo, error)
	// Kill stops the firecracker process and removes the jail chroot. It is
	// best-effort and idempotent — safe to call on an instance that never fully
	// booted.
	Kill(ctx context.Context, l Lease) error
	// DestroyWithExport is the build-aware teardown: it waits for the firecracker
	// child to exit, captures the exit code, and (if exportDir != "") loopback-
	// mounts the chroot-local drive1 to copy out the produced artifacts before
	// removing the chroot. App VMs pass exportDir=""; builder VMs (M6) pass the
	// host directory builderd wants files under. Returns the captured exit code
	// (0 for app VMs, the build's own exit code for builder VMs).
	DestroyWithExport(ctx context.Context, l Lease, exportDir string) (int, error)
	// StageSecretsEnv is the G2 write-side counterpart to DestroyWithExport's
	// read-side artifact pull: loopback-mounts drive1 in the chroot, writes
	// /etc/faas/secrets.env (already-unsealed JSON), and umounts. jsonBlob may
	// be empty — implementations MUST treat that as a no-op so apps without
	// secrets skip the mount/umount cycle entirely.
	StageSecretsEnv(instance string, jsonBlob []byte) error
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

	mu         sync.Mutex
	live       map[string]*Instance
	exportDirs map[string]string // instance -> host export dir (builder VMs only, M6)
	// metrics is the cold-boot fallback counter (vmmd_cold_boot_fallback_total).
	// nil-safe: bringUp calls m.metrics.ObserveFallback() which no-ops when nil,
	// so unit tests that construct a Manager without metrics don't need a stub.
	metrics *ColdBootMetrics
	// hostIdentity is the X25519 secret key used to unseal per-app sealed env
	// blobs at wake time (spec §11/G2). nil means "no host age configured" —
	// a Wake call with SealedEnvEntries set is rejected with ErrNoHostKey
	// rather than silently dropping plaintext. vmmd owns the on-disk file.
	hostIdentity *age.X25519Identity
	// conntrackCap is the effective per-instance conntrack cap. Probed once
	// at construction from api.ConntrackCapProbe(): DefaultConntrackCap when
	// the kernel supports ct expressions in netns (CONFIG_NF_CONNTRACK_NET_NS),
	// 0 when it doesn't (the ct cap rule is omitted, egress tc cap unaffected).
	conntrackCap int64
}

// NewManager wires a Manager. fcVersion is the running Firecracker version (used
// to decide snapshot usability, ADR-005). log may be nil (a discard logger).
// metrics may be nil (e.g. unit tests that don't care about Prometheus); the
// fallback counter is then a no-op (ColdBootMetrics.ObserveFallback is nil-safe).
func NewManager(run Runner, vmm VMM, paths Paths, fcVersion string, log *slog.Logger, metrics *ColdBootMetrics) *Manager {
	if log == nil {
		log = slog.New(slog.NewTextHandler(discard{}, nil))
	}
	return &Manager{
		alloc:        NewAllocator(),
		run:          run,
		vmm:          vmm,
		paths:        paths,
		fcVersion:    fcVersion,
		log:          log,
		live:         make(map[string]*Instance),
		exportDirs:   make(map[string]string),
		metrics:      metrics,
		conntrackCap: api.ConntrackCapProbe(),
	}
}

// SetHostIdentity attaches the unseal key. Only vmmd calls this — the
// Manager holds the private half for the duration of the process. NOT
// safe to call concurrently with Wake; production wires it before
// serving traffic.
func (m *Manager) SetHostIdentity(id *age.X25519Identity) {
	m.hostIdentity = id
}

// HostIdentity returns the identity the Manager was constructed with
// (nil if SetHostIdentity was never called). Used by tests and by the
// daemon's start-up self-check.
func (m *Manager) HostIdentity() *age.X25519Identity { return m.hostIdentity }

// ErrNoHostKey is returned when a WakeRequest carries SealedEnvEntries
// but the Manager was not configured with a host identity. Surface this
// to schedd so the wake fails fast — never silently drop the ciphertext
// or accept-and-discard the plaintext.
var ErrNoHostKey = errors.New("fcvm: host identity not loaded")

// jsonMarshalEnvelope re-marshals the unsealed Envelope to canonical JSON.
// Lives in manager.go (not secretbox) because it's part of the staging
// step, not the seal/open API surface.
func jsonMarshalEnvelope(e secretbox.Envelope) ([]byte, error) {
	return json.Marshal(e)
}

// stageSecretsEnv delegates to the VMM's loopback-mount write. The Manager
// holds no mount logic of its own — the VMM owns the chroot root + instance
// layout (JailerVMM) or, in tests, a stub that writes the file directly.
func (m *Manager) stageSecretsEnv(instance string, jsonBlob []byte) error {
	return m.vmm.StageSecretsEnv(instance, jsonBlob)
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
	// ExportDir, if non-empty, marks this instance as a builder VM (M6).
	// vmmd's Manager.DestroyWithExport waits for exit, captures the exit code,
	// and copies build artifacts (build-done.json + /build/out/*) into this host
	// directory. App VMs leave it empty.
	ExportDir string
	// SealedEnvEntries are the per-key ciphertext rows from `app_secrets`
	// the caller wants loaded into the guest's env (spec §11/G2). Each entry is
	// sealed independently by apid via pkg/secretbox.SealOne against the host
	// X25519 recipient; vmmd unseals each, merges into an envelope, and writes
	// /etc/faas/secrets.env on drive1. Empty slice = no file written.
	//
	// Per-key (rather than one combined envelope) because that's how apid
	// already persists them — the wire stays narrow and unseal work scales with
	// the per-app quota (≤100 keys at Scale), not with arbitrary blob lengths.
	//
	// The plaintext is held ONLY in memory by the manager at this point — the
	// Manager is the unseal-and-forget boundary. It is never logged, never
	// persisted, never returned to any caller.
	SealedEnvEntries []SealedEnvEntry
}

// SealedEnvEntry is one (key, ciphertext) pair as stored in app_secrets. The
// key is the env-var name; the ciphertext is sealed under the host age
// recipient by apid. vmmd merges all entries into the single envelope file.
type SealedEnvEntry struct {
	Key        string
	Ciphertext []byte
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
	// ExportDir is non-empty for builder VMs. See WakeRequest.
	ExportDir string
	// SealedEnvEntries is forwarded to WakeRequest for staging onto drive1
	// (spec §11/G2). Empty slice = no secrets file written.
	SealedEnvEntries []SealedEnvEntry
}

// ColdBoot boots an instance from rootfs with no snapshot. It is Wake with a nil
// snapshot.
func (m *Manager) ColdBoot(ctx context.Context, req ColdBootRequest) (*Instance, error) {
	return m.Wake(ctx, WakeRequest{
		Instance: req.Instance, BasePath: req.BasePath, LayerPath: req.LayerPath,
		VcpuCount: req.VcpuCount, MemSizeMiB: req.MemSizeMiB,
		EgressMbit: req.EgressMbit, Snapshot: nil,
		ExportDir: req.ExportDir, SealedEnvEntries: req.SealedEnvEntries,
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
	// Spec §7 conntrack cap (ADR-018 deferral). Platform-wide constant;
	// not propagated through vmmd gRPC because every instance sees the
	// same value (the failure mode is host-table exhaustion, shared).
	// netns.Config omits the rule when ConntrackCap <= 0 so a vmmd that
	// hasn't been rebuilt still wakes cleanly.
	nc.ConntrackCap = m.conntrackCap

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

	// G2: stage sealed env → unseal each entry → merge into envelope →
	// loopback-mounted write → umount. The Manager is the unseal point
	// (holds host.age). We refuse the request if any sealed blob was
	// supplied without a key configured — silent drop would mean plaintext
	// ciphertext never reaches the guest and the caller's "wake succeeded"
	// hides a missing secret.
	if len(req.SealedEnvEntries) > 0 {
		if m.hostIdentity == nil {
			return nil, fmt.Errorf("wake %s: %w", req.Instance, ErrNoHostKey)
		}
		// We loop-and-merge rather than unseal-into-buf because each entry
		// is a sealed full envelope (per-key rows). That's the natural shape
		// coming from apid's per-row upserts.
		merged := secretbox.Envelope{}
		for _, e := range req.SealedEnvEntries {
			inner, err := secretbox.Open(m.hostIdentity, e.Ciphertext)
			if err != nil {
				return nil, fmt.Errorf("wake %s: open sealed env[%s]: %w",
					req.Instance, logsanitize.Field(e.Key), err)
			}
			for k, v := range inner {
				// Last write wins on key collision. apid upserts on a single
				// row at a time, so collisions can only happen across wake
				// scheduling — meaning a stale row got in; the newer one is
				// the truth.
				merged[k] = v
			}
		}
		// Re-marshal as canonical JSON so guest-init reads the same envelope
		// shape secretbox.Open returns. The plaintext never escapes into any
		// log line — only the size and key count are observable above.
		blob, err := jsonMarshalEnvelope(merged)
		if err != nil {
			return nil, fmt.Errorf("wake %s: marshal envelope: %w", req.Instance, err)
		}
		if err := m.stageSecretsEnv(req.Instance, blob); err != nil {
			return nil, fmt.Errorf("wake %s: stage secrets.env: %w", req.Instance, err)
		}
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
	if req.ExportDir != "" {
		m.exportDirs[req.Instance] = req.ExportDir
	}
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
			VMStatePath: req.Snapshot.VMStatePath,
			// #96 / ADR-025 axis 2: thread the canonical storage key the
			// scheduler populated into WakeRequest.Snapshot. The VMM
			// resolves it through the StorageBackend before staging.
			StorageKey: req.Snapshot.StorageKey,
			Tap:        nc.Tap,
			// The restored VM re-reads kernel + drives under the chroot
			// basenames; Park→Kill erased the previous chroot, so hand the
			// Manager.ColdBoot equivalents back to the VMM to re-stage.
			KernelPath: m.paths.Kernel,
			BasePath:   req.BasePath,
			LayerPath:  req.LayerPath,
			// ADR-022: same vsock device the cold-boot path attaches, derived
			// from the lease's slot so the guest's listener is reachable at a
			// globally unique guest_cid.
			VsockDevice: NewVsockDevice(lease.Slot),
		}
		if rErr := m.vmm.Restore(ctx, lease, rs); rErr == nil {
			return WakeRestore, nil
		} else {
			// Fall back to cold boot into the same netns; kill any half-restored VM.
			// The wrapped rErr names the failure mode (vsock dial timeout vs
			// ack-nack vs /snapshot/load failure) so the operator doesn't have
			// to dig through vmm.go to find out why the resume hook fired.
			m.log.Warn("restore failed, falling back to cold boot",
				"instance", req.Instance,
				"err", rErr,
				"slot", lease.Slot)
			m.metrics.ObserveFallback()
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
	if err := m.vmm.Boot(ctx, lease, BuildColdBootConfig(spec, lease.Slot)); err != nil {
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
// unknown instance is a no-op (already gone). App-VM callers use this; builder
// VMs use DestroyWithExport to surface the build's exit code and copy out
// produced artifacts.
func (m *Manager) Destroy(ctx context.Context, instance string) error {
	_, err := m.DestroyWithExport(ctx, instance, "")
	return err
}

// DestroyWithExport is the builder-VM teardown. It blocks until the
// firecracker child exits, captures the exit code, and copies build artifacts
// into exportDir (loopback-mounted from the chroot). See
// pkg/fcvm/vmm.go::DestroyWithExport for the full contract.
//
// Returns the captured exit code (0 for app VMs / unknown instances). Like
// Destroy, it tears down network + lease on the success path; on failure it
// still runs cleanup (invariant §6.2-4/5).
func (m *Manager) DestroyWithExport(ctx context.Context, instance, exportDir string) (int, error) {
	m.mu.Lock()
	inst, ok := m.live[instance]
	if ok {
		delete(m.live, instance)
	}
	m.mu.Unlock()
	if !ok {
		// Already gone — still safe to export (idempotent), and the exit code
		// is meaningless here.
		if exportDir != "" {
			_ = m.vmm // touch nothing; vmmd's recursion handles unknown
		}
		code, err := m.vmm.DestroyWithExport(ctx, Lease{Instance: instance}, exportDir)
		return code, err
	}
	code, err := m.vmm.DestroyWithExport(ctx, inst.Lease, exportDir)
	// Teardown uses a context detached from the caller's: if the caller's ctx
	// has already expired (test deadline, caller gave up), we still owe the
	// invariant §6.2-4/5 cleanup. Without this, a 30s test deadline firing
	// mid-Destroy leaves the netns + cgroup on disk; observed on the Lima
	// arm64 metal path where nested-KVM cold boot can take >25s. The vmm wait
	// above used the original ctx and is allowed to be cancelled by it.
	m.cleanup(context.WithoutCancel(ctx), inst.Lease, inst.Net)
	m.mu.Lock()
	delete(m.exportDirs, instance)
	m.mu.Unlock()
	if err != nil {
		return code, err
	}
	m.log.Info("destroyed", "instance", instance, "exit_code", code)
	return code, nil
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

// ExportDirFor returns the host export dir registered for an instance at
// Wake/ColdBoot time (M6 builder VMs only). Returns "" for unknown or app VMs.
// The caller MUST treat the returned path as opaque — it's a host directory
// the goroutine that called Wake chose, and it survives only until the
// instance is removed (DestroyWithExport).
func (m *Manager) ExportDirFor(instance string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.exportDirs[instance]
}

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
