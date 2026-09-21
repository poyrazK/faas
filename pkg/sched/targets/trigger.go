package targets

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched/scalesignal"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// wakeBootTriggerTargets mirrors sched.TriggerTargets (ADR-123).
// Defined locally to avoid a sched import; the value is the canonical
// wake-boot trigger enum entry and must stay in lockstep with
// pkg/sched/triggers.go.
const wakeBootTriggerTargets = "targets"

// workerPoolTriggerTargets is the dedicated wake-timeline reason for
// queue-driven worker replica reconciliation. It is local to avoid importing
// sched (which would create the existing targets ↔ sched cycle).
const workerPoolTriggerTargets = "worker.pool"

// AdmitResult is the typed wake-subset the trigger needs from the
// engine. Mirrors pkg/sched/scaleup.AdmitResult exactly; we re-
// declare it locally so the targets package does not import scaleup
// (which would create a cycle: targets ↔ scaleup via their shared
// Engine surface). The concrete *sched.Engine returns this shape
// via a thin adapter constructed in cmd/schedd.
type AdmitResult struct {
	InstanceID string
	AtCapacity bool
}

// WakeOutcome (ADR-098): trigger-local projection of sched.CoordOutcome.
// The leader's ledger enforces max_concurrency; the trigger observes the
// at-capacity path via the bus, not the return value.
type WakeOutcome struct {
	InstanceID string
	WakeID     string
	ColdBoot   bool
}

// Outcome is the closed set of concurrent_requests scale-up
// decision outcomes. Pre-instantiated in pkg/wire.NewOpsMetrics
// alongside the scaleup package outcomes so the counter rows
// surface in /metrics from boot. Adding a new outcome requires
// extending that loop too.
type Outcome string

const (
	// OutcomeAdmit: per-instance inflight exceeds target and
	// headroom is available; Engine.AdmitInstance returned a live
	// instance id.
	OutcomeAdmit Outcome = "admit"
	// OutcomeRejectAtCap: target met but the ledger already has
	// max_concurrency instances. The trigger does NOT issue a wake.
	OutcomeRejectAtCap Outcome = "reject_at_cap"
	// OutcomeNoSignal: target not met OR no inflight signal (cold
	// path, instats nil, or all instances idle).
	OutcomeNoSignal Outcome = "no_signal"
	// OutcomeCooldownHeld (PR-C, issue #462): target met but
	// Concurrency(appID) > 0 AND now - app.LastScaleOutAt <
	// ScalingPolicy.ScaleOutCooldownS. The customer's
	// "rate-limit scale-outs" knob. Cold-start wakes (concurrency
	// == 0) bypass cooldown — see the decide() doc for the
	// load-bearing discriminator.
	OutcomeCooldownHeld Outcome = "cooldown_held"
)

// AppStore is the read-only slice of state.Store the trigger needs.
// Defined as an interface so tests can inject a fake without spinning
// up Postgres.
type AppStore interface {
	ListAllApps(ctx context.Context) ([]state.App, error)
	ListAppsByNodeID(ctx context.Context, nodeID string) ([]state.App, error)
}

// Ledger is the read-only slice of NodeLedger the trigger needs.
// Concurrency returns the number of instances of appID counting toward
// its plan cap (pkg/sched/admission.go).
type Ledger interface {
	Concurrency(appID string) int
}

// Engine is the slice of sched.Engine the trigger needs. AdmitInstance
// performs the admission; the typed AdmitResult.AtCapacity=true signals
// the cap rejection path.
type Engine interface {
	// AdmitInstance (PR-B / issue #272): scope is the preview scope
	// (`pr-{N}`) forwarded to the underlying sched.Engine.
	// Empty = prod (legacy single-deployment behaviour).
	AdmitInstance(ctx context.Context, appID, scope, trigger string) (AdmitResult, error)
	// EnsureWake (ADR-098) is retained on the shared engine surface for
	// compatibility with other wake producers. The reactive scale-up
	// path deliberately does not call it: its idempotent Phase-1
	// shortcut cannot create a second VM for a hot app.
	EnsureWake(ctx context.Context, appID, trigger string) (WakeOutcome, error)
}

// BurstEngine is the optional fast path for engines that can admit a bounded
// batch of instances. Keeping it separate from Engine preserves the small
// fake/test seam and lets older adapters fall back to one admission at a time.
type BurstEngine interface {
	AdmitInstances(ctx context.Context, appID, scope, trigger string, count int) ([]AdmitResult, error)
}

// WorkerPoolEngine is the optional queue-backed worker path. Worker pools
// cannot use the request wake primitive because worker-mode admissions are
// intentionally rejected there; the scheduler instead reconciles the desired
// resident count through an explicit deployment admission path.
type WorkerPoolEngine interface {
	ReconcileWorkerPool(ctx context.Context, appID string, desired int, trigger string) error
}

