// Package sched — daemon glue that translates pg_notify events into ledger
// updates and instance state writes. schedd is the sole writer to the
// instances table (spec §Component ownership); this file owns the loop that
// reacts to apid's notifications and drives the reaper tick. All instance
// mutation (create, transition, snapshot, destroy) goes through the Engine —
// the Loop is pure glue that decides *when* to act, not *how*.
package sched

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/sched/floor"
	"github.com/onebox-faas/faas/pkg/sched/recentload"
	"github.com/onebox-faas/faas/pkg/sched/scaleup"
	"github.com/onebox-faas/faas/pkg/sched/targets"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// reaperParkTimeout bounds the synchronous Engine.Park call made by the
// scheduler loop. Firecracker snapshot creation is normally fast, but a
// saturated disk can take considerably longer; without a bound, one stalled
// park blocks the loop and prevents the state watchdog from reclaiming the
// row. Deployment priming is intentionally separate and keeps its existing
// build/snapshot budget.
const reaperParkTimeout = 2 * time.Minute

// lifecycleReconcileMaxPerTick bounds the durable deletion sweep. Deletion
// cleanup is retryable and oldest-first at the store layer, so a bounded pass
// gives every schedd tick a predictable cost without allowing one abandoned
// account or app to monopolise the scheduler.
const lifecycleReconcileMaxPerTick = 100

// Loop subscribes to the pg_notify channels schedd cares about and reacts. It
// runs the idle reaper on a 10 s tick and cron on a 60 s tick (spec §4.3). The
// Engine holds the store, ledger, and vmmd client; the Loop only orchestrates.
type Loop struct {
	pool    *pgxpool.Pool
	engine  *Engine
	log     *slog.Logger
	gateway GatewaySynth
	// Issue #757 / ADR-0NN (commit #14): the trigger dispatch tick
	// posts batch envelopes to the gateway's batch endpoint.
	// gatewayHTTPClient + gatewayBaseURL carry the http.Client +
	// base URL so dispatch_triggers.go doesn't have to reach
	// through the GatewaySynth interface.
	gatewayHTTPClient *http.Client
	gatewayBaseURL    string
	// triggerPollers caches one triggerSource per trigger id. The
	// cache is invalidated by NotifyTriggerChanged (commit #16);
	// for now we never rebuild within a process lifetime.
	triggerPollers map[string]triggerSource
	// rateLimiter is the per-app wake rate limiter (shared with
	// cron dispatch via pkg/sched/rate_limit.go).
	rateLimiter *WakeRateLimiter
	// triggerWakeup is the channel-side wakeup signal the schedd's
	// pg_notify subscriber delivers on every NotifyTriggerReady +
	// NotifyTriggerChanged payload (commit #16). The Loop's run
	// selects on it alongside the 1s ticker so an idle broker
	// doesn't sit for a full 1s tick before the first batch.
	triggerWakeup     chan struct{}
	triggerWakeupOnce sync.Once
	// primeSlots bounds how many snapshot_prime handlers run off the
	// main select goroutine. Prime is the one notification handler that
	// does VM work (cold boot + snapshot), so it is the only one that
	// can stall the shared loop for tens of seconds; every other case
	// in handleNotification is a cheap DB or cache operation and stays
	// inline. See dispatchPrime.
	primeSlots           chan struct{}
	primeSlotsOnce       sync.Once
	now                  func() time.Time
	flowCounts           FlowCounter
	ops                  *wire.OpsMetrics       // issue #171 shared registry; nil safe
	audit                *audit.Auditor         // cron-fired audit row writer; nil opts out (no row written)
	watchdog             *Watchdog              // §6.1 watchdog; nil means "no watchdog" (tests can opt out)
	retention            *Retention             // §17 retention sweep; nil means "no retention" (tests can opt out)
	invocationsRetention *InvocationsRetention  // ADR-134 PR-B: invocations retention + deadline-breach sweep; nil opts out
	triggersRetention    *TriggersRetention     // ADR-134 PR-E: trigger_records retention sweep; nil opts out
	heartbeat            *Heartbeat             // issue #97 / ADR-025 axis 3 (PR #114) per-node liveness; nil opts out
	diskDrift            *DiskDrift             // PR scale-out readiness #3 read-only /srv/fc/snap vs DB drift sweep; nil opts out
	migratingWatchdog    *MigratingWatchdog     // Tier A6 / ADR-067 wedged-migration self-healer; nil opts out
	deadNodeReconciler   *DeadNodeReconciler    // dead-node billing-leak self-healer; nil opts out (no ticker arm)
	instStats            InstanceStatsPoller    // issue #170 / PR-A per-{app,node} metrics poller; nil opts out
	scaleup              *scaleup.Trigger       // issue #169 / #172 reactive scale-up trigger; nil opts out
	scaleupMu            sync.Mutex             // serializes asynchronous scale-up ticks
	scaleupRunning       bool                   // true while one scale-up tick is in flight
	targets              *targets.Trigger       // issue #462 (PR-C) concurrent_requests target trigger; nil opts out
	floor                *floor.Trigger         // issue #557 / ADR-071 proactive min-instances floor reconciler; nil opts out
	recentLoad           *recentload.RecentLoad // issue #171 aggressive-reaper signal mirror; nil opts out
	livenessWindow       *LivenessWindow        // issue #554 / ADR-078 per-deployment liveness-restart tracker; nil opts out (Engine does not call ParkDeployment)
	appDelete            *AppDeleteSubscriber   // ADR-098 app_delete handler; nil = no-op dispatch (tests / opt-out)
	reaperAggressive     bool                   // issue #171 FAAS_REAPER_AGGRESSIVE; default ON; false = skip the new path
	reaperParkCap        int                    // issue #171 per-app per-tick park cap; default MaxParksPerTickPerApp
	// lastFloorByApp (issue #557 closure / ADR-072): per-app
	// effective floor from the previous reaper tick, used to emit
	// `instances.parked_min_instances_released` when the floor
	// drops and the idle park actually frees an instance below
	// the previous bound. The aggressive branch's
	// floorByApp > postPark predicate is structurally
	// unsatisfiable (ReapAggressive's own `limit` arithmetic
	// never parks below the floor it was given), so the audit
	// emit lives on the idle path instead — keyed on
	// lastFloorByApp > effectiveFloor AND ≥1 instance was parked
	// by ReapIdle for the app this tick.
	lastFloorByApp map[string]int
	// brokerAccountor (issue #757 / ADR-118 commit 8) — the
	// per-tick broker-egress accounting seam. nil opts out
	// (noop-on-nil semantics; the dispatch hot path guards
	// against nil because tests + pre-commit-8 wiring never
	// reaches this surface). Production wires NewBrokerAccountor
	// via WithBrokerAccountor.
	brokerAccountor BrokerAccountor
	// appPublicAuthModeLookup (ADR-119) is the Loop-level sibling
	// of httpGatewaySynth.appPublicAuthModeLookup. The trigger
	// dispatch tick (postBatch) does NOT go through
	// httpGatewaySynth — it uses l.gatewayHTTPClient directly —
	// so the auth-state lookup + minter are wired here too.
	// nil = every app is treated as "open" (no Authorization
	// header attached). Same nil-safe posture as the synth
	// version; the cmd-side wiring (cmd/schedd/main.go) sets
	// both via WithAppPublicAuthModeLookup + WithMintInternalSvcToken.
	appPublicAuthModeLookup PublicAuthModeLookupFunc
	// mintInternalSvcToken (ADR-119) Loop-level sibling of
	// httpGatewaySynth.mintInternalSvcToken. Same nil-safe
	// posture; nil + internal_only mode = loud warn + the gate
	// 403s on the receiving end.
	mintInternalSvcToken func(appID string) (string, error)

	// jobsDispatched is the FAAS_JOBS_DISPATCH opt-in for the
	// job dispatch tick + stuck-job reaper. When false (the
	// default), both tickers are skipped and queued job_tasks
	// stay in the DB (Mega-1 cluster-wide gate).
	jobsDispatched bool

	// workflowsDispatched is the FAAS_WORKFLOWS_ENABLED opt-in for the
	// workflow dispatch tick (ADR-081).
	workflowsDispatched bool
	workflowOrch        *WorkflowOrchestrator
	workflowRetention   *WorkflowRetention
}

func NewLoop(pool *pgxpool.Pool, engine *Engine, log *slog.Logger) *Loop {
	return &Loop{
		pool: pool, engine: engine, log: log,
		now:        time.Now,
		flowCounts: noopFlowCounter{},
	}
}

// WithJobsDispatched opts the Loop into the jobs dispatch + reaper
// ticker. Default OFF so the Mega-1 surface stays opt-in behind
// FAAS_JOBS_DISPATCH=1 — the schedd reads the env, then attaches via
// this method. When OFF, dispatchJobsTick + JobReaperTick never fire
// and queued job_tasks just sit in DB (the operator chose this).
func (l *Loop) WithJobsDispatched(enabled bool) *Loop {
	l.jobsDispatched = enabled
	return l
}

// WithWorkflowsDispatched opts the Loop into the workflow dispatch ticker (ADR-081).
func (l *Loop) WithWorkflowsDispatched(enabled bool) *Loop {
	l.workflowsDispatched = enabled
	return l
}

// WithWorkflowOrchestrator sets the orchestrator instance for workflow dispatch.
func (l *Loop) WithWorkflowOrchestrator(o *WorkflowOrchestrator) *Loop {
	l.workflowOrch = o
	return l
}

// WithWorkflowRetention attaches the workflow retention cleaner.
func (l *Loop) WithWorkflowRetention(r *WorkflowRetention) *Loop {
	l.workflowRetention = r
	return l
}

// WithAudit attaches the IAM-4 audit seam so the cron-fire path can
// emit a `cron.fired` events row once the dispatch loop has decided
// to fire a cron (i.e. after the boundary guard AND the suspended-
// account guard). nil opts out (legacy / tests that don't care about
// the audit row).
func (l *Loop) WithAudit(a *audit.Auditor) *Loop {
	l.audit = a
	return l
}

// WithWatchdog attaches the §6.1 watchdog (commit 3). Tests can skip
// it by not calling this; the watchdog field stays nil and Run's
// 4th ticker simply never fires a case. Production cmd/schedd wires
// the real Watchdog from the existing engine deps so the watchdog
// shares the same store / engine / clock as the rest of the loop.
func (l *Loop) WithWatchdog(w *Watchdog) *Loop {
	l.watchdog = w
	return l
}

// WithRetention attaches the §17 retention sweep (PR #74). Same opt-out
// shape as WithWatchdog: nil means no ticker fires the retention case.
// Production wires NewRetention(store, log); the default retention
// window + interval live in pkg/api/limits.
func (l *Loop) WithRetention(r *Retention) *Loop {
	l.retention = r
	return l
}

// WithInvocationsRetention attaches the ADR-134 PR-B invocations
// sweep: deletes rows whose result_retention_until has passed and
// transitions (pending|dispatching) rows whose deadline_at has
// passed to dead_letter. Same nil-skip semantics as WithRetention.
func (l *Loop) WithInvocationsRetention(r *InvocationsRetention) *Loop {
	l.invocationsRetention = r
	return l
}

// WithTriggersRetention attaches the ADR-134 PR-E trigger_records
// retention sweep. Same nil-skip semantics as WithInvocationsRetention.
func (l *Loop) WithTriggersRetention(r *TriggersRetention) *Loop {
	l.triggersRetention = r
	return l
}

// WithAppDeleteSubscriber attaches the ADR-098 app_delete handler
// that evicts any in-flight wake for a deleted app via the wake
// coordinator (Engine.wakeCoord.Forget). The notification is
// consumed off the loop's existing LISTEN (db.NotifyAppDelete is
// added to the channel list below), so this option costs zero
// additional pgxpool connections — the same zero-cost pattern used
// for NotifyCronRunNow above (PR-D / issue #791).
//
// nil = no dispatch (tests / opt-out). nil is safe because the
// notify channel is still LISTENed (Postgres accepts unknown
// channels silently); handleNotification just no-ops on
// NotifyAppDelete when the field is nil.
func (l *Loop) WithAppDeleteSubscriber(d *AppDeleteSubscriber) *Loop {
	l.appDelete = d
	return l
}

// WithHeartbeat attaches the per-node liveness sweep (issue #97 /
// ADR-025 axis 3, PR #114). Same nil-skip semantics as the
// watchdog + retention tickers — production cmd/schedd wires
// sched.NewHeartbeat(store, vmmRouter, log); tests inject a fake
// or skip. The interval lives on the Heartbeat itself
// (DefaultHeartbeatInterval = 30s; overridable for tests).
func (l *Loop) WithHeartbeat(h *Heartbeat) *Loop {
	l.heartbeat = h
	return l
}

// WithDiskDrift attaches the read-only /srv/fc/snap vs DB drift
// sweep (PR scale-out readiness #3). Same nil-skip semantics as
// WithHeartbeat: nil means the ticker block in Run is skipped, the
// select case never fires, and the counter stays at zero. Production
// cmd/schedd wires sched.NewDiskDrift(store, log).WithMetrics(ops);
// tests inject a fake or skip. Cadence is api.DefaultDiskDriftInterval
// (1h) — same hourly cadence as WithRetention so the two sweeps
// fire on aligned boundaries. The sweep is diagnostic only (never
// writes, never follows symlinks, never repairs); operators read
// rate(snapshot_disk_drift_total[5m]) and alert on a non-zero rate.
func (l *Loop) WithDiskDrift(d *DiskDrift) *Loop {
	l.diskDrift = d
	return l
}

// InstanceStatsPoller is the narrow interface the Loop's per-tick
// instance-stats worker exposes (issue #170 / PR-A). Production
// wires *instancestats.Poller; tests inject a fake. The interface
// lives here — not in pkg/sched/instancestats — to avoid the
// import cycle "instancestats → sched → instancestats" the
// existing flowcount pattern established (the interface is the
// contract the Loop reads; the concrete type lives behind it).
type InstanceStatsPoller interface {
	Tick(ctx context.Context) error
	TickInterval() time.Duration
}

// WithInstanceStats attaches the per-instance metrics poller
// (issue #170 / PR-A). Same nil-skip semantics as the heartbeat
// ticker — production wires instancestats.NewPoller(...); tests
// inject a fake or skip. The interval lives on the poller itself
// (instancestats.DefaultStatsInterval = 200 ms — 5 Hz, the 250 ms
// spike-capture acceptance gate). The reader the poller populates
// is the canonical seam #171 (reaper scale-down bias) and #169
// (reactive scale-up trigger) will read from.
func (l *Loop) WithInstanceStats(p InstanceStatsPoller) *Loop {
	l.instStats = p
	return l
}

// WithGatewaySynth wires the gateway-internal RPC client the cron
// dispatch loop uses. Production calls this from cmd/schedd after
// dialing the gateway socket; tests inject a recording stub.
func (l *Loop) WithGatewaySynth(g GatewaySynth) *Loop {
	l.gateway = g
	return l
}

// WithGatewayHTTPClient wires the HTTP transport used by the ESM
// batch dispatcher (issue #757 / ADR-100, commit #14). The cron
// path uses WithGatewaySynth above (RPC over unix socket); the
// trigger dispatch path posts JSON envelopes to the gateway's
// /v1/invocations:dispatch_batch endpoint over plain HTTP. The
// reason for the split is that the batch endpoint reuses the
// existing gateway HTTP server's mux (no separate dial needed).
//
// baseURL must include scheme + host + port but NOT a trailing
// slash (postBatch concatenates "/v1/invocations:dispatch_batch").
// nil opts out (tests that don't exercise the dispatch tick).
func (l *Loop) WithGatewayHTTPClient(client *http.Client, baseURL string) *Loop {
	l.gatewayHTTPClient = client
	l.gatewayBaseURL = baseURL
	return l
}

// WithAppPublicAuthModeLookup (ADR-119) arms the Loop-level
// mode lookup that postBatch consults before posting the
// trigger batch envelope. Mirror of
// httpGatewaySynth.WithAppPublicAuthModeLookup (loop.go:1925);
// the trigger dispatch path uses gatewayHTTPClient directly
// rather than the GatewaySynth interface, so the lookup lives
// on Loop too. nil = every app treated as "open".
func (l *Loop) WithAppPublicAuthModeLookup(lookup PublicAuthModeLookupFunc) *Loop {
	l.appPublicAuthModeLookup = lookup
	return l
}

// WithMintInternalSvcToken (ADR-119) arms the Loop-level JWT
// minter that postBatch calls when the app is in
// 'internal_only' mode. Mirror of
// httpGatewaySynth.WithMintInternalSvcToken; the trigger
// dispatch path is the third outbound surface that needs the
// JWT (after SynthesizeRequest + Invoke). nil = the gate would
// 403 every internal_only batch — surfaced as a loud warn so
// an operator sees the misconfig.
func (l *Loop) WithMintInternalSvcToken(mint func(appID string) (string, error)) *Loop {
	l.mintInternalSvcToken = mint
	return l
}

// WithClock swaps the time source. Tests use it to advance through cron
// boundaries deterministically; production leaves the default.
// WithBrokerAccountor attaches the per-tick broker-egress
// accounting seam (issue #757 / ADR-118 commit 8). nil opts
// out: the dispatch hot path guards against a nil accountor
// and silently drops the byte count (the noop semantics). The
// default constructed by NewLoop is noopBrokerAccountor{}; the
// production wiring is
//
//	NewLoop(...).WithBrokerAccountor(
//	    NewBrokerAccountor(BrokerEgressConfig{...}),
//	)
//
// …called from cmd/schedd after FAAS_BROKER_EGRESS_MBIT is
// read from the env.
func (l *Loop) WithBrokerAccountor(b BrokerAccountor) *Loop {
	l.brokerAccountor = b
	return l
}

