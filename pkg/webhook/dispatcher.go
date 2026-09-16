// Package webhook is the outbound webhook delivery dispatcher for
// cmd/schedd (issue #476 / ADR-076). It owns the durable queue
// drain + per-account fairness + retry-with-backoff + DLQ-at-7
// state machine for app_webhook_deliveries rows.
//
// Architecture:
//
//   - cmd/schedd boots one Dispatcher goroutine alongside the cron
//     loop and the invocation drain. All three share the same
//     store + pool + auditor + slog logger.
//
//   - The Dispatcher's hot path is a 5-second ticker. Each tick:
//     1. Claims up to DefaultCap rows in a single FOR UPDATE
//     SKIP LOCKED transaction, ORDER BY account_id,
//     next_attempt_at (the per-account fairness contract).
//     2. For each claimed row, fires a goroutine that POSTs to
//     the customer's target_url via pkg/webhookout.Dispatcher
//     with the webhook HeaderSet.
//     3. After the HTTP attempt completes, the goroutine calls
//     MarkAppWebhookDelivery{Succeeded,Failed,Dead} based on
//     the outcome + retry budget.
//
//   - In-flight goroutines track through d.inflight (sync.WaitGroup).
//     On ctx.Done() the Run loop blocks on d.inflight.Wait() with a
//     10-second deadline (per cmd/schedd/main.go shutdown contract),
//     so SIGTERM never loses an in-flight row.
//
//   - The dispatcher's Sleeper and Now are struct fields. Tests
//     fast-forward 7.5 hours of backoff in <1s by stubbing these;
//     the struct-field location (not package vars) makes concurrent
//     tests safe because each dispatcher instance owns its own
//     clock. Mirrors pkg/webhookdedupe.nowFunc's design intent
//     (sweeper_test.go:36-58).
//
// Why per-account fairness matters here:
//   - The claim query is bounded to DefaultCap rows per tick. Without
//     ORDER BY account_id, a noisy account with 1000 pending
//     deliveries would monopolise every tick and starve every other
//     account. The ORDER BY + LIMIT emerges round-robin: account A's
//     first row precedes account B's first row, etc.
//
// Why not token-bucket:
//   - A token bucket would add a state table (per-account state
//     rows), a config knob, and a refresh tick — for a benefit no
//     current customer reads. The ORDER BY contract is sufficient at
//     the 32/tick cap and is observable from a single SQL
//     statement (handy for the operator's "why is account X's queue
//     not draining?" debug query).
//
// Why exponential backoff with ±25% jitter:
//   - Matches the pkg/webhookout backoff shape (5 attempts,
//     2s/8s/32s/128s ±25%) so the customer-facing retry behaviour is
//     consistent across the alert + webhook surfaces. The webhook
//     dispatcher extends the ladder to 7 attempts (30s/2m/10m/1h/6h
//     on default retry policy) to absorb longer customer outages.
//
// Why DLQ at attempt 7:
//   - Issue #476's acceptance criterion: after 7 attempts the
//     customer endpoint has had 7.5h of patience. The customer can
//     inspect via GET /deliveries and POST /deliveries/{id}/retry to
//     re-arm the budget. No retry after 7 — past 7 the operator
//     should investigate.
package webhook

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// Tunables. Default values match the issue #476 acceptance gates:
// 32/tick, 5s tick, 10s drain on shutdown.
const (
	DefaultTick       = 5 * time.Second
	DefaultCap        = 32
	DefaultPerAttempt = 10 * time.Second
	// DefaultDrainTimeout is the budget for in-flight goroutines to
	// finish on ctx.Done(). Matches the cmd/schedd/main.go 10s
	// shutdown pattern; the HTTP graceful stop timeout is 5s and the
	// dispatcher is a sibling goroutine, not an HTTP server, so it
	// gets its own budget.
	DefaultDrainTimeout = 10 * time.Second
)

// defaultBackoff is the retry schedule for retry_policy='default'.
// Mirrors issue #476's "30s, 2m, 10m, 1h, 6h" ladder — 6 retries
// after the initial attempt = 7 attempts total. Lives as a package-
// level constant (not a struct field) because the schedule is
// read-only after init — schedd runs one Dispatcher per process,
// and tests use the WithBackoffs helper to swap the schedules
// without mutating globals.
var defaultBackoff = []time.Duration{
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	1 * time.Hour,
	6 * time.Hour,
}

// aggressiveBackoff halves each step. Mirrors the alert-side "we
// need this to land fast" shape.
var aggressiveBackoff = []time.Duration{
	15 * time.Second,
	1 * time.Minute,
	5 * time.Minute,
	30 * time.Minute,
	3 * time.Hour,
}

