// Package vmmdgrpc turns pkg/fcvm.Manager into the gRPC service defined in
// api/proto/onebox/faas/vmmd/v1. Handlers stay thin — each one wraps a single
// Manager call and translates its result into the proto envelope. The wire
// shape is fixed by ADR-013/014/016; this file does not invent fields.
//
// Every handler ≤ 50 lines per spec §Conventions line 472. Anything bigger
// gets extracted to proto.go (type adapters) or stats.go (Stats workload).

package vmmdgrpc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/fcvm/activity"
	"github.com/onebox-faas/faas/pkg/fcvm/cpustats"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
	"github.com/onebox-faas/faas/pkg/fcvm/netstats"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// VmmdAPI is the slice of pkg/fcvm.Manager that the handlers need. Defined
// here (not imported) so the unit tests can pass a fake without depending
// on the firecracker side effects.
type VmmdAPI interface {
	Wake(ctx context.Context, req fcvm.WakeRequest) (*fcvm.Instance, error)
	Park(ctx context.Context, instance string, spec fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error)
	Destroy(ctx context.Context, instance string) error
	DestroyWithExport(ctx context.Context, instance, exportDir string) (int, error)
	LiveCount() int
	LeasedCount() int
	// NetnsFor is the issue #98 / ADR-028 + ADR-047 bridge: the
	// ForwardHTTPStream handler needs the per-instance netns name
	// (fc-<instance>) to nsenter before dialing
	// netns.GuestIP:netns.AppPort. Defined here (not in
	// pkg/vmmdgrpc/forward.go) so the Server struct's interface
	// check catches a Manager wiring gap at compile time.
	NetnsFor(instance string) (string, bool)
	// UpdateEgressAllowlist (ADR-031 + ADR-033, tier-2 PR-B) walks
	// the live-instance map and applies the new per-app egress
	// allowlist in-place via incremental nft patch. Idempotent: an
	// empty allowlist flips the chain policy back to accept; an
	// allowlist equal to the cached prior one is a no-op. schedd
	// invokes this from the egress-drift subscriber
	// (pkg/sched/egress_drift.go) on every pg_notify app_changed
	// payload with kind="updated".
	UpdateEgressAllowlist(ctx context.Context, appID string, allowlist []netip.Prefix) error
	// InstancePID (M8 §11 — seccomp assertion) returns the host PID
	// of the running jailer child for instance, or (0, false) if
	// the instance is not currently alive. The SeccompStatus gRPC
	// handler reads /proc/<pid>/status to verify the jailer default
	// seccomp filter is in place. Defined on VmmdAPI (not on
	// pkg/fcvm.Manager) so the handler reads through the narrow
	// seam the Manager already exposes — no new Manager method.
	InstancePID(instance string) (int, bool)
	// LogRing (issue #254 / Move 4) returns the per-instance ring
	// buffer of stdout/stderr lines the ringWriter in pkg/fcvm/vmm.go
	// is filling from firecracker's own stdout. nil when the instance
	// is not alive on this vmmd (the Logs handler maps nil to NotFound).
	LogRing(instance string) *logbuf.Ring
	// MountParentExt4 (ADR-053) loopback-mounts the parent base ext4
	// identified by `storageKey` read-only and returns the absolute
	// host path of the mountpoint. vmmd is the only root component
	// (spec §11); imaged (User=faas-imaged, NoNewPrivileges=yes)
	// cannot mount on its own. Returns ErrNotFound when the storage
	// key isn't published (imaged's parent must already be staged via
	// EnsureBases before any child re-stage fires this).
	MountParentExt4(ctx context.Context, storageKey string) (string, error)
	// MaterializeParentExt4 copies a parent ext4 tree into a shared staging
	// directory while the root-owned vmmd process can still see its mount.
	MaterializeParentExt4(ctx context.Context, storageKey, targetDir string) error
	// UmountParentExt4 (ADR-053) releases a mount MountParentExt4
	// previously returned. Idempotent on unknown mountpoints so
	// imaged's defer-after-error pattern is safe.
	UmountParentExt4(ctx context.Context, mountpoint string) error
	// MountOverlayParent (ADR-075 / DEPLOY-1) issues an overlayfs
	// mount on behalf of imaged. lowerdir is a path under
	// /srv/fc/parent/ that MountParentExt4 returned; upperdir,
	// workdir, and merged are paths under
	// /dev/shm/faas-base-staging/. The mount satisfies the
	// kernel's overlayfs tmpfile contract (host /tmp is ext4
	// and cannot host the workdir's atomic rename cycle). This
	// RPC replaces the unix.Mount(2) syscall that used to live
	// in pkg/imaged under AmbientCapabilities=cap_sys_admin —
	// the architectural violation DEPLOY-1 erases.
	MountOverlayParent(ctx context.Context, lowerdir, upperdir, workdir, merged string) error
	// UmountOverlayParent (ADR-075 / DEPLOY-1) releases an
	// overlay mount MountOverlayParent previously returned.
	// Idempotent on unknown mountpoints so imaged's
	// defer-after-error pattern is safe.
	UmountOverlayParent(ctx context.Context, merged string) error
	// MarkInstanceFrameworkReady (issue #470 / PR #470-FU-B)
	// stamps the per-instance `framework_ready_at` clock on the
	// live Instance, observes the
	// vmmd_guest_framework_warmup_seconds histogram (if wired),
	// and returns the values the gRPC handler needs to publish
	// back to schedd (instance id + app id + runtime). Returns
	// (false, "", "", nil) when the instance is unknown — the wire
	// RPC translates that to a NotFound gRPC code so a stale
	// DGRAM receipt from a guest that's already gone is a clean,
	// observable error rather than a silent success.
	MarkInstanceFrameworkReady(ctx context.Context, instance string, warmupMs int64) (stamped bool, appID, runtime string, err error)
	// WarmSnapshot (issue #470 / PR #470-FU-A) is the warm-tier
	// capture entry point. See Manager.WarmSnapshot for the
	// full contract. The handler is intentionally narrow — it
	// just lifts the wire fields into a SnapshotSpec and
	// translates the Manager error into an Internal gRPC code
	// (the engine's captureWarmSnapshotLocked decides whether
	// to Destroy the VM on failure).
	WarmSnapshot(ctx context.Context, instance string, spec fcvm.SnapshotSpec) (fcvm.SnapshotInfo, error)
	// SignalAndKill (M-2 / ADR-138 §Decision 1) is the
	// graceful signal-then-grace-then-SIGKILL stop sequence.
	// signal is the POSIX signal number to send (0 = use
	// manifest StopSignal, defaulting to SIGTERM). grace is
	// the upper bound on clean-shutdown wait. Returns
	// (killSignalSent, exitCode, err); killSignalSent=true
	// means the grace window expired and vmmd escalated to
	// SIGKILL. The gRPC handler lifts these into the
	// StopInstanceResponse envelope. Unknown instances
	// return (false, 0, nil) so the gRPC surface is
	// idempotent — same shape as Destroy.
	SignalAndKill(ctx context.Context, instance string, signal int32, graceSeconds int32) (killSignalSent bool, exitCode int32, err error)
}

// JobVMMAPI is the optional job-task slice of fcvm.Manager. It is deliberately
// separate from VmmdAPI so existing vmmd test fakes and non-job integrations
// do not need to grow job-specific methods.
type JobVMMAPI interface {
	BootJob(context.Context, fcvm.JobBootRequest) (*fcvm.Instance, error)
	WaitJobExit(context.Context, string, time.Duration) (fcvm.JobExitPayload, error)
}

// flowCounter is the compute-side conntrack seam. Keeping it local to the
// gRPC package avoids widening VmmdAPI (and every test fake) while allowing
// production to inject flowcount.Reader and tests to inject a tiny fake.
type flowCounter interface {
	Warm(context.Context, []state.Instance) error
	Open(context.Context, string) (int64, error)
}

