// Package alerts is the meterd-side evaluator that fires customer-
// configured alert rules (issue #396 / ADR-045). The package is
// deliberately split off pkg/meter so pkg/meter stays a pure tick
// scheduler and the alert logic can be unit-tested without spinning
// the full daemon timer set.
//
// The Evaluator is the single goroutine that wakes every
// FAAS_ALERT_EVAL_INTERVAL (default 60 s), walks
// `state.Store.ListEnabledAlertRules`, fetches the metric for each
// rule via pkg/appmetrics (Prometheus) or pkg/state.CountFailedInvocationsSince
// (Postgres), and dispatches a signed webhook via pkg/webhookout when
// the comparison fires.
//
// # Concurrency model
//
// Single meterd process today → no two evaluators race on
// ClaimAlertFire per rule. A future meterd-replica deploy relies on
// the `alert_deliveries.idempotency_key` UNIQUE constraint as the
// load-bearing dedupe (migrations/00062). Documented in ADR-045.
//
// pkg/webhookout.Dispatcher is concurrency-unsafe (see the type's
// doc comment); the evaluator serialises calls via a sync.Mutex
// owned by the daemon wiring so a future replica that distributes
// evaluation across multiple goroutines still respects the
// dispatcher's contract.
package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// AlertSecretNamespace is the secretbox namespace stamped onto every
// alert-rule webhook secret (PR 3). The evaluator asserts this on
// open so a stale seal from a previous namespace tag (defensive
// future-proofing — only one namespace ships today) fails closed
// instead of being silently treated as a live alert secret.
const AlertSecretNamespace = "alert_rule"

// Store is the narrow Store surface Evaluator depends on. Defined
// here so pkg/alerts does not import pkg/state's full 200+ method
// surface and so unit tests can stub it directly with a small fake.
//
// The methods mirror state.Store with identical semantics; the only
// reason they aren't `state.Store` is so we don't drag in
// pkg/state's full interface when the only methods the evaluator
// touches are these nine.
type Store interface {
	ListEnabledAlertRules(ctx context.Context) ([]state.AlertRule, error)
	AlertRuleByID(ctx context.Context, id string) (state.AlertRule, error)
	CountFailedInvocationsSince(ctx context.Context, accountID, appID string, source state.InvocationSource, since time.Time) (int, error)
	// Issue #1233 / ADR-123 — 5 new metric cases learn these:
	CountFailedDeploymentsSince(ctx context.Context, accountID, appID string, since time.Time) (int, error)
	WasInvokedSuccessfullySince(ctx context.Context, accountID, appID string, since time.Time) (bool, error)
	MTDSpendEurCents(ctx context.Context, accountID string) (int64, error)
	MinCertExpiryForApp(ctx context.Context, accountID, appID string) (int64, error)
	ClaimAlertFire(ctx context.Context, ruleID, idempotencyKey string, payload []byte, observed float64, at time.Time) (deliveryID string, won bool, err error)
	SetAlertRuleState(ctx context.Context, ruleID string, to state.AlertState, at time.Time) (changed bool, err error)
	SetAlertRuleLastEvaluated(ctx context.Context, ruleID string, at time.Time) error
	RecordAlertDelivery(ctx context.Context, d state.AlertDelivery) (state.AlertDelivery, error)
	UpdateAlertDeliveryStatus(ctx context.Context, id string, status state.AlertDeliveryStatus, attempt int, statusCode int, lastErr string, deliveredAt *time.Time) error
	ListAlertDeliveriesForRule(ctx context.Context, ruleID string, limit int, includeTest bool) ([]state.AlertDelivery, error)
}

// Dispatcher is the narrow surface the evaluator calls. The
// production pkg/webhookout.Dispatcher satisfies it directly; the
// evaluator's unit tests inject a recording fake.
type Dispatcher interface {
	Dispatch(ctx context.Context, target webhookout.Target, evt webhookout.Event) webhookout.Result
}

// ActionExecutor is the narrow surface the evaluator calls when an
// alert rule's action column ≠ 'webhook'. The production impl is
// pkg/safedeploy.ActionDispatcher (commit 5 of SAFE-RELEASES Mega
// #2); unit tests inject a recording fake that captures
// (rule, observed, at) for assertion.
//
// The fan-out boundary is intentional: pkg/alerts has zero
// dependency on pkg/safedeploy (the safe-deploy orchestrator would
// re-introduce the meterd → apid writer race the evaluator was
// designed to avoid). The action surface here is the only seam
// that crosses the boundary, and Execute is the only method — the
// evaluator never calls pkg/safedeploy.Orchestrator directly.
//
// Concurrency: the evaluator serialises Execute behind the same
// dispatchMu that guards Dispatch. pkg/safedeploy.ActionDispatcher
// is concurrency-safe today (its only side-effect is a single HTTP
// call into apid), but the lock keeps the contract honest if a
// future fan-out becomes multi-goroutine.
type ActionExecutor interface {
	Execute(ctx context.Context, rule state.AlertRule, observed float64, at time.Time) error
}

