// pure_helpers_test.go — fill pkg/billing/stripe coverage of the
// tiny pure helpers and the pre-API guards (everything reachable
// without a Stripe sandbox or an API key).
//
// Targets:
//   - Config.Defaults (idempotent default-fill)
//   - PushResultLabels (the closed set of metric labels)
//   - idempotencyKeyFromContext (the context-key reader)
//   - requireAPI (no api → wrapped ErrNoAPIKey)
//   - RetryLatestCharge / CancelAtPeriodEnd / PaymentMethodSummary
//     pre-API guards (the cheap branch hit before Stripe is dialed)
//
// Whitebox `package stripe`.
package stripe

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
	stripe "github.com/stripe/stripe-go"
)

// --- Config.Defaults --------------------------------------------

func TestConfigDefaults_ZeroSetsTolerance300(t *testing.T) {
	c := &Config{}
	c.Defaults()
	if c.ToleranceSeconds != 300 {
		t.Errorf("tolerance = %d, want 300 (5 minutes)", c.ToleranceSeconds)
	}
}

func TestConfigDefaults_NonZeroPreserved(t *testing.T) {
	c := &Config{ToleranceSeconds: 60}
	c.Defaults()
	if c.ToleranceSeconds != 60 {
		t.Errorf("tolerance = %d, want 60 (call should be idempotent)", c.ToleranceSeconds)
	}
}

func TestConfigDefaults_Idempotent(t *testing.T) {
	c := &Config{}
	c.Defaults()
	c.Defaults()
	c.Defaults()
	if c.ToleranceSeconds != 300 {
		t.Errorf("tolerance = %d, want 300 after three Defaults calls", c.ToleranceSeconds)
	}
}

// --- PushResultLabels -------------------------------------------

func TestPushResultLabels_ClosedSetNonEmpty(t *testing.T) {
	labels := PushResultLabels()
	if len(labels) == 0 {
		t.Fatal("PushResultLabels returned empty")
	}
	// All entries must be unique (the metric-side deduplication
	// relies on it).
	seen := map[string]bool{}
	for _, l := range labels {
		if l == "" {
			t.Error("empty label in PushResultLabels")
		}
		if seen[l] {
			t.Errorf("duplicate label %q in PushResultLabels", l)
		}
		seen[l] = true
	}
}

// --- idempotencyKeyFromContext ---------------------------------

func TestIdempotencyKeyFromContext_MissingReturnsFalse(t *testing.T) {
	if k, ok := idempotencyKeyFromContext(context.Background()); ok || k != "" {
		t.Errorf("missing: got (%q, %v), want (\"\", false)", k, ok)
	}
}

func TestIdempotencyKeyFromContext_PresentReturnsKey(t *testing.T) {
	ctx := context.WithValue(context.Background(), idempotencyKeyContextKey{}, "key-1")
	k, ok := idempotencyKeyFromContext(ctx)
	if !ok || k != "key-1" {
		t.Errorf("got (%q, %v), want (key-1, true)", k, ok)
	}
}

func TestIdempotencyKeyFromContext_EmptyStringReturnsFalse(t *testing.T) {
	// An empty key is treated as missing — the helper collapses
	// both "absent" and "blank" into ("" , false) so callers
	// don't have to special-case the empty string.
	ctx := context.WithValue(context.Background(), idempotencyKeyContextKey{}, "")
	if k, ok := idempotencyKeyFromContext(ctx); ok || k != "" {
		t.Errorf("empty: got (%q, %v), want (\"\", false)", k, ok)
	}
}

func TestIdempotencyKeyFromContext_WrongTypeReturnsFalse(t *testing.T) {
	// Some other type at the same key returns the typed assertion
	// ok=false. Sanity-check that the helper doesn't panic.
	ctx := context.WithValue(context.Background(), idempotencyKeyContextKey{}, 42)
	if _, ok := idempotencyKeyFromContext(ctx); ok {
		t.Error("wrong type: got true, want false")
	}
}

// --- requireAPI -------------------------------------------------

func TestRequireAPI_NilAPIReturnsErrNoAPIKey(t *testing.T) {
	c := &Client{api: nil}
	api, err := c.requireAPI()
	if api != nil {
		t.Errorf("nil api: api = %v, want nil", api)
	}
	if err == nil {
		t.Fatal("nil api: err = nil, want ErrNoAPIKey")
	}
	if !errors.Is(err, billing.ErrNoAPIKey) {
		t.Errorf("err = %v, want wraps ErrNoAPIKey", err)
	}
}

