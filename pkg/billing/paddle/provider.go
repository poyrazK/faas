package paddle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	paddle "github.com/PaddleHQ/paddle-go-sdk/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// PlanPriceIDs + OveragePriceIDs together hold the price handles
// Paddle returned from EnsurePlanProducts. Keys are api.Plan values
// (free/hobby/pro/scale) so the meterd pusher can look up the
// overage line-item handle without re-stamping strings.
//
// The cache lives on the Client because it is constructed once at
// boot and read by every PushUsageRecord call. concurrent-safe via
// the same mutex — pattern matches pkg/billing/stripe's PlanPriceIDs.
type priceCatalog struct {
	mu            sync.RWMutex
	planMonthly   map[api.Plan]string // plan → pri_…
	planOverage   map[api.Plan]string // plan → pri_…
	planCustomers map[api.Plan]string // plan → pro_…
}

// Provider is the Paddle Billing v2 implementation of billing.Provider.
// All four interface methods map onto the paddle-go SDK's REST endpoints;
// provider-specific wire-format concerns (signature scheme, line-item
// shape, customer ID format) stay inside this package — apid and
// meterd see only billing.Event / state.Account / the Provider
// interface. ADR-025.
type Provider struct {
	apiKey        string
	webhookSecret string
	client        *paddle.SDK
	log           *slog.Logger
	catalog       *priceCatalog
	// flushFn is the test seam defaultFlushLocked is reached through.
	// nil → defaultFlushLocked (production). The seam matters: the PR
	// that introduced the stateless per-push shape (this PR) needed a
	// way to assert "no SDK POST fired" without standing up a real
	// *paddle.SDK, and counter stubs are the cheapest way to do that.
	//
	// Kept on the Provider (not on a constructor-level opts struct) so
	// the test code can swap flushFn on a single constructed instance
	// instead of re-constructing the whole provider per test case.
	flushFn FlushFn
	// dedupe is the state-store-backed cross-process gate consulted
	// before each Paddle CreateTransaction POST and stamped after.
	// nil → within-process dedupe only (apid's path; apid never pushes
	// overage, so the only writer is meterd's). meterd wires it via
	// NewProviderWithDedupe so a crash between POST and stamp cannot
	// cause a second POST on the next process boot.
	dedupe PaddleOverageDedupe
	// createUpgradeTxnFn is the seam CreateUpgradeTransaction delegates
	// to. Tests substitute a counter/recorder stub so they can assert
	// the SDK request shape (price handle, CustomData, Idempotency-Key
	// tag) without standing up a full *paddle.SDK. nil → the default
	// production body (defaultCreateUpgradeTxn).
	createUpgradeTxnFn CreateUpgradeTxnFn
	now                func() time.Time
	// flushRecorder is the test seam defaultFlushLocked writes one
	// RecordedFlush row into per push. nil → no recording (production
	// and the bulk of unit tests that exercise the dedupe / flusher
	// counters). When set, the slice is appended under
	// flushRecorderMu — tests that race two flushes against the same
	// provider must take the same mutex. Read with FlushRecorderForTest
	// or by reading the slice through the returned pointer.
	//
	// Sits next to flushFn rather than replacing it because the two
	// seams answer different questions: flushFn lets tests substitute
	// the SDK POST entirely (counter / err-injection / capture-only),
	// while flushRecorder observes the production body without
	// changing it. The PR that closed the under-billing bug needs
	// the latter — flushFn alone hides the Quantity field on the
	// CreateTransactionRequest the production body builds.
	flushRecorder   []RecordedFlush
	flushRecorderMu sync.Mutex
	// instanceID is the free-form identity stamp passed into
	// ClaimPaddleOverageWindow — used by ops to identify which
	// process holds the claim when a stuck row is investigated.
	// Stable for the life of the process (computed once at
	// construction); not a uniqueness constraint, the
	// (account_id, window_start) PK is.
	instanceID string
	// lastSyncAt is stamped at the end of every successful
	// EnsurePlanProducts call. Surfaced via the OpProvider
	// interface (PR-P3) so operators can tell from `faas billing
	// status` whether the catalog hydration ran and when. Zero
	// (un-initialized) when no hydration has yet succeeded —
	// ListCatalog surfaces this so the CLI can render "never".
	// catalog.mu guards the write (called under the same lock as
	// the catalog map updates in ensureProducts); reads hold the
	// RLock via the snapshot helpers.
	lastSyncAt time.Time
	// webhookTolerance is the replay-protection window applied
	// during VerifyWebhook. PR-P4 introduced the operator knob
	// (FAAS_PADDLE_WEBHOOK_TOLERANCE_SECONDS); pre-PR-P4 the value
	// was hard-coded at the apid handler call-site. Zero value
	// means "use the verifier's clamp-to-default behaviour" —
	// WebhookTolerance() resolves this to webhookDefaultTolerance
	// so callers don't have to special-case 0.
	webhookTolerance time.Duration
}

