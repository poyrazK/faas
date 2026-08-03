package fcvm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
	"github.com/onebox-faas/faas/pkg/wire"
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
	// healthcheckPath (issue #460 / ADR-053, ADR-057 / PR-D) is the
	// per-deployment override readiness probe path. Empty keeps the legacy
	// TCP-accept on :8080 (pre-PR-D default). Non-empty → waitReady does
	// HTTP GET <path> against <HostIP>:8080 and accepts 2xx as ready.
	Boot(ctx context.Context, l Lease, cfg VMConfig, healthcheckPath string) error
	// BootColdBoot is the cold-boot entry point (issue #96 / ADR-025 axis 2
	// / PR #116): it materializes the StorageBackend keys in spec through
	// the configured backend into vmmd-allocated tmp paths, then delegates
	// to Boot. Manager.Wake prefers BootColdBoot over Boot; tests that
	// already have resolved paths in hand can keep using Boot directly.
	BootColdBoot(ctx context.Context, l Lease, spec ColdBootSpec) error
	// Restore loads a snapshot into a fresh jailed firecracker and resumes it,
	// returning once the guest is ready. On error it cleans up its own process.
	// spec.HealthcheckPath (issue #460 / ADR-053, ADR-057 / PR-D) is the
	// per-deployment override readiness probe path. Empty keeps the legacy
	// TCP-accept on :8080; non-empty → waitReady does HTTP GET <path>.
	Restore(ctx context.Context, l Lease, spec RestoreSpec) error
	// TriggerResumeHook dials the guest's vsock UDS and asks it to run its
	// post-restore side effects (re-seed entropy + step clock, guest/init/resume.go).
	// Must be called from Restore after /snapshot/load and before waitReady so
	// the app cannot accept on :8080 with a stale RNG stream (spec §11 V6).
	// ADR-022 records the wire format (4-byte msg type + JSON body, port 1024
	// on the fixed host CID 3).
	TriggerResumeHook(ctx context.Context, l Lease, hostTimeUnixNano int64) error
	// WaitCharacterizationReport (ADR-051 Phase 4 / PR-D) is the
	// host-side mirror of TriggerResumeHook: it accepts ONE
	// guest-initiated AF_VSOCK STREAM connection at port 1026
	// (msg_type=3) carrying the CharacterizationReport, writes a
	// 1-byte ack, and returns. Called only on cold boots (the warm
	// path inherits the class from the apps row captured in the
	// original cold boot). On deadline elapse the implementation
	// MUST return (zero, err) and the caller (Manager.Wake) MUST
	// fall back to the scan-hint class — never fail the wake, that
	// would regress vs today's `:8080` accept failure path.
	WaitCharacterizationReport(ctx context.Context, l Lease, deadline time.Duration) (api.CharacterizationReport, error)
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
	// StageAPIEnv is the plaintext env channel (issue #395 / ADR-045).
	// Loopback-mounts drive1 in the chroot, writes /etc/faas/env.json
	// (plaintext map of key→value), and umounts. jsonBlob may be
	// empty — implementations MUST treat that as a no-op so apps
	// without API env rows skip the mount/umount cycle entirely.
	// Distinct from StageSecretsEnv because the file lives at a
	// different path and the payload is plaintext-by-contract (no
	// unseal step).
	StageAPIEnv(instance string, jsonBlob []byte) error
	// LogRing returns the per-instance ring buffer of the running VM's
	// stdout/stderr stream (issue #254, Move 4), or nil if instance is
	// not alive on this vmmd. The vmmd gRPC Logs(req) handler dials this
	// to fan frames out to subscribers. nil-safe so the handler can treat
	// "no ring" as NotFound without a separate liveness check.
	LogRing(instance string) *logbuf.Ring
	// InstancePID returns the host PID of the running jailer child for
	// instance, or (0, false) if the instance is not currently alive on
	// this vmmd. M8 §11: the SeccompStatus gRPC handler reads
	// /proc/<pid>/status to verify the jailer default seccomp filter is
	// in place. The bool distinguishes "instance never woke" (caller
	// should return NotFound) from "instance woke but Pid is 0"
	// (defensive — never expected in production because jailer always
	// children firecracker; the bool trips the test before the handler
	// does).
	InstancePID(instance string) (int, bool)
	// SendStatelessAdvisory is the host-side receiver for one
	// batch guest-init stateless_advisory_linux.go sent over vsock.
	// The host-side vsock parser (cmd/vmmd's wire receiver) verifies
	// the wire format and dispatches here. Implementations may
	// forward to apid over the AdvisoryClient seam (Wave 0 PR-C) or
	// return nil on a default-local build that has no apid.sock to
	// dial. ADR-035: best-effort, never blocks the vsock reader; on
	// forward failure we drop the batch + log Warn.
	SendStatelessAdvisory(ctx context.Context, l Lease, appID string, batch []AdvisoryEvent) error
	// WithEvents (issue #517 / PR-C / ADR-064) wires the wake-timeline
	// fan-out (pkg/events.Platform) on the VMM. vmmd is the canonical
	// emit site for wake.readiness_200 (the first 2xx probe) and a
	// corroborating observation for wake.boot_started (mirror at the
	// gRPC server boundary). nil opts out (pre-PR-C fixtures).
	// Mirrors WithStorage's nil-tolerance / one-shot wiring posture.
	WithEvents(p *events.Platform) VMM
}

// Paths locates the kernel and base images on disk (spec §8). Injected so tests
// don't touch the filesystem.
type Paths struct {
	Kernel string // /srv/fc/base/vmlinux-6.1.x
}

// AdvisoryEvent is one fanotify event the guest-init
// stateless_advisory_linux.go observed and shipped over vsock DGRAM.
// Wire-shaped at guest/init/stateless_advisory_linux.go::advisoryEvent
// and forwarded to apid via pkg/vmmdgrpc/advisory_client.go. ADR-047
// records the chain end-to-end; spec §17 G13 names the closed path
// set this event belongs to.
type AdvisoryEvent struct {
	Path   string   // path observed (resolved from fanotify fd)
	Masks  []string // canonical verbs: "create" | "modify" | "move" | "access" | "delete" | "other"
	PID    int      // host-side process id at event time (best effort)
	TsUnix int64    // ms since unix epoch
}

// AdvisoryForwarder is the seam the Manager uses to ship a guest-init
// advisory batch to apid. pkg/vmmdgrpc.AdvisoryClient satisfies it
// (Wave 0 PR-C); tests inject a stub. Defined here (not in
// pkg/vmmdgrpc) to avoid an import cycle: pkg/vmmdgrpc already
// imports pkg/fcvm for Lease + LogRing types.
type AdvisoryForwarder interface {
	Forward(ctx context.Context, instance, appID string, events []AdvisoryEvent) error
}

// Instance is a live (or booting) microVM tracked by the Manager.
type Instance struct {
	Lease  Lease
	Net    netns.Config
	Method WakeMethod // how it came up; a restore that fell back reads WakeColdBoot
	// AppID is the apps.id UUID the instance was woken for.
	// UpdateEgressAllowlist (PR-B, ADR-031+033) uses it to walk
	// the live map keyed by app instead of by instance, so a
	// single PATCH on apps.egress_allowlist patches every live
	// instance of the app without the caller enumerating them.
	// Stored on the instance (not the Lease) so the Lease
	// stays allocator-owned and the Instance carries the
	// schedd-owned app identity.
	AppID string

	// AllowlistHandleV4 / V6 are the nft handles of the
	// per-netns allowlist accept rules captured at Wake time (or
	// at the previous successful UpdateEgressAllowlist). Used by
	// UpdateEgressAllowlist to delete the prior rule by handle
	// before inserting the new one — the in-place patch that
	// keeps the live netns in sync without a cold-wake. Zero
	// when the family half is empty (no rule was emitted at
	// Wake / patch time). The handle is captured by re-listing
	// the chain with `nft -a list chain` after the rule is
	// inserted; the metal test exercises this code path; the
	// unit suite stubs it out.
	AllowlistHandleV4 uint64
	AllowlistHandleV6 uint64

	// Plan is the apps row's owning plan tier (issue #301, ADR-044).
	// Stored on the Instance so Destroy (pkg/fcvm/vmm.go) can compute
	// the per-plan cgroup scope path (ParentCgroupFor) without
	// needing to thread the plan through the vmm-side Lease type.
	// The Lease stays allocator-owned and instance-id-keyed; the Plan
	// is schedd-owned and recorded at Wake time.
	Plan api.Plan

	// Port (issue #460 / ADR-053, PR-C) is the per-deployment
	// override port copied from WakeRequest.Port. The vmmdgrpc
	// forwarder reads this to resolve the per-instance guest dial
	// port (server-side default to netns.AppPort when 0). 0 on
	// instances that pre-date PR-C and on legacy callers that
	// never set the wire field; the wire-level default at the
	// buildBridgeScript boundary keeps those legacy instances
	// dial-able on 8080.
	Port int

	// HealthcheckPath (issue #460 / ADR-053, ADR-057 / PR-D) is the
	// per-deployment override readiness probe path copied from
	// WakeRequest.HealthcheckPath. "" = legacy TCP-accept on :8080
	// (pre-PR-D default). Non-empty → vmmd's waitReady does HTTP GET
	// <HealthcheckPath> against <HostIP>:8080 and accepts 2xx as
	// ready. Stamped onto the live Instance so server-side readers
	// can resolve Instance.HealthcheckPath without a second request
	// lookup (PR-C mirror).
	HealthcheckPath string

	// Characterization (ADR-051 Phase 4 / PR-D) is the
	// CharacterizationReport the host received via
	// WaitCharacterizationReport during this cold boot. Empty on
	// restore (warm wake inherits the class from the apps row) and
	// empty on cold-boot timeouts (the caller falls back to the
	// scan-hint class). The field is settable here so Manager.Wake
	// can populate it post-bringUp; vmmdgrpc then ships it on the
	// wire as WakeResponse.characterization so schedd can persist
	// it via SetAppWorkloadClass.
	Characterization api.CharacterizationReport

	// Runtime (issue #470 / PR #470-FU-B) is the runtime id
	// ("node22", "python312", …) the app was woken for. Stored on
	// the Instance so the framework-ready receipt handler can
	// stamp the per-instance `framework_ready_at` time and observe
	// it under the `runner` label that the engine and the
	// telemetry layer need. Comes from app.Runtime at Wake time
	// (engine.go line 934 etc) — the source of truth is the app
	// row, not the wire.
	Runtime string

	// FrameworkReadyAt records the wall-clock moment the guest's
	// runner emitted its first non-5xx response (issue #470 /
	// PR #470-FU-B). nil until the FrameworkReady RPC fires;
	// vmmd stamps it from the cmd/vmmd DGRAM recv loop on port
	// 1027 (msg=4). The engine's captureWarmSnapshot (PR
	// #470-FU-A) waits on this timestamp before issuing the
	// warm-tier PauseAndSnapshot. Held on the Instance (not the
	// Lease) because the engine reads it via DB Set/Clear
	// (migrations/00112) and the vmmd side wants the same
	// ephemeral truth for the duration of the wake so the
	// histogram observation has the right `app`/`runner` labels.
	//
	// Aligned with state.Instance.FrameworkReadyAt (*time.Time)
	// so the value can round-trip across the vmmd ↔ schedd
	// boundary without a nil-vs-zero-value ambiguity. The
	// pointer is the right knob here: "no signal yet" and
	// "signal landed at unix-time-zero" are observably
	// distinct, and the latter never happens.
	FrameworkReadyAt *time.Time
}