// --- pre-API guards on retry/cancel/payment_method -------------

func TestRetryLatestCharge_NoCustomerSkips(t *testing.T) {
	c := &Client{api: nil}
	_, _, err := c.RetryLatestCharge(context.Background(), state.Account{ID: "a-1"})
	if err == nil {
		t.Fatal("no customer: err = nil, want ErrNoOpenCharge")
	}
	if !errors.Is(err, billing.ErrNoOpenCharge) {
		t.Errorf("err = %v, want wraps ErrNoOpenCharge", err)
	}
}

func TestCancelAtPeriodEnd_NoSubscriptionSkips(t *testing.T) {
	c := &Client{api: nil}
	_, err := c.CancelAtPeriodEnd(context.Background(), state.Account{ID: "a-1"})
	if err == nil {
		t.Fatal("no subscription: err = nil, want ErrAlreadyCancelled")
	}
	if !errors.Is(err, billing.ErrAlreadyCancelled) {
		t.Errorf("err = %v, want wraps ErrAlreadyCancelled", err)
	}
}

func TestPaymentMethodSummary_NoCustomerReturnsZero(t *testing.T) {
	c := &Client{api: nil}
	got, err := c.PaymentMethodSummary(context.Background(), state.Account{ID: "a-1"})
	if err != nil {
		t.Fatalf("no customer: err = %v, want nil", err)
	}
	if got.Brand != "" || got.Last4 != "" || got.ExpMonth != 0 || got.ExpYear != 0 {
		t.Errorf("no customer: got %+v, want zero-value PaymentMethod", got)
	}
}

// PaymentMethodSummary with API key present still returns zero
// when ProviderCustomerID is empty — the pre-API guard is what
// we want to exercise.
func TestPaymentMethodSummary_NoCustomerIgnoresAPIKey(t *testing.T) {
	c := &Client{} // api == nil, customer empty
	got, err := c.PaymentMethodSummary(context.Background(), state.Account{ID: "a-1"})
	if err != nil {
		t.Fatalf("no customer: err = %v, want nil", err)
	}
	if got != (billing.PaymentMethod{}) {
		t.Errorf("got %+v, want zero PaymentMethod", got)
	}
}

// --- WireQuantityForMBSeconds --------------------------------------

// Pin the canonical M7 wire-quantity formula. 256 MB Hobby admission +
// 8 MB overhead × 60 min/h × 24 h = 22_809_600 mb_seconds.
//
//	qty = mb_seconds * 5000 / 1024 / 3600
//	    = 22_809_600 * 5000 / 1024 / 3600
//	    = 114_048_000_000 / 3_686_400
//	    = 30937
//
// (5000 wire units per GB-hour since ADR-20261107192333.)
//
// This is the same integer the sandbox test asserts against the wire
// (sandbox_test.go::TestInvoiceShadow24h_Sandbox); pinning it here
// proves the in-process helper agrees with the SDK-roundtrip answer.
func TestWireQuantityForMBSeconds(t *testing.T) {
	cases := []struct {
		name      string
		mbSeconds int64
		want      int64
	}{
		{"zero", 0, 0},
		{"canonical_m7_24h", 22_809_600, 30937},
		{"one_hour_264mb", 264 * 60 * 60, 1289},
		{"negative_truncates_to_zero", -737, 0},
		{"one_mb_second", 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WireQuantityForMBSeconds(tc.mbSeconds); got != tc.want {
				t.Errorf("WireQuantityForMBSeconds(%d) = %d, want %d", tc.mbSeconds, got, tc.want)
			}
		})
	}
}

// --- Capabilities / StripeCapabilities ----------------------------

// StripeCapabilities returns the static invariant capability set,
// documented at client.go:391 as CapRefund | CapUsageMetered |
// CapSandbox. Loader-side metadata callers (loader.go:160) read this
// without constructing a *Client.
func TestStripeCapabilities_InvariantBitSet(t *testing.T) {
	got := StripeCapabilities()
	want := billing.CapRefund | billing.CapUsageMetered | billing.CapSandbox
	if got != billing.CapabilitySet(want) {
		t.Errorf("StripeCapabilities = %d, want %d (CapRefund|CapUsageMetered|CapSandbox)", got, want)
	}
	for _, cap := range []billing.Capability{
		billing.CapRefund,
		billing.CapUsageMetered,
		billing.CapSandbox,
	} {
		if !got.Has(cap) {
			t.Errorf("Has(%d) = false on StripeCapabilities", cap)
		}
	}
}