// Server implements vmmdpb.VmmdServer.
type Server struct {
	vmmdpb.UnimplementedVmmdServer

	vmm   VmmdAPI
	ops   *wire.OpsMetrics
	fcVer string
	log   *slog.Logger
	// events (issue #517 / PR-C / ADR-064) is the wake-timeline
	// fan-out. vmmd is the corroborating-observation source for
	// wake.boot_started (mirror at the gRPC server boundary) and
	// the canonical emit site for wake.readiness_200 (the first
	// 2xx probe). nil opts out (pre-PR-C fixtures).
	events *events.Platform
	// cpuCache holds the per-instance rate + accumulator used by
	// Stats() to populate cpu_pct and cpu_seconds on the wire
	// (issue #279, PR-B). The cache is fed by a small sample loop
	// in cmd/vmmd; nil-safe so unit tests that don't care about
	// CPU can pass nil to NewWithCPU.
	cpuCache *cpustats.Cache
	// netCache holds the per-instance byte counter cache used by
	// Stats() to populate net_tx_bytes on the wire (ADR-046,
	// step 7). The cache is fed by cmd/vmmd/network_poller.go
	// on a 250 ms tick; nil-safe so tests that don't care about
	// egress can pass nil to NewWithCPUAndNet. Wire unit is
	// interface bytes (includes Ethernet framing) — the same
	// kernel counter the per-plan tc tbf qdisc reads.
	netCache *netstats.Cache
	// activity (PR-B, issue #462) holds the per-instance
	// in-flight ForwardHTTP request counter used by Stats()
	// to populate inflight_requests and last_request_at on
	// the wire. The cache is fed by ForwardHTTP's Begin/End
	// defer pair (forward.go) and consumed here in
	// buildInstanceStatsRow. nil-safe so unit tests that
	// don't assert inflight can pass nil to the lower
	// constructors; production cmd/vmmd uses
	// NewWithCPUAndNetAndActivity.
	activity *activity.ActivityTracker
	// flowCounter samples conntrack on the compute host. Stats includes the
	// resulting count in the existing persistent capacity telemetry stream so
	// schedd's reaper does not query a remote node or misclassify a live flow.
	flowCounter flowCounter
	// migrations (Tier A5 / ADR-066) tracks in-flight
	// Phase 1 → Phase 5 leases. The dying vmmd mints a
	// lease at PrepareLiveMigration; the new owner vmmd
	// validates it at AdoptMigratedInstance; the dying
	// vmmd clears it on AcknowledgeMigration /
	// CancelLiveMigration. nil-safe — handlers treat a
	// nil tracker as "not wired" and return a typed
	// codes.Unavailable, except for the idempotent
	// Phase 4 / 5 ack paths which succeed.
	migrations *migrationTracker
	// migrationStore is the durable instance-row view used by the
	// destination Adopt handler and by lease expiry reconciliation.
	// The source tracker is intentionally still authoritative for
	// Ack/Cancel: after a successful commit the database row no
	// longer has state='migrating', but the source still owns the
	// paused VM until Ack destroys it.
	migrationStore state.Store
	// nodeID is this vmmd's registered compute-node identity. Lease
	// expiry uses it to distinguish a source VM that still belongs
	// here from a row already committed to a peer; without that
	// distinction it could either strand a paused source or resume
	// a VM after ownership moved away.
	nodeID string
	// streamBridges owns the reusable v2 bridge process and H2C transport for
	// each live instance. It is lazy so tests and legacy/v1 traffic pay no
	// startup cost until the optimized path is actually used.
	streamBridges *streamBridgeManager
}

// New wires the server. ops may be nil (noop metrics), log may be nil
// (slog default).
func New(vmm VmmdAPI, ops *wire.OpsMetrics, fcVer string, log *slog.Logger) *Server {
	return NewWithCPUAndNet(vmm, ops, fcVer, log, nil, nil)
}

// NewWithCPU is New plus an explicit CPU cache handle. Pass nil for
// the cache to keep the legacy behaviour (cpu_pct / cpu_seconds
// always absent on the wire); production cmd/vmmd passes
// cpustats.NewWithDefaults(). Net cache stays nil — callers that
// need it use NewWithCPUAndNet directly.
func NewWithCPU(vmm VmmdAPI, ops *wire.OpsMetrics, fcVer string, log *slog.Logger, cpu *cpustats.Cache) *Server {
	return NewWithCPUAndNet(vmm, ops, fcVer, log, cpu, nil)
}

// NewWithCPUAndNet wires both caches. ADR-046 (step 7) — net
// must be passed by cmd/vmmd's wiring (netstats.NewWithDefaults()
// from cmd/vmmd/main.go); nil is the safe default for unit tests
// that don't assert egress bytes.
func NewWithCPUAndNet(vmm VmmdAPI, ops *wire.OpsMetrics, fcVer string, log *slog.Logger, cpu *cpustats.Cache, net *netstats.Cache) *Server {
	return NewWithCPUAndNetAndActivity(vmm, ops, fcVer, log, cpu, net, nil)
}

// NewWithCPUAndNetAndActivity (PR-B, issue #462) wires all
// three caches. act must be passed by cmd/vmmd's wiring
// (activity.NewWithDefaults()); nil is the safe default for
// unit tests that don't assert inflight — Stats leaves
// InflightRequests at the zero default and LastRequestAt
// nil when act is nil, which matches the pre-PR-B wire
// shape and the schedd poller's additive-merge assumption.
//
// TODO (follow-up): collapse New/NewWithCPU/NewWithCPUAndNet/
// NewWithCPUAndNetAndActivity into a single Options-struct
// constructor. Tracked but not worth the call-site churn in
// PR-B.
func NewWithCPUAndNetAndActivity(vmm VmmdAPI, ops *wire.OpsMetrics, fcVer string, log *slog.Logger, cpu *cpustats.Cache, net *netstats.Cache, act *activity.ActivityTracker) *Server {
	if log == nil {
		log = slog.Default()
	}
	if ops == nil {
		// Use a fresh registry with a no-op prefix; observe still records but
		// never exported. Tests that don't assert metrics use this path.
		ops = wire.NewOpsMetrics("vmmd_test")
	}
	return &Server{vmm: vmm, ops: ops, fcVer: fcVer, log: log, cpuCache: cpu, netCache: net, activity: act, migrations: newMigrationTracker(), streamBridges: newStreamBridgeManager(log)}
}

// WithEvents (issue #517 / PR-C / ADR-064) wires the wake-timeline
// fan-out (pkg/events.Platform) on the gRPC server. vmmd is the
// corroborating-observation source for wake.boot_started (mirror
// at the gRPC server boundary) and the canonical emit site for
// wake.readiness_200 (the first 2xx probe). Returns the receiver
// to match the fluent setter pattern; nil opts out (pre-PR-C
// fixtures).
func (s *Server) WithEvents(p *events.Platform) *Server {
	s.events = p
	return s
}

// WithMigrationStore wires the durable instance-row view required by
// cross-node migration adoption and lease-expiry cleanup. It is optional so
// legacy/unit-test server fixtures can continue to exercise token tracking
// without a database.
func (s *Server) WithMigrationStore(store state.Store) *Server {
	if s != nil {
		s.migrationStore = store
	}
	return s
}

// WithNodeID wires the local compute-node identity used by migration
// lease-expiry reconciliation. It is optional for legacy single-box and
// unit-test fixtures that do not have a compute_nodes row.
func (s *Server) WithNodeID(nodeID string) *Server {
	if s != nil {
		s.nodeID = strings.TrimSpace(nodeID)
	}
	return s
}

// MigrationLeaser exposes the server's migration tracker through the common
// lease interface. The returned adapter shares the exact tracker used by the
// Phase 1/3/4/5 handlers, so callers cannot accidentally create a second
// lease namespace for this vmmd.
func (s *Server) MigrationLeaser() sched.Leaser[any] {
	if s == nil || s.migrations == nil {
		return nil
	}
	return NewMigrationLeaser(s.migrations)
}

// WithFlowCounter wires the compute-side conntrack reader used by Stats.
// Keeping this fluent and optional preserves non-Linux/unit-test behavior.
func (s *Server) WithFlowCounter(counter flowCounter) *Server {
	if s != nil {
		s.flowCounter = counter
	}
	return s
}

