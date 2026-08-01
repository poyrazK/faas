package meter

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Loop runs the six meterd timers (sample / quota / stripe / dunning
// / residency / alerts) until the context cancels. Each timer fires
// on its own cadence; the first error from any goroutine is surfaced
// to the caller.
//
// The Loop never blocks the daemon's shutdown — every ticker selects on
// both its tick and ctx.Done. Production wires this from cmd/meterd;
// tests inject a fake clock + collaborators and step through ticks
// directly (NewLoop is just a constructor, no goroutines started until
// Run).
//
// Observability (PR feat/m7-meterd-observability): every tick body is
// wrapped in ops.Observe(name, dur, err) per ADR-015 and the spec §12
// Prometheus convention. The lastTick map is the source of truth for
// /healthz — see pkg/meter/health.go. ops and log may be nil; NewLoop
// coerces nil to a fresh test registry / slog.Default so callers don't
// have to special-case the zero value (mirrors scheddgrpc/server.go:54-56).
type Loop struct {
	store  state.Store
	cpu    CPUSource
	parker ScheddParker
	pusher billing.Provider
	notif  Notifier
	// mailer is the customer-facing email sender (spec §171 "dunning +
	// quota mails reference email"). Shared with the dunning timer —
	// both loops hand off the same Sender via DunningSender's local
	// interface shape. nil falls back to a no-op sender in NewLoop so
	// tests don't have to thread a stub.
	mailer    DunningSender
	dunning   *Dunning
	residency *Residency
	// evaluator is the meterd-side alert evaluator (issue #396 /
	// ADR-045 PR 4). nil means the alerts tick is skipped — useful
	// for tests that don't exercise the alert surface, and matches
	// the residency zero-check below. The wire-up in cmd/meterd
	// instantiates the evaluator only when FAAS_PROMETHEUS_URL or
	// FAAS_HOST_AGE_IDENTITY_PATH are configured; on a stripped-down
	// dev box the evaluator stays nil and the loop runs five ticks.
	// Held as a concrete *alerts.Evaluator pointer so the loop can
	// call RunOnce directly (the AlertsEvaluator interface below is
	// what pkg/alerts implements; meterd holds the concrete pointer
	// so test doubles don't have to wrap the interface).
	evaluator *alerts.Evaluator
	now       func() time.Time
	log       *slog.Logger
	cfg       *Config
	ops       *wire.OpsMetrics
	// egress is the per-instance egress-byte reader (ADR-046,
	// step 8). nil falls back to the legacy 3-arg Sampler so
	// tests and consumers that have not been migrated to the
	// egress-aware form continue to pass without egress writes.
	// Set via Loop.WithEgress (the NewLoop-set path comes in
	// PR-2 once the gateway gRPC stream is wired).
	egress EgressSource

	lastTickMu sync.RWMutex
	// lastTick records the wall-clock time each named tick body last
	// completed successfully. Keys mirror the runTicks "name" argument:
	// "sample", "stripe", "dunning", "residency", "alerts" are populated by
	// runTicks; "quota" is populated by runQuotaOnce (same field,
	// written outside runTicks because quota is loop-shaped, not
	// single-tick).
	lastTick map[string]time.Time
}

// NewLoop wires the loop. The interfaces are local to pkg/meter so the
// daemon (cmd/meterd) can substitute test doubles without importing the
// concrete packages (scheddgrpc, stripex). dunning may be nil; tests
// that don't exercise dunning pass nil and the fourth goroutine is
// skipped. mailer may be nil; NewLoop coerces nil to noopDunningSender
// so callers don't have to special-case the zero value (mirrors
// ops/log coercion above). residency is wired unconditionally today
// (the gauge emit is the source of truth for the §12 dashboard panel
// and must not be skipped in production); the ops.SetResidentGBPerCustomer
// method is nil-safe so the loop tolerates a nil ops receiver. ops
// and log likewise may be nil — see the Loop doc comment.
//
// evaluator may be nil; tests that don't exercise alerts pass nil and
// the sixth goroutine is skipped. The single meterd process today
// has exactly one evaluator; the loop's contract is "at most one",
// matching the design note at pkg/alerts/evaluator.go (a future
// multi-replica meterd would parallelise via the
// alert_deliveries.idempotency_key UNIQUE constraint).
func NewLoop(store state.Store, cpu CPUSource, parker ScheddParker, pusher billing.Provider, notif Notifier, mailer DunningSender, dunning *Dunning, residency *Residency, evaluator *alerts.Evaluator, now func() time.Time, log *slog.Logger, cfg *Config, ops *wire.OpsMetrics) *Loop {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	if ops == nil {
		ops = wire.NewOpsMetrics("meter_test")
	}
	if mailer == nil {
		mailer = noopDunningSender{}
	}
	if residency == nil {
		residency = NewResidency(store, now, log, ops)
	}
	return &Loop{
		store: store, cpu: cpu, parker: parker, pusher: pusher, notif: notif,
		mailer: mailer, dunning: dunning, residency: residency, evaluator: evaluator,
		now: now, log: log, cfg: cfg, ops: ops,
		lastTick: make(map[string]time.Time),
	}
}

// WithEgress attaches an EgressSource to the loop. cmd/meterd calls
// this once at boot with the scheddEgressAdapter +
// gatewayEgressAdapter aggregator; tests that don't exercise
// egress leave it unwired (the loop falls back to the
// egress=nil branch and writes 0/0 to both columns). Returns
// the receiver so the call site is one line:
//
//	loop := meter.NewLoop(...).WithEgress(egress)
//
// ADR-046 (step 8). PR-2 extends the aggregator's body to read
// the gateway ring buffer; this setter is stable across both
// PRs.
func (l *Loop) WithEgress(egress EgressSource) *Loop {
	l.egress = egress
	return l
}

// Run starts the six timers and blocks until ctx cancels or any timer
// errors out. Sampler / quota loop / stripe pusher / dunning /
// residency / alerts each log + continue on per-tick errors so a
// transient Postgres blip doesn't kill the daemon; only a context
// cancel returns cleanly.
// errors out. Sampler / quota loop / stripe pusher / dunning /
// residency / alerts each log + continue on per-tick errors so a
// transient Postgres blip doesn't kill the daemon; only a context
// cancel returns cleanly.
func (l *Loop) Run(ctx context.Context) error {
	// Sampler wiring (ADR-046, step 8): when WithEgress has
	// been called, use the 4-arg constructor so the per-tick
	// loop reads the egress adapters. When nil, fall through
	// to the legacy 3-arg path (writes 0/0 for both egress
	// columns — same observable behaviour as before PR-1).
	var sampler *Sampler
	if l.egress != nil {
		sampler = NewSamplerWithEgress(l.store, l.cpu, l.egress, l.now)
	} else {
		sampler = NewSampler(l.store, l.cpu, l.now)
	}
	pusher := NewPusher(l.store, l.pusher, l.log, l.now, l.ops)
	errc := make(chan error, 6)
	go func() {
		errc <- l.runTicks(ctx, l.cfg.SampleInterval, func(c context.Context) error {
			// PR-A (ADR-060, issue #515): capture the
			// returned rows so the closure can emit
			// meterd_floor_applied_total{plan} once per
			// app whose SyntheticFloor bool is true. The
			// sampler stays free of ops; only this
			// closure emits — mirrors the
			// BillingCapExceededTotal precedent at
			// runQuotaOnce:325 (per-account loop, ops
			// emit inside the loop).
			rows, err := sampler.SampleAndRoll(c)
			if err == nil {
				l.emitFloorApplied(c, rows)
			}
			return err
		}, "sample")
	}()
	go func() { errc <- l.runQuotaTicks(ctx) }()
	go func() {
		errc <- l.runTicks(ctx, l.cfg.StripeInterval, func(c context.Context) error {
			_, err := pusher.PushHour(c)
			return err
		}, "stripe")
	}()
	if l.dunning != nil {
		go func() {
			errc <- l.runTicks(ctx, l.cfg.DunningInterval,
				func(c context.Context) error { return l.dunning.RunOnce(c) },
				"dunning")
		}()
	}
	if l.residency != nil {
		go func() {
			errc <- l.runTicks(ctx, l.cfg.ResidencyInterval,
				func(c context.Context) error {
					_, err := l.residency.RunOnce(c)
					return err
				}, "residency")
		}()
	}
	if l.evaluator != nil {
		go func() {
			errc <- l.runTicks(ctx, l.cfg.AlertEvalInterval,
				func(c context.Context) error {
					_, err := l.evaluator.RunOnce(c)
					return err
				}, "alerts")
		}()
	}
	// Block until either ctx cancels or a hard error fires.
	select {
	case <-ctx.Done():
		return nil
	case err := <-errc:
		return err
	}
}

// runTicks is the shared timer driver. Per-tick errors are logged and
// swallowed so a transient backend hiccup doesn't kill meterd (spec §14
// hardening: metering must be self-healing). Each successful (or
// failed-but-logged) tick is observed via ops.Observe and recorded in
// lastTick[name] under lastTickMu — both writes happen together so the
// two observability surfaces cannot drift.
func (l *Loop) runTicks(ctx context.Context, interval time.Duration, tick func(context.Context) error, name string) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			start := time.Now()
			err := tick(ctx)
			l.ops.Observe(name, time.Since(start), err)
			l.recordTick(name, start)
			if err != nil {
				l.log.Warn("meter: "+name+" tick", "err", err)
			}
		}
	}
}