func (l *Loop) WithClock(now func() time.Time) *Loop {
	if now != nil {
		l.now = now
	}
	return l
}

// FlowCounter is the slice of "open TCP flow count by instance" the
// reaper uses to gate idle parking (spec §17 G7). Production injects a
// conntrack reader in PR-B; the default noopFlowCounter returns 0 for
// every instance, preserving the prior LastRequest-only behaviour.
type FlowCounter interface {
	Open(ctx context.Context, instanceID string) (int64, error)
}

// noopFlowCounter is the default FlowCounter. Used until PR-B wires a
// real reader; keeps ReapIdle's G7 rule inert.
type noopFlowCounter struct{}

func (noopFlowCounter) Open(_ context.Context, _ string) (int64, error) { return 0, nil }

// WithFlowCounter wires the conntrack-derived "open flows per
// instance" source (spec §17 G7). Tests inject a fake to drive table
// cases for the reaper's skip-when-busy rule; production wires a
// real conntrack reader once that lands. Nil/inert callers leave
// the noop default in place.
func (l *Loop) WithFlowCounter(fc FlowCounter) *Loop {
	if fc != nil {
		l.flowCounts = fc
	}
	return l
}

// WithOpsMetrics attaches the per-daemon Prometheus bundle to the
// loop. Mirrors Engine.WithOpsMetrics so schedd wires a single
// *wire.OpsMetrics to both. Nil is safe — observers no-op on a
// nil receiver (the loop reads the field defensively too).
func (l *Loop) WithOpsMetrics(ops *wire.OpsMetrics) *Loop {
	l.ops = ops
	return l
}

// WithScaleUp attaches the per-app reactive scale-up trigger
// (issue #169 / #172, pkg/sched/scaleup). Nil opts out (the
// scaleupTick arm of Run's select never fires). Production wires
// the real trigger from cmd/schedd after the Engine + Store + (PR
// #205) instancestats.Reader are available; tests inject a manual
// ticker or skip via nil. The trigger's own Interval() governs the
// cadence — same opt-out shape as WithHeartbeat / WithWatchdog.
func (l *Loop) WithScaleUp(t *scaleup.Trigger) *Loop {
	l.scaleup = t
	return l
}

// WithTargets (PR-C, issue #462) attaches the per-app
// concurrent_requests target scale-up trigger. Reads
// InstatsReader.MaxInflightForApp and compares against
// ScalingPolicy.Target.Value per app. Distinct from the RPS/CPU
// scale-up trigger (scaleup.Trigger), which is unchanged. Nil opts
// out — the targetsTick arm of Run's select never fires.
// Production wires NewTargets(store, reader, ledger, engine, ops)
// from cmd/schedd after the InstatsReader is available; tests skip
// via nil. The trigger's own Interval() governs the cadence.
func (l *Loop) WithTargets(t *targets.Trigger) *Loop {
	l.targets = t
	return l
}

// WithFloor (issue #557 / ADR-071) attaches the proactive
// min-instances floor reconciler. The trigger walks every app the
// schedd owns each tick and admits instances up to the effective
// min_instances floor (max of legacy column + ScalingPolicy jsonb).
// Nil opts out — the floorTick arm of Run's select never fires. The
// trigger's own Interval() governs the cadence (default
// api.FloorDecisionIntervalSeconds = 1s). Distinct from WithScaleUp
// and WithTargets: those are reactive (RPS / CPU / inflight signal);
// the floor trigger is proactive — it runs regardless of traffic
// because the customer's SLA is "min N resident at all times".
func (l *Loop) WithFloor(t *floor.Trigger) *Loop {
	l.floor = t
	return l
}

// WithRecentLoad attaches the per-app rolling-window RPS mirror
// (issue #171, pkg/sched/recentload). Nil opts out — the
// recentLoadTick arm of Run's select never fires AND the
// runReaper aggressive block is skipped. Production wires the
// mirror from cmd/schedd after the wire.PromScraper is available;
// tests inject a stub or skip via nil. The mirror's own cadence
// (1 s) governs the tick — same opt-out shape as WithScaleUp /
// WithHeartbeat / WithWatchdog.
func (l *Loop) WithRecentLoad(r *recentload.RecentLoad) *Loop {
	l.recentLoad = r
	return l
}

// WithReaperAggressive toggles the issue #171 aggressive-reaper
// scale-down path. Production defaults to ON; the cfg flag lets
// operators disable in-place if a regression surfaces. Mirrors the
// WithWatchdog / WithScaleUp nil-toggle shape. Reads as
// `WithReaperAggressive(true)` to enable, `WithReaperAggressive(false)`
// to keep the mirror wired (for telemetry) but skip the
// runReaper aggressive block.
func (l *Loop) WithReaperAggressive(enabled bool) *Loop {
	l.reaperAggressive = enabled
	return l
}

// WithReaperParkCap sets the per-app per-tick park cap used by the
// aggressive path (issue #171). Zero falls back to
// MaxParksPerTickPerApp (= 8) at evaluation time. The cap prevents
// one tick from blocking the reaper for `cap × ~150 ms` during a
// sudden-scale-down storm.
func (l *Loop) WithReaperParkCap(cap int) *Loop {
	l.reaperParkCap = cap
	return l
}

// WithLivenessWindow attaches the per-deployment liveness-restart
// tracker (issue #554 / ADR-078). The Engine calls
// LivenessWindow.RecordRestart synchronously on every
// DestroyForLivenessFailure; the trigger here is a no-tick seam —
// the in-memory ring is its own bookkeeping. The Loop wires the
// window so cmd/schedd can construct it once and share the same
// pointer between Engine.WithLivenessWindow and Loop.WithLivenessWindow.
// Nil opts out (Engine.WithLivenessWindow not called either;
// DestroyForLivenessFailure skips the ParkDeployment step). Production
// wires sched.NewLivenessWindow(api.DefaultLivenessWindowSeconds,
// api.DefaultLivenessMaxRestarts).
func (l *Loop) WithLivenessWindow(w *LivenessWindow) *Loop {
	l.livenessWindow = w
	return l
}

// WithMigratingWatchdog attaches the Tier A6 / ADR-067
// wedged-migration self-healer. Mirrors the nil-skip semantics
// of WithWatchdog / WithHeartbeat: a nil watchdog means the
// select arm in Run never fires. Production cmd/schedd wires
// the real MigratingWatchdog from the engine deps so the
// watchdog shares the same store / engine / clock as the rest
// of the loop. The watchdog's own interval governs the cadence
// (default api.MigratingWatchdogIntervalSeconds = 1s, overridable
// via FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS).
func (l *Loop) WithMigratingWatchdog(w *MigratingWatchdog) *Loop {
	l.migratingWatchdog = w
	return l
}

// WithDeadNodeReconciler attaches the stale-RUNNING billing-leak
// self-healer. Mirrors WithMigratingWatchdog's nil-skip semantics:
// a nil reconciler means the select arm in Run never fires. Production
// cmd/schedd wires sched.NewDeadNodeReconciler from the engine deps
// so the sweeper shares the same store / engine / clock as the rest
// of the loop. Cadence is the reconciler's own interval (default
// api.DeadNodeReconcilerIntervalSeconds = 30 s, overridable via
// FAAS_DEAD_NODE_RECONCILER_INTERVAL_SECONDS). The 30 s cadence is
// deliberately coarser than the §6.1 watchdog's 1 s tick: the
// staleness window this sweeper enforces is 120 s (api.DeadNode-
// ReconcilerStalenessSeconds), so a 1 s tick would issue ~120
// no-op queries per node death before any row is even eligible.
func (l *Loop) WithDeadNodeReconciler(r *DeadNodeReconciler) *Loop {
	l.deadNodeReconciler = r
	return l
}