// InstatsReader is the per-instance in-flight signal source (PR-C,
// issue #462). Wraps the *instancestats.Reader accessor the sched
// poller populates from the vmmd ActivityTracker wire shape.
// Returns (n, false) when the app has no live instances or the
// poller has not yet ticked. The trigger does NOT call this
// directly; it goes through RingBuffer.AppMaxInflight so the
// sliding window can dedupe bursts across ticks.
type InstatsReader interface {
	MaxInflightForApp(appID string) (n int64, ok bool)
}

// QueueStatsReader is the read-only queue signal used by the
// queue_depth target. state.Store already implements this surface; keeping
// it optional preserves the existing trigger seam for deployments that have
// not enabled queue-backed workers yet.
type QueueStatsReader interface {
	QueueState(ctx context.Context, appID string) (state.QueueStats, error)
}

// QueueBindingStatsReader is the optional binding-aware queue signal. The
// target trigger still scales an app-level worker fleet, but retains each
// enabled binding's sample so its concurrency cap contributes to the target.
type QueueBindingStatsReader interface {
	ListQueueBindingsForApp(ctx context.Context, accountID, appID string) ([]state.QueueBinding, error)
	QueueStateForQueue(ctx context.Context, appID, queueName string) (state.QueueStats, error)
}

// BrokerLagReader supplies broker-reported consumer lag / queue depth for an app.
type BrokerLagReader interface {
	BrokerLag(ctx context.Context, appID string) (int64, bool, error)
}

// CustomMetricReader supplies an app's pushed ADR-202 gauges. Optional: a
// deployment whose apps declare no custom targets wires nil and the axis
// reports no signal.
//
// state.Store already satisfies this. Freshness is applied by the TRIGGER,
// not the store, so the trigger's own tick time drives it — a store that
// filtered by wall clock would make a back-dated evaluation silently wrong.
type CustomMetricReader interface {
	ListCustomMetrics(ctx context.Context, appID string) ([]state.CustomMetric, error)
}

// queueDepthSignal keeps the aggregate queue projection used by the generic
// scaler together with the binding-level samples needed to size a worker
// fleet fairly. A single app-level depth is not enough when two bindings have
// independent concurrency caps: one hot binding must not turn into an
// unbounded replica request while another binding's backlog is ignored.
type queueDepthSignal struct {
	queue    state.QueueStats
	bindings []queueBindingDepth
	// brokerLag is the consumer lag the broker itself reports, and
	// haveBrokerLag distinguishes "the broker says zero" from "no broker
	// reader answered". It is kept SEPARATE from queue.Depth because the
	// two are different quantities: depth is what Gregale has already
	// pulled into its own queue, lag is what is still sitting on the
	// broker. Overloading one field with the other made queue_depth
	// report lag whenever a broker answered, and made queue_lag report
	// depth whenever none did.
	brokerLag     int64
	haveBrokerLag bool
}

type queueBindingDepth struct {
	binding state.QueueBinding
	queue   state.QueueStats
}

// desiredWorkers returns the worker-pool target for a queue-depth signal. With
// bindings, each active backlog earns at least one worker and is capped by its
// binding max-concurrency before the app/account cap is applied by the caller.
// The legacy app-wide queue path preserves its original aggregate formula.
func (s queueDepthSignal) desiredWorkers(target float64, maxInstances int, minWorkers ...int) int {
	minAllowed := 1
	if len(minWorkers) > 0 {
		minAllowed = minWorkers[0]
	}
	if target <= 0 {
		return minAllowed
	}
	desired := 0
	// A broker-reported lag is the authoritative backlog for a
	// broker-backed worker: it counts what is still on the broker, while
	// queue.Depth counts only what the poller has already pulled into
	// Gregale's own queue (at most one batch per tick). Sizing the pool
	// off depth would size it off the batch size.
	//
	// This branch is why the lag has to be carried as its own field. It
	// used to be written into queue.Depth, which made it reach this
	// calculation correctly but also made queue_depth report lag.
	if s.haveBrokerLag {
		if s.brokerLag > 0 {
			desired = int(math.Ceil(float64(s.brokerLag) / target))
		}
	} else if len(s.bindings) == 0 {
		if s.queue.Depth > 0 {
			desired = int(math.Ceil(float64(s.queue.Depth) / target))
		}
	} else {
		for _, perBinding := range s.bindingWorkerDemand(target) {
			desired += perBinding
		}
	}
	if desired < minAllowed {
		desired = minAllowed
	}
	if maxInstances > 0 && desired > maxInstances {
		desired = maxInstances
	}
	return desired
}

// bindingWorkerDemand computes the uncapped per-binding worker demand. The
// app-level worker cap is applied after these values are summed; exposing the
// uncapped values as metrics lets operators see which binding is responsible
// for pressure even when the account cap limits the fleet.
func (s queueDepthSignal) bindingWorkerDemand(target float64) map[string]int {
	if target <= 0 || len(s.bindings) == 0 {
		return nil
	}
	demand := make(map[string]int, len(s.bindings))
	for _, sample := range s.bindings {
		workers := 0
		if sample.binding.Enabled && sample.queue.Depth > 0 {
			workers = int(math.Ceil(float64(sample.queue.Depth) / target))
			if workers < 1 {
				workers = 1
			}
			if sample.binding.MaxConcurrency > 0 && workers > sample.binding.MaxConcurrency {
				workers = sample.binding.MaxConcurrency
			}
		}
		demand[sample.binding.QueueName] = workers
	}
	return demand
}

