package floor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// wakeBootTrigger* mirror sched.TriggerFloor / TriggerFloorDep
// (ADR-123). Defined locally to avoid the floor → sched import
// cycle (see AdmitResult below). The string values are the canonical
// wake-boot trigger enum entries and must stay in lockstep with
// pkg/sched/triggers.go.
const (
	wakeBootTriggerFloor    = "floor"
	wakeBootTriggerFloorDep = "floor.deployment"
)

// AdmitResult is the typed wake-subset the trigger needs from the
// engine. Mirrors sched.WakeResult's AtCapacity / InstanceID fields
// without importing the sched package (which would create a cycle:
// floor → sched → floor via loop.go). The concrete *sched.Engine
// returns AdmitResult{AtCapacity, InstanceID} via a thin adapter
// constructed in cmd/schedd, mirroring schedTargetsEngine at
// cmd/schedd/main.go:1177-1197.
type AdmitResult struct {
	InstanceID string
	AtCapacity bool
}

// WakeOutcome (ADR-098) is the trigger's local projection of
// sched.CoordOutcome. AtCapacity is dropped because the per-app
// leader's ledger already enforces max_concurrency and the trigger
// observes the at-capacity path via the bus, not the return value.
type WakeOutcome struct {
	InstanceID string
	WakeID     string
	ColdBoot   bool
}

// Outcome is the closed set of floor-reconcile decision outcomes.
// Pre-instantiated in pkg/wire.NewOpsMetrics so the counter rows
// surface in /metrics from boot. Adding a new outcome requires
// extending that loop too.
type Outcome string

const (
	// OutcomeAdmit: ledger.Concurrency < floor and Engine.AdmitInstance
	// returned a live instance id. The trigger increments
	// floorInstancesAdmittedTotal so an operator can alarm on a
	// sustained-zero condition (the customer's floor isn't being
	// satisfied).
	OutcomeAdmit Outcome = "admit"
	// OutcomeFloorMet: ledger.Concurrency(appID) >= floor. No-op
	// (the floor is satisfied). This is the natural idempotency —
	// the trigger does NOT need a bypass flag because PARKED does
	// not count toward CountsForConcurrency (pkg/state/machine.go).
	OutcomeFloorMet Outcome = "floor_met"
	// OutcomeDisabled: floor==0, the plan gate is off, or the app
	// is worker-class. No-op.
	OutcomeDisabled Outcome = "disabled"
	// OutcomeAtCapacity: concurrency >= plan MaxConcurrency, OR
	// Engine.AdmitInstance returned AtCapacity=true. The engine
	// already rejected; the trigger records the outcome and moves
	// on.
	OutcomeAtCapacity Outcome = "at_capacity"
	// OutcomeRamCeiling: the §6.2-2 47,600 MB ceiling would be
	// crossed by admitting this app. The trigger yields to live
	// wakes; the next tick (or the next ResidentRAM scrape)
	// reconsiders.
	OutcomeRamCeiling Outcome = "ram_ceiling"
	// OutcomeCooldownHeld: the app's last_scale_out_at is within
	// ScaleOutCooldownS (mirrors pkg/sched/engine.go isOnScaleOutCooldown).
	// The trigger skips and waits for the cooldown window to elapse.
	OutcomeCooldownHeld Outcome = "cooldown_held"
	// OutcomeError: Engine.AdmitInstance returned a non-nil error
	// OTHER than context.Canceled. The trigger records the failure
	// in the per-app backoff and emits the
	// floor_reconcile_errors_total counter.
	OutcomeError Outcome = "error"
	// OutcomeBackoffHeld: per-app backoff is in effect; the trigger
	// skips without calling the engine.
	OutcomeBackoffHeld Outcome = "backoff_held"
)

// AppStore is the read-only slice of state.Store the trigger needs.
// Mirrors pkg/sched/scaleup.AppStore (with the per-schedd
// ListAppsByNodeID slice for Tier A owner sharding).
type AppStore interface {
	ListAllApps(ctx context.Context) ([]state.App, error)
	ListAppsByNodeID(ctx context.Context, nodeID string) ([]state.App, error)
}

// DeploymentStore is the per-deployment slice the trigger needs
// (issue #557 closure / ADR-072). The per-deployment floor sweep
// walks ListAllDeployments / ListDeploymentsByNodeID, reads the
// parent app's floor via AppByID, and admits via
// Engine.AdmitInstanceForDeployment.
type DeploymentStore interface {
	ListAllDeployments(ctx context.Context) ([]state.Deployment, error)
	ListDeploymentsByNodeID(ctx context.Context, nodeID string) ([]state.Deployment, error)
	AppByID(ctx context.Context, id string) (state.App, error)
	ConcurrencyForDeployment(ctx context.Context, appID, deploymentID string) (int, error)
}