// Ops is the narrow counter surface the evaluator increments.
// Defined here so the unit tests can pass a stub; the production
// pkg/wire.OpsMetrics satisfies it.
type Ops interface {
	AlertEvalSkippedDegradedTotal() (inc func())
	AlertEvalFiredTotal() (inc func())
	AlertDeliveryAttemptsTotal(outcome string) (inc func())
	// AlertActionExecutedTotal (issue #976 / ADR-122 /
	// SAFE-RELEASES-B) bumps the in-process-action counter — the
	// 'fan-out beyond webhook' signal. Labelled by action ∈
	// {rollback, demote, promote}. Closed vocabulary; unknown
	// actions drop to a no-op closure.
	AlertActionExecutedTotal(action string) (inc func())
	// SetAlertEvaluatorEnabled stamps the alert-evaluator-enabled
	// gauge. The Evaluator calls this once at construction (and again
	// whenever the boot identity changes) so /healthz and the
	// §12 self-healing alert can tell "evaluator wired?" without
	// scraping the full metric set.
	SetAlertEvaluatorEnabled(enabled bool)
}

// Evaluator wires the dependencies RunOnce needs. The struct is
// intentionally small so a future PR can add per-rule filtering or
// per-account parallelism without reshaping the public surface.
type Evaluator struct {
	store      Store
	promQL     appmetrics.PromQL // nil = Prometheus unreachable; degraded path
	audit      *audit.Auditor
	identity   func() *age.X25519Identity // nil = meterd can't unseal; skip with warn
	identities func() []*age.X25519Identity
	dispatch   Dispatcher
	dispatchMu *sync.Mutex // serialises the concurrency-unsafe Dispatcher
	// actionExec (issue #976 / ADR-122 / SAFE-RELEASES-B) is the
	// in-process action fan-out surface. Nil-coerced at
	// construction so the evaluator's behaviour is the same on
	// pre-B meterd builds — a rule with action='webhook' (the
	// default) skips the call site entirely, and a rule with
	// action='rollback'/'demote'/'promote' just becomes a no-op
	// when actionExec is nil (log warn, Stats.ActionSkipped++).
	// Mutable via SetActionExec so cmd/meterd's two-tap wiring
	// (buildEvaluator → buildSafeDeployOrchestrator) doesn't
	// require a refactor to thread the value through every
	// constructor. Guarded by actionExecMu so a tick in flight
	// reads a coherent value.
	actionExecMu sync.RWMutex
	actionExec   ActionExecutor
	newSigner    func(secret []byte) *webhookout.Signer
	now          func() time.Time
	log          *slog.Logger
	ops          Ops
}

// SetActionExec (issue #976 / ADR-122 / SAFE-RELEASES-B) swaps
// the in-process action executor. cmd/meterd calls this once
// at startup, after building both the evaluator (which doesn't
// know about pkg/safedeploy) and the ActionDispatcher (which
// doesn't know about pkg/alerts). The setter is the seam
// between the two. A nil arg is honoured (clears the surface
// back to the webhook-only path).
//
// Concurrency: the setter takes the write lock; runAction
// reads under the read lock so a mid-tick swap is safe.
func (e *Evaluator) SetActionExec(ae ActionExecutor) {
	if e == nil {
		return
	}
	e.actionExecMu.Lock()
	e.actionExec = ae
	e.actionExecMu.Unlock()
}

// getActionExec reads the current ActionExecutor under the read
// lock. Returns nil if none is wired.
func (e *Evaluator) getActionExec() ActionExecutor {
	if e == nil {
		return nil
	}
	e.actionExecMu.RLock()
	ae := e.actionExec
	e.actionExecMu.RUnlock()
	return ae
}

// EvaluatorOptions is the constructor input. Every field has a
// non-zero default except store (the only required dependency).
type EvaluatorOptions struct {
	Store    Store
	PromQL   appmetrics.PromQL
	Audit    *audit.Auditor
	Identity func() *age.X25519Identity
	// Identities is the rotation-overlap accessor (issue #316 /
	// ADR-057): multi-identity slice from secretbox.LoadHostKeys(dir).
	// Pre-rotation: length 1 (just the current). During the 30-day
	// overlap window: length 2 ([current, previous]). nil means
	// "not wired" — the evaluator falls back to Identity for
	// backward compat.
	Identities func() []*age.X25519Identity
	Dispatcher Dispatcher
	// ActionExec (issue #976 / ADR-122 / SAFE-RELEASES-B) is the
	// in-process action fan-out. Nil = the surface is disabled;
	// rules with action='rollback'/'demote'/'promote' will be
	// log-warned and Stats.ActionSkipped will increment instead
	// of executing the side-effect. The meterd wiring is the
	// canonical site that sets this — the production impl is
	// pkg/safedeploy.ActionDispatcher.
	ActionExec ActionExecutor
	NewSigner  func(secret []byte) *webhookout.Signer
	Now        func() time.Time
	Log        *slog.Logger
	Ops        Ops
}