// Stats is the snapshot of inputs the pure decide() function reads.
// Splitting this out keeps decide() trivially testable — no mocks,
// no goroutines, no engine. The trigger's Tick assembles one Stats
// per app and dispatches to decide.
type Stats struct {
	AppID               string
	TargetValue         float64 // 0 = no target set (OutcomeNoSignal)
	MaxConcurrency      int     // plan cap
	Concurrency         int     // live instances counting toward the cap
	PerInstanceInflight int64   // measured, 0 when no signal
	HaveInflight        bool    // true iff RingBuffer.AppMaxInflight returned a sample
	LastScaleOutAt      time.Time
	ScaleOutCooldownS   int
	Now                 time.Time // injected for testability
}

// QueueDepthStats is the snapshot consumed by decideQueueDepth. TargetValue
// is interpreted as the desired maximum queued messages per worker instance.
// QueueDepth is the live queue depth returned by state.Store.QueueState.
type QueueDepthStats struct {
	TargetValue       float64
	MaxConcurrency    int
	Concurrency       int
	QueueDepth        int
	LastScaleOutAt    time.Time
	ScaleOutCooldownS int
	Now               time.Time
}

// Decision is the decide() result. ShouldAdmit=true triggers the
// Engine.AdmitInstance call in Tick.
type Decision struct {
	ShouldAdmit      bool
	Outcome          Outcome
	Headroom         int
	ObservedInflight int64
	// ObservedQueueDepth is populated by the queue_depth path so callers
	// and focused tests can inspect the signal that drove the decision.
	ObservedQueueDepth int
	// Desired is the estimated resident-instance count, capped at
	// MaxConcurrency. Admissions is the number of new instances this
	// decision should request, before the per-tick burst bound.
	Desired    int
	Admissions int
	// Winner is the metric that produced Desired when more than one was
	// declared (ADR-194). Carried into the decision log so an operator can
	// see WHICH signal drove an admission, not merely that one did — with
	// a list of targets, "it scaled" is no longer a complete answer.
	Winner string
}

// decide is the pure decision function (PR-C, issue #462). Total —
// every (target, signal, headroom, cooldown) combination maps to
// exactly one outcome.
//
// Rules:
//
//   - No target set (TargetValue == 0) → no_signal (defensive: the
//     consumer filters apps without a target before calling).
//   - Cooldown in effect (LastScaleOutAt non-zero AND
//     now - LastScaleOutAt < ScaleOutCooldownS AND Concurrency > 0)
//     → cooldown_held. The Concurrency > 0 discriminator is
//     load-bearing: a cold start (zero concurrency) bypasses
//     cooldown even when LastScaleOutAt is freshly stamped by a
//     concurrent admit. Without this check, a request-driven wake
//     would always hit cooldown and defeat the customer's "scale
//     on demand" use case.
//   - Per-instance inflight > target (strict >) → admit when
//     headroom > 0, else reject_at_cap.
//   - Otherwise → no_signal.
//
// P1A asymmetry note: the cooldown consult above is a fast-bail
// predicate, NOT an emission of schedd_scale_up_decisions_total{
// outcome="cooldown_held"}. The trigger's job is to fire
// Engine.AdmitInstance when over target; if Engine.admitGate
// subsequently rejects via the same cooldown consult, it emits
// `cooldown_held` once at engine.go:4862-4867. Routing the
// emission through the trigger too would double-count and would
// also break the scale-up metric semantics (`cooldown_held` is
// the wake-gate path; the scale-up trigger's cooldown is its own
// gate, not the metric source). The trigger returns
// Decision{Outcome: OutcomeCooldownHeld} but the OutcomeCooldownHeld
// is mapped to a no-op in the trigger-side caller
// (Tick at trigger.go:279) rather than to ObserveScaleUp, so the
// closed-set metric never sees it.
// limits is the per-app envelope every arbitrated decision is clamped to.
// Shared by the single-metric wrappers and the multi-signal Tick path so the
// cap, the cooldown and the outcome ladder have exactly one implementation.
type limits struct {
	MaxConcurrency    int
	Concurrency       int
	LastScaleOutAt    time.Time
	ScaleOutCooldownS int
	Now               time.Time
}