// Ledger is the read-only slice of NodeLedger the trigger needs.
// Concurrency counts toward max_concurrency; ResidentRAM and
// HeadroomMB back the §6.2-2 ceiling pre-check.
// ConcurrencyForDeployment backs the per-deployment floor arithmetic
// (issue #557 closure / ADR-072).
type Ledger interface {
	Concurrency(appID string) int
	ConcurrencyForDeployment(appID, deploymentID string) int
	ResidentRAM() int
	HeadroomMB() int
}

// Engine is the slice of sched.Engine the trigger needs. AdmitInstance
// performs the full admission (per-app cap, cooldown, min-floor gate,
// RAM ceiling §6.2-2, vCPU) — the caller does NOT pre-check.
// AdmitInstanceForDeployment is the per-deployment entry point
// used by the per-deployment sweep (issue #557 closure / ADR-072);
// its `deploymentID` is required, not optional.
type Engine interface {
	// AdmitInstance / AdmitInstanceForDeployment (PR-B / issue #272):
	// scope is the preview scope (`pr-{N}`) forwarded to the
	// underlying sched.Engine. Empty = prod (legacy). trigger
	// (ADR-127) is the wake-boot trigger enum value stamped on the
	// emitted wake.boot_started / wake.boot_completed events.
	AdmitInstance(ctx context.Context, appID, scope, trigger string) (AdmitResult, error)
	AdmitInstanceForDeployment(ctx context.Context, appID, deploymentID, scope, trigger string) (AdmitResult, error)
	// EnsureWake (ADR-098): the single-flight wake entry. The trigger
	// routes through this so a floor tick racing the gateway, cron,
	// scaleup, or targets triggers on the same parked app coalesces
	// into one virtual boot. The WakeOutcome is the trigger's local
	// projection of sched.CoordOutcome (kept in this package so we
	// don't import pkg/sched from pkg/sched/floor — would be a cycle).
	// trigger (ADR-127) is forwarded to the leader's Engine.Wake call.
	EnsureWake(ctx context.Context, appID, trigger string) (WakeOutcome, error)
}

// Auditor is the seam the trigger uses to emit `floor.wake` audit
// events on every successful admit. nil is safe (no-ops). Mirrors
// pkg/audit.Auditor.Emit (cmd/schedd wires a concrete instance).
type Auditor interface {
	Emit(ctx context.Context, kind string, accountID *string, data map[string]any)
}

// PlanResolver returns the customer's plan for floor gate / cap
// arithmetic. nil is safe — the trigger treats a nil resolver as
// "plan gate off" and reads MaxMinInstances from acct.Plan via the
// resolved value. In production cmd/schedd wires
// state.App.AccountID → pkg/state.AppByID → plan lookup.
type PlanResolver interface {
	ResolvePlan(ctx context.Context, accountID string) (api.Plan, bool)
}

// AppStats is the input to the pure decide() function. Tick
// populates this struct per-app; the table-driven decide_test.go
// drives it directly. Keeping the input a typed struct (rather
// than multiple positional args) lets the test rig cover every
// branch compactly.
type AppStats struct {
	AppID             string
	AccountID         string
	Plan              api.Plan
	Floor             int // EffectiveMinInstances (max of column + jsonb)
	Concurrency       int // ledger.Concurrency
	MaxConcurrency    int // plan MaxConcurrency
	ResidentRAMMB     int // ledger.ResidentRAM at tick time
	HeadroomMB        int // ledger.HeadroomMB at tick time
	RAMMB             int // app.RAMMB
	WorkloadClass     state.WorkloadClass
	LastScaleOutAt    time.Time // zero = never stamped
	ScaleOutCooldownS int       // 0 = no cooldown
	Now               time.Time
	BackoffUntil      time.Time // zero = not in backoff
	IsRamCeiling      bool      // pre-computed: would admit breach HeadroomMB
}

// Decision is the typed result of decide(). Outcome must always be
// set; the other fields are populated only when relevant.
type Decision struct {
	Outcome Outcome
	// AdmitNow is true exactly when decide() is ready to call
	// Engine.AdmitInstance.
	AdmitNow bool
}