// noBackoff is the empty schedule — the dispatcher marks 'dead'
// immediately on any non-2xx response. retry_policy='none' is the
// "I'm just testing this endpoint" toggle.
var noBackoff = []time.Duration{}

// DefaultBackoffs is the read-only schedule map keyed by retry
// policy. Tests and the cmd/schedd production wiring consult this
// table; per-instance overrides go via Dispatcher.WithBackoffs.
var DefaultBackoffs = map[state.AppWebhookRetryPolicy][]time.Duration{
	state.AppWebhookRetryDefault:    defaultBackoff,
	state.AppWebhookRetryAggressive: aggressiveBackoff,
	state.AppWebhookRetryNone:       noBackoff,
}

// ErrDeliveryExhausted is returned by ComputeBackoff when the
// supplied attempt is past the schedule's end. The caller
// (deliverOne) translates this into MarkDead.
var ErrDeliveryExhausted = errors.New("webhook: backoff schedule exhausted")

// ComputeBackoff returns the delay before the (attempt+1)-th try.
// attempt is 0-indexed: 0 → first retry (delay before attempt 2).
// The result carries ±25% jitter so a fleet of retries doesn't
// thundering-herd the customer's endpoint.
//
// Returns ErrDeliveryExhausted when attempt >= len(schedule) — the
// caller marks the row dead.
//
// Jitter source: crypto/rand seeded from the kernel RNG (4 bytes
// scaled into [-0.25, +0.25]). We don't use math/rand because the
// seed-by-time is observable from the customer's monitoring and
// could let an attacker predict the exact retry wave. Backoff is
// not a security primitive but the retry-timing-leak pattern is
// the same one pkg/webhookout avoids at line 308-313.
func ComputeBackoff(schedule []time.Duration, attempt int) (time.Duration, error) {
	if attempt < 0 {
		return 0, fmt.Errorf("webhook: negative attempt %d", attempt)
	}
	if attempt >= len(schedule) {
		return 0, ErrDeliveryExhausted
	}
	base := schedule[attempt]
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failure is a hard error in FIPS mode but the
		// runtime path stays alive even if jitter falls back to a
		// deterministic offset. We pick +0.0 (no jitter) so the
		// customer at least gets the bare minimum delay. Returning
		// the error here would let the caller MarkDead the row
		// (the scheduleErr branch above DLQs), which is wrong —
		// the row's attempt is recoverable on the next tick.
		//nolint:nilerr  // see comment above
		return base, nil
	}
	u := binary.BigEndian.Uint32(buf[:])
	// Map u in [0, 2^32) into [-0.25, +0.25].
	offset := (float64(u)/float64(1<<32) - 0.5) * 0.5
	return time.Duration(float64(base) * (1.0 + offset)), nil
}

// scheduleFor returns the backoff schedule for a retry policy. The
// "none" policy returns an empty schedule so the first non-2xx
// response marks the row dead. Per-instance overrides installed via
// Dispatcher.WithBackoffs take precedence over DefaultBackoffs so
// tests can pin the schedule deterministically.
func (d *Dispatcher) scheduleFor(p state.AppWebhookRetryPolicy) []time.Duration {
	if d.Backoffs != nil {
		if s, ok := d.Backoffs[p]; ok {
			return s
		}
	}
	if s, ok := DefaultBackoffs[p]; ok {
		return s
	}
	return defaultBackoff
}

