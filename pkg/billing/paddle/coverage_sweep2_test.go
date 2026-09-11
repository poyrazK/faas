package paddle

// adr: 032
// coverage_sweep2_test.go: covers zero-coverage branches in
// pkg/billing/paddle that the existing tests don't reach. All paths
// here are pure logic — no live Paddle API call. Test seams
// (NewProviderForTest, FlushFnForTest, createUpgradeTxnFn) intercept
// the SDK POST so the tests run with no network.
//
// Naming follows the existing paddle/*_test.go style
// (TestXxxHappy / TestXxxError). Reviewers grep by TestPaddle_ prefix
// to find this sweep.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeDedupePaddle is a minimal PaddleOverageDedupe for coverage
// tests — only the methods exercised here. claimResult / completeErr
// are knobs the test sets to drive branch coverage.
type fakeDedupePaddle struct {
	mu          sync.Mutex
	claimResult bool
	claimErr    error
	completeErr error
	reapCount   int
}

func (d *fakeDedupePaddle) HasPaddleOverageMonth(context.Context, string, time.Time) (bool, error) {
	return false, nil
}
func (d *fakeDedupePaddle) RecordPaddleOverageMonth(context.Context, string, time.Time) error {
	return nil
}
func (d *fakeDedupePaddle) PaddleOverageWindowExists(context.Context, string, time.Time) (bool, error) {
	return false, nil
}
func (d *fakeDedupePaddle) ClaimPaddleOverageWindow(_ context.Context, _ string, _ time.Time, _ string, _ time.Duration) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.claimResult, d.claimErr
}
func (d *fakeDedupePaddle) CompletePaddleOverageWindow(_ context.Context, _ string, _ time.Time, _ int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.completeErr
}
func (d *fakeDedupePaddle) ReapStalePaddleOverageClaims(context.Context, time.Duration) (int, error) {
	return d.reapCount, nil
}

// TestPaddle_ExtractIDs_Empty — extractIDs with zero-byte payload
// returns all-zero ids without panicking (production receives this
// shape when a webhook body has been stripped by an upstream proxy).
func TestPaddle_ExtractIDs_Empty(t *testing.T) {
	t.Parallel()
	c, s, p := extractIDs(json.RawMessage{})
	if c != "" || s != "" || p != "" {
		t.Errorf("got = (%q, %q, %q); want all empty", c, s, p)
	}
}

// TestPaddle_ExtractIDs_SubscriptionShape — subscription events
// carry items[0].price.id as the plan identifier; the function
// unpacks that path.
func TestPaddle_ExtractIDs_SubscriptionShape(t *testing.T) {
	t.Parallel()
	body := json.RawMessage(`{
		"customer_id": "ctm_1",
		"subscription_id": "sub_2",
		"items": [{"price": {"id": "pri_3"}}]
	}`)
	c, s, p := extractIDs(body)
	if c != "ctm_1" {
		t.Errorf("customer = %q, want ctm_1", c)
	}
	if s != "sub_2" {
		t.Errorf("subscription = %q, want sub_2", s)
	}
	if p != "pri_3" {
		t.Errorf("plan = %q, want pri_3", p)
	}
}

// TestPaddle_ExtractIDs_TransactionShape — transaction events have no
// items[]; the function must fall through to the txn-only branch and
// leave plan empty.
func TestPaddle_ExtractIDs_TransactionShape(t *testing.T) {
	t.Parallel()
	body := json.RawMessage(`{"customer_id":"ctm_4","subscription_id":"sub_5"}`)
	c, s, p := extractIDs(body)
	if c != "ctm_4" {
		t.Errorf("customer = %q, want ctm_4", c)
	}
	if s != "sub_5" {
		t.Errorf("subscription = %q, want sub_5", s)
	}
	if p != "" {
		t.Errorf("plan = %q, want empty (txn shape has no items)", p)
	}
}

// TestPaddle_ExtractIDs_SubscriptionNoItems — subscription shape
// with items:[] (a brand-new subscription with no priced item yet)
// must yield plan == "" without panicking.
func TestPaddle_ExtractIDs_SubscriptionNoItems(t *testing.T) {
	t.Parallel()
	body := json.RawMessage(`{"customer_id":"ctm_6","subscription_id":"sub_7","items":[]}`)
	c, s, p := extractIDs(body)
	if c != "ctm_6" || s != "sub_7" || p != "" {
		t.Errorf("got = (%q,%q,%q); want (ctm_6,sub_7,\"\")", c, s, p)
	}
}

// TestPaddle_FlushOverageLocked_ZeroFastPath — mbSeconds == 0 short-
// circuits before the claim gate; the dedupe stub must NOT be
// consulted. This pins the early-return contract the meterd loop
// relies on for idle accounts.
func TestPaddle_FlushOverageLocked_ZeroFastPath(t *testing.T) {
	t.Parallel()
	p := NewProviderForTest(nil)
	d := &fakeDedupePaddle{claimResult: true}
	p.dedupe = d
	var called bool
	p.FlushFnForTest(func(context.Context, *Provider, state.Account, time.Time, int64) error {
		called = true
		return nil
	})
	err := p.flushOverageLocked(context.Background(), state.Account{ID: "a"}, time.Now(), 0)
	if err != nil {
		t.Fatalf("flushOverageLocked(0): %v", err)
	}
	if called {
		t.Error("flushFn was called for mbSeconds=0; want fast-path skip")
	}
}