// paddleOverageLease is the lease window for a ClaimPaddleOverageWindow
// row. Long enough to absorb a slow Paddle POST (p99 historically
// < 30s); short enough that a crashed pod's claim is reaped within
// one boot-cycle of any peer. Configurable later via env if needed.
const paddleOverageLease = 5 * time.Minute

// WebhookTolerance returns the configured replay-protection window,
// clamped to webhookDefaultTolerance when unset / <= 0. Operators
// configure via FAAS_PADDLE_WEBHOOK_TOLERANCE_SECONDS or
// [billing.paddle].webhook_tolerance_seconds in TOML (PR-P4); the
// apid handler reads this rather than the literal `5*time.Minute`
// the pre-PR-P4 code path used.
func (p *Provider) WebhookTolerance() time.Duration {
	if p.webhookTolerance <= 0 {
		return webhookDefaultTolerance
	}
	return p.webhookTolerance
}

// SetWebhookTolerance installs the configured replay-protection
// window. Called by the loader once after NewProvider so the
// constructor signature stays stable for the existing test suite.
// Safe to call before any VerifyWebhook call.
func (p *Provider) SetWebhookTolerance(d time.Duration) {
	if d <= 0 {
		p.webhookTolerance = 0 // WebhookTolerance() clamps to default.
		return
	}
	p.webhookTolerance = d
}

// claimedBy returns the per-process identity stamp used to mark
// paddle_overage_dedupe rows in the claimed_by column. Falls back
// to a static sentinel if HOSTNAME / POD_NAME are unset so dev
// hosts still produce a non-empty value for ops debugging.
func (p *Provider) claimedBy() string {
	if p.instanceID != "" {
		return p.instanceID
	}
	if h := os.Getenv("HOSTNAME"); h != "" {
		return h
	}
	if h := os.Getenv("POD_NAME"); h != "" {
		return h
	}
	return "paddle-push"
}

// NewProvider wires the Paddle v5 SDK. sandbox=true →
// api.sandbox.paddle.com (operator's free sandbox); false →
// api.paddle.com (production).
//
// The SDK is initialized with a custom HTTP client whose Transport
// is wrapped by NewIdempotencyRT — every Writes request the SDK
// emits (CreateTransaction, etc.) flows through the wrapper, which
// copies X-Transit-Id (set by the SDK from paddle.ContextWithTransitID)
// as Idempotency-Key on POST /transactions. See transport.go for the
// full design rationale. The paddle-go-sdk/v5@v5.2.0 SDK exposes
// paddle.WithClient(c client.HTTPDoer) for this — the *http.Client
// we pass satisfies that interface via its Do method.
//
// Catalog + time hooks are initialized lazily so tests can construct
// without live configuration. EnsurePlanProducts must be called
// before PushUsageRecord / CreateCustomer in production; both fail
// fast with a descriptive error if the catalog is empty.
// PaddleCapabilities returns the static capability set for the Paddle
// provider. Lifted out of *Provider.Capabilities so the loader's
// Providers() metadata-only path (loader.go:160) does not have to
// construct a *Provider just to read the bits. The capability set is
// invariant — Capabilities() never reads p.client — so a free function
// is the correct shape.
//
// Exported because the loader (pkg/billing/loader) is a separate
// package and needs to read the static set without constructing a
// *Provider. Exposing the function (not the value) keeps future
// capability-set composition (e.g. adding CapRefund) localised to
// this file.
func PaddleCapabilities() billing.CapabilitySet {
	return billing.CapabilitySet(billing.CapHostedCheckout | billing.CapUsageLineItem | billing.CapRefund | billing.CapSandbox)
}