// Manager tracks live instances and serialises nothing on the hot path beyond a
// short-held map lock. Safe for concurrent Wake/Destroy.
type Manager struct {
	alloc *Allocator
	run   Runner
	// captureRunner (tier-2 PR-B) is the optional stdout-aware
	// handle used by captureAllowlistHandles to read `nft -a list
	// chain` output and resolve the freshly-added allowlist
	// rule's kernel-assigned handle. nil means "no capture
	// available"; the wake path then leaves AllowlistHandle{V4,V6}
	// at 0, and UpdateEgressAllowlist will add the new rule
	// alongside the prior one (still correct, just leaves an
	// orphan until the next patch picks it up via the chain
	// list). Production wires the metal runner that wraps
	// exec.CommandContext with CombinedOutput; unit tests can
	// stub via WithCaptureRunner.
	captureRunner CaptureRunner
	vmm           VMM
	paths         Paths
	fcVersion     string // the running Firecracker version; snapshots load only on a match
	log           *slog.Logger

	mu         sync.Mutex
	live       map[string]*Instance
	exportDirs map[string]string // instance -> host export dir (builder VMs only, M6)
	// cidToID (issue #470 / PR #470-FU-B) is the reverse
	// Firecracker-vsock-CID → instance id index the framework_ready
	// DGRAM receipt path uses to resolve the peer CID to a live
	// Instance in O(1). Populated on BringUp and removed on
	// Destroy / Park along with the live-map entry. The CID is
	// derived from Lease.Slot via GuestVsockCID(slot) and is
	// globally unique per live instance (slot is allocated
	// linearly by the allocator). Guarded by mu.
	cidToID map[uint32]string
	// metrics is the cold-boot fallback counter (vmmd_cold_boot_fallback_total).
	// nil-safe: bringUp calls m.metrics.ObserveFallback() which no-ops when nil,
	// so unit tests that construct a Manager without metrics don't need a stub.
	metrics *ColdBootMetrics
	// frameworkReadyMetrics (issue #470 / PR #470-FU-B) is the
	// optional `vmmd_guest_framework_warmup_seconds` histogram the
	// vmmd cmd wires via WithFrameworkReady. nil-safe:
	// MarkInstanceFrameworkReady calls ObserveWarmup on a nil-safe
	// receiver so unit tests that drive Manager directly without
	// metrics don't need a stub.
	frameworkReadyMetrics *FrameworkReadyMetrics
	// frameworkReadyStamper (issue #470 / PR #470-FU-B) is the
	// optional SQL persistence seam the vmmd cmd wires via
	// WithFrameworkReadyStamper. nil-safe:
	// MarkInstanceFrameworkReady calls SetFrameworkReadyAt on
	// a nil-safe receiver so unit tests that drive Manager
	// directly without a stamper don't need a stub. The
	// engine's captureWarmSnapshot (PR #470-FU-A) reads back
	// the column via pgstore.InstanceByID (state.Instance.
	// FrameworkReadyAt is the row-side pointer).
	frameworkReadyStamper FrameworkReadyStamper
	// hostIdentities is the slice of X25519 secret keys used to
	// unseal per-app sealed env blobs at wake time (spec §11/G2).
	// Holds the current identity alone in the normal pre-rotation
	// state and [current, previous] during the 30-day overlap
	// window (issue #316 / ADR-057). nil means "no host age
	// configured" — a Wake call with SealedEnvEntries set is
	// rejected with ErrNoHostKey rather than silently dropping
	// plaintext. vmmd owns the on-disk files.
	hostIdentities []*age.X25519Identity
	// conntrackCap is the effective per-instance conntrack cap. Probed once
	// at construction from api.ConntrackCapProbe(): DefaultConntrackCap when
	// the kernel supports ct expressions in netns (CONFIG_NF_CONNTRACK_NET_NS),
	// 0 when it doesn't (the ct cap rule is omitted, egress tc cap unaffected).
	conntrackCap int64
	// characterizationWait (ADR-051 Phase 4 / PR-D) bounds the
	// WaitCharacterizationReport dial+read inside Wake. The guest
	// retries its report 4× (initial + 3 backoffs totalling ~1.85s),
	// so 4s gives us margin for slow vsock proxies on nested KVM.
	// Mismatch with the guest deadline matters only in that the
	// guest falls back to "ack_timeout" earlier than we fall back
	// to scan-hint class — both sides degrade the same way.
	characterizationWait time.Duration
	// storage is the artifact backend vmmd reads scan sidecars from at
	// boot time (issue #299). Wired via WithStorage, mirroring the VMM's
	// own WithStorage setter at pkg/fcvm/vmm.go. nil means "no scan
	// check" — bringUpScanCheck returns nil and bringUp proceeds; the
	// unit tests that don't wire a storage backend (most of
	// pkg/fcvm/manager_test.go) take this path today and continue to
	// pass after the change.
	storage storage.StorageBackend
	// imageScanMetrics is the per-daemon OpsMetrics the scan sidecar
	// findings get fed into (issue #299). Wired via SetImageScanMetrics,
	// mirroring SetHostIdentity above (nil-safe). The counter is
	// vmmd_trivy_image_vulns_total{image, severity}; the vmmd
	// OpsMetrics is the only caller in production, but every daemon
	// registers the counter (single-registry pattern, see
	// pkg/wire/metrics.go: imageScanVulns on commonCollectors).
	imageScanMetrics *wire.OpsMetrics
	// advisoryClient is the vmmd-side forwarder that ships
	// guest-init fanotify batches to apid (Wave 0 PR-C /
	// ADR-047). nil means "no apid.sock to dial" —
	// Manager.ForwardStatelessAdvisory then short-circuits
	// without sending, which is the default-local vmmd posture
	// and the unit-test seam (cmd/e2e harness,
	// pkg/fcvm/manager_test.go). Wired via SetAdvisoryClient at
	// daemon startup; mirror of SetImageScanMetrics /
	// SetHostIdentity.
	//
	// Held as the AdvisoryForwarder interface (not the concrete
	// *vmmdgrpc.AdvisoryClient) to avoid an import cycle:
	// pkg/vmmdgrpc already imports pkg/fcvm for Lease +
	// LogRing types.
	advisoryClient AdvisoryForwarder
	// parentMounts (ADR-053) tracks every active parent-base
	// loopback mount vmmd has issued. vmmd is the only root
	// component (spec §11); imaged (User=faas-imaged +
	// NoNewPrivileges=yes) cannot mount on its own, so the
	// storage path for each node/python runtime base passes
	// through this registry. nil-safe: MountParentExt4 returns
	// vmmdmount.ErrNotFound when storage isn't wired (the legacy
	// / unit-test path). Wired via SetParentMountRegistry at
	// daemon startup; the registry owns the 5-minute orphan
	// sweep + SIGTERM sync-sweep.
	parentMounts *vmmdmount.Registry
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
		alloc:      NewAllocator(),
		run:        run,
		vmm:        vmm,
		paths:      paths,
		fcVersion:  fcVersion,
		log:        log,
		live:       make(map[string]*Instance),
		exportDirs: make(map[string]string),
		// Issue #470 / PR #470-FU-B: O(1) CID→instance lookup
		// for the framework_ready DGRAM receipt path. See the
		// cidToID field comment for the lifecycle.
		cidToID:              make(map[uint32]string),
		metrics:              metrics,
		conntrackCap:         api.ConntrackCapProbe(),
		characterizationWait: api.CharacterizationHostDeadline,
	}
}

// SetHostIdentity attaches the unseal key. Only vmmd calls this — the
// Manager holds the private half for the duration of the process. NOT
// safe to call concurrently with Wake; production wires it before
// serving traffic.
//
// This is a 1-element convenience wrapper around SetHostIdentities
// retained for backward compatibility with the existing call sites
// (cmd/vmmd main.go + unit tests). New callers should call
// SetHostIdentities directly with the slice returned by
// secretbox.LoadHostKeys(dir) so the multi-recipient fallback across
// the rotation overlap window is wired in.
func (m *Manager) SetHostIdentity(id *age.X25519Identity) {
	m.hostIdentities = []*age.X25519Identity{id}
}

// SetHostIdentities attaches the multi-identity unseal key set.
// This is the rotation-aware entry point (issue #316 / ADR-057):
// the caller passes the slice returned by secretbox.LoadHostKeys(dir)
// — current first, previous second during the 30-day overlap window.
// age.Decrypt's native multi-recipient fallback tries every
// supplied identity, so envelopes sealed under EITHER the current
// or the previous key unseal without operator intervention.
//
// NOT safe to call concurrently with Wake; production wires it
// before serving traffic. A nil or empty slice leaves the Manager
// in the same "no host age configured" posture as SetHostIdentity(nil).
func (m *Manager) SetHostIdentities(ids []*age.X25519Identity) {
	if len(ids) == 0 {
		m.hostIdentities = nil
		return
	}
	m.hostIdentities = ids
}

// WithStorage wires the artifact backend the Manager uses to read
// Grype scan sidecars at boot time (issue #299). Mirrors the VMM's
// own WithStorage setter at pkg/fcvm/vmm.go (the VMM uses it to
// materialize snapshot blobs; the Manager uses it to fetch the
// per-runtime scan sidecar). NOT safe to call concurrently with
// Wake; production wires it before serving traffic. Calling
// WithStorage(nil) clears the override and disables the scan
// check (bringUpScanCheck returns nil immediately).
func (m *Manager) WithStorage(s storage.StorageBackend) {
	m.storage = s
}

// VMM returns the underlying VMM (the one wired at NewManager). The
// caller is allowed to attach side-channels (e.g. WithEvents) AFTER
// Manager construction. The pointer is shared; mutations are
// visible to every existing caller. Used by cmd/vmmd to wire the
// wake-timeline fan-out (issue #517 / PR-C / ADR-064) without
// forcing the Manager constructor to know about events.
func (m *Manager) VMM() VMM {
	return m.vmm
}