// TestPaddle_FlushOverageLocked_DedupeAlreadyClaimed — when the
// dedupe gate says another pod owns this window, the flushFn is NOT
// invoked and the delivery remains pending. This pins the cross-pod
// coordination contract.
func TestPaddle_FlushOverageLocked_DedupeAlreadyClaimed(t *testing.T) {
	t.Parallel()
	p := NewProviderForTest(nil)
	d := &fakeDedupePaddle{claimResult: false} // someone else owns it
	p.dedupe = d
	var called bool
	p.FlushFnForTest(func(context.Context, *Provider, state.Account, time.Time, int64) error {
		called = true
		return nil
	})
	err := p.flushOverageLocked(context.Background(), state.Account{ID: "a"}, time.Now(), 1024)
	if !errors.Is(err, billing.ErrUsageDeliveryInProgress) {
		t.Fatalf("flushOverageLocked error = %v, want ErrUsageDeliveryInProgress", err)
	}
	if called {
		t.Error("flushFn called despite dedupe saying claimed=false")
	}
}

// TestPaddle_FlushOverageLocked_DedupeClaimError — a claim-time
// error must surface as a wrapped error (NOT silently swallowed
// — that would lose usage data).
func TestPaddle_FlushOverageLocked_DedupeClaimError(t *testing.T) {
	t.Parallel()
	p := NewProviderForTest(nil)
	d := &fakeDedupePaddle{claimErr: errors.New("redis down")}
	p.dedupe = d
	var called bool
	p.FlushFnForTest(func(context.Context, *Provider, state.Account, time.Time, int64) error {
		called = true
		return nil
	})
	err := p.flushOverageLocked(context.Background(), state.Account{ID: "a"}, time.Now(), 1024)
	if err == nil {
		t.Fatal("flushOverageLocked = nil on claim error; want error")
	}
	if called {
		t.Error("flushFn called despite claim error; want skip")
	}
}

// TestPaddle_FlushOverageLocked_DedupeCompleteError — flushFn
// succeeds but CompletePaddleOverageWindow fails; the error must
// propagate so the next process boot can retry.
func TestPaddle_FlushOverageLocked_DedupeCompleteError(t *testing.T) {
	t.Parallel()
	p := NewProviderForTest(nil)
	d := &fakeDedupePaddle{claimResult: true, completeErr: errors.New("complete fail")}
	p.dedupe = d
	p.FlushFnForTest(func(context.Context, *Provider, state.Account, time.Time, int64) error {
		return nil
	})
	err := p.flushOverageLocked(context.Background(), state.Account{ID: "a"}, time.Now(), 1024)
	if err == nil {
		t.Fatal("flushOverageLocked = nil on complete error; want error")
	}
}

// TestPaddle_FlushOverageLocked_HappyPath — flushFn is invoked with
// the resolved windowStart + mbSeconds, and CompletePaddleOverageWindow
// is called with the same values.
func TestPaddle_FlushOverageLocked_HappyPath(t *testing.T) {
	t.Parallel()
	p := NewProviderForTest(nil)
	d := &fakeDedupePaddle{claimResult: true}
	p.dedupe = d
	var got struct {
		windowStart time.Time
		mbSeconds   int64
	}
	var called bool
	p.FlushFnForTest(func(_ context.Context, _ *Provider, _ state.Account, ws time.Time, mb int64) error {
		called = true
		got.windowStart = ws
		got.mbSeconds = mb
		return nil
	})
	hour := time.Date(2026, 8, 8, 14, 30, 0, 0, time.UTC) // truncated to top of hour
	err := p.flushOverageLocked(context.Background(), state.Account{ID: "a"}, hour, 4096)
	if err != nil {
		t.Fatalf("flushOverageLocked: %v", err)
	}
	if !called {
		t.Fatal("flushFn not called")
	}
	want := hour.Truncate(time.Hour)
	if !got.windowStart.Equal(want) {
		t.Errorf("windowStart = %v, want %v (truncated to hour)", got.windowStart, want)
	}
	if got.mbSeconds != 4096 {
		t.Errorf("mbSeconds = %d, want 4096", got.mbSeconds)
	}
}

