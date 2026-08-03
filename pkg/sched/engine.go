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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
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
)

// LayerVerifier checks that a cold-boot layer's signature is
// valid. The local interface keeps pkg/sched decoupled from
// pkg/cosign (the verifier impl); the production wiring is
// *cosign.LocalVerifier, constructed in cmd/schedd/main.go.
//
// Returning *api.Problem with code=sig_invalid means "refuse to
// boot this layer" — the engine transitions the deployment to
// DeployFailed and returns 503 to gatewayd. Any other error is a
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
	ops    *wire.OpsMetrics // nil is tolerated by KillStuck (skip the counter increment)
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

	mu    sync.Mutex
	appMu map[string]*sync.Mutex // app_id -> serialisation lock (never GC'd; one-box scale)

	// warmAffinity is the sticky-warm cache (placement scheduler PR,
	// ADR-025). Defaults to a zero-TTL cache that always returns "no
	// hint" so pre-PR test fixtures keep their existing behaviour.
	// Production wires a real cache via WithWarmAffinity (cmd/schedd/
	// main.go). nil is tolerated by RecordWake / LastWarmNode so a
	// missed wiring is a silent no-op rather than a nil-deref panic.
	warmAffinity *WarmAffinity

	// warmBroadcaster is the push-side of sticky-warm affinity
	// (ADR-025 axis 4). Every RecordWake that actually changes the
	// (appID → nodeID) entry fans out a WarmHintEvent to every
	// subscribed consumer (today: every gatewayd's StreamWarmHints
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
		store:           store,
		ledger:          ledger,
		vmm:             vmm,
		notif:           notif,
		fcVer:           fcVer,
		log:             log,
		appMu:           map[string]*sync.Mutex{},
		warmBroadcaster: newWarmHintBroadcaster(),
		capacityTable:   newNodeCapacityTable(),
		now:             time.Now, // tests override post-construction
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