// NewEvaluator returns a wired Evaluator. nil-coerced dependencies
// collapse to safe no-op equivalents (mirrors the convention in
// pkg/meter.NewLoop) so a future cmd/meterd caller doesn't have to
// thread test doubles through every constructor.
func NewEvaluator(o EvaluatorOptions) *Evaluator {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.NewSigner == nil {
		o.NewSigner = webhookout.NewSigner
	}
	if o.Dispatcher == nil {
		o.Dispatcher = webhookout.NewDispatcher(webhookout.DispatcherOptions{})
	}
	e := &Evaluator{
		store:      o.Store,
		promQL:     o.PromQL,
		audit:      o.Audit,
		identity:   o.Identity,
		identities: o.Identities,
		dispatch:   o.Dispatcher,
		dispatchMu: &sync.Mutex{},
		newSigner:  o.NewSigner,
		now:        o.Now,
		log:        o.Log,
		ops:        o.Ops,
	}
	if o.ActionExec != nil {
		e.actionExec = o.ActionExec
	}
	// Stamp the alert-evaluator-enabled gauge at construction. The
	// gauge is "wired iff identity is non-nil" — without the host.age
	// identity, the evaluator can list rules but cannot unseal
	// webhook secrets, so it is operationally disabled (MED-4). A
	// future cmd/meterd that can swap the identity mid-process would
	// call SetAlertEvaluatorEnabled directly; today the boot-time
	// decision is final. Nil-coerced: unit tests that pass Ops=nil
	// (no metrics) keep working — the interface already nil-coerces
	// at the call sites.
	if e.ops != nil {
		e.ops.SetAlertEvaluatorEnabled(o.Identity != nil)
	}
	return e
}

// Stats is the per-tick observability summary. Counters in
// pkg/wire.OpsMetrics cover the dashboard panel; Stats is the
// per-tick contract RunOnce returns so tests can pin per-tick
// behaviour without scraping the registry.
type Stats struct {
	Evaluated         int // rules walked this tick (enabled list)
	Fired             int // rules that crossed the threshold
	Delivered         int // rules whose dispatch returned 2xx/3xx
	Failed            int // rules whose dispatch hit a terminal/retry-exhausted state
	SkippedDegraded   int // rules skipped because Prometheus returned a degraded source
	SkippedNoIdentity int // rules skipped because FAAS_HOST_AGE_IDENTITY_PATH was unset
	// ActionExecuted (issue #976 / ADR-122 / SAFE-RELEASES-B) is
	// the count of in-process actions (rollback / demote / promote)
	// that landed on the rule's target deployment. Distinct from
	// Delivered (which counts the legacy webhook fan-out).
	ActionExecuted int
	// ActionFailed counts in-process actions whose Execute
	// returned a non-nil error. Per CLAUDE.md ownership rules the
	// error is warn-logged + swallowed — the webhook path keeps
	// firing even when the action path is broken.
	ActionFailed int
	// ActionSkipped counts rules whose action='rollback'/'demote'/
	// 'promote' but ActionExec is nil (ActionExec was never wired
	// by cmd/meterd). The webhook fan-out still fires; the side-
	// effect just doesn't happen.
	ActionSkipped int
}

// RunOnce walks the enabled alert-rule list once and applies the
// per-rule evaluation. Returns Stats so callers can log per-tick
// summaries; the errors are warn-logged at the call site and never
// propagate (matching the convention in pkg/meter.Loop.runTicks —
// a transient backend hiccup must not kill meterd).
//
// The function is safe to call from a single goroutine. cmd/meterd
// wires one Evaluator per daemon.
func (e *Evaluator) RunOnce(ctx context.Context) (Stats, error) {
	var stats Stats
	now := e.now()

	rules, err := e.store.ListEnabledAlertRules(ctx)
	if err != nil {
		// Per-tick failures log + return; the loop's outer tick
		// driver records the err in lastTick + ops.Observe so the
		// operator can correlate.
		e.log.Warn("alerts: list enabled rules", "err", err)
		return stats, fmt.Errorf("alerts: list enabled: %w", err)
	}
	stats.Evaluated = len(rules)

	for i := range rules {
		rule := rules[i]
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		e.evalRule(ctx, rule, now, &stats)
	}
	return stats, nil
}