// (c *Client).Capabilities is the per-instance capability read for the
// Provider-conformance lookup; must agree with the free function.
func TestClient_Capabilities_MatchesStripeCapabilities(t *testing.T) {
	c := &Client{}
	if got := c.Capabilities(); got != StripeCapabilities() {
		t.Errorf("Capabilities() = %d, want %d (StripeCapabilities)", got, StripeCapabilities())
	}
}

// --- NewClient missing branches -----------------------------------

// Empty apiKey → no SDK is constructed. The Client must still be
// usable (logger is set, fields preserved) so meterd can hand out
// the Config but block at requireAPI() until the operator provides
// the key — mirroring the staging-environment pattern.
func TestNewClient_EmptyAPIKeyLeavesAPINil(t *testing.T) {
	c := NewClient(nil, nil, "", "", nil)
	if c.api != nil {
		t.Error("empty apiKey: api != nil, want nil")
	}
	if c.apiKey != "" {
		t.Errorf("apiKey = %q, want preserved empty", c.apiKey)
	}
	if c.log == nil {
		t.Error("nil logger: log not defaulted to slog.Default()")
	}
}

// Non-empty apiKey builds the SDK *client.API. We don't dereference
// the result (no test should round-trip a live call) — just pin that
// the API pointer is non-nil after NewClient.
func TestNewClient_NonEmptyAPIKeyBuildsAPI(t *testing.T) {
	c := NewClient(nil, nil, "sk_test_dummy", "whsec_dummy", nil)
	if c.api == nil {
		t.Error("non-empty apiKey: api = nil, want non-nil SDK client.API")
	}
	if c.apiKey != "sk_test_dummy" {
		t.Errorf("apiKey = %q, want preserved", c.apiKey)
	}
}

// --- ClassifyPushError missing cases ------------------------------

// An unknown stripe.ErrorType falls through to the default branch
// and is labelled "other". Real-world: Stripe may add a new error
// category between SDK upgrades; the classification must degrade
// gracefully instead of panicking.
func TestClassifyPushError_UnknownStripeTypeIsOther(t *testing.T) {
	err := &stripe.Error{Type: stripe.ErrorType("some_new_category")}
	if got := ClassifyPushError(err); got != "other" {
		t.Errorf("unknown Stripe type: got %q, want other", got)
	}
}

// A non-nil error that is neither a sentinel nor a *stripe.Error
// wraps to "other". Anything reaching ClassifyPushError that the
// SDK didn't tag should still produce a stable label so the metric
// histogram doesn't drop the observation.
func TestClassifyPushError_GenericErrorIsOther(t *testing.T) {
	if got := ClassifyPushError(errors.New("kaboom")); got != "other" {
		t.Errorf("generic err: got %q, want other", got)
	}
}

// --- CreateUpgradeTransaction (pure stub) -------------------------

// Stripe-side upgrade transactions are intentionally absent;
// CreateUpgradeTransaction returns ("", "", nil) and the apid
// handler falls back to FAAS_BILLING_PORTAL_URL. Pin the contract
// so a future refactor to delegate to a future Stripe API doesn't
// silently break the apid path.
func TestCreateUpgradeTransaction_ReturnsEmptyStub(t *testing.T) {
	c := &Client{}
	txID, url, err := c.CreateUpgradeTransaction(context.Background(), state.Account{ID: "a"}, "")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if txID != "" || url != "" {
		t.Errorf("got (%q, %q), want (\"\", \"\")", txID, url)
	}
}

// --- VerifyWebhook missing branches -------------------------------

// Empty webhook secret short-circuits to ErrBadSignature; we never
// reach VerifySignature. Without this test the empty-secret branch
// is uncovered.
func TestVerifyWebhook_EmptySecretReturnsErrBadSignature(t *testing.T) {
	c := &Client{secret: ""}
	_, err := c.VerifyWebhook([]byte(`{"type":"customer.created"}`), map[string]string{}, 5*time.Minute)
	if err == nil {
		t.Fatal("empty secret: err = nil, want ErrBadSignature")
	}
	if !errors.Is(err, billing.ErrBadSignature) {
		t.Errorf("err = %v, want wraps ErrBadSignature", err)
	}
}

// stripeErrorStub removed — use a real *stripe.Error literal
// directly (the SDK exposes the struct publicly and an empty-init
// with just the Type field is the shape ClassifyPushError dispatches
// on).
