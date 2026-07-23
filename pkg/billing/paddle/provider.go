package paddle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/PaddleHQ/paddle-go-sdk/v5"
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
	// pendingOverage accumulates mb_seconds between month-rollovers,
	// keyed by acct.ID. PushUsageRecord adds to it on every non-rollover
	// call; FlushOverageNow emits the prior-month line item (idempotent
	// on redelivery via Paddle's Idempotency-Key header) — called by
	// meterd's quota + dunning timers when the calendar month rolls
	// over. Mutex serializes the per-account accumulation.
	pendingOverage sync.Map // map[string]*overageAccumulator
	flushFn        FlushFn  // test seam; nil → defaultFlushLocked (production)
	now            func() time.Time
}

// NewProvider wires the Paddle v5 SDK. sandbox=true →
// api.sandbox.paddle.com (operator's free sandbox); false →
// api.paddle.com (production).
//
// Catalog + time hooks are initialized lazily so tests can construct
// without live configuration. EnsurePlanProducts must be called
// before PushUsageRecord / CreateCustomer in production; both fail
// fast with a descriptive error if the catalog is empty.
func NewProvider(apiKey, webhookSecret string, sandbox bool, log *slog.Logger) *Provider {
	if log == nil {
		log = slog.Default()
	}
	var client *paddle.SDK
	var err error
	if sandbox {
		client, err = paddle.NewSandbox(apiKey)
	} else {
		client, err = paddle.New(apiKey)
	}
	if err != nil {
		// NewSandbox / New only fail on programmer error (invalid
		// options); surface loudly so the daemon doesn't bind silently.
		log.Error("paddle: SDK init failed", "err", err, "sandbox", sandbox)
	}
	return &Provider{
		apiKey:        apiKey,
		webhookSecret: webhookSecret,
		client:        client,
		log:           log,
		catalog:       &priceCatalog{planMonthly: map[api.Plan]string{}, planOverage: map[api.Plan]string{}, planCustomers: map[api.Plan]string{}},
		now:           time.Now,
	}
}

// Compile-time conformance to billing.Provider. Adding a method to the
// interface is a build error here — mirrors pkg/billing/stripe.
var _ billing.Provider = (*Provider)(nil)

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
	if p.client == nil {
		return fmt.Errorf("paddle: SDK not initialized (apiKey=%q)", redactAPIKey(p.apiKey))
	}
	if err := p.ensurePlansAndPrices(ctx); err != nil {
		return fmt.Errorf("paddle: ensure plans: %w", err)
	}
	p.log.Info("paddle: EnsurePlanProducts complete", "monthly", p.snapshotPlans(), "overage", p.snapshotOverage())
	return nil
}

func (p *Provider) ensurePlansAndPrices(ctx context.Context) error {
	// Implementation lives in products.go. Kept as a thin forward so
	// provider.go stays a Provider-surface file.
	return p.ensureProducts(ctx)
}

// PushUsageRecord: per-hour int64 mb_seconds accumulation that
// flushes the prior month's value to Paddle as a flat-rate line
// item when the calendar month rolls over. Paddle Billing v2 has
// no equivalent of Stripe's metered subscription_item — the
// closest shape is a single Transactions POST with a price_id
// (the overage line item) and quantity 1.
//
// Concurrency: meter (cmd/meterd) calls this from a single loop
// goroutine; apid's webhook handler does not. The meter's loop
// holds a single contract: at most one outstanding call per
// (acct.ID, hour-or-month). Tests pin that contract.
//
// Idempotency: each Cross-Month flush carries an Idempotency-Key
// header derived from (acct.ID, prior-month) so a redelivered month-
// rollover is a no-op — the same pattern Stripe's Idempotency-Key
// contract translates to directly (PR #1 surface).
func (p *Provider) PushUsageRecord(ctx context.Context, acct state.Account, hour time.Time, mbSeconds int64) error {
	if p.client == nil {
		return fmt.Errorf("paddle: SDK not initialized")
	}
	if acct.Email == "" {
		return errors.New("paddle: PushUsageRecord requires acct.Email")
	}
	if mbSeconds < 0 {
		return errors.New("paddle: PushUsageRecord rejects negative mb_seconds")
	}
	return p.accumulateOverage(ctx, acct, hour, mbSeconds)
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
	return parsePaddleEvent(payload)
}