// evalRule evaluates a single rule. The body is split out so the
// top-level RunOnce loop reads as a sequence of intents without
// sprawling into the comparison / dispatch logic.
//
// Per-rule errors are warn-logged and swallowed (Stats surface the
// shape of the tick). The only error that propagates is a context
// cancellation — every other failure is observation, not load-
// bearing for the next tick.
func (e *Evaluator) evalRule(ctx context.Context, rule state.AlertRule, now time.Time, stats *Stats) {
	observed, comparisonResult, skipReason := e.observe(ctx, rule)
	if skipReason != "" {
		switch skipReason {
		case skipDegraded:
			stats.SkippedDegraded++
			if e.ops != nil {
				e.ops.AlertEvalSkippedDegradedTotal()()
			}
			e.log.Warn("alerts: eval skipped degraded source",
				"rule", rule.ID, "metric", string(rule.Metric))
		case skipNoIdentity:
			stats.SkippedNoIdentity++
			e.log.Warn("alerts: eval skipped no identity",
				"rule", rule.ID, "name", rule.Name)
		}
		// Stamp last_evaluated_at so the dashboard's "evaluated N
		// seconds ago" reads true even on a degraded tick. Failures
		// are warn-logged; we never block the next rule.
		if err := e.store.SetAlertRuleLastEvaluated(ctx, rule.ID, now); err != nil {
			e.log.Warn("alerts: stamp last_evaluated_at",
				"rule", rule.ID, "err", err)
		}
		return
	}

	if !comparisonResult {
		// Healthy tick — comparison false. If we were firing,
		// flip back to ok and emit audit.resolved.
		if rule.State == state.AlertStateFiring {
			changed, err := e.store.SetAlertRuleState(ctx, rule.ID, state.AlertStateOk, now)
			if err != nil {
				e.log.Warn("alerts: set state ok", "rule", rule.ID, "err", err)
			} else if changed && e.audit != nil {
				e.audit.Emit(ctx, "alert.resolved", &rule.AccountID, map[string]any{
					"rule_id": rule.ID,
					"rule":    rule.Name,
				})
			}
		}
		if err := e.store.SetAlertRuleLastEvaluated(ctx, rule.ID, now); err != nil {
			e.log.Warn("alerts: stamp last_evaluated_at",
				"rule", rule.ID, "err", err)
		}
		return
	}

	// Comparison fires. Build the cool-down bucket key.
	//
	// The bucket is `last_fired_at / cooldownSeconds` (when the rule
	// has fired before) or `now / cooldownSeconds` (first-ever fire
	// — last_fired_at is the zero time). The crucial invariant:
	// ClaimAlertFire re-stamps `last_fired_at` to `now`, so the
	// bucket key after a successful fire is `now /
	// cooldownSeconds`; subsequent ticks within the cooldown window
	// read the same stamped `last_fired_at` and compute the SAME
	// bucket key — UNIQUE collides, dedupe holds. Backdating
	// `last_fired_at` by 2× cooldown (cmd/e2e/meterd_alerts_e2e_test.go
	// Phase 3) shifts the bucket by exactly 2, landing the next
	// claim in a fresh key. The +1 from the previous revision is
	// gone: it was producing a different key on the first vs second
	// tick because last_fired_at advanced from zero to now in a
	// single step.
	cooldownSeconds := int64(rule.CooldownMinutes) * 60
	if cooldownSeconds <= 0 {
		cooldownSeconds = 60 // belt + braces; the schema defaults to ≥ 1
	}
	var bucketUnix int64
	if rule.LastFiredAt.IsZero() {
		bucketUnix = now.Unix()
	} else {
		bucketUnix = rule.LastFiredAt.Unix()
	}
	bucket := bucketUnix / cooldownSeconds
	idempotencyKey := rule.ID + ":" + strconv.FormatInt(bucket, 10)

	// Stamp last_evaluated_at up-front so a successful claim still
	// records "evaluated N seconds ago" if the dispatch path then
	// fails inside RecordAlertDelivery.
	if err := e.store.SetAlertRuleLastEvaluated(ctx, rule.ID, now); err != nil {
		e.log.Warn("alerts: stamp last_evaluated_at (fire)",
			"rule", rule.ID, "err", err)
	}

	// Marshal the webhook envelope ONCE so the dedupe row carries
	// the same payload we'll hand to the dispatcher. ClaimAlertFire
	// takes the bytes directly; the same bytes are re-decoded later
	// (webhookout's HTTP body) so we never re-serialise on the
	// dispatch hot path.
	payloadBytes, payloadMap, err := buildPayload(rule, observed)
	if err != nil {
		e.log.Warn("alerts: marshal payload", "rule", rule.ID, "err", err)
		return
	}

	deliveryID, won, err := e.store.ClaimAlertFire(ctx, rule.ID, idempotencyKey, payloadBytes, observed, now)
	if err != nil {
		// ErrNotFound is a TOCTOU race (rule deleted between
		// ListEnabledAlertRules and the claim) — log + skip.
		// Other errors are warn-logged + skipped so a transient
		// Postgres blip doesn't kill the tick.
		e.log.Warn("alerts: claim fire", "rule", rule.ID, "err", err)
		return
	}
	if !won {
		// Duplicate inside the cool-down bucket. Silent skip per
		// the contract at state.Store.ClaimAlertFire.
		return
	}
	stats.Fired++
	if e.ops != nil {
		e.ops.AlertEvalFiredTotal()()
	}

	// The alert_deliveries row is now created AND stamped with the
	// full envelope (payloadBytes + observed + fired_at) by
	// ClaimAlertFire itself — both PgStore and MemStore insert the
	// row inside their claim path (parity contract from PR #409
	// review). ClaimAlertFire returns the new row's UUID so we can
	// transition it to delivered/failed via
	// UpdateAlertDeliveryStatus. Previously the evaluator called
	// RecordAlertDelivery here too, which collided with the row
	// ClaimAlertFire had just inserted and short-circuited the
	// dispatch (CI run 30436032609: 'alerts: record delivery' state:
	// conflict + 'receiver never received a delivery within 11 s').
	_ = payloadBytes // envelope is already stamped on the row

	// Flip state to firing on a successful claim. ok→firing audit
	// is emitted AFTER the delivery succeeds (mirrors the resolved
	// emission pattern — we only audit terminal transitions).
	if _, err := e.store.SetAlertRuleState(ctx, rule.ID, state.AlertStateFiring, now); err != nil {
		e.log.Warn("alerts: set state firing", "rule", rule.ID, "err", err)
	}

	// Unseal the webhook secret. A nil identity or a namespace
	// mismatch is a hard skip — we never dispatch without a valid
	// signature, and we never re-open a future-namespace blob.
	//
	// Prefer the rotation-aware identities slice (issue #316 /
	// ADR-057) over the single identity accessor when both are
	// wired. The fallback to identity preserves the pre-rotation
	// contract for callers that haven't migrated to LoadHostKeys.
	if e.identity == nil && e.identities == nil {
		e.log.Warn("alerts: no host age identity; skipping dispatch",
			"rule", rule.ID, "name", rule.Name)
		stats.SkippedNoIdentity++
		return
	}
	var openIdents []*age.X25519Identity
	if e.identities != nil {
		openIdents = e.identities()
	}
	if len(openIdents) == 0 && e.identity != nil {
		// identity is a loader closure (issue #316); invoke it to
		// surface the actual identity, not the closure pointer.
		// A loader that returns nil means "no identity at boot" —
		// the canonical degraded mode (FAAS_HOST_AGE_IDENTITY_PATH
		// unset). Skip rather than dispatch a half-built envelope.
		if single := e.identity(); single != nil {
			openIdents = []*age.X25519Identity{single}
		}
	}
	if len(openIdents) == 0 {
		e.log.Warn("alerts: identity loader returned nil; skipping dispatch",
			"rule", rule.ID, "name", rule.Name)
		stats.SkippedNoIdentity++
		return
	}
	ns, plaintext, err := secretbox.OpenBytesMulti(openIdents, rule.WebhookSecretSealed)
	if err != nil {
		e.log.Warn("alerts: open webhook secret; skipping dispatch",
			"rule", rule.ID, "err", err)
		e.recordFailure(ctx, rule, deliveryID, 0, "secret open failed: "+err.Error())
		return
	}
	if ns != AlertSecretNamespace {
		e.log.Warn("alerts: webhook secret namespace mismatch; skipping dispatch",
			"rule", rule.ID, "got_namespace", ns, "want", AlertSecretNamespace)
		e.recordFailure(ctx, rule, deliveryID, 0, "namespace mismatch: "+ns)
		return
	}

	// Build the webhook Event. The Dispatcher's header set pins
	// OccurredAt → X-Faas-Alert-Timestamp so the customer's
	// verifier can pin it without parsing the body twice. The
	// delivery.ID is the canonical wire id (X-Faas-Alert-Id) so
	// the customer can dedupe against the alert_deliveries row.
	evt := webhookout.Event{
		ID:         deliveryID,
		OccurredAt: now,
		Rule:       rule.Name,
		RuleName:   rule.Name,
		AppID:      rule.AppID, // "" for account-wide rules; customer keys off Payload.metric + threshold
		Payload:    payloadMap,
	}

	// Serialise Dispatch calls. pkg/webhookout.Dispatcher is
	// concurrency-unsafe; today the evaluator is single-goroutine,
	// but the mutex keeps the contract for future parallelism
	// honest (the dispatcher holds opts.HTTPClient + opts.Logger +
	// the retry loop's backoff state — all of which would race on
	// parallel Dispatch).
	e.dispatchMu.Lock()
	result := e.dispatch.Dispatch(ctx, webhookout.Target{
		URL:    rule.WebhookURL,
		Signer: e.newSigner(plaintext),
	}, evt)
	e.dispatchMu.Unlock()

	e.recordResult(ctx, rule, deliveryID, observed, result, now, stats)

	// SAFE-RELEASES-B fan-out: when the rule's action column is
	// anything other than the 'webhook' default (or empty), route
	// the same fire to the in-process ActionExecutor. Both paths
	// are independent — a webhook 5xx never blocks the rollback,
	// and a rollback failure never blocks the webhook.
	//
	// The action is gated by the cool-down bucket that
	// ClaimAlertFire enforces above: the webhook path consumes
	// the same idempotency key, so a duplicate inside the
	// cool-down window would have already early-returned at the
	// `if !won` branch and never reached this code.
	//
	// Serialised behind dispatchMu because the ActionDispatcher's
	// Execute path may share connection state with the webhook
	// dispatcher in the future; today the lock is just
	// precautionary.
	e.dispatchMu.Lock()
	e.runAction(ctx, rule, observed, now, stats)
	e.dispatchMu.Unlock()
}

