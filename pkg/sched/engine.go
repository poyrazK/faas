// engine.go is schedd's wake/park engine: the code that turns a policy decision
// (admit this wake, park that idle instance) into a vmmd RPC plus the single
// authoritative write to the `instances` table. It sits between the pure
// selectors (reaper.go, admission.go) and the microVM (vmmclient.go).
//
// Ownership rules it enforces (CLAUDE.md):
//   - schedd is the ONLY writer to `instances` — every transition goes through
//     e.transition, which validates the state-machine edge (state.CanTransition)
//     before writing.
//   - imaged is the ONLY writer to `snapshots` — a park writes the blob via vmmd
//     then hands the row off with a snapshot_written notification (ADR-018); the
//     engine never inserts a snapshot row itself.
//   - the admission ledger is the single choke point for invariants §6.2-1/2 —
//     nothing boots a VM without an Admit first.

package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/whycopy"
	"github.com/onebox-faas/faas/pkg/wire"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// vmmd RPC deadlines (spec §6.1). Centralised here — not in VMMClient —
// because the same client serves every RPC and each has a different
// spec budget. The values are not configurable; they are spec §6.1, not
// operator preference.
const (
	// WakingTimeout is the §6.1 budget for WAKING: "≤ 5s → fall back to
	// cold-boot". 6s = 5s spec + 1s vmmd round trip. The watchdog
	// (commit 3) trips on this same number independently — both stay
	// within ±1s of each other so the watchdog catches a row that
	// sneaks in just before the deadline here.
	WakingTimeout = 6 * time.Second

	// ColdBootTimeout is the §6.1 budget for COLD_BOOTING: "≤ 30s →
	// FAILED". 35s absorbs the vmmd round trip plus jailer setup.
	ColdBootTimeout = 35 * time.Second

	// DestroyTimeout guards the best-effort Destroy calls in the error
	// paths (Wake failed mid-boot, Evict). A hung destroy leaks at
	// worst a stale jail cgroup for 10s — acceptable vs. leaking
	// forever if Firecracker is wedged.
	DestroyTimeout = 10 * time.Second

	// SnapshotTimeout is the budget for the two snapshot-capture RPCs
	// in snapshotAndPark (WarmSnapshot, PauseAndSnapshot). 25s =
	// SnapshotSweepBudget (the §6.1 SNAPSHOTTING budget the watchdog
	// sweeps on, watchdog.go) + 5s vmmd round trip, mirroring how
	// ColdBootTimeout (35s) sits above ColdBootSweepBudget (30s). The
	// engine deadline stays ABOVE the sweep budget so the watchdog
	// still trips first on a row that stalls without the RPC failing.
	//
	// These calls had no deadline at all. DialVMM's godoc states the
	// contract — "per-call deadlines live at the engine call site" —
	// and these two were the sites that never got one. On 2026-09-03 a
	// PauseAndSnapshot blocked in grpc waitOnHeader for 10+ minutes and
	// took the whole scheduler down with it: handleNotification calls
	// Prime synchronously, and Prime → snapshotAndPark → PauseAndSnapshot
	// runs on the same goroutine as the reaper, cron and watchdog ticks
	// (one select in Loop.run). schedd stayed `active` and answered
	// /metrics while doing no work at all.
	SnapshotTimeout = 25 * time.Second

	// SnapshotBudgetBase and SnapshotBudgetPerGB scale the snapshot
	// budget with the instance's memory, because that is what a
	// snapshot writes. One fixed budget cannot serve the plan range:
	// Free is 128 MB and Scale is 1024 MB, an 8x spread, so any single
	// number is either too tight for Scale or uselessly loose for Free.
	//
	// SnapshotTimeout (25s, flat) shipped first and was measured too
	// tight: on 2026-09-03 a 1024 MB prime cold-booted fine in 9s
	// ("wake ok") and then failed at exactly 25s in PauseAndSnapshot,
	// so no deployment could reach `live`. Base covers the fixed
	// pause/serialise/fsync overhead; PerGB covers the memory write.
	//
	// PerGB is sized for a REMOTE upload, not a local write. vmmd runs
	// with FAAS_STORAGE_BACKEND=oci and FAAS_OCI_REGISTRY=https://ghcr.io,
	// so a park pushes a RAM-sized blob to a public registry: the
	// snapshots table holds 47 rows averaging 474 MB with a 1024 MB max.
	// The first cut (30s/GB) assumed local disk and failed every Scale
	// park at exactly its budget — snapshot_ms 45001 against budget_ms
	// 45000 — which blocked every deployment from reaching `live`.
	//
	// Those 47 rows are the evidence this is survivable rather than
	// wedged: 1 GB snapshots DID complete here, before #1288 added the
	// first deadline. Until then PauseAndSnapshot had none at all, so
	// they were free to take as long as the upload needed. The bug
	// #1288 fixed was real — an unbounded RPC wedged the whole
	// scheduler — but the budget it introduced was sized for the wrong
	// storage backend.
	//
	// A 195s park at Scale is tolerable because parking is background
	// work: no customer request waits on it, and ADR-005 makes the
	// snapshot a cache rather than truth, so a park that loses its race
	// costs a cold boot on the next wake, not an outage. The real fix
	// is to stop holding the instance through the upload — capture
	// locally, then push asynchronously — which is a design change, not
	// a constant.
	//
	// CORRECTION. Raising this was the wrong lever and the evidence said
	// so all along: at 25s, 45s, 195s and 615s the capture consumed the
	// budget EXACTLY (snapshot_ms 615001 vs budget_ms 615000 on the last
	// one). A slow upload would have completed at some size. Something
	// that always burns exactly what it is given is not slow — it never
	// finishes.
	//
	// Corroborating: three SIGQUIT dumps caught vmmd with ZERO in-flight
	// gRPC handlers while schedd blocked in waitOnHeader, i.e. schedd
	// never received response headers and vmmd was not running the
	// handler. #1294's router-refresh cancellation was a real and
	// separate bug that muddied earlier readings, but removing it did
	// not make the capture complete.
	//
	// So this is back to a modest value. 600s/GB left every park pinning
	// an instance for ten minutes before failing — strictly worse than
	// the original. 60s/GB covers a plausible local capture plus
	// overhead without holding a slot for minutes on a call that is not
	// going to return.
	//
	// The open bug is tracked separately: PauseAndSnapshot does not
	// reach or does not return from vmmd's handler. That is where the
	// next investigation belongs, NOT here.
	//
	// Superseded rationale kept for the trail:
	// 180s/GB was still short. Re-measured on a QUIET fleet after #1294
	// removed the router-refresh cancellation that had been confounding
	// every earlier reading: snapshot_ms 195000 against budget_ms
	// 195000, DeadlineExceeded, no cancellation error. So a 1 GB park
	// genuinely needs more than 3.25 minutes here — under 5.4 MB/s to
	// ghcr.io, which is plausible for a registry push that also has to
	// digest and commit the blob.
	//
	// 600s/GB puts Scale at ~10 minutes. Deliberately generous: the
	// cost of overshooting is that a genuinely wedged park holds one
	// instance longer, while the cost of undershooting is that NO
	// deployment ever reaches `live`, which is where this fleet has
	// been. The watchdog exemption tracks SnapshotBudgetFor, so the
	// sweep still will not kill a capture that is legitimately running.
	//
	// The true duration is still unmeasured: schedd's deadline
	// propagates to vmmd and aborts the upload, so no run has been
	// allowed to finish. The right answer is to stop holding the
	// instance through the upload at all — capture locally, push
	// asynchronously — and then this constant only has to cover the
	// local capture. Until that lands, snapshot_ms on a SUCCESSFUL park
	// is the number to tighten against.
	SnapshotBudgetBase  = 15 * time.Second
	SnapshotBudgetPerGB = 60 * time.Second
)

// SnapshotBudgetFor returns the wall-clock budget for one instance's
// snapshot capture. ramMB <= 0 (unknown row) falls back to the flat
// SnapshotTimeout so a missing value never yields a zero deadline,
// which would cancel the RPC instantly.
func SnapshotBudgetFor(ramMB int) time.Duration {
	if ramMB <= 0 {
		return SnapshotTimeout
	}
	return SnapshotBudgetBase + time.Duration(float64(SnapshotBudgetPerGB)*(float64(ramMB)/1024.0))
}

// LayerVerifier checks that a cold-boot layer's signature is
// valid. The local interface keeps pkg/sched decoupled from
// pkg/cosign (the verifier impl); the production wiring is
// *cosign.LocalVerifier, constructed in cmd/schedd/main.go.
//
// Returning *api.Problem with code=sig_invalid means "refuse to
// boot this layer" — the engine transitions the deployment to
// DeployFailed and returns 503 to gatewayd-internal. Any other error is a
// transient I/O failure; the caller decides whether to retry.
type LayerVerifier interface {
	Verify(ctx context.Context, layerKey, sigKey string) error
}

// bootTimeout returns the §6.1 budget for a vmmd call when the row is
// in the given state. Unknown states get the cold-boot budget
// (conservative); never returns zero.
//
// This is the production table and the only thing that ships: see
// Engine.budgetFor for the test-only override and why it does not make
// these numbers operator-configurable.
func bootTimeout(s state.State) time.Duration {
	switch s {
	case state.StateWaking:
		return WakingTimeout
	case state.StateColdBooting:
		return ColdBootTimeout
	default:
		return ColdBootTimeout
	}
}

// prefixesToCIDRStrings (ADR-031 + ADR-032) flattens
// state.App.EgressAllowlist (netip.Prefix) into the wire-shape vmmd
// expects ([]string). The empty input returns nil so the proto carries
// an empty list (no allowlist rule emitted). apid's PUT already
// ParsePrefix'd each entry and the apps.egress_allowlist cidr[] DB
// trigger (`apps_egress_allowlist_cidr`, migration 00033) accepts both
// v4 and v6 — every Prefix here is a valid v4 OR v6 — String()
// round-trips through the same parser on the other side
// (vmmdgrpc.proto -> fcvm.WakeRequest -> pkg/fcvm.Wake ->
// netip.ParsePrefix at manager.go, which fails closed). The
// per-family partition happens at the renderer.
func prefixesToCIDRStrings(prefixes []netip.Prefix) []string {
	if len(prefixes) == 0 {
		return nil
	}
	out := make([]string, len(prefixes))
	for i, p := range prefixes {
		out[i] = p.String()
	}
	return out
}

// staticEgressIPString (ADR-119) lifts a *netip.Addr into the
// dotted-quad string the vmmd AppSpec.static_egress_ip field
// expects. nil = no static pin → empty string. The shape is
// validated upstream by apid (family=4, non-reserved) so the
// engine can trust whatever lands in apps.static_egress_ip
// without re-parsing.
func staticEgressIPString(ip *netip.Addr) string {
	if ip == nil {
		return ""
	}
	return ip.String()
}

// startupDeadlineForApp resolves the readiness budget at the scheduler
// boundary. The manifest stores an optional customer override; zero inherits
// the plan default. Returning zero for an unknown plan preserves the vmmd
// default for legacy/test callers instead of inventing a budget here.
func startupDeadlineForApp(app state.App, plan api.Plan) int32 {
	if app.Manifest.StartupDeadlineS > 0 {
		return int32(app.Manifest.StartupDeadlineS)
	}
	limits, ok := api.LimitsFor(plan)
	if !ok || limits.DefaultStartupDeadlineS <= 0 {
		return 0
	}
	return int32(limits.DefaultStartupDeadlineS)
}

// Notifier is the pg_notify surface the engine needs. db.Notify (pool-backed)
// satisfies it via poolNotifier; tests inject a fake.
type Notifier interface {
	Notify(ctx context.Context, channel, payload string) error
}

// Engine drives wakes and parks. It is safe for concurrent use: all mutation of
// one app's instances is serialised by a per-app lock so a Wake and a reaper
// Park for the same app never race the ledger or the state machine.
type Engine struct {
	store  state.Store
	ledger *NodeLedger
	vmm    RoutedVMM
	notif  Notifier
	fcVer  string // running Firecracker version — snapshots load only on a match (ADR-005)
	log    *slog.Logger
	// ops is the per-daemon Prometheus registry (issue #1059 /
	// ADR-127). e.ops.WakeFailure is the schedd-side emitter for
	// the wake-failure observability surface (cluster A commit 3
	// of the platform-observability mega-PR) — schedd emits
	// schedd_wake_failure_total{box, app, reason} from the
	// audit-reason strings on the vmm_boot_failed (engine.go:2123)
	// and record_runtime_failed (:2194) error branches. The
	// closed reason union lives at pkg/wire/metrics.go. ops is
	// nil-safe — nil is tolerated by KillStuck (skip the
	// counter increment) and by WakeFailure (nil receiver returns
	// nil, see pkg/wire/metrics.go).
	ops *wire.OpsMetrics

	// wakeLimiter is the per-app + per-account admission-rate
	// throttle (ADR-099 PR-0 / ADR-080 Risk #1). nil is a no-op —
	// Allow* returns true — so unit tests that don't exercise the
	// rate-limit branch can skip the wire-up. Production
	// cmd/schedd wires NewWakeRateLimiter() so a runaway dispatch
	// fan-out (cron storm, jobs burst) cannot OOM the control
	// plane on cold-boot.
	wakeLimiter *WakeRateLimiter
	// verifier is the build-attestation verifier (ADR-038 / Tier 3
	// phase 3). Wired via WithVerifier after NewEngine returns;
	// nil means "skip verification" — kept for the unit tests
	// that never reach the wake site (the schedule-load and
	// watchdog tests exercise only Ledger + StateMachine). The
	// production path (cmd/schedd/main.go) fails to start if the
	// verifier is nil — see WithVerifier's doc.
	verifier LayerVerifier

	// audit is the IAM-4 seam for cold-boot characterization events
	// (ADR-051 PR-D review finding #6: "app.characterized audit
	// emission"). Distinct from pkg/sched/loop.go::Loop.audit, which
	// serves the cron-fired path; the wake-path emit lives here so
	// it sits next to the SetAppWorkloadClass call it accompanies.
	// nil opts out (no row written); production cmd/schedd wires the
	// same `audit.New(store, log, ops, "schedd")` instance Loop uses.
	audit *audit.Auditor

	// events is the wake-timeline fan-out (issue #517 / PR-C,
	// ADR-064). Sibling of audit — pkg/events.Platform drives the
	// canonical wake.* event vocabulary (queue_accepted, admitted,
	// boot_started, boot_completed, boot_failed, park_started,
	// park_completed, stalled) from the schedd wake path. nil opts
	// out (no row written); production cmd/schedd wires the same
	// `events.NewPlatform("schedd", store, log, ops, broadcaster)`
	// instance cmd/main.go uses.
	events *events.Platform

	// ownerNodeID is the durable Phase 2 / Gate A shard key this
	// schedd serves. Empty = legacy single-box posture (the
	// chooser is free to pick any active node). Non-empty =
	// choosePlacementLocked pins placement.NodeID to this id
	// and refuses to admit an app whose apps.node_id != owner.
	// Wired via WithOwnerNodeID after NewEngine; nil-safe so
	// pre-Phase-2 tests stay green.
	ownerNodeID string

	// tracer is the OTel Tracer that emits sched.wake + vmmd.create_*
	// spans (issue #555 PR-3). nil = no spans emitted (the OTel
	// SDK's noop fallback is a no-op cost). Wired via WithTracer
	// after NewEngine; tests stay span-less.
	tracer oteltrace.Tracer

	// livenessWindow is the per-deployment sliding-window
	// restart counter (issue #554 / ADR-078). DestroyForLivenessFailure
	// calls RecordRestart; on the Nth restart in the window the
	// same call flips the parent app to evicted_cold and emits the
	// instances.parked_liveness_exhausted audit row. nil is safe
	// (the window check is skipped; production cmd/schedd wires a
	// real tracker via WithLivenessWindow).
	livenessWindow *LivenessWindow

	// jobLeaser (issue #1184 Workstream A / ADR-099) is the token-only
	// lease primitive that WakeJob + HandleJobExit
	// use to mint + release per-task leases. nil is tolerated by
	// the unit tests that don't exercise the job surface
	// (WakeJob returns ErrJobTaskAlreadyClaimed without touching
	// the leaser). Production cmd/schedd wires a real PgLeaser via
	// WithJobLeaser.
	jobLeaser JobLeaser
	// jobVmmClient is the vmmd gRPC surface for cold-booting
	// job-task VMs. nil means "unit-test mode" — WakeJob returns
	// a synthetic JobWakeResult without touching vmmd. Production
	// cmd/schedd wires the real client via WithJobVmmClient (M7).
	jobVmmClient jobVmmClient
	// jobExitWaiter supervises the guest job-exit receipt asynchronously so
	// the dispatch tick is never held open for the task's full runtime.
	jobExitWaiter JobExitWaiter
	// jobContext is the schedd lifecycle context used by exit supervisors.
	jobContext context.Context

	// rebalanceCooldownSeconds is the Tier A4 (ADR-064) cooldown
	// between two successful reassignments of the same app. Default
	// api.RebalanceCooldownSeconds = 60s; overridable via
	// FAAS_REBALANCE_COOLDOWN_SECONDS -> WithRebalanceConfig.
	// Stored on the Engine (not as a package-level constant) so
	// tests can drive a tighter window without polluting prod.
	rebalanceCooldownSeconds int
	// rebalanceMaxPerTick mirrors api.RebalanceMaxPerTickPerNode.
	// Default 50; overridable via FAAS_REBALANCE_MAX_PER_TICK ->
	// WithRebalanceConfig. The same per-engine-instance rationale
	// applies (no global state).
	rebalanceMaxPerTick int

	// migrateLiveMaxPerTick (Tier A5 / ADR-066) caps the
	// per-drain-event batch for live-instance migration. Default
	// api.MigrateLiveMaxPerTick = 10; overridable via
	// FAAS_MIGRATE_LIVE_MAX_PER_TICK -> WithMigrateLiveConfig.
	// Live-instance migration is more expensive than parked-
	// app rebalance (each migration spins up a new firecracker
	// VM on the new owner), so the cap is intentionally lower
	// than the parked-app rebalance cap.
	migrateLiveMaxPerTick int
	// migrateLiveLeaseSeconds (Tier A5 / ADR-066) is the per-
	// engine override for the lease window. Default
	// api.MigrateLiveLeaseSeconds = 90; overridable via
	// FAAS_MIGRATE_LIVE_LEASE_SECONDS -> WithMigrateLiveLeaseSeconds.
	// The lease bounds the four-phase handoff end-to-end so a
	// stuck-three-phase handoff surfaces as
	// outcome="lease_expired" rather than a hung goroutine.
	migrateLiveLeaseSeconds int

	// migratingWatchdogTickLimit (Tier A6 / ADR-067) caps the
	// per-tick migration-reconcile batch so a flood of stuck
	// state='migrating' rows from a single dead-owner event
	// does not monopolise the schedd's reconciliation loop.
	// Defaults to api.MigratingWatchdogTickLimit = 50; overridable
	// via FAAS_MIGRATING_WATCHDOG_TICK_LIMIT -> WithMigratingWatchdogTickLimit.
	// The cap is bounded so a runaway flip-loop never spikes
	// the loop into a 100% CPU loop on its own.
	migratingWatchdogTickLimit int
	// migratingWatchdogIntervalSeconds (Tier A6 / ADR-067) is
	// the per-tick cadence on which the migration-reconcile loop
	// runs. Default api.MigratingWatchdogIntervalSeconds = 1s,
	// overridable via FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS ->
	// WithMigratingWatchdogIntervalSeconds. Each tick reconciles
	// up to migratingWatchdogTickLimit rows; an owner that died
	// mid-handoff surfaces as state='migrating' rows here that
	// are then either re-invited (active owner) or hard-deleted
	// (dead owner) within the next tick.
	migratingWatchdogIntervalSeconds int

	// deadNodeReconcilerStalenessSeconds is the heartbeat-age
	// threshold beyond which a RUNNING instance on a (now-dead)
	// node is eligible for the failed-transition self-heal.
	// Default api.DeadNodeReconcilerStalenessSeconds = 120 s;
	// overridable via FAAS_DEAD_NODE_RECONCILER_STALENESS_SECONDS
	// -> WithDeadNodeReconcilerStalenessSeconds. Read inside
	// ReconcileDeadNodeInstances at tick time so the threshold is
	// always fresh — an operator tweak to the env var doesn't
	// require a schedd restart (the next tick picks it up).
	deadNodeReconcilerStalenessSeconds int
	// recoveryArbiter (Workstream B / issue #1184 / ADR-137) is the
	// single per-tick decision policy the migrator + deadnode
	// reconciler consult before any per-instance work. Task #61
	// folds both paths through it so the migrate-vs-recreate
	// verdict lives in one place (recovery_arbiter.go). nil is
	// tolerated — MigrateLiveInstances and ReconcileDeadNodeInstances
	// fall back to their legacy in-method decisions when the
	// arbiter isn't wired (unit-test fixtures; bootstrap window).
	recoveryArbiter *Arbiter
	// pressureAggregator (Tier A9 / ADR-087) is the in-process
	// sliding-window per-app counter of WakeResult{AtCapacity: true}
	// returns. The engine increments it at every AtCapacity return
	// site; the pressure-rebalancer (pkg/sched/pressure_rebalancer.go)
	// polls it every PressureReassessmentIntervalSeconds. Nil is
	// tolerated by IncAtCapacity (test fixtures without the
	// aggregator stay buildable).
	pressureAggregator *PressureAggregator
	// pressureThresholdPerMin mirrors api.PressureAtCapacityThresholdPerMin.
	// Per-app event count over a 60s sliding window that marks the
	// app as "pressured". Default 5; overridable via
	// FAAS_PRESSURE_THRESHOLD_PER_MIN -> WithPressureConfig.
	pressureThresholdPerMin int
	// pressureReassessmentIntervalSeconds mirrors
	// api.PressureReassessmentIntervalSeconds. Pressure-rebalancer
	// sweep cadence. Default 30; overridable via
	// FAAS_PRESSURE_REASSESSMENT_SECONDS -> WithPressureConfig.
	pressureReassessmentIntervalSeconds int
	// pressureMigrationPolicy mirrors api.PressureMigrationPolicy.
	// Closed-set string ∈ {skip_live, migrate_after_1, migrate_after_2}.
	// Parsed once at schedd startup; propagated via
	// WithPressureMigrationPolicy. Unknown values panic at startup.
	pressureMigrationPolicy string
	// pressureSweepCounter counts the consecutive sweeps the
	// pressure-rebalancer has launched for an app within the
	// current cooldown window. Reset on a successful reassign and
	// on the per-event Reset() call. The policy gate reads this
	// counter (migrate_after_2 opens the live-migration window on
	// the second sweep).
	pressureSweepCounter map[string]int
	// pressureSweepMu guards pressureSweepCounter. The map is
	// accessed by the engine (rebalance + reset) and the watcher
	// (increment before delegation); a single mutex keeps the
	// reads race-free without burning atomic shims.
	pressureSweepMu sync.Mutex

	mu    sync.Mutex
	appMu map[string]*sync.Mutex // app_id -> serialisation lock (never GC'd; one-box scale)
	// restartMu/restartInFlight coalesce duplicate restart notifications for
	// one app. The notification is a best-effort hint and can be delivered
	// more than once, so a second caller waits for the first park+fresh-wake
	// operation and receives the same outcome.
	restartMu        sync.Mutex
	restartInFlight  map[string]*restartCall
	restartCompleted map[string]string
	// serviceAppMu serialises service replica allocation across all live
	// deployments for an app. It is distinct from appMu because replica
	// reconciliation invokes admission, which acquires appMu itself, and
	// from serviceMu, which protects one deployment's state transition.
	serviceAppMu map[string]*sync.Mutex
	// serviceMu serialises replica reconciliation per deployment. It is
	// separate from appMu because reconciliation invokes admission, which
	// must acquire appMu itself.
	serviceMu map[string]*sync.Mutex
	// wakeCoord is the per-app demand-aware wake coordinator (ADR-098).
	// Lazily initialised in NewEngine. Lock discipline is a LEAF:
	// wakeCoord.mu is taken and released BEFORE e.lockApp(appID).
	wakeCoord *wakeCoord
	// wakeFanoutCache memoises the per-app fan-out policy for
	// wakeFanoutCacheTTL so a burst does not put an app+account read on
	// the wake hot path for every queued caller.
	wakeFanoutMu    sync.Mutex
	wakeFanoutCache map[string]wakeFanoutEntry

	// warmAffinity is the sticky-warm cache (placement scheduler PR,
	// ADR-025). Defaults to a zero-TTL cache that always returns "no
	// hint" so pre-PR test fixtures keep their existing behaviour.
	// Production wires a real cache via WithWarmAffinity (cmd/schedd/
	// main.go). nil is tolerated by RecordWake / LastWarmNode so a
	// missed wiring is a silent no-op rather than a nil-deref panic.
	warmAffinity *WarmAffinity

	// upstreamAffinity (ADR-098 PR-D) is the connection-aware
	// placement bias. Defaults to nil (FAAS_UPSTREAM_AFFINITY=0
	// branch) so pre-PR test fixtures keep their existing
	// behaviour. Production wires a real cache via
	// WithUpstreamAffinity. nil is tolerated by Score (returns
	// ok=false → chooser falls back to legacy tie-break) so a
	// missed wiring is a silent no-op rather than a nil-deref
	// panic — parallel to warmAffinity's contract above.
	upstreamAffinityMu sync.RWMutex
	upstreamAffinity   *UpstreamAffinity

	// overage is the spend-cap pause-workload seam (issue #561).
	// Nil tolerates the gate branch as a no-op (pre-#561 fixtures
	// keep their existing behaviour); production cmd/schedd wires
	// `newMemCacheOverageChecker(store, 5*time.Second)` via
	// WithOverageChecker. The branch fires inside admitGate AFTER
	// the existing min-floor check — a cap-reached app should not
	// even be considered for warm-hint recycling (issue #462's
	// min-floor is a wake-shape concern, the cap is a budget
	// concern, separated in time so the audit row is unambiguous).
	overage OverageChecker
	// (obs, cap) cents ride on the (outcome, obs, cap) tuple
	// admitGate returns. The earlier field-on-Engine shape was
	// racy across goroutines hitting the cap-reached branch.
	// See admitGate's doc for the full rationale.

	// warmBroadcaster is the push-side of sticky-warm affinity
	// (ADR-025 axis 4). Every RecordWake that actually changes the
	// (appID → nodeID) entry fans out a WarmHintEvent to every
	// subscribed consumer (today: every gatewayd-internal's StreamWarmHints
	// gRPC stream). nil is tolerated by admitAndDispatch (the emit
	// call becomes a no-op) so pre-PR test fixtures that don't wire
	// the broadcaster keep their existing single-box behaviour.
	//
	// Initialised eagerly inside NewEngine (not lazily via
	// WithWarmBroadcaster) because the only producer is the engine
	// itself, and a nil broadcaster at emit time would mask a missed
	// wiring as a silent no-op — eager init catches that mistake at
	// daemon startup.
	warmBroadcaster *warmHintBroadcaster

	// capacityTable is the vmmd→schedd live-capacity cache
	// (ADR-025 axis 5). The handler in pkg/scheddgrpc drives
	// table.Replace on every ReportCapacity RPC event; the
	// chooser (engine.go::applyLiveCapacityMB, PR-2) reads via
	// Lookup before falling back to store.ComputeNodeUsedMB.
	//
	// Initialised eagerly inside NewEngine (not lazily via
	// WithCapacityTable) because the only writer is the gRPC
	// handler and a nil table at lookup time would silently
	// degrade to stale-store — eager init catches a missed
	// wiring at daemon startup.
	//
	// nil is tolerated by the chooser (Lookup returns false)
	// and by the handler (the SchedAPI seam surfaces a nil-safe
	// accessor) so pre-axis-5 test fixtures that don't wire a
	// table keep their existing single-box behaviour.
	capacityTable *nodeCapacityTable

	// nodeRegistry is the notification-backed active-node snapshot used by
	// placement and fleet observers. It is seeded at startup and updated by
	// compute_node_changed; nil keeps older unit fixtures on the store path.
	nodeRegistry *NodeRegistry

	// telemetryCache is the receiver for the batched vmmd Stats rows carried
	// on the persistent ReportCapacity stream. The instance-stats poller
	// projects it locally instead of dialing every compute node.
	telemetryCache *NodeTelemetryCache
	// nodePresence tracks complete vmmd instance sets so a healthy node
	// restart cannot leave RUNNING database rows for VMs that no longer
	// exist locally.
	nodePresence *nodePresenceTracker

	// usageCache is the short TTL cache for the bulk store fallback used when
	// a node has no fresh vmmd capacity report.
	usageCache *NodeUsageCache

	// now is the clock source for the chooser's freshness check
	// (applyLiveCapacityMB). Defaults to time.Now inside NewEngine.
	// Tests override via `Engine.now = func() time.Time { return ... }`
	// to fast-forward the CapacityFreshness budget without sleeping.
	now func() time.Time

	// nodeKeys is the in-memory (key_id → *ecdsa.PublicKey)
	// registry the ReportCapacity handler consults to verify
	// the report's node_signature (ADR-053). Populated by the
	// 'compute_node_changed' pg_notify listener at startup;
	// refreshed on every node key INSERT/UPDATE/DELETE.
	//
	// nil means "signature verification disabled" — pre-slice-3
	// schedd accepts every report as in axis 5. Slice-3 schedd
	// always returns a non-nil registry; the production wiring
	// sets it inside cmd/schedd/main.go's NewEngine caller via
	// WithNodeKeyRegistry (or any future wiring seam).
	nodeKeys *NodeKeyRegistry

	// defaultLocalNodeID is the resolved UUID of the 'default-local'
	// compute_node (issue #97 / ADR-025 axis 3). Looked up once at
	// construction via ComputeNodeByName so the router can resolve
	// target URLs without re-asking the store on every wake. The
	// Router also gets the full active set at startup, but the engine
	// keeps a separate copy because (a) Park / KillStuck need the
	// default-local id without a Store round-trip on the destroy
	// path, and (b) test fixtures that construct the engine without
	// a router still have a usable default-local UUID for cold-boot
	// single-box paths.
	defaultLocalNodeID string

	// bootBudget overrides the §6.1 vmmd call budget. It is nil in every
	// production path — NewEngine never sets it and there is no exported
	// setter — so budgetFor falls through to the bootTimeout constants.
	// The §6.1 budgets remain spec, not operator preference (see the
	// const block above); this field is a *test* seam, not a config knob.
	//
	// Why it exists: the two deadline-enforcement tests used to prove the
	// budget by sleeping a fake vmmd past a real 35s ColdBootTimeout and
	// waiting. That was 70s of the package's 74.5s and made pkg/sched the
	// critical path of the whole `go test ./...` run. Injecting a 200ms
	// budget proves the same property — reservation released, row FAILED,
	// context.DeadlineExceeded surfaced — in 0.2s, and the spec numbers
	// themselves are pinned directly by TestBootTimeout_SpecBudgets.
	bootBudget func(state.State) time.Duration
}

// budgetFor returns the vmmd call budget for a row in state s: the
// injected test budget when one is set, otherwise the §6.1 constants.
func (e *Engine) budgetFor(s state.State) time.Duration {
	if e.bootBudget != nil {
		return e.bootBudget(s)
	}
	return bootTimeout(s)
}

// budgetForWake returns the deadline for the full vmmd operation. A snapshot
// wake is allowed the cold-boot budget because vmmd may need to fall back to a
// cold boot after a restore miss or a slow snapshot load. Reusing the 6-second
// WAKING context for that fallback cancels the cold boot before it can start.
// The test override remains authoritative so deadline tests stay fast.
func (e *Engine) budgetForWake(in bootInput) time.Duration {
	if e.bootBudget != nil {
		return e.bootBudget(in.initState)
	}
	if in.haveSnap && in.snapKey != "" {
		return ColdBootTimeout
	}
	return e.budgetFor(in.initState)
}

// NewEngine wires the engine. notif may be nil (notifications are best-effort in
// tests); log may be nil (slog default); ops may be nil (tests don't assert on
// metrics).
//
// The ctx parameter scopes the constructor's ComputeNodeByName
// bootstrap read (issue #97 / ADR-025 axis 3). Production callers
// pass the daemon's lifecycle ctx; tests pass context.Background()
// wrapped with a t.Deadline-derived timeout if they want a fast
// failure on a missing seed. A lookup failure is a hard error:
// schedd cannot admit wakes without a valid default-local node_id,
// so the daemon refuses to start. The caller (cmd/schedd/main.go)
// logs and exits non-zero; this avoids the silent-degradation
// failure mode where NewEngine returned an Engine with an empty
// defaultLocalNodeID and the next CreateInstance failed at the FK
// with a cryptic "null value in column "node_id"" error far away
// from the root cause (missing migration 00024).
func NewEngine(ctx context.Context, store state.Store, ledger *NodeLedger, vmm RoutedVMM, notif Notifier, fcVer string, log *slog.Logger) (*Engine, error) {
	if log == nil {
		log = slog.Default()
	}
	e := &Engine{
		store:            store,
		ledger:           ledger,
		vmm:              vmm,
		notif:            notif,
		fcVer:            fcVer,
		log:              log,
		jobContext:       ctx,
		appMu:            map[string]*sync.Mutex{},
		restartInFlight:  map[string]*restartCall{},
		restartCompleted: map[string]string{},
		serviceAppMu:     map[string]*sync.Mutex{},
		serviceMu:        map[string]*sync.Mutex{},
		wakeCoord:        newWakeCoord(),
		warmBroadcaster:  newWarmHintBroadcaster(),
		capacityTable:    newNodeCapacityTable(),
		telemetryCache:   NewNodeTelemetryCache(),
		nodePresence:     newNodePresenceTracker(),
		usageCache:       NewNodeUsageCache(),
		now:              time.Now, // tests override post-construction
	}
	// Resolve default-local. Use a bounded context so a wedged DB
	// doesn't block the daemon's boot forever — the watchdog goroutine
	// in cmd/schedd/main.go is the right place for retry, not here.
	bootCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	node, err := store.ComputeNodeByName(bootCtx, state.DefaultLocalNodeName)
	if err != nil {
		return nil, fmt.Errorf("sched: resolve default-local compute_node %q: %w", state.DefaultLocalNodeName, err)
	}
	if node.ID == "" {
		return nil, fmt.Errorf("sched: default-local compute_node %q has empty id", state.DefaultLocalNodeName)
	}
	e.defaultLocalNodeID = node.ID
	return e, nil
}

// WithOpsMetrics attaches a metrics bag to the engine for the §6.1
// watchdog's per-(from,to) kill counter and the audit-log write-failure
// counter. Returns the engine for builder-style wiring.
func (e *Engine) WithOpsMetrics(ops *wire.OpsMetrics) *Engine {
	e.ops = ops
	return e
}

// WithWarmAffinity attaches the sticky-warm cache (placement scheduler
// PR, ADR-025). The engine reads LastWarmNode for the request hint
// before calling ChoosePlacement and records the chosen node on a
// successful admit. nil is tolerated (records become no-ops, hints
// always empty) so legacy test fixtures that don't wire this keep
// their existing single-box behaviour.
func (e *Engine) WithWarmAffinity(w *WarmAffinity) *Engine {
	e.warmAffinity = w
	return e
}

// WithUpstreamAffinity attaches the connection-aware placement
// bias (ADR-098 PR-D). The engine reads Score(appID) before
// calling ChoosePlacement and stamps Request.PreferredRegion;
// the chooser honors it via the upstream_fit tie-break in
// betterCandidate. nil is tolerated (Score returns ok=false →
// legacy tie-break) so legacy test fixtures that don't wire
// this keep their existing single-box behaviour.
func (e *Engine) WithUpstreamAffinity(u *UpstreamAffinity) *Engine {
	e.upstreamAffinityMu.Lock()
	e.upstreamAffinity = u
	e.upstreamAffinityMu.Unlock()
	return e
}

// UpstreamAffinity returns the currently configured placement cache. It is a
// snapshot accessor for the runtime-config watcher; nil means the chooser
// should fail open to its legacy placement order.
func (e *Engine) UpstreamAffinity() *UpstreamAffinity {
	if e == nil {
		return nil
	}
	e.upstreamAffinityMu.RLock()
	u := e.upstreamAffinity
	e.upstreamAffinityMu.RUnlock()
	return u
}

// WithOverageChecker attaches the spend-cap pause-workload seam
// (issue #561). The engine consults the checker inside admitGate
// AFTER the existing min-floor branch; a cap-reached app refuses new
// wakes with `*api.Problem{Code: CodeAdmissionRefused}`. nil is
// tolerated (the branch becomes a no-op, all wakes proceed normally)
// so legacy fixtures that don't wire the seam keep their existing
// behaviour. Production cmd/schedd wires
// `newMemCacheOverageChecker(store, 5*time.Second)`.
func (e *Engine) WithOverageChecker(c OverageChecker) *Engine {
	e.overage = c
	return e
}

// WithVerifier attaches the build-attestation verifier. Production
// wiring is in cmd/schedd/main.go (which fails to start on a
// nil / missing pub-key path). Tests that never reach the wake
// site (scheduler-load + watchdog tests) leave this nil — the
// verify call is gated on `e.verifier != nil` so the absence is
// benign for the unit-test surface.
func (e *Engine) WithVerifier(v LayerVerifier) *Engine {
	e.verifier = v
	return e
}

// WithAudit attaches the IAM-4 audit seam for the cold-boot
// characterization path (ADR-051 PR-D review finding #6).
// Distinct from pkg/sched/loop.go::Loop.WithAudit, which serves
// the cron-fired path; this setter scopes audit emission to the
// wake path. nil opts out (no row written) so pre-PR-D fixtures
// keep their existing behaviour. Production cmd/schedd wires the
// same `audit.New(store, log, ops, "schedd")` instance Loop uses.
func (e *Engine) WithAudit(a *audit.Auditor) *Engine {
	e.audit = a
	return e
}

// WithEvents stamps the wake-timeline fan-out (issue #517 / PR-C,
// ADR-064) on the engine. Sibling of WithAudit — pkg/events.
// Platform drives the canonical wake.* event vocabulary
// (queue_accepted, admitted, boot_started, boot_completed,
// boot_failed, park_started, park_completed, stalled) from the
// schedd wake path. nil opts out (no row written) so pre-PR-C
// fixtures keep their existing behaviour. Production cmd/schedd
// wires the same `events.NewPlatform("schedd", store, log, ops,
// broadcaster)` instance cmd/main.go uses.
func (e *Engine) WithEvents(p *events.Platform) *Engine {
	if e == nil {
		return e
	}
	e.events = p
	return e
}

// WithOwnerNodeID stamps the Phase 2 / Gate A owner shard key
// on the engine. cmd/schedd wires the same id it passes to
// scheddgrpc.WithOwner; choosePlacementLocked pins
// placement.NodeID = ownerNodeID when set, refuses to admit
// an app whose apps.node_id != owner. Empty = legacy
// single-box posture: the chooser picks freely across active
// nodes. Safe to call concurrently with the wake path: reads
// of e.ownerNodeID race only with the initial stamp.
// WithRebalanceConfig overrides the Tier A4 (ADR-064)
// rebalancer tunables for this Engine. Production defaults
// are api.RebalanceCooldownSeconds=60 and
// api.RebalanceMaxPerTickPerNode=50; schedd main reads
// FAAS_REBALANCE_COOLDOWN_SECONDS + FAAS_REBALANCE_MAX_PER_TICK
// once at startup and threads them through here so the envs
// take effect without restarting the engine. Panics on
// non-positive inputs (a bad env must NOT silently default
// back to the api.* constants — that'd mask operator typos).
func (e *Engine) WithRebalanceConfig(cooldownSeconds, maxPerTick int) *Engine {
	if cooldownSeconds <= 0 {
		panic(fmt.Sprintf("sched: WithRebalanceConfig: cooldownSeconds must be > 0, got %d", cooldownSeconds))
	}
	if maxPerTick <= 0 {
		panic(fmt.Sprintf("sched: WithRebalanceConfig: maxPerTick must be > 0, got %d", maxPerTick))
	}
	e.rebalanceCooldownSeconds = cooldownSeconds
	e.rebalanceMaxPerTick = maxPerTick
	return e
}

// WithMigrateLiveConfig (Tier A5 / ADR-066) sets the per-engine
// override for the live-instance migration per-tick cap. Same
// "panic on bad env" contract as WithRebalanceConfig: a typo in
// FAAS_MIGRATE_LIVE_MAX_PER_TICK must not silently fall back to
// the api.* default.
func (e *Engine) WithMigrateLiveConfig(maxPerTick int) *Engine {
	if maxPerTick <= 0 {
		panic(fmt.Sprintf("sched: WithMigrateLiveConfig: maxPerTick must be > 0, got %d", maxPerTick))
	}
	e.migrateLiveMaxPerTick = maxPerTick
	return e
}

// WithMigrateLiveLeaseSeconds (Tier A5 / ADR-066) sets the
// per-engine override for the live-instance migration lease
// window (the upper bound on the four-phase handoff). Same
// "panic on bad env" contract as WithMigrateLiveConfig: a typo
// in FAAS_MIGRATE_LIVE_LEASE_SECONDS must not silently fall
// back to the api.* default.
func (e *Engine) WithMigrateLiveLeaseSeconds(seconds int) *Engine {
	if seconds <= 0 {
		panic(fmt.Sprintf("sched: WithMigrateLiveLeaseSeconds: seconds must be > 0, got %d", seconds))
	}
	e.migrateLiveLeaseSeconds = seconds
	return e
}

// WithMigratingWatchdogTickLimit (Tier A6 / ADR-067) sets the
// per-tick cap on the migration-reconcile loop. Same
// "panic on bad env" contract as WithMigrateLiveLeaseSeconds: a
// typo in FAAS_MIGRATING_WATCHDOG_TICK_LIMIT must not silently
// fall back to the api.* default.
func (e *Engine) WithMigratingWatchdogTickLimit(n int) *Engine {
	if n <= 0 {
		panic(fmt.Sprintf("sched: WithMigratingWatchdogTickLimit: n must be > 0, got %d", n))
	}
	e.migratingWatchdogTickLimit = n
	return e
}

// WithMigratingWatchdogIntervalSeconds (Tier A6 / ADR-067) sets
// the per-tick cadence on which the migration-reconcile loop
// runs. Same "panic on bad env" contract as the other With*
// setters.
func (e *Engine) WithMigratingWatchdogIntervalSeconds(seconds int) *Engine {
	if seconds <= 0 {
		panic(fmt.Sprintf("sched: WithMigratingWatchdogIntervalSeconds: seconds must be > 0, got %d", seconds))
	}
	e.migratingWatchdogIntervalSeconds = seconds
	return e
}

// WithWakeRateLimiter wires the per-app + per-account wake-admission
// rate-limit primitive (ADR-099 PR-0 / ADR-080 Risk #1). A nil value
// is allowed — the rate-limit check then short-circuits to "allow",
// preserving the pre-PR-0 behaviour for unit tests that don't exercise
// the throttle. Production cmd/schedd wires NewWakeRateLimiter() so a
// runaway dispatch fan-out (cron storm, jobs burst) cannot OOM the
// control plane on cold-boot.
//
// Pass the same *WakeRateLimiter to WithRateLimiterForgetting on
// account / app delete events so the buckets don't leak. (Not wired
// in PR-0 — the apid-side Forget hook lands with the delete handlers
// in PR-D; the in-memory bucket leak is bounded by the per-process
// lifetime and surfaced via WakeRateLimiter.BucketCount() in /metrics.)
func (e *Engine) WithWakeRateLimiter(l *WakeRateLimiter) *Engine {
	e.wakeLimiter = l
	return e
}

// WithDeadNodeReconcilerStalenessSeconds sets the heartbeat-age
// threshold beyond which a RUNNING instance on a dead node is
// eligible for the failed-transition self-heal. Same "panic on
// bad env" contract as the other With* setters: a typo in
// FAAS_DEAD_NODE_RECONCILER_STALENESS_SECONDS must not silently
// fall back to the api.* default. The value is read at every
// tick, so an operator can lower it mid-flight if a customer
// complains about a billing interval they consider too long.
func (e *Engine) WithDeadNodeReconcilerStalenessSeconds(seconds int) *Engine {
	if seconds <= 0 {
		panic(fmt.Sprintf("sched: WithDeadNodeReconcilerStalenessSeconds: seconds must be > 0, got %d", seconds))
	}
	e.deadNodeReconcilerStalenessSeconds = seconds
	return e
}

// WithPressureConfig (Tier A9 / ADR-087) sets the per-engine
// overrides for the capacity-pressure rebalancer tunables — the
// aggregation threshold and the reassessment sweep cadence.
// Same "panic on bad env" contract as WithRebalanceConfig: a
// typo in FAAS_PRESSURE_THRESHOLD_PER_MIN or
// FAAS_PRESSURE_REASSESSMENT_SECONDS must not silently fall back
// to the api.* defaults. The values are read at every sweep, so
// an operator tweak to the env vars doesn't require a schedd
// restart (the next tick picks them up).
func (e *Engine) WithPressureConfig(thresholdPerMin, reassessmentSeconds int) *Engine {
	if thresholdPerMin <= 0 {
		panic(fmt.Sprintf("sched: WithPressureConfig: thresholdPerMin must be > 0, got %d", thresholdPerMin))
	}
	if reassessmentSeconds <= 0 {
		panic(fmt.Sprintf("sched: WithPressureConfig: reassessmentSeconds must be > 0, got %d", reassessmentSeconds))
	}
	e.pressureThresholdPerMin = thresholdPerMin
	e.pressureReassessmentIntervalSeconds = reassessmentSeconds
	return e
}

// WithPressureMigrationPolicy (Tier A9 / ADR-087) sets the
// per-engine override for the pressure-rebalancer migration
// policy. Closed-set validation: a typo in
// FAAS_PRESSURE_MIGRATION_POLICY must not silently fall back to
// the api.* default. Unknown values panic at startup — closed-set
// is the contract; falling back to a default would mask operator
// typos that would silently change customer-facing latency.
func (e *Engine) WithPressureMigrationPolicy(policy string) *Engine {
	switch policy {
	case "skip_live", "migrate_after_1", "migrate_after_2":
		e.pressureMigrationPolicy = policy
	default:
		panic(fmt.Sprintf("sched: WithPressureMigrationPolicy: policy must be one of {skip_live, migrate_after_1, migrate_after_2}, got %q", policy))
	}
	return e
}

// WithPressureAggregator (Tier A9 / ADR-087) attaches the
// in-process sliding-window counter to the engine. The engine
// increments it at every WakeResult{AtCapacity: true} return;
// the watcher (pkg/sched/pressure_rebalancer.go) polls it for
// PressuredApps on each sweep. Nil is tolerated by the call
// sites (test fixtures without the aggregator stay buildable).
func (e *Engine) WithPressureAggregator(agg *PressureAggregator) *Engine {
	if e == nil {
		return e
	}
	e.pressureAggregator = agg
	if e.pressureSweepCounter == nil {
		e.pressureSweepCounter = make(map[string]int)
	}
	return e
}

// IncAtCapacity (Tier A9 / ADR-087) is the engine's hook into
// the pressure aggregator. Called at every WakeResult{AtCapacity:
// true} return site; the aggregator keeps a 60s sliding window
// per app. The Prom counter is incremented on the same path so
// the §12 dashboard panel stays consistent with the
// pressure-rebalancer trigger. Nil-safe on both aggregator and
// metrics so test fixtures without either keep building.
func (e *Engine) IncAtCapacity(appID, kind string) {
	if e == nil {
		return
	}
	if e.pressureAggregator != nil {
		e.pressureAggregator.IncAtCapacity(appID, time.Now())
	}
	if e.ops != nil {
		e.ops.AppAtCapacityTotal(appID, kind).Inc()
	}
}

// IncrementPressureSweepCounter (Tier A9 / ADR-087) bumps the
// per-app consecutive-sweep counter the policy gate reads to
// open the live-migration window. Called by the watcher at
// every sweep before delegation; reset by the engine on a
// successful reassign.
func (e *Engine) IncrementPressureSweepCounter(appID string) int {
	if e == nil {
		return 0
	}
	e.pressureSweepMu.Lock()
	defer e.pressureSweepMu.Unlock()
	if e.pressureSweepCounter == nil {
		e.pressureSweepCounter = make(map[string]int)
	}
	e.pressureSweepCounter[appID]++
	return e.pressureSweepCounter[appID]
}

// ResetPressureSweepCounter (Tier A9 / ADR-087) clears the
// per-app consecutive-sweep counter after a successful reassign
// (or any other terminal event). The watcher also calls this
// between sweeps for apps that fell below the threshold.
func (e *Engine) ResetPressureSweepCounter(appID string) {
	if e == nil {
		return
	}
	e.pressureSweepMu.Lock()
	defer e.pressureSweepMu.Unlock()
	delete(e.pressureSweepCounter, appID)
}

func (e *Engine) WithOwnerNodeID(nodeID string) *Engine {
	if e == nil {
		return e
	}
	e.ownerNodeID = nodeID
	return e
}

// WithRecoveryArbiter wires the single per-tick decision policy
// (Workstream B / issue #1184 / ADR-137). Task #61 folds the
// live_migrator + deadnode_reconciler paths through the arbiter;
// before this setter lands they carried duplicate per-instance
// decision logic that raced. cmd/schedd constructs one Arbiter
// (sharing the dispatchers it wires — Engine itself satisfies
// the RecreateDispatcher interface) and passes it here. nil
// is tolerated for the unit tests that don't exercise the
// recovery flow (recovery_arbiter_test.go pins the
// nil-arbiter behaviour).
func (e *Engine) WithRecoveryArbiter(a *Arbiter) *Engine {
	if e == nil {
		return e
	}
	e.recoveryArbiter = a
	return e
}

// WithLivenessWindow attaches the per-deployment restart counter
// (issue #554 / ADR-078). DestroyForLivenessFailure calls
// RecordRestart on every destroy; on the Nth restart in the
// window the same call flips the parent app to evicted_cold and
// emits the instances.parked_liveness_exhausted audit row. nil
// is safe — the window check is skipped. Production cmd/schedd
// wires sched.NewLivenessWindow(window, maxN) at construction.

// createInstanceWithWakeRetry is the cluster-coord Layer 2 helper
// (multi-host safety cluster PR-5 / audit F4). Wraps
// store.CreateInstance; on a SQLSTATE 23505 (the partial unique
// index instances_wake_attempt_active_idx rejected the INSERT
// because another schedd already created an in-flight row with
// the same wake_id) it returns state.ErrWakeAlreadyInflight.
//
// The helper deliberately does NOT return the winner's row. The
// engine's downstream path (ledger.Admit, vmm.CreateColdBoot,
// SetInstanceRuntime, store.DeleteInstance, transitionWithKind,
// emitInstanceChanged) is keyed by (ins.ID, placement.NodeID) —
// if we returned the remote winner's row, this schedd would boot
// a LOCAL microVM tagged with a REMOTE instance UUID, double-
// billing the customer, double-allocating per-app concurrency
// slots, and (in single-box degenerate case) colliding on
// cgroup / jail uid / netns per spec §6.2-5. The partial unique
// index IS the cluster-coord primitive: one inserter wins, all
// others exit cleanly with a typed sentinel that the upstream
// surfaces through the existing error funnel.
//
// Retry policy: a single attempt. The partial unique index is
// binary (succeeds, or 23505) — once tripped, the winner's row
// is already in the table in WAKING / COLD_BOOTING; retries
// against the same wake_id always 23505 and never win. The
// jittered retry loop previously lifted into this helper was
// removed because (a) the binary guarantee above makes retries
// useless, and (b) the previous "winner-recovery" branch read
// the winner's row and returned it as if this schedd had minted
// it — that turned the helper into a dangerous lie (the layered
// downstream was never designed to be safe with a foreign row).
//
// Caller propagation: the three call sites (engine.go:1460 floor
// admit, :1764 wake dispatch, :3750 prime) wrap the sentinel
// with their own context and surface it as a typed error; the
// gateway-side retry / cron-side reschedule / redeploy handles
// the "another box handled it" follow-up. Callers that want to
// observe the winner's progress can call
// state.PgStore.ReadActiveInstanceForWakeID directly — the
// primitive remains exported for that purpose.
func (e *Engine) createInstanceWithWakeRetry(ctx context.Context, appID, deploymentID, initState string, ramMB int, nodeID, wakeID string) (state.Instance, error) {
	ins, err := e.store.CreateInstance(ctx, appID, deploymentID, initState, ramMB, nodeID, wakeID)
	if err == nil {
		return ins, nil
	}
	if errors.Is(err, state.ErrConcurrentWake) {
		// Loser path: another schedd (or this schedd's wakeCoord
		// released the slot to a peer race that landed just before
		// us) won the INSERT. Surfacing ErrWakeAlreadyInflight
		// unwinds the layered downstream — no ledger reservation,
		// no vmmd.CreateColdBoot, no row mutation — so the local
		// box cannot boot a microVM tagged with the remote winner's
		// instance UUID.
		return state.Instance{}, state.ErrWakeAlreadyInflight
	}
	return state.Instance{}, err
}
func (e *Engine) WithLivenessWindow(w *LivenessWindow) *Engine {
	if e == nil {
		return e
	}
	e.livenessWindow = w
	return e
}

// WithTracer wires the OTel Tracer used for sched.wake +
// vmmd.create_* spans (issue #555 PR-3). When nil (the default), the
// engine emits no spans — the OTel SDK's noop fallback is a no-op
// cost. Production cmd/schedd wires the global tracer; legacy tests
// stay span-less.
func (e *Engine) WithTracer(tr oteltrace.Tracer) *Engine {
	if e == nil {
		return e
	}
	e.tracer = tr
	return e
}

// startCreateSpan begins a vmmd.create_* client span under the active
// wakeCtx (issue #555 PR-3). Returns the new child ctx and a nil-safe
// end func the caller MUST defer. extraAttrs is appended to the
// common app/instance/deployment triple; pass nil for the cold-boot
// path (no snap_id). When e.tracer is nil the helper is a no-op and
// the returned end func is also nil-safe.
func (e *Engine) startCreateSpan(wakeCtx context.Context, name, snapID string, bootInput bootInput) (context.Context, oteltrace.Span) {
	if e.tracer == nil {
		return wakeCtx, nil
	}
	attrs := []attribute.KeyValue{
		attribute.String("app_id", bootInput.appID),
		attribute.String("instance_id", bootInput.insID),
		attribute.String("deployment_id", bootInput.depID),
	}
	if snapID != "" {
		attrs = append(attrs, attribute.String("snap_id", snapID))
	}
	return e.tracer.Start(wakeCtx, name, oteltrace.WithAttributes(attrs...))
}

// endSpan is the nil-safe closer for startCreateSpan's return value.
// Kept as a tiny helper so the call sites read as a defer pair.
func endSpan(s oteltrace.Span) {
	if s != nil {
		s.End()
	}
}

// CapacityTable returns the per-node live-capacity table for
// the ReportCapacity gRPC handler to drive (ADR-025 axis 5).
// The handler calls table.Replace per stream Recv; the chooser
// (PR-2) reads via Lookup.
//
// nil-safe: pre-axis-5 fixtures that bypass NewEngine return nil,
// and the handler treats nil as "no table, drop the wire".
// Production paths always go through NewEngine so the table is
// eagerly initialised.
func (e *Engine) CapacityTable() *nodeCapacityTable { return e.capacityTable }

// CapacitySink returns the table-apply sink the ReportCapacity
// handler invokes per stream Recv (ADR-025 axis 5). The closure
// applies the report to the engine's per-node table; a non-nil
// error aborts the stream (today the closure never errors —
// kept as a func-returning-closure to match the SchedAPI /
// WarmHintSink shape and to give tests a stable seam).
//
// Returning the sink rather than the table itself keeps the
// nodeCapacityTable type unexported in pkg/sched and presents a
// narrow surface to the gRPC layer — the handler cannot read or
// mutate table state outside the per-event Replace path.
//
// nil table (pre-axis-5 fixture) returns a no-op sink.
func (e *Engine) CapacitySink() CapacitySink {
	if e == nil || e.capacityTable == nil {
		return func(CapacityReport) error { return nil }
	}
	return e.capacityTable.CapacitySink()
}

// WithNodeRegistry wires the notification-backed active-node cache. The
// bootstrap snapshot is supplied by cmd/schedd after NewEngine returns.
func (e *Engine) WithNodeRegistry(reg *NodeRegistry) *Engine {
	if e == nil {
		return e
	}
	e.nodeRegistry = reg
	return e
}

// NodeRegistry returns the active-node cache used by placement and observers.
func (e *Engine) NodeRegistry() *NodeRegistry {
	if e == nil {
		return nil
	}
	return e.nodeRegistry
}

// NodeTelemetryCache returns the cache populated by ReportCapacity. The
// instance-stats poller reads this cache at its local projection cadence.
func (e *Engine) NodeTelemetryCache() *NodeTelemetryCache {
	if e == nil {
		return nil
	}
	return e.telemetryCache
}

// TelemetrySink returns the narrow callback used by scheddgrpc to apply a
// complete node telemetry batch. A nil-safe no-op keeps pre-stream fixtures
// compatible.
func (e *Engine) TelemetrySink() TelemetrySink {
	if e == nil || e.telemetryCache == nil {
		return func(string, time.Time, time.Time, []NodeTelemetry) error { return nil }
	}
	return func(nodeID string, sampledAt, receivedAt time.Time, rows []NodeTelemetry) error {
		e.telemetryCache.Replace(nodeID, sampledAt, receivedAt, rows)
		return nil
	}
}

// WithNodeKeyRegistry wires the ADR-053 signature-verification
// registry onto the engine. Called once at startup after
// NewEngine returns; the listener for 'compute_node_changed'
// fires Refresh on every notify. A nil registry disables
// signature verification (pre-slice-3 mode).
//
// Returns the engine so it composes with the NewEngine call:
// `e, err := NewEngine(...).WithNodeKeyRegistry(reg)`.
func (e *Engine) WithNodeKeyRegistry(reg *NodeKeyRegistry) *Engine {
	if e == nil {
		return e
	}
	e.nodeKeys = reg
	return e
}

// NodeKeyRegistry returns the engine's signature-verification
// registry. nil means "verification disabled" — the handler
// accepts every report as in pre-slice-3.
//
// Implements scheddgrpc.SchedAPI.
func (e *Engine) NodeKeyRegistry() *NodeKeyRegistry {
	if e == nil {
		return nil
	}
	return e.nodeKeys
}

// WakeResult is what the gateway needs back from a wake: which instance
// serves the app and which compute_node it lives on
// (issue #98 / ADR-028). The gateway uses NodeID to look up the
// per-node gRPC client in its routing cache and forward via the vmmd
// ForwardHTTP RPC.
//
// The previous shape carried `Addr = host_ip:8080`, an inner-netns
// placeholder reachable only from gatewayd-internal on the local box. Remote
// nodes return `host_ip` from inside a different jailer's netns and
// the gateway cannot dial it. The new shape carries the routable
// identity (the compute_node.id), with the dial target chosen on
// the gateway side from that.
type WakeResult struct {
	InstanceID string
	NodeID     string // compute_nodes.id (uuid), empty only on error
	Method     vmmdpb.WakeMethod
	// WakeID is the per-wake-attempt correlation handle (gaps analysis
	// 2026-07-23). UUIDv7 minted at Phase 2 before CreateInstance;
	// gatewayd-internal propagates it back to the client as x-faas-wake-id and
	// operators see it on schedule/wake slog calls. On the Phase-1
	// fast path (a second Wake for an already-RUNNING app) this is
	// the wake_id of the wake that brought the instance up — surfaced
	// from the existing row so the gateway's response header carries
	// the same value a cold-wake response would have. On every other
	// path it's the UUIDv7 minted in Phase 2 (gaps analysis
	// 2026-07-23 review finding #1: previous behaviour left the
	// header unset on the fast path, which lost the correlation
	// handle for warm requests).
	WakeID string
	// AtCapacity is set true by AdmitInstance (issue #168) when the
	// app is already at effective max_concurrency and no new instance
	// row was created. The gateway treats this as a benign no-op when
	// it already has ≥1 cached target; the Wire RPC carries the same
	// signal as a typed at_capacity boolean so the gateway never
	// inspects error codes. Always false on Wake (the existing fast
	// path is the only short-circuit there).
	AtCapacity bool
	// Port (issue #460 / ADR-053, PR-C) is the per-deployment
	// override port copied from dep.OverridePort. 0 = legacy 8080.
	// On the Phase-1 fast path this comes from a LiveDeployment
	// lookup so the gateway sees the same value AdmitInstance would
	// have produced; on the admit path it comes from bootInput.spec.
	Port int
	// DeploymentID (issue #556 / PR-B) is the live deployment id
	// the new instance was admitted for. The gateway caches it on
	// Target so the per-deployment weighted picker (PGBackend.Pick)
	// routes subsequent requests to the right deployment bucket.
	// Set on every admitted path (Phase-1 fast path reads the
	// deployment from LiveDeployment; admit path from the dep the
	// ledger admit produced). Empty on AtCapacity=true and on
	// errors. Additive per ADR-016 — pre-PR-B callers see empty and
	// the gateway treats that as "single-deployment legacy mode"
	// (Target.DeploymentID empty, picker collapses to today's
	// behaviour).
	DeploymentID string
	// RequestCount (ADR-098 C9) is the per-instance request counter
	// schedd has observed via the batched ReportActivity path. The
	// engine stamps it on the admitted path so the gateway's
	// per-instance cache can show "warming up" vs "warmed" without
	// a second round-trip onto the ledger. Read on the warm-snapshot
	// 5th promotion gate (C10) which gates promotion to the warm
	// tier until the instance has served a meaningful number of
	// requests. 0 on the at-capacity path (no instance was admitted)
	// and on freshly-cold-booted instances (the writer hasn't yet
	// flushed the first batch).
	//
	// Mirrors AdmitInstanceResponse.request_count (tag 9) on the
	// wire — additive per ADR-016, pre-PR callers see 0.
	RequestCount int64
}

// Wake ensures a running instance for appID and returns its address (spec §4.3
// wake path). Idempotent: an app that already has a RUNNING instance returns it
// without a new boot — this is what lets the gateway's single-flight WakeGate
// hand every coalesced waiter an address. Admission denial returns a *api.Problem
// (capacity / plan concurrency) the gateway maps straight to 503/409.
//
// Lock discipline (commit 2, fixing finding #1 of the M7 audit):
//
//   - Phase 1 — fast path. Under appMu. A second Wake for the same app
//     that races a RUNNING row returns it without a new boot.
//   - Phase 2 — admit window. Under appMu. resolveApp, CreateInstance,
//     emit, ledger.Admit, AppSpec build. Nothing slow.
//   - Phase 3 — DROP THE LOCK around the vmmd RPC. The cold-boot can
//     take up to ColdBootTimeout (35s, spec §6.1) and we must not hold
//     the per-app mutex for the full boot — a reaper Park for the
//     same app, or a second concurrent Wake, would block for that
//     window. The pre-boot state (WAKING or COLD_BOOTING) plus the
//     ledger reservation are the contract: another caller can observe
//     them, but the row is not yet RUNNING so RunningInstanceForApp
//     keeps missing and the second Wake proceeds to its own boot — no
//     double boot race because of the Phase 4 re-read.
//   - Phase 4 — RE-ACQUIRE the lock. Re-read the row under the lock;
//     if the watchdog (commit 3) or a Park stole the state during
//     Phase 3, abort the Wake: release the ledger, destroy the VM we
//     just booted, and surface the error. Otherwise SetInstanceRuntime,
//     transition → RUNNING.
//
// We re-acquire for Phase 4 (rather than commit without the lock)
// because the post-vmmd commit writes a partial row (host_ip, netns,
// guest_uid) and a Park triggered by the reaper reads the row under
// its own appMu; without re-acquiring, the reaper could see a
// partially-written row and act on it.
// Wake ensures a running instance for appID and returns its address (spec §4.3
// wake path). Idempotent: an app that already has a RUNNING instance returns it
// without a new boot — this is what lets the gateway's single-flight WakeGate
// hand every coalesced waiter an address. Admission denial returns a *api.Problem
// (capacity / plan concurrency) the gateway maps straight to 503/409.
//
// Phase 1 is the fast-path shortcut under appMu; missing means the
// shared admitAndDispatch runs Phase 2-4. AdmitInstance (issue #168)
// skips Phase 1 explicitly so a gateway can demand a new instance
// even when others are already RUNNING.
func (e *Engine) Wake(ctx context.Context, appID, deploymentID, scope, trigger string) (WakeResult, error) {
	// PR-B (issue #272 / ADR-095): scope-aware Wake. Stamp the
	// scope on the ctx so every downstream helper (resolveApp,
	// loadAPIEnv, LiveDeployment lookup, ledger admit) threads the
	// same value. Empty scope is a no-op for WithScope — the ctx
	// is returned unchanged, so pre-PR-B callers (cron, meterd,
	// e2e) keep byte-identical behaviour.
	ctx = WithScope(ctx, scope)
	// ── Phase 1: fast path under appMu ─────────────────────────────
	release := e.lockApp(appID)
	if ins, err := e.store.RunningInstanceForApp(ctx, appID); err == nil && e.wakeInstanceModeMatchesApp(ctx, appID, ins) {
		// PR-C (issue #460 / ADR-053): resolve the live deployment so
		// the response's Port field is consistent with what
		// AdmitInstance would have produced. The instance row
		// carries no port (port is a deployment-level concept); the
		// live dep row carries dep.OverridePort.
		//
		// Why a LiveDeployment read is acceptable here: Wake is the
		// legacy fast path used by meterd's per-minute sampler + cron
		// firings, NOT the customer hot path. Production customer
		// requests go through AdmitInstance (cmd/gatewayd-internal/main.go),
		// which has the live deployment already loaded. So this read
		// adds one cheap PG roundtrip (~1ms, single-row lookup with
		// the existing (app_id, status) partial index) per minute per
		// active app — well below any customer-facing budget.
		//
		// A read failure here logs (slog) and falls through with
		// Port=0 — the vmmd wire boundary defaults to 8080 in that
		// case, so a transient PG hiccup never widens the failure
		// surface beyond the legacy behaviour.
		//
		// If Wake ever becomes customer-facing, denormalise port onto
		// the instances row at admit time and read it back alongside
		// the existing fields — that costs a migration + an extra
		// column on state.Instance + the RunningInstanceForApp query,
		// which is overkill for synth traffic.
		var port int
		// resolvedDeploymentID (issue #556 / PR-C) is the per-deployment
		// wake-fan-out target the gateway caches on Target so the
		// weighted picker routes subsequent requests to the right
		// bucket. Preference order:
		//
		//  1. Caller-supplied non-empty deploymentID wins — the gateway
		//     passed the deployment id it cached on Target; that
		//     wins over any concurrent redeploy. (Shadowing guard:
		//     do NOT name this local `deploymentID` — Go would silently
		//     rebind the parameter, and a regression that did so would
		//     drop the gateway's hint on the floor. Pin via TestEngineWake_HonorsCallerDeploymentID.)
		//  2. Otherwise resolve from LiveDeployment — legacy
		//     single-deployment behaviour, unchanged.
		//
		// The LiveDeployment lookup also feeds `port` (OverridePort);
		// when the caller passes a non-empty deploymentID we still
		// need the lookup unless port defaults are acceptable. vmmd
		// defaults to 8080 when port=0, so a transient lookup failure
		// here is benign; we surface it via slog and carry on.
		//
		// PR-B (issue #272): the LiveDeployment read is scope-aware.
		// A preview wake (scope="pr-{N}") MUST NOT route to the
		// parent's live deployment; it must consult
		// LiveDeploymentForScope so the preview gets the preview's
		// own deployment row. Empty scope falls through to the
		// legacy LiveDeployment (single-deployment app).
		resolvedDeploymentID := deploymentID
		var depErr error
		var dep state.Deployment
		if scope == "" {
			dep, depErr = e.store.LiveDeployment(ctx, appID)
		} else {
			dep, depErr = e.store.LiveDeploymentForScope(ctx, appID, scope)
		}
		if depErr == nil {
			port = dep.OverridePort
			if resolvedDeploymentID == "" {
				resolvedDeploymentID = dep.ID
			}
		} else {
			e.log.Warn("sched: wake: live deployment lookup for port/deployment_id failed; falling through with caller hint (or empty)",
				"app", appID, "caller_deployment_id", deploymentID, "scope", scope, "err", depErr)
		}
		release()
		// Surface the existing row's wake_id so a Phase-1 fast-path
		// response carries x-faas-wake-id just like a cold-wake
		// response would. The correlation handle is the wake that
		// brought the instance up; an operator tailing a warm request
		// can still pin it back to the schedd slog line that stamped
		// it (gaps analysis 2026-07-23 review finding #1).
		return WakeResult{InstanceID: ins.ID, NodeID: ins.NodeID, Method: vmmdpb.WakeMethod_WAKE_RESTORE, WakeID: ins.WakeID, Port: port, DeploymentID: resolvedDeploymentID, RequestCount: ins.RequestCount}, nil
	} else if err != nil && !errors.Is(err, state.ErrNotFound) {
		release()
		return WakeResult{}, fmt.Errorf("sched: wake: running lookup: %w", err)
	}
	release()
	// Wake preserves the legacy contract: a ledger refusal surfaces
	// as *api.Problem{Code: CodePlanLimitConcur}. The ledger's
	// capacity refusal happens INSIDE admitAndDispatch; we forward
	// rather than lift into the typed AtCapacity result.
	return e.admitAndDispatch(ctx, appID, trigger, false)
}

// requestedWakeIDKey carries an API-minted wake correlation id through the
// restart notification. Ordinary wakes continue to mint their UUIDv7 inside
// the admission window; restarts use this key so the 202 response can expose
// the same id that schedd stamps on the new instance and wake events.
type requestedWakeIDKey struct{}

type restartCall struct {
	done chan struct{}
	out  CoordOutcome
	err  error
}

func withRequestedWakeID(ctx context.Context, wakeID string) context.Context {
	if wakeID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestedWakeIDKey{}, wakeID)
}

func requestedWakeID(ctx context.Context) string {
	wakeID, _ := ctx.Value(requestedWakeIDKey{}).(string)
	return wakeID
}

// RestartApp parks every live instance for an app, then ensures one fresh
// instance is awake from the snapshot just captured. The app-level lock
// serializes the park phase with normal wake/reaper work; EnsureWake provides
// the existing per-app single-flight and wake-rate-limit behavior for the
// replacement instance.
//
// This is invoked by schedd after apid emits app_changed{kind:"restart"}.
// Apid writes the initial park intent; schedd owns instance transitions and
// snapshot/VM operations, then reactivates the app before the replacement wake.
func (e *Engine) RestartApp(ctx context.Context, appID, wakeID string) (out CoordOutcome, err error) {
	if e == nil || appID == "" {
		return CoordOutcome{}, fmt.Errorf("sched: restart app: empty app id")
	}
	// app_changed is delivered through pg_notify, so retries and duplicate
	// notifications are expected. Coalesce them before doing any VM work.
	e.restartMu.Lock()
	if e.restartInFlight == nil {
		e.restartInFlight = make(map[string]*restartCall)
	}
	if e.restartCompleted == nil {
		e.restartCompleted = make(map[string]string)
	}
	if wakeID != "" && e.restartCompleted[appID] == wakeID {
		e.restartMu.Unlock()
		return CoordOutcome{}, nil
	}
	if call, ok := e.restartInFlight[appID]; ok {
		e.restartMu.Unlock()
		select {
		case <-call.done:
			return call.out, call.err
		case <-ctx.Done():
			return CoordOutcome{}, ctx.Err()
		}
	}
	call := &restartCall{done: make(chan struct{})}
	e.restartInFlight[appID] = call
	e.restartMu.Unlock()
	defer func() {
		e.restartMu.Lock()
		call.out, call.err = out, err
		if err == nil && out.Instance != nil && wakeID != "" {
			e.restartCompleted[appID] = out.Instance.WakeID
		}
		close(call.done)
		delete(e.restartInFlight, appID)
		e.restartMu.Unlock()
	}()

	// The owner gate mirrors EnsureWake/ParkApp in split-node deployments.
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return CoordOutcome{}, fmt.Errorf("sched: restart app: load app %s: %w", appID, err)
	}
	if e.ownerNodeID != "" && app.NodeID != "" && app.NodeID != e.ownerNodeID {
		return CoordOutcome{}, nil
	}
	if app.Status != state.AppActive && app.Status != state.AppEvictedCold {
		// Deleted or otherwise non-live apps cannot be restarted.
		return CoordOutcome{}, nil
	}

	release := e.lockApp(appID)
	app, err = e.store.AppByID(ctx, appID)
	if err != nil {
		release()
		return CoordOutcome{}, fmt.Errorf("sched: restart app: reload app %s: %w", appID, err)
	}
	if app.Status == state.AppActive {
		parked := state.AppEvictedCold
		if _, err := e.store.UpdateApp(ctx, appID, state.UpdateAppParams{Status: &parked}); err != nil {
			release()
			return CoordOutcome{}, fmt.Errorf("sched: restart app: park app %s: %w", appID, err)
		}
	} else if app.Status != state.AppEvictedCold {
		release()
		return CoordOutcome{}, nil
	}
	instances, err := e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		release()
		return CoordOutcome{}, fmt.Errorf("sched: restart app: list instances %s: %w", appID, err)
	}
	for _, candidate := range instances {
		fresh, readErr := e.store.InstanceByID(ctx, candidate.ID)
		if readErr != nil {
			if errors.Is(readErr, state.ErrNotFound) {
				continue
			}
			release()
			return CoordOutcome{}, fmt.Errorf("sched: restart app: reload instance %s: %w", candidate.ID, readErr)
		}
		switch state.State(fresh.State) {
		case state.StateRunning:
			if parkErr := e.snapshotAndPark(ctx, fresh); parkErr != nil {
				release()
				return CoordOutcome{}, fmt.Errorf("sched: restart app: park instance %s: %w", fresh.ID, parkErr)
			}
		case state.StateWaking, state.StateColdBooting:
			if destroyErr := e.timedDestroy(ctx, fresh.NodeID, fresh.ID, DestroyTimeout); destroyErr != nil {
				release()
				return CoordOutcome{}, fmt.Errorf("sched: restart app: destroy instance %s: %w", fresh.ID, destroyErr)
			}
			e.ledger.Release(fresh.ID)
			e.transition(ctx, fresh.ID, fresh.AppID, state.StateStopped)
		}
	}
	active := state.AppActive
	if _, err := e.store.UpdateApp(ctx, appID, state.UpdateAppParams{Status: &active}); err != nil {
		release()
		return CoordOutcome{}, fmt.Errorf("sched: restart app: reactivate app %s: %w", appID, err)
	}
	release()

	out, err = e.EnsureWake(withRequestedWakeID(ctx, wakeID), appID, TriggerAppRestart)
	if err == nil && out.Instance != nil && e.audit != nil {
		e.audit.Emit(ctx, "app.restarted", &app.AccountID, map[string]any{
			"app_id":  appID,
			"slug":    app.Slug,
			"wake_id": out.Instance.WakeID,
		})
	}
	return out, err
}

func (e *Engine) wakeInstanceModeMatchesApp(ctx context.Context, appID string, ins state.Instance) bool {
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		e.log.Warn("sched: wake: app lookup for instance mode failed; preserving fast path", "app", appID, "instance", ins.ID, "err", err)
		return true
	}
	if instanceModeMatchesApp(app, ins) {
		return true
	}
	e.log.Info("sched: wake: running instance mode does not match app; reconciling", "app", appID, "instance", ins.ID, "instance_mode", normalizedInstanceMode(ins.Mode), "app_mode", instanceModeForApp(app))
	return false
}

// EnsureWake (ADR-098) is the coordinated wake entry point.
// Every wake producer (gateway, cron, floor, scaleup, targets) routes
// through this method. Calls coalesce while existing and in-flight instances
// have capacity; cold bursts fan out only when queued demand exceeds it.
//
// Three phases for the leader:
//
//  1. Reserve a slot in the wake coordinator (under wakeCoord.mu only).
//  2. Defer Complete() so all five completion paths in e.Wake
//     (engine.go:1435 ledger refusal, :1818 re-read failure, :1823
//     state-stolen, :1830-1831 record-runtime, :1892 commit) hand
//     their outcome to followers via one source of truth.
//  3. Run e.Wake. The deferred Complete fires when the function
//     returns, regardless of which path it took.
//
// Followers block on their assigned leader's Complete and inherit its outcome.
//
// Lock discipline: wakeCoord.mu is acquired and released BEFORE
// e.lockApp(appID) is touched. The defer-close pattern keeps the
// decrement site count at one — no hand-placed release at any of the
// five completion paths.
//
// Errors that surface to the caller:
//
//   - ErrQueueFull: per-app follower cap exceeded.
//   - ErrAppDeleted: the app was deleted while we were waiting.
//   - ctx.Err(): the caller's ctx was cancelled before the leader finished.
//   - leader's *api.Problem: ledger / chooser / store error.
//   - nil: the wake succeeded; Outcome.Instance is populated.
func (e *Engine) EnsureWake(ctx context.Context, appID, trigger string) (CoordOutcome, error) {
	if e == nil || e.wakeCoord == nil {
		return CoordOutcome{}, fmt.Errorf("sched: EnsureWake: engine not fully constructed")
	}
	// Multi-host safety cluster PR-5 / audit F4 (Layer 1 — owner gate):
	// refuse the wake if the app is owned by another schedd in the
	// fleet. This is the SAME check that lives in
	// choosePlacementLocked (engine.go:~2483), lifted earlier in the
	// pipeline so a foreign-owned app fails-fast at entry rather than
	// consuming a slot in the wakeCoord queue. The two layers (queue
	// refusal here + placement refusal in choosePlacementLocked) are
	// deliberately redundant — choosePlacementLocked still gates the
	// chooser so a stale app.NodeID can't slip through via a direct
	// choosePlacement call that bypasses EnsureWake.
	//
	// Empty ownerNodeID preserves the single-box dev path (the
	// synthetic default-local row has no NodeID constraint). Empty
	// app.NodeID is the legacy non-shared case (an app never pinned
	// to a node); the engine's chooser places it on the local box.
	if e.ownerNodeID != "" {
		app, err := e.store.AppByID(ctx, appID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return CoordOutcome{}, fmt.Errorf("sched: EnsureWake: load app %q: %w", appID, err)
		}
		if err == nil && app.NodeID != "" && app.NodeID != e.ownerNodeID {
			return CoordOutcome{}, fmt.Errorf(
				"sched: EnsureWake: app %q owned by node %q, this schedd owns %q — refusing (run on the owning box)",
				appID, app.NodeID, e.ownerNodeID,
			)
		}
	}
	call, isLeader, err := e.wakeCoord.Enter(appID, e.wakeFanoutFor(ctx, appID))
	if err != nil {
		return CoordOutcome{}, err
	}
	// Follower path: block on the leader's outcome. The leader's
	// Complete() is the single source of truth.
	if !isLeader {
		out := call.Await(ctx)
		e.wakeCoord.Release(appID, call)
		return out, nil
	}
	// Leader path: run e.Wake on a detached ctx bounded by the
	// coordinator's TTL so a cancelled triggering request cannot kill
	// the in-flight boot. The deferred Complete is the single
	// decrement site for all five completion paths inside e.Wake.
	//
	//nolint:contextcheck // leader's ensure deliberately detaches from the
	// caller's ctx via context.Background() + TTL — the wake must outlive
	// the triggering request so other queued waiters get the same instance.
	// This is the load-bearing coordinated-wake invariant (spec
	// §4.1, ADR-098 §Decision). Mirror of pkg/gateway/gate.go Wait
	// goroutine detach.
	leaderCtx, cancel := context.WithTimeout(context.Background(), e.wakeCoord.TTL())
	defer cancel()
	// The coordinator deliberately detaches from the triggering request, but
	// preserves the restart correlation id while doing so.
	leaderCtx = withRequestedWakeID(leaderCtx, requestedWakeID(ctx))
	out := CoordOutcome{}
	defer func() {
		call.Complete(out)
		e.wakeCoord.Release(appID, call)
	}()
	//nolint:contextcheck // leader's ensure deliberately detaches from the
	// caller's ctx via context.Background() + TTL — the wake must outlive
	// the triggering request so other queued waiters get the same instance.
	// This is the load-bearing coordinated-wake invariant (spec
	// §4.1, ADR-098 §Decision). Mirror of pkg/gateway/gate.go Wait
	// goroutine detach.
	res, err := e.Wake(leaderCtx, appID, "", "", trigger)
	if err != nil {
		out.Err = err
		return out, err
	}
	// AtCapacity (issue #168) — the engine admits benignly (no error)
	// but no instance was created. Returning an empty-but-non-nil
	// Instance would have the gRPC server emit a phantom 200 with
	// zero-valued fields; the gateway fast-path would treat that as
	// "wake succeeded with empty IDs" and re-attempt the same backend
	// query. Forward the typed sentinel so the gateway retries
	// against the existing live targets (per the AdmitInstance
	// AtCapacity contract).
	if res.AtCapacity {
		out.Err = ErrAtCapacity
		return out, ErrAtCapacity
	}
	out.Instance = &CoordInstance{
		InstanceID:   res.InstanceID,
		NodeID:       res.NodeID,
		DeploymentID: res.DeploymentID,
		WakeID:       res.WakeID,
		Port:         int32(res.Port),
		ColdBoot:     res.Method == vmmdpb.WakeMethod_WAKE_COLD_BOOT,
	}
	return out, nil
}

// AdmitInstance attempts to admit one additional instance for appID,
// bypassing the Phase 1 "return newest RUNNING" shortcut. Returns
// WakeResult{AtCapacity: true} when the app is already at effective
// max_concurrency (issue #168); the gateway treats this as a benign
// no-op when it already has at least one cached target. Other
// admission failures (RAM headroom, chooser error) keep the existing
// FAILED-row shape and surface as *api.Problem.
//
// Phase 2-4 are shared with Wake via admitAndDispatch; the only
// behavioural difference is the missing Phase 1 fast-path so a
// second/third/... capacity slot can be opened on demand, plus the
// liftCapacityToResult=true flag that turns a CodePlanLimitConcur
// ledger refusal into the typed AtCapacity result.
// AdmitInstance is the schedule scale-out primitive (issue #168).
// Bypasses the Phase-1 fast-path so a gateway can demand a new
// instance even when others are already RUNNING. Returns a typed
// AtCapacity result on the benign "already at max_concurrency"
// outcome — see sched.WakeResult.AtCapacity.
//
// deploymentID (issue #556 / PR-C): the optional per-deployment
// wake hint for the wake-fan-out path. Empty falls through to
// the newest live deployment — the legacy single-deployment
// behaviour. Non-empty asks the engine to admit on that specific
// live deployment. Additive per ADR-016.
//
// scope (PR-B / issue #272): the preview scope ("pr-{N}") the
// gateway derived from the inbound Host header. Empty = prod
// (legacy single-deployment behaviour). Stamped on the ctx via
// WithScope so resolveApp / loadAPIEnv read the same value.
func (e *Engine) AdmitInstance(ctx context.Context, appID, deploymentID, scope, trigger string) (WakeResult, error) {
	ctx = WithScope(ctx, scope)
	if deploymentID == "" {
		return e.admitAndDispatch(ctx, appID, trigger, true)
	}
	return e.admitAndDispatchForDeployment(ctx, appID, deploymentID, string(state.InstanceModeNormal), true, trigger)
}

// scaleOutBurstContinuationKey marks the additional members of a single
// signal-driven burst. It is intentionally private: customer-facing wakes can
// never bypass the normal scale-out cooldown. Only AdmitInstances creates this
// marker after its first admission has passed every ordinary gate.
type scaleOutBurstContinuationKey struct{}

// burstPlacementSpreadKey marks sibling admissions that belong to one
// gateway burst. The first admission keeps the warm-affinity hint so it can
// reuse local snapshot/page-cache state; siblings must be allowed to choose
// the least-loaded compute node instead of repeatedly pinning to that hint.
type burstPlacementSpreadKey struct{}

func withScaleOutBurstContinuation(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, scaleOutBurstContinuationKey{}, true)
	// The existing gRPC BurstContinuation bit is the transport boundary for
	// sibling admissions. Carry placement spread with the same marker so a
	// remote schedd does not silently reintroduce warm-node pinning.
	return context.WithValue(ctx, burstPlacementSpreadKey{}, true)
}

// WithBurstContinuation marks a schedd admission as a continuation of a
// bounded, already-approved burst. The marker is intentionally narrow: it
// bypasses only the per-app scale-out cooldown; every continuation still
// goes through the normal ledger, placement, resource, and boot pipeline.
// The gRPC adapter uses this when carrying Engine.AdmitInstances semantics
// across the process boundary.
func WithBurstContinuation(ctx context.Context) context.Context {
	return withScaleOutBurstContinuation(ctx)
}

// IsBurstContinuation reports whether a schedd admission carries the bounded
// burst continuation marker. It is used by the gRPC boundary tests and by
// adapters that need to preserve the Engine.AdmitInstances contract without
// exposing the private context-key type.
func IsBurstContinuation(ctx context.Context) bool {
	return isScaleOutBurstContinuation(ctx)
}

func isScaleOutBurstContinuation(ctx context.Context) bool {
	value, _ := ctx.Value(scaleOutBurstContinuationKey{}).(bool)
	return value
}

// WithBurstPlacementSpread marks a bounded burst continuation so placement
// ignores sticky warm affinity for that admission. The marker does not bypass
// any capacity, quota, or scheduler gate. WithBurstContinuation also carries
// this marker so the existing gRPC continuation bit preserves the behavior
// across the gateway→schedd process boundary.
func WithBurstPlacementSpread(ctx context.Context) context.Context {
	return context.WithValue(ctx, burstPlacementSpreadKey{}, true)
}

func isBurstPlacementSpread(ctx context.Context) bool {
	value, _ := ctx.Value(burstPlacementSpreadKey{}).(bool)
	return value
}

// IsBurstPlacementSpread reports whether a scheduler admission is a sibling
// of a bounded gateway burst. It is exposed for gRPC adapters and boundary
// tests; the marker only changes warm-affinity preference, never capacity
// enforcement.
func IsBurstPlacementSpread(ctx context.Context) bool {
	return isBurstPlacementSpread(ctx)
}

// AdmitInstances admits a bounded desired-capacity burst for one app. The
// first admission uses the ordinary wake gates, including the customer's
// scale-out cooldown. Remaining admissions are marked as continuations of the
// same already-approved burst, so they do not serialize behind that cooldown.
// Every instance still passes through the normal per-node placement, ledger,
// rate-limit, attestation, and vmmd paths; the method only batches the trigger
// intent and never grants capacity beyond the ledger.
//
// The first admission is performed synchronously to establish the policy
// decision. Continuations run in parallel, bounded by
// api.ScaleUpMaxBurstPerTick, so a four-instance cold burst does not multiply
// its vmmd latency unnecessarily. An individual failure does not cancel its
// siblings; successful results are returned along with the first error so the
// trigger can retry the remaining desired capacity on its next tick.
func (e *Engine) AdmitInstances(ctx context.Context, appID, scope, trigger string, count int) ([]WakeResult, error) {
	if count <= 0 {
		return nil, nil
	}
	if count > api.ScaleUpMaxBurstPerTick {
		count = api.ScaleUpMaxBurstPerTick
	}
	first, err := e.AdmitInstance(ctx, appID, "", scope, trigger)
	if err != nil {
		return nil, err
	}
	results := []WakeResult{first}
	if first.AtCapacity || count == 1 {
		return results, nil
	}

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
		burstCtx = WithBurstPlacementSpread(withScaleOutBurstContinuation(ctx))
	)
	for i := 1; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, admitErr := e.AdmitInstance(burstCtx, appID, "", scope, trigger)
			mu.Lock()
			defer mu.Unlock()
			if admitErr != nil {
				if firstErr == nil {
					firstErr = admitErr
				}
				return
			}
			results = append(results, result)
		}()
	}
	wg.Wait()
	return results, firstErr
}

// AdmitMirrorInstance (issue #72 / ADR-133 / ADR-125 PR-A3) is
// the mirror-admission entry point the gateway's per-request
// dispatch goroutine calls after the source deployment's
// response has been returned to the customer. The flow:
//
//  1. admitAndDispatchForDeployment — the same wake path the
//     customer-facing trigger uses, with mode='mirror' stamped
//     on the new instances row via the helper's mode parameter
//     (PR-A3 code-review fix #6 — single INSERT, no INSERT+UPDATE
//     race window). The mode='mirror' flag is what tells
//     pkg/meter/sampler.go and pkg/sched/reaper.go to skip the
//     row: the sampler never bills the customer for the shadow
//     VM (skip on mode='mirror'), and the reaper never
//     idles-reaps because mirror VMs self-park on request
//     completion (skip on mode='mirror' — the reaper expects a
//     normal idle park, not the mirror goroutine's explicit
//     ParkInstance).
//
//  2. The per-rule concurrent-mirror-VM cap (PR-A3 code-review
//     fix #3) lives on the GATEWAY Handler, not here — see
//     pkg/gateway/handler.go::mirrorSlots + tryAcquireMirrorSlot.
//     The cap reflects "VMs in flight" through round-trip
//     complete (the goroutine releases on its own defer), not
//     "admit attempts". Holding the slot here would release
//     microseconds after the wake command is sent, well before
//     the mirror VM is done serving.
//
// scope: empty for A3 (mirror rules don't carry a preview scope
// — the source deployment's scope flows through the customer
// wake, not the mirror). trail: empty for A3 (the audit emit
// shape for mirror is a follow-on; A3 logs the dispatch via the
// gateway's structured-log call).
//
// Errors:
//
//   - any error from admitAndDispatchForDeployment — propagated
//     from the wake path (RAM headroom, store error, etc.).
//     The gateway wraps the relevant cap-at-max branch into
//     sched.ErrMirrorSlotAtCapacity when the slot is exhausted.
func (e *Engine) AdmitMirrorInstance(ctx context.Context, appID, mirrorRuleID, mirrorDeploymentID string) (WakeResult, error) {
	// PR-A3 code-review fix #3 — the per-rule concurrent-mirror-VM
	// cap lives on the GATEWAY Handler (mirrorSlots + tryAcquireMirrorSlot).
	// The cap reflects "VMs in flight" through round-trip complete
	// (the goroutine releases on its own defer), not "admit attempts".
	// Holding the slot here would release microseconds after the wake
	// command is sent, well before the mirror VM is done serving.
	_ = mirrorRuleID
	// PR-A3 code-review fix #6 — single INSERT with mode='mirror'.
	// admitAndDispatchForDeployment's mode parameter is threaded
	// straight into CreateInstanceWithMode, so the row is created
	// in one shot instead of INSERT mode='normal' then UPDATE to
	// mode='mirror' (the latter had a race window readable by
	// sampler / reaper between INSERT and UPDATE).
	return e.admitAndDispatchForDeployment(ctx, appID, mirrorDeploymentID, string(state.InstanceModeMirror), false, TriggerMirror)
}

// ErrMirrorSlotAtCapacity (issue #72 / ADR-133 / ADR-125 PR-A3) is the
// sentinel the gateway's dispatchMirror goroutine returns when
// the per-rule cap is reached. As of PR-A3 code-review fix #3,
// the slot lives on the GATEWAY Handler (mirrorSlots +
// tryAcquireMirrorSlot), not on this engine — schedd just stamps
// mode='mirror' on the new instances row. The gateway wraps this
// sentinel via fmt.Errorf("%w", sched.ErrMirrorSlotAtCapacity)
// when its per-rule counter is at cap. The dispatch goroutine
// translates this to a ledger row with status_diff=true + metric
// gateway_mirror_dispatched_total{result="cap_at_max"} and
// otherwise drops the request on the floor. NOT a real failure —
// the customer's source response was already returned; mirror
// is best-effort by design.
var ErrMirrorSlotAtCapacity = errors.New("sched: mirror slot at capacity")

// AdmitInstanceForDeployment is the floor-trigger entry point that
// admits a specific deployment (issue #557 closure / ADR-074).
// The signature differs from AdmitInstance by accepting an explicit
// deploymentID; the floor trigger's per-deployment sweep threads the
// deployment it wants woke. The wake path's per-request target is
// still resolved by admitAndDispatch's `resolveApp` (LiveDeployment),
// which guarantees the wake and the floor admit land on the same
// deployment id — passing an out-of-band id here would race the
// customer's next deploy.
//
// The empty-deploymentID case is the legacy AdmitInstance path: the
// caller falls through to AdmitInstance (which resolves the live
// deployment internally). Pass empty rather than resolving to
// LatestDeployment — the trigger must NOT silently wake a
// superseded deployment.
//
// scope (PR-B / issue #272): threaded through the same way as
// AdmitInstance; the floor trigger's scope is derived from the
// app it's reconciling and must match the per-app scope ledger so
// an over-cap prod app cannot drain a preview (or vice versa).
func (e *Engine) AdmitInstanceForDeployment(ctx context.Context, appID, deploymentID, scope, trigger string) (WakeResult, error) {
	ctx = WithScope(ctx, scope)
	if deploymentID == "" {
		// ADR-123: thread the trigger through to AdmitInstance so the
		// empty-deploymentID branch (which routes through
		// admitAndDispatch and emits BootStarted/BootCompleted) carries
		// the caller's closed-enum value. The deploymentID-set branch
		// below uses admitAndDispatchForDeployment, which carries the
		// explicit deployment through the complete boot lifecycle.
		return e.AdmitInstance(ctx, appID, "", scope, trigger)
	}
	return e.admitAndDispatchForDeployment(ctx, appID, deploymentID, string(state.InstanceModeNormal), true, trigger)
}

// admitAndDispatchForDeployment mirrors admitAndDispatch but threads
// a specific deploymentID through to the ledger. Resolution of the
// app + account + limits still happens via resolveApp; only the
// deployment is overridden. If the override deployment is no longer
// live (a newer deploy happened mid-tick), the call returns
// {AtCapacity: true} — the trigger's next sweep re-evaluates.
//
// P1A asymmetry note: this path (the floor trigger / fan-out /
// per-deployment wake) bypasses Engine.admitGate by design. The
// canonical per-app cap enforcement is NodeLedger.Admit
// (pkg/sched/admission.go:225-228), which fires inside
// ledger.Admit below. The PR-C invariant pinned at
// pkg/sched/invariants_property_test.go:68
// (TestProperty_EngineWake_RespectsMaxConcurrency) requires
// "ledger caps, not lock caps" — both admitAndDispatch (wake path,
// :1191) and admitAndDispatchForDeployment (this function) feed
// into the same ledger write. As a consequence, the per-deployment
// path cannot emit schedd_scale_up_decisions_total{outcome ∈
// {cooldown_held, min_floor_already, overage_cap_reached}} from
// the gate — only reject_at_cap surfaces, via the ledger's
// *api.Problem{Code: CodePlanLimitConcur} return. Documenting this
// here so a future reviewer doesn't try to "fix" it by routing the
// deployment path through the gate (which would require either
// collapsing the two cap-enforcement layers or duplicating the
// ledger write).
func (e *Engine) admitAndDispatchForDeployment(ctx context.Context, appID, deploymentID, mode string, liftCapacityToResult bool, trigger string) (WakeResult, error) {
	return e.admitAndDispatchWithOptions(ctx, appID, deploymentID, mode, trigger, liftCapacityToResult, true)
}

// admitAndDispatch is the shared Phase 2–4 body used by both Wake and
// AdmitInstance. It takes the per-app lock once for Phase 2, drops it
// across the slow vmmd RPC (Phase 3), and re-acquires for the
// post-boot commit (Phase 4). Callers must NOT hold appMu; the helper
// manages the lock itself.
//
// Distinct from Wake's Phase 1: AdmitInstance skips the "return newest
// RUNNING row" shortcut so each call either admits a new instance or
// returns AtCapacity=true. The Phase 1 shortcut is preserved on Wake
// by the wrapper above.
//
// liftCapacityToResult controls the admission-failure branch:
//
//   - true (AdmitInstance): a CodePlanLimitConcur ledger refusal
//     becomes WakeResult{AtCapacity: true}, nil. The unattached row
//     is deleted; no FAILED row is written. The gateway treats this
//     as a no-op when it already has ≥1 cached target.
//
//   - false (Wake): the same CodePlanLimitConcur refusal surfaces
//     as *api.Problem so the existing wake contract is preserved
//     bit-for-bit. The row falls back to the legacy "transition to
//     FAILED, return problem" path.
func (e *Engine) admitAndDispatch(ctx context.Context, appID, trigger string, liftCapacityToResult bool) (WakeResult, error) {
	return e.admitAndDispatchWithOptions(ctx, appID, "", string(state.InstanceModeNormal), trigger, liftCapacityToResult, false)
}

// admitAndDispatchWithOptions is the single admission-to-vmmd pipeline. The
// previous per-deployment helper stopped after inserting a COLD_BOOTING row
// and admitting it into the ledger, leaving the floor path with no Create*
// RPC and no Phase 4 commit. Explicit deployment callers still bypass the
// request wake gates as before, but now share the complete boot lifecycle.
func (e *Engine) admitAndDispatchWithOptions(ctx context.Context, appID, deploymentID, mode, trigger string, liftCapacityToResult, bypassGates bool) (WakeResult, error) {
	// ── Phase 2: admit window, under appMu ──────────────────
	release := e.lockApp(appID)
	app, acct, limits, dep, err := e.resolveApp(ctx, appID)
	if err != nil {
		release()
		return WakeResult{}, err
	}
	if deploymentID != "" {
		explicitDep, depErr := e.store.DeploymentByID(ctx, deploymentID)
		if depErr != nil {
			release()
			if errors.Is(depErr, state.ErrNotFound) {
				e.IncAtCapacity(appID, "admit")
				return WakeResult{AtCapacity: true}, nil
			}
			return WakeResult{}, fmt.Errorf("sched: resolve explicit deployment: %w", depErr)
		}
		if explicitDep.AppID != appID || explicitDep.Status != state.DeployLive {
			release()
			e.IncAtCapacity(appID, "admit")
			return WakeResult{AtCapacity: true}, nil
		}
		dep = explicitDep
	}
	if mode == "" || mode == string(state.InstanceModeNormal) {
		mode = instanceModeForApp(app)
	}

	// PR-D (issue #462): worker-class first-check. Mirrors
	// pkg/sched/reaper.go:170 (workers are reaper-exempt). A
	// worker-class app has no per-request traffic — a wake
	// request is the wrong primitive. The customer-facing surface
	// (apid gate) already rejects PATCH with target.metric=
	// concurrent_requests for a worker app; this gate is the
	// engine-side mirror. Lifts to WakeResult{AtCapacity: true}
	// so the existing AdmitInstance typed-capacity path is
	// reused — no new wakeOutcome, no new metric row. The check
	// fires BEFORE admitGate so the cooldown / reject / min-floor
	// branches are unreachable for worker-class apps.
	if !bypassGates && (app.WorkloadClass == state.WorkloadClassWorker ||
		mode == string(state.InstanceModeWorker) || mode == string(state.InstanceModeJob)) {
		release()
		e.IncAtCapacity(appID, "wake")
		return WakeResult{AtCapacity: true}, nil
	}

	// PR-0 (ADR-099): wake-rate-limit consult. The throttle lives at
	// the admission layer (here) rather than at the gateway edge
	// (pkg/gateway.Limiter) because the wake path also fires from
	// cron ticks and (post-PR-C) jobs dispatch — neither of which
	// crosses gatewayd-internal. Without this gate, a Scale customer
	// firing `--tasks 1000 --parallelism 100` on a parked app could
	// trigger 100 cold-boots in a single 1 s tick and OOM the control
	// plane (ADR-099 Risk #1; same primitive serves ADR-080 Risk #1).
	//
	// The check fires AFTER the worker-class short-circuit so a
	// worker app's wake budget is not consumed by a request the
	// engine would have rejected anyway, and BEFORE admitGate so a
	// throttled wake neither burns the cooldown clock nor writes an
	// unattached instance row. Branch shape: lifts to
	// WakeResult{AtCapacity: true} so the gateway treats it as a
	// no-op when it already has ≥1 cached target (the existing
	// AdmitInstance typed-capacity path).
	//
	// Per-plan ceiling: api.Limits.WakeBurstPerApp / WakeBurstPerAccount
	// (Free 1/1, Hobby 5/10, Pro 20/30, Scale 100/150 per minute).
	// Fail-closed on unknown plan — mirrors pkg/gateway.Limiter.Allow.
	if !bypassGates && e.wakeLimiter != nil {
		if !e.wakeLimiter.AllowWakeApp(appID, acct.Plan) || !e.wakeLimiter.AllowWakeAccount(string(acct.ID), acct.Plan) {
			release()
			e.IncAtCapacity(appID, "rate_limit")
			return WakeResult{AtCapacity: true}, nil
		}
	}

	// PR-C (issue #462): wake-gate consult. Three short-circuit
	// outcomes route to typed errors or AtCapacity=true without
	// touching the ledger or the instances table. The cooldown
	// branch carries the customer's "rate-limit scale-outs" knob;
	// the cold-start discriminator (concurrency > 0) keeps a
	// request-driven wake from being deferred on a freshly-stamped
	// apps.last_scale_out_at column. See admitGate's doc for the
	// branch-by-branch outcome semantics.
	//
	// Wake vs AdmitInstance routing: AdmitInstance (liftCapacityToResult=true)
	// turns the per-app cap rejection into WakeResult{AtCapacity: true},
	// nil so the gateway treats it as a no-op when it already has ≥1
	// cached target. Wake (liftCapacityToResult=false) preserves the
	// existing *api.Problem{Code: CodePlanLimitConcur} wire shape so
	// the gateway's existing 429 handling is unchanged.
	//
	// PR-D (issue #462): splits the cooldown_held branch onto
	// CodeWaitForWarm (503 + Retry-After). The wakeMinFloorAlready
	// branch stays on CodePlanLimitConcur (429) — no scale-out
	// was attempted, the customer's request is asking for a wake
	// that the floor already satisfies.
	var (
		outcome     wakeOutcome
		obsCents    int64
		capCents    int64
		concurrency int
		atCapacity  bool
	)
	if bypassGates {
		concurrency = e.ledger.Concurrency(app.ID)
	} else {
		outcome, obsCents, capCents, concurrency, atCapacity = e.admitGate(ctx, &app, limits)
	}
	if outcome != wakeAdmit {
		release()
		switch outcome {
		case wakeRejectAtCap:
			if liftCapacityToResult {
				e.IncAtCapacity(appID, "wake")
				return WakeResult{AtCapacity: true}, nil
			}
			return WakeResult{}, api.ErrPlanLimitConcurrency(limits, e.ledger.Concurrency(app.ID))
		case wakeCooldownHeld:
			// PR-D: 503 + Retry-After with the cooldown remaining
			// seconds. The customer's plan is fine; their
			// ScaleOutCooldownS is holding the wake. Retry-After is
			// the canonical UX — clients can back off without
			// polling the body alone.
			return WakeResult{}, api.ErrWaitForWarm(
				cooldownSRemaining(&app, time.Now()),
				limits,
				e.ledger.Concurrency(app.ID),
			)
		case wakeMinFloorAlready:
			// No scale-out was attempted (concurrency already
			// at the floor). 429 is the right wire shape — the
			// customer is asking for a wake that the floor already
			// satisfies. PR-D keeps CodePlanLimitConcur here.
			return WakeResult{}, api.ErrPlanLimitConcurrency(limits, e.ledger.Concurrency(app.ID))
		case wakeOverageCapReached:
			// Issue #561: customer's spend cap is at/over the
			// configured monthly ceiling. Lift to
			// CodeAdmissionRefused (HTTP 402; no Retry-After —
			// the cap is a deliberate budget, not back-pressure).
			// AdmitInstance path returns AtCapacity=true so the
			// gateway treats it as a benign no-op when it already
			// has cached targets — the cap is account-scoped, so
			// one over-cap app refusing still leaves other
			// under-cap apps able to admit.
			//
			// obsCents + capCents ride via the gate's return
			// tuple (not Engine state): the lock has been
			// released, so reading them off the Engine would be
			// a -race surface against the next goroutine that
			// hits the gate on the same account.
			if liftCapacityToResult {
				e.IncAtCapacity(appID, "wake")
				return WakeResult{AtCapacity: true}, nil
			}
			return WakeResult{}, api.ErrAdmissionRefused(obsCents, capCents)
		}
	}

	// Mint the per-wake-attempt correlation handle (gaps analysis
	// 2026-07-23). UUIDv7 is time-ordered so the dashboard's "recent
	// wakes for this app" scan can use the partial index
	// (instances_wake_id_app_idx) without a separate sort. UUIDv7
	// also bakes the unix-ms timestamp into the first 48 bits, which
	// makes operator log scans human-friendly. Minted HERE under the
	// lock so the value threads cleanly through every code path that
	// runs under appMu (Phase 2 INSERT, the bootInput bundle used by
	// Phase 3 / Phase 4, and the final WakeResult). uuid.NewV7
	// returns (uuid.UUID, error); crypto/rand failure is impossible
	// in practice but the code carries the surface — on the
	// essentially-zero error path we fall back to a v4 so a wake is
	// never refused for ID-generation reasons.
	wakeID := requestedWakeID(ctx)
	if _, parseErr := uuid.Parse(wakeID); parseErr != nil {
		wakeID = ""
	}
	if wakeID == "" {
		wakeUUID, err := uuid.NewV7()
		if err != nil {
			// crypto/rand failure should be impossible in practice but
			// the surface exists; fall back to v4 so a wake is never
			// refused for ID-generation reasons. v4 breaks the
			// time-ordering invariant the partial index is built on, so
			// log + counter (review finding #6, gaps analysis
			// 2026-07-23). Any non-zero rate is an alertable condition.
			wakeUUID = uuid.New()
			if e.ops != nil {
				e.ops.WakeIDV4Fallback().Inc()
			}
			e.log.Warn("wake: uuid.NewV7 failed, fell back to v4 — partial index time-ordering broken",
				"app", appID, "err", err)
		}
		wakeID = wakeUUID.String()
	}

	// Consult the per-deployment snapshot-miss backoff before touching the
	// snapshot cache. Repeated cache misses must be visible as bounded 503s;
	// silently forcing another cold boot defeats the backoff and can exhaust
	// the node's RAM/capacity under a hot request loop.
	backoff, backoffActive, backoffErr := e.store.DeploymentSnapshotBackoffActive(ctx, dep.ID)
	if backoffErr != nil {
		e.log.Warn("wake: snapshot backoff gate lookup failed; proceeding without gate", "deployment_id", dep.ID, "err", backoffErr)
	} else if backoffActive && !bypassGates {
		if e.ops != nil {
			e.ops.WakeSnapshotTier("cold_boot_fallback").Inc()
			e.ops.SnapshotBackoffGateOutcome("gated").Inc()
		}
		release()
		return WakeResult{}, api.ErrSnapshotBackoff(snapshotBackoffRetryAfter(backoff.SnapshotMissBackoffUntil))
	}

	// Restore iff a fresh, version-matched snapshot exists; else cold boot
	// (ADR-005: cold boot always works, snapshot is cache). The plan
	// gate (issue #470 / PR A / ADR-055) is consulted here: a
	// Free/Hobby account skips the warm tier and reads the init row
	// directly. The warm row is still on disk for the customer's
	// eventual re-upgrade (sticky-on-downgrade, ADR-055 §5).
	//
	// Issue #470 / PR C / ADR-074: the third return value is the
	// chosen tier ∈ {warm, init, cold_boot_fallback}. Increment the
	// wake-tier-mix counter so the dashboard shows the ratio of warm
	// restores vs init restores vs cold-boot fallbacks. nil-safe
	// accessor (OpsMetrics = nil → no-op).
	snap, haveSnap, chosenTier := e.usableSnapshotForWake(ctx, dep.ID, string(acct.Plan))
	if !haveSnap {
		// Only a deployment that has had a snapshot can be said to have
		// missed one. This avoids starting the exponential backoff on a
		// brand-new cold deployment that never had a cache entry.
		if hasHistory, historyErr := e.store.HasSnapshotHistory(ctx, dep.ID); historyErr != nil {
			e.log.Warn("wake: snapshot history lookup failed", "deployment_id", dep.ID, "err", historyErr)
		} else if hasHistory {
			_, _, _ = e.RecordSnapshotMiss(ctx, dep.ID, backoff.SnapshotMissCount)
		}
	}
	if e.ops != nil {
		e.ops.WakeSnapshotTier(chosenTier).Inc()
		if backoffActive {
			e.ops.SnapshotBackoffGateOutcome("miss").Inc()
		}
	}

	initState := state.StateColdBooting
	if haveSnap {
		initState = state.StateWaking
	}

	// Multi-node placement (issue #97 / ADR-025 axis 3): pick the
	// compute_node that has the most free headroom and still fits
	// this wake. Single-box fleets degenerate to "always
	// default-local" because the synthetic row carries the legacy
	// 47,600 MB ceiling and there's no other active node to win
	// the tie-break. The chooser is invoked under appMu so a
	// concurrent wake for the same app sees a coherent (fleet,
	// per-node used_mb) view.
	//
	// Sticky-warm affinity (placement scheduler PR, ADR-025): the
	// WarmAffinity hint is read here so a hot app's snapshot + page
	// cache stay warm across reaper cycles (ADR-009). The hint is
	// bias, never a gate — the chooser falls through to
	// least-loaded when the preferred node is saturated. ADR-005
	// (cold boot must always work) is preserved: an empty hint
	// behaves identically to a fresh install.
	var warmHint string
	if !isBurstPlacementSpread(ctx) {
		warmHint, _ = e.warmAffinity.LastWarmNode(appID)
	}
	var snapshotNodes []string
	if haveSnap {
		warmHint, snapshotNodes = e.snapshotPlacementHints(ctx, snap.ID, warmHint)
	}
	// ADR-098 PR-D: connection-aware placement bias. Score is
	// the synchronous read (per ADR §D2 — schedd does NOT
	// LISTEN on data_upstreams_changed). On cache miss, Refresh
	// fires synchronously to populate the entry; on hit, the
	// cached preferredRegion flows through. ok=false → empty
	// PreferredRegion → legacy tie-break. The refresh error is
	// logged + swallowed so a transient DB hiccup doesn't break
	// the legacy chooser.
	//
	// ADR-098 amendment (issue #954): the cache key widens to
	// (appID, deploymentScope) so each deployment reads its own
	// probe bias — staging and prod stay independent. dep.ID
	// is already in scope here; the empty-string fallback at
	// appDeploymentKeyOf covers the cold-path branch where dep
	// is nil (legacy callers).
	upstreamAffinity := e.UpstreamAffinity()
	preferredRegion, _, _ := upstreamAffinity.Score(appID, dep.ID)
	if preferredRegion == "" && upstreamAffinity != nil {
		if rerr := upstreamAffinity.Refresh(ctx, acct.ID, appID, dep.ID); rerr == nil {
			preferredRegion, _, _ = upstreamAffinity.Score(appID, dep.ID)
		} else {
			// best-effort: log at debug, fall through to legacy
			e.log.Debug("upstream affinity refresh failed; using legacy chooser", "app", appID, "err", rerr)
		}
	}
	placement, err := e.choosePlacementLocked(ctx, Request{
		AppID: appID, Plan: acct.Plan,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency,
		PreferredNodeID:  warmHint,
		PreferredNodeIDs: snapshotNodes,
		PreferredRegion:  preferredRegion,
	})
	if err != nil {
		release()
		return WakeResult{}, err // *api.Problem from chooser
	}
	ins, err := e.store.CreateInstanceWithMode(ctx, appID, dep.ID, string(initState), app.RAMMB, placement.NodeID, wakeID, mode)
	if err != nil {
		release()
		return WakeResult{}, fmt.Errorf("sched: wake: create instance: %w", err)
	}
	e.emitInstanceChanged(ctx, ins.ID, appID, initState, wakeID)

	if err := e.ledger.Admit(Request{
		Instance: ins.ID, AppID: appID, DeploymentID: dep.ID, Plan: acct.Plan,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency,
		NodeID:        placement.NodeID,
		NodeCeilingMB: placement.CeilingMB,
		VCPUBudget:    placement.VCPUBudget,
	}); err != nil {
		// Admit failed (capacity / concurrency). The two rejection
		// modes differ in how loudly the engine surfaces them:
		//
		//   - CodePlanLimitConcur (typed capacity): the app is already
		//     at effective max_concurrency. This is the benign
		//     "app_concurrency_reached" outcome AdmitInstance is
		//     designed to ask for; we delete the row and return
		//     AtCapacity=true so the gateway treats it as a no-op
		//     when it already has ≥1 cached target. Issue #168.
		//
		//   - any other *api.Problem (RAM headroom → CodeCapacity, etc):
		//     a real platform failure. Lock the row to FAILED so a
		//     concurrent reader sees a coherent final state, not an
		//     unattached reservation; transitionWithKind records it
		//     as a wake_boot_error rather than a generic state_transition.
		//
		// Wake's existing behaviour is preserved exactly: a Wake that
		// hits CodePlanLimitConcur still returns the *api.Problem
		// (the FastPath's healthy count check should make this
		// unreachable on the Wake path, but the contract is unchanged).
		var prob *api.Problem
		if liftCapacityToResult && errors.As(err, &prob) && prob.Code == api.CodePlanLimitConcur {
			// AdmitInstance asks for one more slot; the ledger says
			// we're already at the cap. Roll the row back without
			// writing FAILED (the row never had a reservation
			// attached — Admit's failure branch never inserted one).
			if delErr := e.store.DeleteInstance(ctx, ins.ID); delErr != nil {
				e.log.Warn("admit: delete unattached row after concurrency cap",
					"app", appID, "instance", ins.ID, "err", delErr)
			}
			release()
			e.IncAtCapacity(appID, "wake")
			return WakeResult{AtCapacity: true}, nil
		}
		e.transitionWithKind(ctx, ins.ID, appID, state.StateFailed, "wake_boot_error", "admit_denied")
		release()
		return WakeResult{}, err // *api.Problem
	}

	// Sticky-warm record: stamp the chosen node so the NEXT wake
	// for this app picks it back up. This happens only after the
	// instance row exists and the ledger has admitted it; a placement
	// rejection must not leave a false warm hint behind.
	//
	// Push-side fanout (ADR-025 axis 4): if the new entry actually
	// changed appID's warm node, broadcast a WarmHintEvent to every
	// gatewayd-internal subscribed via Engine.StreamWarmHints. Same
	// per-app lock guards the cache write + the emit so the gRPC stream
	// observes writes in the same order the cache does. nil
	// broadcaster is a no-op (the test-only path that constructs
	// Engine without NewEngine's eager init).
	_, changed := e.warmAffinity.RecordWakeIfChanged(appID, placement.NodeID)
	if changed && e.warmBroadcaster != nil {
		e.warmBroadcaster.emit(WarmHintEvent{
			AppID:     appID,
			NodeID:    placement.NodeID,
			WrittenAt: time.Now(),
		})
	}

	// AppSpec is built under the lock and treated as immutable below.
	// The boot call uses the same spec — the vmmd side reads it
	// thread-safely without us touching it again.
	// Issue #96 / ADR-025 axis 2 / PR #116: the wake wire carries
	// StorageBackend keys for the base + layer ext4. vmmd resolves
	// them locally via Storage.Get before staging the chroot. The
	// local backend's Get maps the same keys to the same files the
	// legacy *_path fields used, so single-box behaviour is
	// preserved. See pkg/sched/paths.go baseKey / layerKey.
	//
	// PR-B (issue #460 / ADR-053 §Decision 1): env_secrets override
	// filtering happens here, on the wake path. dep.OverrideEnvSecrets
	// (a jsonb blob) is the per-deployment allowlist; pre-PR-B
	// deployments without override columns get the legacy "stage
	// everything for the app" behaviour so tarball/dockerfile paths
	// keep working unchanged.
	//
	// PR-C (issue #462): stamp apps.last_scale_out_at = now() on
	// every successful wake admit. Best-effort: a stamp failure
	// logs a warning but does NOT roll back the wake — the wake
	// is committed and the next cycle repopulates the stamp. The
	// "stamp miss" direction (stamp UPDATEs after the instance
	// INSERT, and a rare concurrent wake sees NULL on the consult)
	// is the SAFE direction: the wake-gate admitGate consults the
	// stamp BEFORE the insert and bypasses cooldown on NULL.
	if !bypassGates {
		if err := e.store.StampAppScaleOut(ctx, appID); err != nil {
			e.log.Warn("sched: stamp apps.last_scale_out_at failed", "app", appID, "err", err)
		}
	}

	// issue #517 / PR-C / ADR-064 — emit wake.queue_accepted at
	// the Phase 2 admission gate boundary, BEFORE the structured
	// admit timestamp. The customer-facing timeline order is
	// queue_accepted → admitted → boot_started → boot_completed
	// (a wake is "queued" when it passes the lock-window
	// admission, "admitted" when the ledger reservation is
	// confirmed). emit is best-effort and never rolls back the
	// admit. nil opts out (pre-PR-C fixtures).
	var requestID string
	if e.events != nil {
		if fields, ok := wire.FromContext(ctx); ok {
			requestID = fields.RequestID
		}
	}
	if e.events != nil {
		e.events.Emit(ctx, events.QueueAccepted{
			EmitAt:    time.Now().UTC(),
			WakeID:    wakeID,
			AppID:     appID,
			RequestID: requestID,
		})
	}

	// issue #517 / PR-C / ADR-064 — emit wake.admitted on the
	// successful ledger admit. Pairs with wake.queue_accepted and
	// wake.boot_started under the same wake_id so the customer
	// timeline surfaces "queue → admit → boot" as three distinct
	// timestamps. emit is best-effort: a failure is Warn + counter
	// and never rolls back the admit. Subject is the account so a
	// GDPR export can isolate per-account wake timelines.
	if e.events != nil {
		now := time.Now().UTC()
		e.events.Emit(ctx, events.Admitted{
			EmitAt:    now,
			WakeID:    wakeID,
			AppID:     appID,
			RequestID: requestID,
			AccountID: acct.ID,
			Plan:      string(acct.Plan),
		})
	}

	sealedEnv, err := e.loadSealedEnvFor(ctx, acct.ID, appID, dep.Scope, envSecretsFromDep(dep))
	if err != nil {
		e.rollbackAdmittedInstance(ctx, ins.ID, appID, "wake_sealed_env_invalid")
		return WakeResult{}, fmt.Errorf("sched: wake: load sealed env: %w", err)
	}
	sidecars, err := e.sidecarsForDeployment(ctx, dep)
	if err != nil {
		e.rollbackAdmittedInstance(ctx, ins.ID, appID, "wake_sidecars_invalid")
		return WakeResult{}, fmt.Errorf("sched: wake: load sidecars: %w", err)
	}
	spec := AppSpec{
		BaseKey: baseKey(app.Runtime), LayerKey: layerKey(dep.RootfsKey, dep.ID),
		VCPUCount: int32(limits.VCPU), MemSizeMiB: int32(app.RAMMB), CPUMillicores: int32(app.CPUMillicores),
		EgressMbit: int32(limits.EgressMbit),
		// M-3: resolve the optional app override against the account's
		// plan before crossing the scheduler/vmmd boundary.
		StartupDeadlineS: startupDeadlineForApp(app, acct.Plan),
		Plan:             acct.Plan, AccountID: acct.ID,
		AppID: appID, DeploymentID: dep.ID,
		SealedEnv: sealedEnv,
		Sidecars:  sidecars,
		// Issue #395 / ADR-045: plaintext api_env layer mirrors the
		// sealed secrets surface but stores non-sensitive runtime
		// config. Precedence at the guest layer is "secrets >
		// api_env > manifest_env > os.environ".
		APIEnv: e.loadAPIEnv(ctx, acct.ID, appID, dep.Scope),
		// ADR-031: surface the per-app egress allowlist on the
		// wake wire. vmmd translates the CIDRs into the per-netns
		// forward chain. Empty slice = no allowlist rule (current
		// behaviour); the apps_changed pg_notify handler at the
		// top of Wake re-reads the app row under a fresh ledger
		// lock, so a PATCH that lands between two wakes takes
		// effect on the next wake. Live instances keep their
		// old netns — same contract as RAMMB and MaxConcurrency.
		EgressAllowlist: prefixesToCIDRStrings(app.EgressAllowlist),
		// ADR-119: customer-supplied static egress IPv4
		// (BYOIP, Scale-only). Empty = no static pin
		// (default behaviour preserved). vmmd sets
		// netns.Config.AccountStaticIP from this value so
		// the per-netns renderer emits a sibling SNAT rule.
		// Plan-gated upstream; the apps_static_egress_ip_key
		// partial unique index (migration 00325) defends at
		// the DB layer. Live instances of the same app keep
		// their old netns — the app_changed pg_notify path
		// fires UpdateStaticEgressIP gRPC to patch them
		// (pkg/sched/egress_drift.go).
		StaticEgressIP: staticEgressIPString(app.StaticEgressIP),
		// Issue #460 / ADR-053 (PR-C): per-deployment override
		// port the customer's app binds inside the guest. 0 =
		// legacy 8080 (vmmd's wire-level default). The host's
		// waitReady + DNAT stay fixed on 8080 (ADR-009 +
		// guest/init/portnorm_linux.go); only vmmd's ForwardHTTP
		// bridge uses this port to dial the guest.
		Port: dep.OverridePort,
		// Issue #460 / ADR-053, ADR-057 / PR-D: per-deployment
		// override readiness probe path. Empty = legacy TCP-accept
		// on :8080 (pre-PR-D default). Non-empty → vmmd's
		// waitReady does HTTP GET <HealthcheckPath> against
		// <HostIP>:8080 and accepts 2xx as ready. The host probe
		// target is always :8080 — ADR-009 + portnorm re-expose the
		// customer bind on :8080 inside the guest, so the path is
		// the customer's choice and the port is the host's choice.
		// Mirror of `Port` above: empty OverrideHealthcheck
		// (legacy / no-override) → empty path → legacy probe.
		HealthcheckPath: healthcheckPathFromDep(dep),
		// Issue #470 / PR #470-FU-B: per-deployment runner id
		// (e.g. "node22"). Threaded onto the vmmd AppSpec so
		// the framework_ready DGRAM receipt path can label
		// vmmd_guest_framework_warmup_seconds by runner. See
		// buildAppSpec (engine.go:1757) for the same field
		// wired on the (re)build path. Empty falls back to
		// "unknown" in the histogram observer.
		Runtime: app.Runtime,
	}

	// Capture the boot inputs we need across the unlocked window. These
	// are values (not references) — they remain valid after release.
	// startedAt stamps the wake's accept moment (issue #517 / PR-C /
	// ADR-064) so the boot_started emit + boot_completed emit pair
	// can compute the boot span under the same wake_id. Hoisted
	// here so the timestamp is the same one the boot_started row
	// uses (RequestedAt), and the watchdog's "started_at" anchor
	// stays consistent.
	startedAt := time.Now().UTC()
	bootInput := bootInput{
		insID:     ins.ID,
		appID:     appID,
		depID:     dep.ID,
		initState: initState,
		haveSnap:  haveSnap,
		snapID:    snap.ID,
		snapVer:   snap.FCVersion,
		// #96: snap row's canonical StorageBackend key. F-1 on
		// CreateSnapshot guarantees non-empty; an empty value here
		// means a buggy inserter slipped a row past the contract and
		// Phase 3 will fall back to cold boot.
		snapKey: snap.StorageKey,
		// nodeID is the chosen compute_node from Phase 2. Phase 3
		// threads it through every vmmd RPC so the router dials
		// the right per-target client.
		nodeID: placement.NodeID,
		spec:   spec,
		// wakeID is the per-wake-attempt correlation handle (gaps
		// analysis 2026-07-23). Carried across the unlocked Phase 3
		// window so the vmmd-failure log path, the state-stolen abort
		// path, and the Phase 4 commit's WakeResult all surface the
		// same value. The row already carries wake_id (CreateInstance
		// stamped it); this is the value the caller observes.
		wakeID:    wakeID,
		startedAt: startedAt,
		// ADR-123: stamp trigger / queued_count / concurrency_at_admit
		// on the bootInput bundle so both the BootStarted and
		// BootCompleted events emit the same snapshot. `concurrency`
		// is the ledger.Concurrency reading returned by admitGate
		// (single read under lock); queuedCount re-reads the same
		// value for wire-shape clarity. trigger is the closed-enum
		// value the caller (Engine.Wake / EnsureWake / AdmitInstance)
		// threaded through.
		//
		// PR-A: atCapacity is the bool computed under the same Phase 2
		// lock at admitGate (true when this admit pushes the ledger
		// to the plan MaxConcurrency ceiling). Stamped on the same
		// bundle so the BootStarted emit picks it up unchanged. The
		// per-deployment path (admitAndDispatchForDeployment, line
		// 1325) bypasses admitGate by design (its own deployment-
		// scoped admit semantics) and surfaces AtCapacity=false on
		// bootInput — the dashboard's recent-wakes view is per-app,
		// not per-deployment.
		trigger:            trigger,
		queuedCount:        concurrency,
		concurrencyAtAdmit: concurrency,
		atCapacity:         atCapacity,
	}
	// issue #517 / PR-C / ADR-064 — emit wake.queue_accepted at
	// the boundary between Phase 2 (admit + ledger) and Phase 3
	// (unlocked vmmd RPC). The customer-facing timeline endpoint
	// joins this row to the wake.boot_started / boot_completed /
	// readiness_200 / proxy_first_byte rows that downstream
	// emit sites will write under the same wake_id. nil events
	// opts out (pre-PR-C fixtures) — guarded so the test
	// corpus doesn't need to wire a Platform.
	//
	// NOTE: queue_accepted is now emitted at the admission gate
	// (above) so the customer-facing timeline reads as
	// queue_accepted → admitted → boot_started. The earlier
	// Phase 2→3 boundary emit was redundant.
	release()

	// ADR-038 / Tier 3 phase 3: cold-boot layer attestation. The
	// layer key in spec is the same key imaged signed in
	// pkg/rootfs/publishExt4 — verify reads the sig from storage
	// and checks ECDSA-P-256 over SHA-256(layer). On mismatch the
	// verifier returns *api.Problem with code=sig_invalid; we
	// transition the deployment to DeployFailed (spec §6 failure
	// path) and surface the same Problem to the caller — the
	// gateway renders it as 503.
	if e.verifier != nil {
		if err := e.verifier.Verify(ctx, spec.LayerKey, "sigs/"+spec.LayerKey+".sig"); err != nil {
			var p *api.Problem
			if errors.As(err, &p) && p.Code == api.CodeSigInvalid {
				e.log.Warn("wake: rejecting tampered layer",
					"app", appID, "layer", spec.LayerKey, "err", err)
				e.transitionWithKind(ctx, bootInput.insID, appID, state.StateFailed, "wake_boot_error", "sig_invalid")
				e.ledger.Release(bootInput.insID)
				return WakeResult{}, err
			}
			// Transient I/O — fail the boot but don't mark the
			// layer compromised. Same shape as the vmmd
			// round-trip failure path below: transition + release.
			// Wrap in a *api.Problem so gatewayd-internal's writeWakeError
			// sees a Problem (and therefore writes through to the
			// client with Retry-After) instead of falling through
			// to its ErrCapacity fallback that lacks the header
			// (review finding #1a on PR #322). The detail
			// preserves the underlying storage error verbatim so
			// log greps still find it.
			e.log.Warn("wake: verifier i/o error",
				"app", appID, "layer", spec.LayerKey, "err", err)
			e.transitionWithKind(ctx, bootInput.insID, appID, state.StateFailed, "wake_boot_error", "sig_verify_io")
			e.ledger.Release(bootInput.insID)
			return WakeResult{}, api.NewProblem(503, api.CodeCapacity,
				"signature verification storage error",
				fmt.Sprintf("verifier I/O error for layer %q: %v (retry shortly)", spec.LayerKey, err)).
				WithHeader("Retry-After", "5")
		}
	}

	// ── Phase 3: drop the lock, do the slow vmmd RPC ──────────────
	var out *WakeOutcome
	bootCtx, cancel := context.WithTimeout(ctx, e.budgetForWake(bootInput))
	defer cancel()
	// Issue #555 PR-3: sched.wake span encloses the vmmd RPC. The
	// span's trace_id is the wake_id (uuidv7 → 32-hex), so the
	// gateway-initiated trace continues across the schedd → vmmd
	// boundary. The OTel-to-slog bridge (PR-3) lifts the trace_id
	// onto every log line under bootCtx, giving a single grep key
	// across the schedd engine.
	var wakeSpan oteltrace.Span
	if e.tracer != nil {
		bootCtx, wakeSpan = e.tracer.Start(bootCtx, "sched.wake",
			oteltrace.WithAttributes(
				attribute.String("app_id", appID),
				attribute.String("deployment_id", bootInput.depID),
				attribute.String("instance_id", bootInput.insID),
				attribute.String("wake_id", wakeID),
				attribute.String("init_state", string(bootInput.initState)),
			),
		)
		defer wakeSpan.End()
	}
	// issue #517 bootCtx stamp point: lift the inbound correlation
	// (request_id / invocation_id from gatewayd-internal) and join the
	// engine-minted wake_id / instance_id so a single inbound id
	// carries across the schedd → vmmd boundary.
	inboundCorr, _ := wire.FromContext(ctx)
	bootCtx = wire.WithContext(bootCtx, wire.CorrelationFields{
		RequestID:          inboundCorr.RequestID,
		InvocationID:       inboundCorr.InvocationID,
		AppID:              appID,
		DeploymentID:       bootInput.depID,
		InstanceID:         bootInput.insID,
		WakeID:             wakeID,
		Trigger:            bootInput.trigger,
		QueuedCount:        bootInput.queuedCount,
		ConcurrencyAtAdmit: bootInput.concurrencyAtAdmit,
	})
	// issue #517 / PR-C / ADR-064 — emit wake.boot_started at the
	// entry to Phase 3 (the unlocked vmmd RPC). The customer-facing
	// timeline endpoint joins this row to wake.boot_completed /
	// wake.boot_failed / wake.readiness_200 (vmmd-side emit) under
	// the same wake_id. method is "restore" or "cold_boot" so the
	// dashboard can split p50/p95 by wake method without joining
	// the legacy state_transition rows.
	method := "cold_boot"
	if bootInput.haveSnap {
		method = "restore"
	}
	if e.events != nil {
		e.events.Emit(bootCtx, events.BootStarted{
			EmitAt:             time.Now().UTC(),
			WakeID:             bootInput.wakeID,
			AppID:              bootInput.appID,
			InstanceID:         bootInput.insID,
			NodeID:             bootInput.nodeID,
			Method:             method,
			RequestedAt:        bootInput.startedAt, // best-effort stamp
			Trigger:            bootInput.trigger,
			QueuedCount:        bootInput.queuedCount,
			ConcurrencyAtAdmit: bootInput.concurrencyAtAdmit,
			AtCapacity:         bootInput.atCapacity, // PR-A — see bootInput.atCapacity doc
		})
	}
	// ADR-097 (P1B): capture the schedd-side wake-phase boundaries.
	//   - rpcStartedAt: the moment just before the vmmd
	//     CreateFromSnapshot / CreateColdBoot RPC fires. Used to
	//     observe admit_to_rpc (gap from bootInput.startedAt) and
	//     rpc_call (gap from rpcStartedAt to RPC return).
	//   - rpcEndedAt: the moment the vmmd RPC returns nil on the
	//     success path. Used to observe rpc_to_running (gap from
	//     rpcEndedAt to the WAKING/COLD_BOOTING → RUNNING
	//     transition below at engine.go:1892).
	// Both captures are wall-clock time.Now() — negligible overhead,
	// <1µs each. The error path at :1781-1790 does not capture
	// rpcEndedAt; the error duration is already surfaced via the
	// events.BootFailed - events.BootStarted math.
	rpcStartedAt := time.Now().UTC()
	if bootInput.haveSnap && bootInput.snapKey != "" {
		// #96 / ADR-025 axis 2: read the storage key the snap row
		// carries (imaged stamps it from the snapshot_written
		// payload). The deprecation-window fallback is gone after
		// #96 slice 3: F-1 contract on CreateSnapshot makes an empty
		// StorageKey an error, so by the time a row is reachable
		// here its key is set. If a row ever shows up empty here
		// (e.g. a buggy inserter that bypassed the F-1 contract),
		// the Wake below drops to cold-boot — the same ADR-005
		// fallback vmmdgrpc would apply on the wire. Keeping the
		// branch here means the engine never asks vmmd to restore
		// from an unkeyed snap row.
		//
		// #121 / ADR-025 axis 2 slice 4: populate both vmstate
		// locators. VMStatePath is reconstructed from the
		// deployment ID + SnapDir() so fcvm.Snapshot.Usable()
		// continues to succeed for default-local single-box (the
		// canonical host-path branch the engine relied on
		// pre-#121). VMStateStorageKey is the canonical
		// StorageBackend key — empty for default-local (the
		// helper returns "" so vmmd's host-path branch is taken
		// bit-for-bit), populated for remote nodes so vmmd's
		// storage path is taken. Closing the VMStatePath
		// reconstruction here also fixes the latent
		// cold-boot-regression surfaced during the #121
		// exploration (wake had been sending an empty
		// VMStatePath since migration 23 dropped snapshots.path).
		vmstatePath, vmstateStorageKey := e.snapshotStateLocators(bootInput.nodeID, state.Snapshot{
			DeploymentID: bootInput.depID, StorageKey: bootInput.snapKey,
		})
		// Issue #555 PR-3: vmmd.create_from_snapshot child span.
		// The vmmd-side gRPC stats handler (otelgrpc) starts a
		// server span for the CreateFromSnapshot RPC; this client
		// span is the parent linkage in the trace tree.
		bootCtx, createSpan := e.startCreateSpan(bootCtx, "vmmd.create_from_snapshot", bootInput.snapID, bootInput)
		out, err = e.vmm.CreateFromSnapshot(bootCtx, bootInput.nodeID, bootInput.insID, bootInput.spec, SnapshotRef{
			DeploymentID:      bootInput.depID,
			FCVersion:         bootInput.snapVer,
			StorageKey:        bootInput.snapKey,
			VMStatePath:       vmstatePath,
			VMStateStorageKey: vmstateStorageKey,
		})
		endSpan(createSpan)
	} else {
		// Either no snap row at all (cold path), or a snap row with
		// an empty StorageKey (F-1 contract violation — fall back to
		// a real cold boot per ADR-005: snapshots are cache, not
		// truth; wake must never depend on a snapshot existing).
		// Issue #555 PR-3: vmmd.create_cold_boot child span.
		bootCtx, createSpan := e.startCreateSpan(bootCtx, "vmmd.create_cold_boot", "", bootInput)
		out, err = e.vmm.CreateColdBoot(bootCtx, bootInput.nodeID, bootInput.insID, bootInput.spec)
		endSpan(createSpan)
	}
	if err != nil {
		// Boot error path. Release the reservation, transition to
		// FAILED. The transition's own re-read will write the row
		// even though we no longer hold the lock — transition is
		// lock-free by design (it only re-reads + writes one row).
		// Audit-log it under kind="wake_boot_error" so a query for
		// `kind='wake_boot_error'` finds both this and the
		// SetInstanceRuntime-failure case below.
		e.ledger.Release(bootInput.insID)
		// issue #517 / PR-C / ADR-064 — emit wake.boot_failed with
		// the structured reason. The customer-facing timeline pairs
		// this with wake.boot_started under the same wake_id so the
		// "wake took N ms before failing" latency is computable
		// without joining the legacy state_transition rows.
		if e.events != nil {
			e.events.Emit(ctx, events.BootFailed{
				EmitAt:     time.Now().UTC(),
				WakeID:     bootInput.wakeID,
				AppID:      bootInput.appID,
				InstanceID: bootInput.insID,
				NodeID:     bootInput.nodeID,
				Method:     method,
				Reason:     "vmm_boot_failed",
				FailedAt:   time.Now().UTC(),
			})
		}
		// issue #1059 / ADR-127 §3.6 — schedd-side parity
		// (cluster A commit 3 of the platform-observability
		// mega-PR). The schedd emits schedd_wake_failure_total
		// with reason="vmm_boot_failed" alongside the events
		// emit above. The reason literal is from the closed
		// wakeFailureReasons union (pkg/wire/metrics.go) — the
		// schedd-side audit-reason vocabulary joins the
		// vmmd-side classifier vocabulary so a single dashboard
		// legend covers both. bootInput.appID is the actual app
		// slug — schedd has the app identifier in scope here
		// (the audit-reason emit also references it). e.ops is
		// nil-safe per the field doc comment at engine.go:139 —
		// the guard skips the metric increment in unit tests
		// that don't wire an OpsMetrics.
		if e.ops != nil {
			e.ops.WakeFailure("", bootInput.appID, "vmm_boot_failed").Inc()
		}
		e.transitionWithKind(ctx, bootInput.insID, bootInput.appID, state.StateFailed, "wake_boot_error", "vmm_boot_failed")
		return WakeResult{}, err
	}

	// A restore that fell back to cold boot means the snapshot is bad:
	// mark it stale so the next wake cold-boots directly and the next
	// park re-snapshots. Best-effort — failure here doesn't block the
	// RUNNING transition (the stale snapshot also gets the next-park
	// treatment from snapshotAndPark).
	if bootInput.haveSnap && out.Method == vmmdpb.WakeMethod_WAKE_COLD_BOOT {
		if err := e.store.MarkSnapshotStale(ctx, bootInput.snapID); err != nil {
			e.log.Warn("wake: mark snapshot stale", "snapshot", bootInput.snapID, "wake_id", bootInput.wakeID, "err", err)
		}
		e.log.Info("wake: restore fell back to cold boot", "app", bootInput.appID, "instance", bootInput.insID, "wake_id", bootInput.wakeID)
	}

	// ── Phase 4: re-acquire the lock for the post-vmmd commit ────
	release2 := e.lockApp(bootInput.appID)
	defer release2()

	// ADR-097 (P1B): success-path RPC end capture. The error branch
	// above does NOT capture rpcEndedAt — error duration is already
	// surfaced via the events.BootFailed - events.BootStarted math
	// (see engine.go:1810-1817). We only need the success-path
	// capture to scope rpc_to_running.
	rpcEndedAt := time.Now().UTC()

	// Re-read the row. If a watchdog (commit 3) or a Park or another
	// Wake moved it out of initState during Phase 3, abort: this Wake
	// is no longer the canonical owner. Free the reservation and
	// destroy the VM we just booted.
	fresh, fresErr := e.store.InstanceByID(ctx, bootInput.insID)
	if fresErr != nil {
		// Couldn't re-read — take the conservative path. Destroy and
		// release; the transition will fail (no row), but the original
		// row must already be gone too (otherwise re-read wouldn't
		// fail).
		e.ledger.Release(bootInput.insID)
		e.bestEffortDestroy(ctx, bootInput.nodeID, bootInput.insID)
		return WakeResult{}, fmt.Errorf("sched: wake: re-read instance %s: %w", bootInput.insID, fresErr)
	}
	if fresh.State != string(bootInput.initState) {
		e.ledger.Release(bootInput.insID)
		e.bestEffortDestroy(ctx, bootInput.nodeID, bootInput.insID)
		e.log.Warn("wake: state stolen during boot, aborting",
			"app", bootInput.appID, "instance", bootInput.insID, "wake_id", bootInput.wakeID,
			"expected", bootInput.initState, "got", fresh.State)
		return WakeResult{}, fmt.Errorf("sched: wake: state stolen by another transition: was %s, now %s", bootInput.initState, fresh.State)
	}

	if err := e.store.SetInstanceRuntime(ctx, bootInput.insID, out.Netns, out.HostIP, int(out.LeaseUID)); err != nil {
		// Booted but unrecordable — destroy to avoid a resource leak,
		// then fail. Best-effort with a hard ceiling: a hung
		// Firecracker can't pin the Wake goroutine forever.
		e.bestEffortDestroy(ctx, bootInput.nodeID, bootInput.insID)
		e.ledger.Release(bootInput.insID)
		// issue #517 / PR-C / ADR-064 — emit wake.boot_failed with
		// the structured reason. Pairs with wake.boot_started under
		// the same wake_id (the prior emit at the Phase 3 entry).
		if e.events != nil {
			e.events.Emit(ctx, events.BootFailed{
				EmitAt:     time.Now().UTC(),
				WakeID:     bootInput.wakeID,
				AppID:      bootInput.appID,
				InstanceID: bootInput.insID,
				NodeID:     bootInput.nodeID,
				Method:     method,
				Reason:     "record_runtime_failed",
				FailedAt:   time.Now().UTC(),
			})
		}
		// issue #1059 / ADR-127 §3.6 — schedd-side parity
		// (cluster A commit 3). Same WakeFailure increment as
		// the vmm_boot_failed branch above; only the reason
		// literal changes (post-boot SetInstanceRuntime DB write
		// failed instead of pre-boot vmm.* RPC failed). e.ops
		// is nil-safe per the field doc comment at
		// engine.go:139 — the guard skips the metric increment
		// in unit tests that don't wire an OpsMetrics.
		if e.ops != nil {
			e.ops.WakeFailure("", bootInput.appID, "record_runtime_failed").Inc()
		}
		e.transitionWithKind(ctx, bootInput.insID, bootInput.appID, state.StateFailed, "wake_boot_error", "record_runtime_failed")
		return WakeResult{}, fmt.Errorf("sched: wake: record runtime: %w", err)
	}

	// ADR-051 Phase 4 / PR-D: persist the workload class the
	// characterize probe observed on the cold boot. On restore we
	// inherit from the apps row (no observation here — the warm
	// path runs the same scan-hint class the original cold boot
	// committed). On cold-boot timeouts the report is empty and we
	// keep the scan-hint class (no row mutation). Best-effort:
	// SetAppWorkloadClass failure doesn't block the RUNNING
	// transition — the class is metadata, not the boot path.
	if out.Characterization.ObservedClass != "" {
		if _, err := e.store.SetAppWorkloadClass(ctx, bootInput.appID, state.WorkloadClass(out.Characterization.ObservedClass), "observed"); err != nil {
			e.log.Warn("wake: SetAppWorkloadClass", "app", bootInput.appID, "err", err)
		}
		// PR-D review finding #6: emit an `app.characterized` audit
		// row so an operator tailing events can pin the observed
		// class back to the boot that surfaced it. Carries the
		// guest's class hint, the observed port, exit code, and
		// the chosen portnorm rung — enough to reconstruct "why is
		// this app now classed http" from the event log alone
		// (no vmmd slog archaeology). Best-effort per ADR-035:
		// audit.Emit never returns an error and never blocks the
		// RUNNING transition. nil auditor (pre-PR-D fixtures) is
		// tolerated via the nil check.
		if e.audit != nil {
			e.audit.Emit(ctx, "app.characterized", nil, map[string]any{
				"app_id":          bootInput.appID,
				"wake_id":         bootInput.wakeID,
				"observed_class":  out.Characterization.ObservedClass,
				"observed_port":   out.Characterization.ObservedPort,
				"exit_code":       out.Characterization.ExitCode,
				"listening_addrs": out.Characterization.ListeningAddrs,
				"port_norm_mode":  out.Characterization.PortNormalizationMode,
				"log_tail_chars":  len(out.Characterization.LogTail),
			})
		}
	}

	e.transition(ctx, bootInput.insID, bootInput.appID, state.StateRunning)
	// A completed wake proves that the deployment can boot again. Clear any
	// expired snapshot-miss backoff so a later miss starts a fresh sequence;
	// this is idempotent for deployments that never had a backoff row.
	if err := e.ClearSnapshotBackoff(ctx, bootInput.depID); err != nil {
		e.log.Warn("wake: clear snapshot backoff after successful boot", "deployment_id", bootInput.depID, "err", err)
	}

	// ADR-097 (P1B): observe the three schedd-side wake phases.
	//   - admit_to_rpc = rpcStartedAt - bootInput.startedAt.
	//     Covers the gRPC handler → admitGate → ledger → placement
	//     → bootInput construction window. bootInput.startedAt is
	//     the existing bootInput capture at :1589 — the bootInput
	//     stamp and the events.Platform BootStarted.RequestedAt are
	//     the same moment, so the histogram joins cleanly to the
	//     events table on wake_id.
	//   - rpc_call = rpcEndedAt - rpcStartedAt.
	//     Wall-clock time inside the vmmd Create{FromSnapshot,ColdBoot}
	//     RPC. Cross-process boundary; the only phase that crosses
	//     a node-local socket.
	//   - rpc_to_running = time.Since(rpcEndedAt).
	//     Covers the bootInput re-read + SetInstanceRuntime +
	//     audit emit + e.transition work.
	//
	// All three are observed on the success path only — the error
	// branches above (engine.go:1795-1838) do not produce a
	// successful RUNNING transition and would skew the histogram
	// with failure latencies that the events.BootFailed row already
	// captures. The wake_id is attached as a prometheus.Exemplar on
	// each observation (the third ObserveWithExemplar arg below)
	// so operators can join to gateway_wake_latency_seconds on the
	// gateway side and to the events table.
	if e.ops != nil {
		// wake_id is the per-wake-attempt correlation handle emitted
		// on the events table and on the boot_started / boot_completed
		// SSE channel. Attach it as a prometheus.Exemplar on each
		// phase observation so operators can join metrics to events
		// on the wake_id key without paying the label cardinality cost.
		exemplar := prometheus.Labels{"wake_id": bootInput.wakeID}
		if obs, ok := e.ops.WakeRPCDuration(bootInput.appID, "admit_to_rpc").(prometheus.ExemplarObserver); ok {
			obs.ObserveWithExemplar(
				rpcStartedAt.Sub(bootInput.startedAt).Seconds(),
				exemplar,
			)
		}
		if obs, ok := e.ops.WakeRPCDuration(bootInput.appID, "rpc_call").(prometheus.ExemplarObserver); ok {
			obs.ObserveWithExemplar(
				rpcEndedAt.Sub(rpcStartedAt).Seconds(),
				exemplar,
			)
		}
		if obs, ok := e.ops.WakeRPCDuration(bootInput.appID, "rpc_to_running").(prometheus.ExemplarObserver); ok {
			obs.ObserveWithExemplar(
				time.Since(rpcEndedAt).Seconds(),
				exemplar,
			)
		}
	}

	// issue #517 / PR-C / ADR-064 — emit wake.boot_completed. The
	// three sibling emits (wake.boot_started, wake.boot_completed,
	// wake.readiness_200 from vmmd) give the customer-facing
	// timeline endpoint span latencies per wake method under the
	// same wake_id. The actual method reported here is
	// vmmd's authoritative observation (`out.Method`), which may
	// differ from the planned `method` above when restore fell
	// back to cold boot (the F-1 stale-snapshot path).
	completedMethod := method
	switch out.Method {
	case vmmdpb.WakeMethod_WAKE_COLD_BOOT:
		completedMethod = "cold_boot"
	case vmmdpb.WakeMethod_WAKE_RESTORE:
		completedMethod = "restore"
	}
	if e.events != nil {
		now := time.Now().UTC()
		e.events.Emit(ctx, events.BootCompleted{
			EmitAt:             now,
			WakeID:             bootInput.wakeID,
			AppID:              bootInput.appID,
			InstanceID:         bootInput.insID,
			NodeID:             bootInput.nodeID,
			Method:             completedMethod,
			StartedAt:          bootInput.startedAt,
			CompletedAt:        now,
			Trigger:            bootInput.trigger,
			QueuedCount:        bootInput.queuedCount,
			ConcurrencyAtAdmit: bootInput.concurrencyAtAdmit,
		})
	}

	return WakeResult{InstanceID: bootInput.insID, NodeID: fresh.NodeID, Method: out.Method, WakeID: bootInput.wakeID, Port: bootInput.spec.Port, DeploymentID: bootInput.depID, RequestCount: fresh.RequestCount}, nil
}

// bootInput is the immutable bundle of values needed across the
// unlocked window in Wake's Phase 3. Captured under the Phase 2 lock;
// consumed by Phase 3 (vmmd call) and Phase 4 (post-boot commit).
type bootInput struct {
	insID     string
	appID     string
	depID     string
	initState state.State
	haveSnap  bool
	snapID    string // empty when haveSnap is false
	snapVer   string // empty when haveSnap is false
	// snapKey is the canonical StorageBackend key for the mem blob
	// (issue #96, ADR-025 axis 2). Read from the snap row under
	// Phase 2; consumed by Phase 3 to set SnapshotRef.StorageKey.
	// Empty when haveSnap is false.
	snapKey string
	// nodeID is the chosen compute_node for this wake (issue #97 /
	// ADR-025 axis 3). Captured under the Phase 2 lock alongside
	// the rest of bootInput so the unlocked Phase 3 vmmd call can
	// route through the right per-target client. Read by Phase 4's
	// best-effort-destroy path on error so the destroy hits the
	// same vmmd instance the boot landed on.
	nodeID string
	spec   AppSpec
	// wakeID is the per-wake-attempt correlation handle (gaps analysis
	// 2026-07-23). UUIDv7 minted at Phase 2 under the lock, persisted
	// on the instances row in CreateInstance, and carried across the
	// unlocked Phase 3 window so the slog calls + WakeResult surface
	// the same value. Fresh on every Wake() — the same instance row
	// can carry many wake_ids over its lifetime as the app parks and
	// wakes again.
	wakeID string
	// startedAt is the wake's accept instant (issue #517 / PR-C /
	// ADR-064). Captured at bootInput construction so the
	// boot_started.RequestedAt + boot_completed.StartedAt pair
	// share a stamp; consumed by the wake.boot_completed emit
	// after the vmmd RPC returns. Best-effort — the watchdog's
	// §6.1 anchor uses instances.started_at separately, so this
	// drift is bounded by 1 row read.
	startedAt time.Time

	// ADR-123: trigger / queued_count / concurrency_at_admit
	// snapshot at admit time. Captured at bootInput construction
	// under the Phase 2 lock and consumed by both the BootStarted
	// (Phase 3 entry, engine.go:1987) and BootCompleted (Phase 4
	// commit, engine.go:2269) emits. The values are immutable
	// across the unlocked Phase 3 window so both rows carry the
	// same snapshot — the customer-facing wake timeline joins them
	// on wake_id and sees identical trigger / queued / concurrency
	// values.
	//
	// queued_count and concurrency_at_admit are both sourced from
	// e.ledger.Concurrency(app.ID) (single read, under lock).
	// The two names reflect "what was the per-app concurrency
	// when this wake admitted" — the names map to the two
	// Cloud-Run fields in the user's reference line. WakeGate.
	// InflightWaiters is NOT stamped (see ADR-123 §Decision 2 —
	// the gateway-side count reflects "currently-waiting request
	// count", not "siblings-admitted").
	trigger            string
	queuedCount        int
	concurrencyAtAdmit int
	// atCapacity (PR-A) is the bool returned by admitGate's
	// wakeAdmit branch — true when the pre-admit ledger reading is
	// maxConc-1 and this admit pushes the post-admit ledger to the
	// plan's per-app MaxConcurrency ceiling. Stamped on the
	// BootStarted emit; BootCompleted intentionally does NOT carry
	// it (admit-time concept, post-RecordRuntime state is stale).
	atCapacity bool
}

// timedDestroy issues a vmm.Destroy bounded by `timeout` and the
// caller's ctx. The parent ctx is preserved so cancellation propagates
// normally — if the caller (Wake / Prime / Park / KillStuck) is
// shutting down, the destroy returns immediately rather than
// continuing against a cancelled parent. The timeout is the upper
// bound: a wedged Firecracker can't pin the caller past `timeout`.
//
// nodeID is the compute_node the instance lives on; the router
// forwards to the right per-target vmmd connection. Park / Evict /
// KillStuck read ins.NodeID from the locked row before calling; an
// empty nodeID is treated as "default-local" so legacy test
// fixtures that pre-date PR #113 still work.
//
// KillStuck uses a tighter 5s so a wedged Firecracker can't pin the
// watchdog goroutine. All other callers use DestroyTimeout.
//
// If a destroy really must run after the caller's ctx is cancelled
// (rare — today, none of the callers do this), route it through a
// dedicated cleanup goroutine in cmd/schedd instead of lying about
// the context here.
func (e *Engine) timedDestroy(ctx context.Context, nodeID, instanceID string, timeout time.Duration) error {
	destroyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return e.vmm.Destroy(destroyCtx, e.nodeForRoute(nodeID), instanceID)
}

// nodeForRoute returns the node ID the router should dial. Empty
// nodeID (legacy test seam) falls back to the engine's
// defaultLocalNodeID so the single-box path stays routable even
// when callers haven't threaded the placement decision through.
// Production callers always pass a non-empty nodeID (Wake / Prime
// via ChoosePlacement; Park / Evict / snapshotAndPark via
// ins.NodeID).
func (e *Engine) nodeForRoute(nodeID string) string {
	if nodeID != "" {
		return nodeID
	}
	return e.defaultLocalNodeID
}

// bestEffortDestroy is the no-error-discard wrapper around
// timedDestroy at the standard DestroyTimeout, used by Phase 4 /
// Prime error paths where the destroy failure is observation-only
// and the row is already doomed.
func (e *Engine) bestEffortDestroy(ctx context.Context, nodeID, instanceID string) {
	_ = e.timedDestroy(ctx, nodeID, instanceID, DestroyTimeout)
}

// applyLiveCapacityMB returns the chooser's per-node used_mb input:
// max(live report, ledger) when the report is fresh, 0 (sentinel)
// when stale or absent. The ledger floor closes the stale-low /
// hostile-report gap — a vmmd that under-reports its cgroup
// memory.current cannot shrink the live accounting and force schedd
// to over-admit. ADR-025 axis 5 / Tier A1.
//
// Zero is a sentinel: the caller distinguishes "no live report" from
// "report says 0 used" by re-reading the store. The caller logs the
// store error and treats a single missing node as zero headroom so a
// transient store failure on one node doesn't block placement on
// others.
//
// nil receiver is tolerated — a pre-axis-5 fixture's nil capacityTable
// returns 0, the caller falls through to the store, and the legacy
// single-box behaviour is preserved.
func (e *Engine) applyLiveCapacityMB(_ context.Context, nodeID string) int64 {
	if e == nil || e.capacityTable == nil {
		return 0
	}
	now := time.Now
	if e.now != nil {
		now = e.now
	}
	live, ok := e.capacityTable.Lookup(nodeID, now())
	if !ok {
		return 0
	}
	used := int64(live.UsedMB)
	if ledger := int64(e.ledger.ResidentRAMForNode(nodeID)); ledger > used {
		used = ledger
	}
	if used < 0 {
		return 0
	}
	return used
}

// fallbackNodeUsage resolves store-backed usage for a set of nodes with one
// aggregate query and a short-lived cache. The per-node method remains as a
// compatibility fallback for custom Store implementations that predate the
// optional ComputeNodeUsageBatcher interface.
func (e *Engine) fallbackNodeUsage(ctx context.Context, nodes []state.ComputeNode) map[string]int64 {
	used := make(map[string]int64, len(nodes))
	if len(nodes) == 0 {
		return used
	}
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	now := time.Now()
	if e.now != nil {
		now = e.now()
	}
	if cached, ok := e.usageCache.Lookup(ids, now); ok {
		return cached
	}
	if batcher, ok := e.store.(state.ComputeNodeUsageBatcher); ok {
		bulk, err := batcher.ComputeNodeUsedMBByNode(ctx, ids)
		if err == nil {
			e.usageCache.Replace(ids, bulk, now)
			for _, id := range ids {
				used[id] = bulk[id]
			}
			return used
		}
		e.log.Warn("sched: placement: bulk compute node used_mb read failed", "err", err)
	}
	// Compatibility path only: production PgStore and the built-in MemStore
	// both implement the bulk interface above.
	for _, node := range nodes {
		value, err := e.store.ComputeNodeUsedMB(ctx, node.ID)
		if err != nil {
			e.log.Warn("sched: placement: compute node used_mb read failed",
				"node_id", node.ID, "node_name", node.Name, "err", err)
			continue
		}
		used[node.ID] = value
	}
	return used
}

// nodeUsageForNodes merges fresh vmmd reports with the one-batch store
// fallback. All callers that make a fleet-wide placement decision use this
// helper so a new placement feature cannot accidentally reintroduce an N+1
// ComputeNodeUsedMB loop.
func (e *Engine) nodeUsageForNodes(ctx context.Context, nodes []state.ComputeNode) map[string]int64 {
	used := make(map[string]int64, len(nodes))
	missing := make([]state.ComputeNode, 0, len(nodes))
	for _, node := range nodes {
		if live := e.applyLiveCapacityMB(ctx, node.ID); live > 0 {
			used[node.ID] = live
		} else {
			missing = append(missing, node)
		}
	}
	for nodeID, value := range e.fallbackNodeUsage(ctx, missing) {
		used[nodeID] = value
	}
	return used
}

// choosePlacement picks a compute_node for the next wake using the
// pure ChoosePlacement chooser (placement.go). It loads the live
// fleet from the store and the per-node used_mb aggregate, both
// inside the per-app lock so a concurrent wake for the same app
// sees a coherent view. Returns the placement (with TargetURL so
// the wake loop doesn't need a second lookup) or a *api.Problem
// from the chooser when no node has headroom.
//
// Tier A1: the per-node used_mb input now starts from
// applyLiveCapacityMB (live publisher report, ledger floor). When
// the live table has no fresh entry for a node, the chooser falls
// back to the legacy store sum so a silent vmmd degrades to the
// pre-axis-5 behaviour rather than dropping the node from the
// fleet view entirely.
func (e *Engine) choosePlacementLocked(ctx context.Context, r Request) (Placement, error) {
	// Phase 2 / Gate A: pin placement to the schedd's owner
	// node. The chooser still runs the per-node headroom checks
	// (usedMB + usedVCPU + ceiling), but every candidate is
	// either this owner or nothing — the wake can't escape to
	// another schedd's fleet. An empty ownerNodeID preserves
	// the legacy behaviour (pick any active node).
	if e.ownerNodeID != "" {
		// Defence-in-depth: refuse to admit if the app's
		// persisted owner doesn't match. The gRPC handler's
		// authorizeApp guard is the load-bearing check (the
		// gateway partitions by apps.node_id before dialling);
		// this is the second-line filter for direct Engine
		// calls (the engine_test.go wake-locality tests).
		if app, err := e.store.AppByID(ctx, r.AppID); err == nil && app.NodeID != "" && app.NodeID != e.ownerNodeID {
			return Placement{}, api.ErrCapacity(fmt.Sprintf(
				"placement: app %s is owned by node %s; this schedd owns %s",
				r.AppID, app.NodeID, e.ownerNodeID))
		}
		r.PreferredNodeID = e.ownerNodeID
	}
	var nodes []state.ComputeNode
	var err error
	if e.nodeRegistry != nil {
		nodes = e.nodeRegistry.Snapshot()
	} else {
		nodes, err = e.store.ActiveComputeNodes(ctx)
		if err != nil {
			return Placement{}, fmt.Errorf("sched: placement: list active compute_nodes: %w", err)
		}
	}
	// One pass over the fleet — use fresh vmmd capacity where available, and
	// resolve all misses through one bulk store aggregate (Tier A1). The
	// ledger's per-node UsedVCPU remains an independent local reservation view.
	usedMB := e.nodeUsageForNodes(ctx, nodes)
	usedVCPU := make(map[string]int64, len(nodes))
	for _, n := range nodes {
		// Tier A2: per-node vCPU is ledger-authoritative. The chooser
		// uses this to enforce compute_nodes.vcpu_budget per node;
		// absent or zero is treated as 0 by the chooser (the
		// per-node budget is the gate, not the absolute number).
		usedVCPU[n.ID] = int64(e.ledger.UsedVCPUForNode(n.ID))
	}
	return ChoosePlacement(nodes, usedMB, usedVCPU, r)
}

// ClaimUnplaced is the schedd-side async placement claim
// (Phase 2 / Gate A migration 00091 — apps.node_id nullable).
//
// Called by pkg/sched.PlacementClaimSubscriber when apid emits a
// NotifyAppChanged "created" event. The post-00091 schema lets
// apid insert with node_id = NULL; every schedd races to stamp
// the owner via Store.SetAppNodeID, whose conditional UPDATE
// serialises N schedds into exactly one winner.
//
// Idempotent: a redelivered notify or a peer that already won
// both observe app.NodeID != "" and return nil. Returns the
// underlying store / chooser error wrapped with %w+op when
// placement cannot proceed (capacity exhausted, FK rejection,
// etc.); the subscriber logs and continues — the next notify
// (e.g. a later admin update) is a fresh opportunity.
//
// Caller contract:
//   - ctx must be cancellable; the subscriber drops on cancel.
//   - The cold-start sweep calls this once per ListUnplacedApps
//     row at schedd startup (closes the pg_notify missed-event
//     window).
func (e *Engine) ClaimUnplaced(ctx context.Context, appID string) error {
	if appID == "" {
		return fmt.Errorf("sched: claim unplaced: empty app id")
	}
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		// AppByID wraps state.ErrNotFound for a deleted row; the
		// subscriber drops silently on that path (M7 hard-delete
		// is expected; re-stamping a deleted app is a bug).
		return fmt.Errorf("sched: claim unplaced: lookup app: %w", err)
	}
	if app.NodeID != "" {
		// Already claimed (by us on redelivery or by a peer).
		// Idempotent no-op.
		return nil
	}
	if app.RAMMB <= 0 {
		// Defensive: the quota path normally guarantees a positive
		// RAM. A non-positive value would have choosePlacementLocked
		// return ErrCapacity before SetAppNodeID ever runs — which
		// means the row stays NULL and another peer would also see
		// it. Better to drop loudly here than to spin on a bad row.
		e.log.Warn("sched: claim unplaced: skipping app with non-positive RAM",
			"app_id", appID, "ram_mb", app.RAMMB)
		return nil
	}
	acct, err := e.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return fmt.Errorf("sched: claim unplaced: lookup account: %w", err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	placement, err := e.choosePlacementLocked(ctx, Request{
		AppID:          appID,
		RAMMB:          app.RAMMB,
		VCPU:           limits.VCPU,
		MaxConcurrency: app.MaxConcurrency,
	})
	if err != nil {
		return fmt.Errorf("sched: claim unplaced: choose: %w", err)
	}
	if err := e.store.SetAppNodeID(ctx, appID, placement.NodeID); err != nil {
		if errors.Is(err, state.ErrConflict) {
			// Peer won between our AppByID and SetAppNodeID.
			// Expected under contention; drop silently.
			return nil
		}
		return fmt.Errorf("sched: claim unplaced: stamp owner: %w", err)
	}
	// Re-emit so any listener interested in the binding (the
	// gateway's schedd client cache rebuild, the runbook's
	// SELECT … GROUP BY node_id observability) sees the
	// transition. The subscriber itself filters out kind=claimed
	// to avoid re-entry.
	if e.notif != nil {
		payload := fmt.Sprintf(`{"kind":"claimed","app_id":%q,"node_id":%q}`, appID, placement.NodeID)
		if err := e.notif.Notify(ctx, db.NotifyAppChanged, payload); err != nil {
			e.log.Warn("sched: claim unplaced: notify claimed",
				"app_id", appID, "node_id", placement.NodeID, "err", err)
		}
	}
	e.log.Info("sched: claim unplaced: stamped owner",
		"app_id", appID, "node_id", placement.NodeID, "node_name", placement.Name)
	return nil
}

// RebalanceOrphanedApps reassigns active/evicted_cold apps orphaned
// by a dead node to the local schedd's owner_node. Tier A4
// (ADR-064). Triggered by the rebalancer watcher's
// compute_node_changed(active=false) handler; also invoked
// once at schedd cold-start with deadNodeID="" to sweep apps
// missed by a missed notify.
//
// Contract (mirrors the post-#509 placement-claim rationale):
//
//   - Idempotent. A peer schedd's claim + our conditional
//     UPDATE means only one wins per app (RowsAffected()==0 on
//     the loser; surfaced as state.ErrConflict).
//   - Paced. A per-app cooldown (default 60s,
//     api.RebalanceCooldownSeconds) suppresses flap-loops; the
//     apps.reassigned_at column is the timestamp source.
//   - Capped. Per-drain-event work is bounded by
//     api.RebalanceMaxPerTickPerNode (default 50) so a
//     5,000-app orphaned node doesn't monopolise the worker
//     pool. Excess apps stay pinned on the dead node — the
//     next compute_node_changed re-fires (or
//     heartbeat-staleness in issue #97 §3).
//   - Admission-aware. The rebalancer threads the live
//     compute_node_used_mb into the per-app decision so we
//     never blow api.RAMAdmissionCeilingMB on a 9,500-MB node
//     by re-stamping a 1,024-MB app.
//   - Outcome-observable. Each decision increments
//     schedd_rebalance_decisions_total{outcome=…}
//     (migrated / conflict / no_headroom / cooldown / no_eligibility).
//
// deadNodeID is the node whose apps we want to migrate.
//
// When non-empty: filter ListOrphanedApps results to that
// node in memory (the SQL already excludes active owners).
// When empty: cold-start sweep — every orphaned app is in
// scope regardless of which dead node originally owned it.
func (e *Engine) RebalanceOrphanedApps(ctx context.Context, deadNodeID string) error {
	if e.ownerNodeID == "" {
		// Legacy single-box posture: there's no peer to migrate
		// to. The orphaned apps are already ours in spirit (the
		// synthetic default-local is the only active owner); do
		// nothing and let the next cold-boot stamp clean rows.
		e.log.Info("sched: rebalance skipped — no owner_node_id",
			"dead_node_id", deadNodeID)
		return nil
	}

	// Load the full orphan set; cap + cooldown filter are SQL
	// constraints, not in-memory filters, so the caller's
	// Store already trims the result set. Per-engine overrides
	// (FAAS_REBALANCE_COOLDOWN_SECONDS /
	// FAAS_REBALANCE_MAX_PER_TICK via WithRebalanceConfig)
	// take precedence over the api.* constants.
	cooldownSec := api.RebalanceCooldownSeconds
	maxPerTick := api.RebalanceMaxPerTickPerNode
	if e.rebalanceCooldownSeconds > 0 {
		cooldownSec = e.rebalanceCooldownSeconds
	}
	if e.rebalanceMaxPerTick > 0 {
		maxPerTick = e.rebalanceMaxPerTick
	}
	orphans, err := e.store.ListOrphanedApps(ctx, cooldownSec, maxPerTick)
	if err != nil {
		return fmt.Errorf("sched: rebalance: list orphaned: %w", err)
	}
	if len(orphans) == 0 {
		return nil
	}

	// Read the live per-node used RAM once up-front so the
	// admission filter accounts for everything currently
	// resident on this schedd. A deadNodeID-driven event does
	// not change this (only successful reassignments do, and
	// we decrement locally as we go).
	usedMB, err := e.store.ComputeNodeUsedMB(ctx, e.ownerNodeID)
	if err != nil {
		return fmt.Errorf("sched: rebalance: read used mb: %w", err)
	}

	// Per-node ceiling for the admission check. Fall back to
	// the global api.RAMAdmissionCeilingMB for the legacy
	// single-box posture (and for un-registered compute_nodes).
	ceiling := e.admissionCeilingForOwn(ctx)
	if ceiling <= 0 {
		ceiling = api.RAMAdmissionCeilingMB
	}

	migrated, conflict, noHeadroom, cooldown, ineligible := 0, 0, 0, 0, 0
	defer func() {
		// One summary line per call — keeps the logs compact
		// without losing the per-outcome breakdown, which
		// operators read off the metric for normal ops.
		e.log.Info("sched: rebalance batch done",
			"dead_node_id", deadNodeID,
			"migrated", migrated, "conflict", conflict,
			"no_headroom", noHeadroom, "cooldown", cooldown,
			"ineligible", ineligible,
			"processed", migrated+conflict+noHeadroom+cooldown+ineligible)
	}()

	now := time.Now().UTC()
	for _, app := range orphans {
		// deadNodeID filter (cold-start sweep with empty
		// deadNodeID skips this — every orphan is in scope).
		if deadNodeID != "" && app.NodeID != deadNodeID {
			continue
		}
		// Defense-in-depth: the SQL filter on store.ListOrphanedApps
		// already restricts to non-deleted app statuses, but the
		// in-memory check documents the contract + survives a
		// future store-port without a SQL review. apps.status
		// CHECK is 'active'|'evicted_cold'|'deleted', not instance
		// states ('parked'/'stopped' are instance states — see
		// pkg/state/machine.go).
		if app.Status != state.AppActive && app.Status != state.AppEvictedCold {
			if e.ops != nil {
				e.ops.RebalanceDecisions("no_eligibility").Inc()
			}
			ineligible++
			continue
		}
		// Cooldown filter — apps.reassigned_at is the
		// authoritative source (set by ReassignAppOwner). The
		// SQL filter already excludes in-window apps; the
		// in-memory recheck tolerates a clock-skewed row that
		// would otherwise escape via the SQL "< now() - interval"
		// comparison. Conservative: this branch can only over-
		// skip, never over-claim.
		if app.ReassignedAt != nil && now.Sub(*app.ReassignedAt) < time.Duration(cooldownSec)*time.Second {
			if e.ops != nil {
				e.ops.RebalanceDecisions("cooldown").Inc()
			}
			cooldown++
			continue
		}
		// Admission filter — admission ceiling is conservative
		// (the API surface is "ceilings are inclusive"; billable
		// RAM is RAMMB + api.PerVMOverheadMB so we count the
		// overhead in the prospective reservation).
		neededMB := int64(app.RAMMB) + int64(api.PerVMOverheadMB)
		if usedMB+neededMB > int64(ceiling) {
			if e.ops != nil {
				e.ops.RebalanceDecisions("no_headroom").Inc()
			}
			noHeadroom++
			continue
		}
		// Optimistically reserve the headroom; a lost race
		// rolls it back below. This keeps the cap honest
		// within a batch — a 50-app drain doesn't double-count
		// the per-app RAM.
		usedMB += neededMB
		if err := e.store.ReassignAppOwner(ctx, app.ID, app.NodeID, e.ownerNodeID); err != nil {
			usedMB -= neededMB
			if errors.Is(err, state.ErrConflict) {
				// Peer won between our ListOrphanedApps
				// and our ReassignAppOwner. Expected under
				// contention; keep going.
				if e.ops != nil {
					e.ops.RebalanceDecisions("conflict").Inc()
				}
				conflict++
				continue
			}
			// Non-conflict failures (network blip, FK
			// violation, ErrNotFound on a soft-deleted
			// app) surface as a per-app Warn but do NOT
			// halt the batch — the remaining apps still
			// need a decision. A non-conflict error on the
			// last app of a batch is recoverable on the next
			// compute_node_changed re-fire, so swallowing
			// it (vs. returning) is the safer default.
			e.log.Warn("sched: rebalance: reassign failed",
				"app_id", app.ID, "from_node", app.NodeID,
				"to_node", e.ownerNodeID, "err", err)
			continue
		}
		migrated++
		if e.ops != nil {
			e.ops.RebalanceDecisions("migrated").Inc()
		}

		// Emit the per-app reassignment notify. The gateway's
		// per-node schedd client cache subscribes to this
		// channel and evicts the now-stale dial target for
		// the dead node; pkg/sched/placement_claim.go's
		// subscriber drops the rebalanced kind so no re-
		// entry loop happens.
		if e.notif != nil {
			payload := fmt.Sprintf(
				`{"kind":"rebalanced","app_id":%q,"from_node":%q,"to_node":%q}`,
				app.ID, app.NodeID, e.ownerNodeID)
			if err := e.notif.Notify(ctx, db.NotifyAppChanged, payload); err != nil {
				e.log.Warn("sched: rebalance: notify rebalanced",
					"app_id", app.ID, "from", app.NodeID,
					"to", e.ownerNodeID, "err", err)
			}
		}
		e.log.Info("sched: rebalance: migrated app",
			"app_id", app.ID, "slug", app.Slug,
			"from_node", app.NodeID, "to_node", e.ownerNodeID)
	}
	return nil
}

// RebalancePressuredApps (Tier A9 / ADR-087) reassigns a
// single pressured app to a peer schedd's fleet when the
// owner-of-record is at sustained capacity-refusal pressure.
// The cheap path (this method) only handles the parked-only
// case; the policy-gated live-instance migration hook was
// removed in the Tier A10 follow-up PR (see the comment at the
// ReassignAppOwner call site below) — peer-to-peer live
// migration on the pressure path is a Tier A10.1 follow-up
// pending a real peer-to-peer migrator (ADR-066's four-phase
// handoff only supports destination = local schedd).
//
// Triggered by the pressure-rebalancer (pkg/sched/pressure_rebalancer.go)
// on every sweep where the app's sliding-window AtCapacity
// count exceeds the threshold. The watcher increments the
// consecutive-sweep counter BEFORE this call; the policy gate
// (migrate_after_2) reads the counter to decide whether to
// also fire the four-phase live handoff.
//
// appID is the pressured app. Empty string means "cold-start
// sweep" — every app currently above the threshold is in
// scope (the watcher enumerates via aggregator.PressuredApps).
//
// Errors are logged per-app; the batch returns nil on
// non-conflict failures (a transient DB blip on one app must
// not halt the sweep — the next sweep retries).
func (e *Engine) RebalancePressuredApps(ctx context.Context, appID string) error {
	if e.ownerNodeID == "" {
		// Legacy single-box posture: no peer to migrate to.
		// The pressure signal is informative only; the
		// threshold is the operator's cue to add capacity.
		return nil
	}
	if appID == "" {
		// Cold-start sweep is delegated to the watcher; the
		// engine's per-app method is the per-tick path.
		return nil
	}

	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return fmt.Errorf("sched: pressure rebalance: lookup app: %w", err)
	}
	// apps.node_id is the source of truth for ownership. If a
	// peer schedd already claimed the app (a racing sweep
	// won), or the app is no longer ours, silently drop.
	if app.NodeID == "" {
		e.observePressure("no_eligibility")
		return nil
	}
	if app.NodeID != e.ownerNodeID {
		e.observePressure("no_eligibility")
		return nil
	}
	// Eligibility filter — mirrors RebalanceOrphanedApps.
	if app.Status != state.AppActive && app.Status != state.AppEvictedCold {
		e.observePressure("no_eligibility")
		return nil
	}
	// Cooldown filter — apps.reassigned_at is the authoritative
	// source. The pressure-rebalancer's per-apps cooldown is the
	// same deadline as the dead-node rebalancer's; both stamp
	// the same column via Store.ReassignAppOwner.
	cooldownSec := api.RebalanceCooldownSeconds
	if e.rebalanceCooldownSeconds > 0 {
		cooldownSec = e.rebalanceCooldownSeconds
	}
	if app.ReassignedAt != nil &&
		time.Since(*app.ReassignedAt) < time.Duration(cooldownSec)*time.Second {
		e.observePressure("cooldown")
		return nil
	}
	// Peer selection — name-ASC sort, deterministic.
	// Tier A10 / ADR-088: per-app overflow_node preference. If
	// the customer pinned a spill target, try it first; on
	// miss (inactive / no headroom / gone) the engine observes
	// `overflow_target_unavailable` on the A9 outcome axis
	// AND `unavailable` on the A10 spill axis, then falls
	// through to the existing first-peer-with-headroom path.
	// A successful overflow-target assignment is counted as a
	// regular `migrated` on the A9 axis (the outcome is the
	// same — the app moved) and `used` on the A10 axis (the
	// preference was honoured). The two metric surfaces stay
	// independent so a Grafana panel can branch "did the
	// preference matter?" without reading the same series.
	var overflowTargetUsed bool
	peer := ""
	if app.OverflowNode != nil && *app.OverflowNode != "" {
		var perr error
		peer, perr = e.findOverflowPeerWithHeadroom(ctx, app, *app.OverflowNode)
		if perr != nil {
			e.log.Warn("sched: pressure rebalance: overflow peer lookup",
				"app_id", app.ID, "overflow_node", *app.OverflowNode, "err", perr)
		}
		if peer != "" {
			overflowTargetUsed = true
		} else {
			// Mirror the unavailable outcome on the A9 axis so
			// the dashboard's existing "no_headroom" panel
			// doesn't swallow a preference miss as a routine
			// "fleet full" event. Operators branching on
			// `overflow_target_unavailable` know the customer
			// pinned a target and the engine couldn't honour
			// it (vs `no_headroom` which is "no peer anywhere
			// on the fleet has space").
			e.observePressure("overflow_target_unavailable")
			e.observeOverflowSpill("unavailable")
		}
	}
	if peer == "" {
		peer, err = e.findPeerWithHeadroom(ctx, app)
		if err != nil {
			return fmt.Errorf("sched: pressure rebalance: find peer: %w", err)
		}
		if peer == "" {
			e.observePressure("no_headroom")
			// Tier A10 / ADR-088: if the customer's
			// overflow_node preference was missed earlier
			// (overflowTargetUsed=false) and the fallback
			// also produced no peer, no spill-axis label is
			// warranted — the sweep simply had no place to
			// land the app. A non-empty peer would have
			// bumped `fallback_used` at the success site.
			return nil
		}
		// Tier A10 / ADR-088: the overflow preference was
		// missed earlier (overflowTargetUsed=false) and
		// the A9 fallback actually landed a peer. Bump
		// `fallback_used` so the spill-axis panel
		// distinguishes "preference lost, fleet empty"
		// (no fallback_used increment) from "preference
		// lost, fleet absorbed the app" (this increment).
		if !overflowTargetUsed {
			e.observeOverflowSpill("fallback_used")
		}
	}
	// Live-instance migration hook (Tier A10 follow-up):
	// previously called e.maybeMigrateLiveInstancesFor here and
	// bumped observePressure("peer_live_migrated") on success.
	// That helper always no-op'd because MigrateLiveInstances
	// self-skips when deadNodeID == e.ownerNodeID (engine.go:2944
	// in pkg/sched/engine.go), and ADR-066's four-phase handoff
	// only supports destination = local schedd (active-passive HA,
	// ADR-083) — peer-to-peer live migration on the pressure path
	// is a Tier A10.1 follow-up. Until that ships, the pressure
	// rebalancer's effective policy is `skip_live`: only parked
	// apps are reassigned; apps with live instances on the owner
	// stay pinned (the customer's wake fails until the instances
	// drain). The migration policy knob keeps the closed set
	// {skip_live, migrate_after_1, migrate_after_2} so a future
	// PR can wire the policy to a real peer-to-peer migrator
	// without churn on the API surface.
	//
	// Atomic reassign of apps.node_id. RowsAffected==0 (the
	// peer-claim race) is the only conflict path; the engine
	// sees ErrConflict and increments the conflict metric.
	if err := e.store.ReassignAppOwner(ctx, app.ID, app.NodeID, peer); err != nil {
		if errors.Is(err, state.ErrConflict) {
			e.observePressure("conflict")
			return nil
		}
		e.observePressure("no_eligibility")
		e.log.Warn("sched: pressure rebalance: reassign failed",
			"app_id", app.ID, "from_node", app.NodeID, "to_node", peer, "err", err)
		return nil
	}
	// Reset the aggregator slice for this app — the new owner
	// may be at lower pressure; let the counter rebuild
	// against the new owner's metric.
	if e.pressureAggregator != nil {
		e.pressureAggregator.Reset(app.ID)
	}
	e.ResetPressureSweepCounter(app.ID)
	e.observePressure("migrated")
	// Tier A10 / ADR-088: bump the spill-axis `used` counter
	// alongside the A9 `migrated` outcome. The two metric
	// surfaces stay independent so a Grafana panel can branch
	// "did the preference matter?" (used vs no_used) without
	// reading the same series with two queries.
	if overflowTargetUsed {
		e.observeOverflowSpill("used")
	}
	// Emit the per-app reassignment notify. The gateway's
	// per-node schedd client cache subscribes to this channel
	// and evicts the now-stale dial target for the old owner;
	// pkg/sched/placement_claim.go's subscriber drops the
	// pressure_rebalanced kind so no re-entry loop happens.
	if e.notif != nil {
		payload := fmt.Sprintf(
			`{"kind":"pressure_rebalanced","app_id":%q,"from_node":%q,"to_node":%q}`,
			app.ID, app.NodeID, peer)
		if err := e.notif.Notify(ctx, db.NotifyAppChanged, payload); err != nil {
			e.log.Warn("sched: pressure rebalance: notify rebalanced",
				"app_id", app.ID, "from", app.NodeID, "to", peer, "err", err)
		}
	}
	e.log.Info("sched: pressure rebalance: migrated app",
		"app_id", app.ID, "slug", app.Slug,
		"from_node", app.NodeID, "to_node", peer)
	return nil
}

// observePressure is the per-outcome pressure-rebalancer metric
// helper. Centralising the nil-safety keeps the call sites
// single-line.
func (e *Engine) observePressure(outcome string) {
	if e.ops != nil {
		e.ops.PressureReassignments(outcome).Inc()
	}
}

// observeOverflowSpill is the per-outcome Tier A10 / ADR-088
// overflow_node preference metric helper. Mirrors
// observePressure's nil-safe shape so the call sites stay
// single-line and the metric surface stays "ops != nil → bump".
func (e *Engine) observeOverflowSpill(outcome string) {
	if e.ops != nil {
		e.ops.OverflowTargetSpillHits(outcome).Inc()
	}
}

// findPeerWithHeadroom returns the first peer (sorted by name
// ASC, deterministic) whose admission_headroom is at least
// app.RAMMB + api.PerVMOverheadMB. Returns "" if no peer
// qualifies. Errors are wrapped with %w+op.
func (e *Engine) findPeerWithHeadroom(ctx context.Context, app state.App) (string, error) {
	var nodes []state.ComputeNode
	if e.nodeRegistry != nil {
		nodes = e.nodeRegistry.Snapshot()
	} else {
		var err error
		nodes, err = e.store.ActiveComputeNodes(ctx)
		if err != nil {
			return "", fmt.Errorf("sched: pressure rebalance: list active nodes: %w", err)
		}
	}
	// Sort by name for deterministic tie-break (the caller
	// picks the first peer with headroom, so the iteration
	// order is load-bearing for the test surface).
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	neededMB := int64(app.RAMMB) + int64(api.PerVMOverheadMB)
	usedByNode := e.nodeUsageForNodes(ctx, nodes)
	for _, n := range nodes {
		if n.ID == e.ownerNodeID {
			continue
		}
		used := usedByNode[n.ID]
		ceiling := n.AdmissionCeilingMB
		if ceiling <= 0 {
			ceiling = api.RAMAdmissionCeilingMB
		}
		if int64(ceiling)-used >= neededMB {
			return n.ID, nil
		}
	}
	return "", nil
}

// findOverflowPeerWithHeadroom (Tier A10 / ADR-088) resolves a
// per-app overflow_node preference to a single peer UUID,
// applying the same headroom check findPeerWithHeadroom uses.
// targetUUID is the resolved compute_nodes.id the customer
// pinned at create / PATCH time (apid does the name → UUID
// resolution server-side so this helper never sees a wire
// shape). Returns "" if any of the following is true:
//
//   - targetUUID == e.ownerNodeID (no-op self-migration).
//   - target node is missing (operator deleted it; the FK
//     ON DELETE SET NULL would have cleared the column, but a
//     racing sweep can still observe the prior value before
//     the SET NULL lands).
//   - target node is inactive (operator drained it; the
//     customer didn't notice and never unset the preference).
//   - target node has no headroom for this app
//     (ceiling - used < neededMB).
//
// Errors from the underlying Store reads are wrapped with
// %w+op; the engine caller decides whether to log+fall-through
// (it does) or surface the error (it doesn't — the fallback
// path is the canonical recovery).
func (e *Engine) findOverflowPeerWithHeadroom(ctx context.Context, app state.App, targetUUID string) (string, error) {
	if targetUUID == e.ownerNodeID {
		return "", nil
	}
	n, err := e.store.ComputeNodeByID(ctx, targetUUID)
	if err != nil {
		// ErrNotFound is the deleted-node path — the FK
		// ON DELETE SET NULL would normally have cleared
		// the column, so this is a racing-sweep observation
		// only. ANY other error is also swallowed (logged at
		// debug below) because the engine's fallback to
		// first-peer-with-headroom is the canonical recovery
		// for a preference miss — a transient store blip
		// shouldn't escalate to a sweep-wide failure. Return
		// "" + nil so the caller falls through without
		// logging at warn.
		if !errors.Is(err, state.ErrNotFound) {
			e.log.Debug("sched: pressure rebalance: overflow peer lookup failed",
				"app_id", app.ID, "overflow_node", targetUUID, "err", err)
		}
		return "", nil
	}
	if !n.Active {
		return "", nil
	}
	used := e.nodeUsageForNodes(ctx, []state.ComputeNode{n})[n.ID]
	ceiling := n.AdmissionCeilingMB
	if ceiling <= 0 {
		ceiling = api.RAMAdmissionCeilingMB
	}
	neededMB := int64(app.RAMMB) + int64(api.PerVMOverheadMB)
	if int64(ceiling)-used < neededMB {
		return "", nil
	}
	return n.ID, nil
}

// pressureSweepCounterValue is the read-only accessor for the
// pressure-sweep counter. Read under the mutex; no allocation
// (the engine's watchdog / engine test paths use the value).
func (e *Engine) pressureSweepCounterValue(appID string) int {
	if e == nil {
		return 0
	}
	e.pressureSweepMu.Lock()
	defer e.pressureSweepMu.Unlock()
	return e.pressureSweepCounter[appID]
}

// admissionCeilingForOwn returns the active per-node
// admission ceiling for ownerNodeID, or 0 when the row is
// missing/un-registered. Mirrors choosePlacementLocked's
// lookup at engine.go:1665-1678; small enough to inline.
// Called with e.mu NOT held — the lookup is read-only.
func (e *Engine) admissionCeilingForOwn(ctx context.Context) int {
	n, err := e.store.ComputeNodeByID(ctx, e.ownerNodeID)
	if err != nil {
		return 0
	}
	return n.AdmissionCeilingMB
}

// resolveNodeCeiling returns (ceilingMB, vcpuBudget) for a nodeID
// via store.ComputeNodeByID. Used by MigrationHarness to thread
// the destination's per-node admission limits into the Phase 3
// ledger reservation — without this, NodeLedger.Admit falls back
// to the global api.RAMAdmissionCeilingMB / api.VCPUSlots and a
// heterogeneous fleet with one smaller destination gets
// over-admitted (violating invariant §6.2-2).
//
// Errors fall through to (0, 0) which the ledger treats as the
// legacy single-box fallback (safe for an un-registered node or a
// transient store error; the migration proceeds at the global
// ceiling — slightly less safe but never silently wrong). Called
// with e.mu NOT held; ComputeNodeByID is read-only.
func (e *Engine) resolveNodeCeiling(ctx context.Context, nodeID string) (int, int, error) {
	if nodeID == "" {
		return 0, 0, nil
	}
	n, err := e.store.ComputeNodeByID(ctx, nodeID)
	if err != nil {
		return 0, 0, err
	}
	return n.AdmissionCeilingMB, n.VCPUBudget, nil
}

// BuildAppSpecForMigration (Tier A5 / ADR-066) rebuilds the
// AppSpec shape vmmd needs to restore a migrated VM from the
// local app + deployment view. The lookup walks: instance → app
// + deployment → drive0 base key + drive1 layer key + sealed
// env (filtered through dep.OverrideEnvSecrets) + api env +
// egress allowlist + override port + override healthcheck path.
//
// This method is the migration path's analogue of the Wake-time
// spec builder (engine.go:911-948). The two MUST stay in lock-
// step — a divergence silently regresses the migration:
//
//   - wrong LayerKey → cold-boot from the base, not the layer
//     (the snapshot the dying vmmd wrote is irrelevant; the
//     guest overlayfs mounts the base as drive1 too)
//   - wrong VCPUCount (e.g. app.MaxConcurrency) → Scale-tier
//     apps under-provisioned post-migration
//   - dropped EgressMbit → no per-plan tc cap on the migrated
//     netns (Scale tier ships at 200 Mbit; a 0 cap removes it)
//   - dropped HealthcheckPath → readiness probe skipped; vmmd
//     falls back to TCP-accept which can pass for an unready
//     app (issue #460 / ADR-053, ADR-057 / PR-D)
//   - swallowed secrets/env errors → guest env.json ships
//     empty; Stripe keys and DB creds silently disappear
//
// Returns an error if the instance / app / deployment triple
// can't be resolved. The caller (MigrationHarness.loadAppSpecForInstance)
// treats this as a Phase 3 setup failure and rolls back
// Phase 2 + Phase 4.
func (e *Engine) BuildAppSpecForMigration(ctx context.Context, instanceID string) (AppSpec, error) {
	ins, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		return AppSpec{}, fmt.Errorf("sched: build app spec: instance by id: %w", err)
	}
	app, err := e.store.AppByID(ctx, ins.AppID)
	if err != nil {
		return AppSpec{}, fmt.Errorf("sched: build app spec: app by id: %w", err)
	}
	dep, err := e.store.LiveDeployment(ctx, ins.AppID)
	if err != nil {
		return AppSpec{}, fmt.Errorf("sched: build app spec: live deployment: %w", err)
	}
	acct, err := e.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return AppSpec{}, fmt.Errorf("sched: build app spec: account by id: %w", err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	// Sealed env is filtered through dep.OverrideEnvSecrets
	// (jsonb) when present, mirroring the Wake path at
	// engine.go:907-910. A migration without the override
	// surface ships the full secret set; one with the override
	// ships only the requested env_keys. A missing-required
	// key fails loud (the legacy "stage everything" path is
	// preserved when OverrideEnvSecrets is nil).
	sealedEnv, err := e.loadSealedEnvFor(ctx, app.AccountID, app.ID, dep.Scope, envSecretsFromDep(dep))
	if err != nil {
		return AppSpec{}, fmt.Errorf("sched: build app spec: sealed env: %w", err)
	}
	sidecars, err := e.sidecarsForDeployment(ctx, dep)
	if err != nil {
		return AppSpec{}, fmt.Errorf("sched: build app spec: sidecars: %w", err)
	}
	return AppSpec{
		BaseKey:       baseKey(app.Runtime),
		LayerKey:      layerKey(dep.RootfsKey, dep.ID),
		VCPUCount:     int32(limits.VCPU),
		MemSizeMiB:    int32(app.RAMMB),
		CPUMillicores: int32(app.CPUMillicores),
		EgressMbit:    int32(limits.EgressMbit),
		// M-3: migration must preserve the same readiness budget as the
		// original wake, including a manifest override.
		StartupDeadlineS: startupDeadlineForApp(app, acct.Plan),
		Plan:             acct.Plan,
		AccountID:        acct.ID,
		AppID:            app.ID,
		DeploymentID:     dep.ID,
		SealedEnv:        sealedEnv,
		Sidecars:         sidecars,
		// ADR-045: api_env plaintext layer; the loadAPIEnv
		// helper already fail-softs on a lookup error and logs
		// Warn (engine.go:2382-2396). A hiccup here ships an
		// empty api_env block, NOT a failed migration — the
		// overlayfs upper layers carry the same precedence
		// rules as Wake time and the customer's runtime config
		// (most of it) lives in sealedEnv + manifest_env.
		APIEnv: e.loadAPIEnv(ctx, app.AccountID, app.ID, dep.Scope),
		// ADR-031: per-app egress allowlist; same CIDR-string
		// flattening as the Wake path.
		EgressAllowlist: prefixesToCIDRStrings(app.EgressAllowlist),
		// ADR-119: customer-supplied static egress IPv4
		// (BYOIP, Scale-only). Same threading as the Wake
		// path above.
		StaticEgressIP: staticEgressIPString(app.StaticEgressIP),
		// Issue #460 / ADR-053 (PR-C): per-deployment override
		// port. 0 = legacy 8080 (vmmd wire default).
		Port: dep.OverridePort,
		// Issue #460 / ADR-053, ADR-057 (PR-D): per-deployment
		// override readiness probe path. "" = legacy TCP-accept.
		HealthcheckPath: healthcheckPathFromDep(dep),
		// Issue #470 / PR #470-FU-B: per-deployment runner id
		// (e.g. "node22", "python312"). The sched sources it
		// from the apps row at Wake time and threads it onto
		// the vmmd AppSpec so the framework_ready DGRAM receipt
		// path can label
		// vmmd_guest_framework_warmup_seconds by runner. Empty
		// falls back to "unknown" in the histogram observer.
		Runtime: app.Runtime,
	}, nil
}

// MigrateLiveInstances (Tier A5 / ADR-066) is the live-instance
// counterpart to RebalanceOrphanedApps. Given a dying nodeID,
// it lists every live instance on that node (state in
// {WAKING, COLD_BOOTING, RUNNING, SNAPSHOTTING}) and runs the
// four-phase handoff via MigrationHarness.MigrateOne for each,
// capped by MigrateLiveMaxPerTick.
//
// Per-instance work is sequential within a single MigrateLiveInstances
// call (a future PR can fan-out via worker pool, but the lease
// clock + the per-instance gauge is simpler this way). The
// caller is the cmd/schedd drain watcher — same trigger shape
// as RebalanceOrphanedApps but on a different state filter.
//
// Returns the count of instances we attempted; per-instance
// outcomes land in schedd_live_migration_decisions_total.
//
// Failure modes:
//   - deadNodeID == e.ownerNodeID: nothing to do (can't migrate
//     from yourself to yourself); return 0 silently.
//   - ownerNodeID == "": legacy single-box posture; return 0
//     with a log.
//   - ListLiveInstancesOnNode error: propagate.
//   - per-instance harness error: logged Warn + metric, the
//     loop continues. A failed migration is left to the next
//     compute_node_changed re-fire (lease-expiry on the dying
//     vmmd clears the entry).
func (e *Engine) MigrateLiveInstances(ctx context.Context, deadNodeID string) (int, error) {
	if e.ownerNodeID == "" {
		e.log.Info("sched: live migrate skipped — no owner_node_id",
			"dead_node_id", deadNodeID)
		return 0, nil
	}
	if deadNodeID == "" || deadNodeID == e.ownerNodeID {
		return 0, nil
	}
	maxPerTick := api.MigrateLiveMaxPerTick
	if e.migrateLiveMaxPerTick > 0 {
		maxPerTick = e.migrateLiveMaxPerTick
	}
	liveInstances, err := e.store.ListLiveInstancesOnNode(ctx, deadNodeID, maxPerTick)
	if err != nil {
		return 0, fmt.Errorf("sched: live migrate: list: %w", err)
	}
	if len(liveInstances) == 0 {
		return 0, nil
	}
	if len(liveInstances) > maxPerTick {
		e.log.Info("sched: live migrate: capped",
			"dead_node_id", deadNodeID,
			"available", len(liveInstances),
			"cap", maxPerTick)
		liveInstances = liveInstances[:maxPerTick]
	}

	harness := NewMigrationHarness(ctx, e.store, e.vmm, e.ops, e.log,
		e.ownerNodeID, e.BuildAppSpecForMigration, e.ledger,
		e.resolveNodeCeiling)
	harness.SetMaxPerTick(maxPerTick)
	// Workstream B / Task #66: share the recovery-timeline
	// Platform so a successful Phase 4 ack emits
	// instance.migrated alongside the wake timeline.
	if e.events != nil {
		harness.WithEvents(e.events)
	}
	leaseSeconds := api.MigrateLiveLeaseSeconds
	if e.migrateLiveLeaseSeconds > 0 {
		leaseSeconds = e.migrateLiveLeaseSeconds
	}
	harness.SetLeaseSeconds(leaseSeconds)

	migrated, attempted := 0, 0
	// Task #61: when the recovery arbiter is wired, ask it for the
	// per-instance verdict before driving the 4-phase handoff. The
	// arbiter's Recreate verdict routes to Engine.RecreateInstance
	// (the dead-VM-shaped PARKED landing) and skips the migration
	// entirely — there is no usable snapshot to migrate, so the
	// 4-phase handoff would orphan the row. With no arbiter, the
	// legacy behaviour stands (every instance attempts the handoff).
	if e.recoveryArbiter != nil {
		if cn, lookupErr := e.store.ComputeNodeByID(ctx, deadNodeID); lookupErr == nil {
			for _, ins := range liveInstances {
				attempted++
				handled, dispatchErr := e.dispatchRecovery(ctx, cn, ins.ID)
				if dispatchErr != nil {
					e.log.Warn("sched: live migrate: dispatch failed",
						"instance_id", ins.ID, "from", deadNodeID,
						"to", e.ownerNodeID, "err", dispatchErr)
					continue
				}
				if handled {
					migrated++
					continue
				}
				if err := harness.MigrateOne(ctx, ins.ID, deadNodeID); err == nil {
					migrated++
				} else if errors.Is(err, state.ErrConflict) {
					e.log.Debug("sched: live migrate: peer conflict",
						"instance_id", ins.ID, "from", deadNodeID,
						"to", e.ownerNodeID)
				} else {
					e.log.Warn("sched: live migrate: instance failed",
						"instance_id", ins.ID, "from", deadNodeID,
						"to", e.ownerNodeID, "err", err)
				}
			}
			e.log.Info("sched: live migrate batch done",
				"dead_node_id", deadNodeID,
				"attempted", attempted, "migrated", migrated,
				"to_node", e.ownerNodeID)
			return attempted, nil
		}
	}
	for _, ins := range liveInstances {
		attempted++
		err := harness.MigrateOne(ctx, ins.ID, deadNodeID)
		if err == nil {
			migrated++
			continue
		}
		// ErrConflict is expected under contention (peer
		// re-owner / peer rollback); log Debug, not Warn.
		// Anything else is a per-instance failure; log Warn
		// and continue with the rest of the batch.
		if errors.Is(err, state.ErrConflict) {
			e.log.Debug("sched: live migrate: peer conflict",
				"instance_id", ins.ID, "from", deadNodeID,
				"to", e.ownerNodeID)
			continue
		}
		e.log.Warn("sched: live migrate: instance failed",
			"instance_id", ins.ID, "from", deadNodeID,
			"to", e.ownerNodeID, "err", err)
	}
	e.log.Info("sched: live migrate batch done",
		"dead_node_id", deadNodeID,
		"attempted", attempted, "migrated", migrated,
		"to_node", e.ownerNodeID)
	return attempted, nil
}

// MigrateRecoveryInstance dispatches one arbiter-approved RUNNING instance
// to this schedd's owner node. Keeping the single-instance adapter on Engine
// lets the recovery runner use the same four-phase handoff as the legacy
// notify path, while the arbiter remains the sole migrate-vs-recreate policy.
func (e *Engine) MigrateRecoveryInstance(ctx context.Context, instanceID string) error {
	if e == nil || e.store == nil {
		return nil
	}
	if e.ownerNodeID == "" {
		return nil
	}
	ins, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("sched: recovery migration: load instance %s: %w", instanceID, err)
	}
	if ins.NodeID == "" || ins.NodeID == e.ownerNodeID {
		// A schedd must never hand an instance back to its current owner.
		// In particular, the source node's own recovery runner can observe
		// the row while a peer is racing to adopt it.
		return nil
	}
	if ins.State != string(state.StateRunning) {
		return fmt.Errorf("sched: recovery migration: instance %s is %q, want running", instanceID, ins.State)
	}
	if e.vmm == nil {
		return fmt.Errorf("sched: recovery migration: vmmd router is not configured")
	}
	if e.ledger == nil {
		return fmt.Errorf("sched: recovery migration: node ledger is not configured")
	}
	metrics := e.ops
	if metrics == nil {
		metrics = wire.NewOpsMetrics("schedd")
	}
	harness := NewMigrationHarness(ctx, e.store, e.vmm, metrics, e.log,
		e.ownerNodeID, e.BuildAppSpecForMigration, e.ledger,
		e.resolveNodeCeiling)
	harness.SetMaxPerTick(1)
	leaseSeconds := api.MigrateLiveLeaseSeconds
	if e.migrateLiveLeaseSeconds > 0 {
		leaseSeconds = e.migrateLiveLeaseSeconds
	}
	harness.SetLeaseSeconds(leaseSeconds)
	if e.events != nil {
		harness.WithEvents(e.events)
	}
	return harness.MigrateOne(ctx, instanceID, ins.NodeID)
}

// ReconcileExpiredMigrations (Tier A6 / ADR-067 migrating-
// instance watchdog) is the per-tick work function that self-
// heals stuck state='migrating' rows that never committed (the
// new owner vmmd died mid-handoff, the network partition
// dropped the gRPC, the operator killed the new owner before
// the Phase-3 commit). The watchdog is the ONLY writer that
// can move a row out of 'migrating' without a peer commit —
// every Phase-4 path (CancelInstanceMigration) requires a
// peer, and the peer is the very thing that's gone.
//
// Per-instance decision (driven by compute_nodes.active for
// the row's current node_id, which is the OLD/dying vmmd —
// Phase 2 MarkInstanceMigrating did not flip node_id, only
// Phase 3 MigrateInstanceOwner does):
//
//  1. Active owner (compute_nodes.active = true): the row is
//     wedged but the dying vmmd is still up. Issue a
//     Store.ReinviteMigratingInstance conditional UPDATE that
//     flips state='migrating' → 'running', stamps migrated_at,
//     and clears lease_token. node_id stays on the OLD owner.
//     Bumps outcome="reinvited".
//  2. Dead owner (compute_nodes.active = false): the dying
//     vmmd is gone. Issue a Store.AbortMigratingInstance
//     conditional UPDATE that flips state='migrating' →
//     'parked' and clears lease_token. node_id is left on the
//     dead OLD owner (migrated_from_node_id is NULL pre-Phase-3;
//     the wake path dispatches via app.NodeID, not instance.
//     NodeID, so a dead instance.NodeID is harmless). Bumps
//     outcome="hard_deleted".
//  3. Conflict (RowsAffected() == 0): the lease expired or a
//     peer already committed/rolled back. Bumps
//     outcome="conflict" and drops silently.
//  4. Error (transient DB / lookup blip): bumps
//     outcome="error", logs Warn, continues.
//
// The conditional UPDATE predicates (state='migrating' AND
// lease_token=$1) are the load-bearing race-safety guarantee.
// A peer that committed while the watchdog was thinking fails
// the predicate and bumps outcome="conflict" (peer-wins); a
// peer that rolled back also fails the predicate (state is
// now 'parked' from CancelInstanceMigration).
//
// Returns (reconciled int, err error). reconciled is the
// number of rows that successfully transitioned out of
// 'migrating' (reinvited + hard_deleted). err is non-nil only
// on a fatal-but-recoverable issue (the input-set query
// failed); per-row failures are reported via the metric and
// the slog so a transient PG blip never stops the tick.
func (e *Engine) ReconcileExpiredMigrations(ctx context.Context) (int, error) {
	maxPerTick := api.MigratingWatchdogTickLimit
	if e.migratingWatchdogTickLimit > 0 {
		maxPerTick = e.migratingWatchdogTickLimit
	}
	rows, err := e.store.ListExpiredMigrations(ctx, maxPerTick)
	if err != nil {
		return 0, fmt.Errorf("sched: reconcile expired migrations: list: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if len(rows) > maxPerTick {
		e.log.Info("sched: reconcile expired migrations: capped",
			"available", len(rows), "cap", maxPerTick)
		rows = rows[:maxPerTick]
	}
	reconciled := 0
	for _, ins := range rows {
		ownerActive, lookupErr := e.computeNodeActive(ctx, ins.NodeID)
		if lookupErr != nil {
			if e.ops != nil {
				e.ops.MigratingReconcileDecisions("error").Inc()
			}
			e.log.Warn("sched: reconcile expired migrations: owner lookup failed",
				"instance_id", ins.ID, "node_id", ins.NodeID, "err", lookupErr)
			continue
		}
		var recErr error
		if ownerActive {
			recErr = e.store.ReinviteMigratingInstance(ctx, ins.ID, ins.LeaseToken)
			if recErr == nil {
				if e.ops != nil {
					e.ops.MigratingReconcileDecisions("reinvited").Inc()
				}
				e.log.Info("sched: reconcile expired migrations: reinvited",
					"instance_id", ins.ID, "node_id", ins.NodeID)
				reconciled++
				continue
			}
		} else {
			recErr = e.store.AbortMigratingInstance(ctx, ins.ID, ins.LeaseToken)
			if recErr == nil {
				if e.ops != nil {
					e.ops.MigratingReconcileDecisions("hard_deleted").Inc()
				}
				e.log.Info("sched: reconcile expired migrations: hard_deleted",
					"instance_id", ins.ID, "node_id", ins.NodeID)
				reconciled++
				continue
			}
		}
		if errors.Is(recErr, state.ErrConflict) {
			if e.ops != nil {
				e.ops.MigratingReconcileDecisions("conflict").Inc()
			}
			e.log.Debug("sched: reconcile expired migrations: peer conflict",
				"instance_id", ins.ID, "err", recErr)
			continue
		}
		if e.ops != nil {
			e.ops.MigratingReconcileDecisions("error").Inc()
		}
		e.log.Warn("sched: reconcile expired migrations: instance failed",
			"instance_id", ins.ID, "err", recErr)
	}
	e.log.Info("sched: reconcile expired migrations batch done",
		"reconciled", reconciled, "attempted", len(rows))
	return reconciled, nil
}

// dispatchRecovery asks the recovery arbiter for the per-
// (node, instance) verdict and dispatches accordingly. Returns
// (handled bool, err error) — handled=true means the arbiter
// already disposed of the row (RecreateInstance transitioned it
// to PARKED), so the caller should skip its own per-instance
// work. handled=false means DecisionLiveMigrate (caller proceeds
// with migrate) or DecisionNone (caller should skip silently).
//
// nil arbiter ⇒ returns (false, nil) so legacy callers without
// the arbiter wired keep their pre-#1184 semantics (no behavior
// change). This is the nil-arbiter bootstrap path
// recovery_arbiter_test.go pins.
func (e *Engine) dispatchRecovery(ctx context.Context, node state.ComputeNode, instanceID string) (handled bool, err error) {
	if e.recoveryArbiter == nil {
		return false, nil
	}
	ins, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			// Peer already removed the row — caller should skip.
			return true, nil
		}
		return false, fmt.Errorf("sched: dispatchRecovery: load %s: %w", instanceID, err)
	}
	switch e.recoveryArbiter.Decide(node, state.RecoveryInstance{
		ID:           ins.ID,
		State:        ins.State,
		AppID:        ins.AppID,
		DeploymentID: ins.DeploymentID,
	}) {
	case DecisionRecreate:
		// The arbiter's recreate verdict wins; the row transitions
		// to PARKED with kind='recovery_recreate' (Task #60). The
		// legacy migration-handoff path is skipped — there is
		// nothing to migrate.
		if recErr := e.RecreateInstance(ctx, ins.ID); recErr != nil {
			return false, recErr
		}
		return true, nil
	case DecisionNone:
		return true, nil
	case DecisionLiveMigrate:
		return false, nil
	}
	return false, nil
}

// ReconcileDeadNodeInstances closes the dead-node billing leak.
//
// The gap it fills: schedd's heartbeat sweep calls
// MarkComputeNodeInactive when a node stops answering, but that
// UPDATE touches only `compute_nodes` — instance rows are left
// untouched by design (placement reads node state; it does not
// rewrite instance state). Meanwhile meterd's sampler bills every row
// whose State.CountsForRAM() is true, with no node-liveness
// cross-check. So a vmmd that dies without transitioning its rows
// leaves them RUNNING indefinitely: the customer is billed for a VM
// that does not exist, and the phantom rows keep consuming the
// §6.2-2 RAM admission ceiling, suppressing real wakes.
//
// The sweep is deliberately conservative. A row is only failed when
// its node has been unreachable for DeadNodeReconcilerStalenessSeconds
// (120s) — one full heartbeat interval beyond the 90s window at which
// schedd itself declares a node dead. Failing sooner than schedd's own
// verdict would terminate instances on a node that is merely slow.
//
// Per-row safety comes from the conditional UPDATE in
// FailRunningInstanceOnDeadNode (state = 'running' AND node_id = $2).
// If the node recovered, or a peer already parked/evicted/migrated the
// row, RowsAffected() is 0, the store returns ErrConflict, and we count
// it as a peer-wins no-op rather than second-guessing the state
// machine. That is the same race-safety contract as
// ReconcileExpiredMigrations.
//
// FAILED (not PARKED) is the correct terminal state: no snapshot was
// taken, because the VM died with its host. Claiming PARKED would
// assert a snapshot that does not exist. FAILED is cold-bootable
// (ADR-005: snapshots are cache, not truth), so the customer's next
// request still serves — it just pays the cold-boot path.
//
// Returns (reconciled, err). err is non-nil only when the input-set
// query fails; per-row failures are logged and counted so one wedged
// row never stalls the sweep.
func (e *Engine) ReconcileDeadNodeInstances(ctx context.Context) (int, error) {
	staleness := time.Duration(api.DeadNodeReconcilerStalenessSeconds) * time.Second
	if e.deadNodeReconcilerStalenessSeconds > 0 {
		staleness = time.Duration(e.deadNodeReconcilerStalenessSeconds) * time.Second
	}
	threshold := time.Now().UTC().Add(-staleness)
	maxPerTick := api.DeadNodeReconcilerTickLimit

	rows, err := e.store.ListRunningInstancesOnDeadNodes(ctx, threshold, maxPerTick)
	if err != nil {
		return 0, fmt.Errorf("sched: reconcile dead-node instances: list: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if len(rows) == maxPerTick {
		// A full batch means there may be more waiting. Worth an
		// operator line: a whole node's fleet going dead at once is
		// exactly the "you broke something" event this sweep exists
		// to surface, not just silently repair.
		e.log.Info("sched: reconcile dead-node instances: batch at cap",
			"cap", maxPerTick)
	}

	reconciled := 0
	for _, ins := range rows {
		recErr := e.store.FailRunningInstanceOnDeadNode(ctx, ins.ID, ins.NodeID)
		switch {
		case recErr == nil:
			// Release the admission reservation so a replacement
			// instance can be admitted immediately. Release is
			// idempotent and a no-op on unknown instances
			// (admission.go), so this is safe even when the
			// reservation was already freed by another path.
			e.ledger.Release(ins.ID)
			if e.ops != nil {
				e.ops.DeadNodeReconcileDecisions("failed").Inc()
			}
			e.log.Warn("sched: reconcile dead-node instances: failed orphaned instance",
				"instance_id", ins.ID, "app_id", ins.AppID, "node_id", ins.NodeID,
				"ram_mb", ins.RAMMB)
			if e.events != nil {
				e.events.EmitRecovery(ctx, events.InstanceFailedEvent{
					EmitAt:     time.Now().UTC(),
					InstanceID: ins.ID,
					AppID:      ins.AppID,
					NodeID:     ins.NodeID,
					Reason:     "liveness_lost",
				})
			}
			if ins.Mode == string(state.InstanceModeService) {
				e.scheduleServiceReconcile(ctx, ins.DeploymentID)
			}
			reconciled++
		case errors.Is(recErr, state.ErrConflict):
			// Node recovered, or a peer moved the row first. Benign
			// at the row level — but Task #62 source-ledger
			// backstop closes the billing side of the same race:
			// the peer's failure path might not have freed the
			// admission slot (the gateway-listener used to be the
			// only path that called Release on terminal transitions;
			// a peer that crashed before reaching it leaked the
			// slot). ResidentFor + Release is the idempotent
			// cleanup so the deadnode reconciler is the canonical
			// path for "row is no longer billable" regardless of
			// who moved it.
			if e.ledger.ResidentFor(ins.ID) {
				e.ledger.Release(ins.ID)
				e.log.Info("sched: reconcile dead-node instances: ledger backstop released",
					"instance_id", ins.ID, "node_id", ins.NodeID)
			}
			if e.ops != nil {
				e.ops.DeadNodeReconcileDecisions("conflict").Inc()
			}
			e.log.Debug("sched: reconcile dead-node instances: peer conflict",
				"instance_id", ins.ID, "err", recErr)
		default:
			if e.ops != nil {
				e.ops.DeadNodeReconcileDecisions("error").Inc()
			}
			e.log.Warn("sched: reconcile dead-node instances: transition failed",
				"instance_id", ins.ID, "node_id", ins.NodeID, "err", recErr)
		}
	}
	if reconciled > 0 {
		e.log.Warn("sched: reconcile dead-node instances batch done",
			"reconciled", reconciled, "attempted", len(rows))
	}
	return reconciled, nil
}

// computeNodeActive returns compute_nodes.active for the given
// nodeID. Returns false on ErrNotFound (the row was never
// registered, or was deleted) — a missing compute_node is
// always treated as inactive for the watchdog's purpose. Any
// other error is propagated.
func (e *Engine) computeNodeActive(ctx context.Context, nodeID string) (bool, error) {
	if nodeID == "" {
		return false, nil
	}
	cn, err := e.store.ComputeNodeByID(ctx, nodeID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return cn.Active, nil
}

// Prime boots a freshly-built deployment once, snapshots it, and parks it —
// step 6 of the deploy pipeline (spec §5). schedd runs it on imaged's
// snapshot_prime handshake (ADR-018); on success it emits snapshot_written so
// imaged records the snapshot row and marks the deployment live.
func (e *Engine) Prime(ctx context.Context, appID, deploymentID string) error {
	release := e.lockApp(appID)
	defer release()

	app, acct, limits, err := e.resolveAppForDeploy(ctx, appID)
	if err != nil {
		return err
	}

	// Load the deployment row so layerPath can read the rootfs_path imaged
	// stamped. Missing row (race with apid? — shouldn't happen, schedd only
	// primes after receiving snapshot_prime for a row imaged has already
	// built) is treated as a hard error.
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("sched: prime: load deployment: %w", err)
	}

	// Multi-node placement (issue #97 / ADR-025 axis 3): pick the
	// compute_node for this prime. Prime takes the same placement
	// path as Wake — single-box fleets degenerate to
	// "default-local" because the synthetic row carries the legacy
	// ceiling and there's no other active node.
	// Keep the initial snapshot near the app's assigned node when it fits.
	// The chooser still enforces liveness and CPU/RAM admission.
	placement, err := e.choosePlacementLocked(ctx, Request{
		AppID: appID, Plan: acct.Plan,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency,
		PreferredNodeID: app.NodeID,
	})
	if err != nil {
		return err // *api.Problem from chooser
	}
	// Prime is a wake-shape event (gaps analysis 2026-07-23): the
	// instance is being created for the first time as part of a fresh
	// deploy, so it earns its own wake_id just like Engine.Wake
	// does. UUIDv7 time-orders it with the deploy timestamp. Same
	// fallback-to-v4 contract as Wake() above.
	primeWakeUUID, err := uuid.NewV7()
	if err != nil {
		// Same fallback contract as Wake() above. Review finding #6.
		primeWakeUUID = uuid.New()
		if e.ops != nil {
			e.ops.WakeIDV4Fallback().Inc()
		}
		e.log.Warn("prime: uuid.NewV7 failed, fell back to v4 — partial index time-ordering broken",
			"app", appID, "err", err)
	}
	primeWakeID := primeWakeUUID.String()
	ins, err := e.store.CreateInstanceWithMode(ctx, appID, deploymentID, string(state.StateColdBooting), app.RAMMB, placement.NodeID, primeWakeID, instanceModeForApp(app))
	if err != nil {
		return fmt.Errorf("sched: prime: create instance: %w", err)
	}
	e.emitInstanceChanged(ctx, ins.ID, appID, state.StateColdBooting, primeWakeID)

	if err := e.ledger.Admit(Request{
		Instance: ins.ID, AppID: appID, DeploymentID: deploymentID, Plan: acct.Plan,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency,
		NodeID:        placement.NodeID,
		NodeCeilingMB: placement.CeilingMB,
		VCPUBudget:    placement.VCPUBudget,
	}); err != nil {
		e.transitionWithKind(ctx, ins.ID, appID, state.StateFailed, "wake_boot_error", "prime_admit_denied")
		return err
	}

	// Issue #96 / ADR-025 axis 2 / PR #116: the wake wire carries
	// StorageBackend keys for the base + layer ext4. vmmd resolves
	// them locally via Storage.Get before staging the chroot. The
	// local backend's Get maps the same keys to the same files the
	// legacy *_path fields used, so single-box behaviour is
	// preserved. See pkg/sched/paths.go baseKey / layerKey.
	//
	// PR-B (issue #460 / ADR-053 §Decision 1): env_secrets override
	// filtering — see Wake builder for the full contract. ColdBoot /
	// Prime shares the wake path; the dep row is the same one Wake
	// loaded (so no extra DB read).
	sealedEnv, err := e.loadSealedEnvFor(ctx, acct.ID, appID, dep.Scope, envSecretsFromDep(dep))
	if err != nil {
		e.rollbackAdmittedInstance(ctx, ins.ID, appID, "prime_sealed_env_invalid")
		return fmt.Errorf("sched: prime: load sealed env: %w", err)
	}
	sidecars, err := e.sidecarsForDeployment(ctx, dep)
	if err != nil {
		e.rollbackAdmittedInstance(ctx, ins.ID, appID, "prime_sidecars_invalid")
		return fmt.Errorf("sched: prime: load sidecars: %w", err)
	}
	spec := AppSpec{
		BaseKey: baseKey(app.Runtime), LayerKey: layerKey(dep.RootfsKey, dep.ID),
		VCPUCount: int32(limits.VCPU), MemSizeMiB: int32(app.RAMMB), CPUMillicores: int32(app.CPUMillicores),
		EgressMbit: int32(limits.EgressMbit),
		// M-3: deploy prime uses the same plan-resolved readiness budget
		// as ordinary wakes, so first boot and later wakes agree.
		StartupDeadlineS: startupDeadlineForApp(app, acct.Plan),
		Plan:             acct.Plan, AccountID: acct.ID,
		AppID: appID, DeploymentID: dep.ID,
		SealedEnv: sealedEnv,
		Sidecars:  sidecars,
		// Issue #395 / ADR-045: plaintext api_env layer mirrors the
		// sealed secrets surface but stores non-sensitive runtime
		// config. Precedence at the guest layer is "secrets >
		// api_env > manifest_env > os.environ".
		APIEnv: e.loadAPIEnv(ctx, acct.ID, appID, dep.Scope),
		// ADR-031: see the Wake builder above. Prime is the
		// deploy-pipeline first boot — same wire shape, same
		// per-netns ruleset; a freshly-deployed app starts under
		// its declared egress policy rather than awaiting a later
		// wake.
		EgressAllowlist: prefixesToCIDRStrings(app.EgressAllowlist),
		// ADR-119: see the Wake builder above. Prime threads
		// the customer-supplied static IPv4 (BYOIP, Scale-only)
		// onto the vmmd AppSpec so the per-netns renderer
		// emits the SNAT-to-customer sibling rule.
		StaticEgressIP: staticEgressIPString(app.StaticEgressIP),
		// Issue #470 / PR #470-FU-B: per-deployment runner id
		// (e.g. "node22"). Threaded onto the vmmd AppSpec so
		// the framework_ready DGRAM receipt path can label
		// vmmd_guest_framework_warmup_seconds by runner. See
		// buildAppSpec (engine.go:1757) for the same field
		// wired on the (re)build path. Empty falls back to
		// "unknown" in the histogram observer.
		Runtime: app.Runtime,
	}
	// ADR-038 / Tier 3 phase 3: same verify path as Wake. Prime
	// is the deploy-pipeline first boot; a tampered layer here
	// means imaged shipped something that should never have been
	// allowed out, so the verifier rejection transitions the
	// deployment to DeployFailed the same way. The sig key
	// derivation matches pkg/rootfs/publishExt4's
	// "sigs/<layerKey>.sig" convention.
	if e.verifier != nil {
		if err := e.verifier.Verify(ctx, spec.LayerKey, "sigs/"+spec.LayerKey+".sig"); err != nil {
			var p *api.Problem
			if errors.As(err, &p) && p.Code == api.CodeSigInvalid {
				e.log.Warn("prime: rejecting tampered layer",
					"app", appID, "layer", spec.LayerKey, "err", err)
				e.transitionWithKind(ctx, ins.ID, appID, state.StateFailed, "wake_boot_error", "prime_sig_invalid")
				e.ledger.Release(ins.ID)
				return err
			}
			// Transient I/O — same Retry-After shape as the Wake
			// branch. Wrap as a Problem so gatewayd-internal's writeWakeError
			// flushes both status + header in one path (review
			// finding #1a on PR #322).
			e.log.Warn("prime: verifier i/o error",
				"app", appID, "layer", spec.LayerKey, "err", err)
			e.transitionWithKind(ctx, ins.ID, appID, state.StateFailed, "wake_boot_error", "prime_sig_verify_io")
			e.ledger.Release(ins.ID)
			return api.NewProblem(503, api.CodeCapacity,
				"signature verification storage error",
				fmt.Sprintf("verifier I/O error for layer %q: %v (retry shortly)", spec.LayerKey, err)).
				WithHeader("Retry-After", "5")
		}
	}

	// Per-call deadline (commit 1, spec §6.1). Same rationale as Wake:
	// Prime's vmmd call gets the ColdBootTimeout budget — a Prime
	// that takes longer is dead and the operator should restart
	// imaged's pipeline, not wait for a hung Firecracker.
	bootCtx, pcancel := context.WithTimeout(ctx, e.budgetFor(state.StateColdBooting))
	defer pcancel()
	out, err := e.vmm.CreateColdBoot(bootCtx, placement.NodeID, ins.ID, spec)
	if err != nil {
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, appID, state.StateFailed, "wake_boot_error", "prime_cold_boot_failed")
		return fmt.Errorf("sched: prime: cold boot: %w", err)
	}
	if err := e.store.SetInstanceRuntime(ctx, ins.ID, out.Netns, out.HostIP, int(out.LeaseUID)); err != nil {
		// Best-effort destroy; same rationale as Wake above. Uses a
		// detached context so a cancelled caller ctx doesn't make the
		// destroy fire-and-forget (it would still need its own
		// timeout).
		e.bestEffortDestroy(ctx, placement.NodeID, ins.ID)
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, appID, state.StateFailed, "wake_boot_error", "prime_record_runtime_failed")
		return fmt.Errorf("sched: prime: record runtime: %w", err)
	}
	e.transition(ctx, ins.ID, appID, state.StateRunning)

	// Boot succeeded; snapshot + park it (the prime is not left running).
	ins.AppID, ins.DeploymentID = appID, deploymentID
	return e.snapshotAndPark(ctx, ins)
}

// markPrimeFailed closes the deployment lifecycle when the scheduler cannot
// produce the first snapshot. Prime already transitions its instance to
// FAILED, but historically the deployment row was left in SNAPSHOTTING, so
// the API kept reporting "no live deployment" without an actionable deploy
// failure. This runs after the original error has been logged and is
// intentionally best-effort: the prime error remains the scheduler's primary
// signal, while the persisted deployment/stage state is the customer-facing
// recovery path.
func (e *Engine) markPrimeFailed(ctx context.Context, deploymentID string, cause error) {
	if deploymentID == "" || cause == nil {
		return
	}
	// The notification handler may be unwinding because the scheduler is
	// shutting down. Keep the terminal state write independent of that
	// cancellation, but bound it so shutdown cannot wait on a wedged database.
	markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	dep, err := e.store.DeploymentByID(markCtx, deploymentID)
	if err != nil {
		e.log.Warn("sched: prime failure: load deployment", "deployment", deploymentID, "err", err)
		return
	}
	// A duplicate notification can arrive after another worker has already
	// completed or terminally failed the deployment. Do not let a late prime
	// error overwrite a successful/live or independently terminal result.
	if dep.Status == state.DeployLive || dep.Status.IsTerminal() {
		return
	}

	problem := api.ErrDeployFailed(cause.Error())
	code := api.CodeDeployFailed
	var upstream *api.Problem
	if errors.As(cause, &upstream) && upstream != nil && upstream.Code != "" {
		problem = upstream
		code = upstream.Code
	}
	_ = whycopy.Decorate(problem, code, nil)

	now := time.Now().UTC()
	message := "snapshot prime failed: " + cause.Error()
	if _, err := e.store.SetDeploymentFailedEx(markCtx, deploymentID, code, message, problem.Hint, problem.Why, problem.Fix, nil); err != nil {
		e.log.Warn("sched: prime failure: mark deployment failed", "deployment", deploymentID, "err", err)
	}
	if _, err := e.store.MarkDeploymentStageFailed(markCtx, deploymentID, now, message); err != nil {
		// Older/imported rows may not have a current stage. The deployment
		// status is still terminal and remains the source of truth.
		e.log.Warn("sched: prime failure: mark stage failed", "deployment", deploymentID, "err", err)
	}
}

// Park snapshots a RUNNING instance and frees its RAM (idle reaper, spec §4.3).
// Acquires the app lock; the reaper calls it per selected instance. The reaper
// builds its selection without the lock, so we re-read under the lock and skip
// anything no longer RUNNING (a concurrent wake/park already moved it).
func (e *Engine) Park(ctx context.Context, instanceID string) error {
	ins, err := e.lockedRunning(ctx, instanceID)
	if err != nil || ins == nil {
		return err
	}
	defer e.unlockApp(ins.AppID)
	if err := e.snapshotAndPark(ctx, *ins); err != nil {
		return err
	}
	// Park is also used by operator and quota paths, not only the request
	// reaper. A service replica that reaches PARKED through one of those paths
	// must still trigger the desired-count reconciler; otherwise the service
	// can remain below its configured floor without another lifecycle event.
	if ins.Mode == string(state.InstanceModeService) {
		e.scheduleServiceReconcile(ctx, ins.DeploymentID)
	}
	return nil
}

// ParkWithReason is the meterd-triggered variant (M7, spec §4.7). It
// delegates to Park and stamps a structured log line with the reason
// ("quota_exceeded_free", "manual_admin", etc) so the audit trail can
// answer "why was this instance parked?" without grepping the code.
func (e *Engine) ParkWithReason(ctx context.Context, instanceID, reason string) error {
	err := e.Park(ctx, instanceID)
	if err != nil {
		e.log.Warn("sched: park_with_reason failed", "instance", instanceID, "reason", reason, "err", err)
		return err
	}
	e.log.Info("sched: park_with_reason", "instance", instanceID, "reason", reason)
	return nil
}

// ParkApp tears down every live instance of an app whose lifecycle has already
// been changed to evicted_cold by apid. The app-level park endpoint is
// asynchronous: apid owns the app status write, while schedd owns instance
// snapshots, VM destruction, and instance-state transitions.
//
// The operation is deliberately idempotent. A duplicate notification, a
// retry, or the periodic reconciliation sweep may all call this method. The
// per-app lock serializes it with Wake/Park, and the instance is re-read under
// that lock before each action so a concurrent lifecycle change cannot cause a
// second snapshot or destroy.
//
// It returns the number of instances that were successfully moved out of a
// live state. An app that was unparked before the notification was handled is
// ignored; the status check prevents an old `parked` notification from
// tearing down a newly-woken app.
func (e *Engine) ParkApp(ctx context.Context, appID string) (int, error) {
	if appID == "" {
		return 0, fmt.Errorf("sched: park app: empty app id")
	}

	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return 0, fmt.Errorf("sched: park app: load app %s: %w", appID, err)
	}
	// In the split-node topology every schedd receives the notification.
	// Only the owner may mutate the app's instances. Empty owner/node values
	// preserve the legacy single-box and pre-sharding test posture.
	if e.ownerNodeID != "" && app.NodeID != "" && app.NodeID != e.ownerNodeID {
		return 0, nil
	}
	if app.Status != state.AppEvictedCold {
		return 0, nil
	}

	release := e.lockApp(appID)
	defer release()

	// Re-check after acquiring the lock. A wake may have won the race with
	// the notification and explicitly unparked the app while we waited.
	app, err = e.store.AppByID(ctx, appID)
	if err != nil {
		return 0, fmt.Errorf("sched: park app: reload app %s: %w", appID, err)
	}
	if e.ownerNodeID != "" && app.NodeID != "" && app.NodeID != e.ownerNodeID {
		return 0, nil
	}
	if app.Status != state.AppEvictedCold {
		return 0, nil
	}

	instances, err := e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return 0, fmt.Errorf("sched: park app: list instances %s: %w", appID, err)
	}

	acted := 0
	var errs []error
	for _, candidate := range instances {
		fresh, err := e.store.InstanceByID(ctx, candidate.ID)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			errs = append(errs, fmt.Errorf("instance %s: reload: %w", candidate.ID, err))
			continue
		}

		switch state.State(fresh.State) {
		case state.StateRunning:
			// snapshotAndPark owns the full RUNNING → SNAPSHOTTING →
			// PARKED path and the resident-ledger release.
			if err := e.snapshotAndPark(ctx, fresh); err != nil {
				errs = append(errs, fmt.Errorf("instance %s: snapshot and park: %w", fresh.ID, err))
				continue
			}
			acted++
		case state.StateWaking, state.StateColdBooting:
			// A wake drops appMu during the vmmd RPC. If the park
			// notification wins that interleaving, destroy the in-flight
			// VM and land the row in STOPPED; the wake's phase-4 re-read
			// will then discard its result. This prevents an app parked
			// during a cold wake from becoming RUNNING after the park.
			if err := e.timedDestroy(ctx, fresh.NodeID, fresh.ID, DestroyTimeout); err != nil {
				errs = append(errs, fmt.Errorf("instance %s: destroy in-flight wake: %w", fresh.ID, err))
				continue
			}
			e.ledger.Release(fresh.ID)
			e.transition(ctx, fresh.ID, fresh.AppID, state.StateStopped)
			acted++
		}
	}

	return acted, errors.Join(errs...)
}

// ReconcileLifecycleInstance destroys a VM whose parent app or account is in
// a deletion state. Deletion notifications are only hints, so this method is
// also called by the durable reaper sweep. It is intentionally idempotent:
// Destroy is routed through vmmd's idempotent endpoint, ledger.Release ignores
// unknown reservations, and a second pass sees either STOPPED or the terminal
// account-deletion state.
//
// App deletion lands in STOPPED because the instance row remains useful for
// normal retention and the app can be recreated with the same slug. Account
// deletion lands in StateEvictingAccountDeleting because DeleteAccount's
// 30-day grace walk owns removal of the historical row.
func (e *Engine) ReconcileLifecycleInstance(ctx context.Context, instanceID string) (bool, error) {
	if instanceID == "" {
		return false, fmt.Errorf("sched: lifecycle reconcile: empty instance id")
	}

	ins, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		return false, fmt.Errorf("sched: lifecycle reconcile: load instance %s: %w", instanceID, err)
	}
	app, err := e.store.AppByID(ctx, ins.AppID)
	if err != nil {
		return false, fmt.Errorf("sched: lifecycle reconcile: load app %s: %w", ins.AppID, err)
	}
	if e.ownerNodeID != "" && app.NodeID != "" && app.NodeID != e.ownerNodeID {
		return false, nil
	}

	release := e.lockApp(app.ID)
	defer release()

	// Re-read every decision under the app lock. This serializes deletion
	// cleanup with Wake, Park, and migration's app-level lifecycle writes.
	ins, err = e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		return false, fmt.Errorf("sched: lifecycle reconcile: reload instance %s: %w", instanceID, err)
	}
	app, err = e.store.AppByID(ctx, ins.AppID)
	if err != nil {
		return false, fmt.Errorf("sched: lifecycle reconcile: reload app %s: %w", ins.AppID, err)
	}
	if e.ownerNodeID != "" && app.NodeID != "" && app.NodeID != e.ownerNodeID {
		return false, nil
	}

	// Account deletion takes precedence when both parent rows are being
	// removed. That preserves the account-deletion terminal state expected by
	// the grace-period hard-delete walk.
	accountDeleting := false
	account, accountErr := e.store.AccountByID(ctx, app.AccountID)
	if accountErr != nil && !errors.Is(accountErr, state.ErrNotFound) {
		return false, fmt.Errorf("sched: lifecycle reconcile: load account %s: %w", app.AccountID, accountErr)
	}
	if accountErr == nil {
		accountDeleting = account.Status == state.AccountDeletedPending
	}
	appDeleting := app.Status == state.AppDeleted
	if !accountDeleting && !appDeleting {
		return false, nil
	}

	current := state.State(ins.State)
	if current == state.StateEvictingAccountDeleting {
		if err := e.timedDestroy(ctx, ins.NodeID, ins.ID, DestroyTimeout); err != nil {
			return false, fmt.Errorf("sched: lifecycle reconcile: destroy evicting instance %s: %w", ins.ID, err)
		}
		e.ledger.Release(ins.ID)
		return true, nil
	}
	if !state.IsLive(ins.State) {
		return false, nil
	}

	if accountDeleting {
		if !state.CanTransition(current, state.StateEvictingAccountDeleting) {
			return false, fmt.Errorf("sched: lifecycle reconcile: illegal account-delete edge %s -> %s", current, state.StateEvictingAccountDeleting)
		}
		e.transition(ctx, ins.ID, ins.AppID, state.StateEvictingAccountDeleting)
		marked, err := e.store.InstanceByID(ctx, ins.ID)
		if err != nil {
			return false, fmt.Errorf("sched: lifecycle reconcile: verify account-delete state %s: %w", ins.ID, err)
		}
		if state.State(marked.State) != state.StateEvictingAccountDeleting {
			return false, fmt.Errorf("sched: lifecycle reconcile: account-delete state write did not stick for %s", ins.ID)
		}
		if err := e.timedDestroy(ctx, ins.NodeID, ins.ID, DestroyTimeout); err != nil {
			return false, fmt.Errorf("sched: lifecycle reconcile: destroy account-deleting instance %s: %w", ins.ID, err)
		}
		e.ledger.Release(ins.ID)
		return true, nil
	}

	if err := e.timedDestroy(ctx, ins.NodeID, ins.ID, DestroyTimeout); err != nil {
		return false, fmt.Errorf("sched: lifecycle reconcile: destroy deleted-app instance %s: %w", ins.ID, err)
	}
	if !state.CanTransition(current, state.StateStopped) {
		return false, fmt.Errorf("sched: lifecycle reconcile: illegal app-delete edge %s -> %s", current, state.StateStopped)
	}
	e.transition(ctx, ins.ID, ins.AppID, state.StateStopped)
	stopped, err := e.store.InstanceByID(ctx, ins.ID)
	if err != nil {
		return false, fmt.Errorf("sched: lifecycle reconcile: verify stopped state %s: %w", ins.ID, err)
	}
	if state.State(stopped.State) != state.StateStopped {
		return false, fmt.Errorf("sched: lifecycle reconcile: stopped state write did not stick for %s", ins.ID)
	}
	e.ledger.Release(ins.ID)
	return true, nil
}

// ReconcileDeletedApp is the notification-side app cleanup entry point. The
// app row is soft-deleted, so ListInstancesForApp remains available even after
// DELETE /v1/apps/{slug} has returned. The per-instance method rechecks the
// app/account status under the app lock before touching a VM.
func (e *Engine) ReconcileDeletedApp(ctx context.Context, appID string) (int, error) {
	if appID == "" {
		return 0, fmt.Errorf("sched: deleted-app reconcile: empty app id")
	}
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return 0, fmt.Errorf("sched: deleted-app reconcile: load app %s: %w", appID, err)
	}
	if app.Status != state.AppDeleted {
		return 0, nil
	}
	if e.ownerNodeID != "" && app.NodeID != "" && app.NodeID != e.ownerNodeID {
		return 0, nil
	}
	instances, err := e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return 0, fmt.Errorf("sched: deleted-app reconcile: list app %s: %w", appID, err)
	}

	acted := 0
	var errs []error
	for _, candidate := range instances {
		ok, err := e.ReconcileLifecycleInstance(ctx, candidate.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("instance %s: %w", candidate.ID, err))
			continue
		}
		if ok {
			acted++
		}
	}
	return acted, errors.Join(errs...)
}

// Evict destroys a RUNNING instance under RAM pressure (spec §4.3). Unlike Park
// it does not snapshot — the next wake cold-boots (ADR-005), so the state lands
// in STOPPED rather than PARKED.
func (e *Engine) Evict(ctx context.Context, instanceID string) error {
	ins, err := e.lockedRunning(ctx, instanceID)
	if err != nil || ins == nil {
		return err
	}
	defer e.unlockApp(ins.AppID)

	// Per-call deadline (commit 1). Evict is RAM-pressure, so a wedged
	// Destroy cannot pin the reaper — the deadline frees it. Using a
	// detached context for the same reason as the Wake/Prime error
	// paths: a shutting-down reaper should still get its destroy
	// cleanup.
	if err := e.timedDestroy(ctx, ins.NodeID, instanceID, DestroyTimeout); err != nil {
		return fmt.Errorf("sched: evict: destroy %s: %w", instanceID, err)
	}
	e.ledger.Release(instanceID)
	e.transition(ctx, instanceID, ins.AppID, state.StateStopped)
	return nil
}

// StopInstance (M-2 / ADR-138 §Decision 1) is the engine-side
// graceful stop sequence. Distinct from Park (snapshot+park,
// preserves snapshot cache) and Evict (hard destroy, RAM
// pressure): StopInstance honours the per-app StopSignal +
// StopGracePeriod on the OCI lifecycle contract, escalates to
// SIGKILL via vmmd's SignalAndKill (commit 5) when the grace
// timer expires, and finally tears the chroot / cgroup down
// via DestroyWithExport. State lands in STOPPED (no snapshot —
// per ADR-138 worker/job instances are not snapshotted because
// their on-disk state is reconstructed on the next cold boot).
//
// Dispatch (ADR-137 §Decision 1):
//
//	mode='worker'  → SignalAndKill (SIGTERM by default, or
//	                 manifest.StopSignal) + DestroyWithExport +
//	                 transition to STOPPED. Worker idle-reaper
//	                 exempt already widened in reaper.go (this
//	                 commit).
//	mode='job'     → Same as worker (SignalAndKill + destroy).
//	                 RestartPolicy='no' is the M-2 default for
//	                 job mode, so no replacement wake is
//	                 scheduled.
//	mode='service' → SnapshotAndPark preserves the snapshot
//	                 cache (a service replica's snapshot is
//	                 shared with the desired-replica wake path).
//	                 If the deployment's running count has
//	                 dropped below desired, schedule a
//	                 replacement wake so the replica set
//	                 converges.
//	mode='request' (default) → SnapshotAndPark (existing path).
//
// The mode-aware dispatch lives here, NOT in vmmd, because the
// replica-counting logic (decrement on transition + trigger
// replacement wake for service) requires the engine's app lock
// and the deployment state machine. vmmd is stateless about
// app-level shape — it only knows about per-instance lifecycles.
//
// Returns the captured exit code + killSignalSent so callers
// (the vmmdgrpc StopInstance path or the cron shutdown trigger)
// can stamp an audit row with lifecycle_failure_reason.
func (e *Engine) StopInstance(ctx context.Context, instanceID string, opts StopOptions) (StopOutcome, error) {
	const op = "StopInstance"
	ins, err := e.lockedRunning(ctx, instanceID)
	if err != nil || ins == nil {
		return StopOutcome{}, err
	}
	defer e.unlockApp(ins.AppID)

	mode := state.InstanceMode(ins.Mode)
	switch mode {
	case state.InstanceModeWorker, state.InstanceModeJob:
		// Signal-grace-SIGKILL sequence (ADR-138 §Decision 1).
		// signal=0 → SIGTERM default; graceSeconds comes from
		// manifest.StopGracePeriodS capped at the per-plan tier
		// (commit 10).
		signal := syscall.Signal(opts.Signal)
		if signal == 0 {
			signal = syscall.SIGTERM
		}
		out, serr := e.vmm.StopInstanceOnNode(ctx, ins.NodeID, instanceID, int32(signal), int32(opts.GraceSeconds))
		if serr != nil {
			e.log.Warn("sched: stop instance signal-grace failed; falling through to destroy",
				"op", op, "instance", instanceID, "err", serr)
		}
		// Detached destroy: the caller's ctx may have been
		// cancelled (e.g. a process-group shutdown), but the
		// invariant §6.2-4/5 cleanup still owes us a
		// chroot/cgroup release.
		destroyCtx := context.WithoutCancel(ctx)
		if derr := e.timedDestroy(destroyCtx, ins.NodeID, instanceID, DestroyTimeout); derr != nil {
			return StopOutcome{}, fmt.Errorf("sched: stop instance destroy %s: %w", instanceID, derr)
		}
		e.ledger.Release(instanceID)
		e.transition(ctx, instanceID, ins.AppID, state.StateStopped)
		// Worker/job: NO replacement wake. RestartPolicy='no'
		// for job mode (M-2 default); RestartPolicy='always'
		// for worker mode causes the supervisor (commit 7) to
		// re-exec the workload inside the same VM — the
		// instance row stays RUNNING, only the inner child
		// restarts. The engine does not schedule a new
		// instance for either mode; the workload's contract
		// owns its own resurrection.
		//
		// Guard out == nil: vmmd RPC failure returns (nil, err).
		// Without this guard the deref on out.ExitCode below
		// would panic, schedd would crash, and the orphaned
		// STOPPED row would never get reaped (netns/chroot/
		// cgroup leak).
		outcome := StopOutcome{
			Instance:        instanceID,
			Mode:            string(mode),
			LifecycleReason: LifecycleReasonCleanExit,
		}
		if out != nil {
			outcome.ExitCode = out.ExitCode
			outcome.KillSignalSent = out.KillSignalSent
		}
		return outcome, nil
	case state.InstanceModeService:
		// Service replicas converge to desired. snapshotAndPark
		// preserves the snapshot cache the desired-replica wake
		// path reads; after the park, count live service
		// instances vs desired and schedule a replacement wake
		// if we're under.
		if err := e.snapshotAndPark(ctx, *ins); err != nil {
			return StopOutcome{}, fmt.Errorf("sched: stop service instance park %s: %w", instanceID, err)
		}
		// Converge: if running count < desired, schedule a
		// replacement wake. Best-effort — a failed wake is
		// observed at the next admission tick. The async
		// pattern matches Engine.Wake's own deferred-wake
		// behaviour.
		e.scheduleServiceReconcile(ctx, ins.DeploymentID)
		return StopOutcome{
			Instance:        instanceID,
			Mode:            string(mode),
			LifecycleReason: LifecycleReasonCleanExit,
		}, nil
	default:
		// mode='request', 'mirror', or unknown (the mirror path
		// is unreachable — mirror VM lifecycle is owned by the
		// mirror goroutine which calls ParkInstance, but we
		// route here for consistency).
		if err := e.snapshotAndPark(ctx, *ins); err != nil {
			return StopOutcome{}, fmt.Errorf("sched: stop instance park %s: %w", instanceID, err)
		}
		return StopOutcome{
			Instance:        instanceID,
			Mode:            string(mode),
			LifecycleReason: LifecycleReasonCleanExit,
		}, nil
	}
}

// StopOptions is the input shape for Engine.StopInstance.
// Signal is a POSIX signal number (0 = use manifest.StopSignal,
// defaulting to SIGTERM). GraceSeconds is the upper bound on
// clean-shutdown wait in seconds (0 = immediate SIGKILL, the
// legacy Destroy shape — distinct semantics from the snapshot
// path).
type StopOptions struct {
	Signal       int32
	GraceSeconds int32
}

// StopOutcome is the engine-side result of Engine.StopInstance.
// ExitCode + KillSignalSent are surfaced from vmmd's
// SignalAndKill for the worker/job dispatch path; the request /
// service / mirror path leaves both at zero (snapshotAndPark
// doesn't capture an exit code). LifecycleReason is the
// engine-side aggregation — today always LifecycleReasonCleanExit
// for the success path; a future taxonomy addition can
// distinguish killed_after_grace (grace expired + SIGKILL
// escalation) from the clean-exit case.
type StopOutcome struct {
	Instance        string
	Mode            string
	ExitCode        int32
	KillSignalSent  bool
	LifecycleReason LifecycleReason
}

// LifecycleReason is the engine-side reason code for a StopInstance
// transition. M-2 surfaces two: clean_exit (workload exited
// within the grace window) and unknown (legacy / no-grace
// path). Future taxonomy additions (killed_after_grace,
// oom, sigkilled_timeout) land alongside the per-mode
// expansion in M-3.
type LifecycleReason string

const (
	// LifecycleReasonCleanExit is the success path: the
	// workload exited cleanly within the grace window
	// (worker/job) or snapshotAndPark succeeded (request/
	// service).
	LifecycleReasonCleanExit LifecycleReason = "clean_exit"
)

// lockedRunning loads an instance, takes its app lock, and returns it only if it
// is still RUNNING under the lock. A (nil, nil) return means "not RUNNING, skip"
// and the app lock has already been released. On a real error the lock is not
// held. Callers that get a non-nil instance own the lock and must unlockApp.
func (e *Engine) lockedRunning(ctx context.Context, instanceID string) (*state.Instance, error) {
	ins, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("sched: load instance %s: %w", instanceID, err)
	}
	e.lockApp(ins.AppID)
	fresh, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		e.unlockApp(ins.AppID)
		return nil, fmt.Errorf("sched: reload instance %s: %w", instanceID, err)
	}
	if fresh.State != string(state.StateRunning) {
		e.unlockApp(ins.AppID)
		return nil, nil
	}
	return &fresh, nil
}

// ReportActivity persists a batch of last_request_at touches from the gateway
// (spec §4.1, ADR-018). schedd is the sole writer to instances, so the gateway
// hands it the batch instead of writing directly.
//
// ADR-098 C9: the gateway's per-instance cache (Target.RequestCount)
// ships the per-instance request_count delta on the same touch
// batch. The engine flushes both last_request_at and the delta
// atomically via TouchInstancesWithRequestDelta. The delta is
// additive ("request_count = request_count + delta") so a
// re-delivered batch is idempotent on Phase-4-loser re-applies.
//
// Why piggyback on ReportActivity rather than spin a separate
// 250ms goroutine:
//   - The gateway already batches touches every 1–2s (per
//     gateway's own batched timer); a per-instance delta on the
//     same touch turns the writer into a one-round-trip UPDATE
//     instead of N per-instance ORMs.
//   - The "batched on the gateway" cadence is the load-bearing
//     amortization: the same gateway-side timer that absorbs
//     last_request_at noise also absorbs request_count noise.
//   - Production test (issue #543, framework_ready_at) uses the
//     same pattern: stateful stamp piggybacked on a batched RW
//     rather than a hot-loop per-instance ORM.
//
// Metric: schedd_instance_request_count_flushed_delta_total is
// bumped with the post-flush delta count, tagged by outcome
// (success / dropped-or-error). Dropped flushes are visible on
// the §12 dashboard.
func (e *Engine) ReportActivity(ctx context.Context, touches []state.InstanceTouch) (int, error) {
	// Pre-filter: touches with RequestDelta == 0 fall back to the
	// legacy last_request_at-only path. The split keeps the
	// existing ORM narrow for the case where the gateway's
	// per-instance cache is fresh (no incremental delta yet).
	needDelta := false
	for _, t := range touches {
		if t.RequestDelta != 0 {
			needDelta = true
			break
		}
	}
	if !needDelta {
		return e.store.TouchInstancesLastSeen(ctx, touches)
	}
	n, err := e.store.TouchInstancesWithRequestDelta(ctx, touches)
	return n, err
}

// SeedLedger rebuilds the admission ledger from live instance rows at startup so
// the RAM/concurrency accounting survives a schedd restart (spec §4.3). Called
// once by cmd/schedd before the loop starts serving.
//
// Per-node accounting (issue #97 / ADR-025 axis 3): each instance row
// carries its compute_node.id (PR #112). SeedLedger threads that into
// the Admit request so the per-node resident counter on every node is
// rebuilt correctly. A row whose node_id is empty (pre-#97 fixture)
// falls back to the default-local node id so legacy tests still
// rebuild.
func (e *Engine) SeedLedger(ctx context.Context) error {
	apps, err := e.store.ListAllApps(ctx)
	if err != nil {
		return fmt.Errorf("sched: seed ledger: list apps: %w", err)
	}
	// Per-node ceiling cache so we don't fire a ComputeNodeByID
	// per instance row (PR scale-out readiness #4, this would
	// otherwise be O(instances × nodes) on a busy fleet). The
	// map is local to SeedLedger — once the daemon's loop is
	// running, choosePlacementLocked reloads from the store on
	// every wake so a node row edited at runtime is picked up
	// the next time the chooser runs.
	ceilings := map[string]int{}
	budgets := map[string]int{}
	loadCeiling := func(ctx context.Context, nodeID string) int {
		if c, ok := ceilings[nodeID]; ok {
			return c
		}
		n, err := e.store.ComputeNodeByID(ctx, nodeID)
		if err != nil || n.AdmissionCeilingMB <= 0 {
			ceilings[nodeID] = 0
			return 0
		}
		ceilings[nodeID] = n.AdmissionCeilingMB
		return n.AdmissionCeilingMB
	}
	// Tier A2: per-node vCPU budget, paralleling loadCeiling.
	// Missing rows / zero budgets cache as 0; the Admit path
	// falls back to api.VCPUSlots when VCPUBudget<=0, so a
	// fresh deployment with no compute_nodes row degrades
	// gracefully to the legacy single-box posture.
	loadVCPUBudget := func(ctx context.Context, nodeID string) int {
		if b, ok := budgets[nodeID]; ok {
			return b
		}
		n, err := e.store.ComputeNodeByID(ctx, nodeID)
		if err != nil || n.VCPUBudget <= 0 {
			budgets[nodeID] = 0
			return 0
		}
		budgets[nodeID] = n.VCPUBudget
		return n.VCPUBudget
	}
	for _, app := range apps {
		acct, err := e.store.AccountByID(ctx, app.AccountID)
		if err != nil {
			continue
		}
		limits, ok := api.LimitsFor(acct.Plan)
		if !ok {
			continue
		}
		instances, err := e.store.ListInstancesForApp(ctx, app.ID)
		if err != nil {
			continue
		}
		for _, ins := range instances {
			if !state.State(ins.State).CountsForRAM() {
				continue
			}
			nodeID := ins.NodeID
			if nodeID == "" {
				nodeID = e.defaultLocalNodeID
			}
			if err := e.ledger.Admit(Request{
				Instance: ins.ID, AppID: app.ID, Plan: acct.Plan,
				RAMMB: ins.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency,
				NodeID:        nodeID,
				NodeCeilingMB: loadCeiling(ctx, nodeID),
				VCPUBudget:    loadVCPUBudget(ctx, nodeID),
			}); err != nil {
				e.log.Warn("seed ledger: admit", "instance", ins.ID, "err", err)
				continue
			}
			// SNAPSHOTTING is resident but no longer counts toward concurrency.
			if state.State(ins.State) == state.StateSnapshotting {
				e.ledger.BeginSnapshot(ins.ID)
			}
		}
	}
	return nil
}

// vmstateHostPathFor returns the deterministic host path the single-box
// vmstate file lives at — the same value the legacy `caller-supplied
// VMStatePath` used to be. We reconstruct it on every wake (not just
// on park) so fcvm.Snapshot.Usable() continues to hold when vmmd's
// VMStateStorageKey is empty (default-local branch). #121 / ADR-025
// axis 2 slice 4; closes the cold-boot-regression that surfaced
// during the #121 exploration (wake had been sending empty
// VMStatePath since migration 23 dropped snapshots.path).
func (e *Engine) vmstateHostPathFor(depID string) string {
	return SnapDir() + "/" + depID + "/vmstate"
}

// vmstateStorageKeyFor returns the canonical StorageBackend key the
// vmstate blob is published under, or "" when this node should
// continue using the host-path legacy layout (default-local).
//
// The branch discriminator is the node identity, NOT the
// StorageBackend's nilness: production cmd/vmmd always wires a
// non-nil StorageBackend (cmd/vmmd/main.go:126-148), so a
// `v.storage != nil` style guard would falsely route default-local
// through the local backend and break the host-path behaviour the
// engine relies on. #121 / ADR-025 axis 2 slice 4.
//
// Empty result for default-local means vmmd's
// `spec.VMStateStorageKey == ""` branch lands on the legacy
// `moveOut(spec.VMStatePath)` path. Populated result for remote
// nodes means vmmd publishes via `storage.Put` at the canonical
// snap/<dep>/vmstate key the OCI driver already understands
// (pkg/storage/oci.go:272-280). defaultLocalNodeID is resolved at
// engine construction (see NewEngine + defaultLocalNodeID lookup)
// so the identity check here is a stable UUID compare rather than
// a string match against the synthetic row's name.
func (e *Engine) vmstateStorageKeyFor(nodeID, depID string) string {
	if nodeID == "" {
		// Defensive: an empty nodeID here is a misroute, not default-local
		// (those have a real UUID resolved at construction). Falling
		// through to "" routes the wake to vmmd's legacy host-path
		// branch, which preserves single-box semantics but masks the
		// upstream bug. Surfacing a Warn here so dev / staging catches
		// placement decisions that omit node_id at the source.
		if e.log != nil {
			e.log.Warn("engine: vmstateStorageKeyFor called with empty nodeID; routing to host-path fallback",
				"deployment_id", depID)
		}
		return ""
	}
	if nodeID == e.defaultLocalNodeID {
		return ""
	}
	return state.SnapVMStateKey(depID)
}

// snapshotAndPark is the unlocked park core (caller holds the app lock). It
// walks RUNNING → SNAPSHOTTING → PARKED, writing the snapshot blob via vmmd and
// emitting snapshot_written for imaged to record the row.
func (e *Engine) snapshotAndPark(ctx context.Context, ins state.Instance) error {
	// Issue #667 / ADR-078 — waitUntil drain watchdog. If the instance
	// has active waitUntil tasks (ins.TailCount > 0), the runner is
	// still draining them in-process after the response was flushed.
	// The reaper gate (pkg/sched/reaper.go) keeps the reaper from
	// picking this instance up while TailCount > 0, but a hard park
	// caller (eviction, manual park, ParkNow) can still land here.
	// We give the runner ParkTailDrainTimeoutSeconds (5 s — equal to
	// the Free plan's TailTimeoutS floor so the watchdog is always
	// shorter than the longest per-plan timeout) to drain the tail
	// before the watchdog force-parks. On the watchdog path we emit
	// `wake.tail_failed{reason=forced_at_park}` per unfinished tail
	// and an audit row keyed by "tail_count_force_park" so an
	// operator can spot a host-side drain stall.
	//
	// We poll the SQL tail_count via the store interface rather than
	// a channel because vmmd is the canonical owner of the per-task
	// 0x04 DGRAM fan-in and the SQL tail_count is the cross-host
	// truth. Polling at 200 ms keeps the read load on PG bounded
	// even at fleet scale (60 nodes × 16 instances × 5 Hz = 4 800
	// SELECT/s of TAIL_COUNT — negligible next to the meterd cron).
	if ins.TailCount > 0 {
		deadline := time.Now().Add(time.Duration(api.ParkTailDrainTimeoutSeconds) * time.Second)
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if time.Now().After(deadline) {
				// Watchdog fired. Re-read the live tail_count from
				// the store rather than relying on the in-memory
				// ins.TailCount snapshot taken at function entry:
				// a fresh tail registered between the entry read
				// and the deadline (e.g. a second request arri­ved
				// while we polled) MUST be counted, otherwise those
				// tasks are silently lost when the runner exits
				// after the watchdog force-parks. The fresh read
				// also covers the symmetrical case where terminal
				// receipts drained the counter past the stale
				// snapshot — we emit exactly the live unfinished
				// count and decrement by exactly that much.
				liveCount, liveErr := e.store.GetInstanceTailCount(ctx, ins.ID)
				n := int64(ins.TailCount)
				if liveErr == nil && liveCount > 0 {
					n = int64(liveCount)
				}
				// Log the stall so an operator can correlate with
				// the runner's tail-host log lines.
				e.log.Warn("snapshotAndPark: tail drain watchdog fired",
					"instance", ins.ID,
					"ins_tail_count", ins.TailCount,
					"live_tail_count", n,
					"watchdog_seconds", api.ParkTailDrainTimeoutSeconds)
				// Emit one forced-at-park audit row per unfinished tail
				// (best-effort: bails out on context cancel). The event
				// warehouse dedupes on (instance_id, wake_id, kind) so a
				// late DGRAM that lands after the row is harmless.
				for i := int64(0); i < n; i++ {
					if e.events != nil {
						e.events.Emit(ctx, events.TailFailed{
							EmitAt:     time.Now().UTC(),
							AppID:      ins.AppID,
							InstanceID: ins.ID,
							Reason:     "forced_at_park",
						})
					}
				}
				// Floor tail_count at 0 before transitioning so the
				// wake.row + the next meterd tick are consistent.
				_ = e.store.DecrementInstanceTailCount(ctx, ins.ID, int32(n))
				break
			}
			// Read the fresh tail_count from the store. The pgstore
			// implementation is a single SELECT … FROM instances
			// WHERE id = $1 — no row lock, no contention with the
			// vmmd BumpInstanceTailCount path.
			cur, err := e.store.GetInstanceTailCount(ctx, ins.ID)
			if err != nil {
				// Best-effort: a transient store error means we
				// cannot make the "drained" decision, so we let
				// the watchdog decide on the next loop.
				e.log.Warn("snapshotAndPark: tail count read",
					"instance", ins.ID, "err", err)
			} else if cur == 0 {
				break
			}
			// Sleep 200 ms. Select on the context as well so a
			// caller-cancel propagates promptly.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	storageKey := state.SnapshotCaptureMemKey(ins.DeploymentID, state.SnapshotTierInit, uuid.NewString())
	vmstateKey := state.SnapshotVMStateKey(state.Snapshot{StorageKey: storageKey})
	vmstate := filepath.Join(SnapDir(), strings.TrimPrefix(vmstateKey, "snap/"))
	vmstateStorageKey := vmstateKey
	e.ledger.BeginSnapshot(ins.ID) // drops concurrency, keeps RAM (§6.2-1 excludes snapshotting)
	// Stamp parked_at on entry into SNAPSHOTTING so the §6.1 watchdog
	// (commit 3) has an "age of state" anchor for the row.
	now := time.Now()
	if err := e.store.UpdateInstanceStateWithTimestamp(ctx, ins.ID, string(state.StateSnapshotting), now); err != nil {
		e.log.Warn("snapshotAndPark: stamp parked_at", "instance", ins.ID, "err", err)
		// Fall through to the normal path — the watchdog's beginSnapshot
		// anchor being lost is recoverable (it'll trip after
		// started_at + 20s, slightly inflating the budget).
	}
	e.emitInstanceChanged(ctx, ins.ID, ins.AppID, state.StateSnapshotting, ins.WakeID)
	// issue #517 / PR-C / ADR-064 — emit wake.park_started at the
	// RUNNING→SNAPSHOTTING transition. Pairs with wake.park_completed
	// below under the same wake_id so the timeline endpoint can
	// surface "park took N ms" without joining the legacy
	// state_transition rows. The wake_id is the one the just-finished
	// boot produced (ins.WakeID), per ADR-035 the audit join key.
	if e.events != nil {
		e.events.Emit(ctx, events.ParkStarted{
			EmitAt:       now.UTC(),
			WakeID:       ins.WakeID,
			AppID:        ins.AppID,
			DeploymentID: ins.DeploymentID,
			InstanceID:   ins.ID,
			NodeID:       ins.NodeID,
			StartedAt:    now.UTC(),
		})
	}

	// issue #470 / PR A / ADR-070 — warm capture runs FIRST, while
	// the VM is still live (RUNNING). The init tier's trailing Kill
	// in vmm.Snapshot would otherwise destroy the VM the warm tier
	// is trying to snapshot. Reordering also matches §6.3's warm-wake
	// budget semantic: the runner is alive across the pause window
	// and only the init tier's trailing Kill ends the customer-
	// visible lifetime.
	//
	// capturedWarm returns (info, err). On error the helper has
	// already Destroyed the VM and transitioned the row to STOPPED
	// (the state machine forbids PARKED→STOPPED, so the warm
	// failure must land BEFORE the PARKED transition).
	warmInfo, warmErr := e.captureWarmSnapshotLocked(ctx, ins)
	if warmErr != nil {
		// Init blob on disk is orphaned — the next wake cold-boots
		// (ADR-005) and PR C's GC sweep evicts the orphaned init row.
		e.log.Warn("sched: capture warm snapshot failed", "instance", ins.ID, "err", warmErr)
		return warmErr
	}
	_ = warmInfo

	snapBudget := SnapshotBudgetFor(ins.RAMMB)
	snapCtx, snapCancel := context.WithTimeout(ctx, snapBudget)
	snapStart := time.Now()
	b, reused, err := e.captureInitOrReuse(snapCtx, ins, vmstate, storageKey, vmstateStorageKey)
	if reused != nil {
		storageKey = reused.StorageKey
	}
	snapCancel()
	// The park path has no phase instrumentation (unlike wakePhases on
	// the boot path), so at minimum record how long the capture ran
	// against the budget it was given — enough to tell "budget too
	// tight" from "vmmd wedged" on the next occurrence.
	if snapMS := time.Since(snapStart).Milliseconds(); err != nil || snapMS > snapBudget.Milliseconds()/2 {
		e.log.Warn("sched: snapshot capture timing",
			"instance", ins.ID, "ram_mb", ins.RAMMB,
			"snapshot_ms", snapMS, "budget_ms", snapBudget.Milliseconds(),
			"err", err)
	}
	if err != nil {
		// Snapshot failed (disk?) — free RAM and land in STOPPED; next wake
		// cold-boots (ADR-005). The app still has a cold-bootable rootfs (§6.2-3).
		// Audit-log it as park_snapshot_error (per the kind taxonomy) so
		// "all park-snapshot failures in the last hour" is queryable.
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, ins.AppID, state.StateStopped, "park_snapshot_error", "snapshot_failed")
		return fmt.Errorf("sched: park: snapshot %s: %w", ins.ID, err)
	}
	e.ledger.Release(ins.ID)
	e.transition(ctx, ins.ID, ins.AppID, state.StateParked)
	// issue #517 / PR-C / ADR-064 — emit wake.park_completed on the
	// successful park. The snapshot_id is the storage key the next
	// wake will use to restore (per ADR-025 axis 2), so the timeline
	// row lets an operator trace "this wake restored from the
	// snapshot produced by that wake" by joining the storage_key
	// back to the upcoming wake.row's restore_path metadata.
	if e.events != nil {
		e.events.Emit(ctx, events.ParkCompleted{
			EmitAt:       time.Now().UTC(),
			WakeID:       ins.WakeID,
			AppID:        ins.AppID,
			DeploymentID: ins.DeploymentID,
			InstanceID:   ins.ID,
			NodeID:       ins.NodeID,
			StartedAt:    now.UTC(),
			CompletedAt:  time.Now().UTC(),
			SnapshotID:   storageKey,
		})
	}
	// Init-tier capture is the "cold" snapshot the next wake falls back
	// to when no warm row exists or the plan no longer allows warm
	// (issue #470 / PR A / ADR-070). Tagged tier="init" so the
	// snapshot_written payload can be routed by imaged's row writer.
	// The warm tier's row is owned by imaged's snapshot_written
	// subscriber (PR #525) — the engine emits a sibling notify
	// payload (tier=warm) from captureWarmSnapshotLocked on the
	// success path so imaged writes both rows. The engine does NOT
	// write the warm row directly to avoid a unique-violation on
	// (deployment_id, tier) between engine and imaged.
	if reused == nil {
		e.emitSnapshotWritten(ctx, ins.DeploymentID, ins.NodeID, vmstate, storageKey, b, state.SnapshotTierInit)
	}
	return nil
}

// captureWarmSnapshotLocked (issue #470 / PR A / ADR-070) is the
// warm-tier counterpart to snapshotAndPark's init-tier capture. It
// runs under appMu inside the Park site — caller already holds the
// lock. The five gates (any one fails → no warm capture) are:
//  1. app.WarmSnapshotEnabled (the operator-opt-in flag — sticky on
//     plan downgrade per ADR-070 §Plan gate)
//  2. acct.Plan.WarmSnapshotAllowed() (the plan-gate that rejects
//     warm at wake time anyway; doing it here too avoids burning
//     pause/resume cycles for nothing)
//  3. ins.FrameworkReadyAt != nil (the PR #543 stamp — a freshly
//     primed instance is not yet warm; this is the only difference
//     between the manual Prime path and the reaper-driven Park path)
//  4. now - FrameworkReadyAt >= app.WarmSnapshotMinMs (the
//     time-since-first-ready floor; A.3 covers the time half of
//     the gate)
//  5. ins.RequestCount >= app.WarmSnapshotMinRequests (ADR-098
//     C10, supersedes ADR-071 PR-C). The per-instance request-
//     count half of the gate. min == 0 is the "disabled" label
//     case (Free/Hobby default) — the gate never opens. The
//     comparison is `count < min` (threshold), not
//     `count % min == 0` (multiple-of-5 regression).
//
// On success: returns (nil, info) after emitting snapshot_written
// {tier:warm}. imaged's snapshot_written subscriber (PR #525) is
// the SINGLE writer of the warm-tier snapshots row — the engine
// does NOT call store.CreateSnapshot for tier=warm directly, to
// avoid a (deployment_id, tier) unique-violation with imaged's
// subscriber. The caller (snapshotAndPark) threads the byte counts
// through for log side-effects only; the audit row's MemBytes come
// from the imaged-side write.
//
// On failure: Destroy + ledger.Release + transitionWithKind(STOPPED,
// warm_capture_error, warm_snapshot_failed). The warm capture runs
// BEFORE the init capture (caller's responsibility), so the VM is
// still live at this point — Destroy releases the jailer / cgroup /
// netns / chroot cleanly. The init capture NEVER runs in this branch
// (the caller returns early). The next wake cold-boots (ADR-005).
func (e *Engine) captureWarmSnapshotLocked(ctx context.Context, ins state.Instance) (SnapshotBytes, error) {
	// Gate 1 + 2: the easy cheap read. Load app + account once so
	// the warm-failure path can also seal the audit row with the
	// correct app/account pair.
	app, err := e.store.AppByID(ctx, ins.AppID)
	if err != nil {
		return SnapshotBytes{}, fmt.Errorf("sched: warm capture: load app: %w", err)
	}
	if !app.WarmSnapshotEnabled {
		return SnapshotBytes{}, nil
	}
	acct, err := e.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return SnapshotBytes{}, fmt.Errorf("sched: warm capture: load account: %w", err)
	}
	if !acct.Plan.WarmSnapshotAllowed() {
		return SnapshotBytes{}, nil
	}

	// Gate 3 + 4: framework-ready stamp + time-since-first-ready
	// floor. A nil FrameworkReadyAt means the runner never sent
	// its DGRAM (it crashed, never woke, or the warmup timed out).
	// WarmSnapshotMinMs is bounded [100, 60000] by apid's edit
	// validator (handlers_ext.go:249-257), so denormalising to a
	// duration is safe.
	if ins.FrameworkReadyAt == nil {
		return SnapshotBytes{}, nil
	}
	if app.WarmSnapshotMinMs > 0 {
		minAge := time.Duration(app.WarmSnapshotMinMs) * time.Millisecond
		if time.Since(*ins.FrameworkReadyAt) < minAge {
			return SnapshotBytes{}, nil
		}
	}

	// Gate 5 (ADR-098 C10, supersedes ADR-071 PR-C): per-instance
	// request-count floor. The plan is the load-bearing knob: with
	// min > 0 the promotion only fires once the instance has served
	// at least `min` requests. min == 0 is the "disabled" label
	// case (Free/Hobby default) — the gate never opens, AND the
	// engine does NOT spin a warm snapshot, so a Free app with
	// min=0 never sees a warm tier (per ADR-071 §Plan gate, the
	// plan forbids it anyway, but the explicit zero guard is
	// belt-and-braces against a Free app that somehow reaches
	// here).
	//
	// The comparison is `count < min` not `count % min == 0`. The
	// latter is the multiple-of-5 regression (issue #675 review
	// finding): a cold app requests_count=3 with min=5 would
	// never promote (3 % 5 = 3, not 0), and the warm tier would
	// stay cold indefinitely. The threshold is a "have we
	// crossed the threshold?" comparison, not a "is this a
	// multiple?" one.
	//
	// Why mirror on the instance row (not an engine cache):
	//   The column is the authoritative counter; the engine
	//   reads ins.RequestCount (mirror on the Instance struct)
	//   on every park site. The batched writer flushes
	//   gateway-side per-instance counts every 1–2s (C9), and
	//   the engine reads the latest value at gate time. There
	//   is no in-process cache to drift.
	if app.WarmSnapshotMinRequests > 0 && ins.RequestCount < int64(app.WarmSnapshotMinRequests) {
		return SnapshotBytes{}, nil
	}

	// Compute the per-tier storage keys. The /warm/ segment keeps
	// the blobs physically separate from the init tier so imaged's
	// per-tier GC (PR C) can keep 2+2 without conflating them.
	if _, ok := e.reusableSnapshot(ctx, ins.DeploymentID, state.SnapshotTierWarm); ok {
		return SnapshotBytes{}, nil
	}
	warmMemKey := state.SnapshotCaptureMemKey(ins.DeploymentID, state.SnapshotTierWarm, uuid.NewString())
	warmVMStateStorageKey := state.SnapshotVMStateKey(state.Snapshot{StorageKey: warmMemKey})

	warmCtx, warmCancel := context.WithTimeout(ctx, SnapshotBudgetFor(ins.RAMMB))
	b, err := e.vmm.WarmSnapshot(warmCtx, ins.NodeID, ins.ID, warmMemKey, warmVMStateStorageKey)
	warmCancel()
	if err != nil {
		// Warm capture failed. The VM may be in a wedged state
		// (paused — vmmd did the pause but the snapshot RPC
		// failed before resume). Destroy + release + STOPPED so
		// the next wake can cold-boot cleanly. Skip the init
		// capture: there's no warm AND no init to keep — the
		// operator gets a cold-boot next wake (ADR-005).
		destroyErr := e.vmm.Destroy(ctx, ins.NodeID, ins.ID)
		if destroyErr != nil {
			e.log.Warn("sched: warm capture: destroy after snapshot failure", "instance", ins.ID, "err", destroyErr)
		}
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, ins.AppID, state.StateStopped, "warm_capture_error", "warm_snapshot_failed")
		if e.ops != nil {
			e.ops.WarmSnapshotErrors("vmm_call").Inc()
		}
		return SnapshotBytes{}, fmt.Errorf("sched: warm capture: vmm WarmSnapshot: %w", err)
	}

	// Success: emit snapshot_written{tier:warm} so imaged's
	// subscriber writes the warm-tier row (PR #525). The init
	// capture (running next in the caller's Park sequence) emits
	// its own snapshot_written{tier:init}; imaged's subscriber
	// fans both out into distinct rows. We do NOT call
	// store.CreateSnapshot tier=warm here to avoid a unique-
	// violation on (deployment_id, tier) with imaged's row.
	vmstatePath := filepath.Join(SnapDir(), strings.TrimPrefix(warmVMStateStorageKey, "snap/"))
	e.emitSnapshotWritten(ctx, ins.DeploymentID, ins.NodeID, vmstatePath, warmMemKey, b, state.SnapshotTierWarm)
	// Issue #470 / PR C / ADR-074: emit app.warm_snapshot_promoted
	// so operators can grep gregale audit-events --kind-prefix
	// warm_snapshot to see lifecycle activity. Subject is
	// &app.AccountID (matches app.updated's account-scoped shape at
	// handlers_ext.go:569 — diverges from app.characterized's nil
	// subject deliberately for account-scoped listing per ADR-074
	// §3.2). MemBytes comes from b; the snap row id is unknown at
	// this point (imaged's subscriber writes it), so payload
	// carries the deployment id instead.
	if e.audit != nil {
		// Defensive: the top-of-function nil check at line ~2836
		// guarantees ins.FrameworkReadyAt is non-nil here, but
		// future edits could regress that. A nil stamp means the
		// warm-capture happened but the time-since-first-ready is
		// unknown — log the omission rather than panic.
		var readyToParkMs any
		if ins.FrameworkReadyAt != nil {
			readyToParkMs = time.Since(*ins.FrameworkReadyAt).Milliseconds()
		} else {
			readyToParkMs = nil
		}
		e.audit.Emit(ctx, "app.warm_snapshot_promoted", &app.AccountID, map[string]any{
			"app_id":                     app.ID,
			"deployment_id":              ins.DeploymentID,
			"warm_min_requests":          app.WarmSnapshotMinRequests,
			"warm_min_ms":                app.WarmSnapshotMinMs,
			"request_count":              ins.RequestCount,
			"mem_bytes":                  b.MemBytes,
			"tier":                       state.SnapshotTierWarm,
			"framework_ready_to_park_ms": readyToParkMs,
		})
	}
	return b, nil
}

// resolveApp loads the app, account, plan limits, and current live deployment a
// wake needs. A missing live deployment is a *api.Problem (an app should always
// have one, invariant §6.2-3).
//
// PR-B (issue #272): the LiveDeployment lookup is scope-aware —
// a non-empty scope reads the scope's live deployment row via
// LiveDeploymentForScope. Empty scope falls through to the legacy
// single-deployment LiveDeployment. The scope is read from the
// ctx stamped by WithScope at Wake / AdmitInstance /
// AdmitInstanceForDeployment entry points — see engine_scope.go.
func (e *Engine) resolveApp(ctx context.Context, appID string) (state.App, state.Account, api.Limits, state.Deployment, error) {
	app, acct, limits, err := e.resolveAppForDeploy(ctx, appID)
	if err != nil {
		return state.App{}, state.Account{}, api.Limits{}, state.Deployment{}, err
	}
	// A liveness-exhausted app is deliberately parked until an explicit
	// unpark operation changes its lifecycle back to active. Treating the
	// parked app as wakeable lets a pending async invocation retry forever:
	// every retry creates a fresh FAILED instance row before the same
	// underlying artifact error is observed. Join the scheduler sentinel
	// with the public problem so the drain can terminally fail the row while
	// the RPC surface still carries an actionable error.
	if app.Status == state.AppEvictedCold {
		return state.App{}, state.Account{}, api.Limits{}, state.Deployment{}, errors.Join(
			ErrPermanentWake,
			api.NewProblem(409, api.CodeConflict, "App is parked", "the app is evicted_cold; wake the app before invoking"),
		)
	}
	scope := ScopeFrom(ctx)
	var dep state.Deployment
	if scope == "" {
		dep, err = e.store.LiveDeployment(ctx, appID)
	} else {
		dep, err = e.store.LiveDeploymentForScope(ctx, appID, scope)
	}
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return state.App{}, state.Account{}, api.Limits{}, state.Deployment{},
				api.NewProblem(404, api.CodeNotFound, "No live deployment",
					"the app has no live deployment to wake")
		}
		return state.App{}, state.Account{}, api.Limits{}, state.Deployment{},
			fmt.Errorf("sched: resolve app: live deployment: %w", err)
	}
	return app, acct, limits, dep, nil
}

func (e *Engine) resolveAppForDeploy(ctx context.Context, appID string) (state.App, state.Account, api.Limits, error) {
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return state.App{}, state.Account{}, api.Limits{}, fmt.Errorf("sched: resolve app: %w", err)
	}
	acct, err := e.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return state.App{}, state.Account{}, api.Limits{}, fmt.Errorf("sched: resolve app: account: %w", err)
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return state.App{}, state.Account{}, api.Limits{}, fmt.Errorf("sched: resolve app: unknown plan %q", acct.Plan)
	}
	return app, acct, limits, nil
}

// loadSealedEnvFor returns the sealed env entries to stage at wake for the
// given deployment.
//
// Issue #460 / ADR-053 §Decision 1: when the deployment's OverrideEnvSecrets
// is non-empty, the result is filtered to ONLY those keys (the override is a
// positive allowlist — "secret:DB_URL" resolves to the app_secrets row whose
// Key == "DB_URL"). When OverrideEnvSecrets is empty (legacy behaviour for
// source-tarball / dockerfile deploys that pre-date the override surface),
// the entire app_secrets set for the app is returned.
//
// Missing-secret posture (mirrors ADR-053 §Decision 2 "fail-loud"): an
// override entry referencing a NAME that has no row in app_secrets is
// reported as a loud error — schedd aborts the wake so the deployment row
// transitions to failed. The shape was already validated at apid-create time
// (CreateDeploymentOverrides.Validate at pkg/api/dto.go using
// api.SecretRefNameRe); the existence check is the wake-side equivalent.
// Customers who specify an env_secrets override expect those keys to land in
// the guest — silently dropping them surfaces as a confusing "env var
// missing" without ever telling the customer why.
//
// When ANY override entry is missing its row, ALL missing keys are reported
// in a single error — non-deterministic, but bounded: a customer with three
// missing secrets sees all three in one wake failure, not three sequential
// "fix one, retry, see the next" deploys.
//
// Behaviour change vs. the pre-PR-B loadSealedEnv: a ListAppSecrets error
// (PG hiccup, replication lag, role separation dropping the connection)
// now aborts the wake instead of being silently logged-and-swallowed. This
// is intentional — a wake that comes up without the sealed env the customer
// configured is exactly the "silent drop" ADR-053 §Decision 2 forbids.
//
// Ciphertext + key only — VALUES never appear here or in logs.
//
// We carry AccountID explicitly so a cross-account (accountID, appID) pair
// returns ErrNotFound (consistent with apid's 404 contract).
func (e *Engine) loadSealedEnvFor(ctx context.Context, accountID, appID, scope string, overrideEnvSecrets map[string]string) ([]fcvm.SealedEnvEntry, error) {
	// Defensive collapse: a deployment pre-PR-B may have dep.Scope
	// empty (NULL column). The store surface uses scope='default'
	// everywhere else, so this keeps wake-time behaviour identical
	// to the pre-PR-A path for that deployment.
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	rows, err := e.store.ListAppSecretsInScope(ctx, accountID, appID, scope)
	if err != nil {
		return nil, fmt.Errorf("load sealed env (account=%s app=%s scope=%s): %w", accountID, appID, scope, err)
	}
	if len(overrideEnvSecrets) == 0 {
		// Legacy path: stage everything for the app at the deployment's
		// scope. Preserved for pre-PR-A deployments without override
		// columns populated AND for tarball/dockerfile deploys that
		// don't use the override surface.
		out := make([]fcvm.SealedEnvEntry, 0, len(rows))
		for _, r := range rows {
			out = append(out, fcvm.SealedEnvEntry{Key: r.Key, Ciphertext: r.Ciphertext})
		}
		return out, nil
	}
	// Filtered path: STRICT PER-SCOPE (ADR-092 PR-A). Each
	// override entry resolves to the (account_id, app_id, scope,
	// env_key) sealed row. Missing rows fail loud with intent —
	// silent 'default' overlay would defeat the entire feature
	// (a customer who wants a different sealed DATABASE_URL in
	// 'prod' would NOT see their override). The override map's
	// values are still 'secret:<KEY>' refs; the KEY is the env
	// var name in app_secrets (the env_key in app_envs is the
	// same string but routes to the env table).
	// requested env_keys in declaration order (so the staged
	// /etc/faas/secrets.env is stable and easy to diff in support tickets).
	// Each requested env_key MUST resolve; missing keys are accumulated and
	// reported as one error rather than one-at-a-time so support tickets see
	// the full set.
	index := make(map[string]state.AppSecret, len(rows))
	for _, r := range rows {
		index[r.Key] = r
	}
	var missing []string
	out := make([]fcvm.SealedEnvEntry, 0, len(overrideEnvSecrets))
	for envKey, ref := range overrideEnvSecrets {
		row, ok := index[envKey]
		if !ok {
			missing = append(missing, fmt.Sprintf("%q (-> %q)", envKey, ref))
			continue
		}
		out = append(out, fcvm.SealedEnvEntry{Key: row.Key, Ciphertext: row.Ciphertext})
	}
	if len(missing) > 0 {
		// Sort for determinism — Go map iteration is randomised, so without
		// this a customer with three missing keys would see them in
		// different orders on different wakes. Scope is part of the
		// error so the operator knows which deployment tripped.
		sort.Strings(missing)
		return nil, fmt.Errorf("env_secrets[scope=%s]: missing app_secrets rows for %s on (account=%s, app=%s); set the secret first via faas secrets set --scope %s",
			scope, strings.Join(missing, ", "), accountID, appID, scope)
	}
	return out, nil
}

// envSecretsFromDep unmarshals dep.OverrideEnvSecrets (jsonb column) into a
// map[string]string. Pre-PR-B deployments store nil here (the column didn't
// exist); an empty result preserves the legacy "stage everything for the
// app" behaviour. A malformed column is treated as no override rather than
// fail-the-wake, because the apid path validates the shape at INSERT time —
// a tampered column would need a direct DB write, which the spec gates
// behind DB role separation (CLAUDE.md security rules).
//
// Returned map is owned by the caller; mutating it does not affect the
// deployment row.
func envSecretsFromDep(dep state.Deployment) map[string]string {
	if len(dep.OverrideEnvSecrets) == 0 {
		return nil
	}
	out := make(map[string]string)
	if err := json.Unmarshal(dep.OverrideEnvSecrets, &out); err != nil {
		// Defensive: apid validates shape at INSERT. Treat malformed as
		// no-override so a corrupted row doesn't compound with a missing
		// secrets row to surface as a confusing wake failure.
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// healthcheckPathFromDep (issue #460 / ADR-053, ADR-057 / PR-D) unmarshals
// dep.OverrideHealthcheck (jsonb column) and returns the readiness probe
// path. Returns "" when the column is nil (pre-PR-A deployments), when
// the path field is empty (legacy no-healthcheck), or when the column is
// malformed (fail-soft to the legacy TCP-accept on :8080). The mirror of
// envSecretsFromDep above: defensive against a malformed column rather
// than fail-the-wake, because the apid validator already enforces the
// shape at INSERT time — a tampered column would need a direct DB write
// behind the spec's role separation.
//
// Returned string is owned by the caller; mutating it does not affect
// the deployment row.
func healthcheckPathFromDep(dep state.Deployment) string {
	if len(dep.OverrideHealthcheck) == 0 {
		return ""
	}
	var hc api.DeploymentHealthcheck
	if err := json.Unmarshal(dep.OverrideHealthcheck, &hc); err != nil {
		return ""
	}
	return hc.Path
}

// loadAPIEnv is the plaintext sibling of loadSealedEnv (issue #395 /
// ADR-045). Reads the per-app app_envs rows for the given scope and
// flattens them into the fcvm shape Manager.Wake consumes. Same
// non-fatal read-failure posture as loadSealedEnv — a transient PG
// hiccup drops the env layer (the next wake retries) rather than
// failing the wake itself. Plaintext by contract so there's nothing
// to leak; the worst case is a missing env var, which customer
// support can spot from the "API env X missing" log line.
//
// ADR-091 / PR-D: scope is threaded through here. Pre-PR callers pass
// api.DefaultEnvScope (`"default"`) and the legacy behaviour is
// preserved. Scope-aware callers pass the deployment's declared scope
// (read via store.LiveDeploymentForScope or by reading `dep.Scope`
// after DeploymentByID). The scope's row-set is read via
// ListAppEnvInScope (the scope-aware sibling of the legacy flat
// ListAppEnv).
//
// Carries AccountID explicitly so a cross-account (accountID, appID)
// pair returns ErrNotFound (consistent with apid's 404 contract).
func (e *Engine) loadAPIEnv(ctx context.Context, accountID, appID, scope string) []fcvm.APIEnvEntry {
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	rows, err := e.store.ListAppEnvInScope(ctx, accountID, appID, scope)
	if err != nil {
		e.log.Warn("load api env", "app", appID, "err", err)
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	out := make([]fcvm.APIEnvEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, fcvm.APIEnvEntry{Key: r.Key, Value: r.Value})
	}
	return out
}

// usableSnapshotForWake (issue #470 / PR A / ADR-055) is the tier-aware
// counterpart to usableSnapshot. It honours the plan gate: a Free or
// Hobby account (WarmSnapshotAllowed() == false) skips the warm-tier
// lookup entirely and reads the init-tier row directly. Pro/Scale
// accounts consult LatestSnapshot, which already ranks warm > init on
// the (tier='warm') DESC, created_at DESC order (PR #525 /
// pkg/state/pgstore.go:5669).
//
// The function is the only place the wake path consults the tier
// column — Phase 3 reads snap.StorageKey and Threads it through
// SnapshotRef{StorageKey: snapKey}, so the "warm row" preference
// surfaces naturally because warm-tier publication keys live at
// <snap/<dep>/warm/mem> vs init's <snap/<dep>/mem>. vmmd resolves
// the key through the StorageBackend, so the storage path is
// transparent to the engine.
//
// Sticky-on-downgrade (ADR-055 §5): apps.warm_snapshot_enabled stays
// true across a Pro→Free downgrade, but the next wake sees the
// init-tier row instead. The warm row is left on disk for the
// customer's eventual re-upgrade; the next park the plan allows
// warm again will pick up where the engine left off.
//
// The third return value is the chosen tier
// ∈ {warm, init, cold_boot_fallback}; the caller (the lone
// usableSnapshotForWake call site in this file) is responsible for
// incrementing the WakeSnapshotTier counter (issue #470 / PR C /
// ADR-074). Returning the tier from this function — instead of
// calling the metric accessor directly — keeps the function
// testable without a metric registry.
func (e *Engine) usableSnapshotForWake(ctx context.Context, deploymentID, plan string) (state.Snapshot, bool, string) {
	if !planAllowsWarm(plan) {
		snap, err := e.store.LatestSnapshotForTier(ctx, deploymentID, state.SnapshotTierInit)
		if err != nil || snap.Stale || snap.FCVersion != e.fcVer {
			return state.Snapshot{}, false, wakeTierColdBootFallback
		}
		return snap, true, wakeTierInit
	}
	// PR C / ADR-074: prefer warm when available. LatestSnapshot
	// already ranks warm > init, but checking tier explicitly lets us
	// distinguish warm-wake from init-wake for the operator metric.
	warm, err := e.store.LatestSnapshotForTier(ctx, deploymentID, state.SnapshotTierWarm)
	if err == nil && !warm.Stale && warm.FCVersion == e.fcVer {
		return warm, true, wakeTierWarm
	}
	snap, err := e.store.LatestSnapshotForTier(ctx, deploymentID, state.SnapshotTierInit)
	if err != nil || snap.Stale || snap.FCVersion != e.fcVer {
		return state.Snapshot{}, false, wakeTierColdBootFallback
	}
	return snap, true, wakeTierInit
}

// planAllowsWarm is a thin wrapper that resolves the plan's
// WarmSnapshotAllowed() at the Wake site without dragging the
// whole api.Limits table into engine.go's import graph. Returns
// false for unknown plans (Free/Hobby/explicit false) and true only
// for Pro/Scale. The string form is what the Wake already has on
// hand (acct.Plan) so we don't re-resolve the api.Limits row.
func planAllowsWarm(plan string) bool {
	p := api.Plan(plan)
	return p.WarmSnapshotAllowed()
}

// StuckReason is the watchdog's reason for forcing a transition
// (spec §6.1 budgets: WAKING ≤5s, COLD_BOOTING ≤30s, SNAPSHOTTING ≤20s).
// Each constant maps to one {from, to} terminal state pair in
// KillStuck. The values are stable (wire format for the audit log + the
// ops metric labels).
type StuckReason string

const (
	StuckWakingTimeout   StuckReason = "waking_timeout"
	StuckColdBootTimeout StuckReason = "cold_boot_timeout"
	StuckSnapshotTimeout StuckReason = "snapshot_timeout"
)

// Wake-tier label values (issue #470 / PR C / ADR-074). These
// match the pre-instantiated Prometheus counter labels
// {warm, init, cold_boot_fallback} on
// {prefix}_wake_snapshot_tier_total. Kept as a typed string const
// set rather than state.SnapshotTier* because the counter label
// for the cold-boot case is a metric-only concern, not a snapshot
// row tier.
const (
	wakeTierWarm             = "warm"
	wakeTierInit             = "init"
	wakeTierColdBootFallback = "cold_boot_fallback"
)

// expectedStateForReason returns the source state the row must be in
// for the supplied timeout reason. Used by KillStuck's pre-check.
func expectedStateForReason(r StuckReason) state.State {
	switch r {
	case StuckWakingTimeout:
		return state.StateWaking
	case StuckColdBootTimeout:
		return state.StateColdBooting
	case StuckSnapshotTimeout:
		return state.StateSnapshotting
	default:
		return ""
	}
}

// terminalStateForReason picks the spec §6.1 transition target:
//   - WAKING → COLD_BOOTING (the "fall back" branch; we abandon this
//     row and let the next wake start a fresh cold-boot).
//   - COLD_BOOTING → FAILED.
//   - SNAPSHOTTING → STOPPED.
func terminalStateForReason(r StuckReason) state.State {
	switch r {
	case StuckWakingTimeout:
		return state.StateColdBooting
	case StuckColdBootTimeout:
		return state.StateFailed
	case StuckSnapshotTimeout:
		return state.StateStopped
	default:
		return ""
	}
}

// KillStuck is the spec §6.1 watchdog's terminal action on a stuck
// row. It runs under appMu, re-reads the row, and only acts if the
// state matches the reason's source state (a Wake / Park that
// completed during the watchdog's planning time must not be
// double-killed). The fast path returns nil for the no-op case so a
// goroutine that just raced us is safe.
//
// KillStuck releases the ledger reservation (idempotent), best-effort
// destroys the vmmd-side VM with a 5s deadline (a wedged Firecracker
// can't pin the watchdog goroutine forever), and finally writes the
// terminal state via transition — which is itself the audit-log
// entrypoint once commit 4 lands.
func (e *Engine) KillStuck(ctx context.Context, instanceID, appID string, reason StuckReason) error {
	if reason != StuckWakingTimeout && reason != StuckColdBootTimeout && reason != StuckSnapshotTimeout {
		return fmt.Errorf("sched: KillStuck: unknown reason %q", reason)
	}

	release := e.lockApp(appID)
	defer release()

	fresh, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		// Row gone — someone else (or a prior watchdog pass) already
		// cleaned up. The reservation may also be gone; Ledger.Release
		// is a no-op on unknown instances (admission.go:117).
		e.ledger.Release(instanceID)
		return nil //nolint:nilerr // state.ErrNotFound is a successful no-op here
	}

	want := expectedStateForReason(reason)
	if state.State(fresh.State) != want {
		// Race: a Wake / Park / prior watchdog already moved the row.
		// Don't second-guess — release the reservation in case it
		// leaked, but do not touch the state machine.
		e.ledger.Release(instanceID)
		return nil
	}

	terminal := terminalStateForReason(reason)

	// Free the ledger reservation first so a parallel Wake for the
	// same app can admit a new instance immediately. Release is
	// idempotent (admission.go:117).
	e.ledger.Release(instanceID)

	// Best-effort destroy. A wedged Firecracker can't pin the
	// watchdog goroutine past the 5s ceiling. Use Background so a
	// cancelled tick ctx doesn't cause us to skip the destroy.
	if err := e.timedDestroy(ctx, fresh.NodeID, instanceID, 5*time.Second); err != nil {
		e.log.Warn("watchdog: destroy failed (best-effort)", "instance", instanceID, "reason", reason, "err", err)
	}

	// Final state write + audit-log emission. transitionWithKind
	// (commit 4) handles the events row's AppendEvent call as part
	// of the normal transition path; we just supply the kind and
	// reason so the audit row is searchable on `kind='watchdog_timeout'`.
	// issue #517 / PR-C / ADR-064 — emit wake.stalled alongside the
	// legacy watchdog_timeout audit row. The legacy row is preserved
	// for GDPR-export compatibility (backwards-compat invariant per
	// the plan); the typed row gives the customer-facing timeline
	// endpoint a structured payload with the exact reason.
	if e.events != nil {
		e.events.Emit(ctx, events.Stalled{
			EmitAt:     time.Now().UTC(),
			WakeID:     fresh.WakeID,
			AppID:      appID,
			InstanceID: instanceID,
			NodeID:     fresh.NodeID,
			Reason:     string(reason),
		})
	}
	e.transitionWithKind(ctx, instanceID, appID, terminal, "watchdog_timeout", string(reason))
	if e.ops != nil {
		e.ops.WatchdogKills(string(reason), string(terminal)).Inc()
	}

	// Error-explanations cluster (spec §6.4 amendment 1): a
	// StuckColdBootTimeout marks the deployment row as failed with
	// the app_startup_timeout code + prose so post-mortem retrieval
	// via `gregale inspect <slug> --errors` surfaces the right
	// hint/why/fix. The watchdog killed the instance because the
	// cold boot exhausted the budget — that's distinct from the
	// ECONNREFUSED (app_not_listening) case handled in pkg/fcvm.
	// Best-effort: SetDeploymentFailedEx failure doesn't block the
	// instance transition (the transition is the source of truth
	// for the customer-facing timeline).
	if reason == StuckColdBootTimeout && fresh.DeploymentID != "" {
		p := api.NewProblem(422, api.CodeAppStartupTimeout,
			"app did not become ready in time",
			fmt.Sprintf("watchdog forced the instance to failed after the cold-boot budget elapsed (instance=%s, app=%s)", instanceID, appID))
		_ = whycopy.Decorate(p, api.CodeAppStartupTimeout, nil)
		if _, err := e.store.SetDeploymentFailedEx(ctx, fresh.DeploymentID,
			api.CodeAppStartupTimeout,
			fmt.Sprintf("cold_boot_timeout: instance=%s", instanceID),
			p.Hint, p.Why, p.Fix, nil,
		); err != nil {
			e.log.Warn("watchdog: stamp app_startup_timeout failed", "deployment", fresh.DeploymentID, "err", err)
		}
	}
	return nil
}

// DestroyForLivenessFailure is the liveness-probe-triggered destroy
// path (issue #554 / ADR-078). The vmmd poll goroutine calls
// Manager.ReportLivenessFailed (pkg/fcvm/manager.go) when the
// consecutive-failure counter reaches the per-plan N (default 3);
// the vmmd relay drains into this method. The method is the
// mirror of KillStuck — same destroy + transition + audit shape —
// with two liveness-specific invariants:
//
//  1. MarkSnapshotStale is called eagerly on the instance's
//     snap_id BEFORE the destroy commits, so the next Wake
//     cold-boots from rootfs per ADR-005 ("snapshot of a wedged
//     VM is a wedged VM"). Without this, the cold-boot path
//     would silently restore the wedged snapshot and the
//     customer-facing outage persists.
//
//  2. TouchInstancesLastSeen is called on the destroyed instance
//     so the new cold-boot instance's idle budget restarts on a
//     fresh slate (issue #554 §implementation notes, user-confirmed
//     choice: "Yes — reset on restart"). The destroyed instance's
//     reaper grace is irrelevant because the VM is gone; the
//     new instance's first-request timestamp is the source of
//     truth for the §13 idle reaper.
//
// `reason` is the wire-side string from the vmmd poll goroutine
// (probe set {liveness_n_consecutive, liveness_timeout,
// liveness_conn_refused, liveness_conn_err, liveness_non_200} plus
// source classifications {liveness_infrastructure,
// liveness_process_exited}). Infrastructure is recoverable but is
// deliberately excluded from the app-wide permanent-eviction budget;
// process_exited remains eligible for that budget.
// Audit-kind discriminator is `liveness_failed` (the state-machine
// event); `reason` lands in the audit row's data JSON so the
// dashboard can group by outcome class.
func (e *Engine) DestroyForLivenessFailure(ctx context.Context, instanceID, reason string) error { //nolint:contextcheck // terminal cleanup must outlive vmmd's canceled liveness RPC.
	// This RPC is the authoritative terminal cleanup after vmmd observed a
	// dead guest. vmmd cancels its liveness loop while the destroy RPC is in
	// flight, so retaining the incoming context can cancel the DB transition
	// and leave the instance row RUNNING even though Firecracker is gone.
	// Keep trace values, detach cancellation, and apply a bounded cleanup
	// deadline long enough for the VMM destroy ceiling plus state writes.
	if ctx == nil {
		ctx = context.Background()
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cleanupCancel()
	ctx = cleanupCtx

	// Two reads: a fresh InstanceByID for the app_id +
	// deployment_id, then a re-read under the lock to confirm
	// state hasn't moved. Both reads are best-effort — a missing
	// row means a Park / Destroy race already cleaned up; we
	// return nil so the vmmd poll goroutine doesn't accumulate
	// retries.
	fresh, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		e.ledger.Release(instanceID)
		return nil
	}
	appID := fresh.AppID
	deploymentID := fresh.DeploymentID

	// Acquire the app lock so a parallel Wake / Park for the
	// same app observes a consistent state. The lock is the
	// same one WatchdogKills takes; the comment there about
	// releasing early on a Park race is mirrored here.
	release := e.lockApp(appID)
	defer release()

	// Re-read under the lock for the state-machine check.
	freshLocked, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		e.ledger.Release(instanceID)
		return nil
	}
	if state.State(freshLocked.State) != state.StateRunning {
		// Race: a Park / Wake / prior watchdog already moved
		// the row. Mirror the KillStuck shape — release the
		// reservation in case it leaked, but don't second-guess
		// the state machine.
		e.ledger.Release(instanceID)
		return nil
	}

	// Eagerly mark the deployment's latest snapshot stale so the
	// next Wake cold-boots. This is the load-bearing invariant
	// per ADR-005 — a wedged snapshot is never restored. We
	// resolve the snap via LatestSnapshotForTier because the
	// Instance row doesn't carry the snap_id directly;
	// useableSnapshotForWake will return false on the next Wake
	// after the stale flag flips, forcing a cold boot. Both
	// warm and init tiers are flipped so the next Wake on either
	// plan tier (Free/Hobby cold-boot only; Pro/Scale picks
	// warm first) sees the stale flag.
	for _, tier := range []string{state.SnapshotTierWarm, state.SnapshotTierInit} {
		snap, terr := e.store.LatestSnapshotForTier(ctx, deploymentID, tier)
		if terr != nil || snap.ID == "" {
			continue
		}
		if err := e.store.MarkSnapshotStale(ctx, snap.ID); err != nil {
			e.log.Warn("liveness: mark snapshot stale", "instance", instanceID, "snap_id", snap.ID, "tier", tier, "err", err)
		}
	}

	// Reset the idle timer on the destroyed instance so the
	// replacement cold-boot instance's idle budget starts fresh
	// (issue #554 §implementation notes, user-confirmed: "Yes —
	// reset on restart"). TouchInstancesLastSeen takes a batch;
	// a 1-element batch is the API shape the existing
	// pgstore-side wrapper supports.
	now := time.Now().UTC()
	if _, terr := e.store.TouchInstancesLastSeen(ctx, []state.InstanceTouch{
		{InstanceID: instanceID, LastRequest: now},
	}); terr != nil {
		e.log.Warn("liveness: touch instances last seen", "instance", instanceID, "err", terr)
	}

	// Free the ledger reservation before the destroy so a
	// parallel Wake for the same app can admit a new instance
	// immediately. Mirrors KillStuck's ordering.
	e.ledger.Release(instanceID)

	// Best-effort destroy with the 5s ceiling. A wedged
	// Firecracker cannot pin the goroutine past the deadline —
	// the destroy times out and the liveness_resume hook on
	// the next cold-boot instance takes over.
	destroyErr := e.timedDestroy(ctx, freshLocked.NodeID, instanceID, 5*time.Second)
	if destroyErr != nil {
		e.log.Warn("liveness: destroy failed (best-effort)", "instance", instanceID, "reason", reason, "err", destroyErr)
	}

	// Audit row + metric. The audit kind is
	// `instances.liveness_failed` so the customer's
	// `GET /v1/audit-events?kind_prefix=instances.liveness_*`
	// filter surfaces it; the data JSON carries the
	// reason + fail-count snapshot for the operator.
	if e.events != nil {
		e.events.Emit(ctx, events.LivenessFailed{
			EmitAt:       now,
			InstanceID:   instanceID,
			AppID:        appID,
			DeploymentID: deploymentID,
			Reason:       reason,
		})
	}

	// RUNNING → STOPPED with the liveness_failed kind. The
	// reason field lands in the audit row's data JSON.
	// transitionWithKind (engine.go:3597-3648) is the existing
	// helper — pass `"liveness_failed"` as the kind and the
	// reason as the cause.
	e.transitionWithKind(ctx, instanceID, appID, state.StateStopped, "liveness_failed", reason)

	// Counter emission. The (app, deployment) label set is
	// bounded by the plan's deployed_apps × deployments size
	// (Hobby: 5 apps, Pro: 25 apps, Scale: 100 apps). Empty
	// tuples are collapsed to "unknown" inside the metrics
	// accessor.
	if e.ops != nil {
		e.ops.LivenessRestarts(appID, deploymentID).Inc()
	}

	// Error-explanations cluster (spec §6.4 amendment 1): stamp
	// the deployment as failed with the matching cluster code so
	// `gregale inspect <slug> --errors` can lift the whycopy prose
	// post-mortem. The reason→code mapping is closed-set:
	// liveness_unauthorized → app_healthz_unauthorized (the only
	// cluster code the liveness path emits); the other liveness
	// outcomes stay un-coded (legacy path). Best-effort — a
	// SetDeploymentFailedEx failure is logged but doesn't block
	// the destroy+transition path.
	if deploymentID != "" && reason == "liveness_unauthorized" {
		problem := &api.Problem{
			Code:   api.CodeAppHealthzUnauthorized,
			Status: 422,
			Title:  api.CodeAppHealthzUnauthorized,
		}
		_ = whycopy.Decorate(problem, api.CodeAppHealthzUnauthorized, nil)
		if _, terr := e.store.SetDeploymentFailedEx(ctx,
			deploymentID,
			api.CodeAppHealthzUnauthorized,
			"healthz returned 401 after 3 consecutive probes",
			problem.Hint, problem.Why, problem.Fix, nil); terr != nil {
			e.log.Warn("liveness: stamp deployment failed",
				"instance", instanceID, "deployment_id", deploymentID, "err", terr)
		}
	}

	// Sliding-window check: N confirmed guest restarts in the window parks the
	// parent app so further traffic is rejected at the wake gate.
	// shouldPark=true flips apps.status='evicted_cold' + emits
	// the instances.parked_liveness_exhausted audit row. Best-effort:
	// the destroy above is the source of truth; a window miss
	// just means the next liveness failure repaints the same
	// state. Infrastructure-correlated recovery is intentionally
	// excluded: it can replace a VM, but it cannot permanently
	// evict a healthy app. A destroy timeout is excluded too,
	// because the control plane has no proof that a restart completed.
	// nil window → no check (test-only opt-out).
	budgetedRestart := destroyErr == nil && reason != fcvm.LivenessReasonInfrastructure
	if !budgetedRestart {
		e.log.Info("liveness: restart excluded from eviction budget",
			"instance", instanceID,
			"reason", reason,
			"destroy_succeeded", destroyErr == nil)
	}
	if e.livenessWindow != nil && budgetedRestart {
		if shouldPark, _ := e.livenessWindow.RecordRestartOnNode(deploymentID, freshLocked.NodeID, now); shouldPark {
			if err := e.ParkDeployment(ctx, deploymentID, "liveness_exhausted"); err != nil {
				e.log.Warn("liveness: park deployment failed", "deployment", deploymentID, "err", err)
			}
		}
	}
	// Issue #586 / ADR-129 / cluster C commit 12: persist the
	// lifetime restart counter on the deployments row so a
	// schedd restart doesn't reset the signal. Best-effort: the
	// in-memory LivenessWindow is the runtime decision authority;
	// a column bump miss just means the persistent source-of-truth
	// lags by one restart (the next bump catches up). Mirrors the
	// AuditWriteFail warning posture above — log + continue.
	if budgetedRestart {
		if err := e.store.RecordRestart(ctx, deploymentID); err != nil {
			e.log.Warn("liveness: persist restart count failed", "deployment", deploymentID, "err", err)
		}
	}
	return nil
}

// ForceColdBootNextWake (P2b of the operator-side observability
// mega-PR) marks a deployment's latest warm + init snapshots
// stale so the next customer Wake cold-boots from rootfs per
// ADR-005 ("snapshot of a wedged VM is a wedged VM"). This is the
// recovery primitive for the case where the live instance is fine
// (or already parked by the operator) but the snapshot backing
// the warm tier is suspected to be the carrier of the customer-
// reported wedge. Unlike DestroyForLivenessFailure, this method
// does NOT touch the instance state machine, does NOT acquire
// lockApp (no transition is happening), and does NOT destroy a
// VM — it's a snapshot-policy flip only.
//
// Returns the snap IDs that were marked stale, in (warm, init)
// order. Empty list when the deployment has no snapshots in
// either tier (durable no-op — the operator audit row still
// records the call so a future operator can see the action was
// taken).
//
// Idempotent: MarkSnapshotStale is itself idempotent (stale=
// true on an already-stale row is a no-op). state.ErrNotFound
// when the deployment_id has no row in apps.deployments; the
// caller (gRPC layer) maps that to codes.NotFound.
//
// Best-effort: a MarkSnapshotStale failure on one tier does not
// stop the other tier from being marked stale. The first error
// is logged + dropped — the audit row + the wire response are
// the durable record of what was attempted.
func (e *Engine) ForceColdBootNextWake(ctx context.Context, deploymentID string) ([]string, error) {
	deployment, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return nil, err
	}
	var snapIDs []string
	for _, tier := range []string{state.SnapshotTierWarm, state.SnapshotTierInit} {
		snap, terr := e.store.LatestSnapshotForTier(ctx, deployment.ID, tier)
		if terr != nil || snap.ID == "" {
			continue
		}
		if merr := e.store.MarkSnapshotStale(ctx, snap.ID); merr != nil {
			e.log.Warn("force_cold_boot: mark snapshot stale failed",
				"deployment_id", deploymentID,
				"snap_id", snap.ID,
				"tier", tier,
				"err", merr.Error())
			continue
		}
		snapIDs = append(snapIDs, snap.ID)
	}
	return snapIDs, nil
}

// ForceRestart (P2d of the operator-side observability follow-up mega-PR)
// is the operator-initiated kill-instance + cold-boot-on-next-wake
// primitive. Routes through operator_intents (kind = 'force_restart',
// schema CHECK widened by migrations/00446); the schedd subscriber
// (operator_intent_subscriber.go) dispatches here.
//
// Distinction from siblings:
//   - Engine.Park (force-park primitive) → idempotent stop, no
//     snapshot flip.
//   - Engine.ForceColdBootNextWake (force-cold-boot primitive) →
//     snapshot-policy flip only, no instance destroy.
//   - Engine.DestroyForLivenessFailure (vmmd retry-loop drain) →
//     destroy-side, swallows errors.
//   - Engine.ForceRestart (here) → destroy-side AND snapshot flip,
//     surfaces errors. The third primitive in the
//     `operator_intents.kind` closed set.
//
// Locking: acquires e.lockApp(app_id) — same lock the watchdog +
// Park paths take. A customer-driven Park racing this call observes
// a consistent state under the lock; the locked re-read flips the
// state-gate result.
//
// State-machine: RUNNING → STOPPED via transitionWithKind (the
// §6.2 invariant; CanTransition inside transitionWithKind validates
// the edge). The kind discriminator is "force_restart" — distinct
// from DestroyForLivenessFailure's "liveness_failed" so audit-log
// readers can separate operator actions from watchdog kills.
//
// Returns the snap IDs marked stale so the schedd subscriber can
// populate operator_intents.snap_ids_marked_stale (visible via
// GET /v1/admin/operator-intents/{id}). Returns state.ErrInstanceNotRunning
// when the locked re-read observes a non-RUNNING state — the
// race-loser posture documented on the sentinel.
func (e *Engine) ForceRestart(ctx context.Context, instanceID, reason string) ([]string, error) {
	// Initial read for app_id + deployment_id.
	fresh, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("sched: force_restart: initial read instance %s: %w", instanceID, err)
	}
	appID := fresh.AppID
	deploymentID := fresh.DeploymentID

	// Acquire the app lock so a parallel Wake / Park / prior
	// watchdog for the same app observes a consistent state.
	release := e.lockApp(appID)
	defer release()

	// Re-read under the lock for state-machine validation. If
	// the row vanished between the two reads (Park by the
	// customer, retention sweep), surface the read error so the
	// operator sees the truth — same posture as
	// DestroyForWorkloadOOMFailure's review-finding-#4 fix at
	// engine.go:5280-5293.
	freshLocked, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		e.ledger.Release(instanceID)
		return nil, fmt.Errorf("sched: force_restart: locked read instance %s: %w", instanceID, err)
	}
	if state.State(freshLocked.State) != state.StateRunning {
		// Race: a customer-driven Park / Destroy (or a prior
		// force-restart) won the lock. The desired end-state
		// (instance no longer running) is achieved. The
		// caller (schedd subscriber) stamps the operator_intent
		// row failed with state.ErrInstanceNotRunning so the
		// audit trail records the admin click was an
		// idempotent no-op. Mirror KillStuck's reservation
		// release posture for safety.
		e.ledger.Release(instanceID)
		return nil, state.ErrInstanceNotRunning
	}

	// Eagerly mark the deployment's latest snapshot stale so
	// the next Wake cold-boots. ADR-005 invariant — an
	// operator-initiated kill is also implicitly a
	// kill-the-snapshot so a wedged snap is never restored.
	// Both warm + init tiers (parallel to ForceColdBootNextWake
	// at engine.go:5217).
	var snapIDs []string
	for _, tier := range []string{state.SnapshotTierWarm, state.SnapshotTierInit} {
		snap, terr := e.store.LatestSnapshotForTier(ctx, deploymentID, tier)
		if terr != nil || snap.ID == "" {
			continue
		}
		if merr := e.store.MarkSnapshotStale(ctx, snap.ID); merr != nil {
			e.log.Warn("force_restart: mark snapshot stale",
				"instance", instanceID,
				"snap_id", snap.ID,
				"tier", tier,
				"err", merr.Error())
			continue
		}
		snapIDs = append(snapIDs, snap.ID)
	}

	// Reset the idle timer on the destroyed instance so the
	// replacement cold-boot instance's idle budget starts fresh
	// (issue #554 §implementation notes — same precedent as
	// DestroyForLivenessFailure + DestroyForWorkloadOOMFailure).
	now := time.Now().UTC()
	if _, terr := e.store.TouchInstancesLastSeen(ctx, []state.InstanceTouch{
		{InstanceID: instanceID, LastRequest: now},
	}); terr != nil {
		e.log.Warn("force_restart: touch instances last seen", "instance", instanceID, "err", terr)
	}

	// Free the ledger reservation BEFORE the destroy so a
	// parallel Wake for the same app can admit a new instance
	// immediately. Mirrors the liveness + workload-OOM ordering.
	e.ledger.Release(instanceID)

	// Surface the destroy error — operator-initiated, not
	// retry-loop (same rationale as the read-error surfaces
	// above). The snap-stale work above is durable; the destroy
	// is the operator-visible status. If the destroy fails the
	// operator sees (snapIDs, err) on the wire and via the
	// terminal operator_intent row.
	if err := e.timedDestroy(ctx, freshLocked.NodeID, instanceID, 5*time.Second); err != nil {
		return snapIDs, fmt.Errorf("sched: force_restart: destroy instance %s: %w", instanceID, err)
	}

	// RUNNING → STOPPED with kind "force_restart". The operator's
	// reason lands in the events row's data JSON. transitionWithKind
	// does the CanTransition guard internally (engine.go:5569+).
	// Counter emission: deliberately omitted. LivenessRestarts +
	// WorkloadOOMKills are the workload-initiated labels.
	// Operator actions are tracked separately by the operator_intent
	// rows + the terminal operator.action.<verb>.outcome audit
	// rows; adding a third ops counter would create a fourth
	// source of truth for the same volume.
	e.transitionWithKind(ctx, instanceID, appID, state.StateStopped, "force_restart", reason)

	return snapIDs, nil
}

// DestroyForWorkloadOOMFailure (Cluster C / ADR-121) is the
// workload-OOM-triggered destroy path. The producer chain is:
//
//	guest/init/cgroup_partition_linux.go::WatchOOM
//	  (cgroup.events oom_kill delta on the per-VM cgroup v2 leaf)
//	  → guest/init/framework_ready_emit.go::EmitWorkloadOOM
//	    (vsock DGRAM type 0x05 on port 1027)
//	  → cmd/vmmd/framework_ready_recv.go::dispatchWorkloadOOM
//	  → pkg/fcvm/manager.go::ReportWorkloadOOM
//	  → cmd/vmmd main → scheddgrpc::Server.ReportWorkloadOOM
//	  → here
//
// Distinct from DestroyForLivenessFailure:
//   - The workload is dead by the time the signal arrives (the
//     kernel killed it; the guest-init process is still alive
//     enough to emit the DGRAM, but the customer's process is not).
//   - The observed payload (peakMB, planMB) is carried through;
//     the whycopy Observed closure templates it into the
//     deployment row's stored Hint/Why/Fix.
//   - The audit kind is "workload_oom_failed" (the new
//     events.WorkloadOOMFailed), not "liveness_failed".
//   - No liveness-window sliding check (the workload OOM is a
//     distinctive failure mode — a customer hitting the RAM cap
//     doesn't necessarily mean the app is misconfigured; they may
//     just need to upgrade plan).
//
// The state-machine lock + re-read shape, snapshot-stale + touch
// + transition + counter increment are mirrored from
// DestroyForLivenessFailure because the failure-detection surface
// is the same (a vmmd-initiated destroy event), only the trigger
// and the stamping differ.
//
// Best-effort: a failure at any step is logged + dropped because
// the workload is already dead; the destroy + transition is the
// source of truth, the stamp is the customer-facing UX.
func (e *Engine) DestroyForWorkloadOOMFailure(ctx context.Context, instanceID string, peakMB, planMB int) error {
	// Two reads: a fresh InstanceByID for the app_id +
	// deployment_id, then a re-read under the lock to confirm
	// state hasn't moved.
	//
	// Review finding #4: the original shape returned nil on
	// any InstanceByID error so the gRPC handler's
	// `errors.Is(err, state.ErrNotFound)` mapping was
	// unreachable — the handler always saw nil and replied
	// Ok=true. The fix returns the read error so the handler
	// can map NotFound → codes.NotFound and Internal →
	// codes.Internal. DestroyForLivenessFailure uses the
	// nil-return shape because the liveness poll goroutine
	// is a retry loop (silent no-op is desired); the
	// workload-OOM path is a single-shot RPC, so a
	// NotFound is operationally distinct from a healthy
	// idempotent no-op (the caller should know the
	// instance row is gone).
	fresh, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		e.ledger.Release(instanceID)
		// Pass the typed error through with operation context
		// (pkg/api/errors.go convention: %w + op string). The
		// gRPC handler at scheddgrpc/server.go::ReportWorkloadOOM
		// maps state.ErrNotFound → codes.NotFound and any other
		// error → codes.Internal.
		return fmt.Errorf("DestroyForWorkloadOOMFailure: initial read instance %s: %w", instanceID, err)
	}
	appID := fresh.AppID
	deploymentID := fresh.DeploymentID

	// Acquire the app lock so a parallel Wake / Park for the
	// same app observes a consistent state. Mirrors the comment
	// on DestroyForLivenessFailure.
	release := e.lockApp(appID)
	defer release()

	// Re-read under the lock for the state-machine check.
	freshLocked, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		e.ledger.Release(instanceID)
		return fmt.Errorf("DestroyForWorkloadOOMFailure: locked read instance %s: %w", instanceID, err)
	}
	if state.State(freshLocked.State) != state.StateRunning {
		// Race: a Park / Wake / prior watchdog already moved
		// the row. Mirror the liveness path — release the
		// reservation in case it leaked, but don't second-guess
		// the state machine. Return nil so the handler replies
		// Ok=true (the idempotent no-op the wire contract
		// promises).
		e.ledger.Release(instanceID)
		return nil
	}

	// Eagerly mark the deployment's latest snapshot stale so the
	// next Wake cold-boots. ADR-005 invariant — a workload that
	// OOM'd at the plan cap may have a snap that encoded the
	// same blast radius; the next cold boot is a fresh chance.
	// Both warm + init tiers are flipped (mirrors the liveness
	// path).
	for _, tier := range []string{state.SnapshotTierWarm, state.SnapshotTierInit} {
		snap, terr := e.store.LatestSnapshotForTier(ctx, deploymentID, tier)
		if terr != nil || snap.ID == "" {
			continue
		}
		if err := e.store.MarkSnapshotStale(ctx, snap.ID); err != nil {
			e.log.Warn("workload_oom: mark snapshot stale", "instance", instanceID, "snap_id", snap.ID, "tier", tier, "err", err)
		}
	}

	// Reset the idle timer on the destroyed instance so the
	// replacement cold-boot instance's idle budget starts fresh.
	now := time.Now().UTC()
	if _, terr := e.store.TouchInstancesLastSeen(ctx, []state.InstanceTouch{
		{InstanceID: instanceID, LastRequest: now},
	}); terr != nil {
		e.log.Warn("workload_oom: touch instances last seen", "instance", instanceID, "err", terr)
	}

	// Free the ledger reservation before the destroy so a
	// parallel Wake for the same app can admit a new instance
	// immediately. Mirrors the liveness path's ordering.
	e.ledger.Release(instanceID)

	// Best-effort destroy with the 5s ceiling. A wedged
	// Firecracker cannot pin the goroutine past the deadline.
	if err := e.timedDestroy(ctx, freshLocked.NodeID, instanceID, 5*time.Second); err != nil {
		e.log.Warn("workload_oom: destroy failed (best-effort)", "instance", instanceID, "peak_mb", peakMB, "plan_mb", planMB, "err", err)
	}

	// Audit row (Cluster C / ADR-121). The audit kind is
	// `instances.workload_oom_failed` so the customer's
	// `GET /v1/audit-events?kind_prefix=instances.workload_*`
	// filter surfaces it; the data JSON carries the observed
	// (peak_mb, plan_mb) payload for the operator dashboard.
	//
	// Review finding #7: the previous shape emitted TWO
	// audit rows per OOM — this typed event AND the
	// transitionWithKind call below (which also writes
	// to audit_events via AppendEvent). The scheduler-side
	// dashboard panel subscribes to the typed event for
	// the rich payload (peak_mb, plan_mb); the customer's
	// `gregale audit` view was double-counting. The fix
	// keeps the typed event (it carries the rich payload) and
	// drops the transitionWithKind call in favor of the
	// direct state-write + SSE notify path. The state
	// transition is unchanged (RUNNING → STOPPED) but the
	// audit row is now exactly one.
	if e.events != nil {
		e.events.Emit(ctx, events.WorkloadOOMFailed{
			EmitAt:       now,
			InstanceID:   instanceID,
			AppID:        appID,
			DeploymentID: deploymentID,
			PeakMB:       peakMB,
			PlanMB:       planMB,
		})
	}

	// RUNNING → STOPPED — state transition without a
	// second audit row. UpdateInstanceStateToTerminal
	// stamps terminal_at on the same UPDATE so the §17
	// retention sweep has a correct age anchor (see the
	// comment on transitionWithKind at engine.go:5393-5398
	// for the terminal-state column rationale). The SSE
	// notification goes through emitInstanceChanged so
	// subscribers see the column flip on the dashboard.
	if err := e.store.UpdateInstanceStateToTerminal(ctx, instanceID, string(state.StateStopped), now); err != nil {
		e.log.Warn("workload_oom: write terminal state",
			"instance", instanceID, "err", err)
	}
	e.emitInstanceChanged(ctx, instanceID, appID, state.StateStopped, "") // wake_id already on the row; the direct-write path doesn't re-load it.
	if freshLocked.Mode == string(state.InstanceModeService) {
		e.scheduleServiceReconcile(ctx, deploymentID)
	}

	// Counter emission. Cardinality bounds match LivenessRestarts.
	// Empty tuples are collapsed to "unknown" inside the metrics
	// accessor.
	if e.ops != nil {
		e.ops.WorkloadOOMKills(appID, deploymentID).Inc()
	}

	// Error-explanations cluster (spec §6.4 amendment 2,
	// Cluster C / ADR-121): stamp the deployment as failed with
	// CodeAppRuntimeOOM + the whycopy Observed payload (peakMB
	// + planMB). The customer-facing surface is unified with the
	// rest of the cluster: `gregale inspect <slug> --errors` +
	// the dashboard's `.error-explanation` section + the
	// catalogue's Hint/Why/Fix pick up the same prose.
	//
	// Unconditional (no reason-gating like the liveness path) —
	// every workload OOM gets the stamp because the cluster code
	// is dedicated to this failure mode.
	if deploymentID != "" {
		problem := &api.Problem{
			Code:   api.CodeAppRuntimeOOM,
			Status: 422,
			Title:  "Container out of memory",
		}
		_ = whycopy.Decorate(problem, api.CodeAppRuntimeOOM,
			struct{ PeakMB, PlanMB int }{PeakMB: peakMB, PlanMB: planMB})
		if _, terr := e.store.SetDeploymentFailedEx(ctx,
			deploymentID,
			api.CodeAppRuntimeOOM,
			fmt.Sprintf("cgroup OOM-kill fired at %d MB (plan cap %d MB)", peakMB, planMB),
			problem.Hint, problem.Why, problem.Fix, nil); terr != nil {
			e.log.Warn("workload_oom: stamp deployment failed",
				"instance", instanceID, "deployment_id", deploymentID, "err", terr)
		}
	}
	return nil
}

// ParkDeployment is the liveness-window-exhausted stop path
// (issue #554 / ADR-078). It flips the parent app's status to
// `evicted_cold` (the only non-active, non-deleted app.status
// value the apps.status CHECK allows), so subsequent Wakes are
// rejected at the wake gate. It also emits the
// `instances.parked_liveness_exhausted` audit row with the
// deployment id as the subject so operators can grep
// `kind_prefix=instances.parked_liveness_*`.
//
// The method is idempotent: a re-call on an already-evicted_cold
// app is a no-op (UpdateApp returns the unchanged row). The
// audit row is emitted on every call so a customer who triggers
// the gate twice (e.g. across schedd restarts that lose the
// in-memory window) gets a row per event; the
// kind_prefix=instances.parked_liveness_exhausted filter still
// surfaces the latest.
//
// `reason` is a closed-set label, today only "liveness_exhausted";
// future stop-the-traffic paths (e.g. spec §17 retention) may
// reuse this method. AC #3 follow-up (issue #554 / ADR-079): the
// per-deployment `deployments.parked_reason` + `parked_at` columns
// from migration 00157 are stamped here BEFORE the apps.status
// flip so a re-stamp on a schedd crash loop does not re-paint
// the timestamp (SetDeploymentParked is idempotent — see
// pkg/state/pgstore.go). The audit row remains the durable
// source of truth.
func (e *Engine) ParkDeployment(ctx context.Context, deploymentID, reason string) error {
	if deploymentID == "" {
		return fmt.Errorf("sched: ParkDeployment: empty deploymentID")
	}
	// Closed-set guard (issue #554 follow-up): the
	// deployments.parked_reason CHECK constraint accepts only
	// {liveness_exhausted, lifecycle_park, admin_park}. A stray
	// value would surface as a Postgres 23514 at the SQL layer
	// and be silently warn-logged by SetDeploymentParked's
	// caller — operators would see an evicted_cold app with no
	// parked_reason on the wire. Fail fast here so the bug
	// surfaces during dev, not in prod.
	pr := state.ParkReason(reason)
	if !pr.IsValid() {
		return fmt.Errorf("sched: ParkDeployment: deployment %s: invalid reason %q (want one of liveness_exhausted, lifecycle_park, admin_park)", deploymentID, reason)
	}
	// Resolve the parent app so we can flip apps.status. The
	// state store is the single source of truth for the
	// deployment → app mapping.
	dep, err := e.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("sched: ParkDeployment: deployment %s: %w", deploymentID, err)
	}
	appID := dep.AppID

	// AC #3 wire: stamp the per-deployment parked_reason +
	// parked_at columns so the apid GET /v1/apps/{slug}
	// surface can render `parked_deployment: { id,
	// parked_reason, parked_at }`. Idempotent — a re-stamp on
	// a schedd crash loop is a no-op. Best-effort: a failure
	// here logs a warning but does NOT fail the park, because
	// the audit row (below) is the durable source of truth.
	if err := e.store.SetDeploymentParked(ctx, deploymentID, reason, time.Now().UTC()); err != nil {
		e.log.Warn("liveness: stamp deployment parked failed", "deployment", deploymentID, "err", err)
	}

	evicted := state.AppStatus("evicted_cold")
	if _, err := e.store.UpdateApp(ctx, appID, state.UpdateAppParams{
		Status: &evicted,
	}); err != nil {
		return fmt.Errorf("sched: ParkDeployment: update app %s status=evicted_cold: %w", appID, err)
	}
	// Emit the audit row + events event. Subject = deploymentID
	// (not appID) so the dashboard's "this deployment's history"
	// filter surfaces it; the data JSON carries the appID for
	// the cross-cutting ops view.
	data, err := json.Marshal(map[string]any{
		"app":           appID,
		"deployment":    deploymentID,
		"reason":        reason,
		"now":           time.Now().UTC().Format(time.RFC3339Nano),
		"window_recent": e.livenessWindow.recent(deploymentID, time.Now()),
	})
	if err != nil {
		e.log.Warn("liveness: marshal parked audit", "err", err)
	} else {
		subject := deploymentID
		if err := e.store.AppendEvent(ctx, "schedd", "instances.parked_liveness_exhausted", &subject, data); err != nil {
			e.log.Warn("liveness: parked audit write failed", "deployment", deploymentID, "err", err)
		}
	}
	if e.events != nil {
		e.events.Emit(ctx, events.ParkedLivenessExhausted{
			EmitAt:       time.Now().UTC(),
			AppID:        appID,
			DeploymentID: deploymentID,
			ParkedReason: reason,
		})
	}
	return nil
}

// transition validates and applies one instance state change, then emits
// instance_changed. An illegal edge is logged and dropped rather than written —
// schedd must never persist an impossible transition (spec §6.1).
//
// Commit 4 also writes the events audit-log row (spec §6.1: "every
// transition is an events row"). The events write is best-effort —
// the state row is the source of truth, the events table is audit.
// A failure here logs a warning and increments the
// events_write_failures counter; the transition itself still
// succeeded.
//
// `reason` is an opaque label for the cause ("watchdog_timeout",
// "wake_boot_error", …) carried in the events row's data payload.
// The default kind is "state_transition" — the only other kind
// reserved today is "watchdog_timeout" (set by KillStuck).
func (e *Engine) transition(ctx context.Context, instanceID, appID string, to state.State) {
	e.transitionWithKind(ctx, instanceID, appID, to, "state_transition", "")
}

// rollbackAdmittedInstance closes the small window between ledger admission
// and boot-spec construction. Secret-resolution failures are deterministic
// input errors, not watchdog work: release the RAM reservation immediately
// and leave an auditable terminal row so the next wake cannot count the
// abandoned instance as live.
func (e *Engine) rollbackAdmittedInstance(ctx context.Context, instanceID, appID, reason string) {
	e.ledger.Release(instanceID)
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	e.transitionWithKind(cleanupCtx, instanceID, appID, state.StateFailed, "wake_boot_error", reason)
}

// transitionWithKind is the audit-log-emitting variant of transition.
// Callers that need a non-default kind (Wake's "wake_boot_error" path,
// KillStuck's "watchdog_timeout", snapshotAndPark's "park_snapshot_error")
// go through here. The transition body itself is unchanged from
// transition() — only the appended events row differs.
// transitionWithKindCAS is the CAS-aware sibling of transitionWithKind,
// added by ADR-137 follow-up / fix #5. It performs the same
// load → validate edge → write → audit-log flow but returns
// (ok, error) so race-losers can suppress their metric / event
// emission. ok=true means the state write landed AND the
// from→to edge was legal AND the row existed; ok=false means the
// row wasn't found, the edge was refused, or the write failed
// (the error distinguishes the failure shape).
//
// Used by callers that race with peers on the same row —
// Engine.RecreateInstance is the load-bearing one (the recovery
// arbiter and the deadnode reconciler can both target the same
// stranded row; the loser must not double-count).
func (e *Engine) transitionWithKindCAS(ctx context.Context, instanceID, appID string, to state.State, kind, reason string) (bool, error) {
	ins, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		return false, err
	}
	from := state.State(ins.State)
	if from == to {
		return false, nil // benign no-op (idempotent re-entry); not a CAS win
	}
	if !state.CanTransition(from, to) {
		e.log.Error("transition: illegal edge refused", "instance", instanceID, "from", from, "to", to)
		return false, nil
	}
	if to == state.StateStopped || to == state.StateFailed {
		if err := e.store.UpdateInstanceStateToTerminal(ctx, instanceID, string(to), time.Now().UTC()); err != nil {
			return false, err
		}
	} else if err := updateInstanceStateCAS(ctx, e.store, instanceID, string(from), string(to)); err != nil {
		return false, err
	}
	e.emitInstanceChanged(ctx, instanceID, appID, to, ins.WakeID)
	subject := instanceID
	data, _ := json.Marshal(map[string]any{
		"from": string(from), "to": string(to), "reason": reason, "ts": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err := e.store.AppendEvent(ctx, "schedd", kind, &subject, data); err != nil {
		e.log.Warn("transition: append event", "instance", instanceID, "from", from, "to", to, "kind", kind, "err", err)
		if e.ops != nil {
			e.ops.EventsWriteFailures().Inc()
		}
	}
	return true, nil
}

// updateInstanceStateCAS is kept behind the state.Store interface so the
// recovery primitive cannot silently fall back to an unconditional write.
// Both production stores implement this method; the explicit assertion also
// makes a partially wired test/store fail closed instead of reintroducing the
// load-then-write race this helper exists to close.
func updateInstanceStateCAS(ctx context.Context, store state.Store, instanceID, expectedState, nextState string) error {
	return store.UpdateInstanceStateIf(ctx, instanceID, expectedState, nextState)
}

func (e *Engine) transitionWithKind(ctx context.Context, instanceID, appID string, to state.State, kind, reason string) {
	ins, err := e.store.InstanceByID(ctx, instanceID)
	if err != nil {
		e.log.Warn("transition: load instance", "instance", instanceID, "to", to, "err", err)
		return
	}
	from := state.State(ins.State)
	if from == to {
		return
	}
	if !state.CanTransition(from, to) {
		e.log.Error("transition: illegal edge refused", "instance", instanceID, "from", from, "to", to)
		return
	}
	// Terminal transitions ({STOPPED, FAILED}) stamp terminal_at on the
	// same UPDATE so the §17 retention sweep has a correct age anchor
	// (PR #74). started_at means "row creation" and parked_at is
	// overloaded, so neither is right for a STOPPED row whose vmmd
	// boot succeeded days earlier. Non-terminal transitions keep the
	// single-column UPDATE.
	if to == state.StateStopped || to == state.StateFailed {
		if err := e.store.UpdateInstanceStateToTerminal(ctx, instanceID, string(to), time.Now().UTC()); err != nil {
			e.log.Warn("transition: write terminal", "instance", instanceID, "to", to, "err", err)
			return
		}
	} else if err := e.store.UpdateInstanceState(ctx, instanceID, string(to)); err != nil {
		e.log.Warn("transition: write", "instance", instanceID, "to", to, "err", err)
		return
	}
	// Surface the row's wake_id in the SSE payload. The audit-log
	// caller loaded `ins` at the top of this function precisely to
	// validate the from→to edge, so reusing it here avoids an extra
	// round-trip — wake_id is on the row already. Review finding #3
	// (gaps analysis 2026-07-23): previously the payload carried
	// wake_id="" for every transition, which meant dashboards
	// subscribed to instance_changed saw the column go empty as
	// soon as the instance entered RUNNING.
	e.emitInstanceChanged(ctx, instanceID, appID, to, ins.WakeID)
	if (to == state.StateRunning || to == state.StateStopped || to == state.StateFailed) &&
		ins.Mode == string(state.InstanceModeService) {
		e.scheduleServiceReconcile(ctx, ins.DeploymentID)
	}

	// Audit-log emission (spec §6.1). Best-effort: a failure logs
	// and counts, never rolls back the transition. The state row is
	// the source of truth; this is observation.
	subject := instanceID
	data, _ := json.Marshal(map[string]any{
		"from": string(from), "to": string(to), "reason": reason, "ts": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err := e.store.AppendEvent(ctx, "schedd", kind, &subject, data); err != nil {
		e.log.Warn("transition: append event", "instance", instanceID, "from", from, "to", to, "kind", kind, "err", err)
		if e.ops != nil {
			e.ops.EventsWriteFailures().Inc()
		}
	}
}

func (e *Engine) emitInstanceChanged(ctx context.Context, instanceID, appID string, st state.State, wakeID string) {
	if e.notif == nil {
		return
	}
	// wakeID is the per-wake correlation handle. transitionWithKind
	// (the audit-log caller) loads it from the row before emitting so
	// every state-transition event carries the same wake_id the row
	// currently has. Wake/Prime pass the value they just minted at
	// Phase 2; snapshotAndPark passes ins.WakeID from the loaded
	// instance. The JSON key is always present (even when empty for
	// legacy callers) so SSE subscribers can use a fixed parse path.
	// produced the row, while the wake / prime callers always do. Empty
	// string keeps the JSON key present so the SSE subscriber can use
	// a fixed parse path; dashboard queries can read wake_id back off
	// the instances row when needed.
	payload, _ := json.Marshal(map[string]any{"instance_id": instanceID, "app_id": appID, "state": string(st), "wake_id": wakeID})
	if err := e.notif.Notify(ctx, db.NotifyInstanceChanged, string(payload)); err != nil {
		e.log.Warn("emit instance_changed", "instance", instanceID, "wake_id", wakeID, "err", err)
	}
}

// emitSnapshotWritten publishes the snapshot_written payload imaged
// consumes to record a row in the snapshots table. The tier argument
// (issue #470 / PR A / ADR-055) lets the same payload carry
// tier="warm" when the engine captured a warm snapshot and tier="init"
// for the legacy cold capture. imaged's subscriber reads the field
// from the JSON and writes the matching snapshots.tier column.
func (e *Engine) emitSnapshotWritten(ctx context.Context, deploymentID, nodeID, vmstatePath, storageKey string, b SnapshotBytes, tier string) {
	if e.notif == nil {
		return
	}
	if tier == "" {
		tier = state.SnapshotTierInit
	}
	payload, _ := json.Marshal(map[string]any{
		"deployment_id": deploymentID,
		"node_id":       nodeID,
		"vmstate_path":  vmstatePath,
		"storage_key":   storageKey,
		"mem_bytes":     b.MemBytes,
		"vmstate_bytes": b.VMStateBytes,
		"fc_version":    e.fcVer,
		"tier":          tier,
	})
	if err := e.notif.Notify(ctx, db.NotifySnapshotWritten, string(payload)); err != nil {
		e.log.Warn("emit snapshot_written", "deployment", deploymentID, "tier", tier, "err", err)
		return
	}
	if err := e.ClearSnapshotBackoff(ctx, deploymentID); err != nil {
		e.log.Warn("emit snapshot_written: clear snapshot backoff", "deployment", deploymentID, "err", err)
	}
}

// wakeOutcome is the discrete result of the wake-gate consult
// (PR-C, issue #462). admitAndDispatch consults admitGate before
// any work that touches the ledger or the instances table; the
// caller routes on the result.
type wakeOutcome int

const (
	wakeAdmit wakeOutcome = iota
	wakeRejectAtCap
	wakeCooldownHeld
	wakeMinFloorAlready
	// wakeOverageCapReached (issue #561): accounts.overage_cap_cents
	// is set and the current-month overage cents met/exceeded the
	// cap. The customer's deliberate budget is blocking us, NOT
	// the per-app concurrency / cooldown / min-floor shape. Caller
	// returns `*api.Problem{Code: CodeAdmissionRefused}` (HTTP 402)
	// from Engine.Wake and WakeResult{AtCapacity: true} from
	// Engine.AdmitInstance (same shape as wakeRejectAtCap). Lives
	// AFTER wakeMinFloorAlready in the enum value list because the
	// scheduler preserves source order for test-pin stability.
	wakeOverageCapReached
)

// admitGate (PR-C, issue #462) is the single decision site for
// the wake-gate outcome metric on the per-app admission
// pre-flight. Called by admitAndDispatch BEFORE the ledger check
// and the instances INSERT. It stamps the wake-gate outcome to
// schedd_scale_up_decisions_total{outcome=...} via the shared
// *wire.OpsMetrics.
//
// Outcomes:
//
//   - wakeAdmit: per-app cap has headroom, no cooldown in effect,
//     and no min-floor collision. Caller proceeds to ledger.Admit
//     and instances INSERT. The caller stamps apps.last_scale_out_at
//     after a successful insert (StampAppScaleOut, best-effort).
//
//   - wakeRejectAtCap: per-app cap reached (Concurrency >= MaxConcur).
//     Caller short-circuits with no INSERT and returns AtCapacity=true
//     (AdmitInstance) or *api.Problem CodePlanLimitConcur (Wake).
//
//   - wakeCooldownHeld: now - apps.last_scale_out_at <
//     ScalingPolicy.ScaleOutCooldownS AND Concurrency(appID) > 0.
//     Cold-start wakes (concurrency == 0) bypass cooldown — the
//     discriminator is load-bearing for the customer's "scale on
//     demand" use case. Caller short-circuits; the existing wake
//     surface returns *api.Problem CodePlanLimitConcur (the same
//     pre-PR-C shape). PR-D adds a dedicated CodeWaitForWarm RFC
//     7807 code and the customer-facing 503 surface.
//
//   - wakeMinFloorAlready: Concurrency >= ScalingPolicy.MinInstances
//     AND a no-signal wake. Today this is the "wake arrived with
//     no inflight reading" branch — the targets trigger did not
//     enqueue this wake. Caller short-circuits with no INSERT.
//     Mostly informational for the dashboard "why didn't this
//     scale?" pane; PR-D will shape the wire surface.
//
//   - wakeOverageCapReached (issue #561): accounts.overage_cap_cents
//     is set and the current-month overage cents already meet or
//     exceed it. The OverageChecker seam returns OverageReached;
//     the gate returns via out-params the (obs, cap) cents so the
//     caller can lift them into `api.ErrAdmissionRefused(obs, cap)`
//     without re-reading the Engine. Distinct from wakeRejectAtCap
//     because (a) the trigger is account-scoped not per-app, (b) the
//     wire surface is CodeAdmissionRefused (HTTP 402 + Retry-After
//     intentionally omitted — the cap is a deliberate budget, not
//     back-pressure), and (c) the existing live instances are NOT
//     auto-parked by the cap alone (that path lives in
//     pkg/meter/quota.go::EnforceQuota case "stop" and is out of
//     scope for #561). Caller short-circuits with no INSERT and
//     emits the audit row overage.cap_reached via the checker's
//     RecordReached (UTC-day deduped).
//
// The signature returns (wakeOutcome, observedCents, capCents) so
// the overage cents ride on the stack from the gate to the
// switch in admitAndDispatch. The earlier shape stashed them on
// Engine fields, which under -race serialised with multiple
// goroutines hitting the cap-reached branch against the same
// account (TestProperty_EngineWake_OverageCapReached). The
// out-params keep the lock-drop invariant documented at
// engine.go:172-203 (release the per-app lock before reading the
// cached values).
//
// PR-A: a final bool — atCapacity — is computed under the same
// Phase 2 lock. It is true ONLY on the wakeAdmit branch when the
// pre-admit ledger reading is maxConc-1 (i.e. this admit pushes the
// ledger to maxConc). Reject branches always return false (no
// BootStarted row is emitted on rejection, so the value is moot).
// Computed alongside `concurrency` to keep the lock footprint the
// same — no extra ledger / Postgres read.
func (e *Engine) admitGate(ctx context.Context, app *state.App, limits api.Limits) (wakeOutcome, int64, int64, int, bool) {
	concurrency := e.ledger.Concurrency(app.ID)
	// Mirror admission.go:149-152: apps created via store.CreateApp
	// without a subsequent UpdateApp leave MaxConcurrency at 0.
	// Clamp against the plan ceiling so legacy / pre-PR-A apps still
	// admit normally. Without the clamp, an app with MaxConcurrency=0
	// would always return wakeRejectAtCap and every wake would 429.
	maxConc := app.MaxConcurrency
	if maxConc <= 0 || maxConc > limits.MaxConcurrency {
		maxConc = limits.MaxConcurrency
	}
	if concurrency >= maxConc {
		if e.ops != nil {
			e.ops.ObserveScaleUp(app.ID, "reject_at_cap")
		}
		return wakeRejectAtCap, 0, 0, concurrency, false
	}
	if !isScaleOutBurstContinuation(ctx) && e.isOnScaleOutCooldown(app, concurrency) {
		if e.ops != nil {
			e.ops.ObserveScaleUp(app.ID, "cooldown_held")
		}
		return wakeCooldownHeld, 0, 0, concurrency, false
	}
	if e.atMinFloorWithNoSignal(app, concurrency) {
		if e.ops != nil {
			e.ops.ObserveScaleUp(app.ID, "min_floor_already")
		}
		return wakeMinFloorAlready, 0, 0, concurrency, false
	}
	// Issue #561: spend cap pause-workload. Nil check tolerates
	// legacy fixtures (the branch becomes a no-op).
	if e.overage != nil {
		status, observedCents, capCents, _ := e.overage.Check(ctx, app.AccountID)
		if status == OverageReached {
			if e.ops != nil {
				e.ops.ObserveScaleUp(app.ID, "overage_cap_reached")
			}
			// Audit row emitted here is intentional — the gate
			// itself is the only decision site that observes the
			// refusal, and pkg/sched/loop.go:1249 `reaper_scale_down`
			// is the precedent for engine-initiated audit writes.
			e.overage.RecordReached(ctx, app.AccountID, observedCents, capCents)
			return wakeOverageCapReached, observedCents, capCents, concurrency, false
		}
	}
	return wakeAdmit, 0, 0, concurrency, concurrency+1 >= maxConc
}

// isOnScaleOutCooldown (PR-C, issue #462) returns true when
// (a) apps.LastScaleOutAt is non-NIL, (b) Concurrency(appID) > 0,
// and (c) time.Since(*apps.LastScaleOutAt) < ScaleOutCooldownS.
//
// The Concurrency > 0 discriminator is load-bearing: it lets a
// cold start (zero concurrency) bypass cooldown even when
// apps.LastScaleOutAt is freshly stamped. Without this check, a
// request-driven wake would always hit cooldown and defeat the
// customer's "rate-limit scale-outs" use case. The "stamp
// missed" direction (LastScaleOutAt == nil → bypass) is safe —
// the wake proceeds normally.
//
// When ScalingPolicy is nil OR ScaleOutCooldownS == 0, the customer
// has not opted into cooldown enforcement, so the branch does
// NOT fire — every existing wake proceeds to the ledger.
func (e *Engine) isOnScaleOutCooldown(app *state.App, concurrency int) bool {
	if concurrency == 0 {
		return false
	}
	if app.LastScaleOutAt == nil {
		return false
	}
	if app.ScalingPolicy == nil || app.ScalingPolicy.ScaleOutCooldownS <= 0 {
		return false
	}
	cooldown := time.Duration(app.ScalingPolicy.ScaleOutCooldownS) * time.Second
	return time.Since(*app.LastScaleOutAt) < cooldown
}

// atMinFloorWithNoSignal (PR-C, issue #462 / issue #557 ADR-071)
// returns true when the customer has opted into floor enforcement
// via ScalingPolicy.MinInstances (jsonb) AND the live concurrency
// has reached that floor. The "no signal" suffix captures the
// customer-facing semantic: a no-inflight-reading wake cannot push
// concurrency above the floor — the dashboard sees this as a no-op
// scale-out attempt.
//
// This gate does NOT read the legacy column (apps.min_instances).
// The legacy column is a reaper-side concern (don't park below
// the floor) plus a billing concern (ADR-060 floor-billed from t=0);
// the proactive wake direction is owned by pkg/sched/floor
// (ADR-071). Mixing the two here would block legitimate request-
// driven wakes on the floor, regressing the §4.3 burst semantics.
//
// When ScalingPolicy is nil OR MinInstances == 0, the customer has
// not opted into floor enforcement, so the branch does NOT fire —
// every existing wake proceeds to the ledger.
func (e *Engine) atMinFloorWithNoSignal(app *state.App, concurrency int) bool {
	if app.ScalingPolicy == nil {
		return false
	}
	if app.ScalingPolicy.MinInstances <= 0 {
		return false
	}
	return concurrency >= app.ScalingPolicy.MinInstances
}

// cooldownSRemaining (PR-D, issue #462) returns the seconds
// until the per-app scale-out cooldown expires. Bounded at 1
// (cooldownS <= 0 is treated as 1) so the wire always emits
// a non-zero hint. RFC 7231 §7.1.3 forbids 0/negative values;
// the bound is a load-bearing UX guarantee — clients that
// consult the header need a positive integer to back off
// correctly. Caller must have validated the stamp and
// concurrency preconditions (admitGate's wakeCooldownHeld).
func cooldownSRemaining(app *state.App, now time.Time) int {
	if app.LastScaleOutAt == nil {
		return 1
	}
	if app.ScalingPolicy == nil || app.ScalingPolicy.ScaleOutCooldownS <= 0 {
		return 1
	}
	cooldown := time.Duration(app.ScalingPolicy.ScaleOutCooldownS) * time.Second
	remaining := cooldown - now.Sub(*app.LastScaleOutAt)
	if remaining <= 0 {
		return 1
	}
	return int(remaining.Seconds())
}

func (e *Engine) lockApp(appID string) func() {
	e.appMutex(appID).Lock()
	return func() { e.unlockApp(appID) }
}

func (e *Engine) unlockApp(appID string) {
	e.appMutex(appID).Unlock()
}

// appMutex returns the stable per-app serialisation mutex, creating it on first
// use. Never GC'd (one-box scale, single-digit apps).
func (e *Engine) appMutex(appID string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	mu, ok := e.appMu[appID]
	if !ok {
		mu = &sync.Mutex{}
		e.appMu[appID] = mu
	}
	return mu
}

// Ledger exposes the engine's admission ledger for the reaper's resident-RAM
// read and for daemon heartbeat logging.
func (e *Engine) Ledger() *NodeLedger { return e.ledger }

// Store exposes the engine's Store so the Loop can build the reaper's
// read-only instance snapshot and read crons.
func (e *Engine) Store() state.Store { return e.store }

// OwnerNodeID returns the Phase 2 / Gate A shard key this
// schedd owns, or "" for the legacy single-box posture. The
// Loop uses this to scope its per-tick reads (reaper, cron,
// scale-up) to apps this schedd is responsible for.
func (e *Engine) OwnerNodeID() string {
	if e == nil {
		return ""
	}
	return e.ownerNodeID
}

// Notifier returns the pg_notify notifier the engine writes through.
// nil-safe: returns a noop when the engine was wired without one
// (tests), so callers don't need to nil-check.
func (e *Engine) Notifier() Notifier {
	if e.notif == nil {
		return noopNotifier{}
	}
	return e.notif
}

// noopNotifier discards every notification. Tests use it; production
// always wires the real pgx-backed notifier in cmd/schedd.
type noopNotifier struct{}

func (noopNotifier) Notify(_ context.Context, _ string, _ string) error { return nil }

// PoolNotifier adapts a pgx pool to the Notifier interface (pg_notify). cmd/schedd
// wires one; the engine and tests depend only on the interface.
type PoolNotifier struct{ Pool *pgxpool.Pool }

func (p PoolNotifier) Notify(ctx context.Context, channel, payload string) error {
	return db.Notify(ctx, p.Pool, channel, payload)
}

// StreamWarmHints (ADR-025 axis 4) is the push-side fanout for
// sticky-warm affinity. It subscribes to the engine's broadcaster
// and invokes sink for every WarmHintEvent until the context
// cancels. Returns nil on a clean shutdown (caller cancels); a
// non-nil sink error propagates so pkg/scheddgrpc.Server.
// StreamWarmHints can carry it back to the gateway caller.
//
// Implementation mirrors Engine.StreamAppLogs (logs.go:60) but
// inverts the channel direction — broadcaster → sink. One
// subscriber channel (buffered at 32, matching StreamAppLogs),
// one writer goroutine reads the channel and invokes the sink.
// The sink runs on the writer goroutine so the proto marshal is
// serialised with the gRPC Send on the scheddgrpc.Server side.
//
// nil broadcaster (a pre-axis-4 fixture that constructed Engine
// without going through NewEngine) is treated as a no-op stream:
// the method returns nil immediately. This keeps the existing
// test fixtures working without a panic — scheddgrpc.Server.
// StreamWarmHints still satisfies the SchedAPI interface, and a
// test that exercises the SchedAPI stub doesn't need the
// broadcaster.
func (e *Engine) StreamWarmHints(ctx context.Context, sink WarmHintSink) error {
	ticker := time.NewTicker(api.WarmHintHeartbeatInterval)
	defer ticker.Stop()
	return e.streamWarmHints(ctx, sink, ticker.C)
}

func (e *Engine) streamWarmHints(ctx context.Context, sink WarmHintSink, heartbeat <-chan time.Time) error {
	if e.warmBroadcaster == nil {
		// Pre-axis-4 fixture. Treat as a clean empty stream so the
		// caller (pkg/scheddgrpc) returns codes.OK + nil and the
		// gateway's consumer treats the early EOF as a normal
		// shutdown.
		<-ctx.Done()
		return nil
	}
	if sink == nil {
		return errors.New("sched: StreamWarmHints requires a non-nil sink")
	}
	ch, unsubscribe := e.warmBroadcaster.subscribe(defaultWarmHintBufCap)
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return nil
		case at := <-heartbeat:
			// Empty IDs and a timestamp form a liveness heartbeat. It never
			// changes placement; Send stays serialized with normal events.
			if err := sink(WarmHintEvent{WrittenAt: at}); err != nil {
				return err
			}
		case ev, ok := <-ch:
			if !ok {
				// Broadcaster closed the channel (Engine shutdown).
				return nil
			}
			if err := sink(ev); err != nil {
				return err
			}
			// On the next loop iteration the select arms
			// <-ctx.Done() again, so a cancellation arriving
			// during sink(ev) is honoured at the top of the
			// next pass — no race against a missed event, and
			// no need for a duplicated ctx.Err() check here.
		}
	}
}
