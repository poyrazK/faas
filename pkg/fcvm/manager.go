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
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
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

// InputRunner is the optional production fast path for tools that accept an
// atomic stdin batch. Test runners and legacy embedders need only implement
// Runner; setupNetwork falls back to one command per rule for them.
type InputRunner interface {
	RunInput(ctx context.Context, argv []string, input []byte) error
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
	// BootColdBootForJob (issue #1184 Workstream A / ADR-099) is the
	// sibling entry point for job-task cold boot. Mirrors BootColdBoot's
	// shape: materializes JobColdBootSpec.KernelKey/BaseKey/ImageRef
	// through StorageBackend, writes /etc/faas/job.json on drive1, and
	// delegates to bootNoWait (SkipReady=true — jobs don't bind :8080).
	// Tests with resolved paths can still call Boot directly; the public
	// schedd→vmmd seam is Manager.BootJob (engine.go::WakeJob).
	BootColdBootForJob(ctx context.Context, l Lease, spec JobColdBootSpec) error
	// WaitJobExit (issue #1184 Workstream A / ADR-099) is the host-side
	// mirror of WaitCharacterizationReport for the job-exit envelope.
	// Opens the per-instance vsock UDS, sends CONNECT <port> to FC,
	// reads [4B msg_type][4B body_len][N JSON], validates msg_type =
	// VsockJobExitMsgType (=4), and returns the parsed JobExitPayload.
	// Deadline is measured from entry; pass EffectiveDestroyWait(task_timeout_s)
	// so the per-task wall-clock cap fits the listener window. On
	// timeout the caller (Engine.HandleJobExit) treats the task as
	// crashed and the reaper takes over after JobReaperTTL.
	WaitJobExit(ctx context.Context, l Lease, deadline time.Duration) (JobExitPayload, error)
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
	// SnapshotKeepAlive (issue #470 / PR #470-FU-A) is the warm-tier
	// twin of Snapshot: pause + /snapshot/create + publish mem + vmstate
	// through the configured StorageBackend, then return WITHOUT the
	// trailing Kill. The VM stays paused until the caller fires Resume
	// (see ResumeVM below). Returns (SnapshotInfo, error) in the same
	// shape as Snapshot so the wire envelope and DB row write are
	// identical. The warm path is gated entirely by the engine's
	// captureWarmSnapshotLocked (pkg/sched/engine.go) — production
	// vmmd callers use PauseAndSnapshot for the legacy init-tier
	// capture and the new WarmSnapshot RPC for the warm-tier capture.
	SnapshotKeepAlive(ctx context.Context, l Lease, spec SnapshotSpec) (SnapshotInfo, error)
	// ResumeVM (issue #470 / PR #470-FU-A) is the host-side resume
	// half of the warm snapshot. PATCH /vm {"state":"Resumed"} on
	// the live Firecracker socket so the paused VM continues
	// executing. The Manager.live[instance] + cidToID entries
	// stay intact — Manager.WarmSnapshot is the only caller and
	// it never releases the lease. Idempotent on an already-running
	// VM (the Firecracker API returns 409 Conflict, which we treat
	// as a soft pass-through so a redundant WarmSnapshot retried by
	// an upstream blip doesn't surface as a hard error).
	ResumeVM(ctx context.Context, l Lease) error
	// Kill stops the firecracker process and removes the jail chroot. It is
	// best-effort and idempotent — safe to call on an instance that never fully
	// booted.
	Kill(ctx context.Context, l Lease) error
	// SignalAndKill (M-2 / ADR-138 §Decision 1) is the
	// graceful signal-then-grace-then-SIGKILL stop sequence.
	// Sends `signal` (a POSIX signal number; 0 = use the
	// manifest StopSignal, defaulting to SIGTERM) and waits
	// up to `grace` for the workload to exit cleanly; on
	// deadline expires, falls through to Kill (the SIGKILL
	// escalation). Returns (killSignalSent, exitCode, err);
	// killSignalSent=true means the grace window expired
	// and SIGKILL was the actual exit cause. signal=0 +
	// grace=0 is the legacy Destroy shape (immediate
	// SIGKILL); in that case the implementation delegates
	// to Kill and reports killSignalSent=true.
	SignalAndKill(ctx context.Context, l Lease, signal syscall.Signal, grace time.Duration) (killSignalSent bool, exitCode int32, err error)
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
	// StageWorkloadEnv writes the already-unsealed env overrides for one
	// sidecar under /etc/faas/workloads/<name>/env.json on the main workload's
	// writable layer. Implementations MUST treat an empty jsonBlob as a no-op.
	// The caller has already validated the name and opened the sealed values;
	// this method must run before guest-init can execute the workload.
	StageWorkloadEnv(instance, workloadName string, jsonBlob []byte) error
	// StageWorkloadManifest (issue #463 / ADR-069 / PR-B) writes
	// /etc/faas/workload.json on a workload's drive so guest-init
	// can fork/exec the workload under the right supervisor. Pass
	// driveIdx = -1 for the main workload (drive1); pass 0..N-1
	// for sidecar drives in the order schedd sent them on the
	// wake wire. The call MUST be before the VM is exposed to
	// the customer (the manifest is read by guest-init during
	// its first boot phase); implementations MUST treat a
	// missing drive file as a contract violation and error
	// out (the VM cannot boot without the manifest).
	StageWorkloadManifest(instance string, driveIdx int, w WorkloadSpec) error
	// StageWorkloadRoster (issue #463 / ADR-069 / PR-B) writes the
	// deployment-level roster at /etc/faas/workloads.json on drive1.
	// guest-init's runWorkloads orchestrator reads this file at boot
	// to discover the main workload's spec + the per-sidecar array;
	// without it, the orchestrator sees a "legacy single-workload"
	// shape and routes through runAppWithEnv unchanged. Pass the
	// main workload's spec as the first arg and the sidecar specs
	// (already filtered to type!="main") as the second. Implementations
	// MUST treat a missing drive1 file as a contract violation
	// and error out (the VM cannot boot into the orchestrator without
	// the roster).
	StageWorkloadRoster(instance string, main WorkloadSpec, sidecars []WorkloadSpec) error
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

// StaticEgressIPEntry (ADR-119 redesign) is the canonical
// (accountID, appID, customerIP, perVMHostIP) tuple consumed by
// Manager.SetStaticEgressIPAliases and the host renderer.
//
// AccountID is the apps.account_id the entry is provisioned for
// (the Postgres bundle gate is keyed on (account_id, customer_ip));
// AppID is the apps.id of the pinned app; IP is the customer-
// supplied IP aliased on br-tenants; PerVMHostIP is the per-VM
// host IP allocated from the dedicated static-egress pool
// (10.200.x.y/16, see alloc.go::AcquireStaticEgressIP) — the
// `ip saddr` in the host renderer's SNAT rule.
//
// Defined in pkg/fcvm so cmd/vmmd/egress_static_ip_bundle.go
// can type-alias it without a circular dependency. Both sides
// agree on the shape; the TOML loader in cmd/vmmd owns the wire
// format (last-wins on appID is enforced at the loader layer).
type StaticEgressIPEntry struct {
	AccountID   string
	AppID       string
	IP          netip.Addr
	PerVMHostIP netip.Addr
}

// Instance is a live (or booting) microVM tracked by the Manager.
// bringUpTimings (ADR-098 C11) is the wake-phase scratchpad the
// Manager.Wake caller allocates on the stack and threads through
// to Manager.bringUp. Restored onto Instance immediately after
// bringUp returns so the vmmd WakeResponse can carry the typed
// scalars. Stays on the stack — never crosses a goroutine boundary.
type bringUpTimings struct {
	restoreMs    int64
	netnsTapMs   int64
	restoreError string
}

// SlowWakeLogThreshold is the elapsed time above which a SUCCESSFUL wake
// still emits its phase breakdown. Failures always emit it regardless.
//
// 20s is deliberately below sched.ColdBootTimeout (35s): a wake that is
// merely slow should show up in the journal before it starts failing, so
// a creeping regression is visible while it is still succeeding.
const SlowWakeLogThreshold = 20 * time.Second

// wakePhases accumulates per-phase durations across Manager.Wake so a
// slow or failed boot can say WHERE the time went.
//
// This existed only for the success path before: bringUpTimings carried
// restore / netns+TAP / guest-ready onto the Instance for the vmmd
// WakeResponse (ADR-098 C11), and an error return discarded all of it.
// Everything BEFORE setupNetwork — lease acquire, runtime env prep,
// sidecar env prep — was never measured at all.
//
// The gap was not academic. On 2026-09-03 a prime cold boot ran 51s
// against a 35s budget and the journal held exactly two lines for the
// whole window: an unrelated "events: emit" and the final failure. The
// error surfaced at `ip addr add` inside setupNetwork with "context
// canceled", which says only that the deadline had already passed by
// then — not which phase consumed it. Raising the timeout on that
// evidence would have been a guess.
type wakePhases struct {
	start  time.Time
	last   time.Time
	phases []wakePhase
}

type wakePhase struct {
	name string
	ms   int64
}

func newWakePhases() *wakePhases {
	now := time.Now()
	return &wakePhases{start: now, last: now}
}

// mark closes the phase that ended now and names it. Cheap enough for
// the hot path: one time.Now plus an append per boundary.
func (w *wakePhases) mark(name string) {
	if w == nil {
		return
	}
	now := time.Now()
	w.phases = append(w.phases, wakePhase{name: name, ms: now.Sub(w.last).Milliseconds()})
	w.last = now
}

// attrs flattens the phases into slog key/values: total_ms plus one
// <phase>_ms per boundary, so the line is greppable per phase rather
// than needing a JSON array reader.
func (w *wakePhases) attrs() []any {
	if w == nil {
		return nil
	}
	out := make([]any, 0, 2+len(w.phases)*2)
	out = append(out, "total_ms", time.Since(w.start).Milliseconds())
	for _, p := range w.phases {
		out = append(out, p.name+"_ms", p.ms)
	}
	return out
}

type Instance struct {
	Lease  Lease
	Net    netns.Config
	Method WakeMethod // how it came up; a restore that fell back reads WakeColdBoot
	// ADR-098 C11: phase-decomposed wake timings stamped at the
	// three boundary sites inside Wake / bringUp so the vmmd
	// WakeResponse can carry the typed scalars (restore_ms /
	// netns_tap_ms / guest_ready_ms). 0 means "not measured"
	// (the gate from the previous version is non-additive).
	// RestoreMs is non-zero only on Method == WakeRestore.
	// NetnsTapMs is non-zero for both methods; the netns + TAP
	// setup runs in every wake. GuestReadyMs is the round-trip
	// from wake RPC return to the guest-init framework-ready
	// DGRAM (issue #470 / PR #543) — it carries the cost the
	// aggregate wake latency has been hiding.
	RestoreMs    int64
	NetnsTapMs   int64
	GuestReadyMs int64
	// RestoreError is populated when a requested snapshot restore falls
	// back to a successful cold boot. It is diagnostic-only: the fallback
	// remains a successful wake, but vmmd must retain the reason so an
	// operator can distinguish a stale snapshot from a guest resume-hook
	// failure (including its ACK code).
	RestoreError string
	// AppID is the apps.id UUID the instance was woken for.
	// UpdateEgressAllowlist (PR-B, ADR-031+033) uses it to walk
	// the live map keyed by app instead of by instance, so a
	// single PATCH on apps.egress_allowlist patches every live
	// instance of the app without the caller enumerating them.
	// Stored on the instance (not the Lease) so the Lease
	// stays allocator-owned and the Instance carries the
	// schedd-owned app identity.
	AppID string

	// AccountID is the apps.account_id the instance was woken
	// for (mirrors AppID). Captured from WakeRequest.AccountID
	// so the host renderer (ADR-119 redesign) can mint an
	// `account=` comment in the SNAT rule for audit + nag
	// dashboards. The Lease intentionally does NOT carry the
	// account ID — it's allocator-owned and instance-id-keyed;
	// the Instance carries the schedd-owned app identity.
	AccountID string

	// DeploymentID (issue #463 / ADR-069 / PR-B AC #1) is the
	// deployments.id UUID the instance was woken for. Captured at
	// Wake time from WakeRequest.DeploymentID so the vsock DGRAM
	// sidecar-init-failed dispatch path can flip the deployments
	// row to status='failed' with the literal CodeInitSidecarFailed
	// via state.Store.SetDeploymentFailed — no pg_notify bridge,
	// no apid round-trip. Empty on legacy single-workload wakes
	// (pre-PR-B callers don't carry a deployment_id on the wire);
	// the dispatch path is tolerant of "" and skips the flip.
	DeploymentID string

	// LivenessProbe preserves the per-deployment probe override across a
	// migration pause/resume cycle. It is copied from WakeRequest so ResumeVM
	// can restart the monitor with the same configuration.
	LivenessProbe json.RawMessage

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
	// StartupDeadlineS is the per-app readiness budget from the lifecycle
	// contract. 0 preserves the vmmd default for legacy callers.
	StartupDeadlineS int

	// WorkloadNames (issue #463 / ADR-069 / PR-B) is the set of
	// workload names whose cgroup child scopes vmmd wrote under
	// the per-instance scope at Wake time. Nil/empty = legacy
	// single-workload path (no child scopes exist). The set
	// starts with "main" and appends each sidecar name in
	// stability order (the order schedd sent in WakeRequest.
	// Sidecars). Captured so cleanup() can remove the child
	// scopes BEFORE the parent scope (which vmm.Kill does via
	// os.RemoveAll). The kernel cascade-removes children when
	// the parent goes away, but on a slow controller the parent
	// removal can race with leakcheck; the explicit pre-remove
	// shortens the leak window.
	WorkloadNames []string

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
	// TailCount (issue #667 / ADR-078) is the in-memory
	// mirror of the per-instance `tail_count` SQL column.
	// Incremented by the runner's WaitGroup each time a
	// waitUntil(promise) is registered (PR 3 does NOT wire
	// the increment — that lives in PR 3's runner-side tail
	// host on the guest; the host only sees decrements via
	// the 0x04 DGRAM receipt path). The schedd reaper (PR
	// 4) reads Instance.TailCount via state.GetInstance to
	// gate the park path: a wake with tail_count > 0 is
	// NOT idle-eligible. Mirrors state.Instance.TailCount
	// so the Manager is the single owner of the in-memory
	// copy.
	TailCount int
	// tailSecondsAccum (issue #667 / ADR-078) is the in-memory
	// accumulator of wall-clock seconds spent draining waitUntil
	// tasks since the previous Sampler tick. Each call to
	// MarkInstanceTailTerminal adds `ceil(elapsedMs / 1000)` to
	// this field; the meterd Sampler reads + atomically resets
	// it via ReadAndResetTailSeconds once per minute so the
	// per-instance tail_seconds is written to usage_minutes
	// (informational only — does NOT enter billing; pinned by
	// pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds).
	// Reset to 0 on every Wake. Mirrors pkg/state/types.go
	// where it lives on DailyUsage after the meterd rollup.
	tailSecondsAccum int64
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

	mu   sync.Mutex
	live map[string]*Instance
	// pendingProcessExits closes the small hand-off race between
	// JailerVMM reporting a child exit and Wake publishing the
	// instance into live. A process can pass readiness and exit
	// before the final live-map insert; retaining the exit under the
	// same mutex lets Wake fail closed instead of registering a dead
	// instance. Entries are consumed by Wake or cleared by cleanup.
	pendingProcessExits map[string]int
	// waking marks leases between acquisition and live-map publication.
	// ProcessExited records a pending marker only for this narrow phase;
	// an exit observed after explicit Destroy has removed live must not
	// be mistaken for a future Wake of the same id.
	waking     map[string]struct{}
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
	// cooldownByDeployment (issue #554 closure / ADR-078, code
	// review #725 finding F1) is the per-deployment liveness
	// cooldown stamp map. Keyed on deployments.id (the
	// deploymentID that survives across cold boots: schedd
	// threads it into the new Instance row at CreateInstance and
	// Wake stamps it onto the live record at BringUp, so the
	// cold-boot replacement can read the stamp the dying instance
	// wrote). The previous design stamped the dying Instance and
	// Park deleted the entry — the replacement saw zero and the
	// gate was structurally a no-op in production. Stamps are
	// written by ReportLivenessFailed; reads are by the
	// per-instance liveness loop via
	// LastLivenessDestroyAtForDeployment. Guarded by mu.
	cooldownByDeployment map[string]time.Time
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
	// wakePhaseMetrics (ADR-098 C11) is the optional
	// `vmmd_wake_phase_duration_seconds` histogram the vmmd cmd
	// wires via SetWakePhaseMetrics. nil-safe: Wake calls
	// ObserveWakePhase on a nil-safe receiver so unit tests that
	// drive Manager directly without metrics don't need a stub.
	wakePhaseMetrics *WakePhaseMetrics
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
	// tailTerminalStamper (issue #667 / ADR-078) is the
	// optional SQL persistence seam the vmmd cmd wires via
	// WithTailTerminalStamper. nil-safe:
	// MarkInstanceTailTerminal calls DecrementInstanceTailCount
	// on a nil-safe receiver so unit tests that drive Manager
	// directly without a stamper don't need a stub. The
	// schedd reaper (PR 4) reads back the `tail_count` column
	// to gate the park path on tail_count == 0.
	tailTerminalStamper TailTerminalStamper
	// livenessMetrics (issue #554 / ADR-078) is the optional
	// Prometheus collector pair the vmmd cmd wires via
	// WithLivenessMetrics. nil-safe:
	// livenessRecorder.ObserveProbe / SetConsecutiveFailures
	// guard on nil so unit tests that construct a Manager
	// without metrics don't need a stub. The two collectors
	// (vmmd_guest_liveness_probe_seconds histogram,
	// vmmd_guest_liveness_consecutive_failures per-instance
	// gauge) are the load-bearing observability surface: the
	// dashboard's "liveness probe: success / failure" panel
	// queries the histogram, the "live consecutive-failure
	// distribution" panel queries the gauge.
	livenessMetrics *LivenessMetrics
	// livenessRelay (issue #554 / ADR-078) is the optional
	// function the vmmd cmd wires via WithLivenessSink. The
	// poll goroutine calls it whenever the consecutive-failure
	// counter reaches the per-plan N (default 3). The relay is
	// how vmmd plumbs the failure to schedd's
	// Engine.DestroyForLivenessFailure — schedd is the only
	// writer to instances (spec §6), so the destroy path
	// MUST live on the schedd side. nil-safe: the poll
	// goroutine guards on nil so a missing wire (a unit
	// test that doesn't construct a relay) is a no-op.
	//
	// The relay is invoked from a per-instance poll goroutine
	// whose lifecycle is owned by livenessRegistry (see below)
	// and started by Manager.bringUp / cancelled by Manager.Park
	// — same shape as the framework_ready DGRAM listener.
	livenessRelay LivenessFailedSink
	// workloadOOMRelay (Cluster C / ADR-121) is the optional
	// function the vmmd cmd wires via WithWorkloadOOMSink. The
	// framework_ready_recv dispatcher calls it whenever the
	// guest-init emits a DGRAM type 0x05 on port 1027
	// (guest-init's cgroup.events "oom_kill" listener observed
	// a kill on the per-VM workload cgroup v2 leaf). The relay
	// is how vmmd plumbs the observed (peakMB, planMB) payload
	// to schedd's Engine.DestroyForWorkloadOOMFailure — schedd
	// is the only writer to instances (spec §6), AND the only
	// component that holds the deployment lock needed to
	// stamp CodeAppRuntimeOOM via SetDeploymentFailedEx. nil-safe:
	// the dispatcher guards on nil so a missing wire (a unit
	// test that doesn't construct a relay) is a no-op.
	//
	// The relay is invoked from the framework_ready_recv DGRAM
	// loop whose lifecycle is owned by the per-instance
	// manager bringUp — same shape as the liveness poll
	// goroutine. Best-effort: a failed relay is logged + dropped
	// because the workload is already dead and the VM is about
	// to be torn down by the manager's destroy path.
	workloadOOMRelay WorkloadOOMSink
	// livenessRegistry (issue #554 / ADR-078 / PR review fix)
	// is the per-instance poll goroutine registry the cmd/vmmd
	// main loop wires via WithLivenessProbes. The Manager owns
	// the lifecycle (start on BringUp success, cancel on Park
	// or Destroy) so the per-VM goroutine count tracks the live
	// instance count. nil-safe: a Manager constructed without
	// the registry (a unit test, or a default-local vmmd run)
	// skips the start/cancel calls entirely.
	livenessRegistry *LivenessRegistry
	// livenessDefaultCfg is the per-plan Hobby/Pro/Scale default
	// (api.Plan.LivenessPeriodSeconds / LivenessConsecutiveFailures /
	// LivenessCooldownSeconds) the cmd/vmmd main loop resolves at
	// construction and hands to WithLivenessProbes. Consumed by
	// startLivenessLoop as the fallback when the WakeRequest omits
	// a per-deployment override.
	livenessDefaultCfg LivenessProbeConfig
	// livenessStarter is the cmd-level helper the cmd/vmmd main
	// loop attaches via WithLivenessProbeStarter. Builds the
	// per-instance goroutine (vsock dial + JSON envelope) and
	// returns its cancel func; the Manager calls it from
	// startLivenessLoop and registers the cancel via
	// livenessRegistry. nil = loop body not wired (cmd default-
	// local vmmd run, or a unit test); startLivenessLoop logs
	// Warn and returns.
	livenessStarter LivenessProbeStarter
	// lifecycleCtx is vmmd's daemon context. Per-instance liveness loops
	// must be children of this context, not of the short-lived Wake RPC
	// context; the latter is normally canceled as soon as Wake returns.
	lifecycleCtx context.Context
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
	// wakeFailureMetrics (issue #1059 / ADR-127) holds the
	// OpsMetrics handle that backs the *_wake_failure_total{box,
	// reason} counter and the *_wake_latency_seconds{box, phase}
	// histogram — the operator-facing wake-failure surface. Like
	// imageScanMetrics this is the per-daemon handle (vmmd
	// producers; schedd / builderd consumers), pre-instantiated at
	// the closed (box × reason) and (box × phase) sets in
	// pkg/wire/metrics.go. nil-safe: the Wake hook sites guard
	// the increment behind a method-receiver check so a unit
	// test that doesn't wire ops keeps working. The
	// single-registry pattern means this field is unique to
	// vmmd's OpsMetrics and builderd's / schedd's handles hold
	// their own (no cross-daemon sharing).
	wakeFailureMetrics *wire.OpsMetrics
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
	// operatorBundle (issue #679 / PR-A) is the operator-managed
	// egress CIDR set every tenant's allowlist is merged with.
	// Sorted + dedup'd at SIGHUP-reload time. operatorBundleMu
	// guards the field; the lock is held briefly for the
	// read-then-merge path at Wake and at live-patch, so the
	// hot path (per-Wake / per-PATCH) does not contend on the
	// bundle's contents. nil/empty slice is the default — the
	// merge is a no-op, matching today's behaviour.
	operatorBundle   []netip.Prefix
	operatorBundleMu sync.RWMutex
	// perAppAllowlist (issue #679 / PR-A) is the per-app
	// POST/PATCH-written egress slice, keyed by appID. The Wake
	// and UpdateEgressAllowlist paths write here BEFORE the
	// operator-bundle merge so SetEgressOperatorBundle can read
	// the authoritative per-app slice on a subsequent bundle
	// reload (otherwise reading inst.Net.EgressAllowlist would
	// re-merge the previous operator bundle on top of the new
	// one, breaking operator subtraction). The map is purely
	// an authoritative-read cache; the rendered / live netns
	// state still lives in inst.Net.EgressAllowlist.
	perAppAllowlist map[string][]netip.Prefix
	// perAppAllowlistMu guards perAppAllowlist. Held briefly
	// for read-then-set; the Wake path is the only writer in
	// the steady-state (UpdateEgressAllowlist also writes).
	perAppAllowlistMu sync.RWMutex
	// staticEgressAliases (ADR-119) is the authoritative alias
	// set on br-tenants, keyed by appID. The SIGHUP-driven
	// reload path (cmd/vmmd/egress_static_ip_bundle.go) writes
	// here through SetStaticEgressIPAliases; the field is read
	// by tests + diagnostic endpoints to confirm the bridge
	// alias set matches the operator TOML. The actual `ip addr
	// add/del` happens inside SetStaticEgressIPAliases so the
	// in-memory cache and the kernel state stay in lock-step.
	staticEgressAliases map[string]netip.Addr
	// staticEgressAliasesMu guards staticEgressAliases. Held
	// briefly for the read-then-diff path inside
	// SetStaticEgressIPAliases. SIGHUP redelivery lands here;
	// without the lock the redelivered entries could race with
	// an in-flight Wake that just aliased a fresh IP.
	staticEgressAliasesMu sync.Mutex
	// perAppStaticIP (ADR-119 redesign) is the per-app POST/PATCH-
	// written static egress IP, keyed by appID. The Wake
	// and UpdateStaticEgressIP paths write here BEFORE the
	// host renderer is rebuilt, so a subsequent host renderer
	// reload (SIGHUP) can re-emit the SNAT rule from the
	// authoritative source. The map is purely an authoritative-
	// read cache; the rendered / live netns state is now
	// driven by the per-VM host IP map (perVMHostIP) instead
	// of per-VM netns patches. nil pointer = no static pin for
	// that app.
	perAppStaticIP map[string]*netip.Addr
	// perAppStaticIPMu guards perAppStaticIP. Held briefly
	// for read-then-set; the Wake path is the only writer
	// in the steady-state (UpdateStaticEgressIP also
	// writes).
	perAppStaticIPMu sync.RWMutex
	// perVMHostIP (ADR-119 redesign) is the live per-VM
	// host IP map keyed by appID. The host renderer
	// consumes the (perVMHostIP, customerIP) tuple for each
	// live VM of a static-egress-pinned app. The map is
	// populated by:
	//   1. SetStaticEgressIPAliases (operator bundle reload)
	//   2. RegisterStaticEgressIPForVM (called on Wake)
	//   3. UnregisterStaticEgressIPForVM (called on Park /
	//      Destroy, on the customer-clear path)
	// Source of truth is the pkg/fcvm static-egress pool
	// (alloc.go::AcquireStaticEgressIP / ReleaseStaticEgressIP);
	// this map is the vmmd Manager's view of that pool,
	// updated on every mutation.
	perVMHostIP map[string]netip.Addr
	// perVMHostIPMu guards perVMHostIP. Same discipline as
	// perAppStaticIPMu (brief read-then-set locks).
	perVMHostIPMu sync.RWMutex
	// hostRenderer is the seam for vmmd's egress watcher to
	// push fresh StaticEgressRules into the host renderer.
	// Set by NewManager via the wire-up path in cmd/vmmd/main.go;
	// nil for unit tests that don't drive the host renderer.
	// The watcher path is fire-and-forget — failures log a
	// Warn and continue (the next cache event will re-trigger
	// the reload).
	hostRenderer HostRenderer
}

// HostRenderer is the narrow seam the vmmd Manager uses to push
// a fresh static-egress rule list into the host renderer
// (cmd/vmmd/egress_watcher.go::liveHostPolicy). The interface
// is the only way the Manager touches the renderer — the actual
// nftables write is gated through the existing watcher's
// staging-dir + atomic-replace pipeline.
type HostRenderer interface {
	// Render writes the current HostPolicy (including the
	// freshly-pushed StaticEgressRules) to the staging file,
	// validates via nft -c -f, atomic-replaces /etc/nftables.conf,
	// and reloads the kernel ruleset. Returns nil on a clean
	// reload; non-nil on any step (caller logs + continues).
	Render(ctx context.Context) error
	// SetStaticEgressRules replaces the renderer's
	// StaticEgressRules slice. Cheap (single pointer swap
	// under a lock); the next Render() picks up the new
	// rules.
	SetStaticEgressRules(rules []netns.StaticEgressRule)
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
		alloc:               NewAllocator(),
		run:                 run,
		vmm:                 vmm,
		paths:               paths,
		fcVersion:           fcVersion,
		log:                 log,
		live:                make(map[string]*Instance),
		pendingProcessExits: make(map[string]int),
		waking:              make(map[string]struct{}),
		exportDirs:          make(map[string]string),
		// Issue #470 / PR #470-FU-B: O(1) CID→instance lookup
		// for the framework_ready DGRAM receipt path. See the
		// cidToID field comment for the lifecycle.
		cidToID:              make(map[uint32]string),
		cooldownByDeployment: make(map[string]time.Time),
		lifecycleCtx:         context.Background(),
		// ADR-119 redesign: per-app static-egress caches. The
		// perAppStaticIP map (per-app pin) stays; perVMHostIP
		// (per-VM host IP) is the new map that drives the host
		// renderer.
		perAppStaticIP:       make(map[string]*netip.Addr),
		perVMHostIP:          make(map[string]netip.Addr),
		metrics:              metrics,
		conntrackCap:         api.ConntrackCapProbe(),
		characterizationWait: api.CharacterizationHostDeadline,
	}
}