// runAction is the SAFE-RELEASES-B fan-out (issue #976 / ADR-122).
// Pulled out of evalRule so the call site stays readable and so
// unit tests can exercise the action path in isolation. The
// function is fail-soft at every branch — log warn + Stats
// increment is the contract. Per CLAUDE.md ownership rules the
// evaluator never writes deployment rows directly; it only
// relays the fire to the ActionExecutor which in turn talks to
// apid via pkg/api.Client (or for the manual-recover path, via
// the existing handlers_rollouts.go surface landed in commit 6).
func (e *Evaluator) runAction(ctx context.Context, rule state.AlertRule, observed float64, now time.Time, stats *Stats) {
	action := rule.Action
	// Empty string and the explicit 'webhook' default are the
	// legacy path — ActionExecutor is not consulted.
	if action == "" || action == state.AlertActionWebhook {
		return
	}
	// Membership check against the closed vocabulary (mirrors
	// the catalog seed at migrations/00481_alert_rules_action.sql).
	// An unknown action is log-warned + skipped — never silently
	// dropped, never propagated to ActionExecutor.
	if !state.IsValidAlertAction(string(action)) {
		e.log.Warn("alerts: unknown action on rule; skipping in-process fan-out",
			"rule", rule.ID, "name", rule.Name, "action", action)
		stats.ActionSkipped++
		return
	}
	actionExec := e.getActionExec()
	if actionExec == nil {
		// ActionExec was never wired (older meterd or test
		// harness that omits the surface — or the meterd is
		// running without FAAS_SAFEDEPLOY_TOKEN). The webhook
		// fan-out already succeeded above; this rule's
		// side-effect simply doesn't happen.
		e.log.Warn("alerts: action set but ActionExecutor not wired; side-effect skipped",
			"rule", rule.ID, "name", rule.Name, "action", action)
		stats.ActionSkipped++
		return
	}
	if err := ctx.Err(); err != nil {
		// Don't fire a side-effect on a cancelled context — the
		// apid HTTP call would hang or surface an opaque
		// connection-reset.
		return
	}
	if err := actionExec.Execute(ctx, rule, observed, now); err != nil {
		// Fail-soft: log warn + bump ActionFailed. The webhook
		// path's result is already stamped on the delivery row;
		// a rollback failure does not block the next tick.
		e.log.Warn("alerts: action execute failed",
			"rule", rule.ID, "name", rule.Name, "action", action, "err", err)
		stats.ActionFailed++
		return
	}
	stats.ActionExecuted++
	if e.ops != nil {
		e.ops.AlertActionExecutedTotal(string(action))()
	}
	if e.audit != nil {
		// Audit kind 'alert.action_executed' is additive; the
		// audit table's kind column is a closed set in E.2 and
		// the seed in migration 00477 already includes it.
		e.audit.Emit(ctx, "alert.action_executed", &rule.AccountID, map[string]any{
			"rule_id":  rule.ID,
			"rule":     rule.Name,
			"action":   action,
			"observed": observed,
			"fired_at": now.UTC().Format(time.RFC3339Nano),
		})
	}
}