// emitBootStartedMirror (issue #517 / PR-C / ADR-064) is the
// vmmd-side mirror of wake.boot_started. Schedd is the canonical
// source (the engine emits the row at the Phase 3 entry); vmmd's
// mirror is a corroborating observation that the boot RPC
// actually entered the FC bring-up path on this vmmd instance.
// The wake_id is recovered from the wire envelope (PR-A), which
// schedd stamped on the bootCtx before dialing vmmd. nil events
// opts out (pre-PR-C fixtures).
func (s *Server) emitBootStartedMirror(ctx context.Context, instanceID, method string) {
	if s.events == nil {
		return
	}
	var wakeID, appID, trigger string
	var queued, conc int
	if fields, ok := wire.FromContext(ctx); ok {
		wakeID = fields.WakeID
		appID = fields.AppID
		// ADR-123 — schedd propagates the wake-boot telemetry
		// envelope so the mirror carries the same trigger /
		// queue / concurrency context as the canonical schedd
		// emit. Pre-ADR-123 schedd peers leave these empty.
		trigger = fields.Trigger
		queued = fields.QueuedCount
		conc = fields.ConcurrencyAtAdmit
	}
	s.events.Emit(ctx, events.BootStarted{
		EmitAt:             time.Now().UTC(),
		WakeID:             wakeID,
		AppID:              appID,
		InstanceID:         instanceID,
		Method:             method,
		RequestedAt:        time.Now().UTC(), // best-effort stamp (vmmd doesn't have schedd's startedAt)
		Trigger:            trigger,
		QueuedCount:        queued,
		ConcurrencyAtAdmit: conc,
	})
}

// ForgetCPU drops the cache baseline for an instance. Called from
// the Destroy path so the cache does not grow unbounded across the
// vmmd process lifetime. Safe on a nil receiver / nil cache.
func (s *Server) ForgetCPU(instance string) {
	if s == nil || s.cpuCache == nil {
		return
	}
	s.cpuCache.Forget(instance)
}

// ForgetNet drops the netstats cache baseline for an instance.
// ADR-046 (step 7): parallel to ForgetCPU. Called from the Destroy
// path so the cache map does not grow unbounded across the vmmd
// process lifetime. Safe on a nil receiver / nil cache.
func (s *Server) ForgetNet(instance string) {
	if s == nil || s.netCache == nil {
		return
	}
	s.netCache.Forget(instance)
}

// ForgetActivity drops the activity tracker entry for an
// instance. PR-B (issue #462): parallel to ForgetCPU/ForgetNet.
// Called from the Destroy path so the activity map does not
// grow unbounded across the vmmd process lifetime. A leaked
// Begin-without-End on a destroyed instance is recovered here,
// not on the End call (which is the last-resort cleanup
// documented in pkg/fcvm/activity/doc.go).
func (s *Server) ForgetActivity(instance string) {
	if s == nil || s.activity == nil {
		return
	}
	s.activity.Forget(instance)
}

// incWakeFailure (issue #1059 / ADR-127) bumps the vmmd
// wake-failure counter on the gRPC parse-failure paths only.
// Manager.Wake already increments the counter at every internal
// failure site (see pkg/fcvm/manager.go hook sites: netns_fail,
// cgroup_fail, restore-fallback, cold-boot terminal); the gRPC
// side only adds increments for envelope-validation failures
// that never reach Manager. This avoids double-counting on the
// Manager-failure path (the wire surface would otherwise emit
// for every internal hook site's reason, fragmenting the series
// across (gRPC, Manager) origins).
//
// box is hardcoded to "local" per ADR §3.4 — single-control-plane
// placeholder, replaced by compute_nodes.id in the Tier A
// multi-host rollout.
//
// reason is the closed-vocabulary reason string from
// pkg/wire/metrics.go's wakeFailureReasons set; the call sites
// pass hardcoded literals per the ADR §3 "call site hardcodes
// the literal" rule, never a bare-string derived from the
// wrapped error.
//
// The app value passed to WakeFailure is the empty string here:
// the gRPC proto (CreateFromSnapshotRequest / CreateColdBootRequest)
// does NOT carry an app_id at the wire level — only an AppSpec
// (the runtime shape: base_key, layer_key, vcpu_count, mem_size_mib).
// The app identifier lives on the WakeRequest the schedd-side
// derives from the gRPC envelope; that's where the per-app
// WakeFailure increment lands (pkg/fcvm/manager.go — the 6
// wake-failure hook sites threaded with req.AppID in commit 4
// of the platform-observability mega-PR). The gRPC parse-failure
// path therefore collapses to labelAppUnknown ("") via the
// appLabelSet admission, which is observable distinctly from a
// real app slug that hit the admission cap (otherAppLabel).
func (s *Server) incWakeFailure(_ context.Context, reason string) {
	if s == nil || s.ops == nil {
		return
	}
	s.ops.WakeFailure("", "", reason).Inc()
}

// Register binds s to a gRPC server.
func (s *Server) Register(g *grpc.Server) {
	vmmdpb.RegisterVmmdServer(g, s)
}

// Close releases reusable bridge processes and their unix-socket transports.
// vmmd calls this during graceful shutdown; keeping it explicit also makes
// the child lifecycle testable without relying on parent-death signals.
func (s *Server) Close(ctx context.Context) error {
	if s == nil || s.streamBridges == nil {
		return nil
	}
	return s.streamBridges.close(ctx)
}