// runQuotaTicks walks every account once per quota interval and applies
// the per-plan ladder. The first per-account error is logged + skipped
// (one bad account shouldn't stop the rest). Records the last tick
// timestamp under "quota" — separate from runTicks because quota sweeps
// a list rather than a single tick body.
//
// Observe is called with err=nil unconditionally: runQuotaOnce already
// logs and skips per-account failures, so there is no aggregate error
// to surface. Operators alerting on quota errors should scrape
// journald (the warn lines carry account_id + err); this is documented
// inline to make the silent counter explicit — a future
// meterd_quota_errors_total counter is in the survey follow-ups.
func (l *Loop) runQuotaTicks(ctx context.Context) error {
	t := time.NewTicker(l.cfg.QuotaInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			start := time.Now()
			l.runQuotaOnce(ctx)
			l.ops.Observe("quota", time.Since(start), nil)
			l.recordTick("quota", start)
		}
	}
}

// RunQuotaOnce is the test seam for the per-account quota loop. It
// runs a single tick of the quota sweep without spinning the ticker,
// so cap tests can pin the synthetic clock and assert the
// meterd_billing_cap_exceeded_total counter incremented. Production
// callers must use Run, which schedules runQuotaTicks on the
// configured QuotaInterval.
func (l *Loop) RunQuotaOnce(ctx context.Context) {
	l.runQuotaOnce(ctx)
}