// SetHostRenderer attaches the host renderer seam. The vmmd main
// wiring calls this once at boot, after NewManager, before
// serving traffic. The Manager calls Render() on every cache
// mutation (Wake, Teardown, drift, SIGHUP) so the live
// /etc/nftables.conf reflects the active static-egress pin set.
func (m *Manager) SetHostRenderer(r HostRenderer) {
	m.hostRenderer = r
}

// rebuildHostStaticEgressRules rebuilds the host renderer's
// StaticEgressRules from the active per-VM host IP map and the
// per-app customer IP cache, then triggers a host ruleset
// reload. Called on every cache mutation (Wake, Teardown, drift,
// SIGHUP).
//
// The rule list is the cross-product of perVMHostIP and
// perAppStaticIP, filtered to apps that have BOTH a non-nil
// per-VM host IP entry (the host renderer needs a `ip saddr`
// source) AND a non-nil customer IP (the SNAT target). Apps
// missing either field are silently dropped — the per-VM host
// IP is set on Wake, the customer IP is set on the apid PUT.
//
// The reload is fire-and-forget: a failure logs a Warn and
// returns; the next cache event re-triggers the rebuild. The
// renderer reload is gated through the staging-dir + atomic-
// replace pipeline in cmd/vmmd/egress_watcher.go, so a failed
// Render does NOT break the live ruleset.
func (m *Manager) rebuildHostStaticEgressRules(ctx context.Context) {
	if m.hostRenderer == nil {
		return
	}
	m.perVMHostIPMu.RLock()
	perVM := make(map[string]netip.Addr, len(m.perVMHostIP))
	for k, v := range m.perVMHostIP {
		perVM[k] = v
	}
	m.perVMHostIPMu.RUnlock()

	m.perAppStaticIPMu.RLock()
	perApp := make(map[string]*netip.Addr, len(m.perAppStaticIP))
	for k, v := range m.perAppStaticIP {
		perApp[k] = v
	}
	m.perAppStaticIPMu.RUnlock()

	// Look up account IDs by walking live instances. The
	// account→app mapping is read from the live state; the
	// reservation table (alloc.go) owns the per-VM host IP
	// slot but not the account_id (the worker writes it at
	// acquire time, and SetStaticEgressIPAliases from the
	// operator bundle re-asserts it on SIGHUP).
	accountByApp := make(map[string]string, len(perVM))
	m.mu.Lock()
	for _, inst := range m.live {
		if _, ok := perVM[inst.AppID]; ok {
			accountByApp[inst.AppID] = inst.AccountID
		}
	}
	m.mu.Unlock()

	rules := make([]netns.StaticEgressRule, 0, len(perVM))
	for appID, perVMHostIP := range perVM {
		ip := perApp[appID]
		if ip == nil {
			continue
		}
		rules = append(rules, netns.StaticEgressRule{
			PerVMHostIP: perVMHostIP,
			CustomerIP:  *ip,
			AccountID:   accountByApp[appID],
			AppID:       appID,
		})
	}
	// Install the rules into the live host policy via
	// atomic swap. The current pointer is read-then-copied,
	// the new rules are written into the copy, and the new
	// pointer is published. The watcher reads the new
	// pointer on the next Render cycle.
	cur := netns.ActiveHostPolicyForRender()
	if cur != nil {
		next := *cur
		next.StaticEgressRules = rules
		netns.SwapActiveHostPolicy(next)
	} else {
		m.log.Warn("fcvm: rebuildHostStaticEgressRules: ActiveHostPolicy pointer is nil; rules not installed")
	}
	if err := m.hostRenderer.Render(ctx); err != nil {
		m.log.Warn("fcvm: rebuildHostStaticEgressRules reload failed; live ruleset unchanged",
			"err", err, "rules", len(rules))
	}
}

// RegisterStaticEgressIPForVM is called on Wake to associate the
// per-VM host IP (allocated by alloc.go::AcquireStaticEgressIP at
// the customer's apid PUT time) with the appID. Subsequent
// rebuildHostStaticEgressRules calls surface the (perVMHostIP,
// customerIP) tuple to the host renderer.
func (m *Manager) RegisterStaticEgressIPForVM(appID string, perVMHostIP netip.Addr) {
	if appID == "" || !perVMHostIP.IsValid() {
		return
	}
	m.perVMHostIPMu.Lock()
	m.perVMHostIP[appID] = perVMHostIP
	m.perVMHostIPMu.Unlock()
}