// CreateFromSnapshot wires the snapshot-restore path. Falls back to cold
// boot inside Manager.Wake — the response's `method` reports what
// actually happened. ADR-005 is enforced one layer down.
func (s *Server) CreateFromSnapshot(ctx context.Context, req *vmmdpb.CreateFromSnapshotRequest) (*vmmdpb.WakeResponse, error) {
	const op = "CreateFromSnapshot"
	ctx = withIncomingCorrelation(ctx)
	start := time.Now()
	wr, err := toWakeRequest(ctx, req)
	if err != nil {
		s.ops.Observe(op, time.Since(start), err)
		// Issue #1059 / ADR-127: emit the wake-failure counter on
		// the parse-failure path ONLY (the Manager.Wake path
		// below already increments via its hook sites — adding
		// a second increment here would double-count). The
		// parse-failure path is the wired call from schedd that
		// fails envelope validation before vmmd ever sees the
		// instance; reason="snapshot_restore_err" is the ADR §3
		// hardcoded literal for the gRPC surface because the
		// envelope shape mirrors the snapshot-restore request
		// (the cold-boot endpoint is a sibling).
		s.incWakeFailure(ctx, "snapshot_restore_err")
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	// issue #517 / PR-C / ADR-064 — mirror wake.boot_started at
	// the gRPC server boundary. Schedd is the canonical emit site;
	// this vmmd-side mirror is a corroborating observation that
	// the boot RPC actually entered the FC bring-up path. Both
	// rows share the wake_id from the wire envelope (PR-A) so
	// the customer-facing timeline endpoint can join them.
	s.emitBootStartedMirror(ctx, req.GetInstance(), "restore")
	inst, err := s.vmm.Wake(ctx, wr)
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	if inst.Method == fcvm.WakeColdBoot && inst.RestoreError != "" {
		s.log.Warn("vmmd: restore fell back to cold boot", "instance", req.GetInstance(), "err", inst.RestoreError)
	}
	return wakeResponseFromInstance(req.GetInstance(), wr, inst, vmmdpb.WakeMethod_WAKE_RESTORE), nil
}

// CreateColdBoot primes an instance for the deploy-pipeline first-boot
// path (no snapshot).
func (s *Server) CreateColdBoot(ctx context.Context, req *vmmdpb.CreateColdBootRequest) (*vmmdpb.WakeResponse, error) {
	const op = "CreateColdBoot"
	ctx = withIncomingCorrelation(ctx)
	start := time.Now()
	wr, err := toColdBootRequest(ctx, req)
	if err != nil {
		s.ops.Observe(op, time.Since(start), err)
		// Issue #1059 / ADR-127: parse-failure path on the
		// cold-boot endpoint. Hardcoded reason="mem_backend_err"
		// per ADR §3 — the cold-boot envelope carries the
		// mem-file locator so a parse failure is functionally
		// "I couldn't read the mem backend descriptor" even
		// though today the only backend_type is "File". When
		// hugetlbfs lands (Issue future), this counter
		// already exists for the new failure mode without
		// requiring a label-value migration.
		s.incWakeFailure(ctx, "mem_backend_err")
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	// issue #517 / PR-C / ADR-064 — mirror wake.boot_started at
	// the gRPC server boundary. Same canonical-site pairing as
	// CreateFromSnapshot: schedd is the source of truth, vmmd's
	// mirror is a corroborating observation.
	s.emitBootStartedMirror(ctx, req.GetInstance(), "cold_boot")
	inst, err := s.vmm.Wake(ctx, wr)
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		s.log.Error("vmmd: cold boot failed", "instance", req.GetInstance(), "err", err.Error())
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return wakeResponseFromInstance(req.GetInstance(), wr, inst, vmmdpb.WakeMethod_WAKE_COLD_BOOT), nil
}

// JobColdBoot starts a job-task VM through the optional Manager job surface.
func (s *Server) JobColdBoot(ctx context.Context, req *vmmdpb.JobColdBootRequest) (*vmmdpb.JobColdBootResponse, error) {
	const op = "JobColdBoot"
	start := time.Now()
	jobVMM, ok := s.vmm.(JobVMMAPI)
	if !ok {
		err := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"Job execution unavailable", "vmmd job execution is not configured")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	boot, err := jobBootRequestFromProto(req)
	if err != nil {
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	inst, err := jobVMM.BootJob(ctx, boot)
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	if inst == nil {
		err := api.NewProblem(int(codes.Internal), api.CodeInternal,
			"Job boot failed", "vmmd returned an empty instance")
		return nil, grpcerr.ToStatus(err)
	}
	return &vmmdpb.JobColdBootResponse{
		Instance: inst.Lease.Instance,
		NodeId:   boot.NodeID,
	}, nil
}

// WaitJobExit waits for the guest supervisor's terminal job receipt. The
// scheduler's context carries the task-specific deadline; the fallback keeps
// direct callers from accidentally creating an unbounded wait.
func (s *Server) WaitJobExit(ctx context.Context, req *vmmdpb.WaitJobExitRequest) (*vmmdpb.JobExitResponse, error) {
	const op = "WaitJobExit"
	start := time.Now()
	jobVMM, ok := s.vmm.(JobVMMAPI)
	if !ok {
		err := api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"Job execution unavailable", "vmmd job execution is not configured")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	if req.GetInstance() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Invalid job exit request", "instance is required")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	deadline := fcvm.JobDestroyWaitDefault
	if until, ok := ctx.Deadline(); ok {
		deadline = time.Until(until)
		if deadline <= 0 {
			err := api.NewProblem(int(codes.DeadlineExceeded), api.CodeInternal,
				"Job exit wait timed out", "job exit deadline elapsed")
			s.ops.Observe(op, time.Since(start), err)
			return nil, grpcerr.ToStatus(err)
		}
	}
	payload, err := jobVMM.WaitJobExit(ctx, req.GetInstance(), deadline)
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &vmmdpb.JobExitResponse{
		ExitCode:           payload.ExitCode,
		ErrorClass:         payload.ErrorClass,
		Signal:             payload.Signal,
		FinishedAtUnixNano: payload.FinishedAtUnixNano,
		LeaseToken:         payload.LeaseToken,
	}, nil
}

// PauseAndSnapshot parks an instance, writing its full snapshot. Destroy
// happens inside Manager.Park.
//
// #96 / ADR-025 axis 2: mem + vmstate are both routed through the
// configured StorageBackend. vmmd allocates staging tmps internally
// (SnapshotSpec.StageMemPath), the mem blob is published at the
// canonical StorageKey, and the vmstate blob is published at the
// canonical VMStateStorageKey when one is supplied. Either vmstate
// locator (legacy VMStatePath or new VMStateStorageKey) is acceptable
// to keep default-local single-box behaviour bit-for-bit: the engine
// (pkg/sched/engine.go) sends the empty key for default-local so the
// legacy host-path branch is taken. The two locators are NOT mutually
// exclusive — a remote caller may populate both and the storage key
// is authoritative; VmstatePath is logged-only metadata when the
// storage key is non-empty.
func (s *Server) PauseAndSnapshot(ctx context.Context, req *vmmdpb.PauseAndSnapshotRequest) (*vmmdpb.SnapshotResponse, error) {
	const op = "PauseAndSnapshot"
	start := time.Now()
	// Mem storage_key stays required (F-1 contract, #96 slice 3). Vmstate
	// is acceptable via either vmstate_storage_key (new, ADR-025 axis 2
	// slice 4) or vmstate_path (legacy host path, single-box default).
	// Neither vmstate field set together is rejected so an operator who
	// forgets both gets a clear error naming both field names.
	if req.GetStorageKey() == "" || (req.GetVmstateStorageKey() == "" && req.GetVmstatePath() == "") {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing paths",
			"storage_key is required; at least one of vmstate_storage_key or vmstate_path must be set").
			WithDocs("https://" + wire.DocsHost + "/vmmd#pause")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	info, err := s.vmm.Park(ctx, req.GetInstance(), fcvm.SnapshotSpec{
		VMStatePath:       req.GetVmstatePath(),
		StorageKey:        req.GetStorageKey(),
		VMStateStorageKey: req.GetVmstateStorageKey(),
	})
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &vmmdpb.SnapshotResponse{
		MemBytes:     info.MemBytes,
		VmstateBytes: info.VMStateBytes,
	}, nil
}

// WarmSnapshot (issue #470 / PR #470-FU-A) is the warm-tier twin
// of PauseAndSnapshot. The handler validates the wire instance
// id + both storage keys (warm captures always publish through
// the configured StorageBackend — there is no legacy host-path
// fallback for the warm tier). Manager.WarmSnapshot does the
// pause + /snapshot/create + storage publish + VM resume in
// one atomic sequence; the engine's captureWarmSnapshotLocked
// (pkg/sched/engine.go) is the only entry point. On failure the
// error bubbles up as Internal — the engine's failure path
// fires m.Destroy + transitions the instance to STOPPED with
// reason "warm_snapshot_failed" (see ADR-055).
//
// The handler is a thin pass-through to Manager.WarmSnapshot.
// All side effects (CID/lease tracking, ring buffer, audit) live
// one layer down on the Manager.
func (s *Server) WarmSnapshot(ctx context.Context, req *vmmdpb.WarmSnapshotRequest) (*vmmdpb.SnapshotResponse, error) {
	const op = "WarmSnapshot"
	start := time.Now()
	if req.GetInstance() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing instance",
			"instance is required on WarmSnapshot").
			WithDocs("https://" + wire.DocsHost + "/vmmd#warm-snapshot")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	if req.GetStorageKey() == "" || req.GetVmstateStorageKey() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing storage keys",
			"storage_key and vmstate_storage_key are required on WarmSnapshot (warm captures are storage-backend-only)").
			WithDocs("https://" + wire.DocsHost + "/vmmd#warm-snapshot")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	info, err := s.vmm.WarmSnapshot(ctx, req.GetInstance(), fcvm.SnapshotSpec{
		StorageKey:        req.GetStorageKey(),
		VMStateStorageKey: req.GetVmstateStorageKey(),
	})
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &vmmdpb.SnapshotResponse{
		MemBytes:     info.MemBytes,
		VmstateBytes: info.VMStateBytes,
	}, nil
}

// FrameworkReady is the vmmd-side receipt of the guest-init "framework
// ready" vsock DGRAM (port 1027, msg=4) signal (issue #470, PR
// #470-FU-B). The handler:
//
//  1. Validates the wire instance id is non-empty.
//  2. Calls Manager.MarkInstanceFrameworkReady which stamps the
//     per-instance `framework_ready_at` clock on the live Instance
//     and observes the vmmd_guest_framework_warmup_seconds histogram.
//  3. Returns NotFound when the instance is unknown (the DGRAM
//     listener in cmd/vmmd may have stale packets in flight for an
//     instance that was already destroyed via a parallel wake-park
//     cycle — surfaces the situation observably instead of silently
//     dropping it).
//
// The handler is idempotent: re-stamping on every subsequent signal
// is the intended behavior (the engine's captureWarmSnapshot
// explicitly resets the column to NULL at the start of each
// warm-capture cycle so a stale stamp can't leak across cycles).
// Back-pressure is intentionally absent — DGRAM is fire-and-forget
// by analogy with the stateless-advisory port 1025 path; a missed
// DGRAM means the engine's warm-capture wait times out and falls
// through to init-tier (correctness-preserving).
func (s *Server) FrameworkReady(ctx context.Context, req *vmmdpb.FrameworkReadyRequest) (*vmmdpb.FrameworkReadyResponse, error) {
	const op = "FrameworkReady"
	start := time.Now()
	if req.GetInstance() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing instance",
			"instance is required on FrameworkReady").
			WithDocs("https://" + wire.DocsHost + "/vmmd#framework_ready")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	stamped, appID, runtime, err := s.vmm.MarkInstanceFrameworkReady(ctx, req.GetInstance(), req.GetWarmupMs())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	if !stamped {
		err := api.NewProblem(int(codes.NotFound), api.CodeNotFound,
			"Instance not live",
			"framework_ready receipt for an instance that is not live on this vmmd").
			WithDocs("https://" + wire.DocsHost + "/vmmd#framework_ready")
		return nil, grpcerr.ToStatus(err)
	}
	// Issue #470 / PR C / ADR-074: observe the wall-clock guest-init
	// duration. The warmup ms surfaced by guest-init is the authoritative
	// in-guest timer (boot clock from VM start to framework_ready send);
	// the server-side elapsed is the additional round-trip from DGRAM recv
	// to stamp commit. We observe warmup_ms because it's the
	// operator-facing measurement ("how long did my app take to reach
	// ready"). Bucket set is spec §6.3 verbatim. s.ops is the canonical
	// OpsMetrics handle; nil-safe via the accessor.
	if s.ops != nil {
		s.ops.GuestInitDuration(appID, runtime).Observe(float64(req.GetWarmupMs()) / 1000.0)
	}
	// Surface appID + runtime into the structured log so the
	// Prometheus exemplar downstream picks them up. We don't
	// currently ship them on the wire (the response is empty by
	// design — the engine reads the SQL column directly) but the
	// log line is the audit trail for "which vmmd saw which signal
	// for which app".
	s.log.Info("framework_ready stamped",
		"instance", req.GetInstance(),
		"app_id", appID,
		"runtime", runtime,
		"warmup_ms", req.GetWarmupMs())
	return &vmmdpb.FrameworkReadyResponse{}, nil
}