// Run blocks until ctx is cancelled. It owns three event sources: the LISTEN
// subscriber, the reaper tick, and the cron tick.
func (l *Loop) Run(ctx context.Context) error {
	// F-11: SubscribeWithReconnect wraps Subscribe with exponential backoff
	// (100ms → 5s cap) and re-acquires the LISTEN connection across pg
	// restarts. The outer channel never closes on conn drop — only ctx
	// cancel can stop this loop. Prior `notif, ok := <-` would have
	// exited cleanly the instant the LISTEN conn died, leaving the daemon
	// alive (systemd Restart=on-failure doesn't catch clean exits) but
	// inert. schedd is now a long-running aware subscriber.
	notif, err := db.SubscribeWithReconnect(ctx, l.pool, []string{
		db.NotifyAppChanged,
		db.NotifyDeploymentChanged,
		db.NotifySnapshotPrime,
		db.NotifyCronRunNow, // PR-D / issue #791: multiplexed on the cron loop's existing LISTEN; zero extra pool connections.
		db.NotifyAppDelete,  // ADR-098: multiplexed on the cron loop's existing LISTEN; same zero-cost pattern as NotifyCronRunNow. Saves a 7th long-term pool subscriber (the standalone one tipped pool.MaxConns=8 over the edge and starved the async-invoke drain's BeginTx under e2e query bursts).
		// PR #1099 P2 redesign: multiplexed onto the existing
		// LISTEN. Same zero-cost pattern as NotifyCronRunNow +
		// NotifyAppDelete; one LISTEN connection, one multiplexed
		// handler arm, one extra safety ticker. No additional
		// pool subscriber.
		db.NotifyOperatorIntent,
	}, l.log)
	if err != nil {
		return err
	}
	// SubscribeWithReconnect owns its own cancel via the deferred
	// goroutine inside the wrapper; we close by ending ctx.

	// PR-D / issue #791: drain any cron_fire_now_requests rows that
	// survived a schedd bounce. Postgres re-fires the LISTEN channels
	// immediately on connection establish, but fire-now rows are a
	// TABLE source of truth — the LISTEN is just a wakeup — so we
	// sweep once on startup to bound the post-restart latency at ~1
	// round-trip rather than waiting for the 60s safety tick.
	l.drainPendingFireNowRequests(ctx)

	// PR #1099 P2 redesign: drain any operator_intents rows that
	// survived a schedd bounce. Same defense-in-depth pattern as
	// the fire-now drain above — operator actions are time-
	// sensitive (the on-call is paged) so bounding post-restart
	// latency at ~1 round-trip matters.
	l.drainPendingOperatorIntents(ctx)

	// PR-#TBD / C5: run the completeness tick once at startup so
	// the gauge surfaces a real value at t=0 instead of the
	// pre-instantiation default of 1.0 (vacuous truth). Cheap —
	// two SELECTs over partial indexes — and bounded by the
	// completeness ticker that fires every 60s afterward.
	l.runOperatorIntentCompletenessTick(ctx)

	reaperT := time.NewTicker(10 * time.Second)
	defer reaperT.Stop()
	cronT := time.NewTicker(60 * time.Second)
	defer cronT.Stop()
	// Fire-now safety ticker (PR-D / issue #791). 60s cadence
	// mirrors pkg/sched/fire_now.go::fireNowSafetyTick: when a
	// NotifyCronRunNow delivery is dropped (Postgres bounce, network
	// blip), the pending rows survive in the table and this ticker
	// re-claims them. An operator-initiated action waiting up to
	// 60s for recovery is acceptable.
	fireNowT := time.NewTicker(fireNowSafetyTick)
	defer fireNowT.Stop()
	// Operator-intent safety ticker (PR #1099 P2 redesign).
	// 30s cadence (vs fire-now's 60s) because operator recovery
	// primitives are time-sensitive: an SRE on the incident
	// bridge expects the action to take effect within a minute,
	// not two. Mirrors pkg/sched/operator_intent_subscriber.go::
	// operatorIntentSafetyTick.
	operatorIntentT := time.NewTicker(operatorIntentSafetyTick)
	defer operatorIntentT.Stop()
	// Operator-intent completeness ticker (PR-#TBD / C5). 60s
	// cadence — drives the gauge
	// operatorActionTraceCompletenessRatio and the counter
	// operatorIntentOutcomeMissingTotal that
	// /v1/admin/obs/health surfaces. Slower than the safety
	// tick because the queries are read-only and window-
	// aggregated; a 60s sweep is enough resolution for the
	// 5-minute trace_id coverage gauge. Defined in
	// operator_intent_completeness.go alongside the body.
	operatorIntentCompletenessT := time.NewTicker(operatorIntentCompletenessTick)
	defer operatorIntentCompletenessT.Stop()
	// Watchdog ticker (commit 3, spec §6.1). 1s cadence matches the
	// spec's "per-second" granularity for catching stuck rows before
	// they pin a ledger reservation for the full 30s cold-boot
	// budget. nil watchdog skips this ticker entirely so the test
	// surface stays green without a watchdog dependency.
	var watchdogT *time.Ticker
	if l.watchdog != nil {
		watchdogT = time.NewTicker(DefaultWatchdogInterval)
		defer watchdogT.Stop()
	}
	// Retention ticker (PR #74, spec §17 follow-up). Default cadence
	// is hourly (pkg/api.DefaultRetentionInterval) — the sweep itself
	// reads now-30d, so hourly granularity means a row that crossed
	// the threshold gets DELETED within the next hour. nil retention
	// skips this ticker entirely.
	//
	// First-fire is intentionally DEFERRED one minute after startup
	// (retentionFirstFireDelay). A bare time.NewTicker fires once
	// immediately, which on a fresh deploy would race the §6.1
	// watchdog's first sweep and delete any rows the backfill
	// (migration 00017) anchored to a now()-based terminal_at before
	// the watchdog has had a chance to stamp its first batch.
	var retentionT *time.Ticker
	var retentionFirst <-chan time.Time
	if l.retention != nil {
		t := time.NewTicker(api.DefaultRetentionInterval)
		defer t.Stop()
		retentionT = t
		delay := time.NewTimer(retentionFirstFireDelay)
		defer delay.Stop()
		retentionFirst = delay.C
	}
	// ADR-134 PR-B: invocations retention + deadline-breach sweep.
	// 60s cadence matches the cron sweep (cronT below); both
	// sweeps are read-only SELECTs over partial indexes and cost
	// single-digit ms each. nil = no ticker fires the case.
	var invocationsRetentionT *time.Ticker
	if l.invocationsRetention != nil {
		invocationsRetentionT = time.NewTicker(60 * time.Second)
		defer invocationsRetentionT.Stop()
	}
	// ADR-134 PR-E: trigger_records retention sweep. 5-minute
	// cadence — terminal trigger_records are produced at a much
	// lower rate than terminal invocations (every batch creates
	// one row, not one row per event), so a 5-min sweep is
	// plenty resolution. nil = opt out.
	var triggersRetentionT *time.Ticker
	if l.triggersRetention != nil {
		triggersRetentionT = time.NewTicker(5 * time.Minute)
		defer triggersRetentionT.Stop()
	}
	// Heartbeat ticker (issue #97 / ADR-025 axis 3, PR #114).
	// Per-node liveness sweep: ping each active compute_node,
	// stamp last_heartbeat_at on success or flip active=false
	// on failure. Default cadence DefaultHeartbeatInterval
	// (30s); production cmd/schedd wires NewHeartbeat with the
	// RoutedVMM, tests inject a fake or skip via nil. The ticker
	// fires immediately on construction — a freshly-started
	// schedd stamps the synthetic default-local row's heartbeat
	// without a 30s gap on cold start.
	var heartbeatT *time.Ticker
	if l.heartbeat != nil {
		interval := l.heartbeat.Interval
		if interval <= 0 {
			interval = DefaultHeartbeatInterval
		}
		heartbeatT = time.NewTicker(interval)
		defer heartbeatT.Stop()
		// Establish a fresh liveness baseline before the first interval
		// elapses. This is also important for recovery: a just-started
		// schedd must be able to notice a node that is already back without
		// waiting for the full heartbeat cadence.
		l.runHeartbeat(ctx)
	}
	// Disk-drift sweep ticker (PR scale-out readiness #3). Hourly
	// cadence (api.DefaultDiskDriftInterval = 1h) aligns with the
	// retention sweep's hourly tick so the two read-only sweeps
	// fire on the same wall-clock minute and operators see a
	// coordinated batch. The ticker fires once immediately on
	// construction — same first-fire shape as the heartbeat path
	// above, not the deferred-first-fire shape of the retention
	// ticker; the drift sweep is idempotent (read-only + per-file
	// counter) so an immediate first fire doesn't risk racy
	// backfill semantics like retention's. nil diskDrift skips the
	// ticker entirely (the diskDriftTick helper returns nil for a
	// nil ticker; the select case below never fires).
	var diskDriftT *time.Ticker
	if l.diskDrift != nil {
		diskDriftT = time.NewTicker(api.DefaultDiskDriftInterval)
		defer diskDriftT.Stop()
	}
	// Instance-stats poller ticker (issue #170 / PR-A). Per-Tick
	// sweep: enumerate live instances + active compute_nodes,
	// project the persistent node telemetry snapshot, replace the Reader
	// snapshot, and emit the wire rollup. A legacy fresh-dial fallback remains
	// available for fixtures that do not wire the stream cache. Default cadence
	// instancestats.DefaultStatsInterval (200 ms — 5 Hz). The
	// ticker is constructed BEFORE the first Tick (below) so
	// the first sample lands at t=0 instead of t=Interval — a
	// documented correction to the heartbeat loop's
	// "first sample at t=Interval" behaviour. nil poller skips
	// the ticker entirely (tests + run-without-metrics mode).
	var instStatsT *time.Ticker
	if l.instStats != nil {
		interval := l.instStats.TickInterval()
		if interval <= 0 {
			// Defensive: the poller is contract-bound to
			// return a positive interval, but a test that
			// injects a stub returning 0 must not hang on
			// time.NewTicker(0). Fall back to the package
			// default — the same one the poller would use.
			interval = 200 * time.Millisecond
		}
		instStatsT = time.NewTicker(interval)
		defer instStatsT.Stop()
	}
	// First Tick at t=0 (issue #170 / PR-A). Heartbeat uses
	// time.NewTicker's "fires immediately on construction" property
	// to land its first sample at t=0; the stats poller can't rely
	// on the same — its 200 ms cadence is much shorter and a
	// spurious first fire would burn a dial cycle on every restart.
	// Call Tick directly so the first sample lands deterministically.
	if l.instStats != nil {
		l.runInstanceStats(ctx)
	}
	// Scale-up trigger ticker (issue #169 / #172).
	// Per-app reactive scale-up: every Interval() seconds, run
	// the trigger's Tick so a hot RPS / CPU signal can pre-empt
	// the request-driven wake path. Default cadence 1s
	// (api.ScaleUpDecisionIntervalSeconds); the trigger supervises
	// its own nil-safety so a nil trigger never fires the case.
	var scaleupT *time.Ticker
	if l.scaleup != nil {
		interval := l.scaleup.Interval()
		if interval <= 0 {
			interval = api.ScaleUpDecisionIntervalSeconds * time.Second
		}
		scaleupT = time.NewTicker(interval)
		defer scaleupT.Stop()
	}
	// PR-C (issue #462): concurrent_requests target scale-up ticker.
	// Same 1 s cadence as scaleupTick. Nil opts out — no ticker, no
	// runTargets case.
	var targetsT *time.Ticker
	if l.targets != nil {
		interval := l.targets.Interval()
		if interval <= 0 {
			interval = api.ScaleUpDecisionIntervalSeconds * time.Second
		}
		targetsT = time.NewTicker(interval)
		defer targetsT.Stop()
	}
	// Floor reconciler ticker (issue #557 / ADR-071). 1 s cadence
	// mirrors scaleupTick / targetsTick; nil opts out. The trigger
	// supervises its own nil-safety (New(nil, ...) → Tick no-ops)
	// so a nil-safe wire here would also work, but a typed nil
	// ticker keeps the select arm dead as an explicit choice.
	var floorT *time.Ticker
	if l.floor != nil {
		interval := l.floor.Interval()
		if interval <= 0 {
			interval = api.FloorDecisionIntervalSeconds * time.Second
		}
		floorT = time.NewTicker(interval)
		defer floorT.Stop()
	}
	// Recent-load mirror ticker (issue #171). 1 s cadence keeps the
	// per-app RPS window current between reaper ticks (the reaper
	// itself runs at 10 s). nil mirror opts out — no ticker, no
	// runRecentLoad case, no aggressive block in runReaper.
	var recentLoadT *time.Ticker
	if l.recentLoad != nil {
		recentLoadT = time.NewTicker(time.Second)
		defer recentLoadT.Stop()
	}
	// Migrating-watchdog ticker (Tier A6 / ADR-067). 1 s cadence
	// parallel to the §6.1 watchdog — the watchdog is the only writer
	// that can move a row out of state='migrating' without a peer
	// commit, so its tick resolution is the operator's tripwire
	// latency for the new-owner vmmd dying mid-handoff. nil watchdog
	// opts out (the migratingWatchdogTick helper returns nil for a
	// nil ticker; the select case never fires).
	var migratingWatchdogT *time.Ticker
	if l.migratingWatchdog != nil {
		migratingWatchdogT = time.NewTicker(l.migratingWatchdog.interval)
		defer migratingWatchdogT.Stop()
	}
	// Dead-node reconciler ticker (stale-RUNNING billing-leak fix).
	// Default cadence api.DeadNodeReconcilerIntervalSeconds (30 s) —
	// see WithDeadNodeReconciler for why this is coarser than the
	// §6.1 watchdog's 1 s tick. nil opts out (the deadNodeTick
	// helper returns nil for a nil ticker; the select case never
	// fires).
	var deadNodeReconcilerT *time.Ticker
	if l.deadNodeReconciler != nil {
		deadNodeReconcilerT = time.NewTicker(l.deadNodeReconciler.interval)
		defer deadNodeReconcilerT.Stop()
	}
	// Jobs dispatch + stuck-job reaper tickers (Mega-1, issue
	// #1184 Workstream A). Both gated on jobsDispatched so a
	// FAAS_JOBS_DISPATCH=0 cluster never ticks. 1s matches the
	// cron tick (PR-A pattern); 5s reaper keeps it cheap under
	// load (reaper is one SELECT + per-claim UPDATE).
	var jobsDispatchT *time.Ticker
	var jobsReaperT *time.Ticker
	if l.jobsDispatched {
		jobsDispatchT = time.NewTicker(time.Second)
		defer jobsDispatchT.Stop()
		jobsReaperT = time.NewTicker(5 * time.Second)
		defer jobsReaperT.Stop()
	}
	var workflowsDispatchT *time.Ticker
	if l.workflowsDispatched {
		workflowsDispatchT = time.NewTicker(time.Second)
		defer workflowsDispatchT.Stop()
	}
	// Workflow retention follows the same hourly cadence and deferred first
	// fire as the existing instance retention sweep. It is separately gated
	// because workflow retention is only attached when the workflow runtime is
	// configured, while the legacy instance retention path is always present.
	var workflowRetentionT *time.Ticker
	var workflowRetentionFirst <-chan time.Time
	if l.workflowRetention != nil {
		t := time.NewTicker(api.DefaultRetentionInterval)
		defer t.Stop()
		workflowRetentionT = t
		delay := time.NewTimer(retentionFirstFireDelay)
		defer delay.Stop()
		workflowRetentionFirst = delay.C
	}
	// Trigger dispatch ticker (issue #757 / ADR-100, commit #14).
	// 1 s cadence matches runCronTick and the §6.1 watchdog — a
	// trigger record sitting in `pending` for >1 tick before
	// dispatch is acceptable because broker pullers fill the
	// pending bucket from a side channel (commits #9-12). The
	// wakeup channel WakeupTriggers() drops into selects the
	// next-tick latency for batches that land mid-cycle.
	triggerT := time.NewTicker(time.Second)
	defer triggerT.Stop()

	// Make sure the triggerWakeup channel exists before any
	// wakeup can race the first select iteration. WakeupTriggers
	// uses sync.Once internally too — this is the belt-and-braces
	// pre-create so a goroutine that calls WakeupTriggers from
	// the subscrib-er ring before run() reaches the select never
	// deadlocks on a nil channel.
	l.triggerWakeupOnce.Do(func() {
		l.triggerWakeup = make(chan struct{}, 1)
	})

	for {
		select {
		case <-ctx.Done():
			return nil
		case n, ok := <-notif:
			if !ok {
				// Defensive — wrapper guarantees open until ctx done.
				return nil
			}
			l.handleNotification(ctx, n)
		case <-reaperT.C:
			l.runReaper(ctx)
		case <-cronT.C:
			l.runCronTick(ctx)
		case <-watchdogTick(watchdogT):
			l.runWatchdog(ctx)
		case <-heartbeatTick(heartbeatT):
			l.runHeartbeat(ctx)
		case <-diskDriftTick(diskDriftT):
			l.runDiskDrift(ctx)
		case <-instStatsTick(instStatsT):
			l.runInstanceStats(ctx)
		case <-scaleupTick(scaleupT):
			l.runScaleUp(ctx)
		case <-targetsTick(targetsT):
			l.runTargets(ctx)
		case <-floorTick(floorT):
			l.runFloor(ctx)
		case <-recentLoadTick(recentLoadT):
			l.runRecentLoad(ctx)
		case <-migratingWatchdogTick(migratingWatchdogT):
			l.runMigratingReconcile(ctx)
		case <-deadNodeTick(deadNodeReconcilerT):
			l.runDeadNodeReconcile(ctx)
		case <-jobsTick(jobsDispatchT):
			l.runJobsDispatchTick(ctx)
		case <-jobsTick(jobsReaperT):
			l.runJobsReaperTick(ctx)
		case <-jobsTick(workflowsDispatchT):
			l.runWorkflowsDispatchTick(ctx)
		case <-workflowRetentionFirst:
			l.runWorkflowRetention(ctx)
			workflowRetentionFirst = nil
		case <-workflowRetentionTick(workflowRetentionT):
			l.runWorkflowRetention(ctx)
		case <-retentionFirst:
			// One-shot first fire (see retentionFirstFireDelay). After
			// this the channel is set to nil so subsequent ticks
			// exclusively come from retentionT (the 1h ticker).
			l.runRetention(ctx)
			retentionFirst = nil
		case <-retentionTick(retentionT):
			l.runRetention(ctx)
		case <-invocationsRetentionTick(invocationsRetentionT):
			l.runInvocationsRetention(ctx)
		case <-triggersRetentionTick(triggersRetentionT):
			l.runTriggersRetention(ctx)
		case <-fireNowT.C:
			// PR-D / issue #791: safety sweep. Picks up rows that
			// missed a NotifyCronRunNow delivery (Postgres bounce,
			// network blip). Same dispatcher as the notify arm —
			// drainPendingFireNowRequests is the single owner of the
			// claim + dispatch loop.
			l.drainPendingFireNowRequests(ctx)
		case <-operatorIntentT.C:
			// PR #1099 P2 redesign: safety sweep. Picks up rows
			// that missed a NotifyOperatorIntent delivery
			// (Postgres bounce, network blip). Same dispatcher as
			// the notify arm — drainPendingOperatorIntents is the
			// single owner of the claim + dispatch loop. 30s
			// cadence (vs fire-now's 60s) matches the operator-
			// action SLA.
			l.drainPendingOperatorIntents(ctx)
		case <-operatorIntentCompletenessT.C:
			// PR-#TBD / C5: 60s observability sweep. Reads
			// events + operator_intents to drive
			// operatorActionTraceCompletenessRatio (gauge) and
			// operatorIntentOutcomeMissingTotal (counter). Both
			// queries are read-only; the body is nil-safe on
			// l.ops and tolerates a missing operator_intents
			// table (42P01) for fresh clusters pre-migration.
			l.runOperatorIntentCompletenessTick(ctx)
		case <-triggerT.C:
			// Issue #757 / ADR-100: trigger dispatch tick. 1s
			// safety cadence; WakeupTriggers advances the
			// effective interval when a broker ack/nack wakes the
			// schedd mid-cycle (commits #16).
			l.runTriggerTick(ctx)
		case <-l.triggerWakeup:
			// Same arm as the 1s ticker. The wake channel is
			// buffered-size-1 so a burst of broker deliveries
			// coalesces to a single tick.
			l.runTriggerTick(ctx)
		}
	}
}

// watchdogTick is a helper that turns a nil-ticker's channel into a
// never-firing channel. It keeps the main select above free of
// per-iteration nil checks.
func watchdogTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// retentionTick is the same nil-safe pattern as watchdogTick, kept
// separate so each ticker type's name shows up in stack traces if
// a future regression corrupts the channel wiring.
func retentionTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// workflowRetentionTick is the nil-safe channel adapter for the workflow
// retention ticker. A nil ticker disables the select arm entirely.
func workflowRetentionTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// invocationsRetentionTick mirrors retentionTick for the ADR-134
// PR-B invocations sweep.
func invocationsRetentionTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// triggersRetentionTick mirrors retentionTick for the ADR-134
// PR-E trigger_records sweep.
func triggersRetentionTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// heartbeatTick is the per-node liveness ticker (PR #114). Same
// nil-safe shape as the watchdog/retention tickers: nil ticker ⇒
// nil channel, so the select case never fires.
func heartbeatTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// diskDriftTick is the read-only /srv/fc/snap drift sweep ticker
// (PR scale-out readiness #3). Same nil-safe shape as
// heartbeatTick / retentionTick: nil ticker ⇒ nil channel, so the
// select case in Run never fires. Kept separate from
// heartbeatTick so each ticker type's name shows up in stack traces
// if a future regression corrupts the channel wiring.
func diskDriftTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// instStatsTick is the per-instance metrics ticker (issue #170 /
// PR-A). Same nil-safe shape as the heartbeat ticker: nil ticker
// ⇒ nil channel, so the select case never fires. Kept separate
// from heartbeatTick so each ticker type's name shows up in stack
// traces if a future regression corrupts the channel wiring.
func instStatsTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// scaleupTick is the reactive scale-up trigger ticker (issue #169 /
// #172). Same nil-safe shape as the heartbeat/retention tickers.
func scaleupTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// targetsTick (PR-C, issue #462) is the concurrent_requests target
// scale-up trigger ticker. Same nil-safe shape as scaleupTick.
func targetsTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// floorTick (issue #557 / ADR-071) is the proactive min-instances
// floor reconciler ticker. Same nil-safe shape as scaleupTick.
func floorTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// httpUnixBasePrefix is the synthesized URL prefix for
// gatewayd-internal when dialed over a unix socket. The dialer
// writes bytes directly to the socket, so the request line carries
// "http://<path>" — this constant is shared by DialGatewaySynth,
// DialGatewaySynthTarget, and HTTPClientForGatewaySynthTarget so
// goconst doesn't flag the string literal three times across the
// three sibling functions.
const httpUnixBasePrefix = "http://unix"

// httpScheme + httpsScheme are the URL schemes for the gatewayd-internal
// synth dial when the dial is TCP or DNS. httpScheme is the TLS-off
// fallback; httpsScheme is used when tlsCfg is non-nil. Two
// sibling functions (DialGatewaySynthTarget + HTTPClientForGatewaySynthTarget)
// each reference both — collapse into constants so goconst doesn't flag
// the literal three times across the package.
const (
	httpScheme  = "http"
	httpsScheme = "https"
)

// MaxParksPerTickPerApp bounds the aggressive-reaper (issue #171)
// per-app per-tick park count. Without a cap, a single tick could
// block the reaper for `N × ~150 ms` ≈ 7.5 s on a 50-instance
// burst that just dropped to zero RPS. 8 keeps the per-tick impact
// at ~1.2 s — within schedd's 1 Hz watchdog tick budget and well
// under the reaper's 10 s tick.
const MaxParksPerTickPerApp = 8

// recentLoadTick is the aggressive-reaper signal-mirror ticker
// (issue #171). Same nil-safe shape as scaleupTick — nil ticker ⇒
// nil channel, so the select case never fires. Production wires
// this at 1 s so the rolling window stays current between reaper
// ticks (the reaper itself runs at 10 s).
func recentLoadTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// migratingWatchdogTick is the Tier A6 / ADR-067 wedged-migration
// self-healer ticker. Same nil-safe shape as recentLoadTick —
// nil ticker nil channel, so the select case in Run never
// fires. Kept separate from recentLoadTick so each ticker type's
// name shows up in stack traces if a future regression corrupts
// the channel wiring.
func migratingWatchdogTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// deadNodeTick is the stale-RUNNING billing-leak reconciler ticker.
// Same nil-safe shape as migratingWatchdogTick — nil ticker nil
// channel, so the select case in Run never fires. Kept separate so a
// regression that corrupts channel wiring shows up against a
// recognisable name in the stack trace instead of a generic
// "ticker returned nil" panic.
func deadNodeTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// jobsTick gates the Mega-1 dispatch + reaper tickers on a nil
// pointer (jobsDispatched=false), so the select arms never fire
// when FAAS_JOBS_DISPATCH=0. Mirrors deadNodeTick so the wired-
// but-off invariant is indistinguishable from off-by-default.
func jobsTick(t *time.Ticker) <-chan time.Time {
	if t == nil {
		return nil
	}
	return t.C
}

// runHeartbeat dispatches one sweep of the per-node liveness
// ticker. Exported as a method so tests can drive a single tick
// without spinning up Run's goroutine. Tick errors are logged
// inside Heartbeat.Tick — Run never returns them so a transient
// DB blip can't tear down the loop.
func (l *Loop) runHeartbeat(ctx context.Context) {
	if err := l.heartbeat.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
		l.log.Warn("heartbeat tick error", "err", err)
	}
	// Recovery must be driven from here, not only from Heartbeat.Run:
	// this loop is what production actually runs, and Run is reserved
	// for standalone/test drivers. Wiring TickRecover only into Run
	// left node recovery as dead code on the real path — verified
	// against a live fleet, where a frozen-then-resumed vmmd stayed
	// inactive for six minutes with the fix supposedly deployed.
	l.heartbeat.TickRecover(ctx)
}

// runDiskDrift dispatches one sweep of the read-only /srv/fc/snap
// vs DB drift sweep (PR scale-out readiness #3). Exported as a
// method so tests drive a single tick without spinning up Run's
// goroutine. Tick errors are logged + swallowed (the sweep is
// diagnostic — Tick never returns an error today, but if a future
// contributor adds one we still want the loop to keep ticking).
// Drift counts are logged inside DiskDrift.Tick; this dispatch
// helper intentionally has no return path for them.
//
// The ctx passed in is wrapped with the per-tick timeout
// (sched.DefaultDiskDriftTickTimeout = 5s) so a slow /srv/fc/snap
// ReadDir cannot freeze the loop's 1 Hz tick budget. The
// direct-call test surface (TestLoopRunDiskDriftDispatchesTick)
// passes a context that already has its own deadline — the
// WithTimeout wrap is composable.
func (l *Loop) runDiskDrift(ctx context.Context) {
	if l.diskDrift == nil {
		// Direct-call guard for tests; the select case is already
		// gated by the nil-ticker pattern in diskDriftTick (a nil
		// ticker returns a nil channel, so the case never fires).
		return
	}
	tickCtx, cancel := context.WithTimeout(ctx, l.diskDrift.timeout)
	defer cancel()
	if _, err := l.diskDrift.Tick(tickCtx); err != nil && !errors.Is(err, context.Canceled) {
		l.log.Warn("disk-drift tick error", "err", err)
	}
}