// UnregisterStaticEgressIPForVM is called on the customer-clear
// path (DELETE /v1/apps/{slug}/static-egress-ip) to remove the
// per-VM host IP association. A live VM may still be running for
// the app — the per-VM host IP is NOT released from the alloc.go
// pool until the customer subsequently clears the pin via the
// apid API (the alloc.go side Release is driven by the apid
// handler, not by per-VM teardown).
func (m *Manager) UnregisterStaticEgressIPForVM(appID string) {
	if appID == "" {
		return
	}
	m.perVMHostIPMu.Lock()
	delete(m.perVMHostIP, appID)
	m.perVMHostIPMu.Unlock()
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

// SetWakeFailureMetrics wires the OpsMetrics handle the wake-failure
// hook sites feed into (issue #1059 / ADR-127). The shape mirrors
// SetImageScanMetrics verbatim — nil-safe, single-registry, and
// required-at-startup-but-deferred on the wire so unit tests that
// don't wire ops keep passing. The handle registers the
// *_wake_failure_total{box, reason} counter and the
// *_wake_latency_seconds{box, phase} histogram at NewOpsMetrics time
// (pre-instantiation loop, pkg/wire/metrics.go); the Manager
// invocation is the only producer for `box = "local"` in production —
// schedd uses its own. NOT safe to call concurrently with Wake;
// cmd/vmmd wires it once at startup before serving traffic.
func (m *Manager) SetWakeFailureMetrics(ops *wire.OpsMetrics) {
	m.wakeFailureMetrics = ops
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

// SetWakePhaseMetrics (ADR-098 C11) wires the optional
// vmmd_wake_phase_duration_seconds histogram. nil-safe — Wake
// observes on a nil-check. The vmmd cmd constructs a fresh
// registry via NewWakePhaseMetrics and passes it here.
// Returns m for fluent chain continuation.
func (m *Manager) SetWakePhaseMetrics(wpm *WakePhaseMetrics) *Manager {
	m.wakePhaseMetrics = wpm
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

// TailOutcome (issue #667 / ADR-078) is the closed set of
// terminal states a runner-side waitUntil(promise) task can
// reach. Mirrors the wire-byte encoding guest-init ships on
// DGRAM port 1027 (the second byte after the 0x04 type
// discriminator — see ADR-078 §"Wire format" + the encoding
// helpers in guest/init/sidecar_events_proxy_linux.go). The
// closed set keeps the metric labels bounded (PR 5 adds the
// histogram with `outcome ∈ {completed, failed, timeout}`).
type TailOutcome byte

const (
	// TailOutcomeCompleted — the customer's promise resolved
	// successfully (no error).
	TailOutcomeCompleted TailOutcome = 0x01
	// TailOutcomeFailed — the customer's promise rejected
	// (handler error). Surfaced via the runner's stderr +
	// wake.tail_failed audit row but never the HTTP response.
	TailOutcomeFailed TailOutcome = 0x02
	// TailOutcomeTimeout — the runner-side context.WithTimeout
	// fired before the promise resolved. The customer process
	// is killed by the runner's normal cmd.Wait() shutdown
	// after the tail host drains.
	TailOutcomeTimeout TailOutcome = 0x03
)

// TailTerminalStamper (issue #667 / ADR-078) is the minimal
// write surface the Manager needs to record a tail task terminal
// event. Mirrors FrameworkReadyStamper in shape and intent:
// local-to-pkg/fcvm to avoid a full pkg/state import on the hot
// path; the cmd/vmmd wiring adapts the pgstore directly.
//
// Errors propagate to the receipt path's Warn log — the in-memory
// TailCount on the live Instance is the load-bearing signal for
// the runner's WaitGroup view; the SQL column is the durable
// mirror the schedd reaper reads (PR 4). A transient PG hiccup
// must not lose the receipt; the 5s watchdog in snapshotAndPark
// force-parks regardless.
type TailTerminalStamper interface {
	// DecrementInstanceTailCount atomically decrements the
	// per-instance `tail_count` column by 1 (floored at 0 by
	// the SQL GREATEST(…, 0) guard). Returns ErrNotFound when
	// the instance row is missing.
	DecrementInstanceTailCount(ctx context.Context, id string) error
}

// WithTailTerminalStamper (issue #667 / ADR-078) attaches the
// SQL-persistence seam so the receipt path can mirror the
// in-memory tail count to the `instances.tail_count` column.
// Same wiring pattern as WithFrameworkReadyStamper: optional,
// nil-safe, no-ops when the cmd binary doesn't wire it.
// Production wires *pgstore.PgStore satisfying the interface.
// Returns the *Manager so callers can chain.
func (m *Manager) WithTailTerminalStamper(s TailTerminalStamper) *Manager {
	m.tailTerminalStamper = s
	return m
}

// LivenessFailedSink is the function signature the vmmd poll goroutine
// invokes when the consecutive-failure counter reaches the per-plan N
// (issue #554 / ADR-078). The relay is the boundary between vmmd
// (host-side poll goroutine) and schedd (state machine owner). schedd
// constructs the closure at daemon startup and passes it via
// Manager.WithLivenessSink; the vmmd poll goroutine calls it directly.
//
// Reason is a stable short string from the probe set {timeout,
// conn_refused, conn_err, non_200, unauthorized}, or one of the
// source classifications {liveness_infrastructure,
// liveness_process_exited}. The probe set is the same closed set the
// vmmd_guest_liveness_probe_seconds histogram emits. Process-exit
// reconciliation also uses the process_exited classification. The schedd
// Engine.DestroyForLivenessFailure uses the reason to populate the
// audit event's data JSON.
type LivenessFailedSink func(ctx context.Context, instanceID, reason string)

// WorkloadOOMSink (Cluster C / ADR-121) is the function signature
// the vmmd framework_ready_recv dispatcher invokes when the
// guest-init emits a DGRAM type 0x05 on port 1027 (workload OOM
// detected on the per-VM cgroup v2 leaf). The relay is the
// boundary between vmmd (host-side DGRAM loop) and schedd (state
// machine owner). schedd constructs the closure at daemon startup
// and passes it via Manager.WithWorkloadOOMSink; the framework_ready
// recv dispatcher calls it directly.
//
// peakMB and planMB are the observed payload — the guest-init
// samples memory.events for the post-kill `current` (or `high`
// watermark) and converts to MiB before emission. The schedd
// Engine.DestroyForWorkloadOOMFailure uses these to populate the
// whycopy Observed closure (Why + Fix templated with the peak +
// plan cap). Unit-test-friendly: nil receiver is a no-op.
type WorkloadOOMSink func(ctx context.Context, instanceID string, peakMB, planMB int)

// LivenessProbeStarter (issue #554 / ADR-078) is the cmd-level
// goroutine-launcher the cmd/vmmd main loop attaches via
// WithLivenessProbeStarter. Manager.startLivenessLoop calls it with
// the resolved per-instance config + lease slot; the starter resolves
// the per-instance vsock proxy path and builds the loop body (vsock
// handshake, JSON envelope, classification) and
// returns the cancel func the Manager registers with the
// livenessRegistry. A nil return is treated as "loop not started"
// so a unit test that doesn't wire the starter is a no-op rather
// than a panic.
//
// The signature takes the parent ctx (cmd vmmd lifecycle), the
// instance id, the lease slot used for instance bookkeeping, the
// deployment id (for the cooldown gate stamp key — survives
// across cold boots), and the resolved cfg. Returns the cancel
// func (or nil). Empty deploymentID is allowed (legacy pre-PR-B
// callers that don't carry deployment_id on the wire); the gate
// falls back to the bypass branch in that case.
type LivenessProbeStarter func(ctx context.Context, instance string, slot int, deploymentID string, cfg LivenessProbeConfig) context.CancelFunc

// WithLivenessProbeStarter attaches the cmd-level loop launcher
// (issue #554 / ADR-078). Same nil-safe + chaining pattern as the
// other Manager wire options. cmd/vmmd main wires the closure
// that builds the liveness_recv.livenessProbeLoop and spawns its
// run goroutine; Manager.startLivenessLoop calls the closure and
// registers the returned cancel func with the livenessRegistry.
func (m *Manager) WithLivenessProbeStarter(starter LivenessProbeStarter) *Manager {
	m.livenessStarter = starter
	return m
}

// WithLifecycleContext attaches the vmmd daemon context used for background
// per-instance work. Wake callers have short RPC lifetimes; using those
// contexts for liveness monitoring stops the monitor immediately after a
// successful wake. The daemon context keeps the monitor alive until the
// instance is explicitly torn down or vmmd shuts down.
func (m *Manager) WithLifecycleContext(ctx context.Context) *Manager { //nolint:contextcheck // lifecycle context is intentionally retained by vmmd for background work beyond the RPC lifetime.
	if ctx == nil {
		ctx = context.Background()
	}
	m.lifecycleCtx = ctx
	return m
}

// ProcessExited reports an unexpected Firecracker process exit to schedd.
// The Manager keeps the live entry intact while the relay runs so schedd's
// normal Destroy path can perform the authoritative state transition and
// resource cleanup. If no relay is wired (development/test mode), it falls
// back to local cleanup so the allocator and network cannot leak.
func (m *Manager) ProcessExited(instance string, exitCode int) {
	if m == nil || instance == "" {
		return
	}
	m.mu.Lock()
	inst, live := m.live[instance]
	relay := m.livenessRelay
	lifecycle := m.lifecycleCtx
	_, waking := m.waking[instance]
	if !live && waking {
		if m.pendingProcessExits == nil {
			m.pendingProcessExits = make(map[string]int)
		}
		m.pendingProcessExits[instance] = exitCode
	}
	m.mu.Unlock()
	if !live {
		// An exit during the Wake hand-off is consumed by Wake. An
		// exit after explicit Park/Destroy removed live is expected
		// teardown and is deliberately ignored.
		return
	}
	if inst.Lease.IsBuilder {
		// Builderd owns the expected process-exit → artifact-export
		// sequence. The VMM filters normal builder exits too, but keep
		// this guard for alternate VMM implementations and tests.
		return
	}

	// Stop the health loop before notifying schedd. Otherwise the
	// dead process can produce a second, slower liveness failure while
	// the first destroy is still in flight.
	m.DeleteLivenessConsecutiveFailures(instance)
	m.cancelLivenessLoop(instance)

	if lifecycle == nil {
		lifecycle = context.Background()
	}
	m.ReportLivenessFailed(context.WithoutCancel(lifecycle), instance, "process_exited")
	if relay != nil {
		return
	}

	m.mu.Lock()
	inst, ok := m.live[instance]
	if ok {
		delete(m.live, instance)
		delete(m.cidToID, GuestVsockCID(inst.Lease.Slot))
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	m.cleanup(context.WithoutCancel(lifecycle), inst.Lease, inst.Net, inst.WorkloadNames)
	m.log.Warn("firecracker process exited without schedd relay",
		"instance", instance, "exit_code", exitCode)
}

// WithLivenessMetrics attaches the Prometheus collector pair
// (issue #554 / ADR-078). Same nil-safe + chaining pattern as
// WithFrameworkReady: optional, no-ops when the cmd binary doesn't
// wire it. Returns the *Manager so callers can chain.
func (m *Manager) WithLivenessMetrics(lm *LivenessMetrics) *Manager {
	m.livenessMetrics = lm
	return m
}

// WithLivenessSink attaches the relay the vmmd poll goroutine calls
// when a consecutive-failure counter reaches the per-plan N
// (issue #554 / ADR-078). nil-safe: the poll goroutine guards on nil
// so a missing wire (unit tests, default-local vmmd) is a no-op.
// Returns the *Manager so callers can chain.
func (m *Manager) WithLivenessSink(relay LivenessFailedSink) *Manager {
	m.livenessRelay = relay
	return m
}

// WithWorkloadOOMSink (Cluster C / ADR-121) attaches the relay the
// vmmd framework_ready_recv dispatcher calls when a guest-init
// DGRAM type 0x05 arrives on port 1027 (workload OOM-kill detected
// on the per-VM cgroup v2 leaf). nil-safe: the dispatcher guards
// on nil so a missing wire (unit tests, default-local vmmd) is a
// no-op. Returns the *Manager so callers can chain.
//
// Mirrors WithLivenessSink's signature shape (ctx + instance id +
// observed payload); the only difference is the payload shape
// (peakMB + planMB ints instead of a closed-set reason string).
func (m *Manager) WithWorkloadOOMSink(relay WorkloadOOMSink) *Manager {
	m.workloadOOMRelay = relay
	return m
}

// ReportWorkloadOOM (Cluster C / ADR-121) is the vmmd-side
// invocation of the WorkloadOOMSink. Called by the framework_ready
// DGRAM dispatcher when the guest-init emits a workload OOM
// signal. Schedd is the consumer
// (Engine.DestroyForWorkloadOOMFailure); vmmd never touches the
// DB. Safe on a nil relay — the missing-wire path is a no-op so a
// unit test that doesn't construct a relay doesn't have to stub
// one.
//
// Best-effort: the workload is dead at the time of the call, so a
// failed relay is logged + dropped. The relay's own context
// cancellation (from vmmd shutdown) is honoured by the closing
// goroutine.
func (m *Manager) ReportWorkloadOOM(ctx context.Context, instanceID string, peakMB, planMB int) {
	if m.workloadOOMRelay == nil {
		return
	}
	m.workloadOOMRelay(ctx, instanceID, peakMB, planMB)
}

// ObserveLivenessProbe records one probe's wall-clock duration with
// the given outcome class. Safe on a nil receiver (the underlying
// livenessMetrics is nil-safe). Empty outcome is collapsed to
// "unknown" inside the metrics struct.
func (m *Manager) ObserveLivenessProbe(outcome string, seconds float64) {
	m.livenessMetrics.ObserveProbe(outcome, seconds)
}

// SetLivenessConsecutiveFailures records the current consecutive-failure
// count for an instance. Safe on a nil receiver.
func (m *Manager) SetLivenessConsecutiveFailures(instance string, count int) {
	m.livenessMetrics.SetConsecutiveFailures(instance, count)
}

// DeleteLivenessConsecutiveFailures drops the per-instance gauge entry.
// Called on instance teardown so the high-cardinality {instance} label
// set doesn't accumulate dead instances. Safe on a nil receiver.
func (m *Manager) DeleteLivenessConsecutiveFailures(instance string) {
	m.livenessMetrics.DeleteConsecutiveFailures(instance)
}

// ReportLivenessFailed (issue #554 / ADR-078) is the vmmd-side
// invocation of the LivenessFailedSink. Called by the per-instance
// poll goroutine once the consecutive-failure counter reaches the
// per-plan N. Schedd is the consumer (Engine.DestroyForLivenessFailure);
// vmmd never touches the DB. A nil relay skips the schedd notification,
// but the local cooldown stamp still records the failure for tests and
// default-local callers.
//
// Side effect (issue #554 closure / ADR-078 cooldown gate, code
// review #725 finding F1): stamps the Manager's
// cooldownByDeployment[deploymentID] = now so the next
// liveness-loop incarnation on the cold-boot replacement instance
// (which inherits deploymentID from schedd's CreateInstance) can
// short-circuit fires within the configured CooldownSeconds
// window. Stamping on the dying Instance was structurally broken:
// Park deletes the live-map entry and the replacement carries a
// fresh zero-valued Instance{}, so the gate was a no-op in
// production. See Manager.LastLivenessDestroyAtForDeployment +
// cmd/vmmd/liveness_recv.go's cooldown gate for the consumer.
//
// Legacy call sites that stamped Instance.LastLivenessDestroyAt
// were removed; the field itself is gone (the dying instance is
// about to be Parked anyway).
func (m *Manager) ReportLivenessFailed(ctx context.Context, instanceID, reason string) { //nolint:contextcheck // the relay must outlive the probe loop that it tears down.
	if m == nil || instanceID == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if m.livenessRelay != nil {
		// The relay synchronously asks schedd to destroy the instance. That
		// destroy RPC cancels vmmd's liveness loop as part of teardown; passing
		// the loop context through would cancel the RPC itself midway through
		// the state transition and leave a stale RUNNING row. Preserve values
		// for tracing, but detach cancellation/deadlines from the monitor.
		m.livenessRelay(context.WithoutCancel(ctx), instanceID, reason)
	}
	// Stamp cooldownByDeployment even when no relay is wired so a
	// local/test manager preserves the same restart-cooldown semantics.
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.live[instanceID]
	if !ok {
		// The instance has already been Parked between the
		// sink call and the stamp; the gate is moot for this
		// fire (no replacement will read it). Log + skip.
		m.log.Debug("liveness: stamp skipped (instance not live)",
			"instance", instanceID, "reason", reason)
		return
	}
	if inst.DeploymentID == "" {
		// Legacy wake path (pre-PR-B) carries no deployment_id
		// on the wire. The gate cannot key on "" (every legacy
		// wake would collide), so we skip the stamp and the
		// gate falls back to the AlwaysSample behaviour for
		// this instance. Logged Warn so operators can spot
		// legacy callers in the wild.
		m.log.Debug("liveness: stamp skipped (no deployment_id on instance)",
			"instance", instanceID, "reason", reason)
		return
	}
	m.cooldownByDeployment[inst.DeploymentID] = time.Now()
}

// LivenessProbeConfig is the per-instance configuration the schedd-side
// resolves from the deployment row's `override_liveness_probe` JSONB
// (cmd/apid/handlers_sidecars.go) merged with the parent app's plan
// defaults (issue #554 / ADR-078). The cmd/vmmd main loop constructs
// a copy of this struct per Wake and hands it to the LivenessRegistry
// alongside the per-VM CID + instance id.
//
// PeriodSeconds == 0 disables the probe for that instance (Free plan,
// or a per-deployment override that explicitly turns it off). The
// cmd/vmmd liveness_recv.run entry point short-circuits on
// cfg.PeriodSeconds <= 0.
//
// IdleResetOnDestroy is the user-confirmed choice (issue #554
// §Implementation notes): schedd's Engine.DestroyForLivenessFailure
// resets the per-instance idle timer on the destroyed row, so the
// reaper grace restarts on the cold-boot instance, not the wedged one.
// Surfaced as a boolean here so the schedd side encodes the policy
// without vmmd hard-coding it.
type LivenessProbeConfig struct {
	Path                string
	PeriodSeconds       int
	ConsecutiveFailures int
	CooldownSeconds     int
	IdleResetOnDestroy  bool
}

// LivenessRegistry owns the per-instance liveness-probe poll
// goroutines (issue #554 / ADR-078). One loop per instance, keyed
// by instance id; the Manager's bringUp / Park call sites own
// the lifecycle (start on Wake success, cancel on Park or Destroy).
// The registry itself is data-only — the cmd/vmmd main loop
// constructs one and passes it to Manager.WithLivenessProbes.
//
// The registry's design mirrors pkg/fcvm.cidToID's per-instance
// reverse index for the framework_ready DGRAM receipt path: the
// data structure is in pkg/fcvm because it's referenced from
// Manager methods, but the goroutine bodies live in cmd/vmmd
// because they bind cmd-level concerns (vsock dial, JSON wire
// envelope, metrics access via the Manager).
//
// goroutine-safe: mu guards the loops map. cancelLoop is idempotent
// — repeated calls on the same instance id are a no-op so a Park
// racing a Destroy can't panic.
type LivenessRegistry struct {
	mu     sync.Mutex
	loops  map[string]livenessLoopRegistration
	nextID uint64
}

type livenessLoopToken struct{ id uint64 }

// livenessLoopTokenContextKey keeps the registry generation private to the
// manager/cmd boundary. LivenessProbeConfig is part of the public Manager
// API, so lifecycle bookkeeping must not grow an unexported field that would
// break external unkeyed composite literals.
type livenessLoopTokenContextKey struct{}

type livenessLoopRegistration struct {
	token  *livenessLoopToken
	cancel context.CancelFunc
}

// NewLivenessRegistry constructs an empty registry. cmd/vmmd main
// calls this once at startup; tests can construct one ad-hoc.
func NewLivenessRegistry() *LivenessRegistry {
	return &LivenessRegistry{loops: make(map[string]livenessLoopRegistration)}
}

// prepareProbeLoop reserves the registry slot for a new loop and cancels
// any previous loop for the same instance. The token lets a loop that exits
// by itself remove only its own registration, not a replacement registered
// during a lifecycle race.
func (r *LivenessRegistry) prepareProbeLoop(instance string) *livenessLoopToken {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.loops == nil {
		r.loops = make(map[string]livenessLoopRegistration)
	}
	r.nextID++
	token := &livenessLoopToken{id: r.nextID}
	var previousCancel context.CancelFunc
	if prev, ok := r.loops[instance]; ok && prev.cancel != nil {
		previousCancel = prev.cancel
	}
	r.loops[instance] = livenessLoopRegistration{token: token}
	r.mu.Unlock()
	// Cancel outside the registry lock. A CancelFunc normally only closes a
	// context, but keeping user-provided cancellation callbacks out of the
	// critical section prevents a callback from deadlocking on this registry.
	if previousCancel != nil {
		previousCancel()
	}
	return token
}

// StartProbeLoop registers cancelFn for instance and returns. The
// cmd/vmmd liveness_recv.start helper calls this to record the
// cancel func the per-instance loop reads on shutdown. Returning the
// cancel func from StartProbeLoop keeps the registry as the single
// source of truth for the cancel path; the cmd helper builds the
// loop goroutine outside the registry.
//
// Defensive: a re-registration for the same instance (e.g. a BringUp
// racing a Park) cancels the prior loop before installing the new
// one. Schedd guarantees the instance id is unique per live tile, so
// the only way to see a duplicate is a wire race; cancelling the prior
// loop is the safe choice.
func (r *LivenessRegistry) StartProbeLoop(instance string, cancelFn context.CancelFunc) {
	if r == nil {
		return
	}
	token := r.prepareProbeLoop(instance)
	r.startProbeLoopWithToken(instance, token, cancelFn)
}

// startProbeLoopWithToken installs the cancel function for a slot reserved by
// prepareProbeLoop. If the loop already finished and removed its token, the
// late registration is rejected and the returned loop is cancelled.
func (r *LivenessRegistry) startProbeLoopWithToken(instance string, token *livenessLoopToken, cancelFn context.CancelFunc) {
	if r == nil || token == nil {
		if cancelFn != nil {
			cancelFn()
		}
		return
	}
	if cancelFn == nil {
		r.finishProbeLoop(instance, token)
		return
	}
	r.mu.Lock()
	current, ok := r.loops[instance]
	if !ok || current.token != token {
		r.mu.Unlock()
		cancelFn()
		return
	}
	current.cancel = cancelFn
	r.loops[instance] = current
	r.mu.Unlock()
}

// finishProbeLoop removes a completed loop only when its token is still the
// active registration. This prevents an old loop's deferred cleanup from
// deleting a newer loop for the same instance.
func (r *LivenessRegistry) finishProbeLoop(instance string, token *livenessLoopToken) {
	if r == nil || token == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.loops[instance]; ok && current.token == token {
		delete(r.loops, instance)
	}
}

// CancelProbeLoop stops the loop for the instance. Idempotent.
// Manager.Park calls this right after DeleteLivenessConsecutiveFailures
// so the gauge deletion and the goroutine cancel happen in the same
// critical section.
func (r *LivenessRegistry) CancelProbeLoop(instance string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	registration, ok := r.loops[instance]
	if ok {
		delete(r.loops, instance)
	}
	r.mu.Unlock()
	if ok && registration.cancel != nil {
		// Invoke cancellation after removing the entry. A concurrent loop
		// completion cannot leave a stale registration behind, and a custom
		// cancel callback cannot re-enter the registry while it is locked.
		registration.cancel()
	}
}

// WithLivenessProbes attaches the per-instance poll goroutine
// registry (issue #554 / ADR-078 / PR review fix). cmd/vmmd main
// calls this at Manager construction:
//
//	mgr.WithLivenessProbes(fcvm.NewLivenessRegistry(), defaultCfg)
//
// where defaultCfg is the per-plan Hobby/Pro/Scale default merged
// into a per-deployment override by Manager.startLivenessLoop. nil
// opts out (Manager constructed without a registry skips the
// per-instance start/cancel calls; the cmd default-local vmmd that
// doesn't wire the registry stays a no-op for AC #1 purposes).
//
// The defaultCfg argument is consumed by startLivenessLoop as the
// fallback when the WakeRequest omits LivenessProbe (the legacy /
// pre-PR-D deployments). cmd/vmmd resolves the actual Hobby/Pro/Scale
// defaults via api.Plan.LivenessPeriodSeconds() /
// LivenessConsecutiveFailures() at construction time so the Manager
// is plan-tier-agnostic.
//
// Returns the *Manager so callers can chain.
func (m *Manager) WithLivenessProbes(reg *LivenessRegistry, defaultCfg LivenessProbeConfig) *Manager {
	m.livenessRegistry = reg
	m.livenessDefaultCfg = defaultCfg
	return m
}

// startLivenessLoop launches the per-instance probe goroutine after
// a successful Wake (issue #554 / ADR-078). Called from the Wake
// success path with the live Instance's lease slot (for the vsock
// CID). No-op when:
//   - m.livenessRegistry is nil (unit tests, default-local vmmd)
//   - the resolved cfg.PeriodSeconds <= 0 (Free plan, or
//     override=off)
//
// The cmd/vmmd liveness_recv.start helper builds the loop body and
// returns its cancel func for the registry. We don't
// construct the loop here because the loop body binds cmd-level
// types (slog, vsock, wire envelope).
func (m *Manager) startLivenessLoop(ctx context.Context, instance string, slot int, override json.RawMessage) {
	if m.livenessRegistry == nil {
		return
	}
	cfg := m.livenessDefaultCfg
	// Per-deployment override (api.DeploymentLivenessProbe) merges
	// over the plan defaults. Same fail-soft shape as
	// healthcheckPathFromDep (engine.go:3360): a malformed override
	// is logged Warn and the loop proceeds with the plan defaults
	// — the apid validator already enforced the shape at INSERT
	// time, so a tampered column would need a direct DB write
	// behind the spec's role separation.
	if len(override) > 0 {
		var ov api.DeploymentLivenessProbe
		if err := json.Unmarshal(override, &ov); err != nil {
			m.log.Warn("liveness: malformed override, using plan defaults",
				"instance", instance, "err", err)
		} else {
			if ov.Path != "" {
				cfg.Path = ov.Path
			}
			if ov.IntervalS > 0 {
				cfg.PeriodSeconds = ov.IntervalS
			}
			if ov.ConsecutiveFailures > 0 {
				cfg.ConsecutiveFailures = ov.ConsecutiveFailures
			}
			// Issue #554 closure / ADR-078: CooldownS is the
			// per-deployment override of the cooldown gate.
			// Clamped to [MinLivenessCooldownSeconds=10,
			// MaxLivenessCooldownSeconds=600] by apid's
			// validator (cmd/apid/handlers_ext.go); here we
			// only need to absorb the value. The schedd-side
			// window (pkg/sched/liveness_window.go) is the
			// separate "N restarts in W seconds" gate; this
			// CooldownS is the per-instance "skip the next
			// fire if a destroy just ran within CooldownS
			// seconds" gate at the vmmd probe layer.
			if ov.CooldownS > 0 {
				cfg.CooldownSeconds = ov.CooldownS
			}
		}
	}
	if cfg.PeriodSeconds <= 0 {
		return
	}
	m.log.Debug("liveness: starting probe loop",
		"instance", instance, "slot", slot,
		"period_s", cfg.PeriodSeconds,
		"consecutive", cfg.ConsecutiveFailures)
	// The actual goroutine spawn + vsock dial logic lives in
	// cmd/vmmd/liveness_recv.go (cmd-level binding). We hand the
	// cmd helper a closure that knows how to launch the loop and
	// return its cancel func; the cmd wires that helper via
	// WithLivenessProbeStarter.
	//
	// Resolve deploymentID from the live map so the cooldown gate
	// can stamp on a key that survives across cold boots (code
	// review #725 finding F1). An empty deploymentID is fine —
	// legacy pre-PR-B callers carry "" on the wire; the gate
	// falls back to the bypass branch in that case.
	deploymentID := ""
	m.mu.Lock()
	if inst, ok := m.live[instance]; ok {
		deploymentID = inst.DeploymentID
	}
	m.mu.Unlock()
	if m.livenessStarter == nil {
		m.log.Warn("liveness: registry wired but no starter; loop will not run",
			"instance", instance)
		return
	}
	token := m.livenessRegistry.prepareProbeLoop(instance)
	parent := m.lifecycleCtx //nolint:contextcheck // this is the daemon-owned lifecycle context, intentionally outliving the wake RPC.
	if parent == nil {
		// Managers constructed outside cmd/vmmd still get the safe
		// behavior: a request cancellation must not kill a monitor
		// for a successfully running VM.
		parent = context.WithoutCancel(ctx)
	}
	// Keep the generation token on the private lifecycle context rather than
	// exposing it through LivenessProbeConfig. The cmd-level starter derives
	// its loop context from parent, so the terminal defer can clean up only
	// the registration belonging to that exact loop generation.
	parent = context.WithValue(parent, livenessLoopTokenContextKey{}, token)
	cancelFn := m.livenessStarter(parent, instance, slot, deploymentID, cfg)
	if cancelFn != nil {
		m.livenessRegistry.startProbeLoopWithToken(instance, token, cancelFn)
	} else {
		m.livenessRegistry.finishProbeLoop(instance, token)
	}
}

// cancelLivenessLoop is the Park / Destroy teardown call (issue
// #554 / ADR-078). Manager.Park calls this right after
// DeleteLivenessConsecutiveFailures so the gauge deletion and the
// goroutine cancel happen in lock-step. No-op when the registry is
// nil (a Manager constructed without WithLivenessProbes).
func (m *Manager) cancelLivenessLoop(instance string) {
	if m.livenessRegistry == nil {
		return
	}
	m.livenessRegistry.CancelProbeLoop(instance)
}

// FinishLivenessLoop removes a loop that reached a terminal condition or
// stopped because vmmd is shutting down. The private token is carried on the
// loop context, so an old loop cannot remove a replacement loop that reused
// the same instance key during a lifecycle race.
func (m *Manager) FinishLivenessLoop(instance string, ctx context.Context) {
	if m == nil || m.livenessRegistry == nil {
		return
	}
	var token *livenessLoopToken
	if ctx != nil {
		token, _ = ctx.Value(livenessLoopTokenContextKey{}).(*livenessLoopToken)
	}
	m.livenessRegistry.finishProbeLoop(instance, token)
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

// MarkInstanceTailTerminal (issue #667 / ADR-078) decrements the
// in-memory tail counter on the live Instance and mirrors the
// decrement to the SQL `tail_count` column via the optional
// TailTerminalStamper. The receipt path is the host-side
// counterpart to a runner's WaitGroup.Done() — each
// waitUntil(promise) task that reaches a terminal outcome
// (completed / failed / timeout) fires one 0x04 DGRAM with the
// outcome + elapsed_ms payload; the host's recv loop calls
// here for each.
//
// The in-memory TailCount is the runner's source of truth (the
// WaitGroup) mirrored into the Manager for two purposes: (a)
// the schedd reaper's `tail_count > 0` early-out (PR 4) and (b)
// the snapshotAndPark 5s watchdog (PR 4) deciding when to
// force-park. SQL is the durable record but the receipt is not
// gated on its success — a transient PG hiccup must not lose
// the receipt; the SQL write is best-effort and the in-memory
// state is the load-bearing signal.
//
// Returns (stamped=false, "", nil) when the instance is unknown
// — the wire RPC translates that to a Debug log + drop, same
// convention as MarkInstanceFrameworkReady. A stale receipt
// from a guest that just parked is a normal event during
// instance churn.
//
// Concurrency: short-held m.mu lock around the live-map lookup
// + decrement. The stamper call is unlocked (no Manager lock
// held across the SQL roundtrip — the column is independently
// atomic via the GREATEST(…, 0) SQL guard).
func (m *Manager) MarkInstanceTailTerminal(ctx context.Context, instance string, outcome TailOutcome, elapsedMs int64) (stamped bool, appID string, err error) {
	m.mu.Lock()
	inst, ok := m.live[instance]
	if !ok {
		m.mu.Unlock()
		return false, "", nil
	}
	if inst.TailCount > 0 {
		inst.TailCount--
	}
	// Accumulate wall-clock seconds into the per-instance
	// tailSecondsAccum. The Sampler reads + atomically resets
	// this once per minute (ReadAndResetTailSeconds). Ceiling
	// division so sub-second tasks are not silently dropped:
	// a 1 ms task is 1 second, a 1000 ms task is 1 second, a
	// 1001 ms task is 2 seconds. tail_seconds is informational
	// only; an over-count is safe (it does not enter billing).
	if elapsedMs > 0 {
		inst.tailSecondsAccum += (elapsedMs + 999) / 1000
	}
	appID = inst.AppID
	m.mu.Unlock()
	// Mirror to SQL so schedd's reaper (PR 4) sees the same
	// post-decrement value as the in-memory stamp. Errors are
	// logged at Debug (the in-memory stamp is the authoritative
	// signal — see method doc above).
	if m.tailTerminalStamper != nil {
		if perr := m.tailTerminalStamper.DecrementInstanceTailCount(ctx, instance); perr != nil {
			// Same conservative-log pattern as
			// MarkInstanceFrameworkReady: surface as a
			// Debug so the receipt isn't gated on a
			// transient SQL failure.
			_ = perr
		}
	}
	_ = outcome
	return true, appID, nil
}

// ReadAndResetTailSeconds (issue #667 / ADR-078) atomically
// returns the per-instance accumulated wall-clock seconds spent
// draining waitUntil tasks since the previous Sampler tick, then
// resets the accumulator to 0 so the next tick observes only the
// deltas. The atomic swap-and-reset is the safety property: if a
// tail terminal lands between the Sampler's Read and Reset, the
// delta is rolled into the NEXT minute (the terminal will fire
// another MarkInstanceTailTerminal that adds to the reset field).
//
// Returns (0, false) when the instance is unknown — the caller
// (meterd Sampler) treats that as "no live instance, skip the
// row". A stale receipt after Park is a normal event during
// instance churn; the tailSecondsAccum on the dead instance is
// GC'd by the Manager when the entry is dropped from m.live.
//
// The mutex is held only across the swap-and-reset (the SQL
// AppendUsage call is unlocked to avoid blocking concurrent
// receipts on the same instance).
func (m *Manager) ReadAndResetTailSeconds(instance string) (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.live[instance]
	if !ok {
		return 0, false
	}
	v := inst.tailSecondsAccum
	inst.tailSecondsAccum = 0
	return v, true
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

// InstanceAppID (issue #463 / ADR-069 / ADR-071 / PR-C §3)
// returns the apps.id UUID that the given instance was woken
// for. The host's DGRAM recv loop uses this to stamp the sidecar
// event audit rows (init_failed, restart) with the (appID,
// sidecar) join key the events platform indexes on. The lookup
// is O(1) on the live map and shares the m.mu lock with
// MarkInstanceFrameworkReady / InstanceByCID so a Park/Destroy
// racing a DGRAM recv sees the same view.
//
// Returns an error when the instance is not live. The caller
// (cmd/vmmd's DGRAM loop) treats this as a normal Debug
// event — a DGRAM racing a wake-park cycle is expected
// during instance churn and the audit is best-effort.
func (m *Manager) InstanceAppID(instance string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.live[instance]
	if !ok {
		return "", fmt.Errorf("fcvm: InstanceAppID %s: not live", instance)
	}
	return inst.AppID, nil
}

// InstanceDeploymentIDAndAppID (issue #463 / ADR-069 / PR-B AC #1)
// resolves both the deployment_id and app_id under a single
// lock-held read so a Park racing a vsock DGRAM recv returns a
// consistent pair (the alternative — two separate Lock/Unlock
// sequences — opens a window where Park has cleared the live map
// between reads and the second call returns "not live" while the
// first returned a stale id). Empty deployment_id is a legitimate
// outcome for legacy single-workload wakes (pre-PR-B callers don't
// carry it on the wire); the sidecar-init-failed dispatch skips
// the deploy-row flip on "" but still emits the audit row.
func (m *Manager) InstanceDeploymentIDAndAppID(instance string) (deploymentID, appID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.live[instance]
	if !ok {
		return "", "", fmt.Errorf("fcvm: InstanceDeploymentIDAndAppID %s: not live", instance)
	}
	return inst.DeploymentID, inst.AppID, nil
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
	evicted := m.parentMounts.RegisterOrEvict(mp, vmmdmount.MountKindParentExt4, storageKey, srcPath)
	if evicted != "" {
		m.log.Warn("vmmd: parent mount cap reached; force-umounted oldest",
			"evicted_mountpoint", evicted)
		// Best-effort umount of the evicted entry; the registry
		// already forgot it, so vmmdmount.UmountExt4 will fall
		// through to the kernel syscall and clean the mountpoint
		// dir.
		_ = vmmdmount.UmountExt4(ctx, evicted)
	}
	m.log.Info("vmmd: parent mounted", "storage_key", storageKey, "mountpoint", mp)
	return mp, nil
}

// MaterializeParentExt4 mounts the parent artifact, copies its filesystem
// tree into the shared staging directory, and releases the temporary mount
// before returning. The explicit copy is required because imaged and vmmd
// run in separate service mount namespaces; returning a mountpoint alone does
// not make the mounted view visible to imaged.
func (m *Manager) MaterializeParentExt4(ctx context.Context, storageKey, targetDir string) error {
	mountpoint, err := m.MountParentExt4(ctx, storageKey)
	if err != nil {
		return err
	}
	copyErr := vmmdmount.MaterializeParentExt4(ctx, mountpoint, targetDir)
	umountErr := m.UmountParentExt4(context.WithoutCancel(ctx), mountpoint)
	if copyErr != nil || umountErr != nil {
		return errors.Join(copyErr, umountErr)
	}
	return nil
}

// UmountParentExt4 (ADR-053) releases a parent mount MountParentExt4
// previously returned. Idempotent on unknown mountpoints — imaged's
// defer-after-error pattern is safe to call blindly. Returns the
// nil-equivalent (no error) on success AND on a never-issued
// mountpoint; surfaces a real umount error (e.g. EBUSY) verbatim.
func (m *Manager) UmountParentExt4(ctx context.Context, mountpoint string) error {
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
	if err := vmmdmount.UmountExt4(ctx, mountpoint); err != nil {
		return err
	}
	m.parentMounts.Forget(mountpoint)
	if entry.SrcPath != "" {
		_ = os.Remove(entry.SrcPath)
	}
	m.log.Info("vmmd: parent umounted", "mountpoint", mountpoint, "storage_key", entry.StorageKey)
	return nil
}

// MountOverlayParent (ADR-075 / DEPLOY-1) mounts an overlayfs
// with the loopback-mounted parent as lowerdir and the per-staging
// tmpfs dirs as upperdir+workdir. imaged's parent-ref stage
// (pkg/imaged/base_stage.go:mountOverlayFn) used to issue this
// syscall itself with AmbientCapabilities=cap_sys_admin — a
// silent violation of the CLAUDE.md "vmmd is the only root
// component" invariant. The syscall now lives here; imaged
// forwards via gRPC.
//
// The Mount call delegates to vmmdmount.MountOverlayParent
// which validates the path prefixes (lowerdir under /srv/fc/parent/,
// upper/work/merged under /dev/shm/faas-base-staging/). The
// Registry tracks the merged path so the 30-minute orphan sweep
// (SweepOrphans on the parentMounts registry) can clean up
// after imaged that crashed before its defer fired.
//
// On any error the function returns the underlying error so the
// gRPC handler can lift to a problem document; the staging dirs
// are NOT cleaned up here — imaged owns upper/work (it created
// them via MkdirBaseStaging + MkdirTemp) and a vmmd-side
// cleanup would race with imaged's own defer-after-error.
func (m *Manager) MountOverlayParent(ctx context.Context, lowerdir, upperdir, workdir, merged string) error {
	if m.parentMounts == nil {
		return fmt.Errorf("vmmd: parent overlay mount: registry not wired")
	}
	if err := vmmdmount.MountOverlayParent(ctx, lowerdir, upperdir, workdir, merged); err != nil {
		return err
	}
	// Track merged in the registry with MountKindOverlayParent
	// + empty StorageKey/SrcPath (the overlay mount has neither —
	// it's a vmmd-issued mount over paths imaged chose). The
	// cap (16) is the same as loopback mounts; imaged should
	// umount before issuing the next one anyway.
	//
	// Review finding B5: pre-B5 the returned mountpoint was
	// discarded, so when the cap was hit the evicted mount
	// stayed live on disk — leaking upper/work/merged until
	// the next sweep tick. Now we honor the eviction by
	// dispatching through Registry.Umount (which switches on
	// MountKind and tears down the overlay properly).
	evicted := m.parentMounts.RegisterOrEvict(merged, vmmdmount.MountKindOverlayParent, "", "")
	if evicted != "" {
		m.log.Warn("vmmd: parent overlay mount cap reached; force-umounted oldest",
			"evicted_mountpoint", evicted)
		// Registry.Umount dispatches on MountKind (B4), so this
		// works for either ext4 or overlay evictions. Best-effort
		// — a failed umount surfaces in the next sweep tick.
		if _, uerr := m.parentMounts.Umount(ctx, evicted); uerr != nil {
			m.log.Warn("vmmd: evicted parent overlay umount failed (sweep will retry)",
				"evicted_mountpoint", evicted, "err", uerr)
		}
	}
	m.log.Info("vmmd: parent overlay mounted",
		"lowerdir", lowerdir, "upperdir", upperdir,
		"workdir", workdir, "merged", merged)
	return nil
}

// UmountOverlayParent (ADR-075 / DEPLOY-1) releases an overlay
// mount MountOverlayParent previously issued. Idempotent on
// unknown mountpoints so imaged's defer-after-error is safe.
// The merged dir is also rmdir'd by vmmdmount.UmountOverlayParent
// — the staging tree (upper+work) stays in place because imaged
// may want to reuse them on the next Mount attempt.
func (m *Manager) UmountOverlayParent(ctx context.Context, merged string) error {
	if m.parentMounts == nil {
		return nil
	}
	// Funnel through Registry.Umount (review B4). The Kind
	// dispatch in there issues UmountOverlayParent (not the
	// ext4 umount) so the overlay mount is torn down with the
	// right syscall. Lookup miss → Registry.Umount returns
	// (false, nil), which is the idempotent defer-after-error
	// shape imaged's caller depends on.
	if _, err := m.parentMounts.Umount(ctx, merged); err != nil {
		return err
	}
	m.log.Info("vmmd: parent overlay umounted", "mountpoint", merged)
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

// prepareWakeFiles resolves the two runtime environment files before the VMM
// starts. JailerVMM stages the returned bytes while Firecracker's config FIFO
// is still closed; keeping the preparation here preserves the Manager as the
// only secret-unseal boundary without allowing guest-init to race a late
// loopback write.
func (m *Manager) prepareWakeFiles(req WakeRequest) (secretsJSON, apiJSON []byte, err error) {
	if len(req.SealedEnvEntries) > 0 {
		merged, openErr := m.openSealedEnvEntries(req.SealedEnvEntries)
		if openErr != nil {
			return nil, nil, openErr
		}
		secretsJSON, err = jsonMarshalEnvelope(merged)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal envelope: %w", err)
		}
	}

	if len(req.APIEnvEntries) > 0 {
		merged := make(map[string]string, len(req.APIEnvEntries))
		for _, entry := range req.APIEnvEntries {
			merged[entry.Key] = entry.Value
		}
		apiJSON, err = json.Marshal(merged)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal api env: %w", err)
		}
	}
	return secretsJSON, apiJSON, nil
}

func (m *Manager) openSealedEnvEntries(entries []SealedEnvEntry) (secretbox.Envelope, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	if len(m.hostIdentities) == 0 {
		return nil, ErrNoHostKey
	}
	merged := secretbox.Envelope{}
	for _, entry := range entries {
		inner, err := secretbox.OpenMulti(m.hostIdentities, entry.Ciphertext)
		if err != nil {
			return nil, fmt.Errorf("open sealed env[%s]: %w", logsanitize.Field(entry.Key), err)
		}
		for key, value := range inner {
			merged[key] = value
		}
	}
	return merged, nil
}

// prepareSidecarEnvFiles opens the per-value SealBytes payloads persisted by
// apid and keeps the resulting plaintext only in the wake request until the
// concrete VMM writes it to that instance's writable main layer. Unlike the
// shared sidecar image, that layer is already deployment/instance scoped.
func (m *Manager) prepareSidecarEnvFiles(req *WakeRequest) error {
	for i := range req.Sidecars {
		entries := req.Sidecars[i].SealedEnv
		if len(entries) == 0 {
			continue
		}
		if len(m.hostIdentities) == 0 {
			return ErrNoHostKey
		}
		merged := make(map[string]string, len(entries))
		for _, entry := range entries {
			namespace, plaintext, err := secretbox.OpenBytesMulti(m.hostIdentities, entry.Ciphertext)
			if err != nil {
				return fmt.Errorf("open sidecar env[%s]: %w", logsanitize.Field(entry.Key), err)
			}
			if namespace != "sidecar_env" {
				return fmt.Errorf("sidecar env[%s] has namespace %q, want sidecar_env", logsanitize.Field(entry.Key), namespace)
			}
			merged[entry.Key] = string(plaintext)
		}
		blob, err := json.Marshal(merged)
		if err != nil {
			return fmt.Errorf("marshal sidecar env: %w", err)
		}
		req.Sidecars[i].preparedEnvJSON = blob
		// Do not carry ciphertext any farther than the Manager's unseal
		// boundary; the VMM only needs the per-instance plaintext bytes.
		req.Sidecars[i].SealedEnv = nil
	}
	return nil
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

func (m *Manager) preparesWakeStateBeforeBoot() bool {
	type preBootStatePreparer interface {
		preparesWakeStateBeforeBoot() bool
	}
	preparer, ok := m.vmm.(preBootStatePreparer)
	return ok && preparer.preparesWakeStateBeforeBoot()
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
	Instance     string
	AppID        string // apps.id UUID; PR-B UpdateEgressAllowlist walks live by AppID
	DeploymentID string // deployments.id UUID; PR-B AC #1 stamps onto Instance so the vsock DGRAM sidecar-init-failed path can flip the deploy row (issue #463 / ADR-069)
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
	// BuildTimeoutSec is the guest build wall-clock budget for a builder VM.
	// It must match the timeout written by builderd into the build manifest;
	// vmmd uses it to retain the corresponding export/teardown headroom.
	BuildTimeoutSec int
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
	// StartupDeadlineS is the per-app readiness budget. 0 preserves the
	// vmmd default for legacy callers.
	StartupDeadlineS int
	// LivenessProbe (issue #554 / ADR-078) is the per-deployment
	// override JSON (api.DeploymentLivenessProbe). The engine resolves
	// this from deployments.override_liveness_probe at Wake time and
	// threads it into vmmd via this field. Empty = "use plan defaults";
	// a malformed value is logged Warn by startLivenessLoop and the
	// plan defaults are used (fail-soft, matching the HealthcheckPath
	// pattern at engine.go:3360).
	LivenessProbe json.RawMessage
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
	// preparedSecretsEnvJSON and preparedAPIEnvJSON are internal-only
	// handoff fields. Wake fills them before calling bringUp so a concrete
	// JailerVMM can stage both files before releasing Firecracker's config
	// FIFO. They are deliberately unexported and never cross the gRPC wire.
	preparedSecretsEnvJSON []byte
	preparedAPIEnvJSON     []byte
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
	// StaticEgressIP (ADR-119) is the customer-supplied IPv4
	// (BYOIP, Scale-only) the host MASQUERADE-sibling rule
	// rewrites tenant source traffic to. Empty string = no
	// static pin (default behaviour preserved). When
	// non-empty, the per-netns renderer emits a sibling
	// `oifname <VethPeer> ip saddr 10.0.0.2 snat to
	// <StaticEgressIP>` rule AFTER the default MASQUERADE so
	// the customer's IP wins the SNAT decision. v4-only in
	// v1; the DB family=4 CHECK
	// (apps_static_egress_ip_family_check) prevents IPv6
	// from reaching here. Plan-gated upstream
	// (Free/Hobby/Pro never get here; Scale only). The
	// caller (apid) is responsible for shape + per-plan
	// gating.
	StaticEgressIP string
	// Sidecars (issue #463 / ADR-069 / PR-B) is the per-workload
	// workloads slice carried on the wake wire. schedd resolves
	// the deployment's jsonb sidecars envelope into one
	// WorkloadSpec per sidecar (the main workload is implicit —
	// Workloads[0] in BringUp builds it from WakeRequest.LayerKey
	// + the plan's RAM). vmmd turns each entry into one FC Drive +
	// one nested cgroup scope. Empty slice = legacy single-
	// workload path (pre-PR-B callers). Additive per ADR-016.
	Sidecars []WorkloadSpec
}

// SealedEnvEntry is one (key, ciphertext) pair as stored in app_secrets. The
// key is the env-var name; the ciphertext is sealed under the host age
// recipient by apid. vmmd merges all entries into the single envelope file.
//
// ADR-092 PR-A DELIBERATELY does NOT add a `Scope string` field
// here. Each wake call goes through schedd's loadSealedEnvFor,
// which scopes the read to dep.Scope at the seam
// (pkg/sched/engine.go::loadSealedEnvFor at engine.go:4046 calls
// ListAppSecretsInScope). The result is that this struct holds
// AT MOST ONE scope's rows on every wake — there is no merge
// demux logic at vmmd level. Adding Scope here speculatively
// would (a) be a vestigial field reviewers rightly flag and
// (b) push the per-scope demux into a layer that has no semantic
// access to scope semantics. A future ADR that introduces an
// "overlay" wire shape (analogous to the env.json overlay from
// ADR-090 D4 deferred) would revisit this decision and widen
// the struct + the vmmd merge loop together.
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
	// StartupDeadlineS is the per-app readiness budget forwarded to
	// WakeRequest. 0 preserves the vmmd default for legacy callers.
	StartupDeadlineS int
	// Runtime (issue #470 / PR #470-FU-B) — the runtime id forwarded
	// verbatim to WakeRequest.Runtime. Mirrors the WakeRequest
	// field's contract: empty = legacy wake (the framework-ready
	// receipt path tolerates empty and falls back to the "unknown"
	// histogram label).
	Runtime string
	// Sidecars (issue #463 / ADR-069 / PR-B) is the per-workload
	// sidecar wire forwarded to WakeRequest.Sidecars. Empty =
	// legacy single-workload path. See WakeRequest.Sidecars for
	// the contract. Same symmetry rationale as Port /
	// HealthcheckPath / EgressAllowlist above.
	Sidecars []WorkloadSpec
	// DeploymentID (issue #463 / ADR-069 / PR-B AC #1) is the
	// deployments.id UUID forwarded verbatim to WakeRequest.DeploymentID.
	// Mirrors the WakeRequest field's contract: empty = legacy
	// single-workload wake that doesn't carry a deployment_id on
	// the wire, and the sidecar-init-failed dispatch skips the
	// deploy-row flip on "". Same symmetry rationale as Port /
	// HealthcheckPath above.
	DeploymentID string
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
		HealthcheckPath:  req.HealthcheckPath,
		StartupDeadlineS: req.StartupDeadlineS,
		// PR #470-FU-B: forward the runtime id so the framework-ready
		// receipt handler can label the warmup histogram. See
		// WakeRequest.Runtime for the contract.
		Runtime: req.Runtime,
		// Issue #463 / ADR-069 / PR-B: forward the per-workload
		// sidecar wire so Wake threads each entry into the
		// per-workload drive + cgroup + manifest stage. Empty
		// = legacy single-workload path.
		Sidecars: req.Sidecars,
		// Issue #463 / ADR-069 / PR-B AC #1: forward the
		// deployment_id so Wake stamps it onto the live Instance
		// and the vsock DGRAM sidecar-init-failed dispatch can
		// flip the deployments row back to status='failed'.
		// Empty = legacy wake, dispatch no-ops.
		DeploymentID: req.DeploymentID,
	})
}

// BootJob (issue #1184 Workstream A / ADR-099) is the
// job-task sibling of ColdBoot. Allocates a slot, plumbs the
// netns, runs the vmmd-side cold-boot path (BootColdBootForJob +
// manifest staging), and returns the live Instance.
//
// Job VMs don't bind :8080 (no healthcheck), so Ready is stamped
// when Boot returns (SkipReady=true). schedd's Engine.WakeJob
// treats the returned Instance as the cue to JobTaskMarkClaimed
// and emit job.task.dispatched.
//
// Distinct from Wake: there's no snapshot-restore path for jobs
// (jobs are always cold-boot — a 350ms wake budget is meaningless
// for a 300s task). Distinct from ColdBoot: no LayerKey, no
// healthcheck, no characterize report, single command instead of
// a long-lived listener.
//
// DestroyWait is what the engine passes for the per-task cap + 90s
// grace; pass EffectiveDestroyWait(req.TaskTimeoutSec) at the call
// site.
func (m *Manager) BootJob(ctx context.Context, req JobBootRequest) (_ *Instance, err error) {
	if m.vmm == nil {
		return nil, fmt.Errorf("manager: BootJob: nil vmm")
	}
	lease, err := m.alloc.Acquire(req.Instance)
	if err != nil {
		return nil, fmt.Errorf("manager: BootJob %s: acquire lease: %w", req.Instance, err)
	}
	// Stamp the Plan (issue #301, ADR-044) so the downstream
	// Boot / Kill path computes the per-plan parent cgroup +
	// cpu.weight. Set BEFORE the netns build so a rejected wake
	// cleans up via the same defer that releases the slot.
	lease.Plan = req.Plan
	lease.IsBuilder = false

	// Stamp the per-instance memory cap (CLAUDE.md §11
	// 'memory.max = plan + 8 MB'). Without this, l.MemoryMaxMiB
	// is zero → JailerSpec.MemoryMaxBytes=0 → vmm.go:2099
	// closure returns 0 → config.go:542 skips the --cgroup
	// memory.max arg entirely → the guest has no host memory
	// fence and can OOM-kill the box. CR-2 / code-review #2.
	// Mirrors manager.go:2581 in the Wake path.
	lease.MemoryMaxMiB = req.MemSizeMiB

	// Match the wake path's defer-and-unwind pattern: any error
	// after Acquire must release the slot AND tear down the netns.
	defer func() {
		if err != nil {
			// Drain whatever partial state we accumulated so a
			// rejected job boot doesn't leak a netns / cgroup.
			m.cleanup(context.WithoutCancel(ctx), lease, netns.NewConfig(
				lease.Instance, lease.Netns, lease.VethHost, lease.VethPeer, lease.HostIP,
			), nil)
		}
	}()

	// Plan validation (issue #301 / ADR-043). An empty / unknown
	// plan would land the VM under the wrong cgroup sub-slice
	// (or under none at all) and silently disable per-plan
	// cpu.weight + cpu.max enforcement. Mirror Wake's gate so a
	// job VM is rejected the same way.
	if !req.Plan.Valid() {
		return nil, fmt.Errorf("boot job %s: invalid plan %q (issue #301 / ADR-043)", req.Instance, req.Plan)
	}

	// Plumb the netns (tap0, 10.0.0.2/30, NAT, jailer cgroup).
	// Same shape as Wake's netns setup — every VM gets one
	// (ADR-009, identical inner network world).
	nc := netns.NewConfig(lease.Instance, lease.Netns, lease.VethHost, lease.VethPeer, lease.HostIP)
	nc.TapUID = lease.UID
	if err = m.setupNetwork(ctx, nc); err != nil {
		return nil, fmt.Errorf("manager: BootJob %s: network setup: %w", req.Instance, err)
	}

	// Fire the cold-boot through the VMM. The vmm owns the
	// StorageBackend key materialisation + /etc/faas/job.json
	// manifest write; the manager just brokers the lease + netns
	// and stamps the live record.
	spec := JobColdBootSpec{
		KernelKey:      req.KernelKey,
		BaseKey:        req.BaseKey,
		ImageRef:       req.ImageRef,
		Command:        req.Command,
		Env:            req.Env,
		VcpuCount:      req.VcpuCount,
		MemSizeMiB:     req.MemSizeMiB,
		Tap:            "tap0",
		TaskTimeoutSec: req.TaskTimeoutSec,
		LeaseToken:     req.LeaseToken,
		AccountID:      req.AccountID,
		RunID:          req.RunID,
		TaskIndex:      req.TaskIndex,
	}
	if err = m.vmm.BootColdBootForJob(ctx, lease, spec); err != nil {
		return nil, fmt.Errorf("manager: BootJob %s: vmm boot: %w", req.Instance, err)
	}

	// Stamp the live Instance. No readiness gating — the
	// supervisor exits as soon as the command exits, so the
	// "ready" window is from bootNoWait return to job_exit
	// DGRAM arrival. Mirrors the Wake path's struct literal at
	// vmm.go:3003.
	inst := &Instance{
		Lease:           lease,
		Net:             nc,
		Method:          WakeColdBoot, // jobs are always cold-boot
		AccountID:       req.AccountID,
		Plan:            req.Plan,
		Port:            0,  // no listener port
		HealthcheckPath: "", // no readiness probe
	}
	m.mu.Lock()
	m.live[req.Instance] = inst
	m.cidToID[GuestVsockCID(lease.Slot)] = req.Instance
	m.mu.Unlock()
	// MarkInstanceFrameworkReady isn't called here — the
	// supervisor doesn't emit a framework-ready receipt. The
	// job exit DGRAM is the only receipt.
	return inst, nil
}

// WaitJobExit (issue #1184 Workstream A / ADR-099) is the
// Manager-level thin wrapper around VMM.WaitJobExit. The Engine's
// HandleJobExit call site needs to block on the per-instance vsock
// DGRAM until either the envelope arrives or the deadline elapses.
//
// On deadline elapse the engine treats the task as crashed and
// lets the stuck-task reaper take over (M6). On error return the
// caller logs at WARN and continues — the engine never wedges on
// a single failed wait.
//
// Pass deadline = EffectiveDestroyWait(req.TaskTimeoutSec) so the
// per-task wall-clock cap fits the listener window.
func (m *Manager) WaitJobExit(ctx context.Context, instance string, deadline time.Duration) (JobExitPayload, error) {
	if m.vmm == nil {
		return JobExitPayload{}, fmt.Errorf("manager: WaitJobExit: nil vmm")
	}
	lease := Lease{Instance: instance, Slot: -1} // WaitJobExit ignores slot
	return m.vmm.WaitJobExit(ctx, lease, deadline)
}

// JobBootRequest is the Manager.BootJob payload. Mirrors JobVmmSpec
// in pkg/sched/jobs.go (the schedd-side wire) plus the Manager-side
// fields every Wake needs (Instance, NodeID, Plan).
//
// Kept separate from JobVmmSpec so schedd → vmmdgrpc → Manager
// translation is mechanical: pkg/vmmdgrpc/proto.go's JobColdBoot
// handler unpacks the proto, copies fields into JobBootRequest,
// and calls Manager.BootJob. The split also lets the Manager-side
// type add Manager-specific fields (Plan, NodeID) without
// re-exposing them to schedd.
type JobBootRequest struct {
	Instance       string
	AccountID      string
	NodeID         string
	Plan           api.Plan
	RunID          string
	TaskIndex      int
	ImageRef       string
	KernelKey      string
	BaseKey        string
	Command        []string
	Env            map[string]string
	VcpuCount      int
	MemSizeMiB     int
	TaskTimeoutSec int
	LeaseToken     string
}

// Wake brings an instance up, preferring snapshot restore and falling back to
// cold boot. On any terminal error it unwinds every resource it acquired — the
// caller sees no half-built instance and the box leaks nothing (§6.2-4/5).
func (m *Manager) Wake(ctx context.Context, req WakeRequest) (_ *Instance, err error) {
	// Phase timing (see wakePhases). Reported on EVERY failure and on
	// successes slower than SlowWakeLogThreshold. The named `err`
	// return is what lets this defer distinguish the two.
	phases := newWakePhases()
	defer func() {
		switch {
		case err != nil:
			m.log.Warn("fcvm: wake failed; phase breakdown",
				append([]any{"instance", req.Instance, "err", err}, phases.attrs()...)...)
		case time.Since(phases.start) >= SlowWakeLogThreshold:
			m.log.Warn("fcvm: slow wake; phase breakdown",
				append([]any{"instance", req.Instance,
					"threshold_ms", SlowWakeLogThreshold.Milliseconds()}, phases.attrs()...)...)
		}
	}()

	lease, err := m.alloc.Acquire(req.Instance)
	if err != nil {
		return nil, fmt.Errorf("wake %s: acquire lease: %w", req.Instance, err)
	}
	phases.mark("lease_acquire")
	// Stamp the Plan onto the Lease (issue #301, ADR-044) so the
	// downstream vmm.Boot/Restore/Kill/Destroy path can compute the
	// per-plan parent cgroup + cpu.weight without a separate map
	// lookup. Set BEFORE the validation loop so a rejected wake
	// cleans up via the same defer that releases the slot — the
	// Plan is allocator-side state and must follow the lease's
	// lifetime.
	lease.Plan = req.Plan
	lease.IsBuilder = req.ExportDir != ""
	lease.BuildTimeoutSec = req.BuildTimeoutSec
	if lease.IsBuilder && lease.BuildTimeoutSec <= 0 {
		lease.BuildTimeoutSec = api.BuildTimeoutSeconds
	}
	lease.MemoryMaxMiB = req.MemSizeMiB
	m.mu.Lock()
	if m.waking == nil {
		m.waking = make(map[string]struct{})
	}
	m.waking[req.Instance] = struct{}{}
	m.mu.Unlock()
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
			), nil)
		}
	}()
	nc := netns.NewConfig(lease.Instance, lease.Netns, lease.VethHost, lease.VethPeer, lease.HostIP)
	nc.TapUID = lease.UID
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
	// Prepare runtime files before any VMM boot path. The concrete JailerVMM
	// stages these bytes before releasing Firecracker's config FIFO; test VMMs
	// without the pre-boot marker keep the legacy post-ready staging hooks
	// below for compatibility.
	req.preparedSecretsEnvJSON, req.preparedAPIEnvJSON, err = m.prepareWakeFiles(req)
	if err != nil {
		err = fmt.Errorf("wake %s: prepare runtime env: %w", req.Instance, err)
		return nil, err
	}
	if err = m.prepareSidecarEnvFiles(&req); err != nil {
		err = fmt.Errorf("wake %s: prepare sidecar env: %w", req.Instance, err)
		return nil, err
	}
	// prepareWakeFiles + prepareSidecarEnvFiles together. Both touch
	// the layer/rootfs staging paths, so this is the phase that
	// absorbs a cold layer fetch.
	phases.mark("env_prepare")
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
	// ADR-119 (redesign): per-app static egress IP. The
	// per-netns SNAT was moved to the host renderer (see
	// pkg/netns/policy.go::HostPolicy.StaticEgressRules + the
	// `nftables NAT is first-match + terminal` rule). vmmd no
	// longer writes nc.AccountStaticIP at all — the per-VM
	// host IP is acquired from the alloc.go pool on the
	// customer's apid PUT (the (accountID, appID) reservation
	// lives until the customer clears the pin). The Wake
	// path only:
	//
	//   1. validates the customer IP through the canonical
	//      api.ValidateStaticEgressIP (failure = wake rejected),
	//   2. looks up the reservation,
	//   3. registers the per-VM host IP on the Manager so the
	//      host renderer emits an `ip saddr <perVMHostIP> snat
	//      to <customerIP>` rule.
	//
	// Empty StaticEgressIP is the default (no pin); the early
	// branch skips the lookup + register.
	if req.StaticEgressIP != "" {
		ip, err := netip.ParseAddr(req.StaticEgressIP)
		if err != nil {
			return nil, fmt.Errorf("wake %s: static egress IP: invalid dotted-quad %q: %w", req.Instance, req.StaticEgressIP, err)
		}
		if !ip.Is4() {
			return nil, fmt.Errorf("wake %s: static egress IP: rejected %q (IPv6 deferred)", req.Instance, req.StaticEgressIP)
		}
		if err := api.ValidateStaticEgressIP(ip); err != nil {
			return nil, fmt.Errorf("wake %s: static egress IP: rejected %q: %w", req.Instance, req.StaticEgressIP, err)
		}
		// Reservation must already exist if the customer has
		// pinned. The apid PUT path acquires; the Wake path
		// only reads. If a customer clears their pin between
		// dispatch and wake, the reservation is gone and the
		// wake dies here — the gRPC fault is the customer's
		// signal to drop the request.
		//
		// The accountID guard is the wire-side defence against
		// a malicious or buggy shedder that forwards a
		// different accountID than the one that acquired the
		// pin. The reservation is keyed by (accountID, appID) so
		// the cross-check is one field comparison.
		res, ok := StaticEgressReservationFor(req.AppID)
		if !ok {
			return nil, fmt.Errorf("wake %s: static egress IP: no reservation for app_id=%s", req.Instance, req.AppID)
		}
		if res.AccountID != req.AccountID {
			return nil, fmt.Errorf("wake %s: static egress IP: app_id=%s reserved under account_id=%s, not %s", req.Instance, req.AppID, res.AccountID, req.AccountID)
		}
		if res.CustomerIP != ip {
			return nil, fmt.Errorf("wake %s: static egress IP: app_id=%s reserved with %s, not %s", req.Instance, req.AppID, res.CustomerIP, req.StaticEgressIP)
		}
		m.RegisterStaticEgressIPForVM(req.AppID, res.PerVMHostIP)
	}
	// Issue #679 / PR-A: cache the per-app slice BEFORE the
	// operator-bundle merge so SetEgressOperatorBundle can read
	// the authoritative per-app set on a subsequent bundle
	// reload. The merge happens after this snapshot; the
	// per-app cache is the only place the unaugmented slice
	// survives a SIGHUP-driven operator-bundle change.
	if req.AppID != "" {
		m.perAppAllowlistMu.Lock()
		if m.perAppAllowlist == nil {
			m.perAppAllowlist = make(map[string][]netip.Prefix)
		}
		perAppSnapshot := make([]netip.Prefix, len(nc.EgressAllowlist))
		copy(perAppSnapshot, nc.EgressAllowlist)
		m.perAppAllowlist[req.AppID] = perAppSnapshot
		m.perAppAllowlistMu.Unlock()
		// ADR-119 (redesign): cache the per-app customer IP
		// from the Wake request. The UpdateStaticEgressIP gRPC
		// path writes here too. A nil pointer clears the per-app
		// pin. The address is parsed from the wire payload
		// (validated above against the canonical deny-set);
		// the per-VM host IP lives on the alloc.go reservation
		// — see StaticEgressReservationFor above.
		m.perAppStaticIPMu.Lock()
		if m.perAppStaticIP == nil {
			m.perAppStaticIP = make(map[string]*netip.Addr)
		}
		if req.StaticEgressIP == "" {
			m.perAppStaticIP[req.AppID] = nil
		} else {
			wireIP, _ := netip.ParseAddr(req.StaticEgressIP)
			ipCopy := wireIP
			m.perAppStaticIP[req.AppID] = &ipCopy
		}
		m.perAppStaticIPMu.Unlock()
	}
	// Issue #679 / PR-A: merge the operator-managed egress
	// bundle into the per-app slice before render. The bundle
	// is loaded at vmmd startup + SIGHUP-reload; nil/empty is
	// a no-op and matches pre-PR-A behaviour exactly. The
	// merge dedups across per-app + operator (an entry that's
	// already in the per-app set doesn't get a duplicate row
	// in the rendered anonymous daddr-set).
	nc.EgressAllowlist = m.mergeOperatorBundle(nc.EgressAllowlist)

	// ADR-098 C11: capture the per-boundary timings (netns+TAP /
	// restore / guest-ready) on a private struct that bringUp
	// mutates in-place. Stays on the stack — never crosses the
	// goroutine boundary. Restored onto inst immediately below.
	var timings bringUpTimings
	var method WakeMethod

	// ADR-098 C11: capture the netns+TAP timing at the boundary
	// (issue #574). setupNetwork wraps netns creation + TAP setup
	// + veth pair wiring. Stays roughly constant per shape, so a
	// sudden spike is a host-level signal not a workload signal.
	phases.mark("pre_network")
	netnsStart := time.Now()
	if err = m.setupNetwork(ctx, nc); err != nil {
		// Issue #1059 / ADR-127: closed-reason counter on the
		// setupNetwork path. The error wrap "network setup: %w"
		// surfaces every netns / TAP / nft failure under one
		// umbrella, so per ADR §3 we hardcode reason="netns_fail"
		// rather than rely on inner-error typed-sentinel matching
		// (which would require pkg/netns/config.go callers to
		// wrap with %w ErrNetnsFail — a follow-up extension). The
		// reason literal is load-bearing for the §12
		// "vmmd_wake_failure_total" panel legend — operators
		// triaging a netns_fail spike do not need to know which
		// inner step (netns add, TAP create, nft apply) failed.
		// nil-safe: the receiver guards on m.wakeFailureMetrics.
		if m.wakeFailureMetrics != nil {
			m.wakeFailureMetrics.WakeFailure("", req.AppID, WakeReasonNetnsFail).Inc()
		}
		return nil, fmt.Errorf("wake %s: network setup: %w", req.Instance, err)
	}
	timings.netnsTapMs = time.Since(netnsStart).Milliseconds()

	phases.mark("setup_network")
	method, err = m.bringUp(ctx, lease, nc, req, &timings)
	// Marked before the error check so a FAILED bringUp still reports
	// how long it burned — that is the phase most likely to hold a
	// hung Firecracker, and the one the defer most needs to name.
	phases.mark("bring_up")
	if err != nil {
		return nil, err
	}

	// Test VMMs retain the legacy observable staging hooks. Production
	// JailerVMM receives the prepared bytes through the boot spec and writes
	// them before Firecracker can execute guest-init.
	if !m.preparesWakeStateBeforeBoot() {
		if len(req.preparedSecretsEnvJSON) > 0 {
			if err := m.stageSecretsEnv(req.Instance, req.preparedSecretsEnvJSON); err != nil {
				return nil, fmt.Errorf("wake %s: stage secrets.env: %w", req.Instance, err)
			}
		}
		if len(req.preparedAPIEnvJSON) > 0 {
			if err := m.stageAPIEnv(req.Instance, req.preparedAPIEnvJSON); err != nil {
				return nil, fmt.Errorf("wake %s: stage api env: %w", req.Instance, err)
			}
		}
		for _, sc := range req.Sidecars {
			if len(sc.preparedEnvJSON) == 0 {
				continue
			}
			if err := m.vmm.StageWorkloadEnv(req.Instance, sc.Name, sc.preparedEnvJSON); err != nil {
				return nil, fmt.Errorf("wake %s: stage sidecar %s env: %w", req.Instance, sc.Name, err)
			}
		}
	}

	// Issue #463 / ADR-069 / PR-B: per-workload manifest staging.
	// Each sidecar drive (and the main drive1) gets a
	// /etc/faas/workload.json written with the workload's name,
	// type, ram_mb, port, and essential flag. Guest-init reads
	// these at boot to fork/exec the workload under the right
	// supervisor. The main drive's manifest is redundant with
	// the customer-supplied app.json (the legacy single-workload
	// path) but writing it makes guest-init's "all workloads are
	// uniform" code path identical for both shapes — there is
	// NO legacy fast path inside guest-init that skips the
	// manifest read when Workloads[0] is the only entry.
	//
	// Sidecar drive indices are 0-based; the drive slot
	// BuildColdBootConfig emits is fmt.Sprintf("%s%d",
	// DriveSidecarPrefix, idx) for the (idx+1)-th workload, so
	// index 0 in the sidecar slice = "layer-sidecar-0" in the
	// FC config. The in-chroot basename is the constant
	// sidecarDriveImageName(0) = "sidecar-0.ext4".
	if len(req.Sidecars) > 0 && !m.preparesWakeStateBeforeBoot() {
		// Main workload manifest on drive1.
		if err := m.vmm.StageWorkloadManifest(req.Instance, -1, WorkloadSpec{
			Name:      WorkloadNameMain,
			Type:      WorkloadNameMain,
			RamMB:     req.MemSizeMiB,
			Port:      req.Port,
			Essential: true,
		}); err != nil {
			return nil, fmt.Errorf("wake %s: stage main workload manifest: %w", req.Instance, err)
		}
		// Sidecar manifests, one per sidecar in stability order.
		for i, sc := range req.Sidecars {
			if err := m.vmm.StageWorkloadManifest(req.Instance, i, sc); err != nil {
				return nil, fmt.Errorf("wake %s: stage sidecar %d (%s) workload manifest: %w",
					req.Instance, i, sc.Name, err)
			}
		}
		// Deployment-level roster at /etc/faas/workloads.json on
		// drive1 (issue #463 / ADR-069 / PR-B). Guest-init's
		// runWorkloads orchestrator reads this file at boot to
		// discover the main workload's spec + the per-sidecar
		// array; without it, the orchestrator sees a "legacy
		// single-workload" shape and routes through runAppWithEnv
		// unchanged. Written AFTER per-drive manifests so any
		// partial-failure on those doesn't leave the orchestrator
		// with a roster pointing at non-existent drives.
		mainSpec := WorkloadSpec{
			Name: WorkloadNameMain, Type: WorkloadNameMain,
			RamMB: req.MemSizeMiB, Port: req.Port,
			Essential: true,
		}
		if err := m.vmm.StageWorkloadRoster(req.Instance, mainSpec, req.Sidecars); err != nil {
			return nil, fmt.Errorf("wake %s: stage workload roster: %w", req.Instance, err)
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
	if !m.preparesWakeStateBeforeBoot() {
		if lease.IsBuilder {
			err = writeBuildCgroup(req.Instance, req.MemSizeMiB)
		} else {
			err = writePlanCgroup(req.Instance, req.Plan, req.MemSizeMiB)
		}
		if err != nil {
			// Cgroup setup is a mandatory isolation boundary. The VM is already
			// up, but returning the error routes through Wake's deferred cleanup
			// so the instance cannot become reachable without its memory and CPU
			// fences. Local environments that cannot provide cgroup v2 must use
			// the existing fake VMM/unit-test path rather than weakening this
			// production invariant.
			// Issue #1059 / ADR-127: closed-reason counter. Hardcoded
			// reason="cgroup_fail" per ADR §3 — the wrap already names
			// the surface, and the §11 invariant "cgroups v2 with
			// memory.max = plan + 8 MB" makes any sustained
			// cgroup_fail rate an operator action item. nil-safe.
			if m.wakeFailureMetrics != nil {
				m.wakeFailureMetrics.WakeFailure("", req.AppID, WakeReasonCgroupFail).Inc()
			}
			return nil, fmt.Errorf("wake %s: cgroup fence: %w", req.Instance, err)
		}

		// Issue #463 / ADR-069 / PR-B: per-workload cgroup scopes. The
		// parent scope (writePlanCgroup above) holds the plan ceiling; we
		// carve one child scope per workload with memory.max = workload's
		// ram_mb. The host-side firecracker process stays in the parent
		// scope (jailer's --cgroup arg); the child scopes are defense-in-
		// depth leaves that the kernel cascade-removes when vmm.Kill
		// removes the parent. The PRIMARY OOM isolation happens inside
		// the guest (guest-init's per-workload cgroup partition); this
		// host-side layer is a second line of defense for the host's
		// firecracker process and a per-workload memory.failcnt triage
		// signal. Same "warn + continue" posture as writePlanCgroup
		// because the VM is already up and leaking a host-side cap is
		// strictly better than failing the wake.
		if len(req.Sidecars) > 0 {
			parentCgroup := ParentCgroupFor(req.Plan)
			if lease.IsBuilder {
				parentCgroup = BuilderCgroupParent
			}
			parentScope := filepath.Join(cgroupRoot, parentCgroup, PerInstanceScope(req.Instance))
			// Main workload: matches the per-instance memory.max (the
			// customer pays for the plan RAM, not the +8 MB overhead).
			// The +8 MB lives on the parent scope and is shared across
			// all workload children.
			if wErr := writeWorkloadCgroup(parentScope, WorkloadNameMain, req.MemSizeMiB); wErr != nil {
				m.log.Warn("cgroup fence: writeWorkloadCgroup main failed, continuing",
					"instance", req.Instance, "err", wErr)
				// Issue #1059 / ADR-127: hardcoded reason="cgroup_fail"
				// on the per-workload cgroup warn-and-continue paths.
				// Same posture as writePlanCgroup above — leaks the
				// warn-and-continue through the metric so the
				// operator-facing surface can spot the sustained case
				// without grepping slog.
				if m.wakeFailureMetrics != nil {
					m.wakeFailureMetrics.WakeFailure("", req.AppID, WakeReasonCgroupFail).Inc()
				}
			}
			for _, sc := range req.Sidecars {
				if sc.RamMB == 0 {
					continue
				}
				if wErr := writeWorkloadCgroup(parentScope, sc.Name, sc.RamMB); wErr != nil {
					m.log.Warn("cgroup fence: writeWorkloadCgroup sidecar failed, continuing",
						"instance", req.Instance, "sidecar", sc.Name, "err", wErr)
					if m.wakeFailureMetrics != nil {
						m.wakeFailureMetrics.WakeFailure("", req.AppID, WakeReasonCgroupFail).Inc()
					}
				}
			}
		}
	}

	// ADR-051 Phase 4 / PR-D: on cold boot, gate the wake on the
	// characterization report. The guest dials host CID 2 at port
	// 1026 with the report framed as [4B msg_type=3][4B body_len][JSON].
	// On deadline we fall back to the scan-hint class — never fail
	// the wake, that's strictly worse than today's `:8080` accept
	// failure path (the operator gets no signal). Restore inherits
	// the class from the apps row captured in the original cold boot.
	//
	// ADR-098 C11: stamp the GuestReadyMs = round-trip from wake RPC
	// return to the framework-ready DGRAM (issue #470 / PR #543).
	// WaitCharacterizationReport IS the framework-ready handshake —
	// measuring around it captures the guest-ready tail that the
	// aggregate wake latency has been hiding. The deadline-elapsed
	// path returns immediately with stamp = characterizationWait,
	// signalling "guest never reached ready" rather than fabricating a
	// value. Restore inherits the framework-ready stamp from the
	// original cold-boot's row, but the restore window itself is
	// already on RestoreMs below, so this field on restore reads as
	// 0 (= not measured) and the wire emits it accordingly.
	var report api.CharacterizationReport
	var guestReadyMs int64
	// Builder VMs do not emit the app readiness/characterization signals;
	// builderd owns completion by waiting for Destroy instead.
	if method == WakeColdBoot && req.ExportDir == "" {
		readyStart := time.Now()
		report, _ = m.vmm.WaitCharacterizationReport(ctx, lease, m.characterizationWait)
		guestReadyMs = time.Since(readyStart).Milliseconds()
		m.log.Info("wake: characterization report",
			"instance", req.Instance, "observed_class", report.ObservedClass,
			"observed_port", report.ObservedPort, "exit", report.ExitCode,
			"port_norm_mode", report.PortNormalizationMode)
	}
	inst := &Instance{Lease: lease, Net: nc, Method: method, AppID: req.AppID, AccountID: req.AccountID, DeploymentID: req.DeploymentID, Plan: req.Plan, Port: req.Port, HealthcheckPath: req.HealthcheckPath, LivenessProbe: append(json.RawMessage(nil), req.LivenessProbe...), StartupDeadlineS: req.StartupDeadlineS, WorkloadNames: workloadNamesFor(req.Sidecars), Characterization: report, Runtime: req.Runtime, RestoreMs: timings.restoreMs, NetnsTapMs: timings.netnsTapMs, GuestReadyMs: guestReadyMs, RestoreError: timings.restoreError}
	// ADR-098 C11: emit the three vmmd-side wake phases onto the
	// dedicated histogram. nil-receiver safe. RestoreMs is 0 on
	// cold boot (no /snapshot/load ran) — the histogram's
	// WithLabelValues will still register the series at 0
	// observations, which is the intended behaviour (a future
	// regression that hits a slow /snapshot/load will surface as
	// a populated restore_ms series, not a missing one).
	if m.wakePhaseMetrics != nil {
		m.wakePhaseMetrics.ObserveWakePhase("restore_ms", timings.restoreMs)
		m.wakePhaseMetrics.ObserveWakePhase("netns_tap_ms", timings.netnsTapMs)
		m.wakePhaseMetrics.ObserveWakePhase("guest_ready_ms", guestReadyMs)
	}
	// Issue #1059 / ADR-127: per-box phase observe on the new
	// *_wake_latency_seconds{box, phase} histogram. The fleet
	// aggregate above (wakePhaseMetrics) is preserved for §12
	// dashboard back-compat; this sibling is the
	// operator-facing per-box view. The `box = "local"`
	// placeholder is replaced by a compute_nodes.id lookup
	// before the multi-host rollout — ADR-127 §3.4.
	// Times are observed as time.Duration so the histogram's
	// prometheus.DefBuckets bounds apply (the existing
	// fleet histogram uses ObserveMicroseconds so we
	// explicitly use seconds here to keep the two histograms
	// dimensionally aligned for the alert at
	// docs/runbooks/FaasColdBootRatioHigh.md).
	if m.wakeFailureMetrics != nil {
		m.wakeFailureMetrics.WakeLatency("local", "restore_ms").Observe(float64(timings.restoreMs) / 1000.0)
		m.wakeFailureMetrics.WakeLatency("local", "netns_tap_ms").Observe(float64(timings.netnsTapMs) / 1000.0)
		m.wakeFailureMetrics.WakeLatency("local", "guest_ready_ms").Observe(float64(guestReadyMs) / 1000.0)
	}
	// tailSecondsAccum (issue #667 / ADR-078) starts at 0 on
	// every Wake; MarkInstanceTailTerminal accumulates into it
	// and the meterd Sampler reads+resets via
	// ReadAndResetTailSeconds once per minute. Zero is the
	// implicit default for an int64 field.
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
	hV4, hV6, herr := m.captureAllowlistHandlesForWake(ctx, nc.Netns, nc.EgressAllowlist)
	if herr != nil {
		m.log.Debug("fcvm: Wake handle capture best-effort failed",
			"instance", req.Instance, "netns", nc.Netns, "err", herr)
	}
	inst.AllowlistHandleV4 = hV4
	inst.AllowlistHandleV6 = hV6
	m.mu.Lock()
	if exitCode, exited := m.pendingProcessExits[req.Instance]; exited {
		delete(m.pendingProcessExits, req.Instance)
		delete(m.waking, req.Instance)
		m.mu.Unlock()
		err = fmt.Errorf("wake %s: firecracker exited before lifecycle registration (exit code %d)", req.Instance, exitCode)
		return nil, err
	}
	delete(m.waking, req.Instance)
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
	// Issue #554 / ADR-078 / PR review fix: start the per-instance
	// liveness probe loop after the live map insert so the cmd/vmmd
	// helper can read Lease.Slot via the same instance id. No-op
	// when the registry isn't wired (unit tests, default-local vmmd).
	// The Manager selects its daemon lifecycle context so the loop
	// survives the short-lived Wake RPC and exits with vmmd shutdown
	// or explicit instance teardown.
	m.startLivenessLoop(ctx, req.Instance, lease.Slot, req.LivenessProbe)
	return inst, nil
}

// bringUp performs restore-or-cold-boot into an already-networked netns. A
// restore miss or failure is NOT terminal — it falls back to cold boot (ADR-005).
// The returned method is what actually happened: a restore that fell back reads
// WakeColdBoot, so schedd can mark the snapshot stale and schedule a re-snapshot.
// A non-nil error means even cold boot failed (a real wake failure).
//
// ADR-098 C11: timings (if non-nil) is the wake-phase scratchpad the
// caller allocates on the stack; bringUp writes restoreMs (and
// never guest-ready / netns — those are stamped at the surrounding
// boundaries inside Wake). The Wake caller reads back via timings
// when constructing the Instance. timings is optional for tests
// that wire bringUp directly without a Wake frame.
func (m *Manager) bringUp(ctx context.Context, lease Lease, nc netns.Config, req WakeRequest, timings *bringUpTimings) (WakeMethod, error) {
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
			HealthcheckPath:  req.HealthcheckPath,
			StartupDeadlineS: req.StartupDeadlineS,
			// Issue #463 / ADR-069 / PR-B: per-workload drives
			// (main + sidecars). Empty = legacy single-workload
			// path. Non-empty → Restore stages one extra drive
			// per entry (read-only for sidecars, read-write for
			// the main workload's drive1). Additive per ADR-016.
			Workloads:      buildWorkloadsForRestore(req),
			SecretsEnvJSON: req.preparedSecretsEnvJSON,
			APIEnvJSON:     req.preparedAPIEnvJSON,
		}
		// ADR-098 C11: stamp the RestoreMs (issue #470 / PR #543).
		// vmm.Restore wraps /snapshot/load + waitReady for the
		// snapshot state machine. On a successful restore this is
		// the dominant sub-window of the §6.3 350 ms warm-wake
		// budget; surfacing it on its own histogram lets the §12
		// panel split restore slowness from guest-init slowness.
		// The duration is captured locally; the Wake() caller reads
		// it back via the optional bringUpTimings closure parameter
		// (defined on Wake — non-breaking on internal bringUp).
		restoreStart := time.Now()
		rErr := m.vmm.Restore(ctx, lease, rs)
		if rErr == nil && timings != nil {
			timings.restoreMs = time.Since(restoreStart).Milliseconds()
		}
		if rErr == nil {
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
			if timings != nil {
				timings.restoreError = rErr.Error()
			}
			m.metrics.ObserveFallback()
			// Issue #1059 / ADR-127: closed-reason counter on the
			// restore-fallback path. Classifies via the typed
			// sentinel + ENOSPC substring match. Note the counter
			// fires for EVERY restore failure regardless of
			// fallback success — operators triaging an
			// ObservedFallback hot-spot need the reason split, not
			// only the cold-boot-fallback rate. nil-safe.
			if m.wakeFailureMetrics != nil {
				reason := ClassifyWakeError(rErr, WakeContext{Snapshot: req.Snapshot, FCVersion: m.fcVersion})
				m.wakeFailureMetrics.WakeFailure("", req.AppID, reason).Inc()
			}
			_ = m.vmm.Kill(ctx, lease)
		}
	}

	spec := ColdBootSpec{
		KernelKey: m.paths.Kernel,
		BaseKey:   req.BaseKey,
		// LayerKey is the legacy single-workload path. When
		// Workloads is non-empty (PR-B / sidecars present),
		// buildWorkloadsForColdBoot copies req.LayerKey into
		// Workloads[0].StorageKey; spec.LayerKey must be empty
		// here so the ColdBootSpec.Validate() "LayerKey must be
		// empty when Workloads is set" check doesn't reject
		// the spec. The Validate contract is the load-bearing
		// guard against double-spec'ing the main workload.
		LayerKey:   layerKeyForColdBoot(req),
		VcpuCount:  req.VcpuCount,
		MemSizeMiB: req.MemSizeMiB,
		Tap:        nc.Tap,
		// Issue #460 / ADR-053, ADR-057 / PR-D: per-deployment
		// override readiness probe path. Empty keeps the legacy
		// TCP-accept on :8080 (pre-PR-D default). Non-empty →
		// waitReady does HTTP GET <HealthcheckPath> against
		// <HostIP>:8080 and accepts 2xx as ready.
		HealthcheckPath:  req.HealthcheckPath,
		StartupDeadlineS: req.StartupDeadlineS,
		SkipReady:        req.ExportDir != "",
		// Issue #463 / ADR-069 / PR-B: per-workload drives
		// (main + sidecars). buildWorkloadsForColdBoot emits an
		// empty slice on the legacy single-workload path so
		// BootColdBoot falls through to the LayerKey branch.
		Workloads:      buildWorkloadsForColdBoot(req),
		SecretsEnvJSON: req.preparedSecretsEnvJSON,
		APIEnvJSON:     req.preparedAPIEnvJSON,
	}
	if err := m.vmm.BootColdBoot(ctx, lease, spec); err != nil {
		// Issue #1059 / ADR-127: terminal cold-boot failure
		// counter. Distinct from the restore-fallback
		// IncrementAbove because BootColdBoot is reached only
		// after the restore path already failed and fell back
		// — a sustained terminal-cold-boot rate implies the
		// host can't cold-boot at all (lv-fc exhaustion,
		// kernel arg change, jailer chroot corruption). The
		// counter is incremented ONCE per cold-boot terminal,
		// not once per restore-fallback. The classifier gets
		// the same call shape (Snapshot may be nil here).
		if m.wakeFailureMetrics != nil {
			reason := ClassifyWakeError(err, WakeContext{FCVersion: m.fcVersion})
			m.wakeFailureMetrics.WakeFailure("", req.AppID, reason).Inc()
		}
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
	// Stop liveness before pausing/snapshotting. A parked VM is expected to
	// stop answering probes; leaving the loop active through Snapshot lets it
	// race this teardown and report a second failure for the same instance.
	m.DeleteLivenessConsecutiveFailures(instance)
	m.cancelLivenessLoop(instance)

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
	m.cleanup(ctx, inst.Lease, inst.Net, inst.WorkloadNames)
	m.log.Info("parked", "instance", instance, "mem_bytes", info.MemBytes)
	return info, nil
}