// Destroy tears down an instance. Idempotent for unknown instances. The
// optional ExportDir (passed via CreateColdBoot.BuildSpec and remembered by
// vmmd) triggers a build-aware teardown: vmmd waits for the in-VM build to
// exit, captures the exit code, and copies /build/out/* + build-done.json
// into ExportDir before releasing the chroot. The response carries the exit
// code on the wire so builderd can classify (FailureUserError / OOM / Timeout).
//
// The CPU cache baseline is dropped on every successful destroy (and on
// not-found, which is idempotent) so the cache does not grow
// unbounded across the vmmd process lifetime. issue #279.
func (s *Server) Destroy(ctx context.Context, req *vmmdpb.DestroyRequest) (*vmmdpb.DestroyResponse, error) {
	const op = "Destroy"
	start := time.Now()
	exportDir := s.exportDirFor(req.GetInstance())
	code, err := s.vmm.DestroyWithExport(ctx, req.GetInstance(), exportDir)
	if err != nil {
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	s.ForgetCPU(req.GetInstance())
	s.ForgetNet(req.GetInstance())
	s.ForgetActivity(req.GetInstance())
	s.ops.Observe(op, time.Since(start), nil)
	return &vmmdpb.DestroyResponse{Instance: req.GetInstance(), ExitCode: int32(code)}, nil
}

// StopInstance (M-2 / ADR-138 §Decision 1) is the graceful
// signal-then-grace-then-SIGKILL stop sequence. Distinct from
// Destroy (a hard SIGKILL). Engine.StopInstance (commit 6)
// dispatches per Instance.ExecutionMode: worker/job → StopInstance
// with the manifest's StopSignal + StopGracePeriod; request/service
// → existing snapshotAndPark/Destroy path.
//
// signal is the POSIX signal number to send (0 = use manifest
// StopSignal, defaulting to SIGTERM). graceSeconds is the upper
// bound on clean-shutdown wait (0 = immediate SIGKILL, the legacy
// Destroy shape). The response carries the captured exit code and
// killSignalSent=true iff the grace window expired and vmmd had to
// escalate. The CPU cache baseline is dropped on every successful
// stop (and on not-found, idempotent) so the cache does not grow
// unbounded — same shape as Destroy.
func (s *Server) StopInstance(ctx context.Context, req *vmmdpb.StopInstanceRequest) (*vmmdpb.StopInstanceResponse, error) {
	const op = "StopInstance"
	start := time.Now()
	killSignalSent, exitCode, err := s.vmm.SignalAndKill(ctx, req.GetInstance(), req.GetSignal(), req.GetGracePeriodS())
	if err != nil {
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	s.ForgetCPU(req.GetInstance())
	s.ForgetNet(req.GetInstance())
	s.ForgetActivity(req.GetInstance())
	s.ops.Observe(op, time.Since(start), nil)
	return &vmmdpb.StopInstanceResponse{
		Instance:       req.GetInstance(),
		ExitCode:       exitCode,
		KillSignalSent: killSignalSent,
	}, nil
}

// exportDirFor asks the Manager whether the instance was registered as a
// builder VM at cold-boot. App VMs return "" (so the gRPC Destroy stays
// backwards-compatible — same teardown behaviour as before M6).
func (s *Server) exportDirFor(instance string) string {
	if getter, ok := s.vmm.(interface {
		ExportDirFor(string) string
	}); ok {
		return getter.ExportDirFor(instance)
	}
	return ""
}

// Heartbeat answers the presence ping from schedd's heartbeat
// goroutine (issue #98 / ADR-028). The direction is reversed from
// the vmmd-pushes design because schedd is the admission authority
// and shouldn't trust inbound traffic from a box it may have already
// drained — the heartbeat lives on the schedd side and just proves
// "this vmmd is reachable over the overlay right now". Returning a
// non-Unavailable gRPC code is what schedd counts as liveness.
func (s *Server) Heartbeat(ctx context.Context, _ *vmmdpb.HeartbeatRequest) (*vmmdpb.HeartbeatResponse, error) {
	const op = "Heartbeat"
	start := time.Now()
	s.ops.Observe(op, time.Since(start), nil)
	return &vmmdpb.HeartbeatResponse{}, nil
}

// Stats returns Manager's view: live/leased counts and per-instance
// resident bytes sourced from cgroup memory.current.
//
// # CPU fields
//
// cpu_pct and cpu_seconds are populated from the cpustats cache
// (issue #279 / PR-B). The cache is fed by a 250 ms sample loop in
// cmd/vmmd that reads cgroupstats.Reader.Sample per instance; the
// Stats handler just looks up the cached rate. A nil cache (tests
// that don't wire the cache) returns the legacy behaviour:
// both fields absent on the wire.
//
// A baseline not yet established for an instance also leaves the
// fields absent — schedd stamps Unknown and the {app,node} rollup
// excludes the row, exactly as the cgroupstats docs require.
func (s *Server) Stats(ctx context.Context, _ *vmmdpb.StatsRequest) (*vmmdpb.StatsResponse, error) {
	const op = "Stats"
	start := time.Now()
	defer func() { s.ops.Observe(op, time.Since(start), nil) }()

	resp := &vmmdpb.StatsResponse{
		LiveCount:   int32(s.vmm.LiveCount()),
		LeasedCount: int32(s.vmm.LeasedCount()),
	}

	resident, ok := leakcheck.ResidentBytes()
	if !ok {
		// Non-Linux host (dev). Unset rather than zero — DistinctValue
		// semantics let dashboards distinguish "no data" from "0".
		resp.TotalResidentBytes = nil
		return resp, nil
	}

	var total int64
	openConns := s.openConns(ctx)
	resp.Instances = make([]*vmmdpb.InstanceStats, 0, len(resident))
	for inst, b := range resident {
		total += b
		row := buildInstanceStatsRow(inst, b, s.cpuCache, s.netCache, s.activity, s.ops)
		if provider, ok := s.vmm.(interface {
			DiskUsage(string) (fcvm.DiskUsage, bool)
		}); ok {
			if usage, present := provider.DiskUsage(inst); present {
				row.DiskUsedBytes = wrapperspb.Int64(usage.UsedBytes)
				row.DiskCapacityBytes = wrapperspb.Int64(usage.CapacityBytes)
			}
		}
		row.OpenConns = openConns[inst]
		resp.Instances = append(resp.Instances, row)
	}
	resp.TotalResidentBytes = wrapperspb.Int64(total)
	return resp, nil
}

// openConns performs one bulk conntrack walk for the current live set and
// attributes the result to instance IDs. Errors are fail-open: a missing
// conntrack binary or transient kernel read must not make Stats unavailable.
func (s *Server) openConns(ctx context.Context) map[string]int64 {
	if s == nil || s.flowCounter == nil {
		return nil
	}
	provider, ok := s.vmm.(interface{ SnapshotLiveHostIPs() map[string]string })
	if !ok {
		return nil
	}
	hosts := provider.SnapshotLiveHostIPs()
	if len(hosts) == 0 {
		return map[string]int64{}
	}
	instances := make([]state.Instance, 0, len(hosts))
	for id, hostIP := range hosts {
		instances = append(instances, state.Instance{ID: id, HostIP: hostIP})
	}
	if err := s.flowCounter.Warm(ctx, instances); err != nil {
		if s.log != nil {
			s.log.Warn("vmmd: conntrack snapshot failed; open connections omitted", "err", err)
		}
		return nil
	}
	counts := make(map[string]int64, len(hosts))
	for id := range hosts {
		value, err := s.flowCounter.Open(ctx, id)
		if err != nil {
			if s.log != nil {
				s.log.Warn("vmmd: conntrack instance lookup failed", "instance", id, "err", err)
			}
			continue
		}
		counts[id] = value
	}
	return counts
}

// buildInstanceStatsRow assembles one wire row from the per-instance
// resident bytes plus the CPU + netstats cache lookups. Pulled out of
// the Stats handler so the per-row wire shape can be unit-tested on
// a non-Linux host (issue #279 / PR-B / ADR-046 — without this seam
// the schedd-side and downstream consumers cannot verify the byte
// counter actually reaches the wire).
//
// Wire semantics:
//
//   - ResidentBytes: always populated when leakcheck reports the
//     instance (the caller guarantees it).
//   - CpuPct / CpuSeconds / CpuThrottledSeconds: wrappers, absent
//     on baseline / cgroup regression — schedd stamps Unknown and
//     the {app,node} rollup excludes the row (cpustats regression
//     contract, ADR-039 §3.1).
//   - NetTxBytes (ADR-046, step 7): per-tick byte delta on root-side
//     vethHost.rx_bytes from vmmd's netstats cache. Same wrapper
//     semantics as cpu_pct (absent = baseline / veth recreation).
//     schedd sums across the 250 ms window into per-(instance,
//     minute) accumulator and exposes via pkg/scheddgrpc.
//     ListInstanceStats as a uint64 byte counter; meterd's
//     SampleAndRoll appends to usage_minutes.net_tx_bytes.
//
// The CPU `Lookup` and the netstats `Lookup` are O(1) under their
// own mutexes and never block on sysfs I/O — the sample loop in
// cmd/vmmd owns the disk reads. The per-row CPU collect duration is
// observed separately on `vmmd_stats_cpu_collect_seconds` so the
// hot path can be graphed in isolation from the rest of the Stats
// RPC.
func buildInstanceStatsRow(
	instance string,
	resident int64,
	cpuCache *cpustats.Cache,
	netCache *netstats.Cache,
	activityCache *activity.ActivityTracker,
	ops *wire.OpsMetrics,
) *vmmdpb.InstanceStats {
	row := &vmmdpb.InstanceStats{
		Instance:      instance,
		ResidentBytes: wrapperspb.Int64(resident),
	}
	if cpuCache != nil {
		cpuStart := time.Now()
		if reading, ok := cpuCache.Lookup(instance); ok && reading.Valid {
			row.CpuPct = wrapperspb.Double(reading.CPUPct)
			row.CpuSeconds = wrapperspb.Double(reading.CPUSeconds)
			// PR-D (issue #301, ADR-044): the
			// cumulative throttled-seconds reading.
			// Source: same Reading as cpu_seconds,
			// driven by the same regression contract
			// (absent on baseline / cgroup
			// recreation). The Prometheus rollup
			// lives in pkg/wire/topn_app.go
			// (topAppSet admission, cap=100) — the
			// wire shape stays per-instance and
			// the {account_id, app_id} cardinality
			// stays bounded.
			row.CpuThrottledSeconds = wrapperspb.Double(reading.ThrottledSeconds)
		}
		if h := ops.CPUStatsCollectDuration(); h != nil {
			h.Observe(time.Since(cpuStart).Seconds())
		}
	}
	if netCache != nil {
		if nReading, nok := netCache.Lookup(instance); nok && nReading.Valid {
			// Send cumulative counters. meterd samples far less often
			// than vmmd's network poller, so sending only the latest
			// 250 ms delta silently discarded almost all traffic.
			row.NetTxBytes = wrapperspb.Int64(int64(nReading.CumulativeBytes))
			row.NetRxBytes = wrapperspb.Int64(int64(nReading.IngressCumulativeBytes))
		}
	}
	// PR-B (issue #462): in-flight ForwardHTTP request
	// counter and last-observed moment. The wire field
	// InflightRequests is a bare int64 (no wrapper — see
	// api/proto/onebox/faas/vmmd/v1/vmmd.pb.go:1243);
	// zero is a valid "idle" reading the schedd poller
	// already handles. LastRequestAt is a pointer so nil
	// means "no request observed yet on this instance" —
	// the poller falls back to state.Instance.LastRequestAt
	// in that case (vmmd.pb.go:1245-1247).
	if activityCache != nil {
		if inflight, ok := activityCache.Inflight(instance); ok {
			row.InflightRequests = inflight
		}
		if lastAt, ok := activityCache.LastAt(instance); ok {
			row.LastRequestAt = timestamppb.New(lastAt)
		}
		if total, ok := activityCache.Total(instance); ok {
			// The activity tracker is the source of truth for the
			// topology-independent scale signal. Keep it optional so
			// older/non-production test constructors retain the
			// pre-tracker wire shape.
			row.RequestCountTotal = wrapperspb.Int64(int64(total))
		}
	}
	return row
}

// Ping is a wire-only liveness probe (issue #97 / ADR-025 axis 3,
// PR #114). Returns the vmmd process's Firecracker version + the
// server-side timestamp at the moment the handler ran. schedd's
// heartbeat loop calls this on every active compute_node every
// HeartbeatInterval (default 30s); a successful round-trip proves
// both gRPC socket reachability and that vmmd's goroutine
// scheduler is responsive enough to schedule this handler. Idempotent
// + side-effect free; no backing Manager call.
func (s *Server) Ping(_ context.Context, _ *vmmdpb.PingRequest) (*vmmdpb.PingResponse, error) {
	const op = "Ping"
	start := time.Now()
	defer func() { s.ops.Observe(op, time.Since(start), nil) }()
	return &vmmdpb.PingResponse{
		FcVersion:  s.fcVer,
		ServerTime: timestamppb.Now(),
	}, nil
}

// UpdateEgressAllowlist (ADR-031 + ADR-033, tier-2 PR-B) walks the
// vmmd's live-instance map and applies the new per-app egress
// allowlist in-place via incremental nft patch (Manager.UpdateEgressAllowlist).
// Empty allowlist = clear the rule and let chain-policy default
// accept do its work. Idempotent on a redelivered identical
// allowlist (no-op). Schedd invokes this from the egress-drift
// subscriber on every pg_notify app_changed payload with
// kind="updated"; failures bubble up as Unavailable so schedd
// logs + retries on its next reconcile.
func (s *Server) UpdateEgressAllowlist(ctx context.Context, req *vmmdpb.UpdateEgressAllowlistRequest) (*vmmdpb.UpdateEgressAllowlistAck, error) {
	const op = "UpdateEgressAllowlist"
	start := time.Now()
	defer func() { s.ops.Observe(op, time.Since(start), nil) }()
	if req.GetAppId() == "" {
		return nil, grpcerr.ToStatus(toProblem(api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Missing app_id", "app_id is required").
			WithDocs("https://" + wire.DocsHost + "/vmmd#update-egress-allowlist")))
	}
	allowlist, err := toEgressAllowlist(req.GetEgressAllowlist())
	if err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	if err := s.vmm.UpdateEgressAllowlist(ctx, req.GetAppId(), allowlist); err != nil {
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &vmmdpb.UpdateEgressAllowlistAck{}, nil
}

// SeccompStatus (M8 §11) reports the kernel seccomp state of the
// jailer child backing instance. Sequence:
//  1. Resolve the running jailer PID via VmmdAPI.InstancePID.
//     Unknown instance → NotFound (the e2e tripwire wants "no
//     instance" to be visible on the wire, not silently absorbed).
//  2. Read /proc/<pid>/status from the vmmd process (the test
//     reads again from the e2e process; both should agree because
//     they read the same kernel state — the tripwire is about the
//     value, not the reader).
//  3. Parse the `Seccomp:` and `Seccomp_filters:` lines (kernel
//     text format; the kernel writes them in this order). Map
//     0/1/2 to "disabled"/"strict"/"filter" (the kernel's own
//     mode names).
//
// Empty instance is InvalidArgument — distinguishes "operator
// forgot the field" from "operator named a dead instance" so the
// gRPC code surfaces the diagnosis. The handler does not call
// into Manager (seccomp is a per-process kernel attribute, not a
// Manager concern); the wire is the only contract.
func (s *Server) SeccompStatus(ctx context.Context, req *vmmdpb.SeccompStatusRequest) (*vmmdpb.SeccompStatusResponse, error) {
	const op = "SeccompStatus"
	start := time.Now()
	defer func() { s.ops.Observe(op, time.Since(start), nil) }()

	if req.GetInstance() == "" {
		return nil, grpcerr.ToStatus(api.NewProblem(int(codes.InvalidArgument),
			api.CodeValidation, "Missing instance", "instance is required").
			WithDocs("https://" + wire.DocsHost + "/vmmd#seccomp"))
	}
	pid, ok := s.vmm.InstancePID(req.GetInstance())
	if !ok {
		return nil, grpcerr.ToStatus(api.NewProblem(int(codes.NotFound),
			api.CodeNotFound, "Instance not alive", fmt.Sprintf("instance %q is not alive on this vmmd", req.GetInstance())).
			WithDocs("https://" + wire.DocsHost + "/vmmd#seccomp"))
	}

	mode, filterLen, err := readSeccompStatus(pid)
	if err != nil {
		// Don't fail the gRPC call with Internal — the e2e needs
		// the wire to be OK so the response carries the
		// diagnostic message. The presence of Error is the
		// failure signal the test asserts on.
		return &vmmdpb.SeccompStatusResponse{
			Instance: req.GetInstance(),
			Pid:      int32(pid),
			Mode:     "unknown",
			Error:    err.Error(),
		}, nil
	}
	return &vmmdpb.SeccompStatusResponse{
		Instance:  req.GetInstance(),
		Pid:       int32(pid),
		Mode:      mode,
		FilterLen: filterLen,
	}, nil
}

// MountParentExt4ReadOnly (ADR-053) is the staging-only path
// that lets imaged compose the per-runtime base ext4 from a
// shared debian:12-slim parent. The handler validates the
// storage key against the configured StorageBackend, stages
// the bytes into /srv/fc/parent/faas-parent-src-* (cmd/vmmd
// bootstraps the directory with mode 0750 root:faas so the
// imaged-side MkdirBaseStaging-style operations can read
// through the resulting loopback mount), loopback-mounts
// read-only under /srv/fc/parent/faas-parent-mnt-*, and
// registers the mountpoint in vmmdmount.Registry for sweep-on-
// SIGTERM + 30-minute orphan sweep.
//
// Validation chain:
//  1. Empty storage_key → InvalidArgument.
//  2. Non-parent storage_key → InvalidArgument (allow-list —
//     a misbehaving caller or a leaked token from another
//     `faas`-group member cannot read arbitrary storage bytes
//     through vmmd's loopback mount; the allow-list is the
//     host-arch + sibling-arch set from sched.IsParentBaseKey).
//  3. vmm.MountParentExt4 returns vmmdmount.ErrNotFound →
//     NotFound (the storage backend has no such key — the
//     load-bearing wire code for imaged's pre-staging checks).
//  4. Any other error → Internal.
//
// imaged's defer-after-error pattern relies on UmountParentExt4
// being idempotent so the parent is always released — see
// MountParentRegistry.
func (s *Server) MountParentExt4ReadOnly(ctx context.Context, req *vmmdpb.MountParentExt4ReadOnlyRequest) (*vmmdpb.MountParentExt4ReadOnlyResponse, error) {
	const op = "MountParentExt4ReadOnly"
	start := time.Now()
	if req.GetStorageKey() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing storage_key", "storage_key is required").
			WithDocs("https://" + wire.DocsHost + "/vmmd#mount-parent-ext4")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	// Allow-list check (review finding #4): only the canonical
	// parent-base storage keys may be loopback-mounted. The set
	// is per-arch (host + sibling for heterogenous clusters);
	// see sched.IsParentBaseKey.
	if !sched.IsParentBaseKey(req.GetStorageKey()) {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"storage_key not in allow-list",
			"only the canonical parent base ext4 key may be mounted").
			WithDocs("https://" + wire.DocsHost + "/vmmd#mount-parent-ext4")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	mp, err := s.vmm.MountParentExt4(ctx, req.GetStorageKey())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		// Storage-miss → NotFound (load-bearing mapping for
		// imaged's pre-staging checks). Anything else falls
		// through to the default Internal mapping via
		// toProblem.
		if errors.Is(err, vmmdmount.ErrNotFound) {
			p := api.NewProblem(int(codes.NotFound), api.CodeNotFound,
				"storage_key not found",
				"no artifact under that key in the configured storage backend").
				WithDocs("https://" + wire.DocsHost + "/vmmd#mount-parent-ext4")
			s.ops.Observe(op, time.Since(start), p)
			return nil, grpcerr.ToStatus(p)
		}
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &vmmdpb.MountParentExt4ReadOnlyResponse{Mountpoint: mp}, nil
}

// MaterializeParentExt4 copies a parent tree into imaged's shared staging
// directory. vmmd owns the short-lived loopback mount; the caller receives
// ordinary files, so the handoff is not dependent on mount-namespace sharing.
func (s *Server) MaterializeParentExt4(ctx context.Context, req *vmmdpb.MaterializeParentExt4Request) (*vmmdpb.MaterializeParentExt4Response, error) {
	const op = "MaterializeParentExt4"
	start := time.Now()
	if req.GetStorageKey() == "" || req.GetTargetDir() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing materialize path", "storage_key and target_dir are required").
			WithDocs("https://" + wire.DocsHost + "/vmmd#materialize-parent-ext4")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	if !sched.IsParentBaseKey(req.GetStorageKey()) {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"storage_key not in allow-list",
			"only the canonical parent base ext4 key may be materialized").
			WithDocs("https://" + wire.DocsHost + "/vmmd#materialize-parent-ext4")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	err := s.vmm.MaterializeParentExt4(ctx, req.GetStorageKey(), req.GetTargetDir())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		if errors.Is(err, vmmdmount.ErrNotFound) {
			p := api.NewProblem(int(codes.NotFound), api.CodeNotFound,
				"storage_key not found", "no parent artifact exists under that key").
				WithDocs("https://" + wire.DocsHost + "/vmmd#materialize-parent-ext4")
			return nil, grpcerr.ToStatus(p)
		}
		if errors.Is(err, vmmdmount.ErrInvalidOverlayPath) {
			p := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
				"target_dir outside staging root",
				"target_dir must be under /dev/shm/faas-base-staging/").
				WithDocs("https://" + wire.DocsHost + "/vmmd#materialize-parent-ext4")
			return nil, grpcerr.ToStatus(p)
		}
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &vmmdpb.MaterializeParentExt4Response{}, nil
}

// UmountParentExt4 (ADR-053) releases a parent mount the
// previous handler returned. Empty mountpoint → InvalidArgument;
// unknown mountpoint → InvalidArgument (NOT NotFound) so
// imaged's defer-after-error is idempotent on a never-issued or
// already-released path; the asymmetry with MountParentExt4ReadOnly's
// NotFound is intentional and load-bearing. Real umount errors
// surface as Internal so imaged's log surfaces the cause.
func (s *Server) UmountParentExt4(ctx context.Context, req *vmmdpb.UmountParentExt4Request) (*vmmdpb.UmountParentExt4Response, error) {
	const op = "UmountParentExt4"
	start := time.Now()
	if req.GetMountpoint() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing mountpoint", "mountpoint is required").
			WithDocs("https://" + wire.DocsHost + "/vmmd#umount-parent-ext4")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	if err := s.vmm.UmountParentExt4(ctx, req.GetMountpoint()); err != nil {
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	s.ops.Observe(op, time.Since(start), nil)
	return &vmmdpb.UmountParentExt4Response{}, nil
}

// MountOverlayParent (ADR-075 / DEPLOY-1) is the staging path
// imaged uses to compose the per-runtime base ext4 from a shared
// parent. The handler validates all four paths (any empty path
// is InvalidArgument) and forwards to vmm.VmmdAPI.MountOverlayParent.
//
// The path-prefix validation (lowerdir under /srv/fc/parent/,
// upper/work/merged under /dev/shm/faas-base-staging/) lives in
// vmmdmount.MountOverlayParent — the handler's only structural
// check is "no empty path" so a malformed gRPC request surfaces
// as a 400 with no vmmd-side mount attempt.
//
// Real errors from the syscall surface as Internal; imaged's
// log surfaces the cause. The ErrInvalidOverlayPath sentinel
// is lifted to InvalidArgument so a misbehaving imaged can't
// pass /etc or /var/lib/faas as one of the paths and get a
// silent mount fail — the gRPC code is the wire signal.
func (s *Server) MountOverlayParent(ctx context.Context, req *vmmdpb.MountOverlayParentRequest) (*vmmdpb.MountOverlayParentResponse, error) {
	const op = "MountOverlayParent"
	start := time.Now()
	if req.GetLowerdir() == "" || req.GetUpperdir() == "" ||
		req.GetWorkdir() == "" || req.GetMerged() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing path",
			"lowerdir, upperdir, workdir, and merged are all required").
			WithDocs("https://" + wire.DocsHost + "/vmmd#mount-overlay-parent")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	err := s.vmm.MountOverlayParent(ctx,
		req.GetLowerdir(), req.GetUpperdir(),
		req.GetWorkdir(), req.GetMerged())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		if errors.Is(err, vmmdmount.ErrInvalidOverlayPath) {
			p := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
				"overlay path outside allowed prefixes",
				"lowerdir must be under /srv/fc/parent/; upper/work/merged must be under /dev/shm/faas-base-staging/").
				WithDocs("https://" + wire.DocsHost + "/vmmd#mount-overlay-parent")
			s.ops.Observe(op, time.Since(start), p)
			return nil, grpcerr.ToStatus(p)
		}
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &vmmdpb.MountOverlayParentResponse{}, nil
}

// UmountOverlayParent (ADR-075 / DEPLOY-1) is the idempotent
// release path. Empty merged → InvalidArgument; unknown merged
// → InvalidArgument (NOT NotFound) for the same reason as
// UmountParentExt4 (imaged's defer-after-error must be safe).
func (s *Server) UmountOverlayParent(ctx context.Context, req *vmmdpb.UmountOverlayParentRequest) (*vmmdpb.UmountOverlayParentResponse, error) {
	const op = "UmountOverlayParent"
	start := time.Now()
	if req.GetMerged() == "" {
		err := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Missing merged", "merged is required").
			WithDocs("https://" + wire.DocsHost + "/vmmd#umount-overlay-parent")
		s.ops.Observe(op, time.Since(start), err)
		return nil, grpcerr.ToStatus(err)
	}
	err := s.vmm.UmountOverlayParent(ctx, req.GetMerged())
	s.ops.Observe(op, time.Since(start), err)
	if err != nil {
		if errors.Is(err, vmmdmount.ErrUnknownMountpoint) {
			// Idempotent on unknown: imaged's defer-after-error
			// may call here after a partial Mount or after the
			// registry has already swept the entry. Lift to a
			// benign InvalidArgument so the wire response is
			// observable in imaged's log but not a deployment
			// blocker.
			return &vmmdpb.UmountOverlayParentResponse{}, nil
		}
		return nil, grpcerr.ToStatus(toProblem(err))
	}
	return &vmmdpb.UmountOverlayParentResponse{}, nil
}

// readSeccompStatus parses /proc/<pid>/status and returns (mode, filterLen, err).
// Mode is the human-readable equivalent of the kernel's Seccomp integer
// ("disabled" / "strict" / "filter"). filterLen is the number of distinct
// BPF filter programs attached to the process (the `Seccomp_filters:` line).
// An error means the file couldn't be read or the kernel didn't write the
// expected lines — the handler maps that to Error="…" in the response.
func readSeccompStatus(pid int) (string, int32, error) {
	// /proc/<pid>/status is a kernel-managed procfs path — not a
	// customer-supplied path — so the openCustomerFile symlink/
	// non-regular guard doesn't apply. The lint forbidigo rule
	// catches every bare os.Open as a tripwire for the customer-
	// path case; this is the documented exception.
	//
	//nolint:forbidigo // vetted kernel-ABI path, see comment above
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return "", 0, fmt.Errorf("open /proc/%d/status: %w", pid, err)
	}
	defer func() { _ = f.Close() }()
	mode, filterLen, err := ParseSeccompLines(f)
	if err != nil {
		return "", 0, fmt.Errorf("parse /proc/%d/status: %w", pid, err)
	}
	return mode, filterLen, nil
}

