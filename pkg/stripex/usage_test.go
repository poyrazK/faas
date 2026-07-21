package stripex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	stripe "github.com/stripe/stripe-go"
)

// TestPushUsageRecord_NoSubscriptionItemSkips asserts the
// subscription-item-empty short-circuit — pending customers have no
// sub_item yet so the SDK call is bypassed.
func TestPushUsageRecord_NoSubscriptionItemSkips(t *testing.T) {
	store := state.NewMemStore()
	c := NewClient(store, store, "sk_test_dummy", "whsec_dummy", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := c.PushUsageRecord(context.Background(), state.Account{ID: "acct_pending"}, time.Now(), 1.5); err != nil {
		t.Fatalf("PushUsageRecord with empty subscription item returned error: %v", err)
	}
}

// TestPushUsageRecord_NoCustomerSkips is the prior skip semantics. We
// keep it as a regression guard alongside the subscription-item check.
func TestPushUsageRecord_NoCustomerSkips(t *testing.T) {
	store := state.NewMemStore()
	c := NewClient(store, store, "sk_test_dummy", "whsec_dummy", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := c.PushUsageRecord(context.Background(), state.Account{ID: "acct_no_customer"}, time.Now(), 1.5); err != nil {
		t.Fatalf("PushUsageRecord with empty customer ID returned error: %v", err)
	}
}

// TestPushUsageRecord_MissingAPIKeyFails asserts the no-key path
// returns an error rather than silently dropping the bill (the meterd
// log line is the operator signal on a misconfigured deployment). The
// classifier identifies it via errors.Is(err, ErrNoAPIKey) so the test
// pins the sentinel contract, not a string fragment.
func TestPushUsageRecord_MissingAPIKeyFails(t *testing.T) {
	store := state.NewMemStore()
	c := NewClient(store, store, "", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := c.pushUsageRecordSDK(context.Background(), state.Account{
		ID: "acct_x", StripeCustomerID: "cus_x", StripeSubscriptionItem: "si_test",
	}, time.Now(), 1.5)
	if err == nil || !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("expected error wrapping ErrNoAPIKey, got %v", err)
	}
}

// TestPushUsageRecord_DedupeGateSkipsSecondCall exercises the
// in-memory dedupe table that wraps the SDK: two pushes for the same
// hour stamp only one dedupe row. The unit-level SDK transport
// coverage is the live-sandbox test (skipped without STRIPE_API_KEY);
// at this level we can verify dedupe deterministically without a
// network.
func TestPushUsageRecord_DedupeGateSkipsSecondCall(t *testing.T) {
	store := state.NewMemStore()
	c := NewClient(store, store, "sk_test_dummy", "whsec_dummy", slog.New(slog.NewTextHandler(io.Discard, nil)))
	acct := state.Account{ID: "acct_dedupe", StripeCustomerID: "cus_x", StripeSubscriptionItem: "si_test"}
	hour := time.Now().UTC().Truncate(time.Hour)

	if err := store.RecordStripePushHour(context.Background(), acct.ID, hour); err != nil {
		t.Fatalf("seed dedupe: %v", err)
	}
	// Dedup hit short-circuits before the SDK call; no network needed.
	if err := c.PushUsageRecord(context.Background(), acct, hour, 1.5); err != nil {
		t.Fatalf("PushUsageRecord with dedupe hit returned error: %v", err)
	}
}

// TestPushUsageRecord_PostsToStripeSandbox is the live-sandbox
// acceptance test (issue #52). Skipped unless STRIPE_API_KEY is
// exported to a real sk_test_… key AND FATEST_STRIPE_SUB_ITEM is set
// to a sandbox subscription_item. Run locally with:
//
//	STRIPE_API_KEY=sk_test_... FATEST_STRIPE_SUB_ITEM=si_... go test -run PostsToStripeSandbox ./pkg/stripex/...
//
// Asserts the SDK returned a usage record with a non-empty ID prefixed
// "mbur_" (Stripe's usage-record prefix). On CI this runs under
// .github/workflows/sandbox.yml (workflow_dispatch only); see PR #59.
func TestPushUsageRecord_PostsToStripeSandbox(t *testing.T) {
	key := os.Getenv("STRIPE_API_KEY")
	sub := os.Getenv("FATEST_STRIPE_SUB_ITEM")
	if key == "" || !strings.HasPrefix(key, "sk_test_") || sub == "" {
		t.Skip("STRIPE_API_KEY / FATEST_STRIPE_SUB_ITEM not configured; skipping live POST")
	}
	store := state.NewMemStore()
	c := NewClient(store, store, key, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Past hour keeps the test idempotent across reruns: a second
	// invocation targets a different hour and never double-bills.
	hour := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	record, err := c.PushUsageRecordWithID(context.Background(), state.Account{
		ID:                     "acct_sandbox_" + hour.Format("2006010215"),
		StripeCustomerID:       "cus_sandbox",
		StripeSubscriptionItem: sub,
	}, hour, 0.001) // 1 MB-h keeps the sandbox bill line tiny
	if err != nil {
		t.Fatalf("PushUsageRecordWithID against sandbox: %v", err)
	}
	if record == nil {
		t.Fatal("PushUsageRecordWithID returned nil record with no error")
	}
	if record.ID == "" {
		t.Fatal("Stripe usage record ID is empty")
	}
	if !strings.HasPrefix(record.ID, "mbur_") {
		t.Fatalf("Stripe usage record ID %q does not start with mbur_", record.ID)
	}
}

// TestPushUsageRecord_ValidationNegativeQuantity ensures the
// defensive guard at pushUsageRecordSDK rejects negative quantities
// (which would silently credit the customer instead of billing). The
// classifier identifies it via errors.Is(err, ErrNegativeQuantity).
func TestPushUsageRecord_ValidationNegativeQuantity(t *testing.T) {
	store := state.NewMemStore()
	c := NewClient(store, store, "sk_test_x", "whsec_x", slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := c.pushUsageRecordSDK(context.Background(), state.Account{
		ID: "acct_neg", StripeCustomerID: "cus_x", StripeSubscriptionItem: "si_test",
	}, time.Now(), -0.5)
	if err == nil || !errors.Is(err, ErrNegativeQuantity) {
		t.Fatalf("expected error wrapping ErrNegativeQuantity, got %v", err)
	}
}

// --- ClassifyPushError ---

// wrapStripeError mirrors usage.go:72's wrap shape so the classifier
// exercises the real errors.As path. Wrapped (not bare) because that's
// what the pusher sees in production.
func wrapStripeError(se *stripe.Error) error {
	return fmt.Errorf("stripex: UsageRecords.New account %s hour %s: %w",
		"acct_test", time.Now().UTC().Format(time.RFC3339), se)
}

// TestClassifyPushError_Nil — nil maps to "ok" so the pusher can
// observe success and failure through the same code path. Documents
// the contract that "ok" is the success label, not a sentinel for
// "skip".
func TestClassifyPushError_Nil(t *testing.T) {
	if got := ClassifyPushError(nil); got != "ok" {
		t.Errorf("ClassifyPushError(nil) = %q, want \"ok\"", got)
	}
}

// TestClassifyPushError_NoAPIKey — pre-SDK error from
// pushUsageRecordSDK when apiKey is empty. The classifier matches the
// ErrNoAPIKey sentinel via errors.Is, so the wrapped message can
// carry diagnostic context (account id) without affecting the label.
func TestClassifyPushError_NoAPIKey(t *testing.T) {
	err := fmt.Errorf("%w (account %s)", ErrNoAPIKey, "acct_x")
	if got := ClassifyPushError(err); got != "no-api-key" {
		t.Errorf("ClassifyPushError(no-api-key err) = %q, want \"no-api-key\"", got)
	}
}

// TestClassifyPushError_NegativeQuantity — pre-SDK error from the
// defensive quantity guard at usage.go:74-80. Same sentinel-based
// pattern as NoAPIKey.
func TestClassifyPushError_NegativeQuantity(t *testing.T) {
	err := fmt.Errorf("%w (account %s, qty %d)", ErrNegativeQuantity, "acct_x", -5)
	if got := ClassifyPushError(err); got != "negative-quantity" {
		t.Errorf("ClassifyPushError(negative-quantity err) = %q, want \"negative-quantity\"", got)
	}
}

// TestClassifyPushError_StripeErrorCard — most common production
// failure mode: customer's card declined. Routed to its own label so
// the dashboard can distinguish "Stripe is throttling us" from
// "this customer's card bounced".
func TestClassifyPushError_StripeErrorCard(t *testing.T) {
	err := wrapStripeError(&stripe.Error{Type: stripe.ErrorTypeCard, HTTPStatusCode: 402})
	if got := ClassifyPushError(err); got != "card-error" {
		t.Errorf("ClassifyPushError(card) = %q, want \"card-error\"", got)
	}
}

// TestClassifyPushError_StripeErrorRateLimit — Stripe's 429 response.
// Critical to separate from card-error because the alert path is
// completely different (back off vs contact customer).
func TestClassifyPushError_StripeErrorRateLimit(t *testing.T) {
	err := wrapStripeError(&stripe.Error{Type: stripe.ErrorTypeRateLimit, HTTPStatusCode: 429})
	if got := ClassifyPushError(err); got != "rate-limit" {
		t.Errorf("ClassifyPushError(rate-limit) = %q, want \"rate-limit\"", got)
	}
}

// TestClassifyPushError_StripeErrorInvalidRequest — 4xx that's not a
// card issue. Indicates a meterd bug (bad params) rather than a
// customer or Stripe-side problem.
func TestClassifyPushError_StripeErrorInvalidRequest(t *testing.T) {
	err := wrapStripeError(&stripe.Error{Type: stripe.ErrorTypeInvalidRequest, HTTPStatusCode: 400})
	if got := ClassifyPushError(err); got != "invalid-request" {
		t.Errorf("ClassifyPushError(invalid-request) = %q, want \"invalid-request\"", got)
	}
}

// TestClassifyPushError_StripeErrorUnknownType — future-proofing:
// Stripe adding a new ErrorType (or a malformed response) lands in
// "other" rather than silently drifting to "err" / "ok".
func TestClassifyPushError_StripeErrorUnknownType(t *testing.T) {
	err := wrapStripeError(&stripe.Error{Type: stripe.ErrorType("future_error"), HTTPStatusCode: 500})
	if got := ClassifyPushError(err); got != "other" {
		t.Errorf("ClassifyPushError(unknown-type) = %q, want \"other\"", got)
	}
}

// TestClassifyPushError_UnknownError — any non-Stripe error from the
// pusher path (e.g. dedupe table lookup failure) lands in "other".
// Exercises the !errors.As branch.
func TestClassifyPushError_UnknownError(t *testing.T) {
	err := fmt.Errorf("meter: usage_by_hour: connection refused")
	if got := ClassifyPushError(err); got != "other" {
		t.Errorf("ClassifyPushError(generic) = %q, want \"other\"", got)
	}
}
