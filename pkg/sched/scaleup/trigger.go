package scaleup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// wakeBootTriggerScaleup mirrors sched.TriggerScaleup (ADR-123).
// Defined locally to avoid the scaleup → sched import cycle (see
// AdmitResult below). The string value is the canonical wake-boot
// trigger enum entry; if pkg/sched/triggers.go ever changes the
// value, this constant must be updated in lockstep.
const wakeBootTriggerScaleup = "scaleup"

// AdmitResult is the typed wake-subset the trigger needs from the
// engine. It mirrors sched.WakeResult's AtCapacity / InstanceID
// fields without importing the sched package (which would create a
// cycle: scaleup → sched → scaleup via loop.go). The concrete
// *sched.Engine returns AdmitResult{AtCapacity: result.AtCapacity,
// InstanceID: result.InstanceID} via a thin adapter constructed in
// cmd/schedd.
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

// Outcome is the closed set of scale-up decision outcomes. Pre-instantiated
// in pkg/wire.NewOpsMetrics so the counter rows surface in /metrics from
// boot. Adding a new outcome requires extending that loop too.
type Outcome string

const (
	// OutcomeAdmit: target met + headroom available, Engine.AdmitInstance
	// returned a live instance id.
	OutcomeAdmit Outcome = "admit"
	// OutcomeRejectAtCap: target met but the ledger already has
	// max_concurrency instances. The trigger does NOT issue a wake —
	// the cap is enforced inside AdmitInstance's NodeLedger.Admit.
	OutcomeRejectAtCap Outcome = "reject_at_cap"
	// OutcomeNoSignal: measured signal (RPS or CPU) is below threshold,
	// or zero instances (cold path: no per-instance signal yet).
	OutcomeNoSignal Outcome = "no_signal"
)

// AppStore is the read-only slice of state.Store the trigger needs.
// Defined as an interface so tests can inject a fake without spinning
// up Postgres.
//
// Phase 2 / Gate A: ListAppsByNodeID is the per-schedd slice the
// scale-up trigger calls. Every schedd only scales apps it owns;
// the trigger's ownerNodeID is plumbed in via WithOwnerNodeID at
// wiring time. ListAllApps remains for the legacy one-box schedd
// posture (the default-local-only fleet) where the scaleup trigger
// was wired before this PR.
type AppStore interface {
	ListAllApps(ctx context.Context) ([]state.App, error)
	ListAppsByNodeID(ctx context.Context, nodeID string) ([]state.App, error)
}

// Ledger is the read-only slice of NodeLedger the trigger needs.
// Concurrency returns the number of instances of appID counting toward
// its plan cap (pkg/sched/admission.go:304).
type Ledger interface {
	Concurrency(appID string) int
}