// ParseSeccompLines is the Read-from-text twin of readSeccompStatus.
// Exported so the in-process test (seccomp_test.go) AND the
// cross-process e2e (cmd/e2e/sec11_seccomp_e2e_test.go) both
// pin the kernel-ABI parser without spinning up a real
// /proc/<pid>/status. The two callers MUST stay in sync with
// production — a duplicated parser silently drifts when the
// kernel format changes, and the cross-process test would then
// disagree with vmmd's view. Single source of truth lives here.
func ParseSeccompLines(r io.Reader) (string, int32, error) {
	var mode string
	var filterLen int32
	haveMode, haveFilter := false, false
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "Seccomp:"):
			// Line format: "Seccomp:         2" (kernel pads with spaces).
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return "", 0, fmt.Errorf("malformed Seccomp line: %q", line)
			}
			n, err := strconv.Atoi(fields[1])
			if err != nil {
				return "", 0, fmt.Errorf("parse Seccomp value %q: %w", fields[1], err)
			}
			switch n {
			case 0:
				mode = "disabled"
			case 1:
				mode = "strict"
			case 2:
				mode = "filter"
			default:
				return "", 0, fmt.Errorf("unknown Seccomp value %d", n)
			}
			haveMode = true
		case strings.HasPrefix(line, "Seccomp_filters:"):
			// Line format: "Seccomp_filters: 1" (kernel writes this
			// only when Seccomp is 2; missing line is fine if the
			// mode is 0/1).
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return "", 0, fmt.Errorf("malformed Seccomp_filters line: %q", line)
			}
			n, err := strconv.Atoi(fields[1])
			if err != nil {
				return "", 0, fmt.Errorf("parse Seccomp_filters value %q: %w", fields[1], err)
			}
			// Bound-check before narrowing to int32. The kernel reports
			// the number of distinct BPF filter programs attached; in
			// practice this is 1 (the jailer's policy). A value that
			// doesn't fit in int32 is either a kernel bug or a forged
			// /proc — fail loud rather than silently truncate.
			if n > math.MaxInt32 {
				return "", 0, fmt.Errorf("seccomp_filters value %d overflows int32", n)
			}
			//nolint:gosec // G109 — bounds-checked above against math.MaxInt32
			filterLen = int32(n)
			haveFilter = true
		}
		if haveMode && haveFilter {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return "", 0, fmt.Errorf("scan: %w", err)
	}
	if !haveMode {
		return "", 0, fmt.Errorf("no Seccomp line (kernel too old?)")
	}
	return mode, filterLen, nil
}

// toProblem lifts a plain error to *api.Problem if it isn't one already.
// Manager errors are *fmt.Errorf-wrapped strings, so we synthesise an
// Internal problem rather than risk leaking go-internals across the wire.
func toProblem(err error) *api.Problem {
	if err == nil {
		return nil
	}
	if p := api.AsProblem(err); p != nil {
		return p
	}
	return api.NewProblem(int(codes.Internal), "internal",
		"vmmd operation failed", err.Error())
}

// unused import guard.
var _ = fmt.Sprintf