// decideTargets is the one decision function for this trigger (ADR-194).
//
// It arbitrates over every observation — the max desired count wins — and
// then applies the platform's own policy in a fixed order: cooldown, then
// cap, then burst. The ordering is the contract, and the developer configures
// none of it; they declare signals and the platform decides.
//
// Cooldown is evaluated before hotness, matching what the single-metric
// decide() has always done. The queue-depth path used to short-circuit on an
// empty backlog first, so a parked queue inside a cooldown window reported
// no_signal where an idle request path reported cooldown_held. Unifying them
// here makes the outcome depend on the app's state rather than on which
// metric it happens to have declared; no test pinned the old asymmetry.
func decideTargets(l limits, obs []scalesignal.Observation) Decision {
	declared := false
	for _, o := range obs {
		if o.Target > 0 {
			declared = true
			break
		}
	}
	if !declared {
		return Decision{Outcome: OutcomeNoSignal}
	}
	// Cold starts bypass cooldown: concurrency 0 means there is nothing
	// running to protect from oscillation, and holding here would turn the
	// customer's rate limit into a wake latency floor.
	if l.Concurrency > 0 && !l.LastScaleOutAt.IsZero() {
		cooldown := time.Duration(l.ScaleOutCooldownS) * time.Second
		if l.Now.Sub(l.LastScaleOutAt) < cooldown {
			return Decision{Outcome: OutcomeCooldownHeld}
		}
	}
	res := scalesignal.Arbitrate(l.Concurrency, obs)
	if !res.Hot {
		return Decision{Outcome: OutcomeNoSignal}
	}
	headroom := l.MaxConcurrency - l.Concurrency
	if headroom <= 0 {
		return Decision{Outcome: OutcomeRejectAtCap, Headroom: 0}
	}
	desired := res.Desired
	if desired > l.MaxConcurrency {
		desired = l.MaxConcurrency
	}
	return Decision{
		ShouldAdmit: true,
		Outcome:     OutcomeAdmit,
		Headroom:    headroom,
		Desired:     desired,
		Admissions:  desired - l.Concurrency,
		Winner:      res.Winner,
	}
}

// decide is the single-metric concurrent_requests entry point, retained as
// the narrow surface its own table-driven tests drive. The arithmetic lives
// in scalesignal; this only shapes the inputs and restores the
// ObservedInflight field the metrics path reads.
func decide(s Stats) Decision {
	dec := decideTargets(limits{
		MaxConcurrency:    s.MaxConcurrency,
		Concurrency:       s.Concurrency,
		LastScaleOutAt:    s.LastScaleOutAt,
		ScaleOutCooldownS: s.ScaleOutCooldownS,
		Now:               s.Now,
	}, []scalesignal.Observation{{
		Metric:   api.ScalingMetricConcurrentRequests,
		Target:   s.TargetValue,
		Measured: float64(s.PerInstanceInflight),
		Have:     s.HaveInflight,
	}})
	if dec.Outcome != OutcomeCooldownHeld {
		dec.ObservedInflight = s.PerInstanceInflight
	}
	return dec
}

// decideQueueDepth is the pure queue backlog decision function. A target is
// a per-instance backlog budget: with N workers, a queue depth greater than
// target*N requests another worker. A positive queue with zero workers
// always admits one (the cold-start path). The same cooldown and cap rules
// as the concurrent_requests trigger apply.
// decideQueueDepth is the single-metric queue_depth entry point. Like
// decide() it delegates the arithmetic to scalesignal — which is where the
// "backlog is already fleet-total, do NOT multiply by concurrency" rule
// lives — and only restores the observed field the metrics path reads.
func decideQueueDepth(s QueueDepthStats) Decision {
	dec := decideTargets(limits{
		MaxConcurrency:    s.MaxConcurrency,
		Concurrency:       s.Concurrency,
		LastScaleOutAt:    s.LastScaleOutAt,
		ScaleOutCooldownS: s.ScaleOutCooldownS,
		Now:               s.Now,
	}, []scalesignal.Observation{{
		Metric:   api.ScalingMetricQueueDepth,
		Target:   s.TargetValue,
		Measured: float64(s.QueueDepth),
		Have:     true,
	}})
	dec.ObservedQueueDepth = s.QueueDepth
	return dec
}

// Trigger is the per-app concurrent_requests scale-up trigger
// worker (PR-C, issue #462). Constructed via New(); the only public
// methods are Tick() and Interval(). Nil-safe on every receiver
// and every dep so schedd can wire the trigger before every
// downstream dependency is fully online.
type Trigger struct {
	appStore      AppStore
	instats       InstatsReader
	queueStats    QueueStatsReader
	queueBindings QueueBindingStatsReader
	brokerLag     BrokerLagReader
	customMetrics CustomMetricReader
	engine        Engine
	ledger        Ledger
	metrics       *wire.OpsMetrics
	log           *slog.Logger
	interval      time.Duration

	// ownerNodeID is the durable shard key this schedd scales. Empty
	// preserves the central/legacy posture and reads all apps.
	ownerNodeID string

	// per-app sliding window of per-instance max-inflight. Reads
	// from instats on each Tick; the window keeps the most recent
	// reading so the trigger can debounce single-tick spikes.
	ring *RingBuffer

	// admissionMu serializes the in-memory retry state. The loop normally
	// runs one Tick at a time, but keeping this state protected also makes
	// direct test/integration callers safe.
	admissionMu      sync.Mutex
	admissionBackoff map[string]admissionBackoffState
}