// runInstanceStats dispatches one sweep of the per-instance
// metrics poller (issue #170 / PR-A). Same shape as runHeartbeat
// — exported as a method so tests drive a single tick without
// spinning up Run's goroutine. Tick errors are logged + swallowed
// (a partial sweep is still useful; the next tick has a fresh
// chance). The nil guard mirrors runHeartbeat's defensiveness
// even though Run's ticker construction gates instStatsT to nil
// when the poller is absent — keeping the helper panic-safe
// means tests can call it directly on a bare Loop without
// tripping an unhelpful nil pointer dereference.
func (l *Loop) runInstanceStats(ctx context.Context) {
	if l.instStats == nil {
		return
	}
	if err := l.instStats.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
		l.log.Warn("instance stats tick error", "err", err)
	}
}

// runWatchdog dispatches one sweep of the §6.1 watchdog. Exported as a
// method so tests can drive a single tick without spinning up Run's
// goroutine.
func (l *Loop) runWatchdog(ctx context.Context) {
	l.watchdog.sweepRuns(ctx)
}

// runRetention dispatches one sweep of the §17 retention sweep. Same
// shape as runWatchdog — exported as a method so tests drive a single
// tick without spinning up Run. Errors from SweepOnce are logged +
// swallowed (the sweep itself is idempotent + redelivery-safe; an
// error means a transient store outage, not a permanent fault).
func (l *Loop) runRetention(ctx context.Context) {
	deleted, err := l.retention.SweepOnce(ctx)
	if err != nil {
		l.log.Warn("retention: sweep failed", "err", err)
		return
	}
	if deleted > 0 {
		l.log.Info("retention: swept", "deleted", deleted)
	}
}

// runInvocationsRetention dispatches one tick of the ADR-134 PR-B
// invocations sweep (retention + deadline breach). Mirrors
// runRetention's shape: tick errors are logged + swallowed (the
// sweep is idempotent and a transient DB blip retries next tick).
func (l *Loop) runInvocationsRetention(ctx context.Context) {
	if l.invocationsRetention == nil {
		return
	}
	retentionDeleted, deadlineForced, err := l.invocationsRetention.SweepOnce(ctx)
	if err != nil {
		l.log.Warn("invocations retention: sweep failed", "err", err)
		return
	}
	if retentionDeleted > 0 || deadlineForced > 0 {
		l.log.Info("invocations retention: swept",
			"deleted", retentionDeleted,
			"deadline_forced", deadlineForced)
	}
}

// runTriggersRetention dispatches one tick of the ADR-134 PR-E
// trigger_records sweep. Same shape as runInvocationsRetention.
func (l *Loop) runTriggersRetention(ctx context.Context) {
	if l.triggersRetention == nil {
		return
	}
	deleted, err := l.triggersRetention.SweepOnce(ctx, 1000)
	if err != nil {
		l.log.Warn("trigger_records retention: sweep failed", "err", err)
		return
	}
	if deleted > 0 {
		l.log.Info("trigger_records retention: swept", "deleted", deleted)
	}
}

// runMigratingReconcile dispatches one sweep of the Tier A6 /
// ADR-067 migrating-instance watchdog. Same shape as
// runRetention — exported as a method so tests drive a single
// tick without spinning up Run. Tick errors are logged +
// swallowed (the inner Engine.ReconcileExpiredMigrations is
// per-row recoverable; a transient DB blip on the input
// ListExpiredMigrations query is the only error that surfaces
// here, and the next tick retries).
//
// context.Canceled is treated as a normal shutdown signal
// (matches migrating_watchdog.go's own ctx-cancel handling);
// we don't surface it as a Warn, since the schedd is about to
// exit and operators don't need a flurry of "tick failed" logs
// at shutdown.
func (l *Loop) runMigratingReconcile(ctx context.Context) {
	if l.migratingWatchdog == nil {
		// Direct-call guard for tests; the select case is already
		// gated by the nil-ticker pattern in migratingWatchdogTick
		// (a nil ticker returns a nil channel, so the case never
		// fires).
		return
	}
	reconciled, err := l.migratingWatchdog.handle(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			l.log.Warn("migrating watchdog: tick failed", "err", err)
		}
		return
	}
	if reconciled > 0 {
		l.log.Debug("migrating watchdog: tick reconciled", "reconciled", reconciled)
	}
}

// runDeadNodeReconcile dispatches one sweep of the stale-RUNNING
// billing-leak reconciler. Exported as a method so tests drive a
// single tick without spinning up Run. Per-batch errors are logged
// + swallowed: a transient DB blip on the ListRunningInstancesOnDeadNodes
// query is the only error that surfaces here, and the next tick
// retries. Per-row outcomes (failed / conflict / error) are reported
// by the metric inside Engine.ReconcileDeadNodeInstances — this
// dispatch helper intentionally has no return path for them.
//
// context.Canceled is treated as a normal shutdown signal (matches
// runMigratingReconcile); we don't surface it as a Warn since the
// schedd is about to exit and operators don't need a flurry of
// "tick failed" logs at shutdown.
func (l *Loop) runDeadNodeReconcile(ctx context.Context) {
	if l.deadNodeReconciler == nil {
		// Direct-call guard for tests; the select case is already
		// gated by the nil-ticker pattern in deadNodeTick (a nil
		// ticker returns a nil channel, so the case never fires).
		return
	}
	reconciled, err := l.deadNodeReconciler.handle(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			l.log.Warn("dead-node reconciler: tick failed", "err", err)
		}
		return
	}
	// No per-tick log on reconciled>0 here — the reconciler itself
	// logs at Warn when any orphaned instances were terminated, and
	// the metric carries the per-row outcome. A second Debug log at
	// this layer would be noise.
	_ = reconciled
}

// runScaleUp dispatches one tick of the per-app reactive scale-up
// trigger (issue #169 / #172). The tick runs asynchronously because
// its optional Prometheus scrape can otherwise hold the scheduler loop
// for the HTTP client's timeout. At most one tick is in flight; a slow
// scrape therefore causes the next cadence to be skipped rather than
// creating concurrent ring-buffer writers or piling up goroutines.
func (l *Loop) runScaleUp(ctx context.Context) {
	if l == nil || l.scaleup == nil {
		return
	}
	l.scaleupMu.Lock()
	if l.scaleupRunning {
		l.scaleupMu.Unlock()
		return
	}
	l.scaleupRunning = true
	trigger := l.scaleup
	l.scaleupMu.Unlock()
	go func() {
		defer func() {
			l.scaleupMu.Lock()
			l.scaleupRunning = false
			l.scaleupMu.Unlock()
		}()
		if err := trigger.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) && l.log != nil {
			l.log.Warn("scaleup tick error", "err", err)
		}
	}()
}

// runTargets (PR-C, issue #462) dispatches one tick of the
// concurrent_requests target scale-up trigger. Mirror of runScaleUp
// — Tick errors are logged, never returned, so a transient store
// blip can't tear down the loop. Nil-safe via the
// InstatsReader.MaxInflightForApp contract inside Tick itself.
func (l *Loop) runTargets(ctx context.Context) {
	if err := l.targets.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
		l.log.Warn("targets tick error", "err", err)
	}
}

// runFloor (issue #557 / ADR-071) dispatches one tick of the
// proactive min-instances floor reconciler. Same shape as
// runTargets — Tick errors are logged, never returned, so a
// transient store outage can't tear down the loop. The trigger
// itself is nil-safe (Tick on a nil *Trigger is a no-op); the
// nil-check here is defence-in-depth for the typed-nil ticker arm.
func (l *Loop) runFloor(ctx context.Context) {
	if l.floor == nil {
		return
	}
	if err := l.floor.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
		l.log.Warn("floor tick error", "err", err)
	}
}

// runRecentLoad dispatches one tick of the aggressive-reaper signal
// mirror (issue #171). Same shape as runScaleUp — Touch errors are
// swallowed (the mirror keeps its previous ring; the reaper sees
// stale-but-non-zero, the safer direction). Exported as a method
// so tests drive a single tick without spinning up Run.
//
// Note: we use `time.Now()` here (not `l.now()`) because the
// 1 s mirror cadence is wall-clock driven — the reaper's decision
// tick is the only place that hooks the fake clock for test
// determinism (runReaper → l.now() at loop.go:593). The mirror's
// own behaviour under a frozen clock is exercised by the
// per-tick integration tests (TestLoopReaperAggressive*) which
// drive Touch directly with the frozen value.
func (l *Loop) runRecentLoad(ctx context.Context) {
	if l.recentLoad == nil {
		return
	}
	l.recentLoad.Touch(ctx, time.Now())
}

// maxConcurrentPrimes bounds dispatchPrime's off-loop workers. Prime
// takes a per-app lock (Engine.Prime → lockApp), so concurrent primes
// for one app serialize anyway; this bounds the fan-out across apps and
// keeps goroutine growth from a notify burst bounded.
const maxConcurrentPrimes = 4

// dispatchPrime runs Engine.Prime off the loop's select goroutine.
//
// Prime does real VM work — cold boot then snapshot — so it can occupy
// the goroutine for tens of seconds. That goroutine is shared: Loop.run
// selects over the pg_notify channel AND the reaper, cron and watchdog
// tickers. A slow Prime therefore stops the reaper, cron and watchdog
// too, and a Prime that never returns stops them forever.
//
// That is not hypothetical. On 2026-09-03 a PauseAndSnapshot with no
// deadline blocked in grpc waitOnHeader for 10+ minutes; schedd stayed
// `active`, answered /metrics in 30ms, and did no work at all. The
// SIGQUIT dump showed one goroutine in
// handleNotification → Prime → snapshotAndPark → PauseAndSnapshot and
// ZERO goroutines blocked on a mutex — the stall was head-of-line
// blocking on this select, not lock contention.
//
// SnapshotTimeout now bounds that RPC, so the block is finite; running
// off-loop additionally keeps a merely-slow prime from delaying the
// reaper and watchdog behind it.
//
// When every slot is busy the call runs INLINE rather than being
// dropped. A dropped snapshot_prime strands the deployment in
// `snapshotting` with nothing to retry it (the notification is
// consumed and gone), which is exactly the state this bug left
// deployments in. Blocking the loop is the lesser harm, and it is
// bounded by SnapshotTimeout + the cold-boot budget.
func (l *Loop) dispatchPrime(ctx context.Context, appID, deploymentID string) {
	l.primeSlotsOnce.Do(func() {
		l.primeSlots = make(chan struct{}, maxConcurrentPrimes)
	})
	run := func() {
		if err := l.engine.Prime(ctx, appID, deploymentID); err != nil {
			l.log.Warn("sched: prime failed", "app", appID, "deployment", deploymentID, "err", err)
			l.engine.markPrimeFailed(ctx, deploymentID, err)
		}
	}
	select {
	case l.primeSlots <- struct{}{}:
		go func() {
			defer func() { <-l.primeSlots }()
			run()
		}()
	default:
		l.log.Warn("sched: prime slots saturated; running inline",
			"app", appID, "deployment", deploymentID, "slots", maxConcurrentPrimes)
		run()
	}
}

// waitPrimes blocks until every prime dispatched by dispatchPrime has
// returned. It acquires all slots (so no worker can hold one) and then
// releases them.
//
// Tests need this because dispatchPrime moved Prime off the caller's
// goroutine: a test that calls handleNotification and asserts on the
// resulting rows would otherwise race the worker. Production has no
// caller — the loop is never "done" with primes.
func (l *Loop) waitPrimes() {
	l.primeSlotsOnce.Do(func() {
		l.primeSlots = make(chan struct{}, maxConcurrentPrimes)
	})
	for i := 0; i < maxConcurrentPrimes; i++ {
		l.primeSlots <- struct{}{}
	}
	for i := 0; i < maxConcurrentPrimes; i++ {
		<-l.primeSlots
	}
}

// handleNotification decodes the JSON payload and applies the policy.
//
//   - app_changed: `kind=parked` is actionable and tears down the app's live
//     instances; lifecycle changes reconcile service replicas. Other app
//     changes are informational. Wake materialises request-mode instances on
//     demand (first request), so no eager instance creation is needed there.
//   - deployment_changed: a deployment becoming live reconciles its service
//     replica target; other transitions remain informational.
//   - snapshot_prime: imaged finished building a deployment's layer; boot it
//     once, snapshot it, and park it (spec §5 step 6, ADR-018).
func (l *Loop) handleNotification(ctx context.Context, n db.Notification) {
	switch n.Channel {
	case db.NotifyAppChanged:
		var p struct {
			Kind             string `json:"kind"`
			AppID            string `json:"app_id"`
			LifecycleChanged bool   `json:"lifecycle_changed"`
		}
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			l.log.Warn("sched: bad app_changed payload", "err", err)
			return
		}
		if p.Kind == "parked" {
			if p.AppID == "" {
				l.log.Warn("sched: parked app notification missing app_id")
				return
			}
			if acted, err := l.engine.ParkApp(ctx, p.AppID); err != nil {
				l.log.Warn("sched: park app failed", "app", p.AppID, "acted", acted, "err", err)
			} else {
				l.log.Info("sched: parked app reconciled", "app", p.AppID, "instances", acted)
			}
			return
		}
		if p.LifecycleChanged && p.AppID != "" {
			go l.engine.ReconcileServiceApp(context.WithoutCancel(ctx), p.AppID)
		}
		l.log.Debug("app_changed", "payload", n.Payload)
	case db.NotifyDeploymentChanged:
		var p struct {
			DeploymentID string `json:"deployment_id"`
			To           string `json:"to"`
			Status       string `json:"status"`
		}
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			l.log.Warn("sched: bad deployment_changed payload", "err", err)
			return
		}
		if p.Status == string(state.DeployLive) {
			deploymentID := p.DeploymentID
			if deploymentID == "" {
				// Older imaged versions carried the deployment id in `to`.
				deploymentID = p.To
			}
			if deploymentID != "" {
				go l.engine.ReconcileServiceDeployment(context.WithoutCancel(ctx), deploymentID)
			}
		}
		l.log.Debug("deployment_changed", "payload", n.Payload)
	case db.NotifySnapshotPrime:
		var p struct {
			AppID        string `json:"app_id"`
			DeploymentID string `json:"deployment_id"`
		}
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			l.log.Warn("sched: bad snapshot_prime payload", "err", err)
			return
		}
		if p.AppID == "" || p.DeploymentID == "" {
			l.log.Warn("sched: snapshot_prime missing ids", "payload", n.Payload)
			return
		}
		l.dispatchPrime(ctx, p.AppID, p.DeploymentID)
	case db.NotifyCronRunNow:
		// PR-D / issue #791: fire-now wake. The notify payload is
		// informational (the row in cron_fire_now_requests is the
		// source of truth) — drain handles claim + dispatch
		// regardless of which request_id the notify carried. This
		// matches the build_queued notify-loss defense pattern
		// (cmd/imaged consumer: subscriber re-reads the row).
		l.drainPendingFireNowRequests(ctx)
	case db.NotifyAppDelete:
		// ADR-098: app was deleted. Evict any in-flight wake for
		// the deleted app via the wake coordinator's Forget so
		// followers unwind with ErrAppDeleted instead of waiting
		// for the wake-coord TTL. Multiplexed onto this loop's
		// existing LISTEN (see the SubscribeWithReconnect call
		// above); nil appDelete is a no-op for tests / opt-out.
		if l.appDelete == nil {
			return
		}
		var p struct {
			AppID string `json:"app_id"`
		}
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			l.log.Warn("sched: bad app_delete payload", "err", err)
			return
		}
		if p.AppID == "" {
			l.log.Warn("sched: app_delete missing app_id", "payload", n.Payload)
			return
		}
		l.appDelete.evictApp(ctx, p.AppID)
	case db.NotifyOperatorIntent:
		// PR #1099 P2 redesign: operator-intent wake. The notify
		// payload is informational (the row in operator_intents
		// is the source of truth) — the drain handles claim +
		// dispatch regardless of which intent_id the notify
		// carried. Same defense-in-depth pattern as
		// NotifyCronRunNow's handler arm above.
		l.drainPendingOperatorIntents(ctx)
	}
}