// WarmSnapshot (issue #470 / PR #470-FU-A) is the warm-tier
// capture entry point. The flow:
//
//  1. Look up the live Instance by name (caller is the engine's
//     captureWarmSnapshotLocked; the instance is in RUNNING state
//     and the runner is alive and can keep serving requests across
//     the pause window).
//  2. Call vmm.SnapshotKeepAlive (pause + /snapshot/create + publish
//     mem + vmstate through the configured StorageBackend) WITHOUT
//     releasing the chroot.
//  3. Call vmm.ResumeVM to PATCH /vm {"state":"Resumed"} so the
//     runner can keep accepting requests. Manager.live[instance] +
//     cidToID stay intact — the warm path is purposefully a thin
//     wrapper around SnapshotKeepAlive + ResumeVM, no teardown.
//  4. Return the SnapshotInfo the engine writes into the snapshots
//     row (tier='warm').
//
// Failure path: any error from SnapshotKeepAlive OR ResumeVM is
// surfaced as-is. The engine's captureWarmSnapshotLocked decides
// whether to Destroy the VM and skip the init-tier capture (the
// locked decision in PR-A: yes, destroy on warm failure). The
// Manager itself does NOT touch m.live on the error path — the
// engine owns the destroy so the audit/state-machine transitions
// stay in one place.
func (m *Manager) WarmSnapshot(ctx context.Context, instance string, spec SnapshotSpec) (SnapshotInfo, error) {
	m.mu.Lock()
	inst, ok := m.live[instance]
	m.mu.Unlock()
	if !ok {
		return SnapshotInfo{}, fmt.Errorf("warm_snapshot %s: not live", instance)
	}
	info, err := m.vmm.SnapshotKeepAlive(ctx, inst.Lease, spec)
	if err != nil {
		// The VM is still paused (SnapshotKeepAlive publishes on
		// success but the chroot is still alive). Best-effort
		// resume so the runner can keep serving — failure to
		// resume surfaces to the engine's destroy path with the
		// original error wrapped.
		if rerr := m.vmm.ResumeVM(ctx, inst.Lease); rerr != nil {
			return SnapshotInfo{}, fmt.Errorf("warm_snapshot %s: %w", instance, errors.Join(err, fmt.Errorf("resume after snapshot failure: %w", rerr)))
		}
		return SnapshotInfo{}, fmt.Errorf("warm_snapshot %s: snapshot: %w", instance, err)
	}
	if err := m.vmm.ResumeVM(ctx, inst.Lease); err != nil {
		return SnapshotInfo{}, fmt.Errorf("warm_snapshot %s: resume: %w", instance, err)
	}
	m.log.Info("warm_snapshot", "instance", instance, "mem_bytes", info.MemBytes)
	return info, nil
}