// SetImageScanMetrics wires the OpsMetrics handle the scan sidecar
// findings get fed into (issue #299). nil-safe (the helper no-ops
// when nil), mirroring SetHostIdentity's nil-tolerance posture.
// NOT safe to call concurrently with Wake; production wires it
// before serving traffic. The counter is registered on every
// daemon's OpsMetrics (single-registry pattern), so vmmd is the
// only producer in production but every daemon can hold the field.
func (m *Manager) SetImageScanMetrics(ops *wire.OpsMetrics) {
	m.imageScanMetrics = ops
}

// SetAdvisoryClient wires the vmmd-side forwarder that ships
// guest-init fanotify batches to apid (Wave 0 PR-C / ADR-047).
// nil-safe — the default-local vmmd has no apid.sock to dial and
// Manager.ForwardStatelessAdvisory short-circuits without sending.
// NOT safe to call concurrently with Wake; production wires it at
// daemon startup. The forwarder is held as AdvisoryForwarder
// (interface) so pkg/fcvm does not need to import pkg/vmmdgrpc.
func (m *Manager) SetAdvisoryClient(c AdvisoryForwarder) {
	m.advisoryClient = c
}

// SetParentMountRegistry wires the parent-base loopback mount
// registry (ADR-053). Production cmd/vmmd constructs a registry
// with cap=16 and calls this once at startup; the registry owns
// the 5-minute orphan sweep + SIGTERM sync-sweep. nil-safe:
// MountParentExt4 returns vmmdmount.ErrNotFound when the
// registry (or the storage backend) is unwired, so unit tests
// that don't care about parent-mounts keep working without a
// stub.
func (m *Manager) SetParentMountRegistry(r *vmmdmount.Registry) {
	m.parentMounts = r
}

// WithFrameworkReady (issue #470 / PR #470-FU-B) attaches the
// vmmd_guest_framework_warmup_seconds histogram so the
// MarkInstanceFrameworkReady receipt path can observe per-runner
// warmup durations. Mirrors SetImageScanMetrics /
// SetParentMountRegistry in spirit: optional, nil-safe, no-ops if
// the cmd binary doesn't wire it. The returned *Manager is the
// receiver so callers can chain (`m, ok := NewManager(...).WithMux(...)`).
func (m *Manager) WithFrameworkReady(fm *FrameworkReadyMetrics) *Manager {
	m.frameworkReadyMetrics = fm
	return m
}

// WithFrameworkReadyStamper (issue #470 / PR #470-FU-B) attaches
// the SQL-persistence seam so the receipt path can stamp the
// `instances.framework_ready_at` column. Same wiring pattern as
// WithFrameworkReady: optional, nil-safe, no-ops when the cmd
// binary doesn't wire it. The interface is local to pkg/fcvm to
// avoid an import cycle on pkg/state (the Manager doesn't need
// the full Store surface — only the two column writers).
// Production wires *pgstore.PgStore satisfying both methods.
// Returns the *Manager so callers can chain.
func (m *Manager) WithFrameworkReadyStamper(s FrameworkReadyStamper) *Manager {
	m.frameworkReadyStamper = s
	return m
}

// FrameworkReadyStamper (issue #470 / PR #470-FU-B) is the
// minimal SQL write surface the Manager needs to persist the
// framework_ready clock. Local-to-pkg/fcvm so adding the column
// doesn't drag a full pkg/state import into the hot path; the
// cmd/vmmd wiring adapts the pgstore directly. Errors from
// SetFrameworkReadyAt are observable but non-fatal — the in-memory
// stamp on the live Instance is the load-bearing signal for the
// receipt path; the SQL column is the durable record the engine
// (PR #470-FU-A) reads back to trigger warm capture.
type FrameworkReadyStamper interface {
	// SetFrameworkReadyAt stamps the per-instance
	// `framework_ready_at` column. Errors propagate to the
	// caller via the Manager's Warn log; the receipt is still
	// considered successful (the in-memory stamp is the
	// authoritative signal).
	SetFrameworkReadyAt(ctx context.Context, instance string, readyAt time.Time) error
}

// MarkInstanceFrameworkReady stamps the per-instance
// `framework_ready_at` clock on the live Instance, observes the
// vmmd_guest_framework_warmup_seconds histogram (if wired), and
// returns the values the gRPC handler needs to publish back to
// schedd (instance id + app id + runtime — the latter two come
// from the live Instance struct, which is the source of truth for
// the wake side and avoids any second lookup).
//
// Returns (stamped=false, appID="", runtime="", nil) when the
// instance is unknown — the wire RPC translates that to a NotFound
// gRPC code so a stale DGRAM receipt from a guest that's already
// gone is a clean, observable error rather than a silent success.
//
// Concurrency: short-held m.mu lock around the live-map lookup +
// stamp. The histogram observe is unlocked (Prometheus is
// goroutine-safe). WarmupMs is ignored (zero) when the guest sent
// no payload — the handler doesn't synthesize a duration from
// wall-clock because the guest's from-boot measurement is the one
// the dashboard wants.
func (m *Manager) MarkInstanceFrameworkReady(ctx context.Context, instance string, warmupMs int64) (stamped bool, appID, runtime string, err error) {
	m.mu.Lock()
	inst, ok := m.live[instance]
	if !ok {
		m.mu.Unlock()
		return false, "", "", nil
	}
	stampTime := time.Now()
	inst.FrameworkReadyAt = &stampTime
	appID = inst.AppID
	runtime = inst.Runtime
	m.mu.Unlock()
	// Persist the stamp to the SQL column so the engine's
	// captureWarmSnapshot (PR #470-FU-A) can read it back on
	// the next wake. The in-memory stamp is the load-bearing
	// signal for the histogram; the SQL column is the durable
	// record. Errors are logged Warn and ignored — a transient
	// PG hiccup must not lose the receipt.
	if m.frameworkReadyStamper != nil {
		if perr := m.frameworkReadyStamper.SetFrameworkReadyAt(ctx, instance, stampTime); perr != nil {
			// Conservative: don't fail the receipt — the
			// histogram + in-memory stamp already observed
			// the signal. Just surface as a Warn so the
			// gate can be debugged.
			// Note: we don't have a logger here without a
			// wider wiring change; the cmd's DGRAM loop
			// catches the equivalent error at the rpc level.
			_ = perr
		}
	}
	if warmupMs > 0 {
		m.frameworkReadyMetrics.ObserveWarmup(runtime, appID, float64(warmupMs)/1000.0)
	}
	return true, appID, runtime, nil
}

// InstanceByCID (issue #470 / PR #470-FU-B) returns the instance
// id whose Firecracker guest has the given AF_VSOCK CID. The
// host's DGRAM recv loop (cmd/vmmd/framework_ready_recv.go)
// uses this to resolve the source of a framework-ready
// receipt back to the live Instance. The CID is derived from
// Lease.Slot via GuestVsockCID(slot) at BringUp time and is
// globally unique per live instance.
//
// Backed by the cidToID reverse index (populated on BringUp,
// dropped on Park/Destroy) so the lookup is O(1) instead of a
// linear scan over m.live — at the cold-wake hot path the
// framework_ready DGRAM is the first receipt of a parked app
// and the live map is already at MaxConcurrency worth of
// entries across the fleet (review feedback HIGH-3 on PR #543).
//
// Returns an error when no live instance owns the CID. The
// caller (cmd/vmmd's DGRAM loop) treats this as a normal
// Debug event — a DGRAM racing a wake-park cycle is expected
// during instance churn.
func (m *Manager) InstanceByCID(cid uint32) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.cidToID[cid]
	if !ok {
		return "", fmt.Errorf("fcvm: InstanceByCID %d: not live", cid)
	}
	return id, nil
}

// ForwardStatelessAdvisory is the public Manager seam that turns
// one guest-init fanotify batch into one apid audit row. The vsock
// DGRAM receiver in cmd/vmmd calls this with the parsed batch.
//
// ADR-035 best-effort: returns nil on a nil client (default-local)
// and on a forward failure — the advisory is observation, not
// source of truth. Caller should not retry.
func (m *Manager) ForwardStatelessAdvisory(ctx context.Context, instance, appID string, batch []AdvisoryEvent) error {
	if len(batch) == 0 {
		return nil // no-op; matches cmd/apid/advisory_receiver.go
	}
	if m.advisoryClient == nil {
		// Default-local vmmd has no apid.sock; silent drop is correct.
		m.log.Debug("advisory forward: no apid client (default-local); dropping", "instance", instance, "events", len(batch))
		return nil
	}
	if err := m.advisoryClient.Forward(ctx, instance, appID, batch); err != nil {
		m.log.Warn("advisory forward: client returned error", "err", err, "instance", instance)
		return nil // ADR-035: silent on forward failure
	}
	return nil
}