// decide is the pure decision function. Every (Floor, Concurrency,
// Plan, Class, Cooldown, Ceiling) tuple maps to exactly one Outcome.
// Keeping the function pure lets decide_test.go cover all branches
// without spinning up Postgres or a ledger.
func decide(s AppStats) Decision {
	// Floor disabled: floor==0, plan gate off, worker class.
	if s.Floor <= 0 {
		return Decision{Outcome: OutcomeDisabled}
	}
	if !s.Plan.MinInstancesAllowed() {
		return Decision{Outcome: OutcomeDisabled}
	}
	if s.WorkloadClass == state.WorkloadClassWorker {
		return Decision{Outcome: OutcomeDisabled}
	}
	// Floor satisfied: nothing to do.
	if s.Concurrency >= s.Floor {
		return Decision{Outcome: OutcomeFloorMet}
	}
	// Per-app cap: plan MaxConcurrency already reached.
	if s.Concurrency >= s.MaxConcurrency {
		return Decision{Outcome: OutcomeAtCapacity}
	}
	// Scale-out cooldown in effect (mirrors pkg/sched/engine.go
	// isOnScaleOutCooldown). LastScaleOutAt zero = never stamped,
	// which bypasses the cooldown. Concurrency > 0 discriminator is
	// load-bearing in the engine (lets cold-start wakes bypass
	// cooldown); for the floor trigger concurrency is always >= 1
	// here (we just checked conc < floor and floor >= 1), so the
	// conc > 0 check is implicit.
	if !s.LastScaleOutAt.IsZero() && s.ScaleOutCooldownS > 0 {
		cooldown := time.Duration(s.ScaleOutCooldownS) * time.Second
		if s.Now.Sub(s.LastScaleOutAt) < cooldown {
			return Decision{Outcome: OutcomeCooldownHeld}
		}
	}
	// Per-app backoff: previous AdmitInstance errored; sleep until
	// next probe. This is the load-bearing defense against the
	// FAILED-row hazard on a RAM-saturated box.
	if !s.BackoffUntil.IsZero() && s.Now.Before(s.BackoffUntil) {
		return Decision{Outcome: OutcomeBackoffHeld}
	}
	// §6.2-2 RAM ceiling pre-check: yield to live wakes. Engine's
	// NodeLedger.Admit is unchanged and remains the absolute
	// backstop; the trigger's pre-check is the policy layer above.
	if s.IsRamCeiling {
		return Decision{Outcome: OutcomeRamCeiling}
	}
	return Decision{Outcome: OutcomeAdmit, AdmitNow: true}
}

// backoffEntry is the per-app state the trigger tracks. Mutations
// are guarded by Trigger.mu (the package-level sync.Mutex).
type backoffEntry struct {
	attempts    int
	nextRetryAt time.Time
}

// Trigger is the per-app min-instances floor reconciler. Constructed
// via New(); the only public methods are Tick, Interval, and
// WithOwnerNodeID.
type Trigger struct {
	appStore        AppStore
	deploymentStore DeploymentStore
	ledger          Ledger
	engine          Engine
	auditor         Auditor
	planResolver    PlanResolver
	metrics         *wire.OpsMetrics
	log             *slog.Logger
	interval        time.Duration
	ownerNodeID     string

	// mu guards the per-app backoff map. Read paths in Tick take
	// the lock once per app, holding it just long enough to copy
	// the nextRetryAt value into the AppStats struct; write paths
	// (recordSuccess / recordFailure) likewise take it briefly.
	mu      sync.Mutex
	backoff map[string]*backoffEntry
}

// Options is the functional-options bag for New(). All fields are
// optional; zero-values fall back to sane defaults.
type Options struct {
	Metrics      *wire.OpsMetrics
	Logger       *slog.Logger
	Interval     time.Duration
	Auditor      Auditor
	PlanResolver PlanResolver
}