func NewProvider(apiKey, webhookSecret string, sandbox bool, log *slog.Logger) (*Provider, error) {
	if log == nil {
		log = slog.Default()
	}
	// Explicit Paddle deployments must provide an API key. Refuse to construct a
	// *Provider with an empty apiKey —
	// the SDK accepts empty keys silently, EnsurePlanProducts then dials
	// api.paddle.com (or api.sandbox.paddle.com) with no auth, and the
	// whole boot-time catalog hydration degrades to a per-call 401 that
	// the loader warn-logs once. Every fresh CI runner + dev box that
	// hasn't onboarded Paddle yet would silently hit that path. The B2
	// invariant is "daemon refuses to start without a valid key, not
	// boots and emits 401s at runtime". Fail loud at constructor time so
	// the loader's existing error path returns the operator to a
	// clear "set FAAS_PADDLE_API_KEY" message.
	//
	// Whitespace-only keys are treated as empty (vet-against-env typos
	// like a missed unquoted space from a heredoc). The webhookSecret
	// stays optional for the meterd path (no ingress).
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("paddle: %w (set FAAS_PADDLE_API_KEY=… in sealed.env before selecting Paddle)", ErrNoAPIKey)
	}
	httpClient := &http.Client{Transport: NewIdempotencyRT(http.DefaultTransport)}
	var client *paddle.SDK
	var err error
	if sandbox {
		client, err = paddle.NewSandbox(apiKey, paddle.WithClient(httpClient))
	} else {
		client, err = paddle.New(apiKey, paddle.WithClient(httpClient))
	}
	if err != nil {
		// NewSandbox / New only fail on programmer error (invalid
		// options). Surfacing as a constructor return error means the
		// loader can refuse to start the daemon instead of binding with
		// a half-constructed provider — the operator misconfig becomes
		// a clean boot failure, not a per-method runtime tripwire.
		return nil, fmt.Errorf("paddle: SDK init: %w (sandbox=%t)", err, sandbox)
	}
	return &Provider{
		apiKey:        apiKey,
		webhookSecret: webhookSecret,
		client:        client,
		log:           log,
		catalog:       &priceCatalog{planMonthly: map[api.Plan]string{}, planOverage: map[api.Plan]string{}, planCustomers: map[api.Plan]string{}},
		now:           time.Now,
	}, nil
}

// NewProviderWithDedupe is the meterd-side constructor. Same as
// NewProvider but with the state-store-backed cross-process dedupe
// wired so a meterd crash between the Paddle CreateTransaction POST
// and the in-process `acc.flushed` stamp cannot cause a second POST
// on the next process boot for the same (account, month). apid's path
// uses NewProvider (no ingress from apid writes to the overage
// accumulator; the only accumulator is meterd's).
//
// Keeping this as a separate constructor (rather than a WithDedupe
// option) avoids touching every existing test call site that uses
// NewProvider — the loader is the only caller that needs the dedupe.
func NewProviderWithDedupe(apiKey string, sandbox bool, log *slog.Logger, dedupe PaddleOverageDedupe) (*Provider, error) {
	p, err := NewProvider(apiKey, "", sandbox, log)
	if err != nil {
		return nil, err
	}
	p.dedupe = dedupe
	return p, nil
}