// SnapshotKeepAlive pauses and snapshots a live instance without destroying
// its VM, for the cross-node migration prepare phase. Unlike WarmSnapshot,
// this method deliberately leaves the VM paused until ResumeVM or Destroy is
// called by the migration lease owner. Liveness is stopped while the guest is
// paused so the monitor cannot turn a successful handoff into a false failure.
// On a snapshot error the VM is resumed best-effort because no migration lease
// has been returned to the caller yet.
func (m *Manager) SnapshotKeepAlive(ctx context.Context, instance string, spec SnapshotSpec) (SnapshotInfo, error) {
	if m == nil {
		return SnapshotInfo{}, fmt.Errorf("snapshot_keep_alive %s: nil manager", instance)
	}
	m.mu.Lock()
	inst, ok := m.live[instance]
	m.mu.Unlock()
	if !ok {
		return SnapshotInfo{}, fmt.Errorf("snapshot_keep_alive %s: not live", instance)
	}
	if m.vmm == nil {
		return SnapshotInfo{}, fmt.Errorf("snapshot_keep_alive %s: nil vmm", instance)
	}
	m.DeleteLivenessConsecutiveFailures(instance)
	m.cancelLivenessLoop(instance)
	info, err := m.vmm.SnapshotKeepAlive(ctx, inst.Lease, spec)
	if err == nil {
		return info, nil
	}
	resumeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	resumeErr := m.vmm.ResumeVM(resumeCtx, inst.Lease)
	cancel()
	if resumeErr != nil {
		return SnapshotInfo{}, fmt.Errorf("snapshot_keep_alive %s: %w", instance,
			errors.Join(err, fmt.Errorf("resume after snapshot failure: %w", resumeErr)))
	}
	m.startLivenessLoop(context.WithoutCancel(ctx), instance, inst.Lease.Slot, inst.LivenessProbe)
	return SnapshotInfo{}, fmt.Errorf("snapshot_keep_alive %s: snapshot: %w", instance, err)
}

