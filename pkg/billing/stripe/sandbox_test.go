package stripe

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// --- §14 M7 acceptance gate (live sandbox) ---

// TestInvoiceShadow24h_Sandbox is the §14 M7 invoice-shadow
// acceptance gate (issue #52). The local mirrors at
// pkg/meter/meter_test.go::TestInvoiceShadow24h and
// pkg/meter/pusher_shadow_test.go::TestPushHour_Shadow24h prove the
// same math in-process; this test proves the wire is also clean.
//
// Skipped unless STRIPE_API_KEY is exported to a real `sk_test_…` key
// AND FATEST_STRIPE_SUB_ITEM is set to a sandbox subscription_item.
// Run locally with:
//
//	STRIPE_API_KEY=sk_test_… FATEST_STRIPE_SUB_ITEM=si_… \
//	  go test -v -run 'TestInvoiceShadow24h_Sandbox' ./pkg/billing/stripe/...
//
// The test plants 24 h of billable usage via state.MemStore.AppendUsage,
// accumulates the mb_seconds integer sum, calls
// PushUsageRecordSumWithID once against the real Stripe SDK, and
// asserts `record.Quantity == 6187` exactly — the integer answer for
// (256 MB Hobby admission + 8 MB overhead) * 60 min/h * 24 h, run
// through the wire-quantity formula
//
//	qty = mbSeconds * 1000 / 1024 / 3600
//
// with mbSeconds = api.BillableRAMMB(256) * 60 * 60 * 24 =
// 264 * 86_400 = 22_809_600. The hand-computed expected value is the
// spec's M7 acceptance ("< 0.1 % delta"); we assert the stronger 0 %
// delta — the integer-arithmetic wire path is deterministic, so any
// drift at this layer means the SDK call site is broken.
//
// Account-id strategy: a distinct acct_<YYYYMMDD> per test day so the
// (account, hour) dedupe gate is disjoint across reruns. The past-day
// anchor (not today) avoids dedupe collisions with a prior local
// Postman double-poke and matches the convention already used by
// TestPushUsageRecord_PostsToStripeSandbox.
func TestInvoiceShadow24h_Sandbox(t *testing.T) {
	key := os.Getenv("STRIPE_API_KEY")
	sub := os.Getenv("FATEST_STRIPE_SUB_ITEM")
	if key == "" || !strings.HasPrefix(key, "sk_test_") || sub == "" {
		t.Skip("STRIPE_API_KEY / FATEST_STRIPE_SUB_ITEM not configured; skipping live 24h POST")
	}
	store := state.NewMemStore()
	c := NewClient(store, store, key, "", slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Anchor: yesterday at UTC midnight. Truncating to the day
	// boundary then subtracting 24h keeps the test idempotent across
	// reruns within the same UTC day — the past-day hour-window is
	// disjoint from anything a current production meterd would push.
	startUTC := time.Now().UTC().Truncate(24 * time.Hour).Add(-24 * time.Hour)
	// Account id includes a per-run suffix so two reruns within the
	// same UTC day never collide on the (account, hour) dedupe gate.
	// The dedupe key here is (acctID, startUTC) — startUTC is stable
	// across reruns (both pick "yesterday 00:00 UTC"), so acctID has
	// to vary. The legacy TestPushUsageRecord_PostsToStripeSandbox
	// uses hour-precision on the date because *its* hour varies per
	// run (it picks the previous top-of-hour, not a stable anchor).
	// Suffixing on UnixNano() is unique enough; the test never
	// re-derives the id from anywhere else.
	acctID := fmt.Sprintf("acct_sandbox24h_%s_%d", startUTC.Format("20060102"), time.Now().UTC().UnixNano())
	acct := state.Account{
		ID:                     acctID,
		StripeCustomerID:       "cus_sandbox",
		StripeSubscriptionItem: sub,
	}

	// Plant 24 h of billable usage: one (acct, app, minute) row per
	// minute across the 24h window. BillableRAMMB encodes the +8 MB
	// PerVMOverheadMB rule (spec §4.7 / invariant §6.2-2) so the math
	// here stays in lockstep with the production sampler. The minute
	// timestamp is irrelevant for the wire-quantity assertion — the
	// push handler ignores the row timestamps and sums mb_seconds
	// across the SDK call — but a contiguous, ordered layout makes a
	// re-run operator's logs easy to read.
	billableMB := int64(api.BillableRAMMB(256)) // 264
	const minutesPerDay = 24 * 60
	for i := 0; i < minutesPerDay; i++ {
		minute := startUTC.Add(time.Duration(i) * time.Minute)
		if err := store.AppendUsage(context.Background(),
			acct.ID, "app_sandbox", "ins_sandbox",
			minute,
			billableMB*60, // one minute of billable MB = billable * 60 mb_seconds
			0,
		); err != nil {
			t.Fatalf("AppendUsage minute %d: %v", i, err)
		}
	}

	// Sum across the 24h window in pure int64 — must match what the
	// SDK sees. The integer wire-quantity formula is qty = mbSec *
	// 1000 / 1024 / 3600, evaluated in Go int64 arithmetic (no float).
	wantMB := billableMB * 60 * minutesPerDay // 264 * 3600 * 24 = 22_809_600
	wantQty := wantMB * WireQuantityMillicentsPerGBHour / secondsPerGBHour
	if wantQty != 6187 {
		// Surface this loudly: a future change to BillableRAMMB or
		// PerVMOverheadMB shifts the expected integer answer. The test
		// will fail anyway, but a clear "x != 6187" message at the
		// top of the failure saves the operator a hand-calculation.
		t.Fatalf("expected wire quantity = %d (hard-coded in plan as 6187); "+
			"update both if BillableRAMMB(256) or WireQuantityMillicentsPerGBHour changed",
			wantQty)
	}

	// Push once against the real Stripe SDK with the 24h mb_seconds
	// sum. The (account, hour) dedupe gate is keyed on `startUTC`,
	// so a second invocation targeting a different UTC day never
	// short-circuits.
	record, err := c.PushUsageRecordSumWithID(context.Background(), acct, startUTC, wantMB)
	if err != nil {
		t.Fatalf("PushUsageRecordSumWithID against sandbox: %v", err)
	}
	if record == nil {
		t.Fatal("PushUsageRecordSumWithID returned nil record with no error")
	}
	if record.ID == "" || !strings.HasPrefix(record.ID, "mbur_") {
		t.Fatalf("Stripe usage record ID %q does not look like a real usage record", record.ID)
	}

	// §14 M7 acceptance. Zero delta, integer equality. If this fails,
	// inspect pkg/billing/stripe/usage.go::pushUsageRecordSDKSumWithID for
	// any drift away from the integer-arithmetic wire formula. The
	// previous float path produced 6168 (0.315 % short); the spec's
	// 0.1 % gate would still catch that, but exact-integer equality
	// is the cleaner regression guard.
	if record.Quantity != 6187 {
		t.Fatalf("wire Quantity = %d, want 6187 exactly (integer-arithmetic M7 acceptance)",
			record.Quantity)
	}
}