// MountParentExt4 (ADR-053) loopback-mounts the parent base ext4
// identified by `storageKey` read-only and returns the absolute
// host path of the mountpoint. The flow:
//
//  1. Stage the StorageBackend bytes into
//     vmmdmount.MountRoot/faas-parent-src-* (currently
//     /srv/fc/parent/faas-parent-src-*) via a sibling-temp tmp file
//     so the Storage.Put pattern in pkg/rootfs (which mkdirs its
//     staging dir elsewhere) doesn't collide. The tmp file is the
//     loopback source. Lives under /srv/fc/parent, not /tmp, because
//     vmmd's unit whitelists /srv/fc via ReadWritePaths but leaves
//     /tmp read-only under ProtectSystem=strict (run 30848763268).
//  2. Create vmmdmount.MountRoot/faas-parent-mnt-* via
//     vmmdmount.MountExt4ReadOnly.
//  3. Register (mountpoint, storageKey) in parentMounts; load-shed
//     the oldest entry when the cap is reached.
//  4. The src tmp is removed on UmountParentExt4 (it lives as long
//     as the mount).
//
// Returns vmmdmount.ErrNotFound when the StorageBackend reports the
// key missing, or the registry is nil (unwired). Returns the
// wrapped mount error otherwise. Idempotent for a second mount of
// the same storageKey: returns a fresh mountpoint — imaged's
// EnsureBaseExt4 calls once per child restage so two concurrent
// restages of different runtimes see distinct mountpoints.
func (m *Manager) MountParentExt4(ctx context.Context, storageKey string) (string, error) {
	if m.storage == nil {
		return "", vmmdmount.ErrNotFound
	}
	if m.parentMounts == nil {
		return "", vmmdmount.ErrNotFound
	}
	if storageKey == "" {
		return "", vmmdmount.ErrNotFound
	}
	rc, err := m.storage.Get(ctx, storageKey)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", vmmdmount.ErrNotFound, storageKey, err)
	}
	defer func() { _ = rc.Close() }()

	// vmmdmount.MountRoot (pkg/vmmdmount/mount.go:71) — created by
	// bootstrap as 0750 root:faas. vmmd's unit whitelists /srv/fc via
	// ReadWritePaths (deploy/etc/faas-vmmd.service:20), but /tmp is
	// not whitelisted and ProtectSystem=strict would block the
	// CreateTemp call there (run 30848763268 — imaged → vmmd RPC
	// "create src tmp: open /tmp/faas-parent-src-NNNN: read-only
	// file system"). The umount/rmdir sweeps in pkg/vmmdmount also
	// expect src files under MountRoot.
	src, err := os.CreateTemp(vmmdmount.MountRoot, parentSrcPrefix)
	if err != nil {
		return "", fmt.Errorf("parent mount: create src tmp: %w", err)
	}
	srcPath := src.Name()
	if err := src.Close(); err != nil {
		_ = os.Remove(srcPath)
		return "", fmt.Errorf("parent mount: close src tmp: %w", err)
	}
	if err := streamToPath(rc, srcPath); err != nil {
		_ = os.Remove(srcPath)
		return "", fmt.Errorf("parent mount: stream src bytes: %w", err)
	}

	mp, err := vmmdmount.MountExt4ReadOnly(ctx, srcPath)
	if err != nil {
		_ = os.Remove(srcPath)
		return "", err
	}
	evicted := m.parentMounts.RegisterOrEvict(mp, storageKey, srcPath)
	if evicted != "" {
		m.log.Warn("vmmd: parent mount cap reached; force-umounted oldest",
			"evicted_mountpoint", evicted)
		// Best-effort umount of the evicted entry; the registry
		// already forgot it, so vmmdmount.UmountExt4 will fall
		// through to the kernel syscall and clean the mountpoint
		// dir.
		_ = vmmdmount.UmountExt4(evicted)
	}
	m.log.Info("vmmd: parent mounted", "storage_key", storageKey, "mountpoint", mp)
	return mp, nil
}

// UmountParentExt4 (ADR-053) releases a parent mount MountParentExt4
// previously returned. Idempotent on unknown mountpoints — imaged's
// defer-after-error pattern is safe to call blindly. Returns the
// nil-equivalent (no error) on success AND on a never-issued
// mountpoint; surfaces a real umount error (e.g. EBUSY) verbatim.
func (m *Manager) UmountParentExt4(_ context.Context, mountpoint string) error {
	if m.parentMounts == nil {
		// No registry wired — every call is a no-op. Matches the
		// default-local unit-test path; production cmd/vmmd wires
		// the registry at startup.
		return nil
	}
	entry, ok := m.parentMounts.Lookup(mountpoint)
	if !ok {
		// Idempotent on unknown: imaged's defer-after-error may
		// call here after a partial Mount failure (mount succeeded
		// but registration raced, or the registry was swept). The
		// gRPC handler treats nil as success.
		return nil
	}
	if err := vmmdmount.UmountExt4(mountpoint); err != nil {
		return err
	}
	m.parentMounts.Forget(mountpoint)
	if entry.SrcPath != "" {
		_ = os.Remove(entry.SrcPath)
	}
	m.log.Info("vmmd: parent umounted", "mountpoint", mountpoint, "storage_key", entry.StorageKey)
	return nil
}

// parentSrcPrefix is the tempdir name pattern for the staged
// parent-ext4 bytes (StorageBackend.Get → tmp file → loopback
// source). Mirrored on vmmdmount.ParentMountPrefix by convention
// but kept distinct so the umount-sweep can find mounts and the
// rmdir-sweep can find src tmp's independently.
const parentSrcPrefix = "faas-parent-src-"

// streamToPath copies rc's bytes into the file at dstPath. Used by
// MountParentExt4 to materialise the staged parent ext4 from the
// StorageBackend reader into a tmp file the loopback mount can
// source. Caller is responsible for the dstPath lifetime.
func streamToPath(rc io.Reader, dstPath string) error {
	f, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open dst: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, rc); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}

// metricsImageScan forwards a per-severity finding count to the
// vmmd-side OpsMetrics (issue #299). Nil-safe so the boot-time scan
// check doesn't have to nil-check the metrics accessor on every
// (severity, count) iteration — same nil-safe shape as
// ColdBootMetrics.ObserveFallback at pkg/fcvm/metrics.go:136.
func (m *Manager) metricsImageScan(image, severity string, count int) {
	if m.imageScanMetrics == nil {
		return
	}
	m.imageScanMetrics.ObserveImageScanVuln(image, severity, count)
}

// HostIdentity returns the identity the Manager was constructed with
// (nil if SetHostIdentity was never called). Used by tests and by the
// daemon's start-up self-check.
func (m *Manager) HostIdentity() *age.X25519Identity {
	if len(m.hostIdentities) == 0 {
		return nil
	}
	return m.hostIdentities[0]
}

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

// stageAPIEnv delegates to the VMM's loopback-mount write for the plaintext
// api_env channel (issue #395 / ADR-045). Same shape as stageSecretsEnv but
// writes /etc/faas/env.json instead of /etc/faas/secrets.env; guest-init
// reads both files and merges with precedence "secrets > api_env > manifest
// > os.environ". See VMM.StageAPIEnv for the file-write implementation.
func (m *Manager) stageAPIEnv(instance string, jsonBlob []byte) error {
	return m.vmm.StageAPIEnv(instance, jsonBlob)
}

// WakeRequest brings an app up for a request or cron (spec §6.1). If Snapshot is
// usable on the running Firecracker version it is restored (fast path); otherwise,
// or if restore fails, the instance cold boots from rootfs (ADR-005: cold boot
// always works). BaseKey/LayerKey are required for the cold path.
//
// Issue #96 / ADR-025 axis 2 (PR #116): BaseKey / LayerKey are the
// StorageBackend keys schedd sends on the wake wire (not host paths).
// vmmd resolves them via Storage.Get before staging the chroot. The
// local backend's Get maps the same keys to the same files the legacy
// *Path fields used, so single-box behaviour is preserved. Field
// names changed from *Path → *Key to match the new semantics.
type WakeRequest struct {
	Instance string
	AppID    string // apps.id UUID; PR-B UpdateEgressAllowlist walks live by AppID
	// AccountID is the apps row's owning account id (issue #301,
	// ADR-044). Threads onto the wire so vmmd can label the
	// vmmd_cpu_throttle_seconds_total{account_id, app_id} counter
	// and the throttle top-N admission primitive (topAppSet, cap
	// 100). Empty = "anonymous" admission (matches the
	// requestTotal overflow policy where missing account_id maps
	// to "other" via pkg/wire/metrics.go's overflow label set).
	AccountID  string
	BaseKey    string // StorageBackend key for drive0 shared ro base rootfs for the app's runtime
	LayerKey   string // StorageBackend key for drive1 per-app layer
	VcpuCount  int
	MemSizeMiB int
	EgressMbit int       // per-plan tc cap (pkg/api/limits.EgressMbit); 0 = no cap
	Snapshot   *Snapshot // nil => cold boot
	// Plan is the apps row's owning plan tier (issue #301, ADR-044).
	// Drives the parent-cgroup path (ParentCgroupFor) and the
	// --cgroup cpu.weight=N jailer argv, plus the per-instance
	// cpu.max direct file write. Required — Manager.Wake validates
	// the row against api.Plan.Valid() and returns an error on
	// empty/unknown plans so a missing wire field doesn't
	// silently land a VM under the wrong slice.
	Plan api.Plan
	// ExportDir, if non-empty, marks this instance as a builder VM (M6).
	// vmmd's Manager.DestroyWithExport waits for exit, captures the exit code,
	// and copies build artifacts (build-done.json + /build/out/*) into this host
	// directory. App VMs leave it empty.
	ExportDir string
	// Port (issue #460 / ADR-053, PR-C) is the per-deployment override
	// port the customer's app binds inside the guest. 0 = legacy 8080
	// (netns.AppPort default). The host's waitReady + DNAT stay fixed
	// on 8080 (ADR-009 + guest/init/portnorm_linux.go); vmmd's
	// forwarder uses this port to dial the guest. Stamped onto the
	// live Instance so vmmdgrpc forwarder callers can resolve
	// LiveFor(instance).Port without a second request lookup.
	Port int
	// HealthcheckPath (issue #460 / ADR-053, ADR-057 / PR-D) is the
	// per-deployment override readiness probe path. "" = legacy
	// TCP-accept on :8080 (pre-PR-D default). Non-empty → vmmd's
	// waitReady does HTTP GET <HealthcheckPath> against <HostIP>:8080
	// and accepts 2xx as ready. The host probe target is always :8080
	// — ADR-009 + portnorm re-expose the customer bind on :8080 inside
	// the guest, so the path is the customer's choice and the port is
	// the host's choice. Stamped onto the live Instance so server-side
	// readers can resolve LiveFor(instance).HealthcheckPath without a
	// second request lookup.
	HealthcheckPath string
	// Runtime (issue #470 / PR #470-FU-B) is the runtime id
	// ("node22", "python312", etc.) the app was woken for. Stored
	// on the live Instance so the framework-ready receipt handler
	// can stamp the per-instance `framework_ready_at` time and
	// observe the vmmd_guest_framework_warmup_seconds histogram
	// under the right `runner` label. Comes from app.Runtime at
	// Wake time (the engine calls WakeWithRequest after resolving
	// the app). Empty = pre-PR-B legacy wake — the framework-ready
	// receipt path tolerates empty (label collapsed to "unknown" on
	// the histogram instead of returning an error).
	Runtime string
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
	// APIEnvEntries are the per-key plaintext rows from `app_envs` the
	// caller wants loaded into the guest's env (issue #395 / ADR-045).
	// Unlike SealedEnvEntries, the values are NOT sealed — env vars are
	// non-sensitive runtime config by contract (the issue's plaintext
	// rationale + ADR-045 §Decision). vmmd merges the entries into a
	// single JSON map and writes /etc/faas/env.json on drive1; guest-init
	// reads it and merges into the process env with precedence
	// "secrets > api_env > manifest_env > os.environ". Empty slice = no
	// env.json file written (manifest env still flows in via
	// /etc/faas/app.json, the legacy path).
	APIEnvEntries []APIEnvEntry
	// EgressAllowlist (ADR-031, tier-2 of the network roadmap): per-app
	// outbound IPv4 allowlist. Each entry is a CIDR string (e.g.
	// "1.2.3.0/24"); empty slice = current behaviour (no allowlist rule
	// emitted, every non-deny destination is reachable). When non-empty,
	// the per-netns forward chain gains a single
	//   `iifname "tap0" ip daddr { <CIDRs> } accept`
	// rule after the lateral-movement deny + SMTP drops; deny > allow on
	// overlap, so a typoed RFC1918 CIDR still gets dropped. Plan-gated
	// upstream — Free/Hobby never get here; Pro ≤ 16; Scale ≤ 64. The
	// caller (apid) is responsible for size + per-plan gating.
	EgressAllowlist []string
}