// skip reason codes. Used as sentinels inside evalRule to keep the
// observe() return shape small.
const (
	skipDegraded   = "degraded"
	skipNoIdentity = "no_identity"
)

// AlertOutcomeDelivered / AlertOutcomeFailed are the closed-vocab
// strings the alert-delivery metric labels use (the wire-side
// AlertDeliveryStatus enum in pkg/state has its own values — see
// state.AlertDeliveryDelivered — which intentionally differ because
// the metric label is a one-axis bucket and the row column is a
// four-state lifecycle). Pinning these as exported constants keeps
// pkg/alerts and pkg/wire/metrics.go in lockstep; goconst catches
// drift if a future rename happens in only one place.
const (
	AlertOutcomeDelivered = "delivered"
	AlertOutcomeFailed    = "failed"
)

// observe returns the metric value for `rule` plus a "fires?"
// comparison result and a skip reason. The two boolean-shaped
// returns are intentionally separate: comparisonResult is the
// threshold verdict, skipReason is a fail-closed "we can't even
// fetch" signal.
func (e *Evaluator) observe(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	switch rule.Metric {
	case state.AlertMetricFailedInvocs:
		// Postgres-backed. No Prometheus dependency; the
		// per-rule source filter expands "any" to the four
		// InvocationSource values that ship today.
		since := e.windowStart(rule.WindowSpec, e.now())
		source := rule.FailureSource
		if source == "" || source == state.AlertFailureAny {
			// Empty defaults to "any" per the handler-side
			// schema validation. Sum across the four sources
			// by issuing four queries — the table is small
			// (terminal rows only) and the per-rule scan
			// stays bounded.
			return e.summariseFailed(ctx, rule, since)
		}
		n, err := e.store.CountFailedInvocationsSince(ctx, rule.AccountID, rule.AppID, state.InvocationSource(source), since)
		if err != nil {
			e.log.Warn("alerts: count failed invocations",
				"rule", rule.ID, "err", err)
			return 0, false, skipDegraded
		}
		return float64(n), compareFloat(float64(n), rule.Comparison, rule.Threshold), ""
	case state.AlertMetricFailedDeployments:
		// Postgres-backed. Issue #1233 / ADR-123.
		// Walks the deployments table per migration 00349.
		since := e.windowStart(rule.WindowSpec, e.now())
		n, err := e.store.CountFailedDeploymentsSince(ctx, rule.AccountID, rule.AppID, since)
		if err != nil {
			e.log.Warn("alerts: count failed deployments",
				"rule", rule.ID, "err", err)
			return 0, false, skipDegraded
		}
		return float64(n), compareFloat(float64(n), rule.Comparison, rule.Threshold), ""
	case state.AlertMetricAPIUp:
		// Postgres-backed binary reachability. Issue #1233 /
		// ADR-123. The rule threshold is the expected count
		// (1 = reachable, 0 = not reachable); comparison 'lt 1'
		// fires when no successful invocation has landed in the
		// window. Cooldown gates redundant fires.
		since := e.windowStart(rule.WindowSpec, e.now())
		ok, err := e.store.WasInvokedSuccessfullySince(ctx, rule.AccountID, rule.AppID, since)
		if err != nil {
			e.log.Warn("alerts: api reachability probe",
				"rule", rule.ID, "err", err)
			return 0, false, skipDegraded
		}
		var observed float64
		if ok {
			observed = 1
		}
		return observed, compareFloat(observed, rule.Comparison, rule.Threshold), ""
	case state.AlertMetricAccountSpendEUR:
		// Postgres-backed MTD SUM. Issue #1233 / ADR-123.
		// Threshold is EUR (not cents) per the catalog seed
		// (spend_eur_20 fires at gt 20). CLAUDE.md says "Floats
		// near money fail review" — observed is int64 cents,
		// compareCents rounds the EUR threshold to cents at the
		// seam so the comparison is integer-aligned end-to-end.
		cents, err := e.store.MTDSpendEurCents(ctx, rule.AccountID)
		if err != nil {
			e.log.Warn("alerts: mtd spend query",
				"rule", rule.ID, "err", err)
			return 0, false, skipDegraded
		}
		return float64(cents) / 100, compareCents(cents, rule.Comparison, rule.Threshold), ""
	case state.AlertMetricCertExpirySeconds:
		// Postgres-backed min-seconds-remaining across the
		// meterd_tenant_surface_cert_expiry_state rows for the
		// (account, app). The walker in cmd/meterd/main.go
		// keeps the table fresh; -1 means "no cert observed
		// yet" and the comparison verdict fires false for
		// any lt threshold (the customer-facing preset uses
		// `lt 1209600` for "fewer than 14 days remaining").
		secs, err := e.store.MinCertExpiryForApp(ctx, rule.AccountID, rule.AppID)
		if err != nil {
			e.log.Warn("alerts: min cert expiry query",
				"rule", rule.ID, "err", err)
			return 0, false, skipDegraded
		}
		observed := float64(secs)
		return observed, compareFloat(observed, rule.Comparison, rule.Threshold), ""
	default:
		// PromQL-driven metrics.
		resp, source := appmetrics.Fetch(ctx, e.promQL, e.log, rule.AppID, string(rule.WindowSpec))
		if !appmetrics.IsDegradedSource(source) && source != appmetrics.SourcePrometheus {
			// Defensive: any unexpected Source value is treated
			// as degraded so a future appmetrics source type
			// can't silently disable evaluation.
			return 0, false, skipDegraded
		}
		if appmetrics.IsDegradedSource(source) {
			return 0, false, skipDegraded
		}
		var observed float64
		switch rule.Metric {
		case state.AlertMetricErrorRate:
			observed = resp.ErrorRatePct
		case state.AlertMetricLatencyP50:
			observed = resp.LatencyP50MS
		case state.AlertMetricLatencyP95:
			observed = resp.LatencyP95MS
		case state.AlertMetricLatencyP99:
			observed = resp.LatencyP99MS
		case state.AlertMetricColdStartPct:
			observed = resp.ColdStartPct
		case state.AlertMetricRequestCount:
			observed = float64(resp.RequestCount)
		case state.AlertMetricQueueDepth:
			// Issue #1233 / ADR-123. gateway_queue_depth{app}
			// is fed by SetQueueDepth in pkg/gateway/handler.go;
			// appmetrics.Fetch surfaces it on resp.QueueDepth.
			observed = float64(resp.QueueDepth)
		default:
			// Unknown / future metric — skip silently. The
			// closed vocabulary at state.AlertMetric rejects
			// unknown values at creation, but a defensive
			// default keeps a future metric type safe.
			return 0, false, skipDegraded
		}
		return observed, compareFloat(observed, rule.Comparison, rule.Threshold), ""
	}
}