// New constructs the trigger. Any of appStore, ledger, engine may
// be nil — Tick handles each defensively (the trigger no-ops on
// that path). This is the load-bearing property that lets schedd
// wire the trigger before every downstream dependency is fully
// online.
//
// deploymentStore is optional: when nil, the trigger falls back to
// the per-app-only walk (pre-#557 posture) and ignores per-deployment
// floors. The per-deployment axis is the issue #557 closure / ADR-072
// addition; the one-box deploy shape that doesn't carry per-deployment
// floors can pass nil and keep the legacy behaviour.
func New(appStore AppStore, deploymentStore DeploymentStore, ledger Ledger, engine Engine, opts Options) *Trigger {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Interval <= 0 {
		opts.Interval = api.FloorDecisionIntervalSeconds * time.Second
	}
	return &Trigger{
		appStore:        appStore,
		deploymentStore: deploymentStore,
		ledger:          ledger,
		engine:          engine,
		auditor:         opts.Auditor,
		planResolver:    opts.PlanResolver,
		metrics:         opts.Metrics,
		log:             opts.Logger,
		interval:        opts.Interval,
		backoff:         map[string]*backoffEntry{},
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

// WithOwnerNodeID stamps the Phase 2 / Gate A owner shard key.
// Mirrors scaleup.Trigger.WithOwnerNodeID. Empty string falls back
// to ListAllApps for the legacy one-box posture.
func (t *Trigger) WithOwnerNodeID(nodeID string) {
	if t == nil {
		return
	}
	t.ownerNodeID = nodeID
}

// observe is a nil-receiver-safe metric emitter. Mirrors
// ObserveScaleUp at pkg/wire/metrics.go.
func (t *Trigger) observe(app string, outcome Outcome) {
	if t == nil || t.metrics == nil {
		return
	}
	t.metrics.ObserveFloor(app, string(outcome))
}

// observeError is a nil-receiver-safe error counter emitter.
// kind ∈ {"admit_denied", "admit_error"}; mirrors
// IncFloorReconcileError.
func (t *Trigger) observeError(app, kind string) {
	if t == nil || t.metrics == nil {
		return
	}
	t.metrics.IncFloorReconcileError(app, kind)
}

// incAdmitted increments the global "floor wake succeeded" counter.
// Mirrors IncFloorInstanceAdmitted.
func (t *Trigger) incAdmitted() {
	if t == nil || t.metrics == nil {
		return
	}
	t.metrics.IncFloorInstanceAdmitted()
}

// peekBackoff returns the nextRetryAt for an app, or zero. Cheap;
// takes the lock briefly.
func (t *Trigger) peekBackoff(appID string) time.Time {
	if t == nil {
		return time.Time{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.backoff[appID]; ok {
		return e.nextRetryAt
	}
	return time.Time{}
}

// recordSuccess clears the backoff for an app.
func (t *Trigger) recordSuccess(appID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.backoff, appID)
}

// recordFailure advances the per-app backoff exponentially, capped
// at api.MaxFloorBackoffSeconds (60 s). Mirrors the targets
// trigger's per-app cooldown (PR-C, issue #462).
func (t *Trigger) recordFailure(appID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.backoff[appID]
	if !ok {
		e = &backoffEntry{}
		t.backoff[appID] = e
	}
	e.attempts++
	if e.attempts > 6 {
		e.attempts = 6
	}
	delay := time.Duration(1<<e.attempts) * time.Second
	maxDelay := api.MaxFloorBackoffSeconds * time.Second
	if delay > maxDelay {
		delay = maxDelay
	}
	e.nextRetryAt = time.Now().Add(delay)
}

// Tick runs one sweep. Single public entry point schedd's loop
// calls. Returns nil on success; errors are logged inside the loop
// (the trigger never aborts the loop on a transient store outage).
//
// The trigger is read-only on the apps + deployments tables; the
// only side effect is the Engine.AdmitInstance / AdmitInstanceForDeployment
// call on the admit branch and the metric observations. AdmitInstance
// is the same path the gateway uses on a request-driven wake, so
// the trigger cannot bypass the cap.
//
// Issue #557 closure / ADR-072 — the sweep walks the deployment list
// when deploymentStore is wired. The unit of admission is now the
// (app, deployment) pair; the per-app axis (apps with no deployment
// floor) is honored via the floor-met shortcut (every deployment
// at floor → app is at floor). The per-app shortcut is preserved
// for legacy callers that don't wire deploymentStore.
func (t *Trigger) Tick(ctx context.Context) error {
	if t == nil {
		return nil
	}
	// Per-deployment walk is the issue #557 closure path. Legacy
	// callers (no deploymentStore wired) fall through to the per-app
	// walk below.
	if t.deploymentStore != nil {
		return t.tickPerDeployment(ctx)
	}
	return t.tickPerApp(ctx)
}

// tickPerDeployment is the per-(app, deployment) floor sweep. For
// each deployment, compute the effective floor =
// `max(app.EffectiveMinInstances(), d.EffectiveMinInstances())` and
// admit up to that floor.
func (t *Trigger) tickPerDeployment(ctx context.Context) error {
	var deps []state.Deployment
	var err error
	if t.ownerNodeID != "" {
		deps, err = t.deploymentStore.ListDeploymentsByNodeID(ctx, t.ownerNodeID)
	} else {
		deps, err = t.deploymentStore.ListAllDeployments(ctx)
	}
	if err != nil {
		return fmt.Errorf("floor: list deployments: %w", err)
	}
	now := time.Now()
	var residentRAM, headroom int
	if t.ledger != nil {
		residentRAM = t.ledger.ResidentRAM()
		headroom = t.ledger.HeadroomMB()
	}
	for _, d := range deps {
		// A configured floor is serviceable only after the deployment is
		// live. ListAllDeployments intentionally includes historical and
		// in-flight rows, so guard here before resolving the parent app or
		// asking Engine to admit. Otherwise every failed/superseded row
		// inherits app.min_instances and produces an impossible wake on each
		// reconciliation tick.
		if d.Status != state.DeployLive {
			t.observe(d.AppID, OutcomeDisabled)
			continue
		}
		app, appErr := t.deploymentStore.AppByID(ctx, d.AppID)
		if appErr != nil {
			t.observe(d.AppID, OutcomeError)
			continue
		}
		effective := app.EffectiveMinInstances()
		if dFloor := d.EffectiveMinInstances(); dFloor > effective {
			effective = dFloor
		}
		if effective <= 0 {
			t.observe(d.AppID, OutcomeDisabled)
			continue
		}
		plan := api.PlanFree
		if t.planResolver != nil {
			if resolved, ok := t.planResolver.ResolvePlan(ctx, app.AccountID); ok {
				plan = resolved
			}
		}
		if !plan.MinInstancesAllowed() {
			t.observe(d.AppID, OutcomeDisabled)
			continue
		}
		if app.WorkloadClass == state.WorkloadClassWorker {
			t.observe(d.AppID, OutcomeDisabled)
			continue
		}
		var conc int
		if t.ledger != nil {
			conc = t.ledger.ConcurrencyForDeployment(d.AppID, d.ID)
		}
		if conc >= effective {
			t.observe(d.AppID, OutcomeFloorMet)
			continue
		}
		if conc >= effectiveMaxConcurrency(app, plan) {
			t.observe(d.AppID, OutcomeAtCapacity)
			continue
		}
		// RAM ceiling pre-check (same as tickPerApp).
		isRamCeiling := false
		if t.ledger != nil && api.BillableRAMMB(app.RAMMB) > headroom {
			isRamCeiling = true
		}
		var lastScaleOut time.Time
		if app.LastScaleOutAt != nil {
			lastScaleOut = *app.LastScaleOutAt
		}
		stats := AppStats{
			AppID:             d.AppID,
			AccountID:         app.AccountID,
			Plan:              plan,
			Floor:             effective,
			Concurrency:       conc,
			MaxConcurrency:    effectiveMaxConcurrency(app, plan),
			ResidentRAMMB:     residentRAM,
			HeadroomMB:        headroom,
			RAMMB:             app.RAMMB,
			WorkloadClass:     app.WorkloadClass,
			LastScaleOutAt:    lastScaleOut,
			ScaleOutCooldownS: scalingOutCooldownS(app),
			Now:               now,
			BackoffUntil:      t.peekBackoff(d.AppID),
			IsRamCeiling:      isRamCeiling,
		}
		decision := decide(stats)
		t.observe(d.AppID, decision.Outcome)
		if !decision.AdmitNow {
			continue
		}
		result, err := t.engine.AdmitInstanceForDeployment(ctx, d.AppID, d.ID, "", wakeBootTriggerFloorDep)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			t.recordFailure(d.AppID)
			t.observeError(d.AppID, "admit_error")
			t.log.Warn("floor: per-deployment admit error",
				"app", d.AppID, "deployment", d.ID, "err", err)
			continue
		}
		t.recordSuccess(d.AppID)
		if result.AtCapacity {
			t.observe(d.AppID, OutcomeAtCapacity)
			continue
		}
		t.incAdmitted()
		if t.auditor != nil {
			acctID := app.AccountID
			// Dual-emit (ADR-072 §Decision 6): the issue AC names
			// `instances.warmed_min_instances`; the original PR #618
			// shipped `floor.wake`. Both kinds are emitted for one
			// release as a compat layer. The follow-up PR drops
			// `floor.wake`.
			payload := map[string]any{
				"app_id":             d.AppID,
				"deployment_id":      d.ID,
				"floor":              effective,
				"concurrency_before": conc,
				"wake_id":            result.InstanceID,
			}
			t.auditor.Emit(ctx, "instances.warmed_min_instances", &acctID, payload)
			t.auditor.Emit(ctx, "floor.wake", &acctID, payload)
		}
	}
	return nil
}

// tickPerApp is the legacy per-app-only walk (issue #557 PR #618).
// Preserved for callers that don't wire deploymentStore; the unit of
// admission is the app, not the (app, deployment) pair.
func (t *Trigger) tickPerApp(ctx context.Context) error {
	if t.appStore == nil {
		return nil
	}
	var apps []state.App
	var err error
	if t.ownerNodeID != "" {
		apps, err = t.appStore.ListAppsByNodeID(ctx, t.ownerNodeID)
	} else {
		apps, err = t.appStore.ListAllApps(ctx)
	}
	if err != nil {
		return fmt.Errorf("floor: list apps: %w", err)
	}
	now := time.Now()
	var residentRAM, headroom int
	if t.ledger != nil {
		residentRAM = t.ledger.ResidentRAM()
		headroom = t.ledger.HeadroomMB()
	}
	for _, app := range apps {
		floor := app.EffectiveMinInstances()
		if floor <= 0 {
			t.observe(app.ID, OutcomeDisabled)
			continue
		}
		plan := api.PlanFree
		if t.planResolver != nil {
			if resolved, ok := t.planResolver.ResolvePlan(ctx, app.AccountID); ok {
				plan = resolved
			}
		}
		if !plan.MinInstancesAllowed() {
			t.observe(app.ID, OutcomeDisabled)
			continue
		}
		if app.WorkloadClass == state.WorkloadClassWorker {
			t.observe(app.ID, OutcomeDisabled)
			continue
		}
		conc := 0
		if t.ledger != nil {
			conc = t.ledger.Concurrency(app.ID)
		}
		isRamCeiling := false
		if t.ledger != nil && api.BillableRAMMB(app.RAMMB) > headroom {
			isRamCeiling = true
		}
		var lastScaleOut time.Time
		if app.LastScaleOutAt != nil {
			lastScaleOut = *app.LastScaleOutAt
		}
		stats := AppStats{
			AppID:             app.ID,
			AccountID:         app.AccountID,
			Plan:              plan,
			Floor:             floor,
			Concurrency:       conc,
			MaxConcurrency:    effectiveMaxConcurrency(app, plan),
			ResidentRAMMB:     residentRAM,
			HeadroomMB:        headroom,
			RAMMB:             app.RAMMB,
			WorkloadClass:     app.WorkloadClass,
			LastScaleOutAt:    lastScaleOut,
			ScaleOutCooldownS: scalingOutCooldownS(app),
			Now:               now,
			BackoffUntil:      t.peekBackoff(app.ID),
			IsRamCeiling:      isRamCeiling,
		}
		decision := decide(stats)
		t.observe(app.ID, decision.Outcome)
		if !decision.AdmitNow {
			continue
		}
		result, err := t.engine.EnsureWake(ctx, app.ID, wakeBootTriggerFloor)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			t.recordFailure(app.ID)
			t.observeError(app.ID, "admit_error")
			t.log.Warn("floor: admit error",
				"app", app.ID,
				"err", err)
			continue
		}
		t.recordSuccess(app.ID)
		t.incAdmitted()
		if t.auditor != nil {
			acctID := app.AccountID
			t.auditor.Emit(ctx, "floor.wake", &acctID, map[string]any{
				"app_id":             app.ID,
				"floor":              floor,
				"concurrency_before": conc,
				"wake_id":            result.InstanceID,
			})
		}
	}
	return nil
}

// effectiveMaxConcurrency returns the per-app max concurrency,
// clamping legacy / pre-PR-A apps whose MaxConcurrency is 0 against
// the plan ceiling. Mirrors pkg/sched/engine.go admitGate clamp.
func effectiveMaxConcurrency(app state.App, plan api.Plan) int {
	limits, ok := api.LimitsFor(plan)
	planMax := 0
	if ok {
		planMax = limits.MaxConcurrency
	}
	if app.MaxConcurrency <= 0 || app.MaxConcurrency > planMax {
		return planMax
	}
	return app.MaxConcurrency
}

// scalingOutCooldownS returns the per-app scale-out cooldown in
// seconds, or 0 when ScalingPolicy is nil / cooldown disabled.
func scalingOutCooldownS(app state.App) int {
	if app.ScalingPolicy == nil {
		return 0
	}
	return app.ScalingPolicy.ScaleOutCooldownS
}