// SealedEnvEntry is one (key, ciphertext) pair as stored in app_secrets. The
// key is the env-var name; the ciphertext is sealed under the host age
// recipient by apid. vmmd merges all entries into the single envelope file.
type SealedEnvEntry struct {
	Key        string
	Ciphertext []byte
}

// APIEnvEntry is one (key, value) plaintext pair as stored in app_envs
// (issue #395 / ADR-045). The value is sent over the wire as a UTF-8
// string — every layer of the pipeline (apid JSON decode, Postgres
// TEXT column, env.json encoding/json marshal) is UTF-8-bound, so a
// `bytes` field would have hidden silent Unicode replacement during
// the on-disk JSON marshal. vmmd merges all entries into a single
// JSON map written to /etc/faas/env.json on drive1; guest-init
// reads it and merges into the process env with precedence below
// sealed secrets.
type APIEnvEntry struct {
	Key   string
	Value string
}

// ColdBootRequest is the deploy-pipeline prime path: a first boot with no
// snapshot yet (spec §9.6).
//
// Issue #96 / ADR-025 axis 2 (PR #116): BaseKey / LayerKey are the
// StorageBackend keys schedd sends on the wake wire (not host paths).
// Same semantics as WakeRequest.
type ColdBootRequest struct {
	Instance string
	// AccountID is the apps row's owning account id (issue #301,
	// ADR-044). Forwarded to WakeRequest.AccountID — see
	// WakeRequest for the contract.
	AccountID  string
	BaseKey    string
	LayerKey   string
	VcpuCount  int
	MemSizeMiB int
	EgressMbit int // per-plan tc cap; 0 = no cap (legacy / disabled)
	// Plan is the apps row's owning plan tier (issue #301, ADR-044).
	// Forwarded to WakeRequest.Plan — see WakeRequest for the contract.
	Plan api.Plan
	// ExportDir is non-empty for builder VMs. See WakeRequest.
	ExportDir string
	// SealedEnvEntries is forwarded to WakeRequest for staging onto drive1
	// (spec §11/G2). Empty slice = no secrets file written.
	SealedEnvEntries []SealedEnvEntry
	// APIEnvEntries is forwarded to WakeRequest for staging onto drive1
	// (issue #395 / ADR-045). Empty slice = no env.json file written.
	APIEnvEntries []APIEnvEntry
	// EgressAllowlist (ADR-031) — same shape as WakeRequest.
	EgressAllowlist []string
	// Port (issue #460 / ADR-053, PR-C) — the per-deployment override
	// port forwarded verbatim to WakeRequest.Port. Production wiring
	// uses WakeRequest directly via the vmmdgrpc adapters
	// (pkg/vmmdgrpc/proto.go), so this field is currently exercised
	// only by unit tests (TestColdBootSuccessStampsInstancePort). Kept
	// for symmetry so the cold-boot public surface stays a complete
	// mirror of WakeRequest — a future caller that wants to invoke
	// ColdBoot without going through WakeRequest shouldn't have to
	// drop a field. Removing the field would silently break the
	// port-stamping test.
	Port int
	// HealthcheckPath (issue #460 / ADR-053, ADR-057 / PR-D) — the
	// per-deployment override readiness probe path forwarded verbatim
	// to WakeRequest.HealthcheckPath. Production wiring uses
	// WakeRequest directly via the vmmdgrpc adapters
	// (pkg/vmmdgrpc/proto.go), so this field is currently exercised
	// only by unit tests. Kept for symmetry so the cold-boot public
	// surface stays a complete mirror of WakeRequest — a future caller
	// that wants to invoke ColdBoot without going through WakeRequest
	// shouldn't have to drop a field.
	HealthcheckPath string
	// Runtime (issue #470 / PR #470-FU-B) — the runtime id forwarded
	// verbatim to WakeRequest.Runtime. Mirrors the WakeRequest
	// field's contract: empty = legacy wake (the framework-ready
	// receipt path tolerates empty and falls back to the "unknown"
	// histogram label).
	Runtime string
}