// Engine is the slice of sched.Engine the trigger needs. AdmitInstance
// performs the admission; the typed AdmitResult.AtCapacity=true signals
// the cap rejection path. The signature intentionally avoids importing
// sched (see AdmitResult's doc comment for the cycle rationale).
type Engine interface {
	// AdmitInstance (PR-B / issue #272): scope is the preview
	// scope (`pr-{N}`) forwarded to the underlying sched.Engine.
	// Empty = prod (legacy).
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

// PromScraper is the per-app RPS signal source. Production impl
// scrapes gatewayd-internal's /metrics for `gateway_requests_total`. Returns
// the per-app request count for the most recent reading; the trigger
// maintains the per-app deltas in its ring buffer. The
// returned map is app_id → count (lifetime since gatewayd-internal last booted).
type PromScraper interface {
	Scrape(ctx context.Context) (map[string]int64, error)
}

// PromScraperFunc is the func adapter pattern (mirrors the
// HeartbeatDialerFunc precedent from PR #122) so callers can construct
// a PromScraper from a closure without a custom struct.
type PromScraperFunc func(ctx context.Context) (map[string]int64, error)

// Scrape implements PromScraper.
func (f PromScraperFunc) Scrape(ctx context.Context) (map[string]int64, error) {
	return f(ctx)
}

// InstatsReader is the per-instance CPU and inflight signal source
// (PR #205 + PR-C, issue #462). May be nil at runtime — the trigger
// falls back to RPS-only mode and ignores the CPU / inflight
// targets. The interface is the minimal surface the trigger needs;
// the real reader in pkg/sched/instancestats has a richer
// SnapshotForApp signature.
type InstatsReader interface {
	// MaxCPU returns the max CPU% observed across live instances of
	// appID in the most recent snapshot. Returns (0, false) when no
	// snapshot is available yet (cold path) or when the reader is
	// not configured for this app.
	MaxCPU(appID string) (pct float64, ok bool)
	// MaxInflightForApp (PR-C, issue #462) returns the max in-flight
	// ForwardHTTP count across live instances of appID in the most
	// recent snapshot. Returns (0, false) when no rows exist for
	// the app. The pkg/sched/targets trigger consumes this to
	// compare against ScalingPolicy.Target.Value for the
	// concurrent_requests axis. The scaleup trigger itself does
	// not currently consult it (PR-C did not add a
	// concurrent_requests scale-up axis to scaleup — that lives in
	// pkg/sched/targets); the interface widening is the only
	// shared surface so callers can wire a single reader into both
	// triggers.
	MaxInflightForApp(appID string) (n int64, ok bool)
}

// RequestRateReader is an optional provider-independent RPS signal. The
// concrete instancestats.Reader derives it from VMMD's cumulative activity
// counter, so split-box and bare-metal deployments do not need to expose a
// gateway metrics listener across node boundaries.
type RequestRateReader interface {
	RequestsPerSecond(appID string) (rps float64, ok bool)
}

// AppStats is the snapshot of inputs the pure decide() function
// reads. Splitting this out keeps decide() trivially testable — no
// mocks, no goroutines, no engine. The trigger's Tick assembles an
// AppStats per app and dispatches to decide.
type AppStats struct {
	AppID          string
	TargetRPS      int     // 0 = no RPS target
	TargetCPU      int     // 0 = no CPU target
	MaxConcurrency int     // plan cap
	Concurrency    int     // live instances counting toward the cap
	PerInstanceRPS float64 // measured, 0 when no RPS signal
	PerInstanceCPU float64 // measured, 0 when no CPU signal
	HaveCPU        bool    // true iff InstatsReader returned a sample
	HaveRPS        bool    // true iff a current RPS signal is available
}

// Decision is the result of the pure decide() function. The trigger
// converts this into a metric observation + an Engine.AdmitInstance
// call (when ShouldAdmit is true).
type Decision struct {
	ShouldAdmit bool
	Outcome     Outcome
	// Headroom is the number of additional instances that fit under
	// the cap. Pre-computed for the metric path; unused on the
	// reject_at_cap / no_signal branches.
	Headroom int
	// ObservedRPS is the per-instance RPS at decision time. Used to
	// feed the scale_up_admit_rps histogram on the admit branch.
	ObservedRPS float64
	// Desired is the estimated resident-instance count, capped at
	// MaxConcurrency. Admissions is the number of new instances this
	// decision should request, before the per-tick burst bound.
	Desired    int
	Admissions int
}

// decide is the pure decision function. Extracted so tests can drive
// every branch combination without spinning up a trigger. Inputs are
// the AppStats snapshot; outputs are a Decision. The function is
// total — every (target, signal, headroom) combination maps to
// exactly one outcome.
//
// Rules:
//   - No target set (both 0) → no_signal, no admit (the consumer
//     filters these out before calling, but the function is defensive).
//   - Target met on RPS (when configured) OR on CPU (when configured
//     and HaveCPU) → admit when headroom > 0, else reject_at_cap.
//   - Target not met on either configured signal → no_signal.
//
// "Target met" means measured > target. The strict > matters: a
// target of exactly 50 with measured 50 should NOT admit — the
// instance is performing exactly at the threshold, the next request
// can ride on it. > 50 means the next request would push the
// instance over the target, so admit.
func decide(s AppStats) Decision {
	if s.TargetRPS == 0 && s.TargetCPU == 0 {
		return Decision{Outcome: OutcomeNoSignal}
	}
	rpsHot := s.TargetRPS > 0 && s.HaveRPS && s.PerInstanceRPS > float64(s.TargetRPS)
	cpuHot := s.TargetCPU > 0 && s.HaveCPU && s.PerInstanceCPU > float64(s.TargetCPU)
	if !rpsHot && !cpuHot {
		return Decision{Outcome: OutcomeNoSignal}
	}
	headroom := s.MaxConcurrency - s.Concurrency
	if headroom <= 0 {
		return Decision{Outcome: OutcomeRejectAtCap, Headroom: 0}
	}
	desired := s.Concurrency + 1
	if rpsHot && s.Concurrency > 0 {
		// The ring stores total app RPS while the decision compares
		// per-instance RPS. Reconstruct total demand and ceil so a
		// fractional remainder gets capacity.
		desired = int(math.Ceil((s.PerInstanceRPS * float64(s.Concurrency)) / float64(s.TargetRPS)))
	}
	// CPU is a saturation signal rather than a capacity measurement,
	// so it safely contributes one additional instance when no
	// stronger RPS estimate is available. When both signals are hot,
	// retain the larger estimate.
	if cpuHot && desired < s.Concurrency+1 {
		desired = s.Concurrency + 1
	}
	if desired > s.MaxConcurrency {
		desired = s.MaxConcurrency
	}
	if desired <= s.Concurrency {
		desired = s.Concurrency + 1
		if desired > s.MaxConcurrency {
			desired = s.MaxConcurrency
		}
	}
	return Decision{
		ShouldAdmit: true,
		Outcome:     OutcomeAdmit,
		Headroom:    headroom,
		ObservedRPS: s.PerInstanceRPS,
		Desired:     desired,
		Admissions:  desired - s.Concurrency,
	}
}

// Trigger is the per-app scale-up trigger worker. Constructed via
// New(); the only public method is Tick(), which is intended to be
// driven by schedd's Loop on a 1s ticker.
type Trigger struct {
	appStore    AppStore
	instats     InstatsReader
	promScraper PromScraper
	engine      Engine
	ledger      Ledger
	metrics     *wire.OpsMetrics
	log         *slog.Logger
	interval    time.Duration

	// ownerNodeID is the durable Phase 2 / Gate A shard key this
	// schedd scales. Set via WithOwnerNodeID after construction
	// (the schedd's startup sequence resolves its local
	// compute_nodes.id after the trigger is wired). Empty = legacy
	// one-box posture: Tick reads ListAllApps (the synthetic
	// default-local-only fleet).
	ownerNodeID string

	// per-app ring buffer of per-app request deltas. Pre-allocated
	// in New(); Touch is called on every Tick with the new scrape.
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
	// api.ScaleUpDecisionIntervalSeconds (1s).
	Interval time.Duration
}

// New constructs the trigger. Any of appStore, instats, promScraper,
// engine, and ledger may be nil — every nil is handled defensively in
// Tick (the trigger no-ops on that path). This is the load-bearing
// property that lets schedd wire the trigger before every downstream
// dependency is fully online.
func New(appStore AppStore, instats InstatsReader, scraper PromScraper, engine Engine, ledger Ledger, opts Options) *Trigger {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Interval <= 0 {
		opts.Interval = api.ScaleUpDecisionIntervalSeconds * time.Second
	}
	return &Trigger{
		appStore:         appStore,
		instats:          instats,
		promScraper:      scraper,
		engine:           engine,
		ledger:           ledger,
		metrics:          opts.Metrics,
		log:              opts.Logger,
		interval:         opts.Interval,
		ring:             NewRingBuffer(5, time.Second, opts.Interval),
		admissionBackoff: make(map[string]admissionBackoffState),
	}
}

// Interval returns the tick rate. schedd's loop uses this when
// constructing the ticker so the cadence is owned by the trigger.
func (t *Trigger) Interval() time.Duration {
	if t == nil {
		return 0
	}
	return t.interval
}

// WithOwnerNodeID stamps the Phase 2 / Gate A owner shard key on
// the trigger. Called by cmd/schedd after ResolveLocalNodeID
// completes; the trigger then routes Tick's app read through
// ListAppsByNodeID (the per-schedd slice) rather than ListAllApps
// (the legacy one-box slice). Safe to call concurrently with Tick:
// reads of ownerNodeID race only with the initial stamp and the
// one-tick-delayed fallback to ListAllApps on the very first tick
// is benign — schedd's loop starts after WithOwnerNodeID is set,
// so in practice the race never fires.
//
// Empty string is allowed (legacy posture): the trigger falls
// back to ListAllApps so a single-box install without
// FAAS_NODE_NAME keeps working bit-for-bit.
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
		return burst.AdmitInstances(ctx, appID, "", wakeBootTriggerScaleup, count)
	}
	// An older adapter has no safe way to carry the burst continuation
	// marker, so preserve its original one-admission behavior.
	result, err := t.engine.AdmitInstance(ctx, appID, "", wakeBootTriggerScaleup)
	if err != nil {
		return nil, err
	}
	return []AdmitResult{result}, nil
}