// runReaper builds a read-only snapshot of every instance and applies the idle /
// aggressive / RAM-pressure selectors, delegating each action to the Engine:
//   - ReapIdle → Engine.Park (snapshot + park; snapshot reused on next wake).
//   - ReapAggressive (issue #171) → Engine.Park (snapshot + park) for the
//     surplus above max(min_instances, desired + 1), where desired is
//     derived from the per-app rolling-window RPS signal.
//   - SelectEvictions → Engine.Evict (destroy; next wake cold-boots, ADR-005).
func (l *Loop) runReaper(ctx context.Context) {
	store := l.engine.Store()
	// pg_notify is a wakeup hint, not a durable queue. Reconcile deletion
	// candidates directly from app/account status before the normal app list.
	// The regular app list excludes soft-deleted apps, and the account-deletion
	// state is intentionally outside the idle reaper's live set, so neither
	// path can recover an orphaned VM on its own.
	lifecycle, lifecycleErr := store.ListInstancesForLifecycleReconciliation(ctx, l.engine.OwnerNodeID(), lifecycleReconcileMaxPerTick)
	if lifecycleErr != nil {
		l.log.Warn("reaper: list lifecycle reconciliation candidates", "err", lifecycleErr)
	} else {
		for _, candidate := range lifecycle {
			acted, err := l.engine.ReconcileLifecycleInstance(ctx, candidate.ID)
			if err != nil {
				l.log.Warn("reaper: lifecycle reconciliation", "instance", candidate.ID, "acted", acted, "err", err)
			}
		}
	}
	// Phase 2 / Gate A: scope the reaper to this schedd's owner
	// apps/instances. Empty owner = legacy single-box posture
	// (list everything). Non-empty = per-node slice via the
	// apps_node_id_idx / instances_app_id_idx joins added in
	// migration 00090.
	var apps []state.App
	var err error
	if owner := l.engine.OwnerNodeID(); owner != "" {
		apps, err = store.ListAppsByNodeID(ctx, owner)
	} else {
		apps, err = store.ListAllApps(ctx)
	}
	if err != nil {
		l.log.Warn("reaper: list apps", "err", err)
		return
	}
	// pg_notify is a wakeup hint, not a durable queue. Reconcile parked apps
	// from the table source of truth on every reaper tick so a schedd restart,
	// LISTEN reconnect, or transient notification loss cannot leave a VM live
	// behind an evicted_cold app. This runs before the normal idle snapshot so
	// successfully parked rows are excluded from the same tick's accounting.
	for _, app := range apps {
		if app.Status != state.AppEvictedCold {
			continue
		}
		if acted, err := l.engine.ParkApp(ctx, app.ID); err != nil {
			l.log.Warn("reaper: parked app reconcile", "app", app.ID, "acted", acted, "err", err)
		}
	}
	// G7 conntrack warm (spec §17): if the FlowCounter is also a Warm-able
	// reader (the production flowcount.Reader is), feed it every live
	// instance up front so Open calls below are cheap map lookups. The
	// type assertion keeps the FlowCounter interface narrow — test mocks
	// that don't implement Warm are simply skipped, preserving the
	// existing test surface. Either failure falls through to
	// LastRequest-only reaping per the fail-open contract pinned by
	// TestRunReaperFlowCounterErrorFailsOpen.
	if warmer, ok := l.flowCounts.(interface {
		Warm(context.Context, []state.Instance) error
	}); ok {
		var all []state.Instance
		var err error
		if owner := l.engine.OwnerNodeID(); owner != "" {
			all, err = store.ListInstancesByNodeID(ctx, owner)
		} else {
			all, err = store.ListAllInstances(ctx)
		}
		if err != nil {
			l.log.Warn("reaper: list all instances for warm", "err", err)
		} else if warmErr := warmer.Warm(ctx, all); warmErr != nil {
			l.log.Warn("reaper: warm flow reader", "err", warmErr)
		}
	}
	now := l.now()
	// snapshot is a point-in-time view of every instance on the
	// box. The three selectors below (ReapIdle, ReapAggressive,
	// SelectEvictions) all read from this same slice. IMPORTANT:
	// the snapshot does NOT reflect state changes made by the
	// Engine.Park / Engine.Evict calls below — those selectors
	// are stateless over a []InstanceInfo. For scale-down flows
	// this is benign (each successful Park reduces the resident
	// set and the next selector sees a smaller pool via the store,
	// not the snapshot). For test determinism this is also why
	// `now := l.now()` (vs `time.Now()`) was lifted here: the
	// integration tests pin all selectors to the same instant.
	var snapshot []InstanceInfo
	// appDeploymentFloor (issue #557 closure / ADR-072): per-app
	// max deployment floor across the app's instances. The reaper
	// stamps InstanceInfo.MinInstances with this max so it agrees
	// with pkg/meter/sampler.go:470-485 (the biller). Without this
	// mirror, an app with app.min_instances=0 and
	// deployment.min_instances=3 is billed for 3 warm instances
	// but reaped to 0 — a paid warm/park flap on every tick.
	// Reading the instance.DeploymentID carrier means we don't
	// need a separate ListDeploymentsByApp query; the snapshot
	// walk already pulls every instance. A deployment lookup
	// that errors (e.g. stale instance row pointing at a deleted
	// deployment) is treated as floor=0 — the biller does the
	// same and the customer sees a transient bill drop, never
	// a false floor that keeps garbage resident.
	appDeploymentFloor := map[string]int{}
	for _, a := range apps {
		floor := a.EffectiveMinInstances()
		// Floor pushed by per-deployment overrides. We don't have
		// the instance list yet — stash a placeholder (app floor)
		// and re-walk after the snapshot is built so we can read
		// each instance's DeploymentID carrier.
		appDeploymentFloor[a.ID] = floor
	}
	for _, a := range apps {
		plan := api.Plan("")
		if acct, err := store.AccountByID(ctx, a.AccountID); err == nil {
			plan = acct.Plan
		}
		instances, err := store.ListInstancesForApp(ctx, a.ID)
		if err != nil {
			continue
		}
		for _, ins := range instances {
			// ListInstancesForApp is also used by dashboard and audit
			// surfaces, so it intentionally returns terminal rows. The
			// reaper must apply the same live-state filter as
			// ListAllInstances before warming conntrack or asking for a
			// flow count; terminal rows have no veth and can otherwise
			// flood the log and monopolise this synchronous loop, starving
			// snapshot_prime notifications.
			if !reaperInstanceState(state.State(ins.State)) {
				continue
			}
			// G7 flow count (spec §17): the conntrack reader is the
			// production source; nil/error falls back to 0 so a flow-source
			// glitch fails open (LastRequest-only path; safe default).
			var open int64
			if l.flowCounts != nil {
				if v, err := l.flowCounts.Open(ctx, ins.ID); err == nil {
					open = v
				} else {
					l.log.Warn("reaper: flow count", "instance", ins.ID, "err", err)
				}
			}
			snapshot = append(snapshot, InstanceInfo{
				Instance: ins.ID,
				AppID:    ins.AppID,
				Plan:     plan,
				State:    state.State(ins.State),
				// ADR-072: carrier for the post-snapshot
				// app-wide max floor enrichment. Empty on legacy
				// rows that pre-date the per-deployment column.
				DeploymentID: ins.DeploymentID,
				RAMMB:        ins.RAMMB,
				LastRequest:  ins.LastRequestAt,
				Started:      ins.StartedAt,
				IdleTimeoutS: a.IdleTimeoutS,
				NodeID:       ins.NodeID,
				// ux_spec §6.5: per-app floor the reaper honors
				// when parking idle instances. Plan-tier-gated
				// upstream (apid updateApp handler), so the
				// value is always >= 0 here. ADR-071: read via
				// EffectiveMinInstances so the reaper agrees
				// with the engine gate and the meterd sampler
				// (closes the column/jsonb revenue gap).
				// ADR-072: the carrier is the app-wide max
				// (`max(app.EffectiveMinInstances(),
				// max(dep.EffectiveMinInstances() across the
				// app's instances)`) so the reaper agrees with
				// pkg/meter/sampler.go:470-485 (the biller).
				// Without this mirror, a customer with
				// app.min_instances=0 + deployment.min_instances=3
				// is billed for 3 warm instances but reaped to 0
				// — a paid warm/park flap on every tick.
				MinInstances: appDeploymentFloor[a.ID],
				OpenConns:    open,
				// Issue #667 / ADR-078: in-flight waitUntil task count.
				// Sourced from instances.tail_count (PR #671 schema);
				// the reaper gate keeps RUNNING instances alive while
				// the runner's tail host drains them.
				TailCount: ins.TailCount,
				// ADR-051 PR-D: workload class drives the
				// reaper-exempt carve-out. Workers skip
				// ReapIdle + ReapAggressive; RAM pressure
				// (SelectEvictions) still wins.
				WorkloadClass: a.WorkloadClass,
				// PR-C (issue #462): per-app scale-in cooldown
				// carrier fields. Same value across all rows of
				// one app — sourced from apps.last_scale_in_at
				// + ScalingPolicy.ScaleInCooldownS.
				LastScaleInAt:    a.LastScaleInAt,
				ScaleInCooldownS: state.ScalingPolicyOrDefault(a.ScalingPolicy).ScaleInCooldownS,
				// Issue #475: per-app eviction tier (best_effort
				// | reserved). Same carrier semantics as
				// MinInstances — identical across all instances of
				// one app, sourced from apps.eviction_priority, and
				// consulted by SelectEvictions' tier comparator.
				// Default 'best_effort' preserves pre-#475 LRU
				// behaviour bit-for-bit (the empty-string fallback
				// in the sort comparator handles pre-PR rows).
				EvictionPriority: a.EvictionPriority,
				// Issue #72 / ADR-125: instance mode is the
				// reaper-exempt predicate for mirror VMs. The
				// reaper's ReapIdle consults Mode and skips
				// mode='mirror' rows; the sampler mirrors the
				// same skip on the biller side. Sourced from
				// state.Instance.Mode; pre-feature rows carry
				// 'normal' so this is a no-op for every existing
				// customer until a mirror goroutine wakes a
				// mode='mirror' VM via Engine.AdmitInstance.
				Mode: ins.Mode,
			})
		}
	}
	// ADR-072 floor-mirror enrichment (post-snapshot): walk each
	// instance's DeploymentID carrier, look up the deployment's
	// floor, and push appDeploymentFloor to max(app, deployment)
	// for the app-wide max. Then re-stamp every snapshot row's
	// MinInstances with the (now-final) app-wide max so
	// ReapIdle / ReapAggressive consult the same number the biller
	// charges for. A deployment lookup that errors (stale
	// instance row pointing at a deleted deployment) is treated as
	// floor=0 — the biller does the same at sampler.go:478-480.
	//
	// depCache (code review #725 finding F4): cache distinct
	// deployments per tick. An app with N instances all on
	// deployment D1 produced N store.DeploymentByID(ctx, D1)
	// round-trips pre-F4 — on PgStore that's N queries fetching
	// the same row. The cache collapses it to 1 query per
	// distinct deployment per tick. Scope is per-tick; the
	// loop is rebuilt every reaper tick so cache lifetime
	// never spans a stale deployment row.
	depCache := map[string]int{}
	for _, ins := range snapshot {
		if ins.DeploymentID == "" {
			continue
		}
		dFloor, cached := depCache[ins.DeploymentID]
		if !cached {
			dep, derr := store.DeploymentByID(ctx, ins.DeploymentID)
			if derr != nil {
				// Treat as floor=0 (matches the biller at
				// sampler.go:478-480) and remember the
				// miss so a later snapshot row on the same
				// deployment doesn't re-query.
				depCache[ins.DeploymentID] = 0
				continue
			}
			dFloor = dep.EffectiveMinInstances()
			depCache[ins.DeploymentID] = dFloor
		}
		if dFloor > appDeploymentFloor[ins.AppID] {
			appDeploymentFloor[ins.AppID] = dFloor
		}
	}
	for i := range snapshot {
		if snapshot[i].MinInstances < appDeploymentFloor[snapshot[i].AppID] {
			snapshot[i].MinInstances = appDeploymentFloor[snapshot[i].AppID]
		}
	}
	resident := l.engine.Ledger().ResidentRAM()
	// instanceToApp (PR-C review fix): O(N) instance→app map shared
	// between the idle and aggressive reaper branches. The pre-PR-C
	// idle branch was O(N×M) (each parked ID linear-scanned the
	// snapshot) and the aggressive branch built the same map
	// locally — hoisting here is O(N+M) for the whole reaper.
	instanceToApp := make(map[string]string, len(snapshot))
	for _, s := range snapshot {
		instanceToApp[s.Instance] = s.AppID
	}
	// PR-C (issue #462): per-app stamp after a successful park. We
	// group the idle park list by app so we stamp ONCE per app per
	// tick (the column is a per-app stamp; one park from each app is
	// enough to drive the cooldown consult on the next tick). The
	// "stamp missed" direction is safe — the consult bypasses
	// cooldown on a NIL stamp.
	// cooldownHeldByApp (P1D): per-tick shared set for the
	// cooldown_held metric emission. ReapIdle (run first) populates
	// this with apps it skipped due to cooldown; ReapAggressive
	// (run second) consults the set before its own emission so the
	// same app in the same tick is counted once. See reaper.go for
	// the load-bearing contract.
	idleParkByApp := map[string]struct{}{}
	cooldownHeldByApp := map[string]struct{}{}
	for _, id := range ReapIdle(now, snapshot, l.ops, cooldownHeldByApp) {
		if err := l.reaperShutdown(ctx, snapshot, id); err != nil {
			l.log.Warn("reaper: idle park", "instance", id, "err", err)
			continue
		}
		// Issue #475: per-tier eviction counter. The idle path is
		// the per-app floor's friend — both 'best_effort' and
		// 'reserved' instances get parked after their idle
		// timeout, so the metric observation is keyed by the
		// InstanceInfo's EvictionPriority (the empty-string
		// fallback here would never produce a real label value —
		// ResolvePriority falls back to 'best_effort' to match the
		// pre-#475 default).
		if tier, ok := resolvePriority(snapshot, id); ok {
			if counter := l.ops.EvictedPriority(tier, "idle"); counter != nil {
				counter.Inc()
			}
		}
		// O(1) lookup via the hoisted instance→app map.
		if appID, ok := instanceToApp[id]; ok {
			idleParkByApp[appID] = struct{}{}
		}
	}
	for appID := range idleParkByApp {
		if err := l.engine.Store().StampAppScaleIn(ctx, appID); err != nil {
			l.log.Warn("reaper: stamp scale-in", "app", appID, "err", err)
		}
		// Issue #557 closure / ADR-072: emit a
		// `instances.parked_min_instances_released` audit row when
		// the app-wide max floor (post-enrichment) has dropped
		// below the previous tick's floor AND this tick actually
		// parked ≥1 instance for the app. The pre-#557 audit
		// emit lived in runReaperAggressive's
		// `floorByApp > postPark` branch, which is structurally
		// unsatisfiable (ReapAggressive's `limit` arithmetic
		// never parks below the floor it was given). The idle
		// path is the only branch that genuinely drops below
		// the floor — keyed on lastFloorByApp vs the new
		// app-wide max floor. Pre-#557 this case was silent;
		// operators couldn't tell whether the bill change came
		// from traffic or from a PATCH.
		if l.lastFloorByApp != nil {
			if prev, ok := l.lastFloorByApp[appID]; ok && prev > appDeploymentFloor[appID] {
				l.emitFloorReleasedAudit(ctx, appID, prev, appDeploymentFloor[appID], now)
			}
		}
	}
	// Stamp the per-app floor after the idle branch consumes
	// lastFloorByApp so the next tick's consult is fresh. Guarded
	// on the field being non-nil so tests that don't care can skip
	// the bookkeeping without nil-deref'ing.
	if l.lastFloorByApp == nil {
		l.lastFloorByApp = map[string]int{}
	}
	for _, a := range apps {
		l.lastFloorByApp[a.ID] = appDeploymentFloor[a.ID]
	}

	// Aggressive scale-down (issue #171). Park the surplus above
	// max(min_instances, desired + 1) where desired comes from the
	// per-app rolling-window RPS signal. Apps with no autoscale
	// target are absent from desiredByApp and ReapAggressive
	// skips them — ReapIdle owns their cooldown.
	//
	// Per-tick park cap (MaxParksPerTickPerApp = 8) prevents one
	// tick from blocking the reaper for `cap × ~150 ms` during a
	// sudden-scale-down storm.
	if l.recentLoad != nil && l.reaperAggressive {
		l.runReaperAggressive(ctx, apps, snapshot, instanceToApp, cooldownHeldByApp, now)
	}

	for _, id := range SelectEvictions(resident, now, snapshot) {
		if err := l.engine.Evict(ctx, id); err != nil {
			l.log.Warn("reaper: eviction", "instance", id, "err", err)
			continue
		}
		// Issue #475: per-tier eviction counter. The RAM-pressure
		// path is the load-bearing observation for #475 — the
		// success criterion is best_effort≫reserved on
		// schedd_evicted_priority_total{reason="eviction_ram"}.
		// A reserved instance showing up here means the box
		// exhausted every best_effort candidate and had to fall
		// through to the reserved tier; the alert is on a
		// non-zero rate over a 5-minute window.
		if tier, ok := resolvePriority(snapshot, id); ok {
			if counter := l.ops.EvictedPriority(tier, "eviction_ram"); counter != nil {
				counter.Inc()
			}
		}
	}
}

// parkFromReaper keeps a slow or wedged snapshot from monopolising Loop.Run.
// Engine.Park remains the single lifecycle owner; this wrapper only supplies
// the reaper-specific deadline so the existing STOPPED/error transition runs
// through the normal Engine path when the deadline expires.
func (l *Loop) parkFromReaper(ctx context.Context, instanceID string) error {
	parkCtx, cancel := context.WithTimeout(ctx, reaperParkTimeout)
	defer cancel()
	return l.engine.Park(parkCtx, instanceID)
}