// ResumeVM resumes a migration-prepared instance and restarts its liveness
// monitor. It is idempotent at the VMM layer, which lets cancel and lease
// expiry safely race with a late acknowledgement.
func (m *Manager) ResumeVM(ctx context.Context, instance string) error {
	if m == nil {
		return fmt.Errorf("resume_vm %s: nil manager", instance)
	}
	m.mu.Lock()
	inst, ok := m.live[instance]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("resume_vm %s: not live", instance)
	}
	if m.vmm == nil {
		return fmt.Errorf("resume_vm %s: nil vmm", instance)
	}
	if err := m.vmm.ResumeVM(ctx, inst.Lease); err != nil {
		return fmt.Errorf("resume_vm %s: %w", instance, err)
	}
	m.startLivenessLoop(context.WithoutCancel(ctx), instance, inst.Lease.Slot, inst.LivenessProbe)
	return nil
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

// SignalAndKill (M-2 / ADR-138 §Decision 1) is the Manager-level
// wrapper around JailerVMM.SignalAndKill — the graceful
// signal-grace-SIGKILL stop sequence used by Engine.StopInstance
// for worker / job mode instances. Returns
// (killSignalSent, exitCode, err); killSignalSent is true iff
// vmmd had to escalate to SIGKILL after the grace window
// expired.
//
// Like Destroy, the Manager drops the live row + CID→instance
// join + calls cleanup() unconditionally on the way out so
// §6.2-4/5 invariants are preserved even when the inner
// SignalAndKill returns an error (the error is surfaced; cleanup
// has already happened). Caller (vmmdgrpc.Server.StopInstance)
// translates the (killSignalSent, exitCode) pair into the
// StopInstanceResponse wire envelope.
//
// Returns (false, 0, nil) when the instance is unknown to the
// Manager — same idempotent-on-unknown contract as Destroy.
func (m *Manager) SignalAndKill(ctx context.Context, instance string, signal syscall.Signal, grace time.Duration) (killSignalSent bool, exitCode int32, err error) {
	m.mu.Lock()
	inst, ok := m.live[instance]
	if ok {
		delete(m.live, instance)
		delete(m.cidToID, GuestVsockCID(inst.Lease.Slot))
	}
	m.mu.Unlock()
	if !ok {
		// Unknown instance — match Destroy's idempotent shape.
		return false, 0, nil
	}
	killSignalSent, exitCode, err = m.vmm.SignalAndKill(ctx, inst.Lease, signal, grace)
	// Cleanup uses a context detached from the caller's (same
	// rationale as DestroyWithExport above — a cancelled caller's
	// ctx must not leak netns / cgroup).
	m.cleanup(context.WithoutCancel(ctx), inst.Lease, inst.Net, inst.WorkloadNames)
	m.mu.Lock()
	delete(m.exportDirs, instance)
	m.mu.Unlock()
	return killSignalSent, exitCode, err
}