// Options is the functional-options bag for New(). All fields are
// optional; zero-values fall back to sane defaults.
type Options struct {
	// Metrics is the per-daemon OpsMetrics the trigger emits into.
	// Nil is safe — the trigger no-ops on every Observe call.
	Metrics *wire.OpsMetrics
	// Logger is used for warn-level diagnostics. Nil falls back to
	// slog.Default() so the trigger is always observable.
	Logger *slog.Logger
	// Interval is the tick rate. Zero falls back to
	// api.ScaleUpDecisionIntervalSeconds (1s) — same cadence as
	// pkg/sched/scaleup.
	Interval time.Duration
	// QueueStatsReader supplies the queue depth for queue_depth targets.
	// It is optional so existing RPS/concurrency-only deployments retain
	// their current behavior.
	QueueStatsReader QueueStatsReader
	// QueueBindingStatsReader enables per-binding queue gauges and aggregates
	// enabled binding backlogs for queue_depth targets. It is optional so
	// legacy app-wide queues continue to use QueueStatsReader.
	QueueBindingStatsReader QueueBindingStatsReader
	// BrokerLagReader supplies broker-reported consumer lag for queue_lag / queue_depth targets.
	BrokerLagReader BrokerLagReader
	// CustomMetricReader supplies pushed ADR-202 gauges. Optional.
	CustomMetricReader CustomMetricReader
}

// New constructs the trigger. instats is REQUIRED (unlike the
// scaleup trigger, which has a fallback RPS path); a nil instats
// turns the trigger into a no-op because there is no other signal
// source for the concurrent_requests axis. appStore, engine, and
// ledger may be nil — every nil is handled defensively in Tick.
func New(appStore AppStore, instats InstatsReader, engine Engine, ledger Ledger, opts Options) *Trigger {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Interval <= 0 {
		opts.Interval = api.ScaleUpDecisionIntervalSeconds * time.Second
	}
	return &Trigger{
		appStore:         appStore,
		instats:          instats,
		queueStats:       opts.QueueStatsReader,
		queueBindings:    opts.QueueBindingStatsReader,
		customMetrics:    opts.CustomMetricReader,
		brokerLag:        opts.BrokerLagReader,
		engine:           engine,
		ledger:           ledger,
		metrics:          opts.Metrics,
		log:              opts.Logger,
		interval:         opts.Interval,
		ring:             NewRingBuffer(5, time.Second, opts.Interval),
		admissionBackoff: make(map[string]admissionBackoffState),
	}
}

// WithBrokerLagReader attaches a broker lag reader to the trigger.
func (t *Trigger) WithBrokerLagReader(r BrokerLagReader) *Trigger {
	if t != nil {
		t.brokerLag = r
	}
	return t
}

// readQueueState returns the queue signal used by the existing scaler. When
// bindings exist, only enabled binding queues contribute to the aggregate;
// the individual samples are retained so worker pools can scale fairly across
// bindings. An app with no bindings falls back to the legacy app-wide queue
// projection.
func (t *Trigger) readQueueState(ctx context.Context, app state.App, now time.Time) (queueDepthSignal, bool, error) {
	// Broker lag is an ADDITIONAL reading, not a replacement for the queue
	// state. Returning here — as this did — threw away the binding
	// breakdown, zeroed InFlight/DeadLetter/OldestPendingAt for the
	// SetQueueState gauges, and handed queue_depth a number that was
	// actually lag.
	var brokerLag int64
	var haveBrokerLag bool
	if t.brokerLag != nil {
		lag, ok, err := t.brokerLag.BrokerLag(ctx, app.ID)
		if err != nil {
			return queueDepthSignal{}, false, err
		}
		if ok {
			brokerLag, haveBrokerLag = lag, true
		}
	}
	// withBroker stamps the lag onto whichever queue projection the paths
	// below produce, so every return carries it.
	withBroker := func(sig queueDepthSignal, ok bool, err error) (queueDepthSignal, bool, error) {
		sig.brokerLag = brokerLag
		sig.haveBrokerLag = haveBrokerLag
		// A broker reading alone is enough to have a signal, even when no
		// queue reader is configured — that is the whole point of a
		// broker-backed trigger.
		return sig, ok || haveBrokerLag, err
	}
	if t.queueBindings != nil {
		bindings, err := t.queueBindings.ListQueueBindingsForApp(ctx, app.AccountID, app.ID)
		if err != nil {
			return queueDepthSignal{}, false, err
		}
		if len(bindings) > 0 {
			signal := queueDepthSignal{bindings: make([]queueBindingDepth, 0, len(bindings))}
			for _, binding := range bindings {
				var queue state.QueueStats
				if binding.Enabled {
					queue, err = t.queueBindings.QueueStateForQueue(ctx, app.ID, binding.QueueName)
					if err != nil {
						return queueDepthSignal{}, false, fmt.Errorf("queue binding %q: %w", binding.QueueName, err)
					}
					signal.queue.Depth += queue.Depth
					signal.queue.InFlight += queue.InFlight
					signal.queue.DeadLetter += queue.DeadLetter
					if !queue.OldestPendingAt.IsZero() && (signal.queue.OldestPendingAt.IsZero() || queue.OldestPendingAt.Before(signal.queue.OldestPendingAt)) {
						signal.queue.OldestPendingAt = queue.OldestPendingAt
					}
				}
				signal.bindings = append(signal.bindings, queueBindingDepth{binding: binding, queue: queue})
				if t.metrics != nil {
					t.metrics.SetQueueBindingState(app.ID, binding.QueueName, queue.Depth, queue.InFlight, queue.DeadLetter, queue.OldestPendingAt, now)
				}
			}
			return withBroker(signal, true, nil)
		}
	}
	if t.queueStats == nil {
		return withBroker(queueDepthSignal{}, false, nil)
	}
	queue, err := t.queueStats.QueueState(ctx, app.ID)
	return withBroker(queueDepthSignal{queue: queue}, true, err)
}