// reaperShutdown (M-2 / //code-review PR #1202 finding #8) is the
// mode-aware dispatcher the cron-loop reaper uses to shut down
// instances. Worker / job instances must go through
// Engine.StopInstance (signal-and-grace, no snapshot) so the
// guest-init Supervisor has a chance to run the customer's
// StopSignal handler before the engine escalates to SIGKILL.
// Request / service / mirror instances still go through
// Engine.Park (snapshot-and-park) — the snapshot cache is what
// makes the next wake cheap, and ADR-005 pins cold-boot as the
// fallback, not the primary path.
//
// Today's reaper exemption (finding #5) means worker / job / service
// IDs are not normally returned by ReapIdle / ReapAggressive —
// but the dispatcher is still load-bearing for the cron-loop
// paths the exemption does not cover: a future "max-worker-count"
// reaper rule, an admin operator force_stop on a worker, or any
// cron-loop shutdown primitive added in M-4 / M-5. Putting the
// dispatch at the shared call site means the routing is correct
// the first time, instead of being retro-fitted under each new
// reaper rule.
//
// The mode lookup walks the snapshot the reaper built earlier in
// the tick — a benign race exists if a different cron-loop tick
// mutated the instance between ReapIdle returning and
// reaperShutdown running, but the worst-case outcome is the
// dispatcher parks instead of stops (or vice versa), and both
// paths converge on STOPPED. ReaperParkTimeout bounds the
// synchronous call regardless of which engine path runs.
func (l *Loop) reaperShutdown(ctx context.Context, snapshot []InstanceInfo, instanceID string) error {
	mode := l.modeForShutdown(snapshot, instanceID)
	switch state.InstanceMode(mode) {
	case state.InstanceModeWorker, state.InstanceModeJob:
		return l.stopInstanceFromReaper(ctx, instanceID)
	default:
		return l.parkFromReaper(ctx, instanceID)
	}
}

// modeForShutdown looks up the InstanceInfo for id in the reaper
// snapshot and returns its Mode. Falls back to the empty string
// (which InstanceMode treats as ModeNormal, so the dispatcher
// parks) when the snapshot has no entry for id — a benign race
// covered in reaperShutdown's doc comment.
func (l *Loop) modeForShutdown(snapshot []InstanceInfo, instanceID string) string {
	for _, info := range snapshot {
		if info.Instance == instanceID {
			return info.Mode
		}
	}
	return ""
}

// stopInstanceFromReaper is the reaper-side wrapper around
// Engine.StopInstance. Same reaperParkTimeout cap as
// parkFromReaper so a wedged guest-init can't pin the cron loop
// for longer than the idle-path budget. The StopOptions pick
// SIGTERM (default per ADR-138 §Decision 1) and a 30 s grace
// window — matches the per-plan DefaultStopGracePeriodS for the
// Hobby tier; Pro / Scale get the same 30 s here because the
// cron-loop shutdown is operator-driven and shouldn't out-grace
// a customer's intentional force-stop.
func (l *Loop) stopInstanceFromReaper(ctx context.Context, instanceID string) error {
	stopCtx, cancel := context.WithTimeout(ctx, reaperParkTimeout)
	defer cancel()
	_, err := l.engine.StopInstance(stopCtx, instanceID, StopOptions{
		Signal:       int32(syscall.SIGTERM),
		GraceSeconds: 30,
	})
	return err
}

// reaperInstanceState mirrors the SQL partial-index predicate used by
// ListAllInstances. Keeping the filter at the per-app expansion site is
// necessary because ListInstancesForApp deliberately has a broader contract.
func reaperInstanceState(s state.State) bool {
	switch s {
	case state.StateRunning, state.StateWaking, state.StateColdBooting, state.StateSnapshotting:
		return true
	default:
		return false
	}
}

// resolvePriority looks up the per-app EvictionPriority for an
// instance id from the carriers built in runReaper. The boolean
// return is false when the id is not in the snapshot (a benign
// race: the instance was already parked by an earlier branch in
// the same tick). The empty-string snap to 'best_effort' for
// pre-#475 carriers (whose reaper tick lands before the column has
// been backfilled) flows through state.EvictionPriorityOrBestEffort
// so the metric observation matches the same bucket the INSERT
// path stamps on a fresh row.
func resolvePriority(snapshot []InstanceInfo, instanceID string) (string, bool) {
	for _, s := range snapshot {
		if s.Instance != instanceID {
			continue
		}
		return state.EvictionPriorityOrBestEffort(s.EvictionPriority), true
	}
	return "", false
}

// runReaperAggressive (issue #171) is the per-tick body of the
// aggressive scale-down path. Builds desiredByApp from the
// rolling-window RPS signal, calls ReapAggressive, and parks the
// returned candidates with a per-app per-tick cap. Emits one
// audit row per app that parked ≥ 1 instance, and one metric
// observation per app per tick. Carved out of runReaper so the
// behaviour is unit-testable without a clock / DB round-trip on
// the full reaper body.
func (l *Loop) runReaperAggressive(ctx context.Context, apps []state.App, snapshot []InstanceInfo, instanceToApp map[string]string, cooldownHeldByApp map[string]struct{}, now time.Time) {
	// PR-C review fix: instanceToApp is built once in runReaper
	// (O(N)) and threaded through here. Previously this function
	// built its own copy — cheap but a second O(N) walk on every
	// tick for no reason.
	// consideredAppIDs: every autoscale-enabled multi-instance app
	// gets exactly one metric observation per tick (either `park`
	// or `keep`). Apps absent from this set fall outside the
	// aggressive path's scope — no metric, no audit.
	// desiredByApp: subset of considered apps where the rolling
	// signal says we need > 0 instances. ReapAggressive is only
	// asked about these apps. An app in consideredAppIDs but
	// absent from desiredByApp means desired=0 — the signal says
	// "park everything down to floor+1".
	consideredAppIDs := map[string]struct{}{}
	desiredByApp := map[string]int{}
	for _, a := range apps {
		// Skip apps that don't participate in the aggressive path:
		// single-instance apps can't exceed max(min_instances,
		// desired+1) > 1 anyway, and apps with no autoscale target
		// fall through to ReapIdle for the timeout-based cooldown.
		if a.MaxConcurrency <= 1 {
			continue
		}
		if a.AutoscaleTargetRPS <= 0 {
			continue
		}
		consideredAppIDs[a.ID] = struct{}{}
		// Always record desired (even when 0) so ReapAggressive
		// can compute the surplus above max(floor, desired + 1).
		// Apps absent from desiredByApp would be SKIPPED by
		// ReapAggressive (the function's contract: apps not in the
		// map defer to ReapIdle), which would silently keep all 5
		// instances of a 100→0 rps burst alive.
		desired := l.recentLoad.RecentDesiredReplicas(a.ID, now, a.AutoscaleTargetRPS)
		desiredByApp[a.ID] = desired
	}
	if len(consideredAppIDs) == 0 {
		return
	}
	// ReapAggressive returns instance IDs in oldest-LastRequest
	// first order (per the freshness-floor direction). Group by
	// app_id so the per-tick cap applies per-app, not globally.
	parkByApp := map[string][]string{}
	if len(desiredByApp) > 0 {
		for _, id := range ReapAggressive(now, snapshot, desiredByApp, l.ops, cooldownHeldByApp) {
			appID := instanceToApp[id]
			if appID == "" {
				continue
			}
			parkByApp[appID] = append(parkByApp[appID], id)
		}
	}
	// runningByApp (PR-C, issue #462): per-app RUNNING count for the
	// min_floor_already outcome. We track this so the `keep` branch
	// can distinguish "signal said keep" (running > floor+1) from
	// "running already at the floor" (running == floor) and emit the
	// more-informative outcome label.
	runningByApp := map[string]int{}
	for _, s := range snapshot {
		if s.State != state.StateRunning {
			continue
		}
		runningByApp[s.AppID]++
	}
	// floorByApp (PR-C, issue #462 + issue #557 / ADR-071, code
	// review #725 finding F3): per-app MinInstances for the
	// min_floor_already comparison. Sourced from
	// max(app.EffectiveMinInstances(), max(ins.MinInstances)
	// across this app's instances in the snapshot) — the
	// snapshot rows already carry the post-enrichment
	// MinInstances value (max of app + per-deployment floor,
	// stamped at runReaper's snapshot walk) so a max across
	// the app's instances collapses to the same number the
	// biller charges for (pkg/meter/sampler.go:470-485) and
	// that ReapIdle / ReapAggressive consult via
	// ins.MinInstances. The previous shape used
	// a.EffectiveMinInstances() alone, which under-reports
	// when a customer sets a per-deployment floor via
	// ScalingPolicy — the metric would emit
	// schedd_scale_down_total{outcome="keep"} on a
	// deployment that's actually held at the per-deployment
	// floor, fooling operators reading the dashboard.
	floorByApp := map[string]int{}
	for _, a := range apps {
		floorByApp[a.ID] = a.EffectiveMinInstances()
	}
	for _, s := range snapshot {
		if floorByApp[s.AppID] < s.MinInstances {
			floorByApp[s.AppID] = s.MinInstances
		}
	}
	// For each considered app, emit either `park` (≥ 1 instance
	// parked), `min_floor_already` (running already at the floor
	// and no signal-driven park), or `keep` (signal said keep and
	// we have headroom). The metric counts one decision per app per
	// tick.
	cap := l.reaperParkCap
	if cap <= 0 {
		cap = MaxParksPerTickPerApp
	}
	for appID := range consideredAppIDs {
		ids := parkByApp[appID]
		if len(ids) == 0 {
			// PR-C (issue #462): distinguish the
			// "running already at the floor" case from
			// the plain "keep" case so the dashboard can
			// render "why didn't this scale down?" with
			// a more informative label. We only emit
			// min_floor_already when the customer has
			// explicitly set MinInstances > 0 (no point
			// calling out a floor of 0).
			if floorByApp[appID] > 0 && runningByApp[appID] <= floorByApp[appID] {
				l.ops.ObserveScaleDown(appID, "min_floor_already")
				continue
			}
			l.ops.ObserveScaleDown(appID, "keep")
			continue
		}
		if len(ids) > cap {
			l.log.Info("reaper: aggressive cap hit",
				"app", appID, "wanted", len(ids), "cap", cap)
			ids = ids[:cap]
		}
		l.ops.ObserveScaleDown(appID, "park")
		l.emitScaleDownAudit(ctx, appID, desiredByApp[appID], ids, now)
		// Issue #557 closure / ADR-072: the
		// `instances.parked_min_instances_released` audit emit
		// used to live here on a `floorByApp[appID] > postPark`
		// predicate, but that branch is structurally
		// unsatisfiable — ReapAggressive's `limit` arithmetic
		// never parks below the floor it was given
		// (`max(floor, desired+1)` floors before any park).
		// The audit now lives in runReaper's ReapIdle branch,
		// keyed on the lastFloorByApp carrier and the
		// post-enrichment app-wide max floor.
		aggressiveParkOK := false
		for _, id := range ids {
			if err := l.reaperShutdown(ctx, snapshot, id); err != nil {
				l.log.Warn("reaper: aggressive park", "instance", id, "err", err)
				continue
			}
			// Issue #475: per-tier eviction counter. The
			// aggressive path is the per-app scale-down arm of
			// issue #171 — both 'best_effort' and 'reserved'
			// instances get parked when the rolling-window RPS
			// signal says the customer is over-provisioned, so
			// the metric increments once per parked instance.
			if tier, ok := resolvePriority(snapshot, id); ok {
				if counter := l.ops.EvictedPriority(tier, "eviction_aggressive"); counter != nil {
					counter.Inc()
				}
			}
			aggressiveParkOK = true
		}
		// PR-C (issue #462): stamp last_scale_in_at after a
		// successful aggressive park. Best-effort — a stamp failure
		// logs a warning but does not roll back the parks.
		if aggressiveParkOK {
			if err := l.engine.Store().StampAppScaleIn(ctx, appID); err != nil {
				l.log.Warn("reaper: stamp scale-in (aggressive)", "app", appID, "err", err)
			}
		}
	}
}

// emitScaleDownAudit writes one events row per aggressive scale-
// down decision (issue #171). The row carries the desired
// replica count, observed RPS, target RPS, and the parked IDs so
// operators can reconstruct "why did this scale down?" without
// correlating instances. Best-effort: a failure is logged but
// does not roll back the Park calls (matches the engine's
// transitionWithKind posture on events table errors).
func (l *Loop) emitScaleDownAudit(ctx context.Context, appID string, desired int, parked []string, now time.Time) {
	data, err := json.Marshal(map[string]any{
		"app":     appID,
		"desired": desired,
		"parked":  parked,
		"reason":  "traffic_dropped",
		"now":     now.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		l.log.Warn("reaper: marshal scale-down audit", "err", err)
		return
	}
	subject := appID
	if err := l.engine.Store().AppendEvent(ctx, "schedd", "reaper_scale_down", &subject, data); err != nil {
		l.log.Warn("reaper: scale-down audit write failed", "app", appID, "err", err)
	}
}

// emitFloorReleasedAudit (issue #557 / ADR-071) writes one events
// row per aggressive-park tick where the post-park running count
// drops below the customer's effective min_instances. The semantic
// is "the customer's floor dropped, so we're releasing instances
// the floor would have kept resident". Pre-#557 the reaper silently
// released those; operators couldn't tell whether the bill change
// came from traffic or from a PATCH. Best-effort: a failure is
// logged but does not roll back the Park calls.
func (l *Loop) emitFloorReleasedAudit(ctx context.Context, appID string, floor, postPark int, now time.Time) {
	data, err := json.Marshal(map[string]any{
		"app":       appID,
		"floor":     floor,
		"post_park": postPark,
		"reason":    "min_instances_lowered",
		"now":       now.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		l.log.Warn("reaper: marshal floor-released audit", "err", err)
		return
	}
	subject := appID
	if err := l.engine.Store().AppendEvent(ctx, "schedd", "instances.parked_min_instances_released", &subject, data); err != nil {
		l.log.Warn("reaper: floor-released audit write failed", "app", appID, "err", err)
	}
}

// GatewaySynth is the slice of the gateway-internal RPC the cron
// loop (and Move 1's drain) use to fire a synthetic request through
// gatewayd-internal (so metering + rate-limit apply identically to user
// traffic). Defined as an interface here so the cron loop can be
// tested without a live gateway socket.
//
// SynthesizeRequest is the legacy no-payload path (back-pressure
// probe; deprecated — kept so old callers and tests don't break).
// Invoke is the Move 1 path: it carries an invocation row through
// the wake gate so cron / async / queue-pull / delayed-task traffic
// reaches the runner envelope unchanged.
type GatewaySynth interface {
	SynthesizeRequest(ctx context.Context, appID, method, path string) error
	Invoke(ctx context.Context, appID string, inv state.Invocation) (state.Invocation, error)
}

// prewokenGatewaySynth is the optional fast path for the invocation drain.
// Schedd already owns admission and has the live target after engine.Wake;
// implementations can carry that target to gatewayd-internal instead of
// waking the same app a second time. Legacy GatewaySynth implementations
// continue to use Invoke and retain their existing behaviour.
type prewokenGatewaySynth interface {
	InvokeWithWake(ctx context.Context, appID string, inv state.Invocation, wake WakeResult) (state.Invocation, error)
}

// httpGatewaySynth is the production GatewaySynth: an HTTP client
// pointed at gatewayd-internal's internal listener. The transport is chosen
// by the dial target:
//
//   - unix:// — net.Dial("unix", ...) over a unix socket; the host
//     component of the URL is the literal "unix" so http.Client
//     can shape the request line. basePrefix = "http://unix".
//   - tcp://|dns:// — plain http.Client.Do on the address; basePrefix
//     = "http://<addr>" or "https://<addr>" depending on tlsCfg.
//
// basePrefix is host-only (no path) so each method appends its own
// route: SynthesizeRequest → /v1/synthesize, Invoke →
// /v1/invocations:dispatch.
type httpGatewaySynth struct {
	client     *http.Client
	basePrefix string
	log        *slog.Logger
	// appPublicAuthModeLookup (ADR-119) returns the
	// public_auth_mode for the given appID. nil = every app
	// is treated as "open" (no JWT attached on the outbound
	// synth request). Wired by cmd/schedd/main.go via
	// WithAppPublicAuthModeLookup; tests construct directly.
	// The lookup is consulted on EVERY SynthesizeRequest call
	// — the call is rare (cron cadence, not per-request) so a
	// store round-trip is acceptable.
	//
	// Round-2 follow-up: the lookup carries (result, error) so
	// a transient lookup failure degrades to "assume
	// internal_only" (fail-closed) rather than "open"
	// (fail-open). See pkg/sched/configure_internal_svc.go.
	appPublicAuthModeLookup PublicAuthModeLookupFunc
	// mintInternalSvcToken (ADR-119) mints a JWT for outbound
	// Authorization: Bearer headers on synth requests targeting
	// apps whose public_auth_mode='internal_only'. Receives the
	// appID so the JWT can carry an app_id claim (future
	// per-app key-pinning). nil = the gate would 403 every
	// internal_only synth — surfaced as a loud error log so an
	// operator sees the misconfig. Wired by cmd/schedd/main.go
	// via WithMintInternalSvcToken; tests construct directly.
	mintInternalSvcToken func(appID string) (string, error)
}

// DialGatewaySynth opens an HTTP unix-socket client targeting
// gatewayd-internal's internal listener. The client is stateless — the unix
// socket is opened per request by the transport — so dial failures
// surface on the first SynthesizeRequest call.
//
// This is the legacy one-box dial. Multi-box schedd uses
// DialGatewaySynthTarget instead, which accepts a wire.ParseTarget-
// style URL (unix://|tcp://|dns://). The legacy function is kept
// because every test in cron_loop_test.go / drain_test.go uses
// it directly; renaming would be a much larger blast radius than
// this PR can afford.
func DialGatewaySynth(socketPath string, log *slog.Logger) (GatewaySynth, error) {
	if socketPath == "" {
		return nil, errors.New("sched: gateway synth socket path is empty")
	}
	if log == nil {
		log = slog.Default()
	}
	tr := &http.Transport{
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("unix", socketPath)
		},
	}
	c := &http.Client{Transport: tr, Timeout: 30 * time.Second}
	return &httpGatewaySynth{
		client:     c,
		basePrefix: httpUnixBasePrefix,
		log:        log,
	}, nil
}

// DialGatewaySynthTarget opens an HTTP client targeting gatewayd-internal's
// internal listener over a wire.ParseTarget-style URL
// (unix://|tcp://|dns://). Placement scheduler PR / ADR-025 axis 3
// (Q8): in a multi-box deploy, schedd and gatewayd-internal are not on the
// same host, so the unix-socket dial is no longer reachable. The
// TCP path uses TLS when the env wires a TLS config
// (FAAS_GATEWAY_SYNTH_TLS_CA / CERT / KEY; spec §7); the dns path
// delegates resolution to the stdlib. A nil tlsCfg falls through to
// plain HTTP — fine for tailnet-only deployments where the overlay
// is private (PR #120 / ADR-028 §Out of scope explicitly leaves
// tailnet ACLs to operator config).
//
// The baseURL scheme is "http" for tcp+dns (TLS-off) and "https"
// when tlsCfg is set; the unix path always uses "http" because the
// dialer writes bytes directly to the socket.
func DialGatewaySynthTarget(rawTarget string, tlsCfg *tls.Config, log *slog.Logger) (GatewaySynth, error) {
	if rawTarget == "" {
		return nil, errors.New("sched: gateway synth target is empty")
	}
	t, err := wire.ParseTarget(rawTarget)
	if err != nil {
		return nil, fmt.Errorf("sched: gateway synth parse %q: %w", rawTarget, err)
	}
	if log == nil {
		log = slog.Default()
	}
	switch t.Scheme {
	case wire.SchemeUnix:
		tr := &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", t.Address)
			},
		}
		return &httpGatewaySynth{
			client:     &http.Client{Transport: tr, Timeout: 30 * time.Second},
			basePrefix: httpUnixBasePrefix,
			log:        log,
		}, nil
	case wire.SchemeTCP, wire.SchemeDNS:
		scheme := httpScheme
		if tlsCfg != nil {
			scheme = httpsScheme
		}
		tr := &http.Transport{
			TLSClientConfig:       tlsCfg,
			ResponseHeaderTimeout: 30 * time.Second,
		}
		return &httpGatewaySynth{
			client:     &http.Client{Transport: tr, Timeout: 30 * time.Second},
			basePrefix: scheme + "://" + t.Address,
			log:        log,
		}, nil
	default:
		return nil, fmt.Errorf("sched: gateway synth target scheme %q not supported", t.Scheme)
	}
}