func (m *Manager) DestroyWithExport(ctx context.Context, instance, exportDir string) (int, error) {
	// Stop background liveness work before removing the live entry or
	// waiting on the VMM. A liveness report can race this destroy path;
	// cancelling first prevents the loop from probing an instance whose
	// resources are already being torn down. Keep this outside the live-map
	// branch so an idempotent destroy also cleans up a stale registration.
	m.DeleteLivenessConsecutiveFailures(instance)
	m.cancelLivenessLoop(instance)

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
	m.cleanup(context.WithoutCancel(ctx), inst.Lease, inst.Net, inst.WorkloadNames)
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

// LiveInstances returns a snapshot copy of the Manager's live map,
// keyed by instance name. The returned map is a fresh copy taken
// under m.mu, so concurrent Destroy / Wake updates do not race the
// caller's iteration. Used by metal tests (issue #554 / ADR-069
// follow-up: pkg/fcvm/sidecar_metal_test.go) that need to look up a
// just-ColdBooted instance by name from outside the Manager's hot
// path; production code should prefer the targeted accessors
// (InstanceByCID, InstanceAppID, InstanceDeploymentIDAndAppID).
//
// Returns nil when no instances are live.
func (m *Manager) LiveInstances() map[string]*Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.live) == 0 {
		return nil
	}
	out := make(map[string]*Instance, len(m.live))
	for id, inst := range m.live {
		out[id] = inst
	}
	return out
}