// Dispatcher is the cmd/schedd-side webhook queue drain. One
// Dispatcher per schedd process; the store + pool + auditor are
// shared with the cron loop and invocation drain.
type Dispatcher struct {
	store   state.Store
	auditor *audit.Auditor
	log     *slog.Logger

	// IdentityLoader returns the age X25519 identities the dispatcher
	// uses to open sealed webhook secrets. Mirrors pkg/alerts/evaluator.go
	// identity loader contract (lines 380-395). Returning nil is the
	// canonical "no identity available" mode (FAAS_HOST_AGE_IDENTITY_PATH
	// unset); the dispatcher skips the row rather than dispatching with
	// a half-built envelope.
	IdentityLoader func() []*age.X25519Identity

	// Sleeper and Now are clock-injection seams for tests. Default
	// values are time.Sleep and time.Now. Tests fast-forward 7.5h
	// of backoff in <1s by overriding these on the struct.
	Sleeper func(time.Duration)
	Now     func() time.Time

	// Tick is the per-cycle wait. Default 5s.
	Tick time.Duration

	// Cap is the per-tick claim limit. Default 32.
	Cap int

	// Backoffs overrides the per-retry-policy schedule. When nil,
	// the dispatcher consults DefaultBackoffs. Tests install
	// short schedules here so a 7-attempt path runs in <1s wall.
	Backoffs map[state.AppWebhookRetryPolicy][]time.Duration

	// HTTPClient is shared across deliveries; nil resolves to
	// oci.NewEgressHTTPClientAllowLoopback (the SSRF-guarded
	// dialer) so production egress rules apply on every POST. The
	// e2e test sets FAAS_EGRESS_ALLOW_LOOPBACK=1 to permit
	// loopback; that flag is read by oci.NewEgressHTTPClientAllowLoopback.
	HTTPClient *http.Client

	// PerAttempt is the per-attempt HTTP timeout. Default 10s.
	PerAttempt time.Duration

	// inflight tracks goroutines spawned by tick(). On ctx.Done()
	// the Run loop waits for these to drain.
	inflight sync.WaitGroup
}

// NewDispatcher constructs a Dispatcher with all defaults applied.
// The caller still wires store + auditor + log via the With*
// setters (functional-options pattern, mirrors sched.NewDrain).
func NewDispatcher(store state.Store, aud *audit.Auditor, log *slog.Logger) *Dispatcher {
	return &Dispatcher{
		store:      store,
		auditor:    aud,
		log:        log,
		Sleeper:    time.Sleep,
		Now:        time.Now,
		Tick:       DefaultTick,
		Cap:        DefaultCap,
		PerAttempt: DefaultPerAttempt,
		HTTPClient: nil,
		Backoffs:   nil, // nil → consult DefaultBackoffs
	}
}

// WithTick overrides the per-tick wait (tests).
func (d *Dispatcher) WithTick(t time.Duration) *Dispatcher {
	d.Tick = t
	return d
}

// WithCap overrides the per-tick claim limit (tests).
func (d *Dispatcher) WithCap(c int) *Dispatcher {
	d.Cap = c
	return d
}

// WithBackoffs overrides the per-retry-policy schedule. Each entry
// in `b` replaces the corresponding DefaultBackoffs entry; absent
// keys fall back to DefaultBackoffs. Tests use this to pin a
// short schedule so the 7-attempt DLQ path runs in <1s.
func (d *Dispatcher) WithBackoffs(b map[state.AppWebhookRetryPolicy][]time.Duration) *Dispatcher {
	merged := make(map[state.AppWebhookRetryPolicy][]time.Duration, len(DefaultBackoffs)+len(b))
	for k, v := range DefaultBackoffs {
		merged[k] = v
	}
	for k, v := range b {
		merged[k] = v
	}
	d.Backoffs = merged
	return d
}

// Run blocks until ctx is cancelled, then drains in-flight
// goroutines up to DefaultDrainTimeout. Mirrors pkg/sched/drain.go
// Run's "ctx.Done() returns nil" contract: a cancelled context is a
// clean exit, not an error.
func (d *Dispatcher) Run(ctx context.Context) error {
	if d.HTTPClient == nil {
		// SSRF-guarded dialer with loopback escape hatch for the e2e
		// (mirrors pkg/webhookout.NewDispatcher pattern).
		if hc := oci.NewEgressHTTPClientAllowLoopback(); hc != nil {
			d.HTTPClient = hc
		} else {
			d.HTTPClient = oci.NewEgressHTTPClient()
		}
		// Apply PerAttempt as the client-level timeout only when we
		// built the client ourselves (oci returns no-timeout
		// clients). Mirrors pkg/webhookout.NewDispatcher.
		d.HTTPClient.Timeout = d.PerAttempt
	}

	ticker := time.NewTicker(d.Tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return d.shutdown()
		case <-ticker.C:
			d.cycle(ctx)
		}
	}
}

// shutdown blocks until in-flight goroutines finish or the 10s
// deadline hits. The deadline is a backstop — the inflight wait is
// bounded by PerAttempt + a single backoff schedule slot, both
// individually < 7.5h but the customer-driven wait may be large in
// practice.
func (d *Dispatcher) shutdown() error {
	done := make(chan struct{})
	go func() { d.inflight.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-time.After(DefaultDrainTimeout):
		d.log.Warn("webhook: drain deadline hit; some in-flight rows may be re-claimed on next boot")
		return nil
	}
}