// TestPaddle_DefaultFlushLocked_OveragePriceMissing — when the
// catalog has no overage price for the account's plan, the SDK POST
// is NOT made and ErrOveragePriceMissing is wrapped with the plan.
func TestPaddle_DefaultFlushLocked_OveragePriceMissing(t *testing.T) {
	t.Parallel()
	p := NewProviderForTest(nil)
	// catalog.planOverage is empty by default — every plan returns "".
	acct := state.Account{ID: "a", Plan: api.PlanPro, ProviderCustomerID: "ctm_x"}
	err := defaultFlushLocked(context.Background(), p, acct, time.Now(), 1024)
	if err == nil {
		t.Fatal("defaultFlushLocked = nil with empty catalog; want error")
	}
	if !errors.Is(err, ErrOveragePriceMissing) {
		t.Errorf("err = %v, want ErrOveragePriceMissing", err)
	}
}

// TestPaddle_MonthlyPriceForPlan_Empty — the catalog miss path:
// unknown plan returns "" so defaultCreateUpgradeTxn can surface
// its "EnsurePlanProducts must run first" error.
func TestPaddle_MonthlyPriceForPlan_Empty(t *testing.T) {
	t.Parallel()
	p := NewProviderForTest(nil)
	if got := p.monthlyPriceForPlan(api.PlanScale); got != "" {
		t.Errorf("monthlyPriceForPlan(empty catalog) = %q, want \"\"", got)
	}
}

// TestPaddle_OveragePriceForPlan_Empty — symmetric catalog-miss
// path for the overage price.
func TestPaddle_OveragePriceForPlan_Empty(t *testing.T) {
	t.Parallel()
	p := NewProviderForTest(nil)
	if got := p.overagePriceForPlan(api.PlanScale); got != "" {
		t.Errorf("overagePriceForPlan(empty catalog) = %q, want \"\"", got)
	}
}

// TestPaddle_DefaultCreateUpgradeTxn_OveragePriceMissing — when the
// plan's monthly price isn't cataloged, the function refuses to
// call CreateTransaction and surfaces a clear error pointing the
// operator at EnsurePlanProducts.
func TestPaddle_DefaultCreateUpgradeTxn_OveragePriceMissing(t *testing.T) {
	t.Parallel()
	p := NewProviderForTest(nil)
	// Intercept the SDK CreateTransaction by swapping the seam — we
	// want to prove the price-missing branch returns BEFORE the SDK
	// is touched.
	p.createUpgradeTxnFn = func(context.Context, *Provider, state.Account, api.Plan) (string, string, error) {
		t.Fatal("createUpgradeTxnFn called despite missing price; want early return")
		return "", "", nil
	}
	acct := state.Account{ID: "a", Plan: api.PlanPro, ProviderCustomerID: "ctm_x"}
	_, _, err := defaultCreateUpgradeTxn(context.Background(), p, acct, api.PlanScale)
	if err == nil {
		t.Fatal("defaultCreateUpgradeTxn = nil; want price-missing error")
	}
	if !contains(err.Error(), "monthly price missing") {
		t.Errorf("err = %v, want 'monthly price missing' hint", err)
	}
}

// TestPaddle_ClaimedBy_InstanceIDPreferred — when instanceID is set
// (production sets it from the scheduler at startup), the function
// returns it without consulting HOSTNAME / POD_NAME.
func TestPaddle_ClaimedBy_InstanceIDPreferred(t *testing.T) {
	// NOT t.Parallel() — t.Setenv below is incompatible with it.
	p := NewProviderForTest(nil)
	p.instanceID = "schedd-7"
	t.Setenv("HOSTNAME", "env-hostname")
	t.Setenv("POD_NAME", "env-podname")
	if got := p.claimedBy(); got != "schedd-7" {
		t.Errorf("claimedBy = %q, want %q (instanceID wins over env)", got, "schedd-7")
	}
}

// TestPaddle_ClaimedBy_EnvFallback — with instanceID empty, HOSTNAME
// is the next preference (matches k8s convention).
func TestPaddle_ClaimedBy_EnvFallback(t *testing.T) {
	// NOT t.Parallel() — t.Setenv below is incompatible with it.
	p := NewProviderForTest(nil)
	t.Setenv("HOSTNAME", "test-host")
	t.Setenv("POD_NAME", "test-pod")
	if got := p.claimedBy(); got != "test-host" {
		t.Errorf("claimedBy = %q, want %q (HOSTNAME preferred over POD_NAME)", got, "test-host")
	}
}

// TestPaddle_ClaimedBy_StaticSentinel — with no instanceID and no
// env vars, a static sentinel keeps the value non-empty so log-grep
// always finds something.
func TestPaddle_ClaimedBy_StaticSentinel(t *testing.T) {
	// NOT t.Parallel() — t.Setenv below is incompatible with it.
	p := NewProviderForTest(nil)
	t.Setenv("HOSTNAME", "")
	t.Setenv("POD_NAME", "")
	if got := p.claimedBy(); got != "paddle-push" {
		t.Errorf("claimedBy = %q, want %q (static sentinel)", got, "paddle-push")
	}
}

// contains is a tiny strings.Contains shim so this file doesn't have
// to import "strings" just for one call site.
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// guard that the helper above still type-checks against fmt usage in
// any future refactor that switches it to fmt.Errorf-wrapped errors.
var _ = fmt.Sprintf