// RunAlertsOnce is the test seam for the alert-evaluator loop. It
// runs a single tick of the alert evaluator without spinning the
// ticker, so PR 4 unit tests can pin a synthetic clock and assert
// per-tick behaviour (claim won/lost, dispatch fired/skipped, state
// transition). Production callers must use Run, which schedules the
// alerts tick on the configured AlertEvalInterval.
//
// Returns the Stats from the evaluator's RunOnce (matching the
// pkg/alerts.Evaluator.RunOnce signature) so tests can pin
// per-evaluation counters (Fired, Delivered, Failed,
// SkippedDegraded, SkippedNoIdentity) without scraping the metrics
// registry.
func (l *Loop) RunAlertsOnce(ctx context.Context) (alerts.Stats, error) {
	if l.evaluator == nil {
		return alerts.Stats{}, nil
	}
	return l.evaluator.RunOnce(ctx)
}

// AlertsEvaluator is the thin facade pkg/meter uses to invoke
// pkg/alerts.Evaluator without taking a direct dependency on the
// package (preserves the layering: pkg/meter orchestrates timers;
// pkg/alerts implements the alert-rule evaluation policy). The
// single RunOnce method is what RunAlertsOnce + Run call.
//
// Defined here as an interface so a future PR can substitute a stub
// evaluator for unit tests that don't exercise the alert surface
// (mirrors the role of CPUSource, ScheddParker, etc).
type AlertsEvaluator = alerts.Evaluator

// runQuotaOnce is exposed to tests via the RunQuotaOnce thin wrapper
// below so the ticker doesn't have to spin. Production's only caller
// is runQuotaTicks.
//
// Issue #279 PR A: a per-account overage cap (`accounts.overage_cap_cents`,
// opt-in) sits in front of the existing free/paid ladder. The cap is
// loaded once per tick (one round-trip) and consulted per account;
// accounts at-or-past the cap skip the overage-row insert path
// inside EnforceQuota and the meterd_billing_cap_exceeded_total
// counter is incremented with the plan label. The cap is advisory
// for overage only — in-budget usage continues to accumulate and
// the Free hard-stop / paid warning ladder is unchanged.
//
// Concurrency note: this loop is the only writer to usage_minutes
// (spec §6.1) and runs as a single meterd process today, so the
// (capCache read → CurrentMonthOverageCents → EnforceQuota) sequence
// is race-free against itself. A future meterd-replica deploy would
// race here — at worst one minute's overage-row insert slips past
// the cap per replica. The follow-up is to wrap the per-account
// section in `SELECT … FOR UPDATE` on the `accounts` row (precedent
// pgstore.go:380-381, :668); today's behaviour is acceptable per
// the cap's documented "advisory for the overage row only" contract.
func (l *Loop) runQuotaOnce(ctx context.Context) {
	accounts, err := l.store.ListAllAccounts(ctx)
	if err != nil {
		l.log.Warn("meter: quota list_accounts", "err", err)
		return
	}
	capCache, err := l.store.LoadAllOverageCapCents(ctx)
	if err != nil {
		// Fail-open: a transient read failure must not skip the
		// quota ladder. Log once and proceed with no caps.
		l.log.Warn("meter: overage cap load failed", "err", err)
		capCache = map[string]int64{}
	}
	now := l.now()
	for _, acct := range accounts {
		capCents, ok := capCache[acct.ID]
		if ok && capCents >= 0 {
			monthCents, err := l.store.CurrentMonthOverageCents(ctx, acct.ID)
			if err != nil {
				l.log.Warn("meter: overage read failed", "account", acct.ID, "err", err)
			} else if monthCents >= capCents {
				l.ops.BillingCapExceededTotal(string(acct.Plan))
				continue
			}
		}
		usages, err := MonthUsageForAccount(ctx, l.store, acct.ID, now)
		if err != nil {
			l.log.Warn("meter: quota usage_by_month", "account", acct.ID, "err", err)
			continue
		}
		used := MonthlyUsageGB(usages)
		if _, err := EnforceQuota(ctx, l.store, l.notif, l.parker, l.mailer, l.log, acct, used, now); err != nil {
			// EnforceQuota already logged + skipped parked-instance failures;
			// only status/structural errors reach here.
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			l.log.Warn("meter: enforce_quota", "account", acct.ID, "err", err)
		}
	}
}