// summariseFailed walks the four InvocationSource values and sums
// the per-source counts so a rule with source="any" sees the total
// of terminal-failed invocations on (account, app) inside the
// window. Bounded: four queries, indexed by account_id +
// state=failed.
func (e *Evaluator) summariseFailed(ctx context.Context, rule state.AlertRule, since time.Time) (float64, bool, string) {
	var total int
	for _, src := range []state.InvocationSource{
		state.InvocationAsyncInvoke,
		state.InvocationQueue,
		state.InvocationDelayedTask,
		state.InvocationCron,
	} {
		n, err := e.store.CountFailedInvocationsSince(ctx, rule.AccountID, rule.AppID, src, since)
		if err != nil {
			e.log.Warn("alerts: count failed invocations (any)",
				"rule", rule.ID, "source", src, "err", err)
			return 0, false, skipDegraded
		}
		total += n
	}
	return float64(total), compareFloat(float64(total), rule.Comparison, rule.Threshold), ""
}

// windowStart translates a closed AlertWindowSpec to a time.Time.
// Tests inject a fixed clock; production uses e.now().
func (e *Evaluator) windowStart(spec state.AlertWindowSpec, now time.Time) time.Time {
	var d time.Duration
	switch spec {
	case state.AlertWindow5m:
		d = 5 * time.Minute
	case state.AlertWindow15m:
		d = 15 * time.Minute
	case state.AlertWindow1h:
		d = time.Hour
	case state.AlertWindow6h:
		d = 6 * time.Hour
	case state.AlertWindow24h:
		d = 24 * time.Hour
	case state.AlertWindow7d:
		d = 7 * 24 * time.Hour
	case state.AlertWindow15d:
		d = 15 * 24 * time.Hour
	default:
		// Default to 5m (matches appmetrics.DefaultRange).
		d = 5 * time.Minute
	}
	return now.Add(-d)
}

// compareFloat applies the closed-vocabulary comparison. Returns
// true if the comparison fires (e.g. observed gt threshold).
func compareFloat(observed float64, op state.AlertComparison, threshold float64) bool {
	switch op {
	case state.AlertGt:
		return observed > threshold
	case state.AlertGte:
		return observed >= threshold
	case state.AlertLt:
		return observed < threshold
	case state.AlertLte:
		return observed <= threshold
	default:
		// Unknown operator is fail-safe (no fire). The closed
		// vocabulary at state.AlertComparison rejects unknown
		// values at creation, but a defensive default keeps a
		// future operator safe.
		return false
	}
}