// NewProviderForTest is the test-only constructor that returns a
// *Provider with a stubbed SDK client. Tests inject a flushFn stub
// so the SDK is never invoked. Used by pkg/meter's
// TestPushHour_PaddleDispatchHitsPaddleHistogram to construct a real
// *paddle.Provider concrete type — providerOpsFor's type-switch
// dispatches on concrete type, so the dispatch seam is only
// exercisable with a real *Provider value, not a test fake satisfying
// only the Provider interface.
//
// The SDK is constructed as &paddle.SDK{} (non-nil, zero-value) so
// PushUsageRecord's pre-SDK guards (ErrNoAPIKey on nil client) pass
// through. The flushFn stub intercepts before any real SDK call.
// The apiKey is unused (no real init). The log is required so the
// test caller controls the slog sink; nil falls back to slog.Default().
func NewProviderForTest(log *slog.Logger) *Provider {
	if log == nil {
		log = slog.Default()
	}
	return &Provider{
		apiKey:  "test-key",
		client:  &paddle.SDK{}, // non-nil placeholder; never invoked (flushFn stub intercepts)
		log:     log,
		catalog: &priceCatalog{planMonthly: map[api.Plan]string{}, planOverage: map[api.Plan]string{}, planCustomers: map[api.Plan]string{}},
		now:     time.Now,
	}
}

// FlushFnForTest swaps in a flushFn stub. Tests use this to
// substitute the production defaultFlushLocked with a counter or
// recorder so the SDK is never invoked. Lives at package scope
// (not on the *Provider) so test packages from outside this
// directory can reach for it without exposing the field directly.
//
// Mirrors the pattern at pkg/billing/stripe/client.go where the
// SDK push is also seam-driven for testability.
func (p *Provider) FlushFnForTest(fn FlushFn) {
	p.flushFn = fn
}

// RecordedFlush is one row the FlushRecorder captures per push through
// defaultFlushLocked. The load-bearing field is Quantity — the wire
// quantity posted to Paddle's per-line-item Quantity field. A regression
// in the wire-quantity conversion (e.g. reverting to Quantity=1) is
// caught by asserting records[i].Quantity == expected.
//
// MBSeconds + WindowStart + AccountID are kept for parity with the
// existing FlushFn signature so a test can drive both seams from the
// same input. The CustomData field is intentionally not recorded here
// — it's already pinned by a separate Paddle merchant-dashboard
// inspection; if the audit-trail format ever changes, that's a
// downstream concern.
type RecordedFlush struct {
	AccountID   string
	WindowStart time.Time
	MBSeconds   int64
	Quantity    int64
}

// FlushRecorderForTest installs a recording sink and returns the slice
// the production defaultFlushLocked appends into. The slice is appended
// under flushRecorderMu; tests that read it concurrently must take the
// returned pointer's own mutex or copy the slice first. The seam is
// intentionally minimal — it sits next to FlushFnForTest so a test can
// choose to (a) substitute the production body entirely, (b) let the
// production body run and observe what it posted, or (c) both — install
// a recorder and a flushFn stub to count + capture in the same run.
//
// Two flushes on the same *Provider are serialised by
// flushOverageLocked's per-(acct, window) claim gate, so concurrent
// flushes on distinct windows are still safe to record — the mutex
// protects the slice header, not the row contents.
func (p *Provider) FlushRecorderForTest() *[]RecordedFlush {
	p.flushRecorderMu.Lock()
	defer p.flushRecorderMu.Unlock()
	if p.flushRecorder == nil {
		p.flushRecorder = []RecordedFlush{}
	}
	return &p.flushRecorder
}