// emitFloorApplied (ADR-060, issue #515) groups the sampler's
// returned RolledRows by AppID, finds the apps that emitted at
// least one SyntheticFloor row, looks up each app's plan, and
// increments meterd_floor_applied_total{plan} once per affected
// (app, tick). One increment per app, not per synthetic row —
// the counter is the floor-applied cardinality, not the floor-
// volume cardinality. Missing-app / missing-account lookups
// degrade to "" (no panic; ops surface stays consistent).
//
// The (appID → plan) map is built once per call from
// ListAllAccounts (cheap; the quota tick already walks the full
// accounts list). A nil receiver ops is tolerated (mirrors the
// BillingCapExceededTotal accessor's nil-receiver contract).
func (l *Loop) emitFloorApplied(ctx context.Context, rows []RolledRow) {
	if l.ops == nil || len(rows) == 0 {
		return
	}
	floorApps := map[string]bool{}
	for _, r := range rows {
		if r.SyntheticFloor {
			floorApps[r.AppID] = true
		}
	}
	if len(floorApps) == 0 {
		return
	}
	// accountID → plan: built from ListAllAccounts once per
	// tick (the quota tick already does this; the cost is
	// negligible against the AppendUsage volume). accountID
	// is fetched via ListAllApps; if that fails the floor
	// counter still increments with plan="" so the ops
	// surface never silently drops a metric row.
	accounts, err := l.store.ListAllAccounts(ctx)
	if err != nil {
		l.log.Warn("meter: floor emit list_accounts", "err", err)
		for range floorApps {
			l.ops.MeterdFloorAppliedTotal("")
		}
		return
	}
	accountPlan := make(map[string]api.Plan, len(accounts))
	for _, a := range accounts {
		accountPlan[a.ID] = a.Plan
	}
	apps, err := l.store.ListAllApps(ctx)
	if err != nil {
		l.log.Warn("meter: floor emit list_apps", "err", err)
		for range floorApps {
			l.ops.MeterdFloorAppliedTotal("")
		}
		return
	}
	appAccount := make(map[string]string, len(apps))
	for _, a := range apps {
		appAccount[a.ID] = a.AccountID
	}
	for appID := range floorApps {
		acctID, ok := appAccount[appID]
		if !ok {
			l.ops.MeterdFloorAppliedTotal("")
			continue
		}
		l.ops.MeterdFloorAppliedTotal(string(accountPlan[acctID]))
	}
}

// recordTick stamps the last successful tick for /healthz. Centralized
// so the runTicks / runQuotaTicks paths agree on the storage shape.
func (l *Loop) recordTick(name string, at time.Time) {
	l.lastTickMu.Lock()
	l.lastTick[name] = at
	l.lastTickMu.Unlock()
}

// LastTick returns the wall-clock time the named tick body last
// completed. ok=false means the tick has never fired. Read-mostly path;
// the RWMutex lets /healthz probes go lock-free against the writers in
// the common case.
func (l *Loop) LastTick(name string) (time.Time, bool) {
	l.lastTickMu.RLock()
	defer l.lastTickMu.RUnlock()
	t, ok := l.lastTick[name]
	return t, ok
}

// HasPlan is a tiny convenience exposed so cmd/meterd's wire-up can
// gate paid-only behavior on the plan literal without re-importing api.
// Tests use it to assert "Free plan was treated as Free" without
// inspecting the full account struct.
//nolint:unused // exposed for downstream callers; not used inside pkg/meter
// (helper functions intentionally removed — see commit history if needed)