// compareCents is the integer-aligned sibling of compareFloat for
// monetary metrics (issue #1233 / ADR-123, account_spend_eur).
// CLAUDE.md mandates "Floats near money fail review" — the
// AccountSpend path stores cents (int64) and the catalog's
// spend_eur_20 preset ships a threshold in EUR (20.0), so we convert
// thresholdEUR to cents ONCE at the seam (rounded half-to-even
// avoids the 19.995 EUR phantom-fire at the month edge) and
// compare two integers. No further float math touches the value.
func compareCents(observedCents int64, op state.AlertComparison, thresholdEUR float64) bool {
	// math.Round(threshold*100)/100 then *100 keeps the float out
	// of the comparison — banker's rounding via math.RoundToEven.
	thresholdCents := int64(math.RoundToEven(thresholdEUR * 100))
	switch op {
	case state.AlertGt:
		return observedCents > thresholdCents
	case state.AlertGte:
		return observedCents >= thresholdCents
	case state.AlertLt:
		return observedCents < thresholdCents
	case state.AlertLte:
		return observedCents <= thresholdCents
	default:
		return false
	}
}

// buildPayload serialises the JSON envelope recorded on
// alert_deliveries.payload and sent to the customer webhook. Same
// shape for the dashboard scrape + the wire — saves a second
// marshal on the dispatch hot path.
//
// Returns ([]byte, map, error). The bytes are what ClaimAlertFire
// stores on alert_deliveries.payload (JSONB column); the map is
// what webhookout.Dispatcher encodes into the HTTP body. By sharing
// the canonical map across both sides, we guarantee the dashboard
// scrape and the customer's webhook see the same envelope — one
// source of truth, one marshal per firing.
func buildPayload(rule state.AlertRule, observed float64) ([]byte, map[string]any, error) {
	m := map[string]any{
		"rule_id":    rule.ID,
		"rule_name":  rule.Name,
		"metric":     string(rule.Metric),
		"comparison": string(rule.Comparison),
		"threshold":  rule.Threshold,
		"observed":   observed,
		"window":     string(rule.WindowSpec),
		"fired_at":   time.Now().UTC().Format(time.RFC3339Nano),
	}
	if rule.FailureSource != "" {
		m["failure_source"] = string(rule.FailureSource)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, nil, fmt.Errorf("alerts: marshal payload: %w", err)
	}
	return b, m, nil
}

// recordResult closes the loop: UpdateAlertDeliveryStatus stamps the
// final status + attempt count + last error onto the dedupe row
// already inserted by ClaimAlertFire + RecordAlertDelivery. On a
// successful delivery the alert.delivered audit is emitted; on a
// failure, alert.failed.
func (e *Evaluator) recordResult(ctx context.Context, rule state.AlertRule, deliveryID string, observed float64, result webhookout.Result, now time.Time, stats *Stats) {
	status := state.AlertDeliveryFailed
	lastErr := ""
	deliveredAt := (*time.Time)(nil)
	if result.Err == nil && result.StatusCode >= 200 && result.StatusCode < 400 {
		status = state.AlertDeliveryDelivered
		stats.Delivered++
		if e.ops != nil {
			e.ops.AlertDeliveryAttemptsTotal(AlertOutcomeDelivered)()
		}
		deliveredAt = &now
	} else {
		stats.Failed++
		if e.ops != nil {
			e.ops.AlertDeliveryAttemptsTotal(AlertOutcomeFailed)()
		}
		if result.Err != nil {
			lastErr = result.Err.Error()
		}
		if lastErr == "" {
			lastErr = "unknown dispatch failure"
		}
	}

	if err := e.store.UpdateAlertDeliveryStatus(ctx, deliveryID, status, result.Attempts, result.StatusCode, lastErr, deliveredAt); err != nil {
		e.log.Warn("alerts: update delivery status",
			"rule", rule.ID, "delivery_id", deliveryID, "err", err)
		return
	}

	if e.audit != nil {
		kind := "alert.delivered"
		if status != state.AlertDeliveryDelivered {
			kind = "alert.failed"
		}
		e.audit.Emit(ctx, kind, &rule.AccountID, map[string]any{
			"rule_id":     rule.ID,
			"rule":        rule.Name,
			"delivery_id": deliveryID,
			"attempts":    result.Attempts,
			"status_code": result.StatusCode,
			"observed":    observed,
		})
	}
}

// recordFailure records a non-dispatch failure (secret open, namespace
// mismatch) on the delivery row already inserted by
// RecordAlertDelivery. Used when we never even reached the dispatcher.
func (e *Evaluator) recordFailure(ctx context.Context, rule state.AlertRule, deliveryID string, statusCode int, lastErr string) {
	if err := e.store.UpdateAlertDeliveryStatus(ctx, deliveryID, state.AlertDeliveryFailed, 0, statusCode, lastErr, nil); err != nil {
		e.log.Warn("alerts: update delivery status (pre-dispatch failure)",
			"rule", rule.ID, "delivery_id", deliveryID, "err", err)
	}
	if e.audit != nil {
		e.audit.Emit(ctx, "alert.failed", &rule.AccountID, map[string]any{
			"rule_id":     rule.ID,
			"rule":        rule.Name,
			"delivery_id": deliveryID,
			"last_error":  lastErr,
		})
	}
}