// Interval returns the tick rate. schedd's loop uses this when
// constructing the ticker so the cadence is owned by the trigger.
func (t *Trigger) Interval() time.Duration {
	if t == nil {
		return 0
	}
	return t.interval
}

// WithOwnerNodeID scopes the trigger to apps owned by this schedd's
// compute node. Empty preserves the central one-box posture.
func (t *Trigger) WithOwnerNodeID(nodeID string) {
	if t == nil {
		return
	}
	t.ownerNodeID = nodeID
}

// admit requests a bounded batch. Production's sched.Engine implements the
// BurstEngine fast path; small adapters and existing tests intentionally fall
// back to the original one-at-a-time call. The engine remains the authority on
// capacity, so a race can still return AtCapacity for the final attempt.
func (t *Trigger) admit(ctx context.Context, appID string, count int) ([]AdmitResult, error) {
	if count <= 0 {
		return nil, nil
	}
	if count > api.ScaleUpMaxBurstPerTick {
		count = api.ScaleUpMaxBurstPerTick
	}
	if burst, ok := t.engine.(BurstEngine); ok {
		return burst.AdmitInstances(ctx, appID, "", wakeBootTriggerTargets, count)
	}
	// An older adapter has no safe way to carry the burst continuation
	// marker, so preserve its original one-admission behavior.
	result, err := t.engine.AdmitInstance(ctx, appID, "", wakeBootTriggerTargets)
	if err != nil {
		return nil, err
	}
	return []AdmitResult{result}, nil
}

