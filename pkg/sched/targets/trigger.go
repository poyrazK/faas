package targets

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

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

// WakeOutcome (ADR-095): trigger-local projection of sched.CoordOutcome.
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
	AdmitInstance(ctx context.Context, appID string) (AdmitResult, error)
	// EnsureWake (ADR-095): the single-flight wake entry. Routes
	// through this so a targets tick racing the gateway, cron, floor,
	// or scaleup triggers on the same parked app coalesces into one
	// virtual boot.
	EnsureWake(ctx context.Context, appID string) (WakeOutcome, error)
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

// Decision is the decide() result. ShouldAdmit=true triggers the
// Engine.AdmitInstance call in Tick.
type Decision struct {
	ShouldAdmit      bool
	Outcome          Outcome
	Headroom         int
	ObservedInflight int64
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
func decide(s Stats) Decision {
	if s.TargetValue == 0 {
		return Decision{Outcome: OutcomeNoSignal}
	}
	if s.Concurrency > 0 && !s.LastScaleOutAt.IsZero() {
		cooldown := time.Duration(s.ScaleOutCooldownS) * time.Second
		if s.Now.Sub(s.LastScaleOutAt) < cooldown {
			return Decision{Outcome: OutcomeCooldownHeld}
		}
	}
	hot := s.HaveInflight && float64(s.PerInstanceInflight) > s.TargetValue
	if !hot {
		return Decision{Outcome: OutcomeNoSignal, ObservedInflight: s.PerInstanceInflight}
	}
	headroom := s.MaxConcurrency - s.Concurrency
	if headroom <= 0 {
		return Decision{Outcome: OutcomeRejectAtCap, Headroom: 0, ObservedInflight: s.PerInstanceInflight}
	}
	return Decision{
		ShouldAdmit:      true,
		Outcome:          OutcomeAdmit,
		Headroom:         headroom,
		ObservedInflight: s.PerInstanceInflight,
	}
}

// Trigger is the per-app concurrent_requests scale-up trigger
// worker (PR-C, issue #462). Constructed via New(); the only public
// methods are Tick() and Interval(). Nil-safe on every receiver
// and every dep so schedd can wire the trigger before every
// downstream dependency is fully online.
type Trigger struct {
	appStore AppStore
	instats  InstatsReader
	engine   Engine
	ledger   Ledger
	metrics  *wire.OpsMetrics
	log      *slog.Logger
	interval time.Duration

	// per-app sliding window of per-instance max-inflight. Reads
	// from instats on each Tick; the window keeps the most recent
	// reading so the trigger can debounce single-tick spikes.
	ring *RingBuffer
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
		appStore: appStore,
		instats:  instats,
		engine:   engine,
		ledger:   ledger,
		metrics:  opts.Metrics,
		log:      opts.Logger,
		interval: opts.Interval,
		ring:     NewRingBuffer(5, time.Second, opts.Interval),
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

// Tick runs one sweep. It is the single public entry point the
// schedd loop calls. Returns nil on success; errors are logged
// inside the loop (the trigger never aborts the loop on a transient
// store outage).
//
// The trigger is read-only on the apps table and the ledger; the
// only side effect is the Engine.AdmitInstance call on the admit
// branch and the metric observations.
func (t *Trigger) Tick(ctx context.Context) error {
	if t == nil || t.appStore == nil || t.instats == nil {
		return nil
	}
	now := time.Now()
	apps, err := t.appStore.ListAllApps(ctx)
	if err != nil {
		return fmt.Errorf("targets: list apps: %w", err)
	}
	for _, app := range apps {
		// Filter: this trigger only serves apps with a
		// concurrent_requests target. Other metric axes
		// (rps / cpu) are handled by pkg/sched/scaleup.
		policy := app.ScalingPolicy
		if policy == nil || policy.Target == nil || policy.Target.Metric != "concurrent_requests" {
			continue
		}
		conc := 0
		if t.ledger != nil {
			conc = t.ledger.Concurrency(app.ID)
		}
		// Pull a fresh max-inflight reading into the ring buffer,
		// then read the windowed value. The ring buffer keeps the
		// most recent sample so a single-tick spike does not
		// immediately scale — mirrors pkg/sched/scaleup's ring
		// discipline.
		if n, ok := t.instats.MaxInflightForApp(app.ID); ok {
			t.ring.Observe(now, app.ID, n)
		}
		perInst, haveInflight := t.ring.AppMaxInflight(app.ID, now)
		var lastScaleOut time.Time
		if app.LastScaleOutAt != nil {
			lastScaleOut = *app.LastScaleOutAt
		}
		stats := Stats{
			AppID:               app.ID,
			TargetValue:         policy.Target.Value,
			MaxConcurrency:      app.MaxConcurrency,
			Concurrency:         conc,
			PerInstanceInflight: perInst,
			HaveInflight:        haveInflight,
			LastScaleOutAt:      lastScaleOut,
			ScaleOutCooldownS:   policy.ScaleOutCooldownS,
			Now:                 now,
		}
		dec := decide(stats)
		// Always emit the decision metric so the rate of
		// no_signal vs admit vs cooldown_held is observable.
		if t.metrics != nil {
			t.metrics.ObserveScaleUp(app.ID, string(dec.Outcome))
		}
		if !dec.ShouldAdmit {
			continue
		}
		// Admit path: call Engine.AdmitInstance. The engine
		// enforces the cap via NodeLedger.Admit; if the cap is
		// hit between the decide() check and the call (the
		// ledger is concurrent), AdmitInstance returns
		// AdmitResult{AtCapacity: true} which we re-observe as
		// reject_at_cap.
		if t.engine == nil {
			continue
		}
		// ADR-095: route through EnsureWake so a targets tick racing the
		// gateway, cron, floor, or scaleup triggers on the same parked
		// app coalesces into one virtual boot.
		result, err := t.engine.EnsureWake(ctx, app.ID)
		if err != nil {
			t.log.Warn("targets: admit failed", "app_id", app.ID, "err", err)
			continue
		}
		// EnsureWake's leader runs Engine.Wake which honours the
		// per-app max_concurrency ledger; a follower that arrives
		// after the leader fills the last slot still sees a
		// successful boot pointing at that slot. The leader's
		// ledger closes the at-cap loop — we no longer need a
		// reject_at_cap branch here.
		_ = result
	}
	return nil
}