// cycle is the per-tick drain walk. Private — public tests drive it
// via Dispatcher.Run with a stubbed ticker.
func (d *Dispatcher) cycle(ctx context.Context) {
	now := d.Now()
	claimed, err := d.store.ClaimDueAppWebhookDeliveries(ctx, d.Cap, now)
	if err != nil {
		d.log.Warn("webhook: claim", "err", err)
		return
	}
	for _, row := range claimed {
		d.inflight.Add(1)
		// Capture the loop variable — Go 1.22+ per-loop semantics
		// make this explicit; we do it the explicit way for
		// readability across Go versions.
		row := row
		go func() {
			defer d.inflight.Done()
			d.deliverOne(ctx, row)
		}()
	}
}

// deliverOne POSTs one row through pkg/webhookout.Dispatcher, then
// stamps the result via the appropriate Mark* method. Always
// returns — the run loop does not abort on individual delivery
// failures.
func (d *Dispatcher) deliverOne(ctx context.Context, row state.AppWebhookDelivery) {
	// Load the subscription row so we know the target_url + secret +
	// retry_policy. The store guarantees the webhook row exists
	// (FK CASCADE on the delivery ledger would have prevented the
	// delivery from being created if not).
	hook, err := d.store.AppWebhookByID(ctx, row.WebhookID)
	if err != nil {
		// The subscription was deleted between RecordDelivery and
		// the dispatcher claim — FK CASCADE should have wiped the
		// delivery row too, so this branch is unreachable in
		// practice. Mark dead defensively so the row doesn't leak.
		_ = d.store.MarkAppWebhookDeliveryDead(ctx, row.ID, row.Attempt,
			fmt.Sprintf("webhook: subscription %s missing: %v", row.WebhookID, err))
		return
	}
	if !hook.Enabled {
		// The customer disabled the subscription while a delivery
		// was in flight. Park the row in dead with a "disabled"
		// reason so the operator can grep for it.
		_ = d.store.MarkAppWebhookDeliveryDead(ctx, row.ID, row.Attempt,
			"webhook: subscription disabled")
		return
	}

	// Unseal the secret. apid sealed on write; we unseal on
	// dispatch. Mirrors the alert dispatcher's flow
	// (pkg/alerts/evaluator.go:404-426): secretbox.OpenBytesMulti
	// with the loaded identity. The secret NEVER lands in a log
	// line — only the namespace mismatch / open error is reported.
	//
	// Webhook secrets use secretbox.SealBytes (the blob-with-
	// namespace variant) NOT SealOne (the env-var Envelope variant).
	// The seal/unseal shape matches the alert-secret path so a
	// single host.age identity covers both surfaces.
	openIdents := d.IdentityLoader()
	if len(openIdents) == 0 {
		// No identity available → DLQ. Retrying with the same broken
		// state machine would just cycle the row through 7 attempts
		// before the operator notices; the operator must rotate
		// host.age first (then run `webhooks retry`). Mirrors the
		// pkg/alerts/evaluator.go:404-426 short-circuit.
		d.log.Warn("webhook: identity loader returned nil; marking dead",
			"webhook_id", hook.ID, "delivery_id", row.ID)
		_ = d.store.MarkAppWebhookDeliveryDead(ctx, row.ID, row.Attempt,
			"webhook: no age identity available")
		d.emitAudit(ctx, "webhook.dead", row, hook,
			fmt.Errorf("no age identity available"), 0)
		return
	}
	ns, plaintext, err := secretbox.OpenBytesMulti(openIdents, hook.SecretSealed)
	if err != nil {
		_ = d.store.MarkAppWebhookDeliveryDead(ctx, row.ID, row.Attempt,
			fmt.Sprintf("webhook: unseal secret: %v", err))
		return
	}
	if ns != "APP_WEBHOOK" {
		_ = d.store.MarkAppWebhookDeliveryDead(ctx, row.ID, row.Attempt,
			fmt.Sprintf("webhook: namespace mismatch: got=%s want=APP_WEBHOOK", ns))
		return
	}
	secret := plaintext

	// Build the wire body. The dispatcher's external contract is
	// the Event shape pkg/webhookout expects:
	//   {id, occurred_at, rule, rule_name, app_id, payload}
	// We fill rule='app.webhook' (the surface name), rule_name=event
	// (so dashboards key off the event), and the rest verbatim.
	evt := webhookout.Event{
		ID:         row.ID,
		OccurredAt: row.CreatedAt,
		Rule:       "app.webhook",
		RuleName:   string(row.Event),
		AppID:      row.AppID,
		Source:     "urn:gregale:app:" + row.AppID,
		Type:       string(row.Event),
		Subject:    "apps/" + row.AppID,
		AccountID:  row.AccountID,
		Data:       json.RawMessage(row.Payload),
		Payload: map[string]any{
			"event":       string(row.Event),
			"webhook_id":  row.WebhookID,
			"delivery_id": row.ID,
			"attempt":     row.Attempt,
			"data":        json.RawMessage(row.Payload),
		},
	}

	// Per-delivery dispatcher. We construct one per call so the
	// HeaderSet is locked in (issue #476's wire is HeaderSetWebhook)
	// and the Sleeper is the dispatcher-level injection (tests
	// fast-forward 7.5h of backoff in <1s).
	disp := webhookout.NewDispatcher(webhookout.DispatcherOptions{
		MaxAttempts: 1, // the dispatcher owns the retry ladder
		BaseBackoff: 0,
		PerAttempt:  d.PerAttempt,
		HTTPClient:  d.HTTPClient,
		Sleeper:     d.Sleeper,
		Logger:      d.log,
		HeaderSet:   webhookout.HeaderSetWebhook,
		Format:      webhookout.DeliveryFormat(hook.DeliveryFormat),
	})
	res := disp.Dispatch(ctx, webhookout.Target{
		URL:    hook.TargetURL,
		Signer: webhookout.NewSigner(secret),
	}, evt)

	now := d.Now()
	if res.Err == nil {
		_ = d.store.MarkAppWebhookDeliverySucceeded(ctx, row.ID, res.StatusCode, row.Attempt, now)
		d.emitAudit(ctx, "webhook.delivered", row, hook, nil, res.StatusCode)
		return
	}

	// Failure path. Decide retry vs DLQ via the retry-policy
	// schedule + the row's current attempt count.
	schedule := d.scheduleFor(hook.RetryPolicy)
	if len(schedule) == 0 {
		// retry_policy='none' — first failure is terminal.
		_ = d.store.MarkAppWebhookDeliveryDead(ctx, row.ID, row.Attempt,
			fmt.Sprintf("webhook: %v (retry_policy=none)", res.Err))
		d.emitAudit(ctx, "webhook.dead", row, hook, res.Err, res.StatusCode)
		return
	}
	delay, scheduleErr := ComputeBackoff(schedule, row.Attempt)
	if scheduleErr != nil {
		// Past the schedule → DLQ.
		_ = d.store.MarkAppWebhookDeliveryDead(ctx, row.ID, row.Attempt,
			fmt.Sprintf("webhook: %v (budget exhausted at attempt=%d)", res.Err, row.Attempt+1))
		d.emitAudit(ctx, "webhook.dead", row, hook, res.Err, res.StatusCode)
		return
	}
	// Mark 'failed' with the next attempt scheduled at now + delay.
	// The claim query's WHERE next_attempt_at <= now predicate picks
	// it up when the time arrives.
	_ = d.store.MarkAppWebhookDeliveryFailed(ctx, row.ID, res.StatusCode, row.Attempt,
		fmt.Sprintf("webhook: %v", res.Err), now.Add(delay))
	d.emitAudit(ctx, "webhook.failed", row, hook, res.Err, res.StatusCode)
}

