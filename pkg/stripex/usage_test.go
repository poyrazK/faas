package stripex

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
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
// log line is the operator signal on a misconfigured deployment).
func TestPushUsageRecord_MissingAPIKeyFails(t *testing.T) {
	store := state.NewMemStore()
	c := NewClient(store, store, "", "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := c.pushUsageRecordSDK(context.Background(), state.Account{
		ID: "acct_x", StripeCustomerID: "cus_x", StripeSubscriptionItem: "si_test",
	}, time.Now(), 1.5)
	if err == nil || !strings.Contains(err.Error(), "apiKey") {
		t.Fatalf("expected error mentioning apiKey, got %v", err)
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
	err := c.PushUsageRecord(context.Background(), state.Account{
		ID:                     "acct_sandbox",
		StripeCustomerID:       "cus_sandbox",
		StripeSubscriptionItem: sub,
	}, hour, 0.001) // 1 MB-h keeps the sandbox bill line tiny
	if err != nil {
		t.Fatalf("PushUsageRecord against sandbox: %v", err)
	}
}

// TestPushUsageRecord_ValidationNegativeQuantity ensures the
// defensive guard at pushUsageRecordSDK rejects negative quantities
// (which would silently credit the customer instead of billing).
func TestPushUsageRecord_ValidationNegativeQuantity(t *testing.T) {
	store := state.NewMemStore()
	c := NewClient(store, store, "sk_test_x", "whsec_x", slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := c.pushUsageRecordSDK(context.Background(), state.Account{
		ID: "acct_neg", StripeCustomerID: "cus_x", StripeSubscriptionItem: "si_test",
	}, time.Now(), -0.5)
	if err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("expected error mentioning negative quantity, got %v", err)
	}
}