// Tick runs one sweep. It is the single public entry point the schedd
// loop calls. Returns nil on success; errors are logged inside the
// loop (the trigger never aborts the loop on a transient store
// outage).
//
// The trigger is read-only on the apps table and the ledger; the only
// side effect is the Engine.AdmitInstance call on the admit branch
// and the metric observations. AdmitInstance is the same path the
// gateway uses on a request-driven wake, so the trigger cannot
// bypass the cap.
func (t *Trigger) Tick(ctx context.Context) error {
	if t == nil || t.appStore == nil {
		return nil
	}
	// Feed the ring buffer one fresh scrape. Keep the set of apps present
	// in this scrape separately from the ring: an old ring observation must
	// not mask a current scrape failure or make a disappeared app look like
	// a fresh Prometheus signal.
	var promApps map[string]struct{}
	if t.promScraper != nil {
		counts, err := t.promScraper.Scrape(ctx)
		if err != nil {
			// Degraded mode: log once-ish (slog.Warn is cheap
			// enough at 1Hz), keep the previous ring buffer. The
			// trigger will produce no_signal on every app this
			// tick, which is safe — the gateway still serves
			// requests on the existing instances.
			t.log.Warn("scaleup: scrape failed", "err", err)
		} else {
			t.ring.Touch(time.Now(), counts)
			promApps = make(map[string]struct{}, len(counts))
			for appID := range counts {
				promApps[appID] = struct{}{}
			}
		}
	}
	// Phase 2 / Gate A: per-schedd slice. ownerNodeID set via
	// WithOwnerNodeID at schedd startup → ListAppsByNodeID
	// (apps_node_id_idx, O(apps-on-this-node)); empty (single-box
	// posture) → ListAllApps (the legacy read path).
	var apps []state.App
	var err error
	if t.ownerNodeID != "" {
		apps, err = t.appStore.ListAppsByNodeID(ctx, t.ownerNodeID)
	} else {
		apps, err = t.appStore.ListAllApps(ctx)
	}
	if err != nil {
		return fmt.Errorf("scaleup: list apps: %w", err)
	}
	for _, app := range apps {
		// Autoscale is "enabled" iff at least one target is set.
		// No target → skip. Spec is explicit: no separate boolean
		// (per user direction).
		if app.AutoscaleTargetRPS == 0 && app.AutoscaleTargetCPUPct == 0 {
			continue
		}
		conc := 0
		if t.ledger != nil {
			conc = t.ledger.Concurrency(app.ID)
		}
		// Per-instance RPS = windowed requests/second / conc.
		// When conc=0 (cold path: no instances yet), the rollup
		// is undefined; we treat HaveRPS=false so the trigger
		// fires no_signal. The first instant wake that lands an
		// instance will be picked up by the next tick.
		var perInstRPS float64
		haveRPS := false
		if conc > 0 {
			_, promAvailable := promApps[app.ID]
			if promAvailable && t.ring.HasObservation(app.ID) {
				rps := t.ring.AppRate(app.ID, time.Now())
				perInstRPS = rps / float64(conc)
				haveRPS = true
			}
		}
		if conc > 0 && !haveRPS {
			if reader, ok := t.instats.(RequestRateReader); ok {
				if rps, ok := reader.RequestsPerSecond(app.ID); ok {
					perInstRPS = rps / float64(conc)
					haveRPS = true
				}
			}
		}
		var perInstCPU float64
		haveCPU := false
		if t.instats != nil {
			if pct, ok := t.instats.MaxCPU(app.ID); ok {
				perInstCPU = pct
				haveCPU = true
			}
		}
		stats := AppStats{
			AppID:          app.ID,
			TargetRPS:      app.AutoscaleTargetRPS,
			TargetCPU:      app.AutoscaleTargetCPUPct,
			MaxConcurrency: app.MaxConcurrency,
			Concurrency:    conc,
			PerInstanceRPS: perInstRPS,
			PerInstanceCPU: perInstCPU,
			HaveCPU:        haveCPU,
			HaveRPS:        haveRPS,
		}
		dec := decide(stats)
		// Always emit the decision metric so the rate of
		// no_signal vs admit is observable.
		t.metrics.ObserveScaleUp(app.ID, string(dec.Outcome))
		if !dec.ShouldAdmit {
			continue
		}
		// Admit path: request the desired number of instances, bounded
		// by ScaleUpMaxBurstPerTick. The engine enforces the cap via
		// NodeLedger.Admit; if it is hit between the decide() check and
		// a batch attempt, the corresponding result is AtCapacity.
		if t.engine == nil {
			continue
		}
		if t.admissionBackoffActive(app.ID, time.Now()) {
			continue
		}
		// Scale-out must use AdmitInstance, not EnsureWake. EnsureWake is
		// deliberately idempotent: its Phase-1 fast path returns an
		// existing RUNNING instance. Using it here makes a hot app with
		// one running VM stay at one VM forever, even when decide() found
		// headroom. AdmitInstance skips that fast path and reserves a new
		// capacity slot through the shared ledger.
		results, err := t.admit(ctx, app.ID, dec.Admissions)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			t.recordAdmissionFailure(app.ID, time.Now())
			// Defensive: do not surface this as a hard
			// error to the loop (the next tick can
			// retry). Log + skip.
			t.log.Warn("scaleup: admit failed", "app", app.ID, "err", err)
		} else {
			t.clearAdmissionBackoff(app.ID)
		}
		for _, result := range results {
			if result.AtCapacity {
				// The ledger can reach the cap between decide() and
				// an individual batch attempt. Preserve the decision
				// metric and record the effective rejection.
				t.metrics.ObserveScaleUp(app.ID, string(OutcomeRejectAtCap))
				continue
			}
			// Successful admit: observe the per-instance RPS at
			// decision time so the dashboard can p95/p99 the
			// trigger's aggressiveness.
			t.metrics.ObserveScaleUpAdmitRPS(dec.ObservedRPS)
		}
	}
	return nil
}