// SetOveragePriceForTest primes the catalog's planOverage entry for
// a plan, bypassing EnsurePlanProducts (which requires a live SDK).
// Tests use this to construct a Provider that reaches defaultFlushLocked
// without standing up a real catalog hydration.
func (p *Provider) SetOveragePriceForTest(plan api.Plan, priceID string) {
	p.catalog.mu.Lock()
	defer p.catalog.mu.Unlock()
	if p.catalog.planOverage == nil {
		p.catalog.planOverage = map[api.Plan]string{}
	}
	p.catalog.planOverage[plan] = priceID
}

// SetDedupeForTest swaps the dedupe gate. nil disables the gate —
// useful when the test wants to exercise the flushFn directly
// without driving the claim state machine.
func (p *Provider) SetDedupeForTest(d PaddleOverageDedupe) {
	p.dedupe = d
}

// Compile-time conformance to billing.Provider. Adding a method to the
// interface is a build error here — mirrors pkg/billing/stripe.
var _ billing.Provider = (*Provider)(nil)

// Capabilities returns the Paddle provider's supported optional surfaces.
// Paddle adjustments provide refunds; usage reconciliation remains absent
// because Paddle does not expose a provider usage-summary endpoint.
func (p *Provider) Capabilities() billing.CapabilitySet {
	return PaddleCapabilities()
}

// ---- billing.Provider surface ----

// EnsurePlanProducts: idempotent boot-time setup. Lists products +
// prices; for any missing plan, creates the product, a monthly
// recurring price, and a flat-rate overage line-item price. Matches
// on name prefix `faas-plan-<plan>` so re-running on boot is a
// no-op. Maps onto Paddle's list-then-create pattern (Stripe uses
// Nicknames; Paddle has no equivalent, so we use Name).
//
// Idempotency: redelivered boot on the same platform hits the
// `Status: active` filter on ListProducts, finds the existing
// products/prices, and skips the POST. No merchant-side flag.
func (p *Provider) EnsurePlanProducts(ctx context.Context) error {
	// Defensive: NewProvider returns an error and the loader refuses to
	// start the daemon when the SDK cannot be constructed (B2 invariant).
	// The guard stays so future hand-built *Provider values
	// (NewProviderForTest, future test fixtures) get a descriptive
	// error rather than a nil-panic in ensurePlansAndPrices.
	if p.client == nil {
		return fmt.Errorf("paddle: SDK not initialized (apiKey=%q)", redactAPIKey(p.apiKey))
	}
	if err := p.ensurePlansAndPrices(ctx); err != nil {
		return fmt.Errorf("paddle: ensure plans: %w", err)
	}
	// PR-P3: stamp lastSyncAt under the same catalog mutex the
	// snapshot helpers read through. The snapshot read paths
	// (ListCatalog) hold the RLock; this write acquires the full
	// lock to stay consistent with the catalog map updates inside
	// ensureProducts.
	p.catalog.mu.Lock()
	p.lastSyncAt = p.now()
	p.catalog.mu.Unlock()
	p.log.Info("paddle: EnsurePlanProducts complete", "monthly", p.snapshotPlans(), "overage", p.snapshotOverage())
	return nil
}

func (p *Provider) ensurePlansAndPrices(ctx context.Context) error {
	// Implementation lives in products.go. Kept as a thin forward so
	// provider.go stays a Provider-surface file.
	return p.ensureProducts(ctx)
}