// lookupErrStr renders a lookup error for the warn log. nil
// returns "". audit-redaction invariant companion: the JWT
// token is never included; only the error reason code.
func lookupErrStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// HTTPClientForGatewaySynthTarget (issue #757 / ADR-100, commit #14)
// returns the http.Client + base URL the trigger dispatch tick uses
// to POST batch envelopes at the gateway's
// /v1/invocations:dispatch_batch endpoint.
//
// The cron path uses DialGatewaySynthTarget above (it wraps the
// transport in a GatewaySynth interface and ships cron through the
// legacy handlers). The trigger batch path uses raw http.Client
// because the batch endpoint's contract is plain JSON request /
// per-record JSON response (commits #13); wrapping that in a
// GatewaySynth-shaped interface would force a new method on the
// interface and invalidate every test stub that implements the
// surface (cron_loop_test.go in particular).
//
// target is the same wire.ParseTarget-style URL the cron path uses
// — "unix:///run/faas/gatewayd-internal.sock" for one-box,
// "tcp://host:port" or "dns://gatewayd-internal.service" for
// multi-box (placement scheduler PR / ADR-025 axis 3 Q8). The
// returned baseURL is what dispatch_triggers.go::postBatch
// concatenates with "/v1/invocations:dispatch_batch".
//
// TLS is not supported here (the batch endpoint inherits the
// gateway's own TLS posture — if the unix socket is the dial the
// socket IS the auth; if tcp is the dial the cluster operator
// wires a sidecar). If a future change adds TLS to the batch
// endpoint, thread a *tls.Config through this signature.

// WithAppPublicAuthModeLookup (ADR-119) arms the per-app
// public_auth_mode lookup used by SynthesizeRequest to decide
// whether to attach an Authorization: Bearer JWT (apps whose
// public_auth_mode='internal_only'). Returns the receiver for
// fluent chaining. nil = no lookup (every app treated as
// "open"; synth requests never carry Authorization — safe in
// dev + tests where no app is in internal_only mode).
func (h *httpGatewaySynth) WithAppPublicAuthModeLookup(lookup PublicAuthModeLookupFunc) *httpGatewaySynth {
	h.appPublicAuthModeLookup = lookup
	return h
}

// WithMintInternalSvcToken (ADR-119) arms the JWT minter used
// by SynthesizeRequest when the app is in internal_only mode.
// The minter is responsible for choosing svcName (today: hard-
// coded "schedd"), keypair (today: env-loaded at boot), and
// TTL (today: ≤30s — see plan). Returns the receiver for fluent
// chaining. nil = the gate 403s every internal_only synth and
// the loop logs a loud warning so an operator sees the
// misconfig.
func (h *httpGatewaySynth) WithMintInternalSvcToken(mint func(appID string) (string, error)) *httpGatewaySynth {
	h.mintInternalSvcToken = mint
	return h
}

func HTTPClientForGatewaySynthTarget(target string) (*http.Client, string, error) {
	if target == "" {
		return nil, "", errors.New("sched: gateway synth target is empty")
	}
	t, err := wire.ParseTarget(target)
	if err != nil {
		return nil, "", fmt.Errorf("sched: gateway synth parse %q: %w", target, err)
	}
	switch t.Scheme {
	case wire.SchemeUnix:
		tr := &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", t.Address)
			},
		}
		return &http.Client{Transport: tr, Timeout: 60 * time.Second}, httpUnixBasePrefix, nil
	case wire.SchemeTCP, wire.SchemeDNS:
		scheme := httpScheme
		return &http.Client{Timeout: 60 * time.Second}, scheme + "://" + t.Address, nil
	default:
		return nil, "", fmt.Errorf("sched: gateway synth target scheme %q not supported", t.Scheme)
	}
}