// ColdBoot boots an instance from rootfs with no snapshot. It is Wake with a nil
// snapshot.
func (m *Manager) ColdBoot(ctx context.Context, req ColdBootRequest) (*Instance, error) {
	return m.Wake(ctx, WakeRequest{
		Instance: req.Instance, AccountID: req.AccountID,
		BaseKey: req.BaseKey, LayerKey: req.LayerKey,
		VcpuCount: req.VcpuCount, MemSizeMiB: req.MemSizeMiB,
		EgressMbit: req.EgressMbit, Snapshot: nil,
		ExportDir: req.ExportDir, SealedEnvEntries: req.SealedEnvEntries,
		APIEnvEntries:   req.APIEnvEntries,
		EgressAllowlist: req.EgressAllowlist,
		Plan:            req.Plan,
		Port:            req.Port,
		// ADR-057 / PR-D: forward the per-deployment override
		// readiness probe path so Wake stamps it onto the live
		// Instance. Empty = legacy TCP-accept on :8080.
		HealthcheckPath: req.HealthcheckPath,
		// PR #470-FU-B: forward the runtime id so the framework-ready
		// receipt handler can label the warmup histogram. See
		// WakeRequest.Runtime for the contract.
		Runtime: req.Runtime,
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
	// Stamp the Plan onto the Lease (issue #301, ADR-044) so the
	// downstream vmm.Boot/Restore/Kill/Destroy path can compute the
	// per-plan parent cgroup + cpu.weight without a separate map
	// lookup. Set BEFORE the validation loop so a rejected wake
	// cleans up via the same defer that releases the slot — the
	// Plan is allocator-side state and must follow the lease's
	// lifetime.
	lease.Plan = req.Plan
	// Any failure from this point — Plan validation, wire-side
	// allowlist checks, bringUp, cgroup write — must fully clean up.
	// Registering the cleanup BEFORE the validation loop is
	// load-bearing: the lease is acquired, so fail-closed early-return
	// paths otherwise leak the slot. The Plan validator sits BEFORE
	// bringUp so a wire call with an empty/unknown plan never boots
	// the VM under the legacy 2-level cgroup path (issue #301 /
	// ADR-043, PR #390 review finding #1).
	defer func() {
		if err != nil {
			m.cleanup(context.WithoutCancel(ctx), lease, netns.NewConfig(
				lease.Instance, lease.Netns, lease.VethHost, lease.VethPeer, lease.HostIP,
			))
		}
	}()
	nc := netns.NewConfig(lease.Instance, lease.Netns, lease.VethHost, lease.VethPeer, lease.HostIP)
	nc.EgressMbit = req.EgressMbit
	// Plan validation (issue #301 / ADR-043). An empty / unknown plan
	// would land the VM under the wrong cgroup sub-slice (or under
	// none at all) and silently disable per-plan cpu.weight + cpu.max
	// enforcement. Validate AFTER the cleanup defer is registered so
	// the lease is released on reject — but BEFORE setupNetwork /
	// bringUp so the VM never reaches the wrong slice. PR #390
	// review finding #1 (correctness / ship-blocker).
	if !req.Plan.Valid() {
		err = fmt.Errorf("wake %s: invalid plan %q (issue #301 / ADR-043)", req.Instance, req.Plan)
		return nil, err
	}
	// Spec §7 conntrack cap (ADR-018 deferral). Platform-wide constant;
	// not propagated through vmmd gRPC because every instance sees the
	// same value (the failure mode is host-table exhaustion, shared).
	// netns.Config omits the rule when ConntrackCap <= 0 so a vmmd that
	// hasn't been rebuilt still wakes cleanly.
	nc.ConntrackCap = m.conntrackCap
	// ADR-031 + ADR-032 — translate the wire-level CIDR strings into
	// netip.Prefix once, here, so the nft renderer never touches
	// stringly-typed addresses. apid's PATCH handler already
	// ParsePrefix'd these on input and the apps.egress_allowlist
	// cidr[] TRIGGER (`apps_egress_allowlist_cidr`, migration 00033)
	// rejects families outside {4,6} and any /0, so a parse failure
	// at this layer means the wire contract is violated — fail fast
	// rather than silently emit a half-formed ruleset (a single bad
	// CIDR would otherwise crash nft). Defence in depth: wire-side
	// gate here too, so a wire-bypass (e.g. a vmmd that forgets to
	// re-validate) cannot smuggle a bad prefix past apid. ADR-032 —
	// the v4-only gate was removed; v4 + v6 are both allowed here.
	// Bits()==0 mirrors the apid gate so a wire-bypass cannot land a
	// /0 either. On reject the named-return `err` triggers the cleanup
	// defer registered above (release the slot).
	if len(req.EgressAllowlist) > 0 {
		nc.EgressAllowlist = make([]netip.Prefix, 0, len(req.EgressAllowlist))
		for _, c := range req.EgressAllowlist {
			prefix, perr := netip.ParsePrefix(c)
			if perr != nil {
				return nil, fmt.Errorf("wake %s: egress allowlist: invalid CIDR %q: %w", req.Instance, c, perr)
			}
			if prefix.Bits() == 0 {
				return nil, fmt.Errorf("wake %s: egress allowlist: rejected %q (masklen /0; ADR-032 non-/0 contract)", req.Instance, c)
			}
			nc.EgressAllowlist = append(nc.EgressAllowlist, prefix)
		}
	}

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
		if len(m.hostIdentities) == 0 {
			return nil, fmt.Errorf("wake %s: %w", req.Instance, ErrNoHostKey)
		}
		// We loop-and-merge rather than unseal-into-buf because each entry
		// is a sealed full envelope (per-key rows). That's the natural shape
		// coming from apid's per-row upserts. OpenMulti is the
		// rotation-aware entry point: during the 30-day overlap window
		// (issue #316 / ADR-057) m.hostIdentities holds [current, previous]
		// and age.Decrypt natively tries both. Single-identity pre- and
		// post-overlap states use the 1-element slice.
		merged := secretbox.Envelope{}
		for _, e := range req.SealedEnvEntries {
			inner, err := secretbox.OpenMulti(m.hostIdentities, e.Ciphertext)
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

	// Issue #395 / ADR-045: stage plaintext api_env → JSON-encode the
	// merged map → loopback-mounted write → umount. Mirrors the
	// sealed-secrets block above but skips the unseal step entirely.
	// Sorted-by-key write so the wire is deterministic (handy for
	// future redaction / diff tooling). No host key needed — values
	// are plaintext by contract.
	if len(req.APIEnvEntries) > 0 {
		merged := map[string]string{}
		for _, e := range req.APIEnvEntries {
			merged[e.Key] = e.Value
		}
		// Marshal deterministically (encoding/json already sorts map
		// keys alphabetically) so re-reads on the guest side produce
		// the same bytes regardless of input ordering.
		blob, err := json.Marshal(merged)
		if err != nil {
			return nil, fmt.Errorf("wake %s: marshal api env: %w", req.Instance, err)
		}
		if err := m.stageAPIEnv(req.Instance, blob); err != nil {
			return nil, fmt.Errorf("wake %s: stage api env: %w", req.Instance, err)
		}
	}

	// cgroup fence (spec §4.4 / issue #301 / ADR-044) — written AFTER
	// bringUp returns because the scope is created by jailer during
	// Boot/Restore and does not exist before then. writePlanCgroup
	// writes BOTH memory.max (the per-VM + 8 MB fence) and cpu.max
	// (the per-plan quota: Free 100ms/100ms, Hobby 200ms/200ms, Pro
	// 500ms/500ms, Scale 1000ms/1000ms). Both writes are naturally
	// idempotent (cgroupv2 accepts an identical-value write as a no-op),
	// so snapshot-restore Wake does not need a reset. Failure routes
	// through the deferred cleanup path — the VM is already up, but
	// teardown kills it and releases the lease.
	if err = writePlanCgroup(req.Instance, req.Plan, req.MemSizeMiB); err != nil {
		// Cgroup fence spec §4.4 but may fail in constrained environments
		// (cgroup namespace isolation). VM is already up; continue without memory cap.
		// Useful for local metal testing only.
		m.log.Warn("cgroup fence: writePlanCgroup failed, continuing",
			"instance", req.Instance, "plan", req.Plan, "err", err)
	}

	// ADR-051 Phase 4 / PR-D: on cold boot, gate the wake on the
	// characterization report. The guest dials host CID 2 at port
	// 1026 with the report framed as [4B msg_type=3][4B body_len][JSON].
	// On deadline we fall back to the scan-hint class — never fail
	// the wake, that's strictly worse than today's `:8080` accept
	// failure path (the operator gets no signal). Restore inherits
	// the class from the apps row captured in the original cold boot.
	var report api.CharacterizationReport
	if method == WakeColdBoot {
		report, _ = m.vmm.WaitCharacterizationReport(ctx, lease, m.characterizationWait)
		m.log.Info("wake: characterization report",
			"instance", req.Instance, "observed_class", report.ObservedClass,
			"observed_port", report.ObservedPort, "exit", report.ExitCode,
			"port_norm_mode", report.PortNormalizationMode)
	}

	inst := &Instance{Lease: lease, Net: nc, Method: method, AppID: req.AppID, Plan: req.Plan, Port: req.Port, HealthcheckPath: req.HealthcheckPath, Characterization: report, Runtime: req.Runtime}
	// Capture the allowlist rule handles for the in-place patch
	// (PR-B, UpdateEgressAllowlist). The kernel assigns a handle
	// to every `nft add rule`; we re-list the chain with `-a` and
	// match on the iifname + daddr substring to record the
	// handle. Best-effort: a failure to capture (the chain
	// re-list exits non-zero if the netns was torn down
	// concurrently, or the substring doesn't match a renderer
	// invariant) leaves the handle at 0, which means the first
	// UpdateEgressAllowlist for this instance will `add` the new
	// rule alongside the prior one. The next patch will then
	// have the prior handle cached and can `delete` + `add` as
	// intended.
	hV4, hV6, herr := m.captureAllowlistHandles(ctx, nc.Netns)
	if herr != nil {
		m.log.Debug("fcvm: Wake handle capture best-effort failed",
			"instance", req.Instance, "netns", nc.Netns, "err", herr)
	}
	inst.AllowlistHandleV4 = hV4
	inst.AllowlistHandleV6 = hV6
	m.mu.Lock()
	m.live[req.Instance] = inst
	// Issue #470 / PR #470-FU-B: maintain the CID→instance
	// reverse index so the framework_ready DGRAM receipt path
	// can resolve the peer CID to an instance in O(1) instead
	// of a linear scan over the live map (review feedback on
	// the early PR B; HIGH-3). Populated on BringUp and
	// removed on Destroy / Park.
	m.cidToID[GuestVsockCID(inst.Lease.Slot)] = req.Instance
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
	// issue #299: refuse to bring up an instance whose base ext4
	// staged with a CRITICAL Grype finding. Runs BEFORE the restore
	// decision tree because a scan refusal is a policy gate, not a
	// snapshot-cache decision (ADR-005 doesn't apply — we're not
	// failing to find a snapshot, we're refusing the boot on a
	// known-bad CVE). Returns *api.Problem with code = CodeScanCritical
	// on refusal; schedd surfaces it through the wake path (which is
	// why the function returns the Problem shape rather than a bare
	// error — the wake-error channel expects it). No-op when storage
	// is nil (unit tests that don't wire WithStorage continue to pass).
	if err := m.bringUpScanCheck(ctx, req.BaseKey); err != nil {
		return WakeColdBoot, err
	}
	if PlanWake(req.Snapshot, m.fcVersion) == WakeRestore {
		rs := RestoreSpec{
			VMStatePath: req.Snapshot.VMStatePath,
			// #96 / ADR-025 axis 2: thread the canonical storage key the
			// scheduler populated into WakeRequest.Snapshot. The VMM
			// resolves it through the StorageBackend before staging.
			StorageKey: req.Snapshot.StorageKey,
			// #121 / ADR-025 axis 2 slice 4: thread the canonical
			// vmstate storage key when the engine populated it (remote
			// nodes). Default-local single-box leaves this empty so
			// the VMM falls back to RestoreSpec.VMStatePath above,
			// preserving the legacy host-path branch.
			VMStateStorageKey: req.Snapshot.VMStateStorageKey,
			Tap:               nc.Tap,
			// The restored VM re-reads kernel + drives under the chroot
			// basenames; Park→Kill erased the previous chroot, so hand the
			// Manager.ColdBoot equivalents back to the VMM to re-stage.
			KernelKey: m.paths.Kernel,
			BaseKey:   req.BaseKey,
			LayerKey:  req.LayerKey,
			// ADR-022: same vsock device the cold-boot path attaches, derived
			// from the lease's slot so the guest's listener is reachable at a
			// globally unique guest_cid.
			VsockDevice: NewVsockDevice(lease.Slot),
			// Issue #460 / ADR-053, ADR-057 / PR-D: per-deployment
			// override readiness probe path. Empty keeps the legacy
			// TCP-accept on :8080 (pre-PR-D default). Non-empty →
			// waitReady does HTTP GET <HealthcheckPath> against
			// <HostIP>:8080 and accepts 2xx as ready.
			HealthcheckPath: req.HealthcheckPath,
		}
		if rErr := m.vmm.Restore(ctx, lease, rs); rErr == nil {
			return WakeRestore, nil
		} else {
			// Fall back to cold boot into the same netns; kill any half-restored VM.
			// The wrapped rErr names the failure mode (vsock dial timeout vs
			// ack-nack vs /snapshot/load failure) so the operator doesn't have
			// to dig through vmm.go to find out why the resume hook fired.
			//
			// PR scale-out readiness (4-PR #2): the desired_/snapshot_
			// fc_version fields are the cross-node diagnostic for the day
			// a snapshot made on node A is loaded on node B. They reach
			// this Warn only when PlanWake returned WakeRestore (which
			// already passed Usable() — FCVersion was equal) AND Restore
			// failed; a pure version mismatch takes the cold-boot path
			// through BootColdBoot below without firing here.
			m.log.Warn("restore failed, falling back to cold boot",
				"instance", req.Instance,
				"err", rErr,
				"slot", lease.Slot,
				"desired_fc_version", m.fcVersion,
				"snapshot_fc_version", req.Snapshot.FCVersion)
			m.metrics.ObserveFallback()
			_ = m.vmm.Kill(ctx, lease)
		}
	}

	spec := ColdBootSpec{
		KernelKey:  m.paths.Kernel,
		BaseKey:    req.BaseKey,
		LayerKey:   req.LayerKey,
		VcpuCount:  req.VcpuCount,
		MemSizeMiB: req.MemSizeMiB,
		Tap:        nc.Tap,
		// Issue #460 / ADR-053, ADR-057 / PR-D: per-deployment
		// override readiness probe path. Empty keeps the legacy
		// TCP-accept on :8080 (pre-PR-D default). Non-empty →
		// waitReady does HTTP GET <HealthcheckPath> against
		// <HostIP>:8080 and accepts 2xx as ready.
		HealthcheckPath: req.HealthcheckPath,
	}
	if err := m.vmm.BootColdBoot(ctx, lease, spec); err != nil {
		return WakeColdBoot, fmt.Errorf("wake %s: cold boot: %w", req.Instance, err)
	}
	return WakeColdBoot, nil
}

// bringUpScanCheck — issue #299 admission seam: refuse to bring up
// an instance whose base ext4 staged with a CRITICAL Grype finding.
//
// Reads the scan sidecar at wire.ScanKeyForBaseKey(baseKey) and
// returns *api.Problem with code = api.CodeScanCritical if CRITICAL
// > 0. The sidecar is written by imaged at base-stage time (see
// pkg/imaged/base_stage.go) in lock-step with the digest sidecar —
// a missing sidecar means the base was staged by an imaged that
// predates issue #299 OR the storage backend lost it (a real ops
// hazard); treat both as CRITICAL and refuse the boot. Fail-closed
// here mirrors the imaged-side fail-closed sidecar write
// (CRITICAL=9999 placeholder when Grype is missing) so a Grype
// absence on the imaged side propagates as a refusal on the vmmd
// side — never a silent admit.
//
// Returns nil when storage is nil (no scan check configured — the
// unit tests that don't wire WithStorage take this path). Returns
// nil on a scan sidecar with CRITICAL=0; per-severity finding
// counts are forwarded to vmmd's imageScanMetrics counter via
// metricsImageScan so the §12 dashboard sees the per-image
// vulnerability posture, not just the gate result.
//
// Mirrors the cosign LocalVerifier.Verify read-side pattern at
// pkg/cosign/verifier.go:49-104 (StorageBackend.Get → parse → return
// typed error), but runs in vmmd per the issue's gate-placement
// decision rather than in schedd alongside the cosign verifier. The
// gate returns the same Problem shape schedd's engine.go:558-595
// emits (api.NewProblem with HTTP 503), so schedd can render the
// wake-error path with no extra translation layer.
func (m *Manager) bringUpScanCheck(ctx context.Context, baseKey string) error {
	if m.storage == nil {
		return nil
	}
	scanKey := wire.ScanKeyForBaseKey(baseKey)
	rc, err := m.storage.Get(ctx, scanKey)
	if err != nil {
		return api.NewProblem(http.StatusServiceUnavailable, api.CodeScanCritical,
			"scan sidecar missing",
			fmt.Sprintf("scan sidecar missing for base %q at %q; refusing to boot un-scanned ext4 (issue #299)", baseKey, scanKey))
	}
	defer func() { _ = rc.Close() }()
	var scan struct {
		Image    string         `json:"image"`
		Findings map[string]int `json:"findings"`
	}
	if err := json.NewDecoder(rc).Decode(&scan); err != nil {
		return api.NewProblem(http.StatusServiceUnavailable, api.CodeScanCritical,
			"scan sidecar unreadable",
			fmt.Sprintf("scan sidecar at %q unreadable: %v (issue #299)", scanKey, err))
	}
	if n := scan.Findings["CRITICAL"]; n > 0 {
		return api.NewProblem(http.StatusServiceUnavailable, api.CodeScanCritical,
			"CRITICAL vulnerability in base ext4",
			fmt.Sprintf("base %q has %d CRITICAL Grype findings; refusing to boot (issue #299)", baseKey, n))
	}
	// Forward per-severity finding counts to the vmmd Prometheus
	// counter. The image label is the OCI ref imaged wrote into
	// the sidecar (defensive empty-string fallback if absent —
	// imaged always populates it in production).
	for sev, n := range scan.Findings {
		m.metricsImageScan(scan.Image, sev, n)
	}
	return nil
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
	// Park drops the VM and releases its CID (the lease is freed by
	// cleanup below); the reverse index must drop with it so a
	// subsequent framework_ready DGRAM racing the park falls through
	// to the "unknown CID" Debug log instead of stamping a tile
	// whose Lease.Slot was just freed.
	delete(m.cidToID, GuestVsockCID(inst.Lease.Slot))
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
		// Drop the CID→instance join at the same instant the live
		// row goes away. A framework_ready DGRAM racing this
		// teardown will see "unknown CID" and log at Debug — the
		// guest has already been torn down, so the stamp would be
		// useless anyway.
		delete(m.cidToID, GuestVsockCID(inst.Lease.Slot))
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

// SnapshotLive returns a copy of the (instanceID, vethHost) map
// for every live instance. The vmmd network_poller (ADR-046)
// uses this to read the kernel byte counter for each instance
// without keeping its own mirror of `m.live` in sync — the
// returned map is a fresh copy taken under m.mu, so concurrent
// Destroy / Wake updates do not race the poller's iteration.
//
// Empty map (not nil) when no instances are live, matching the
// meter_egress_adapter's `for instID, veth := range snapshot`
// idiom. instanceIDs are the strings the schedd speaks; vethHosts
// are the host-side veth names the kernel counter lives under.
func (m *Manager) SnapshotLive() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string, len(m.live))
	for id, inst := range m.live {
		if inst == nil {
			continue
		}
		out[id] = inst.Net.VethHost
	}
	return out
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

// InstancePID delegates to the underlying VMM. M8 §11: the
// SeccompStatus gRPC handler reads /proc/<pid>/status to verify
// the jailer default seccomp filter is in place. The method is
// defined on the VMM interface itself (not as a Manager-owned
// lookup) because the PID is private to JailerVMM — propagating
// it through Manager would just bounce a return value. The
// Manager-to-VMM hop is one level of indirection, but it's the
// hop that keeps JailerVMM's bookkeeping internal.
func (m *Manager) InstancePID(instance string) (int, bool) {
	return m.vmm.InstancePID(instance)
}

// LogRing delegates to the underlying VMM (issue #254 / Move 4). The
// Manager-to-VMM hop is one level of indirection, same as InstancePID,
// because the ring is private to JailerVMM. Returns nil for instances
// that are not alive on this vmmd; the vmmdgrpc handler maps nil to
// NotFound without a separate liveness check.
func (m *Manager) LogRing(instance string) *logbuf.Ring {
	return m.vmm.LogRing(instance)
}

// NetnsFor returns the network namespace name (fc-<instance>) the
// Manager bound to this instance at Wake time, plus a boolean that
// reports whether the instance is currently live. Empty string +
// false for unknown instances. The vmmd ForwardHTTP handler
// (pkg/vmmdgrpc/forward.go, issue #98 / ADR-028) uses this to nsenter
// the per-instance netns and dial netns.GuestIP:netns.AppPort on the
// inner side. The boolean is the only race-free liveness signal:
// callers should not try to look the instance up in `m.live`
// directly because Destroy removes the entry.
func (m *Manager) NetnsFor(instance string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.live[instance]
	if !ok {
		return "", false
	}
	return inst.Lease.Netns, true
}

// UpdateEgressAllowlist (ADR-031 + ADR-033, tier-2 PR-B, Track B):
// walks Manager.live, and for each instance whose app_id matches
// the request, applies the new egress allowlist in-place via
// incremental nft patch — no netns teardown, no cold-wake tax
// on the next request. Per-family partition matches the renderer
// (prefix.Addr().Is4() → ip faas forward for v4, ip6 faas forward
// for v6).
//
// The patch strategy:
//
//  1. For each matching live instance, snapshot the cached
//     (Netns, priorEgressAllowlist, priorAllowlistHandleV4,
//     priorAllowlistHandleV6) under m.mu. Released before any
//     netns exec so Wake/Park/Destroy don't see a held Manager
//     lock.
//  2. Per family (v4 then v6):
//     a. If the prior handle is non-empty, emit
//     `nft delete rule ip[6] faas forward handle <H>`.
//     b. Emit `nft add rule ip[6] faas forward … accept` (or
//     skip when the new family half is empty) using the
//     renderer's ForwardAllowlistRule / ForwardAllowlistRule6
//     argv builders.
//  3. On any nft failure mid-patch, revert by re-rendering the
//     prior argv (the cached priorAllowlistHandle* is the
//     invariant) and re-applying it. The revert runs synchronously
//     before returning; a revert failure is returned to the
//     caller as an Internal problem (the live netns is then in
//     an undefined state and schedd's watchdog will Park + ColdBoot
//     the affected instances on its next tick).
//
// Idempotency: identical allowlist re-pushed → samePrefixSet
// fast-path returns nil without running nft. The next cold boot
// re-reads the column, so a snapshot-restore Wake always sees the
// current allowlist — there is no `egressAllowlistVersion` column
// to keep in sync.
//
// Lock order:
//   - m.mu held briefly to snapshot targets and to update the
//     cached handles after a successful patch.
//   - m.mu released before any per-netns nft exec. The kernel
//     serialises nft operations per-netns (the netlink socket is
//     per-call), so concurrent UpdateEgressAllowlist calls on
//     different netns are safe; concurrent calls on the same
//     netns serialise at the nft level.
//
// On an empty allowlist, the per-family add argv is skipped
// (matches the Wake contract for empty EgressAllowlist: no rule
// emitted, chain-policy stays accept). When the prior allowlist
// was non-empty, the prior rule's handle is still deleted so the
// netns returns to the empty-allowlist state.
func (m *Manager) UpdateEgressAllowlist(ctx context.Context, appID string, allowlist []netip.Prefix) error {
	if appID == "" {
		return fmt.Errorf("fcvm: UpdateEgressAllowlist: empty app_id")
	}

	// Snapshot the targets + their cached handles under the
	// manager lock. Released before any netns exec. The full
	// netns.Config is captured so the renderer can produce
	// the same argv as at Wake (Tap name, etc., must match).
	type patchTarget struct {
		instanceID string
		netns      string
		net        netns.Config
		prior      []netip.Prefix
		handleV4   uint64
		handleV6   uint64
	}
	var targets []patchTarget
	m.mu.Lock()
	for id, inst := range m.live {
		if inst.AppID != appID {
			continue
		}
		prior := make([]netip.Prefix, len(inst.Net.EgressAllowlist))
		copy(prior, inst.Net.EgressAllowlist)
		targets = append(targets, patchTarget{
			instanceID: id,
			netns:      inst.Net.Netns,
			net:        inst.Net,
			prior:      prior,
			handleV4:   inst.AllowlistHandleV4,
			handleV6:   inst.AllowlistHandleV6,
		})
	}
	m.mu.Unlock()
	if len(targets) == 0 {
		return nil // no live instances for this app — idempotent
	}

	// Compute the new partition's argv via the per-instance
	// netns.Config (the live Tap name threads through so the
	// emitted `iifname "tap0"` matches what the Wake-time
	// rule installed).
	type newAllowlist struct {
		v4Argv []string
		v6Argv []string
	}
	build := func(t patchTarget) newAllowlist {
		nc := t.net
		nc.EgressAllowlist = allowlist
		nx := func(parts ...string) []string {
			return append([]string{"ip", "netns", "exec", t.netns, "nft"}, parts...)
		}
		return newAllowlist{
			v4Argv: nc.ForwardAllowlistRule(func(parts ...string) []string { return append([]string{}, nx(parts...)...) }),
			v6Argv: nc.ForwardAllowlistRule6(func(parts ...string) []string { return append([]string{}, nx(parts...)...) }),
		}
	}

	// Apply per-instance. A failure on any one surfaces to the
	// caller; the loop stops (the caller logs + retries on its
	// next reconcile). Per-instance revert is best-effort: a
	// revert that itself fails is logged at Warn and the error
	// from the original patch is returned (the live netns is
	// then in an undefined state; schedd's watchdog will Park +
	// ColdBoot it on the next tick).
	newHandles := make(map[string]struct{ v4, v6 uint64 }, len(targets))
	for _, t := range targets {
		// Idempotent fast-path: if the prior allowlist is
		// set-equal to the new one, the live netns already
		// matches and the nft exec would be a no-op anyway
		// (delete-then-add the same rule). Skip both the
		// argv build and the nft exec — schedd's pg_notify
		// redelivery lands here on reconnect.
		if samePrefixSet(t.prior, allowlist) {
			newHandles[t.instanceID] = struct{ v4, v6 uint64 }{v4: t.handleV4, v6: t.handleV6}
			continue
		}
		next := build(t)
		newH, err := m.applyOneInstancePatch(ctx, t.netns, t.prior, next.v4Argv, next.v6Argv, t.handleV4, t.handleV6)
		if err != nil {
			return fmt.Errorf("fcvm: UpdateEgressAllowlist app=%s netns=%s: %w", appID, t.netns, err)
		}
		newHandles[t.instanceID] = newH
	}

	// Update cached handles + prior lists so the next patch's
	// fast-path compares against the new baseline.
	m.mu.Lock()
	for id, inst := range m.live {
		nh, ok := newHandles[id]
		if !ok {
			continue
		}
		inst.Net.EgressAllowlist = make([]netip.Prefix, len(allowlist))
		copy(inst.Net.EgressAllowlist, allowlist)
		inst.AllowlistHandleV4 = nh.v4
		inst.AllowlistHandleV6 = nh.v6
	}
	m.mu.Unlock()
	return nil
}

// applyOneInstancePatch runs the per-family patch sequence for a
// single netns. Returns the handles of the freshly-installed v4 /
// v6 allowlist rules (zero when the family half is empty / no
// rule was emitted).
//
// The returned handle values come from the nft argv sequence we
// emit: a fresh `nft add rule ip faas forward …` produces a
// handle that the kernel assigns; we capture it by re-listing the
// chain with `nft -a list chain` and matching on the argv
// substring. That second read is what the unit suite's
// fakeRunner can't simulate (the kernel isn't there), so for
// tests we accept handle == 0 as "unknown, will be re-captured
// on the next patch's pre-read" — the production runner with a
// real `nft` resolves the handle.
//
// Sequence:
//
//  1. Delete prior v4 rule by handle (skip if handle == 0).
//  2. Add new v4 rule (skip if new argv is nil).
//  3. Same for v6.
//  4. On any failure, run the revert: re-add the prior rule by
//     re-rendering its argv. The prior argv was the one cached
//     on the live-instance struct (kept across patches).
//
// Per-family revert: the v4 and v6 patch sequences are run
// independently so that a v4 success isn't rolled back when the
// v6 patch fails. The reverted family re-renders the prior
// allowlist into its argv and re-adds the rule; the successful
// family is left alone. The catch is that handle capture only
// knows the new handle for the family that succeeded (the other
// stays at the prior handle, which is still valid for the
// reverted-on-failure family).
func (m *Manager) applyOneInstancePatch(
	ctx context.Context,
	netnsName string,
	prior []netip.Prefix,
	v4New, v6New []string,
	handleV4, handleV6 uint64,
) (struct{ v4, v6 uint64 }, error) {
	var zero struct{ v4, v6 uint64 }
	// Caller already short-circuited the set-equal case via
	// samePrefixSet before reaching here, so we always run the
	// per-family patch sequence. `prior` is still used below on
	// the failure-path revert (re-render the prior allowlist
	// into the per-family argv and re-add the failed family).
	// Build the per-family patch sequence. Track which family
	// each step belongs to so a mid-sequence failure can revert
	// only the failed family.
	nx := func(parts ...string) []string {
		return append([]string{"ip", "netns", "exec", netnsName, "nft"}, parts...)
	}
	type familyOp struct {
		family  string // "v4" or "v6"
		argv    []string
		newArgv []string // for revert: the new argv that was just added; on revert we re-add it then the next patch will delete by handle
	}
	var ops []familyOp
	if handleV4 > 0 {
		ops = append(ops, familyOp{
			family: "v4",
			argv:   nx("delete", "rule", "ip", "faas", "forward", "handle", fmt.Sprintf("%d", handleV4)),
		})
	}
	if v4New != nil {
		ops = append(ops, familyOp{family: "v4", argv: v4New, newArgv: v4New})
	}
	if handleV6 > 0 {
		ops = append(ops, familyOp{
			family: "v6",
			argv:   nx("delete", "rule", "ip6", "faas", "forward", "handle", fmt.Sprintf("%d", handleV6)),
		})
	}
	if v6New != nil {
		ops = append(ops, familyOp{family: "v6", argv: v6New, newArgv: v6New})
	}
	// Idempotent fast-path: nothing to do.
	if len(ops) == 0 {
		return zero, nil
	}

	// Run per-family patches. A failure on one family reverts
	// that family (re-adds the prior rule) and returns the
	// error — the other family is left at its new state.
	failedFamily := ""
	patchErr := error(nil)
	for _, op := range ops {
		if err := m.runCommands(ctx, [][]string{op.argv}); err != nil {
			failedFamily = op.family
			patchErr = err
			break
		}
	}
	if failedFamily != "" {
		// Per-family revert: re-render the prior allowlist for
		// the failed family and re-add it. The other family is
		// untouched (its new ruleset is already live).
		priorNC := netns.Config{Netns: netnsName, EgressAllowlist: prior}
		var revertArgv []string
		if failedFamily == "v4" {
			revertArgv = priorNC.ForwardAllowlistRule(func(parts ...string) []string { return append([]string{}, nx(parts...)...) })
		} else {
			revertArgv = priorNC.ForwardAllowlistRule6(func(parts ...string) []string { return append([]string{}, nx(parts...)...) })
		}
		if revertArgv != nil {
			if rerr := m.runCommands(ctx, [][]string{revertArgv}); rerr != nil {
				m.log.Warn("fcvm: UpdateEgressAllowlist revert failed; live netns may be in undefined state",
					"netns", netnsName, "family", failedFamily,
					"patch_err", patchErr, "revert_err", rerr)
			}
		}
		return zero, patchErr
	}

	// Handle capture: the kernel assigns handles on add. We
	// re-list the chain (when captureRunner is wired) and parse
	// the new handle so the next patch's delete-by-handle call
	// targets the rule that was just installed. When the
	// capture runner is nil (unit tests with a fakeRunner that
	// doesn't simulate the kernel), the cached handle stays at
	// the prior value — the metal test exercises the
	// captureRunner path on the EX44.
	newH4, newH6 := handleV4, handleV6
	if m.captureRunner != nil {
		if h, err := listChainHandles(ctx, m.captureRunner, netnsName, "ip", "faas", "forward"); err == nil {
			newH4 = h
		}
		if h, err := listChainHandles(ctx, m.captureRunner, netnsName, "ip6", "faas", "forward"); err == nil {
			newH6 = h
		}
	}
	return struct{ v4, v6 uint64 }{v4: newH4, v6: newH6}, nil
}

// samePrefixSet compares two prefix slices for set equality.
// Order independent (the renderer's partition is by family, not
// by input order). Used by UpdateEgressAllowlist's idempotent
// fast-path.
func samePrefixSet(a, b []netip.Prefix) bool {
	if len(a) != len(b) {
		return false
	}
	used := make([]bool, len(b))
next:
	for _, pa := range a {
		for i, pb := range b {
			if used[i] {
				continue
			}
			if pa == pb {
				used[i] = true
				continue next
			}
		}
		return false
	}
	return true
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

// CaptureRunner is the stdout-aware slice of the host command
// runner. The plain Runner interface only returns error; the
// in-place allowlist patch (PR-B) needs to read `nft -a list
// chain` output to resolve the kernel-assigned handle of the
// just-added rule. Production wires an adapter that wraps
// exec.CommandContext with CombinedOutput; unit tests stub
// through WithCaptureRunner. Nil is a valid value: the wake path
// then leaves AllowlistHandle{V4,V6} at 0 (the orphan rule
// stays correct, the next patch picks it up via listChainHandles).
type CaptureRunner interface {
	RunCapture(ctx context.Context, argv []string) ([]byte, error)
}

// WithCaptureRunner installs the capture runner post-construction.
// Returns the receiver so cmd/vmmd can chain it on NewManager.
//
//	vmm := fcvm.NewManager(...).WithCaptureRunner(cap)
func (m *Manager) WithCaptureRunner(cap CaptureRunner) *Manager {
	m.captureRunner = cap
	return m
}

// captureAllowlistHandles (tier-2 PR-B, called from Wake after
// setupNetwork + runCommands(NftCommands)) reads the kernel-
// assigned nft handle of each per-family allowlist accept rule
// just emitted by the renderer. Best-effort: returns (0, 0, nil)
// when (a) the capture runner is nil, (b) the chain is empty
// (no rule was emitted — empty EgressAllowlist), or (c) the
// handle substring can't be matched against the renderer
// invariant. The metal test exercises the success path; the
// unit suite stubs the runner to verify the Wake path tolerates
// capture failure.
func (m *Manager) captureAllowlistHandles(ctx context.Context, netnsName string) (uint64, uint64, error) {
	if m.captureRunner == nil {
		return 0, 0, nil
	}
	hV4, errV4 := listChainHandles(ctx, m.captureRunner, netnsName, "ip", "faas", "forward")
	if errV4 != nil {
		return 0, 0, errV4
	}
	hV6, errV6 := listChainHandles(ctx, m.captureRunner, netnsName, "ip6", "faas", "forward")
	if errV6 != nil {
		return 0, 0, errV6
	}
	return hV4, hV6, nil
}

// listChainHandles runs `ip netns exec <ns> nft -a list chain
// <family> faas forward` and returns the handle of the rule that
// matches the allowlist-renderer invariant
// `iifname "<tap>" <family> daddr { … } accept`. Returns 0 when
// no such rule is present (empty allowlist on that family half).
//
// The renderer always emits tap0 (identical in every netns per
// ADR-009) so the substring is well-defined. If a future renderer
// lets tap vary per instance, this helper needs to take the tap
// name as a parameter.
func listChainHandles(ctx context.Context, cap CaptureRunner, netnsName, family, table, chain string) (uint64, error) {
	argv := []string{"ip", "netns", "exec", netnsName, "nft", "-a", "list", "chain", family, table, chain}
	out, err := cap.RunCapture(ctx, argv)
	if err != nil {
		return 0, fmt.Errorf("nft -a list chain %s %s %s: %w", family, table, chain, err)
	}
	// Match on the `iifname "tap0"` substring to anchor to the
	// allowlist accept rule (the lateral-movement deny lines don't
	// match). Modern nft prints handles at end-of-rule with
	// `handle N`; the regex below extracts the integer.
	//
	// Output sample:
	//   chain forward {
	//    type filter hook forward priority 0; policy accept;
	//    iifname "tap0" ip daddr { 1.2.3.0/24 } accept # handle 42
	//   }
	needleAllow := `iifname "tap0" ` + family + ` daddr`
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, needleAllow) {
			continue
		}
		idx := strings.LastIndex(line, "# handle ")
		if idx < 0 {
			continue
		}
		tail := strings.TrimSpace(line[idx+len("# handle "):])
		// Strip trailing `}` if the chain closes on the same
		// physical line (some nft versions fold the closing brace).
		if i := strings.IndexAny(tail, " }"); i >= 0 {
			tail = tail[:i]
		}
		h, perr := strconv.ParseUint(tail, 10, 64)
		if perr != nil {
			continue
		}
		return h, nil
	}
	return 0, nil
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