// PushUsageRecord: per-push stateless overage flush. Paddle Billing v2
// has no equivalent of Stripe's metered subscription_item — the shape
// is a single Transactions POST with a price_id (the overage line item)
// and quantity 1. The meterd pusher loop sums that month's mb_seconds
// from usage_minutes rows on every tick and calls this with the sum.
//
// The pre-SDK guards (no apiKey, negative qty, missing overage price)
// return sentinels from usage.go so the classifier at errors.go can
// map them to stable Prometheus labels. Adding a new pre-SDK failure
// mode requires adding a sentinel + a label — the closed label set is
// the dashboard's panel surface, so the change is deliberate.
//
// Concurrency: meter (cmd/meterd) calls this from a single loop
// goroutine; apid's webhook handler does not. The meter's loop
// holds a single contract: at most one outstanding call per
// (acct.ID, month). Tests pin that contract.
//
// Idempotency: each call carries an Idempotency-Key header derived
// from (acct.ID, month) via the NewIdempotencyRT wrapper installed
// at NewProvider. The cross-process dedupe gate (HasPaddleOverageMonth
// / RecordPaddleOverageMonth) collapses on the same shape so a
// redelivered month — across a meterd restart or a stripe-vs-paddle
// test path — is a no-op before the SDK is invoked. Paddle's API
// server may not honor Idempotency-Key today (SDK team is working
// on native support); the header presence is observable on the wire
// for ops debugging and is forward-compat.
func (p *Provider) PushUsageRecord(ctx context.Context, acct state.Account, hour time.Time, mbSeconds int64) error {
	// Pre-SDK guard for the misconfigured case. Today this branch is
	// unreachable from production paths (NewProvider returns an error
	// and the loader refuses to start the daemon when the SDK cannot
	// be constructed) — the guard stays as a defensive runtime check
	// in case the *Provider is hand-constructed in a future caller
	// (e.g. a test, or a one-off script).
	if p.client == nil {
		return fmt.Errorf("%w (account %s)", ErrNoAPIKey, acct.ID)
	}
	if acct.Email == "" {
		return errors.New("paddle: PushUsageRecord requires acct.Email")
	}
	if mbSeconds < 0 {
		return fmt.Errorf("paddle: PushUsageRecord: %w (account %s, qty %d)", ErrNegativeMBSeconds, acct.ID, mbSeconds)
	}
	if mbSeconds == 0 {
		// Defensive: meterd pusher loop filters 0-sum pushes before
		// calling us, but the guard is here for future callers (tests,
		// other ingress paths). flushOverageLocked has the same guard so
		// both surfaces are idempotent on 0.
		return nil
	}
	return p.flushOverageLocked(ctx, acct, hour, mbSeconds)
}

// VerifyWebhook: HMAC-SHA256 over "<unix>:<body>" with the
// Paddle-Signature header's h1= value. Constant-time compare via
// crypto/hmac.Equal (same pattern as pkg/billing/stripe/webhook.go
// but with Paddle's `: ` separator instead of Stripe's `.`).
//
// Header format: `ts=<unix>;h1=<hex-sha256>`. Captured by regex;
// the timestamp is unix-seconds (matching Stripe's t= value for
// interface symmetry).
//
// Returns billing.Event with normalized EventType. mapping in
// mapPaddleEventType; unknown events render as EventUnknown so
// apid's switch falls through to a 200 no-op (Paddle retries on
// 5xx; we 200 unknown types so it doesn't retry forever).
func (p *Provider) VerifyWebhook(payload []byte, headers map[string]string, tolerance time.Duration) (billing.Event, error) {
	if p.webhookSecret == "" {
		return billing.Event{}, fmt.Errorf("paddle: %w: empty webhook secret", billing.ErrBadSignature)
	}
	sigHeader := headers["Paddle-Signature"]
	if sigHeader == "" {
		sigHeader = headers["paddle-signature"]
	}
	if sigHeader == "" {
		return billing.Event{}, fmt.Errorf("paddle: %w: missing Paddle-Signature header", billing.ErrBadSignature)
	}
	if err := verifyPaddleSignature(payload, sigHeader, p.webhookSecret, tolerance); err != nil {
		return billing.Event{}, err
	}
	return parsePaddleEvent(payload, p)
}

// ReconcileUsage is the drift detector seam (ADR-049 §B.1). Paddle
// Billing does not yet expose a usage-summary endpoint, so the
// reconciler observes ErrNotImplemented and skips Paddle accounts.
// When Paddle adds the surface, this implementation will call it
// and return the mb_seconds total for [start, end).
func (p *Provider) ReconcileUsage(_ context.Context, _ state.Account, _, _ time.Time) (int64, error) {
	return 0, billing.ErrNotImplemented
}