// LastLivenessDestroyAtForDeployment (issue #554 closure /
// ADR-078 cooldown gate, code review #725 finding F1) returns the
// most recent ReportLivenessFailed stamp for the named deployment,
// or the zero time if no destroy has been recorded. The cmd/vmmd
// liveness_recv loop reads this on every tick (keyed on the
// cold-boot replacement's deploymentID, which inherits across
// wakes because schedd threads it into the new Instance row at
// CreateInstance) and short-circuits fires within
// cfg.CooldownSeconds. The read is under m.mu; the stamp side is
// too (see ReportLivenessFailed). Returns zero time on a nil
// receiver or unknown deployment so the gate's bypass branch
// fires cleanly on cold-boot and on the legacy pre-PR-B path
// (no deployment_id on the wire).
//
// The previous design keyed on instanceID via the dying Instance's
// LastLivenessDestroyAt field. That was structurally broken in
// production: Park deletes the live-map entry, the cold-boot
// replacement carries a fresh zero-valued Instance{}, and the gate
// was a no-op. Keying on deploymentID survives the cold-boot
// because schedd's CreateInstance threads deploymentID into the
// new Instance row and Wake stamps it onto the live record at
// BringUp.
func (m *Manager) LastLivenessDestroyAtForDeployment(deploymentID string) time.Time {
	if m == nil || deploymentID == "" {
		return time.Time{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cooldownByDeployment[deploymentID]
}

// RegisterInstanceForTest is a test-only seam that injects an
// instance into the live map so cooldown / liveness unit tests
// (cmd/vmmd/liveness_recv_test.go) can drive
// Manager.LastLivenessDestroyAtForDeployment without going through
// the full BringUp path. The BringUp path requires Runner + VMM +
// real netns which a unit test doesn't have. The function name is
// verbose on purpose — production code MUST NOT call this.
// Returns the manager for chaining.
//
// `deploymentID` is the deployments.id that the cold-boot
// replacement would inherit; the stamp lives at
// cooldownByDeployment[deploymentID], not on the Instance, so the
// stamp survives the test's Register→SetLivenessDestroy sequence
// even if the Instance is replaced. Empty deploymentID skips the
// stamp seam (legacy pre-PR-B path is also exempt).
func (m *Manager) RegisterInstanceForTest(instanceID, deploymentID string) *Manager {
	if m == nil {
		return m
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.live == nil {
		m.live = map[string]*Instance{}
	}
	if m.cooldownByDeployment == nil {
		m.cooldownByDeployment = map[string]time.Time{}
	}
	if _, ok := m.live[instanceID]; !ok {
		m.live[instanceID] = &Instance{DeploymentID: deploymentID}
	} else if deploymentID != "" {
		// Update the DeploymentID on an existing entry so the
		// test-side stamp matches the test-side loop's read key.
		m.live[instanceID].DeploymentID = deploymentID
	}
	return m
}

// SetLastLivenessDestroyAtForDeployment is a test-only seam that
// stamps Manager.cooldownByDeployment[deploymentID] = t. Mirrors
// the production ReportLivenessFailed side effect without driving
// the schedd relay. Test-only by naming convention. Empty
// deploymentID is a no-op so a test that forgets to register the
// deployment fails closed (no stamp → gate bypasses, no false
// short-circuit).
func (m *Manager) SetLastLivenessDestroyAtForDeployment(deploymentID string, t time.Time) {
	if m == nil || deploymentID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cooldownByDeployment == nil {
		m.cooldownByDeployment = map[string]time.Time{}
	}
	m.cooldownByDeployment[deploymentID] = t
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

// SnapshotLiveHostIPs returns a copy of the instance-to-host-IP map used by
// compute-side conntrack attribution. The map is intentionally separate from
// SnapshotLive, whose values are veth names for byte telemetry.
func (m *Manager) SnapshotLiveHostIPs() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string, len(m.live))
	for id, inst := range m.live {
		if inst == nil || !inst.Lease.HostIP.IsValid() {
			continue
		}
		out[id] = inst.Lease.HostIP.String()
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

// operatorBundleSnapshot returns a copy of the current
// operator-bundle slice under the read lock so callers can
// safely merge it into a Wake or live-patch path without
// worrying about concurrent SetEgressOperatorBundle calls.
// Issue #679 / PR-A.
func (m *Manager) operatorBundleSnapshot() []netip.Prefix {
	m.operatorBundleMu.RLock()
	defer m.operatorBundleMu.RUnlock()
	if len(m.operatorBundle) == 0 {
		return nil
	}
	out := make([]netip.Prefix, len(m.operatorBundle))
	copy(out, m.operatorBundle)
	return out
}

// SetEgressOperatorBundle installs the operator-bundle CIDRs
// (issue #679 / PR-A) and patches every live netns whose
// effective allowlist differs. Thread-safe; briefly under
// m.mu to snapshot targets; release before nft exec. The
// patch reuses UpdateEgressAllowlist's per-netns argv builders
// + applyOneInstancePatch code path.
//
// cidrs MUST already be sorted + dedup'd — the loader
// (cmd/vmmd/egress_bundle.go::LoadEgressBundle) is
// responsible. Empty slice reverts each netns to its per-app
// slice (the merge becomes a no-op; if prior was merged, patch
// with just per-app). Operators can ALWAYS subtract reachability
// by editing the bundle file to remove a CIDR and SIGHUPing.
//
// The per-app slice is read from m.perAppAllowlist (the
// authoritative cache populated by Wake and UpdateEgressAllowlist
// BEFORE the operator-bundle merge). Reading from
// inst.Net.EgressAllowlist would re-merge the previous operator
// bundle on top of the new one — wrong — so the per-app cache is
// the only correct source. Apps that have never been seen by
// Wake (AppID == "" instances) are skipped; they don't have a
// per-app slice to merge with.
//
// nil-safe on m (a Manager constructed without ever calling
// SetEgressOperatorBundle takes the empty path → noop for
// empty input).
func (m *Manager) SetEgressOperatorBundle(cidrs []netip.Prefix) {
	m.operatorBundleMu.Lock()
	m.operatorBundle = cidrs
	m.operatorBundleMu.Unlock()

	// Snapshot the authoritative per-app slice map under a
	// single read-lock acquisition. One lock, no per-appID
	// re-entry.
	m.perAppAllowlistMu.RLock()
	perAppByID := make(map[string][]netip.Prefix, len(m.perAppAllowlist))
	for appID, slice := range m.perAppAllowlist {
		cp := make([]netip.Prefix, len(slice))
		copy(cp, slice)
		perAppByID[appID] = cp
	}
	m.perAppAllowlistMu.RUnlock()

	if len(perAppByID) == 0 {
		return
	}
	for appID, perApp := range perAppByID {
		if err := m.UpdateEgressAllowlist(context.Background(), appID, perApp); err != nil {
			m.log.Warn("fcvm: SetEgressOperatorBundle patch failed; live netns may be stale until next reconcile",
				"app_id", appID, "err", err)
		}
	}
}

// SetStaticEgressIPAliases (ADR-119) installs the operator-side
// alias set on br-tenants so the kernel accepts the customer's
// IP as a source on this host. The renderer emits the per-VM
// SNAT rule at Wake time and on UpdateStaticEgressIP; the alias
// is what makes the rule work end-to-end (the kernel rejects
// SNAT-to-an-unbound-IP at nft install time on most distros).
//
// The set is replaced wholesale: the diff between the incoming
// entries and the prior set drives `ip addr add` (new) and
// `ip addr del` (removed). An unchanged set is a no-op. The
// per-app pairing is logged at Debug so an operator can verify
// the SIGHUP reload.
//
// Idempotent: re-invoking with the same entries is a no-op. A
// call with no entries clears every alias (the "rotate to /0"
// path). Failures are Warned and the prior alias set stays
// live — a transient `ip addr` error never silently strips a
// customer's static IP.
//
// ADR-119 calls this out as the single-node v1 path: each
// control-plane node owns its own bridge. Multi-host placement
// pin is a follow-up ADR.
func (m *Manager) SetStaticEgressIPAliases(entries []StaticEgressIPEntry) {
	// Normalise: appID → IP map. The TOML loader already
	// enforces last-wins per app_id, so a plain copy is the
	// canonical shape.
	want := make(map[string]netip.Addr, len(entries))
	for _, e := range entries {
		want[e.AppID] = e.IP
	}

	m.staticEgressAliasesMu.Lock()
	defer m.staticEgressAliasesMu.Unlock()
	prev := m.staticEgressAliases

	// Diff: which IPs need to be added, which need to be
	// removed. The per-app ID is informational only here — the
	// alias is on br-tenants (the bridge), not per-VM, so two
	// apps on the same IP would collide at the alias layer
	// anyway; the apid handler's cross-app quota of 1
	// (per-IP per-account) prevents that. The diff operates
	// on the IP set, not the (app, IP) pair.
	wantIPs := make(map[netip.Addr]struct{}, len(want))
	for _, ip := range want {
		wantIPs[ip] = struct{}{}
	}
	prevIPs := make(map[netip.Addr]struct{}, len(prev))
	for _, ip := range prev {
		prevIPs[ip] = struct{}{}
	}

	type op struct {
		ip   netip.Addr
		verb string // "add" or "del"
	}
	var ops []op
	for ip := range wantIPs {
		if _, had := prevIPs[ip]; !had {
			ops = append(ops, op{ip: ip, verb: "add"})
		}
	}
	for ip := range prevIPs {
		if _, has := wantIPs[ip]; !has {
			ops = append(ops, op{ip: ip, verb: "del"})
		}
	}
	if len(ops) == 0 {
		return // no change
	}

	// Apply. Best-effort: each `ip addr` call is its own argv;
	// a failure on one surfaces as a Warn and we continue with
	// the remaining ops so a partial state is still better than
	// silently skipping everything.
	for _, o := range ops {
		argv := []string{"ip", "addr", o.verb, o.ip.String() + "/32", "dev", "br-tenants"}
		if err := m.run.Run(context.Background(), argv); err != nil {
			m.log.Warn("fcvm: SetStaticEgressIPAliases ip addr failed",
				"verb", o.verb, "ip", o.ip.String(), "err", err)
			continue
		}
		m.log.Debug("fcvm: SetStaticEgressIPAliases applied",
			"verb", o.verb, "ip", o.ip.String())
	}
	m.staticEgressAliases = want
}

// mergeOperatorBundle appends the operator bundle to the
// per-app slice and returns the union (sorted + dedup'd).
// The per-app slice is treated as authoritative for ordering;
// the operator bundle is appended then dedup'd against the
// combined set. nil/empty operator bundle returns the
// per-app slice unchanged.
//
// Issue #679 / PR-A.
func (m *Manager) mergeOperatorBundle(perApp []netip.Prefix) []netip.Prefix {
	bundle := m.operatorBundleSnapshot()
	if len(bundle) == 0 {
		return perApp
	}
	combined := make([]netip.Prefix, 0, len(perApp)+len(bundle))
	combined = append(combined, perApp...)
	combined = append(combined, bundle...)
	return dedupSortedPrefixes(combined)
}

// dedupSortedPrefixes removes duplicate netip.Prefix values
// from a slice that may not be sorted. Preserves first-seen
// order (per-app entries arrive first). /0 is filtered out
// defensively (loader already drops them, but a wire-bypass
// could smuggle a /0 — same non-/0 contract as the Wake-side
// parser at manager.go:1900-1912). Issue #679 / PR-A.
func dedupSortedPrefixes(in []netip.Prefix) []netip.Prefix {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]netip.Prefix, 0, len(in))
	for _, p := range in {
		if p.Bits() == 0 {
			continue
		}
		key := p.String()
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}
	return out
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
	// Issue #679 / PR-A: cache the per-app slice BEFORE the
	// operator-bundle merge so SetEgressOperatorBundle can read
	// the authoritative per-app set on a subsequent bundle
	// reload. Without this, the only place to recover the
	// per-app slice is inst.Net.EgressAllowlist, which is the
	// merged slice (Wake stamps it at line 1940) and would
	// re-merge the previous operator bundle on top of the new
	// one — operators could not subtract reachability.
	m.perAppAllowlistMu.Lock()
	if m.perAppAllowlist == nil {
		m.perAppAllowlist = make(map[string][]netip.Prefix)
	}
	cp := make([]netip.Prefix, len(allowlist))
	copy(cp, allowlist)
	m.perAppAllowlist[appID] = cp
	m.perAppAllowlistMu.Unlock()

	// Issue #679 / PR-A: merge the operator-managed egress
	// bundle into the per-app slice before the per-instance
	// argv build. The cached `prior` (read from
	// inst.Net.EgressAllowlist) is already the merged slice
	// (Wake stamps it), so the samePrefixSet fast-path
	// correctly compares merged-vs-merged below.
	allowlist = m.mergeOperatorBundle(allowlist)

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

// UpdateStaticEgressIP (ADR-119 redesign) is the gRPC handler
// invoked by schedd's pg_notify egress-drift subscriber when an
// app's `static_egress_ip` column changes. The per-VM patch path
// is gone (the per-netns SNAT is dead code — nftables NAT is
// first-match + terminal, so the broad MASQUERADE shadows any
// sibling SNAT). The host renderer is the new authority: this
// handler updates the per-app cache, then rebuilds the host
// renderer's StaticEgressRules list and triggers a reload.
//
// `ip == ""` is the "clear" path — the per-app cache entry is
// dropped and the host renderer rebuilds without that app's
// rule. A non-empty IP is validated here the same way Wake
// validates it (parse + IPv4 + canonical deny-set) so a
// malformed or reserved value cannot reach the host ruleset.
//
// Idempotent fast-path: when the cached per-app IP equals the
// incoming IP, the host rebuild is skipped (the ruleset is
// already correct). Schedd's redelivery-on-reconnect lands
// here; without the fast-path the host renderer would rewrite
// the same bytes on every reconnect and atomic-rename the file
// for nothing.
//
// Failure model matches UpdateEgressAllowlist: a failed
// host-renderer reload logs at Warn and returns; the next
// cache mutation (a Wake, Teardown, SIGHUP, or another drift
// event) re-triggers the rebuild. The renderer reload is
// gated through the staging-dir + atomic-replace pipeline in
// cmd/vmmd/egress_watcher.go (a failed Render does NOT break
// the live ruleset).
func (m *Manager) UpdateStaticEgressIP(ctx context.Context, accountID, appID string, ip string) error {
	if appID == "" {
		return fmt.Errorf("fcvm: UpdateStaticEgressIP: empty app_id")
	}
	var next *netip.Addr
	if ip != "" {
		parsed, err := netip.ParseAddr(ip)
		if err != nil {
			return fmt.Errorf("fcvm: UpdateStaticEgressIP app=%s: invalid dotted-quad %q: %w", appID, ip, err)
		}
		if !parsed.Is4() {
			return fmt.Errorf("fcvm: UpdateStaticEgressIP app=%s: rejected %q (IPv6 deferred)", appID, ip)
		}
		if err := api.ValidateStaticEgressIP(parsed); err != nil {
			return fmt.Errorf("fcvm: UpdateStaticEgressIP app=%s: rejected %q: %w", appID, ip, err)
		}
		next = &parsed
	}

	// Idempotent fast-path: cached per-app IP already matches
	// the incoming IP. Skip the cache write + alloc + rebuild.
	m.perAppStaticIPMu.RLock()
	cur, exists := m.perAppStaticIP[appID]
	m.perAppStaticIPMu.RUnlock()
	if next == nil && cur == nil {
		return nil
	}
	if next != nil && exists && cur.Compare(*next) == 0 {
		return nil
	}

	// ADR-119 redesign note: vmmd owns the per-VM host IP
	// reservation. The set path allocates from the dedicated
	// 10.200.0.0/16 pool (alloc.go::AcquireStaticEgressIP); the
	// clear path returns it (alloc.go::ReleaseStaticEgressIP).
	// The reservation is what makes the Wake path able to look
	// up the per-VM host IP for the app — without this the host
	// renderer would have nothing to bind into the rule's `ip
	// saddr <per-VM-host-IP>` source field.
	if next != nil {
		if _, rerr := AcquireStaticEgressIP(accountID, appID, *next); rerr != nil {
			return fmt.Errorf("fcvm: UpdateStaticEgressIP app=%s: reserve per-VM host IP: %w", appID, rerr)
		}
	} else {
		if rerr := ReleaseStaticEgressIP(accountID, appID); rerr != nil {
			// Best-effort release — a release failure does
			// not block the cache clear. The reservation
			// will be re-released on the next reconcile
			// (vmmd restart walks alloc.go's reseed path).
			m.log.Warn("fcvm: UpdateStaticEgressIP release alloc failed", "app_id", appID, "err", rerr)
		}
	}

	// Update the per-app cache. Write the new value BEFORE the
	// host rebuild so the rebuild reads the consistent value
	// (matches the UpdateEgressAllowlist ordering invariant).
	m.perAppStaticIPMu.Lock()
	if m.perAppStaticIP == nil {
		m.perAppStaticIP = make(map[string]*netip.Addr)
	}
	if next == nil {
		m.perAppStaticIP[appID] = nil
	} else {
		ipCopy := *next
		m.perAppStaticIP[appID] = &ipCopy
	}
	m.perAppStaticIPMu.Unlock()

	// Rebuild the host renderer. If there are no live VMs for
	// this app, the rebuild still walks the per-VM map and
	// drops the rule (the alloc.go reservation persists
	// until the customer clears the pin, so a subsequent Wake
	// re-adds the rule from the per-VM host IP registration).
	m.rebuildHostStaticEgressRules(ctx)
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
	// A crashed vmmd/jailer can leave a regular namespace marker behind even
	// after `ip netns del` reports an invalid peer. Clear that exact stale
	// marker before reusing the allocator-derived name; a real mounted netns
	// is still handled by iproute2, while only a regular file reaches the
	// filesystem fallback.
	if err := m.run.Run(ctx, []string{"ip", "netns", "del", nc.Netns}); err != nil {
		m.log.Debug("stale netns cleanup (best-effort)",
			"instance", nc.Instance, "netns", nc.Netns, "err", err)
	}
	removeStaleNetnsMarker(nc.Netns)
	if nc.VethHost != "" {
		if err := m.run.Run(ctx, []string{"ip", "link", "del", nc.VethHost}); err != nil {
			m.log.Debug("stale veth cleanup (best-effort)",
				"instance", nc.Instance, "veth", nc.VethHost, "err", err)
		}
	}
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
	return m.runNftCommands(ctx, nc.Netns, nc.NftCommands())
}

// runNftCommands loads the per-instance ruleset through one nft process when
// the runner supports stdin. A default ruleset contains dozens of individual
// rules; spawning ip+nsenter+nft once per rule accounted for a measurable
// fraction of every restore despite the kernel work itself being tiny.
func (m *Manager) runNftCommands(ctx context.Context, netnsName string, cmds [][]string) error {
	inputRunner, ok := m.run.(InputRunner)
	if !ok || len(cmds) == 0 {
		return m.runCommands(ctx, cmds)
	}
	prefix := []string{"ip", "netns", "exec", netnsName, "nft"}
	var script strings.Builder
	for _, argv := range cmds {
		if len(argv) <= len(prefix) || !slices.Equal(argv[:len(prefix)], prefix) {
			return fmt.Errorf("unexpected nft command prefix: %v", argv)
		}
		script.WriteString(strings.Join(argv[len(prefix):], " "))
		script.WriteByte('\n')
	}
	batchArgv := append(append([]string{}, prefix...), "-f", "-")
	if err := inputRunner.RunInput(ctx, batchArgv, []byte(script.String())); err != nil {
		return fmt.Errorf("nft ruleset batch: %w", err)
	}
	return nil
}

// removeStaleNetnsMarker removes only a regular file in the iproute2 netns
// directory. It is a recovery path for a partially-created namespace marker;
// mounted namespaces and other filesystem objects are left to iproute2.
func removeStaleNetnsMarker(name string) {
	path := filepath.Join("/run/netns", name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	_ = os.Remove(path)
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

// captureAllowlistHandlesForWake avoids nft list operations for families that
// cannot contain an allowlist rule. Config.NftCommands emits no rule when the
// effective allowlist is empty, and emits one rule only for each address family
// represented in the list. Keep the general captureAllowlistHandles method
// unchanged for live-patch paths, where a caller may need to inspect an
// already-rendered chain.
func (m *Manager) captureAllowlistHandlesForWake(ctx context.Context, netnsName string, allowlist []netip.Prefix) (uint64, uint64, error) {
	if m.captureRunner == nil {
		return 0, 0, nil
	}
	var hasV4, hasV6 bool
	for _, prefix := range allowlist {
		switch {
		case prefix.Addr().Is4():
			hasV4 = true
		case prefix.Addr().Is6():
			hasV6 = true
		}
	}
	if !hasV4 && !hasV6 {
		return 0, 0, nil
	}

	var hV4, hV6 uint64
	if hasV4 {
		var err error
		hV4, err = listChainHandles(ctx, m.captureRunner, netnsName, "ip", "faas", "forward")
		if err != nil {
			return 0, 0, err
		}
	}
	if hasV6 {
		var err error
		hV6, err = listChainHandles(ctx, m.captureRunner, netnsName, "ip6", "faas", "forward")
		if err != nil {
			return 0, 0, err
		}
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
func (m *Manager) cleanup(ctx context.Context, lease Lease, nc netns.Config, workloadNames []string) {
	// A child can report its exit after an explicit Destroy/failed Wake
	// removed the live entry but before Kill has finished. Do not let that
	// expected exit poison a later Wake using the same instance id.
	m.mu.Lock()
	delete(m.pendingProcessExits, lease.Instance)
	delete(m.waking, lease.Instance)
	m.mu.Unlock()
	// Issue #463 / ADR-069 / PR-B: tear down per-workload cgroup child
	// scopes BEFORE vmm.Kill removes the parent scope. The kernel
	// cascade-removes children when the parent goes, but the parent
	// removal needs cgroup.procs to be empty in the parent first;
	// reaping the workload children explicitly shortens the leak
	// window for a `make leakcheck` run that immediately follows
	// cleanup. Best-effort: children with EBUSY are logged and
	// swallowed (same posture as vmm.Kill's parent removal below).
	if len(workloadNames) > 0 {
		parentCgroup := ParentCgroupFor(lease.Plan)
		if lease.IsBuilder {
			parentCgroup = BuilderCgroupParent
		}
		parentScope := filepath.Join(cgroupRoot, parentCgroup, PerInstanceScope(lease.Instance))
		removeWorkloadCgroups(parentScope, workloadNames)
	}
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

	// A teardown command failing is genuinely ambiguous: the resource may
	// never have been created (benign — the loop runs unconditionally), or
	// it may exist and have refused to go away (a real leak). Both used to
	// land on the same Debug line, so in production, which runs at INFO,
	// every leak was invisible.
	//
	// That is how 21 netns and 14 veth accumulated on one compute node by
	// 2026-09-04. The cost is not cosmetic: netns operations slow as the
	// namespace count grows, and a `tc qdisc add` inside setupNetwork
	// eventually consumed the entire 35s cold-boot budget, so no
	// deployment could reach `live`. §6.2-4/5 and `make leakcheck` both
	// require zero leaked netns/TAPs; nothing enforced that at runtime.
	//
	// Checking the marker after the fact separates the two cases. Only a
	// namespace that still exists is reported, so the benign path stays
	// quiet and a real leak is greppable and countable.
	if nc.Netns != "" {
		if _, statErr := os.Lstat(filepath.Join("/run/netns", nc.Netns)); statErr == nil {
			// Log only. Deliberately NOT counted on
			// vmmd_wake_failure_total{reason=netns_fail}: that series
			// means "a wake failed because netns setup failed", and a
			// leak during cleanup is a different event. Folding them
			// together would corrupt the §12 panel legend that the
			// setupNetwork call site documents as load-bearing. A
			// dedicated counter belongs with the leak reaper.
			m.log.Warn("cleanup: netns survived teardown (leak)",
				"instance", lease.Instance, "netns", nc.Netns)
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

// buildWorkloadsForColdBoot (issue #463 / ADR-069 / PR-B)
// assembles the per-workload drive slice cold-boot stages.
// Returns nil when WakeRequest.Sidecars is empty so BootColdBoot
// falls through to the legacy single-workload path (LayerKey);
// otherwise returns [main, sidecar-1, …, sidecar-N] where
// the main workload's StorageKey is taken from req.LayerKey.
//
// The drive-slot suffix uses DriveSidecarPrefix + index so
// guest-init's overlay assembly can key off it; the per-
// workload cgroup (PR-B's nested scope) uses WorkloadSpec.Name
// as the leaf directory under the instance cgroup.
func buildWorkloadsForColdBoot(req WakeRequest) []WorkloadSpec {
	if len(req.Sidecars) == 0 {
		return nil
	}
	// Reject any sidecar named "main" (PR-B review finding #3).
	// The "main" leaf is reserved for the main workload (Workloads[0]
	// below) — it doubles as the cgroup scope name under the
	// per-instance scope (writeWorkloadCgroup) and as the
	// guest-init orchestrator's filter key in runCharacterizationForSup
	// (only the main supervisor's PID drives bind detection). A
	// sidecar that shadows the name would either (a) collide on
	// the cgroup mkdir path or (b) silently take over the
	// characterize probe. apid's API gate (PR-A) blocks the same
	// name on POST, but this is the host-side defence-in-depth so
	// an older apid or a hand-crafted WakeRequest from a metal
	// test cannot bypass it. We skip the offending sidecar so a
	// single bad entry doesn't fail the whole deployment; the
	// apid gate's error message is the user-facing surface.
	out := make([]WorkloadSpec, 0, 1+len(req.Sidecars))
	// Workloads[0] is always the main workload.
	out = append(out, WorkloadSpec{
		Name:       WorkloadNameMain,
		Type:       WorkloadNameMain,
		StorageKey: req.LayerKey,
		DriveID:    DriveLayerMain,
		RamMB:      req.MemSizeMiB,
		Port:       req.Port,
		Essential:  true,
	})
	for _, sc := range req.Sidecars {
		if sc.Name == WorkloadNameMain {
			continue
		}
		out = append(out, WorkloadSpec{
			Name:            sc.Name,
			Type:            sc.Type,
			Image:           sc.Image,
			StorageKey:      sc.StorageKey,
			DriveID:         sc.DriveID, // imaged populated this on the wire
			RamMB:           sc.RamMB,
			Port:            sc.Port,
			Essential:       sc.Essential,
			Cmd:             append([]string(nil), sc.Cmd...),
			Entrypoint:      append([]string(nil), sc.Entrypoint...),
			SealedEnv:       append([]SealedEnvEntry(nil), sc.SealedEnv...),
			preparedEnvJSON: append([]byte(nil), sc.preparedEnvJSON...),
		})
	}
	return out
}

// buildWorkloadsForRestore is the wake-restore twin of
// buildWorkloadsForColdBoot (issue #463 / ADR-069 / PR-B).
// The cold-boot helper derives Workloads from req.LayerKey +
// req.Sidecars; the restore twin must use the SAME shape so
// the per-workload drive ordering matches across first-boot
// and every subsequent wake. Future restore-side state
// (snapshot hash, etc.) should be threaded here when PR-C
// wires the snapshot-blob-per-workload invariant.
func buildWorkloadsForRestore(req WakeRequest) []WorkloadSpec {
	return buildWorkloadsForColdBoot(req)
}

// workloadNamesFor (issue #463 / ADR-069 / PR-B) returns the
// child-scope names that writeWorkloadCgroup populates under
// the per-instance scope at Wake time. Returns nil (= legacy
// single-workload path) when Sidecars is empty so the deferred
// child-scope removal in cleanup is a no-op for pre-PR-B
// callers. The ordering is "main" first, then sidecars in
// req.Sidecars order; this matches the deduping order in
// buildWorkloadsForColdBoot so the two helper outputs stay
// in lockstep.
func workloadNamesFor(sidecars []WorkloadSpec) []string {
	if len(sidecars) == 0 {
		return nil
	}
	out := make([]string, 0, 1+len(sidecars))
	out = append(out, WorkloadNameMain)
	for _, sc := range sidecars {
		out = append(out, sc.Name)
	}
	return out
}

// layerKeyForColdBoot (issue #463 / ADR-069 / PR-B) returns the
// LayerKey to stamp on the ColdBootSpec. Empty when Workloads
// is non-empty so the spec's "LayerKey must be empty when
// Workloads is set" Validate check accepts it; non-empty on
// the legacy single-workload path so the BootColdBoot branch
// that resolves spec.LayerKey runs unchanged. The two are
// mutually exclusive — never both populated.
func layerKeyForColdBoot(req WakeRequest) string {
	if len(req.Sidecars) > 0 {
		return ""
	}
	return req.LayerKey
}