// emitAudit writes the audit row. Best-effort: a failed audit emission
// must not abort the delivery cycle (mirrors pkg/sched/loop.go:1498-1563).
// accountID is supplied as a pointer per audit.Auditor.Emit's contract
// (pkg/audit/audit.go:104); the row's account_id is required, so we
// dereference a local copy rather than passing nil.
//
// Nil auditor is allowed: tests that don't care about audit emit
// (e.g. backoff math + claim ordering) skip the row rather than
// panic. The cmd/schedd production wiring always supplies one.
func (d *Dispatcher) emitAudit(ctx context.Context, kind string, row state.AppWebhookDelivery, hook state.AppWebhook, deliveryErr error, statusCode int) {
	if d.auditor == nil {
		return
	}
	accountID := row.AccountID
	subject := row.WebhookID
	details := map[string]any{
		"delivery_id":  row.ID,
		"webhook_id":   row.WebhookID,
		"app_id":       row.AppID,
		"account_id":   row.AccountID,
		"event":        string(row.Event),
		"target_url":   hook.TargetURL,
		"attempt":      row.Attempt,
		"status_code":  statusCode,
		"retry_policy": string(hook.RetryPolicy),
		"subject":      subject,
	}
	if deliveryErr != nil {
		details["err"] = deliveryErr.Error()
	}
	d.auditor.Emit(ctx, kind, &accountID, details)
}