// Tick runs one sweep. It is the single public entry point the
// schedd loop calls. Returns nil on success; errors are logged
// inside the loop (the trigger never aborts the loop on a transient
// store outage).
//
// The trigger is read-only on the apps table and the ledger; the only side
// effects are the admission/reconciliation call on the scale branch and the
// metric observations.
func (t *Trigger) Tick(ctx context.Context) error {
	if t == nil || t.appStore == nil || (t.instats == nil && t.queueStats == nil && t.queueBindings == nil && t.brokerLag == nil && t.customMetrics == nil) {
		return nil
	}
	now := time.Now()
	var apps []state.App
	var err error
	if t.ownerNodeID != "" {
		apps, err = t.appStore.ListAppsByNodeID(ctx, t.ownerNodeID)
	} else {
		apps, err = t.appStore.ListAllApps(ctx)
	}
	if err != nil {
		return fmt.Errorf("targets: list apps: %w", err)
	}
	for _, app := range apps {
		policy := app.ScalingPolicy
		// ADR-194: an app declares a LIST of signals. EffectiveTargets is
		// the only correct reader — it promotes the pre-ADR-194 singular
		// `target` to a one-element list, so reading either field directly
		// misses half the fleet.
		declared := policy.EffectiveTargets()
		if len(declared) == 0 {
			continue
		}
		// This trigger owns the two axes it can observe. rps and cpu are
		// read by pkg/sched/scaleup against the same policy; the two run as
		// separate arms of one select in the schedd loop, so they serialize
		// and the second arbitrates against the concurrency the first
		// raised. Across a tick pair that is the max over all signals.
		inflightTarget, haveInflightTarget := policy.TargetFor(api.ScalingMetricConcurrentRequests)
		queueTarget, haveQueueTarget := policy.TargetFor(api.ScalingMetricQueueDepth)
		queueLagTarget, haveQueueLagTarget := policy.TargetFor(api.ScalingMetricQueueLag)
		// ADR-202: custom targets are keyed by NAME, so they cannot be
		// resolved with TargetFor — an app may declare several.
		customTargets := customTargetsOf(policy)
		if !haveInflightTarget && !haveQueueTarget && !haveQueueLagTarget && len(customTargets) == 0 {
			continue
		}
		conc := 0
		if t.ledger != nil {
			conc = t.ledger.Concurrency(app.ID)
		}
		var lastScaleOut time.Time
		if app.LastScaleOutAt != nil {
			lastScaleOut = *app.LastScaleOutAt
		}
		// Collect one observation per declared axis this trigger can read,
		// then arbitrate over all of them at once. A signal whose reader is
		// missing or whose read failed contributes Have=false rather than
		// aborting the app: with a list of targets, one unavailable source
		// must not suppress a different one that is hot. When it is the
		// only declared axis, arbitration returns not-hot and the app still
		// reports no_signal exactly as it did before ADR-194.
		obs := make([]scalesignal.Observation, 0, 2)
		var dec Decision
		var observedInflight int64
		var observedQueueDepth int
		var observedBrokerLag int64
		var haveBrokerLag bool
		workerQueuePath := false
		workerDesired := 0
		workerMaxInstances := 0
		maxInstances := app.MaxConcurrency
		if policy.MaxInstances > 0 && policy.MaxInstances < maxInstances {
			maxInstances = policy.MaxInstances
		}

		if haveInflightTarget {
			perInst, haveInflight := int64(0), false
			if t.instats != nil {
				// Pull a fresh max-inflight reading into the ring buffer,
				// then read the windowed value. The ring buffer keeps the
				// most recent sample so a single-tick spike does not
				// immediately scale.
				if n, ok := t.instats.MaxInflightForApp(app.ID); ok {
					t.ring.Observe(now, app.ID, n)
				}
				perInst, haveInflight = t.ring.AppMaxInflight(app.ID, now)
			}
			observedInflight = perInst
			obs = append(obs, scalesignal.Observation{
				Metric:   api.ScalingMetricConcurrentRequests,
				Target:   inflightTarget,
				Measured: float64(perInst),
				Have:     haveInflight,
			})
		}

		if haveQueueTarget || haveQueueLagTarget {
			// A missing reader is not an empty queue. Never admit blindly
			// on an unknown backlog — contribute no reading instead.
			haveQueue := false
			var queueSignal queueDepthSignal
			if t.queueStats != nil || t.queueBindings != nil || t.brokerLag != nil {
				sig, ok, err := t.readQueueState(ctx, app, now)
				switch {
				case err != nil:
					t.log.Warn("targets: queue state failed", "app_id", app.ID, "err", err)
				case ok:
					queueSignal, haveQueue = sig, true
				}
			}
			if haveQueue {
				queue := queueSignal.queue
				observedQueueDepth = queue.Depth
				observedBrokerLag = queueSignal.brokerLag
				haveBrokerLag = queueSignal.haveBrokerLag
				if t.metrics != nil {
					// Queue gauges are sampled alongside the queue-depth decision;
					// OpsMetrics bounds app labels before they reach Prometheus.
					t.metrics.SetQueueState(app.ID, queue.Depth, queue.InFlight, queue.DeadLetter, queue.OldestPendingAt, now)
				}
				workerMaxInstances = maxInstances
				workerQueuePath = app.WorkloadClass == state.WorkloadClassWorker || app.Manifest.ExecutionMode == api.ExecutionModeWorker
				if workerQueuePath {
					activeTarget := queueTarget
					if !haveQueueTarget || (haveQueueLagTarget && queueLagTarget < queueTarget) {
						activeTarget = queueLagTarget
					}
					if t.metrics != nil {
						for binding, demand := range queueSignal.bindingWorkerDemand(activeTarget) {
							t.metrics.SetQueueBindingWorkerDemand(app.ID, binding, demand)
						}
					}
					minAllowed := 1
					if app.Manifest.WorkerReplicas != nil && app.Manifest.WorkerReplicas.Min == 0 {
						minAllowed = 0
					}
					workerDesired = queueSignal.desiredWorkers(activeTarget, maxInstances, minAllowed)
					if policy.MinInstances > workerDesired {
						workerDesired = policy.MinInstances
					}
					if workerDesired < minAllowed {
						workerDesired = minAllowed
					}
					if workerDesired > maxInstances && maxInstances > 0 {
						workerDesired = maxInstances
					}
				}
			}
			if haveQueueTarget {
				obs = append(obs, scalesignal.Observation{
					Metric:   api.ScalingMetricQueueDepth,
					Target:   queueTarget,
					Measured: float64(observedQueueDepth),
					Have:     haveQueue,
				})
			}
			if haveQueueLagTarget {
				// queue_lag measures the BROKER's reported consumer lag,
				// not the local queue depth. Falling back to depth here
				// would make a metric named "lag" silently report a
				// different quantity whenever no broker answered — and
				// report it as a confident reading rather than as no
				// signal.
				obs = append(obs, scalesignal.Observation{
					Metric:   api.ScalingMetricQueueLag,
					Target:   queueLagTarget,
					Measured: float64(observedBrokerLag),
					Have:     haveBrokerLag,
				})
			}
		}

		if len(customTargets) > 0 {
			// One observation per declared name. A name with no stored
			// row, or one whose push has gone stale, contributes
			// Have=false: it never scales the app and never suppresses a
			// sibling signal that does have a reading.
			//
			// Failing to "no signal" rather than holding the last value
			// is the deliberate choice (ADR-202). If the pusher dies —
			// the cron stops, the customer's infrastructure has an
			// outage — a frozen backlog would pin the fleet at whatever
			// it was when the pusher stopped, indefinitely, and bill for
			// it.
			stored := map[string]state.CustomMetric{}
			if t.customMetrics != nil {
				rows, err := t.customMetrics.ListCustomMetrics(ctx, app.ID)
				if err != nil {
					t.log.Warn("targets: custom metrics failed", "app_id", app.ID, "err", err)
				}
				for _, row := range rows {
					stored[row.Name] = row
				}
			}
			freshness := time.Duration(api.CustomMetricFreshnessSeconds) * time.Second
			for _, ct := range customTargets {
				row, ok := stored[ct.Name]
				fresh := ok && now.Sub(row.ObservedAt) <= freshness
				obs = append(obs, scalesignal.Observation{
					Metric:   api.ScalingMetricCustom,
					Target:   ct.Value,
					Measured: row.Value,
					Have:     fresh,
				})
			}
		}

		// MaxInstances bounds the arbitrated result for every axis. Before
		// ADR-194 only the queue path applied it, because it was the only
		// path that had loaded it; a concurrent_requests app's
		// max_instances was silently ignored by this trigger.
		dec = decideTargets(limits{
			MaxConcurrency:    maxInstances,
			Concurrency:       conc,
			LastScaleOutAt:    lastScaleOut,
			ScaleOutCooldownS: policy.ScaleOutCooldownS,
			Now:               now,
		}, obs)
		dec.ObservedInflight = observedInflight
		dec.ObservedQueueDepth = observedQueueDepth
		// Always emit the decision metric so the rate of
		// no_signal vs admit vs cooldown_held is observable.
		if t.metrics != nil {
			t.metrics.ObserveScaleUp(app.ID, string(dec.Outcome))
			// Attribute the admission to the signal that actually drove
			// it. Decision.Winner was carried from the arbiter and read
			// by nothing until now, so its doc comment's promise — that
			// an operator can see WHICH signal scaled an app — was not
			// true. With a list of declared targets, "it scaled" is not
			// a complete answer.
			//
			// The outcome check is belt-and-braces: decideTargets only
			// sets Winner on the admit return, and the accessor ignores
			// an empty metric, so either guard alone would do. Keeping
			// both states the invariant at the call site, where a future
			// branch that sets Winner without admitting would otherwise
			// silently break the identity
			// sum(winning_signal) == decisions{outcome="admit"}.
			if dec.Outcome == OutcomeAdmit {
				t.metrics.ObserveScaleUpWinningSignal(app.ID, dec.Winner)
			}
		}
		if workerQueuePath {
			pool, ok := t.engine.(WorkerPoolEngine)
			if ok {
				if dec.Outcome == OutcomeCooldownHeld {
					continue
				}
				if dec.Outcome == OutcomeRejectAtCap && workerMaxInstances > 0 {
					workerDesired = workerMaxInstances
				}
				minAllowed := 1
				if app.Manifest.WorkerReplicas != nil && app.Manifest.WorkerReplicas.Min == 0 {
					minAllowed = 0
				}
				if workerDesired < minAllowed {
					workerDesired = minAllowed
				}
				if t.admissionBackoffActive(app.ID, now) {
					continue
				}
				if err := pool.ReconcileWorkerPool(ctx, app.ID, workerDesired, workerPoolTriggerTargets); err != nil {
					t.recordAdmissionFailure(app.ID, now)
					t.log.Warn("targets: reconcile worker pool failed", "app_id", app.ID, "err", err)
				} else {
					t.clearAdmissionBackoff(app.ID)
				}
				continue
			}
		}
		if !dec.ShouldAdmit {
			continue
		}
		// Admit path: request the desired number of instances, bounded
		// by ScaleUpMaxBurstPerTick. The engine enforces the cap via
		// NodeLedger.Admit; if it is hit between the decide() check and
		// an individual batch attempt, the corresponding result is
		// AtCapacity.
		if t.engine == nil {
			continue
		}
		if t.admissionBackoffActive(app.ID, now) {
			continue
		}
		// Scale-out must use AdmitInstance, not EnsureWake. EnsureWake is
		// deliberately idempotent: its Phase-1 fast path returns an
		// existing RUNNING instance. Using it here prevents a hot app
		// from adding a second VM even when this trigger has found
		// headroom. AdmitInstance skips that fast path and reserves a
		// new capacity slot through the shared ledger.
		results, err := t.admit(ctx, app.ID, dec.Admissions)
		if err != nil {
			t.recordAdmissionFailure(app.ID, now)
			t.log.Warn("targets: admit failed", "app_id", app.ID, "err", err)
		} else {
			t.clearAdmissionBackoff(app.ID)
		}
		for _, result := range results {
			if result.AtCapacity {
				// The ledger can reach the cap between decide() and
				// an individual batch attempt. Re-observe the effective
				// rejection so the pressure dashboard stays accurate.
				if t.metrics != nil {
					t.metrics.ObserveScaleUp(app.ID, string(OutcomeRejectAtCap))
				}
			}
		}
	}
	return nil
}

// customTargetsOf returns the app's ADR-202 targets. Separate from
// ScalingPolicy.TargetFor because custom targets are keyed by name and an app
// may declare more than one, which a metric-keyed lookup cannot express.
func customTargetsOf(policy *state.ScalingPolicy) []state.ScalingTarget {
	var out []state.ScalingTarget
	for _, t := range policy.EffectiveTargets() {
		if t.Metric == api.ScalingMetricCustom && t.Name != "" && t.Value > 0 {
			out = append(out, t)
		}
	}
	return out
}