// SynthesizeRequest posts {app_id, method, path} to gatewayd-internal's internal
// /v1/synthesize endpoint over the unix socket. The HTTP transport
// (DialContext) handles the dial; this method just shapes the request.
func (h *httpGatewaySynth) SynthesizeRequest(ctx context.Context, appID, method, path string) error {
	body, err := json.Marshal(map[string]string{
		"app_id": appID, "method": method, "path": path,
	})
	if err != nil {
		return fmt.Errorf("sched: synth marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.basePrefix+"/v1/synthesize", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sched: synth request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// ADR-119 — attach an Authorization: Bearer JWT when the app
	// is in 'internal_only' mode. Same helper as Invoke + postBatch
	// consult the same per-app mode lookup and mint the same JWT.
	// The mode lookup is nil-safe (nil = every app treated as
	// "open"); the minter is nil-safe (nil + internal_only mode =
	// loud warn + the gate 403s on the receiving end).
	//
	// Fail-closed posture (round-2): a transient lookup
	// failure (Postgres outage, missing appID, etc.) returns
	// err != nil from the lookup. We treat that as "the app
	// is in internal_only mode" — i.e. attach the JWT
	// (fail-closed). The gateway-side gate (synth.go) checks
	// the app's actual mode; if the app is genuinely open,
	// the JWT is harmless (the gate only enforces on
	// internal_only). If the app is genuinely internal_only,
	// the JWT is required. Either way, fail-closed paths.
	if h.appPublicAuthModeLookup != nil {
		if res, lookupErr := h.appPublicAuthModeLookup(ctx, appID); lookupErr != nil || res.Mode == "internal_only" {
			if h.mintInternalSvcToken == nil {
				h.log.Warn("sched: app in internal_only mode (or lookup failed) but no minter wired; gate will 403",
					"app_id", appID, "lookup_err", lookupErrStr(lookupErr))
			} else {
				tok, mErr := h.mintInternalSvcToken(appID)
				if mErr != nil {
					return fmt.Errorf("sched: synth mint: %w", mErr)
				}
				req.Header.Set("Authorization", "Bearer "+tok)
			}
		}
	}
	// issue #517: stamp the per-cron-fire request_id on the
	// synthetic inbound request so gatewayd-internal's middleware picks it
	// up unchanged and the downstream wake timeline logs share
	// the same correlation id. Falls back to a fresh mint when
	// dispatchOneCron didn't set one (defence in depth — direct
	// SynthesizeRequest callers should pass a correlation
	// context, but the helper is robust without one).
	if fields, ok := wire.FromContext(ctx); ok && fields.RequestID != "" {
		req.Header.Set("x-faas-request-id", fields.RequestID)
	} else {
		req.Header.Set("x-faas-request-id", middleware.NewRequestID())
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("sched: synth do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sched: synth: gateway returned %d", resp.StatusCode)
	}
	return nil
}

// Invoke posts the Move 1 invocation envelope to gatewayd-internal's
// /v1/invocations:dispatch route. The response carries the post-dispatch
// state (dispatched/completed) so the drain can call Store.CompleteInvocation
// with the result blob. Network errors bubble up so the drain can retry.
func (h *httpGatewaySynth) Invoke(ctx context.Context, appID string, inv state.Invocation) (state.Invocation, error) {
	return h.invoke(ctx, appID, inv, nil)
}

// ExecuteStep adapts the gateway invocation envelope to the workflow
// executor seam. Unlike the legacy Invoke method it returns the downstream
// HTTP status, which is required for durable retry classification.
func (h *httpGatewaySynth) ExecuteStep(ctx context.Context, appID, path, method string, headers map[string]string, body []byte, timeout time.Duration) (int, []byte, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	headerBytes, err := json.Marshal(headers)
	if err != nil {
		return 0, nil, fmt.Errorf("sched: workflow headers: %w", err)
	}
	inv := state.Invocation{
		ID:      "workflow-" + middleware.NewRequestID(),
		AppID:   appID,
		Source:  state.InvocationSource("workflow"),
		Method:  method,
		Path:    path,
		Headers: headerBytes,
		Payload: body,
	}
	out, statusCode, err := h.invokeWithStatus(ctx, appID, inv, nil)
	if err != nil {
		return 0, nil, err
	}
	return statusCode, out.Result, nil
}

// InvokeWithWake is the pre-woken variant used by the unified invocation
// drain. The target is the result of schedd's engine.Wake call, so the
// gateway-internal side can forward directly to the selected instance.
func (h *httpGatewaySynth) InvokeWithWake(ctx context.Context, appID string, inv state.Invocation, wake WakeResult) (state.Invocation, error) {
	return h.invoke(ctx, appID, inv, &wake)
}

func (h *httpGatewaySynth) invoke(ctx context.Context, appID string, inv state.Invocation, wake *WakeResult) (state.Invocation, error) {
	out, _, err := h.invokeWithStatus(ctx, appID, inv, wake)
	return out, err
}

func (h *httpGatewaySynth) invokeWithStatus(ctx context.Context, appID string, inv state.Invocation, wake *WakeResult) (state.Invocation, int, error) {
	var headers map[string]string
	if len(inv.Headers) > 0 {
		if err := json.Unmarshal(inv.Headers, &headers); err != nil {
			return inv, 0, fmt.Errorf("sched: invocation headers: %w", err)
		}
	}
	dispatch := map[string]any{
		"invocation_id": inv.ID,
		"app_id":        appID,
		"source":        string(inv.Source),
		"method":        inv.Method,
		"path":          inv.Path,
		"headers":       headers,
		"body_b64":      base64.StdEncoding.EncodeToString(inv.Payload),
	}
	if wake != nil {
		dispatch["instance_id"] = wake.InstanceID
		dispatch["node_id"] = wake.NodeID
		dispatch["deployment_id"] = wake.DeploymentID
		dispatch["wake_id"] = wake.WakeID
		dispatch["port"] = wake.Port
	}
	body, err := json.Marshal(dispatch)
	if err != nil {
		return inv, 0, fmt.Errorf("sched: invocation marshal: %w", err)
	}
	url := h.basePrefix + "/v1/invocations:dispatch"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return inv, 0, fmt.Errorf("sched: invocation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Durable workflow steps are an authenticated internal delivery
	// surface even when the customer app's public_auth_mode is open. The
	// synth socket is DAC-protected, but the workflow contract also needs a
	// short-lived signed token so gatewayd-internal can distinguish a fresh
	// schedd delivery from a forged or replayed envelope.
	if inv.Source == state.InvocationSource("workflow") {
		if h.mintInternalSvcToken == nil {
			return inv, 0, errors.New("sched: workflow invocation requires internal service token minter")
		}
		tok, mErr := h.mintInternalSvcToken(appID)
		if mErr != nil {
			return inv, 0, fmt.Errorf("sched: workflow invocation mint: %w", mErr)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	// ADR-119 — schedd's move-1 dial is now gated by the same
	// internal_only mode check SynthesizeRequest uses. Without
	// this attachment, a forged schedd (or anything else in the
	// faas group) could invoke an internal_only app via
	// /v1/invocations:dispatch — the gate at synth.go::handleInvocationDispatch
	// would 403 the request, but the cheap-prevention is
	// attaching the JWT here so the gate accepts. Same
	// nil-safe + fail-closed posture as SynthesizeRequest.
	if h.appPublicAuthModeLookup != nil {
		if res, lookupErr := h.appPublicAuthModeLookup(ctx, appID); lookupErr != nil || res.Mode == "internal_only" {
			if h.mintInternalSvcToken == nil {
				h.log.Warn("sched: invoke path: app in internal_only mode (or lookup failed) but no minter wired; gate will 403",
					"app_id", appID, "lookup_err", lookupErrStr(lookupErr))
			} else {
				tok, mErr := h.mintInternalSvcToken(appID)
				if mErr != nil {
					return inv, 0, fmt.Errorf("sched: invocation mint: %w", mErr)
				}
				if req.Header.Get("Authorization") == "" {
					req.Header.Set("Authorization", "Bearer "+tok)
				}
			}
		}
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return inv, 0, fmt.Errorf("sched: invocation do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return inv, 0, fmt.Errorf("sched: invocation: gateway returned %d", resp.StatusCode)
	}
	var out struct {
		State      string          `json:"state"`
		Result     json.RawMessage `json:"result"`
		StatusCode int             `json:"status_code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return inv, 0, fmt.Errorf("sched: invocation response: %w", err)
	}
	if out.State != "" {
		inv.State = state.InvocationState(out.State)
	} else {
		inv.State = state.InvocationDispatching
	}
	if len(out.Result) > 0 {
		inv.Result = append(json.RawMessage(nil), out.Result...)
	}
	if out.StatusCode == 0 {
		out.StatusCode = http.StatusOK
	}
	return inv, out.StatusCode, nil
}

// runCronTick walks every enabled cron and dispatches any whose
// next-fire boundary has passed. It does NOT compute next-fire from
// robfig itself — the customer's cron.Schedule lives on the crons row
// (Schedule field) and we parse it per-tick. The dispatch path:
//
//  1. Resolve the cron + app, ensure the account isn't suspended.
//  2. Parse the schedule with robfig/cron; if NextFireAt(lastFiredAt) is
//     not in the past, skip.
//  3. Wake the app via the engine (idempotent — already-running apps
//     return their current instance).
//  4. SynthesizeRequest through gatewayd-internal so metering + rate limits apply.
//  5. MarkCronFired + emit NotifyCronFired for the dashboard.
//
// Step 3+4 are the load-bearing spec bits (M7); they route the
// synthetic request through the gateway's full path so the metering +
// quota pipeline can't tell cron traffic from user traffic apart.

// runJobsDispatchTick is one iteration of the Mega-1 job dispatch
// loop (issue #1184 Workstream A / ADR-099). Wired in pkg/sched/loop.go
// gated by FAAS_JOBS_DISPATCH — when OFF the select arm never fires.
// Errors are logged warn-level and the tick continues on the next
// second; a stuck pgpool is louder (the failOpen vmmd adapter keeps
// the surface safe under FAAS_JOBS_DISPATCH=1 until the vmmd gRPC
// JobColdBoot method ships in the follow-up commit).
func (l *Loop) runJobsDispatchTick(ctx context.Context) {
	if err := l.engine.DispatchJobsTick(ctx); err != nil {
		l.log.Warn("schedd: jobs dispatch tick failed", "err", err)
	}
}

// runJobsReaperTick is one iteration of the stuck-job reaper.
// Same gate + warn-and-continue contract as runJobsDispatchTick.
func (l *Loop) runJobsReaperTick(ctx context.Context) {
	if _, err := l.engine.JobReaperTick(ctx); err != nil {
		l.log.Warn("schedd: stuck-job reaper tick failed", "err", err)
	}
}

func (l *Loop) runWorkflowsDispatchTick(ctx context.Context) {
	if l.workflowOrch == nil {
		l.workflowOrch = NewWorkflowOrchestrator(l.engine.Store(), nil, l.audit, nil, l.log)
	}
	if err := l.workflowOrch.DispatchTick(ctx); err != nil {
		l.log.Warn("schedd: workflow dispatch tick failed", "err", err)
	}
}

func (l *Loop) runWorkflowRetention(ctx context.Context) {
	if l.workflowRetention == nil {
		return
	}
	if err := l.workflowRetention.SweepOnce(ctx); err != nil {
		l.log.Warn("workflow retention: sweep failed", "err", err)
	}
}

func (l *Loop) runCronTick(ctx context.Context) {
	// Phase 2 / Gate A: only dispatch crons on apps this schedd
	// owns. Without this filter every schedd would fire every
	// cron and the cron_fired_audit row would diverge from
	// actual dispatch (the duplicate-dispatch hazard). Empty
	// owner = legacy single-box (list every cron).
	var crons []state.Cron
	var err error
	store := l.engine.Store()
	if owner := l.engine.OwnerNodeID(); owner != "" {
		crons, err = store.ListOwnedCronsByNodeID(ctx, owner)
	} else {
		crons, err = store.ListEnabledCrons(ctx)
	}
	if err != nil {
		l.log.Warn("cron: list", "err", err)
		return
	}
	now := l.now()
	for _, c := range crons {
		l.dispatchOneCron(ctx, c, now)
	}
}

// dispatchOneCron is the per-cron decision tree. Factored out so the
// test surface can drive one cron with a fake clock.
// statusStr renders the ok bool as the literal `cron.fired.status`
// audit payload value. Kept as a tiny helper so the dispatch can use
// a single `if ok { ... }` set without a second branch on the emit
// site.
func statusStr(ok bool) string {
	if ok {
		return "ok"
	}
	return "err"
}

// CronDispatchTrigger labels the source of a cron fire for the audit
// payload. ADR-090: fire-now adds a second trigger value while the
// 60s tick keeps the canonical "schedule" path. The Trigger field
// rides on the audit payload so dashboards can split tick fires from
// operator fires without parsing a different event name.
type CronDispatchTrigger string

const (
	// TriggerSchedule is the canonical 60s tick path. cron.fired
	// audit rows + MarkCronFired side effects are emitted.
	TriggerSchedule CronDispatchTrigger = "schedule"
	// TriggerManual is the fire-now API path. cron.fired.manually
	// audit rows are emitted; MarkCronFired is NOT called so the
	// next scheduled fire still lands at the boundary.
	TriggerManual CronDispatchTrigger = "manual"

	// AuditEventCronFired is the IAM-4 audit event name emitted by
	// the 60s tick path. Const-ified so the goconst lint (3+ literal
	// hits across dispatchCronLocked + its inline audit emit) finds
	// one canonical anchor; downstream audit pipelines should
	// match on this constant rather than the bare string.
	AuditEventCronFired = "cron.fired"
	// AuditEventCronFiredManually is the IAM-4 audit event name for
	// manual fires (POST /v1/crons/{id}/run, ADR-090). Splits from
	// the schedule path so audit-event allowlists can gate on the
	// event name directly without consulting the trigger payload.
	AuditEventCronFiredManually = "cron.fired.manually"
)

// CronRun is the lightweight return shape from a fire-now dispatch.
// apid renders this as a FireCronResponse; the schedd tick path
// ignores it (the tick path's caller is runCronTick, which has no
// audit-response surface).
type CronRun struct {
	InvocationID string
	InstanceID   string
	Success      bool
}

func (l *Loop) dispatchOneCron(ctx context.Context, c state.Cron, now time.Time) {
	sched, err := ParseSchedule(c.Schedule)
	if err != nil {
		l.log.Warn("cron: bad schedule", "cron_id", c.ID, "err", err)
		return
	}
	// Boundary guard: fire iff we've crossed the next-fire boundary
	// since LastFiredAt. robfig's NextFireAt(from) is exclusive — call
	// it with LastFiredAt to get the upcoming boundary; if that boundary
	// is in the future, we already fired in this window. If LastFiredAt
	// is zero, the CreatedAt-based boundary is the first-fire guard so
	// we don't double-fire a cron enabled mid-minute.
	var boundary time.Time
	if c.LastFiredAt.IsZero() {
		boundary = c.CreatedAt
	} else {
		boundary = c.LastFiredAt
	}
	if sched.NextFireAt(boundary).After(now) {
		// Already fired in the current window.
		return
	}
	res, ok := l.dispatchCronLocked(ctx, c, now, TriggerSchedule)
	_ = res
	if !ok {
		return
	}
	if err := l.engine.Store().MarkCronFired(ctx, c.ID, now); err != nil {
		l.log.Warn("cron: mark fired", "cron_id", c.ID, "err", err)
	}
}

// dispatchCronLocked is the post-boundary part of the cron fire path.
// Called by both the 60s tick (trigger=TriggerSchedule) and the
// fire-now API (trigger=TriggerManual). The deferred audit emit is
// the only writer of cron.fired* rows — do not duplicate it. The
// caller decides whether to call MarkCronFired (tick path: yes; fire-now:
// no, because manual fire must not shift next_fire_at).
//
// Returns (CronRun, true) on best-effort completion regardless of
// success — the second value is true when the audit row was emitted.
// Returns (CronRun{}, false) when the suspended-account guard rejects
// the fire (no audit row, per spec §11 abuse guard).
func (l *Loop) dispatchCronLocked(ctx context.Context, c state.Cron, now time.Time, trigger CronDispatchTrigger) (CronRun, bool) {
	// issue #517: mint a fresh request_id at the cron dispatch
	// boundary so the synthetic request that flows throughgatewayd-internal
	// carries the same correlation id the rest of the wake
	// timeline logs use. Stored on ctx so SynthesizeRequest can
	// stamp it on the outbound x-faas-request-id header, and on
	// the cron.fired notify payload so an operator query joins
	// the synthetic request back to the cron that fired it.
	requestID := middleware.NewRequestID()
	ctx = wire.WithContext(ctx, wire.CorrelationFields{
		RequestID: requestID,
		AppID:     c.AppID,
	})
	// Capture the pre-fire state for the audit row (issue #291
	// follow-up — schedd emits `cron.fired` after the dispatch path
	// runs to completion). lastFiredAtBefore is what an operator
	// reads to reconstruct the boundary that just crossed; the
	// fireSucceeded/invocationID/instanceID triple is updated below
	// as the dispatch progresses so the defer-block emit at the
	// bottom of this function can record status="ok"|"err" without
	// re-checking the dispatch internals.
	lastFiredAtBefore := c.LastFiredAt
	fireSucceeded := false
	var invocationID, instanceID string
	var acct state.Account
	app, err := l.engine.Store().AppByID(ctx, c.AppID)
	if err != nil {
		l.log.Warn("cron: app", "cron_id", c.ID, "err", err)
		return CronRun{}, false
	}
	acctRec, err := l.engine.Store().AccountByID(ctx, app.AccountID)
	if err != nil {
		l.log.Warn("cron: account", "cron_id", c.ID, "err", err)
		return CronRun{}, false
	}
	if !acctRec.Active() {
		// Suspended accounts don't get cron traffic (spec §11 abuse
		// guard). The meter hard-stop will park the live instance; we
		// just skip the synthetic request here. NO audit row — the
		// cron was scheduled by a customer who is now suspended; we
		// don't want to surface suspended-account crons in the
		// per-account audit list (and §5.1's 4xx-invariant covers
		// the "we didn't fire" semantic too).
		return CronRun{}, false
	}
	acct = acctRec
	// Defer the cron.fired emit so ALL post-boundary failure modes
	// (Wake error, Invoke error, SynthesizeRequest fallback error)
	// surface a status="err" row. SOC 2 CC7.2 cares about the
	// "the cron was scheduled but failed to fire" case more than
	// the happy path — ops needs the audit signal to reconcile
	// expected vs actual fires. Best-effort semantics from
	// pkg/audit.Auditor: never rolls back the underlying state
	// changes (MarkCronFired, EnqueueInvocation, etc.).
	//
	// ADR-090: the trigger field rides on the payload so dashboards
	// can split tick fires (cron.fired) from operator fires
	// (cron.fired.manually) without parsing the event name. The
	// event name itself is split per trigger so audit-event
	// allowlists can gate on it without consulting the payload.
	defer func() {
		if l.audit == nil {
			return
		}
		// last_fired_at_before is omitted on the first fire (no prior
		// fire exists — LastFiredAt was zero). The unconditional
		// format below would have rendered that as the literal
		// "0001-01-01T00:00:00Z", which an operator can't distinguish
		// from data corruption. Missing key → JSON renders the field
		// as absent; payload[k] on the read side returns nil.
		payload := map[string]any{
			"cron_id":       c.ID,
			"app_id":        c.AppID,
			"schedule":      c.Schedule,
			"path":          c.Path,
			"fired_at":      now.UTC().Format(time.RFC3339Nano),
			"status":        statusStr(fireSucceeded),
			"invocation_id": invocationID,
			"instance_id":   instanceID,
			"trigger":       string(trigger),
		}
		if !lastFiredAtBefore.IsZero() {
			payload["last_fired_at_before"] = lastFiredAtBefore.UTC().Format(time.RFC3339Nano)
		}
		eventName := AuditEventCronFired
		if trigger == TriggerManual {
			eventName = AuditEventCronFiredManually
		}
		l.audit.Emit(ctx, eventName, &acct.ID, payload)
	}()
	// ADR-098: cron now goes through EnsureWake so a cron tick racing
	// a gateway burst (or a floor / scaleup / targets trigger on the
	// same parked app) coalesces into one virtual boot. The detached
	// leader ctx means a cancelled triggering cron doesn't kill the
	// boot the next follow-on caller still needs.
	// ADR-123: translate the internal CronDispatchTrigger enum to
	// the external wake-boot trigger enum. Schedule is "60s tick",
	// Manual is "POST /v1/crons/{id}/run (ADR-090)".
	wakeBootTrigger := TriggerCronSched
	if trigger == TriggerManual {
		wakeBootTrigger = TriggerCronManual
	}
	if _, err := l.engine.EnsureWake(ctx, c.AppID, wakeBootTrigger); err != nil {
		l.log.Warn("cron: wake", "cron_id", c.ID, "err", err)
		return CronRun{}, true
	}
	// Move 1: write the cron row to invocations so it shows up in
	// /v1/invocations and the meter sees it. cron_id is stamped so
	// the unified history endpoint can join back to crons for
	// "last_fired_at" semantics (kept on the crons table; both
	// surfaces are still served per the chosen plan).
	cronID := c.ID
	inv := state.Invocation{
		AppID:     c.AppID,
		AccountID: acct.ID,
		Source:    state.InvocationCron,
		Method:    "POST",
		Path:      c.Path,
		CronID:    &cronID,
		Headers:   json.RawMessage(`{"x-faas-cron":"true"}`),
		DueAt:     now,
	}
	enq, err := l.engine.Store().EnqueueInvocation(ctx, inv)
	if err != nil {
		l.log.Warn("cron: enqueue invocation", "cron_id", c.ID, "err", err)
		// Continue past — legacy wake-only path is still safe.
	}
	// Walk the row through pending → dispatching BEFORE calling
	// Invoke. The store's Claim only accepts state=pending, and
	// StampInstanceInvocation only accepts state=dispatching — so
	// the lifecycle must mirror the drain's: claim → invoke → stamp
	// → complete. Doing the claim here also keeps the row out of
	// the drain's next tick (which filters state='pending').
	if enq.ID != "" {
		if _, err := l.engine.Store().ClaimInvocation(ctx, enq.ID, "", 60); err != nil {
			l.log.Warn("cron: claim invocation", "cron_id", c.ID, "err", err)
		}
	}
	if l.gateway != nil {
		// Invoke delivers the synthetic HTTP envelope through the
		// wake gate; the meter + the runner both see this as a
		// request with method+path+headers. The synth adapter
		// (cmd/gatewayd-internal) does its own always-Wake internally and
		// returns the live instance id on the echoed Invocation.
		invokeOut, ierr := l.gateway.Invoke(ctx, c.AppID, inv)
		if ierr != nil {
			l.log.Warn("cron: invoke", "cron_id", c.ID, "err", ierr)
			// issue #791 — terminate the row. Before this, a failed
			// cron invoke left the claimed row parked in
			// state='dispatching' forever: the drain's tick filters
			// on state='pending' so it never reclaimed it, and
			// nothing else wrote a terminal state. The run-history
			// surface would render every failed fire as perpetually
			// "running", and the queue-depth counters (which count
			// dispatching) drifted up by one per failure.
			//
			// retryAfter=0 because the cron loop owns its own
			// schedule: the next boundary re-fires this cron anyway,
			// so re-queueing the row would double-dispatch. The
			// outcome classifier turns a blown deadline into
			// 'timeout' and everything else into 'failed'.
			if enq.ID != "" {
				if err := l.engine.Store().FailInvocation(ctx, enq.ID, "invoke: "+ierr.Error(), 0, 0, failOutcome(ierr)); err != nil {
					l.log.Warn("cron: fail invocation", "cron_id", c.ID, "err", err)
				}
			}
			// Fall through to legacy wake-only shape so this
			// doesn't silently drop. tests may rely on the
			// SynthesizeRequest call for back-compat assertions.
			if err := l.gateway.SynthesizeRequest(ctx, c.AppID, "POST", c.Path); err != nil {
				l.log.Warn("cron: synthesize (legacy)", "cron_id", c.ID, "err", err)
				// status="err" via defer; fireSucceeded stays false.
				return CronRun{InvocationID: enq.ID}, true
			}
		} else if enq.ID != "" {
			// Stamp the live instance handle + complete the row
			// so the drain's per-tick (state='pending' filter)
			// never picks it up. The meter join counts this row
			// once, against the live instance.
			if err := l.engine.Store().StampInstanceInvocation(ctx, enq.ID, invokeOut.InstanceID); err != nil {
				l.log.Warn("cron: stamp instance", "cron_id", c.ID, "err", err)
			}
			if err := l.engine.Store().CompleteInvocation(ctx, enq.ID, nil); err != nil {
				l.log.Warn("cron: complete", "cron_id", c.ID, "err", err)
			}
			// Success path: the synth invoke produced a live
			// instance handle. Capture the audit-trail triples so
			// the deferred emit can record status="ok" with the
			// wake that served the fire. invocation_id is the
			// drain row id; instance_id is the live VM that
			// gatewayd-internal picked. Mirrors the invocations_history
			// join so an operator can pivot from the audit row to
			// the wake record with one query.
			fireSucceeded = true
			invocationID = enq.ID
			instanceID = invokeOut.InstanceID
		}
	}
	payload, _ := json.Marshal(map[string]any{
		"cron_id": c.ID, "app_id": c.AppID, "at": now.UTC().Format(time.RFC3339Nano),
		// issue #517: thread the synthetic request_id so a dashboard
		// join from the cron.fired SSE frame back to the wake
		// timeline logs is one query, not a guess.
		"request_id": requestID,
	})
	if err := l.engine.Notifier().Notify(ctx, db.NotifyCronFired, string(payload)); err != nil {
		l.log.Warn("cron: notify cron_fired", "err", err)
	}
	return CronRun{InvocationID: invocationID, InstanceID: instanceID, Success: fireSucceeded}, true
}

// ErrCronDisabled is the typed error returned by RunCronNow when
// the cron row exists but Enabled=false. apid maps this to a 410
// Gone with stable code `cron_disabled` (pkg/api/errors.go).
var ErrCronDisabled = errors.New("sched: cron disabled")

// ErrAccountSuspended is the typed error returned by RunCronNow
// when the dispatchCronLocked path rejects the fire under the
// suspended-account guard (spec §11 abuse guard). apid maps this
// to a 403 with stable code `account_suspended`. The audit row is
// NOT emitted in this branch (matches the tick path's behaviour).
var ErrAccountSuspended = errors.New("sched: account suspended")

// ErrNoCapacity is the typed error returned by RunCronNow when the
// dispatch path cannot produce a wake (no RAM headroom, no
// eligible runner, etc.). apid maps this to a 503 with the
// existing capacity error shape.
var ErrNoCapacity = errors.New("sched: no dispatch capacity")

// RunCronNow fires a cron immediately, bypassing the due-time boundary
// guard. The cron must be Enabled and the account must be Active; the
// caller (apid's fireCronNow) is responsible for the IDOR-safe ownership
// check. Returns a CronRun (invocation_id, instance_id, success) on
// best-effort completion; errors bubble up only for the wire-shape
// rejections (ErrNoCapacity for the dispatch path). MarkCronFired is
// NOT called: a manual fire must not shift next_fire_at — that stays
// owned by the 60s tick path. The audit event is cron.fired.manually
// (vs. cron.fired for the tick path) and the payload carries the same
// fields plus trigger="manual".
func (l *Loop) RunCronNow(ctx context.Context, cronID, accountID string) (CronRun, error) {
	c, err := l.engine.Store().CronByID(ctx, cronID)
	if err != nil {
		return CronRun{}, err
	}
	if !c.Enabled {
		return CronRun{}, ErrCronDisabled
	}
	now := l.now()
	run, ok := l.dispatchCronLocked(ctx, c, now, TriggerManual)
	if !ok {
		// Suspended-account guard rejects the fire. The deferred
		// audit emit does not run (no row written), so fail
		// closed at the API surface.
		return CronRun{}, ErrAccountSuspended
	}
	// Issue #791 PR-D code-review: previously returned CronRun{
	// Success: true} unconditionally, which discarded the
	// fireSucceeded bool computed inside dispatchCronLocked.
	// PR-D's processFireNowRequest maps this Success to the
	// cron_fire_now_requests row's terminal status, so a fire
	// that the audit row recorded as status="err" was being
	// stamped succeeded on the row — a customer-visible
	// disagreement. Propagate the real result.
	return run, nil
}