func (e *Engine) WithOwnerNodeID(nodeID string) *Engine {
	if e == nil {
		return e
	}
	e.ownerNodeID = nodeID
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
// placeholder reachable only from gatewayd on the local box. Remote
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
	// gatewayd propagates it back to the client as x-faas-wake-id and
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
func (e *Engine) Wake(ctx context.Context, appID string) (WakeResult, error) {
	// ── Phase 1: fast path under appMu ─────────────────────────────
	release := e.lockApp(appID)
	if ins, err := e.store.RunningInstanceForApp(ctx, appID); err == nil {
		// PR-C (issue #460 / ADR-053): resolve the live deployment so
		// the response's Port field is consistent with what
		// AdmitInstance would have produced. The instance row
		// carries no port (port is a deployment-level concept); the
		// live dep row carries dep.OverridePort.
		//
		// Why a LiveDeployment read is acceptable here: Wake is the
		// legacy fast path used by meterd's per-minute sampler + cron
		// firings, NOT the customer hot path. Production customer
		// requests go through AdmitInstance (cmd/gatewayd/main.go),
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
		if dep, depErr := e.store.LiveDeployment(ctx, appID); depErr == nil {
			port = dep.OverridePort
		} else {
			e.log.Warn("sched: wake: live deployment lookup for port failed; falling through with 0",
				"app", appID, "err", depErr)
		}
		release()
		// Surface the existing row's wake_id so a Phase-1 fast-path
		// response carries x-faas-wake-id just like a cold-wake
		// response would. The correlation handle is the wake that
		// brought the instance up; an operator tailing a warm request
		// can still pin it back to the schedd slog line that stamped
		// it (gaps analysis 2026-07-23 review finding #1).
		return WakeResult{InstanceID: ins.ID, NodeID: ins.NodeID, Method: vmmdpb.WakeMethod_WAKE_RESTORE, WakeID: ins.WakeID, Port: port}, nil
	} else if !errors.Is(err, state.ErrNotFound) {
		release()
		return WakeResult{}, fmt.Errorf("sched: wake: running lookup: %w", err)
	}
	release()
	// Wake preserves the legacy contract: a ledger refusal surfaces
	// as *api.Problem{Code: CodePlanLimitConcur}. The ledger's
	// capacity refusal happens INSIDE admitAndDispatch; we forward
	// rather than lift into the typed AtCapacity result.
	return e.admitAndDispatch(ctx, appID, false)
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
func (e *Engine) AdmitInstance(ctx context.Context, appID string) (WakeResult, error) {
	return e.admitAndDispatch(ctx, appID, true)
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
func (e *Engine) admitAndDispatch(ctx context.Context, appID string, liftCapacityToResult bool) (WakeResult, error) {
	// ── Phase 2: admit window, under appMu ──────────────────
	release := e.lockApp(appID)
	app, acct, limits, dep, err := e.resolveApp(ctx, appID)
	if err != nil {
		release()
		return WakeResult{}, err
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
	if app.WorkloadClass == state.WorkloadClassWorker {
		release()
		return WakeResult{AtCapacity: true}, nil
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
	if outcome := e.admitGate(ctx, &app, limits); outcome != wakeAdmit {
		release()
		switch outcome {
		case wakeRejectAtCap:
			if liftCapacityToResult {
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
	wakeID := wakeUUID.String()

	// Restore iff a fresh, version-matched snapshot exists; else cold boot
	// (ADR-005: cold boot always works, snapshot is cache).
	snap, haveSnap := e.usableSnapshot(ctx, dep.ID)

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
	warmHint, _ := e.warmAffinity.LastWarmNode(appID)
	placement, err := e.choosePlacementLocked(ctx, Request{
		AppID: appID, Plan: acct.Plan,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency,
		PreferredNodeID: warmHint,
	})
	if err != nil {
		release()
		return WakeResult{}, err // *api.Problem from chooser
	}
	// Sticky-warm record: stamp the chosen node so the NEXT wake
	// for this app picks it back up. Recorded after a successful
	// admit only — a rejection doesn't "warm" anything. Per-app
	// lock is held here so a concurrent burst for the same app
	// sees a coherent (RecordWake, hint) sequence.
	//
	// Push-side fanout (ADR-025 axis 4): if the new entry actually
	// changed appID's warm node, broadcast a WarmHintEvent to every
	// gatewayd subscribed via Engine.StreamWarmHints. Same per-app
	// lock guards the cache write + the emit so the gRPC stream
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
	ins, err := e.store.CreateInstance(ctx, appID, dep.ID, string(initState), app.RAMMB, placement.NodeID, wakeID)
	if err != nil {
		release()
		return WakeResult{}, fmt.Errorf("sched: wake: create instance: %w", err)
	}
	e.emitInstanceChanged(ctx, ins.ID, appID, initState, wakeID)

	if err := e.ledger.Admit(Request{
		Instance: ins.ID, AppID: appID, Plan: acct.Plan,
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
			return WakeResult{AtCapacity: true}, nil
		}
		e.transitionWithKind(ctx, ins.ID, appID, state.StateFailed, "wake_boot_error", "admit_denied")
		release()
		return WakeResult{}, err // *api.Problem
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
	if err := e.store.StampAppScaleOut(ctx, appID); err != nil {
		e.log.Warn("sched: stamp apps.last_scale_out_at failed", "app", appID, "err", err)
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

	sealedEnv, err := e.loadSealedEnvFor(ctx, acct.ID, appID, envSecretsFromDep(dep))
	if err != nil {
		return WakeResult{}, fmt.Errorf("sched: wake: load sealed env: %w", err)
	}
	spec := AppSpec{
		BaseKey: baseKey(app.Runtime), LayerKey: layerKey(dep.RootfsKey, dep.ID),
		VCPUCount: int32(limits.VCPU), MemSizeMiB: int32(app.RAMMB),
		EgressMbit: int32(limits.EgressMbit),
		SealedEnv:  sealedEnv,
		// Issue #395 / ADR-045: plaintext api_env layer mirrors the
		// sealed secrets surface but stores non-sensitive runtime
		// config. Precedence at the guest layer is "secrets >
		// api_env > manifest_env > os.environ".
		APIEnv: e.loadAPIEnv(ctx, acct.ID, appID),
		// ADR-031: surface the per-app egress allowlist on the
		// wake wire. vmmd translates the CIDRs into the per-netns
		// forward chain. Empty slice = no allowlist rule (current
		// behaviour); the apps_changed pg_notify handler at the
		// top of Wake re-reads the app row under a fresh ledger
		// lock, so a PATCH that lands between two wakes takes
		// effect on the next wake. Live instances keep their
		// old netns — same contract as RAMMB and MaxConcurrency.
		EgressAllowlist: prefixesToCIDRStrings(app.EgressAllowlist),
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
			// Wrap in a *api.Problem so gatewayd's writeWakeError
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
	bootCtx, cancel := context.WithTimeout(ctx, e.budgetFor(bootInput.initState))
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
	// (request_id / invocation_id from gatewayd) and join the
	// engine-minted wake_id / instance_id so a single inbound id
	// carries across the schedd → vmmd boundary.
	inboundCorr, _ := wire.FromContext(ctx)
	bootCtx = wire.WithContext(bootCtx, wire.CorrelationFields{
		RequestID:    inboundCorr.RequestID,
		InvocationID: inboundCorr.InvocationID,
		AppID:        appID,
		DeploymentID: bootInput.depID,
		InstanceID:   bootInput.insID,
		WakeID:       wakeID,
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
			EmitAt:      time.Now().UTC(),
			WakeID:      bootInput.wakeID,
			AppID:       bootInput.appID,
			InstanceID:  bootInput.insID,
			NodeID:      bootInput.nodeID,
			Method:      method,
			RequestedAt: bootInput.startedAt, // best-effort stamp
		})
	}
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
		vmstatePath := e.vmstateHostPathFor(bootInput.depID)
		vmstateStorageKey := e.vmstateStorageKeyFor(bootInput.nodeID, bootInput.depID)
		// Issue #555 PR-3: vmmd.create_from_snapshot child span.
		// The vmmd-side gRPC stats handler (otelgrpc) starts a
		// server span for the CreateFromSnapshot RPC; this client
		// span is the parent linkage in the trace tree.
		var createSpan oteltrace.Span
		if e.tracer != nil {
			bootCtx, createSpan = e.tracer.Start(bootCtx, "vmmd.create_from_snapshot",
				oteltrace.WithAttributes(
					attribute.String("app_id", bootInput.appID),
					attribute.String("instance_id", bootInput.insID),
					attribute.String("snap_id", bootInput.snapID),
					attribute.String("deployment_id", bootInput.depID),
				),
			)
		}
		out, err = e.vmm.CreateFromSnapshot(bootCtx, bootInput.nodeID, bootInput.insID, bootInput.spec, SnapshotRef{
			DeploymentID:      bootInput.depID,
			FCVersion:         bootInput.snapVer,
			StorageKey:        bootInput.snapKey,
			VMStatePath:       vmstatePath,
			VMStateStorageKey: vmstateStorageKey,
		})
		if createSpan != nil {
			createSpan.End()
		}
	} else {
		// Either no snap row at all (cold path), or a snap row with
		// an empty StorageKey (F-1 contract violation — fall back to
		// a real cold boot per ADR-005: snapshots are cache, not
		// truth; wake must never depend on a snapshot existing).
		// Issue #555 PR-3: vmmd.create_cold_boot child span.
		var createSpan oteltrace.Span
		if e.tracer != nil {
			bootCtx, createSpan = e.tracer.Start(bootCtx, "vmmd.create_cold_boot",
				oteltrace.WithAttributes(
					attribute.String("app_id", bootInput.appID),
					attribute.String("instance_id", bootInput.insID),
					attribute.String("deployment_id", bootInput.depID),
				),
			)
		}
		out, err = e.vmm.CreateColdBoot(bootCtx, bootInput.nodeID, bootInput.insID, bootInput.spec)
		if createSpan != nil {
			createSpan.End()
		}
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
			EmitAt:      now,
			WakeID:      bootInput.wakeID,
			AppID:       bootInput.appID,
			InstanceID:  bootInput.insID,
			NodeID:      bootInput.nodeID,
			Method:      completedMethod,
			StartedAt:   bootInput.startedAt,
			CompletedAt: now,
		})
	}

	return WakeResult{InstanceID: bootInput.insID, NodeID: fresh.NodeID, Method: out.Method, WakeID: bootInput.wakeID, Port: bootInput.spec.Port}, nil
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
	nodes, err := e.store.ActiveComputeNodes(ctx)
	if err != nil {
		return Placement{}, fmt.Errorf("sched: placement: list active compute_nodes: %w", err)
	}
	// One pass over the fleet — read each node's live-or-store UsedMB
	// (Tier A1) and the ledger's per-node UsedVCPU (Tier A2) in
	// lockstep. The two reads are independent (the live table is a
	// vmmd-side cgroup mirror, the ledger is schedd's local
	// reservations), so the lockstep is just a fusion of two
	// contiguous maps for the chooser. The chooser enforces them
	// independently downstream.
	usedMB := make(map[string]int64, len(nodes))
	usedVCPU := make(map[string]int64, len(nodes))
	for _, n := range nodes {
		if used := e.applyLiveCapacityMB(ctx, n.ID); used > 0 {
			usedMB[n.ID] = used
		} else {
			// No live report (table miss / stale) or live says zero:
			// fall through to the store sum. A single node's transient
			// store error must not block placement of the rest; the
			// next wake re-reads; a permanent failure surfaces there.
			used, err := e.store.ComputeNodeUsedMB(ctx, n.ID)
			if err != nil {
				e.log.Warn("sched: placement: compute node used_mb read failed",
					"node_id", n.ID, "node_name", n.Name, "err", err)
				used = 0
			}
			usedMB[n.ID] = used
		}
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
	sealedEnv, err := e.loadSealedEnvFor(ctx, app.AccountID, app.ID, envSecretsFromDep(dep))
	if err != nil {
		return AppSpec{}, fmt.Errorf("sched: build app spec: sealed env: %w", err)
	}
	return AppSpec{
		BaseKey:    baseKey(app.Runtime),
		LayerKey:   layerKey(dep.RootfsKey, dep.ID),
		VCPUCount:  int32(limits.VCPU),
		MemSizeMiB: int32(app.RAMMB),
		EgressMbit: int32(limits.EgressMbit),
		SealedEnv:  sealedEnv,
		// ADR-045: api_env plaintext layer; the loadAPIEnv
		// helper already fail-softs on a lookup error and logs
		// Warn (engine.go:2382-2396). A hiccup here ships an
		// empty api_env block, NOT a failed migration — the
		// overlayfs upper layers carry the same precedence
		// rules as Wake time and the customer's runtime config
		// (most of it) lives in sealedEnv + manifest_env.
		APIEnv: e.loadAPIEnv(ctx, app.AccountID, app.ID),
		// ADR-031: per-app egress allowlist; same CIDR-string
		// flattening as the Wake path.
		EgressAllowlist: prefixesToCIDRStrings(app.EgressAllowlist),
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

	harness := NewMigrationHarness(e.store, e.vmm, e.ops, e.log,
		e.ownerNodeID, e.BuildAppSpecForMigration)
	harness.SetMaxPerTick(maxPerTick)
	leaseSeconds := api.MigrateLiveLeaseSeconds
	if e.migrateLiveLeaseSeconds > 0 {
		leaseSeconds = e.migrateLiveLeaseSeconds
	}
	harness.SetLeaseSeconds(leaseSeconds)

	migrated, attempted := 0, 0
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
	placement, err := e.choosePlacementLocked(ctx, Request{
		AppID: appID, Plan: acct.Plan,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency,
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
	ins, err := e.store.CreateInstance(ctx, appID, deploymentID, string(state.StateColdBooting), app.RAMMB, placement.NodeID, primeWakeID)
	if err != nil {
		return fmt.Errorf("sched: prime: create instance: %w", err)
	}
	e.emitInstanceChanged(ctx, ins.ID, appID, state.StateColdBooting, primeWakeID)

	if err := e.ledger.Admit(Request{
		Instance: ins.ID, AppID: appID, Plan: acct.Plan,
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
	sealedEnv, err := e.loadSealedEnvFor(ctx, acct.ID, appID, envSecretsFromDep(dep))
	if err != nil {
		return fmt.Errorf("sched: prime: load sealed env: %w", err)
	}
	spec := AppSpec{
		BaseKey: baseKey(app.Runtime), LayerKey: layerKey(dep.RootfsKey, dep.ID),
		VCPUCount: int32(limits.VCPU), MemSizeMiB: int32(app.RAMMB),
		EgressMbit: int32(limits.EgressMbit),
		SealedEnv:  sealedEnv,
		// Issue #395 / ADR-045: plaintext api_env layer mirrors the
		// sealed secrets surface but stores non-sensitive runtime
		// config. Precedence at the guest layer is "secrets >
		// api_env > manifest_env > os.environ".
		APIEnv: e.loadAPIEnv(ctx, acct.ID, appID),
		// ADR-031: see the Wake builder above. Prime is the
		// deploy-pipeline first boot — same wire shape, same
		// per-netns ruleset; a freshly-deployed app starts under
		// its declared egress policy rather than awaiting a later
		// wake.
		EgressAllowlist: prefixesToCIDRStrings(app.EgressAllowlist),
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
			// branch. Wrap as a Problem so gatewayd's writeWakeError
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
	return e.snapshotAndPark(ctx, *ins)
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
func (e *Engine) ReportActivity(ctx context.Context, touches []state.InstanceTouch) (int, error) {
	return e.store.TouchInstancesLastSeen(ctx, touches)
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
	// vmstate is a small JSON the FC socket writes to during pause; we
	// give it a host path under the snap dir (the local driver maps the
	// storage_key back to this exact location on the next restore, so
	// the two paths must agree).
	//
	// #121 / ADR-025 axis 2 slice 4 — remote nodes no longer need a
	// host path: the engine also computes vmstateStorageKey below and
	// threads it; vmmd chooses which carrier to use based on the
	// field's empty/non-empty value. Default-local always sends empty
	// vmstateStorageKey and the legacy host-path branch is taken
	// bit-for-bit.
	vmstate := SnapDir() + "/" + ins.DeploymentID + "/vmstate"
	// #96 / ADR-025 axis 2: the canonical storage key under which vmmd
	// publishes the mem blob via the StorageBackend. The local driver
	// maps "snap/<dep>/mem" to /srv/fc/snap/<dep>/mem; the OCI driver
	// streams the bytes over HTTP.
	storageKey := state.SnapMemKey(ins.DeploymentID)
	// #121 / ADR-025 axis 2 slice 4: canonical StorageBackend key for
	// the vmstate blob when the new carrier is in scope. Empty for
	// default-local; populated for remote nodes.
	vmstateStorageKey := e.vmstateStorageKeyFor(ins.NodeID, ins.DeploymentID)
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
			EmitAt:     now.UTC(),
			WakeID:     ins.WakeID,
			AppID:      ins.AppID,
			InstanceID: ins.ID,
			NodeID:     ins.NodeID,
			StartedAt:  now.UTC(),
		})
	}

	b, err := e.vmm.PauseAndSnapshot(ctx, ins.NodeID, ins.ID, vmstate, storageKey, vmstateStorageKey)
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
			EmitAt:      time.Now().UTC(),
			WakeID:      ins.WakeID,
			AppID:       ins.AppID,
			InstanceID:  ins.ID,
			NodeID:      ins.NodeID,
			StartedAt:   now.UTC(),
			CompletedAt: time.Now().UTC(),
			SnapshotID:  storageKey,
		})
	}
	e.emitSnapshotWritten(ctx, ins.DeploymentID, vmstate, b)
	return nil
}

// resolveApp loads the app, account, plan limits, and current live deployment a
// wake needs. A missing live deployment is a *api.Problem (an app should always
// have one, invariant §6.2-3).
func (e *Engine) resolveApp(ctx context.Context, appID string) (state.App, state.Account, api.Limits, state.Deployment, error) {
	app, acct, limits, err := e.resolveAppForDeploy(ctx, appID)
	if err != nil {
		return state.App{}, state.Account{}, api.Limits{}, state.Deployment{}, err
	}
	dep, err := e.store.LiveDeployment(ctx, appID)
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
func (e *Engine) loadSealedEnvFor(ctx context.Context, accountID, appID string, overrideEnvSecrets map[string]string) ([]fcvm.SealedEnvEntry, error) {
	rows, err := e.store.ListAppSecrets(ctx, accountID, appID)
	if err != nil {
		return nil, fmt.Errorf("load sealed env (account=%s app=%s): %w", accountID, appID, err)
	}
	if len(overrideEnvSecrets) == 0 {
		// Legacy path: stage everything for the app. Preserved for
		// pre-PR-A deployments without override columns populated AND for
		// tarball/dockerfile deploys that don't use the override surface.
		out := make([]fcvm.SealedEnvEntry, 0, len(rows))
		for _, r := range rows {
			out = append(out, fcvm.SealedEnvEntry{Key: r.Key, Ciphertext: r.Ciphertext})
		}
		return out, nil
	}
	// Filtered path: build a Key→row index, then iterate the override's
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
		// different orders on different wakes.
		sort.Strings(missing)
		return nil, fmt.Errorf("env_secrets: missing app_secrets rows for %s on (%s, %s); set the secret first via faas secrets set",
			strings.Join(missing, ", "), accountID, appID)
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
// ADR-045). Reads the per-app app_envs rows and flattens them into
// the fcvm shape Manager.Wake consumes. Same non-fatal read-failure
// posture as loadSealedEnv — a transient PG hiccup drops the env
// layer (the next wake retries) rather than failing the wake itself.
// Plaintext by contract so there's nothing to leak; the worst case
// is a missing env var, which customer support can spot from the
// "API env X missing" log line.
//
// Carries AccountID explicitly so a cross-account (accountID, appID)
// pair returns ErrNotFound (consistent with apid's 404 contract).
func (e *Engine) loadAPIEnv(ctx context.Context, accountID, appID string) []fcvm.APIEnvEntry {
	rows, err := e.store.ListAppEnv(ctx, accountID, appID)
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

// usableSnapshot returns the freshest non-stale snapshot for a deployment iff it
// was made with the running Firecracker version (ADR-005 pinning).
func (e *Engine) usableSnapshot(ctx context.Context, deploymentID string) (state.Snapshot, bool) {
	snap, err := e.store.LatestSnapshot(ctx, deploymentID)
	if err != nil || snap.Stale || snap.FCVersion != e.fcVer {
		return state.Snapshot{}, false
	}
	return snap, true
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

// transitionWithKind is the audit-log-emitting variant of transition.
// Callers that need a non-default kind (Wake's "wake_boot_error" path,
// KillStuck's "watchdog_timeout", snapshotAndPark's "park_snapshot_error")
// go through here. The transition body itself is unchanged from
// transition() — only the appended events row differs.
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

func (e *Engine) emitSnapshotWritten(ctx context.Context, deploymentID, vmstatePath string, b SnapshotBytes) {
	if e.notif == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"deployment_id": deploymentID,
		"vmstate_path":  vmstatePath,
		"storage_key":   state.SnapMemKey(deploymentID),
		"mem_bytes":     b.MemBytes,
		"vmstate_bytes": b.VMStateBytes,
		"fc_version":    e.fcVer,
	})
	if err := e.notif.Notify(ctx, db.NotifySnapshotWritten, string(payload)); err != nil {
		e.log.Warn("emit snapshot_written", "deployment", deploymentID, "err", err)
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
func (e *Engine) admitGate(_ context.Context, app *state.App, limits api.Limits) wakeOutcome {
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
		return wakeRejectAtCap
	}
	if e.isOnScaleOutCooldown(app, concurrency) {
		if e.ops != nil {
			e.ops.ObserveScaleUp(app.ID, "cooldown_held")
		}
		return wakeCooldownHeld
	}
	if e.atMinFloorWithNoSignal(app, concurrency) {
		if e.ops != nil {
			e.ops.ObserveScaleUp(app.ID, "min_floor_already")
		}
		return wakeMinFloorAlready
	}
	return wakeAdmit
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

// atMinFloorWithNoSignal (PR-C, issue #462) returns true when the
// customer has configured ScalingPolicy.MinInstances > 0 AND the
// live concurrency has reached that floor. The "no signal" suffix
// captures the customer-facing semantic: an idle wake (no inflight
// reading) cannot push concurrency below the floor — the
// dashboard sees this as a no-op scale-out attempt.
//
// When ScalingPolicy is nil OR MinInstances == 0, the customer has
// not opted into floor enforcement, so the branch does NOT fire —
// every existing wake proceeds to the ledger. PR-D closes the
// worker-class bypass closure and adds an explicit wake-path gate
// for worker-class apps (the targets trigger currently no-ops for
// them because MaxInflightForApp returns (0, false)).
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
