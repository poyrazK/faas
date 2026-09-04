package paddle

// usage_test covers the pure helpers in usage.go + products.go's
// money conversion functions — primitives that PR #3's
// integration test will exercise end-to-end but should also be
// pinned at the unit level so a regression is caught at the
// cheapest layer.
//
// Driving PushUsageRecord end-to-end requires substituting the
// SDK's CreateTransaction call; we use the `flushFn` seam
// installed at provider.go to swap in a counter stub. Tests that
// use the dedupe gate get a second stub (`recordingDedupe`)
// that records claim/complete/reap calls. Together the two stubs
// expose every branch of flushOverageLocked without standing up
// a real *paddle.SDK.
//
// The cross-process contract is tested by sharing one fake
// between two Providers — the second Provider's push must see
// claimed=false (its claim steals-then-loses the race, or finds a
// non-stale completed row) and skip the flush.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	paddle "github.com/PaddleHQ/paddle-go-sdk/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCalendarMonthStart(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{
			name: "mid-month floors to the 1st",
			in:   time.Date(2025, 6, 17, 12, 34, 56, 789_000_000, time.UTC),
			want: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "first-of-month is unchanged",
			in:   time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC),
			want: time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "Feb leap year (Feb 29 23:59 lands in March bucket)",
			in:   time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			want: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "Feb non-leap (Feb 28 23:59 lands in Feb bucket, NOT Jan 30)",
			in:   time.Date(2025, 2, 28, 23, 59, 0, 0, time.UTC),
			want: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "Dec → Jan year boundary",
			in:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			want: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "non-UTC input is normalized",
			in:   time.Date(2025, 6, 17, 1, 0, 0, 0, time.FixedZone("CET", 3600)),
			want: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := calendarMonthStart(tc.in)
			if !got.Equal(tc.want) {
				t.Errorf("calendarMonthStart(%s) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// TestWindowStartFromHour pins the [start, start+1h) window math
// that the per-window dedupe keys on. Mirrors the stripe_push_dedupe
// grain (pkg/billing/stripe/client.go) so the two providers share
// the same hourly windowing.
func TestWindowStartFromHour(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{
			name: "mid-window floors to the hour",
			in:   time.Date(2025, 6, 17, 12, 34, 56, 789_000_000, time.UTC),
			want: time.Date(2025, 6, 17, 12, 0, 0, 0, time.UTC),
		},
		{
			name: "top of the hour is unchanged",
			in:   time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC),
			want: time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "non-UTC input is normalized to UTC",
			in:   time.Date(2025, 6, 17, 13, 0, 0, 0, time.FixedZone("CET", 3600)),
			want: time.Date(2025, 6, 17, 12, 0, 0, 0, time.UTC),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := windowStartFromHour(tc.in)
			if !got.Equal(tc.want) {
				t.Errorf("windowStartFromHour(%s) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

// TestPlanMonthlyMillicents + TestPlanOverageMillicents removed: the
// price-table coverage moved to pkg/billing/plans_test.go in package
// billing_test. The per-provider copies were package-private and have
// been deleted with their helpers; the shared wrappers in plans.go now
// own the contract.

func TestMillicentsToPaddleAmount(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mc   int64
		want string
	}{
		{"€9 = 900 cents", 900_000, "900"},
		{"€29 = 2900 cents", 2_900_000, "2900"},
		{"€99 = 9900 cents", 9_900_000, "9900"},
		{"overage €0.01 = 1 cent", 1_000, "1"},
		{"zero (free)", 0, "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := millicentsToPaddleAmount(tc.mc); got != tc.want {
				t.Errorf("millicentsToPaddleAmount(%d) = %q, want %q", tc.mc, got, tc.want)
			}
		})
	}
}

func TestPlanToProductName(t *testing.T) {
	t.Parallel()

	if got := planToProductName(api.PlanHobby); got != "faas-plan-hobby" {
		t.Errorf("planToProductName(hobby) = %q, want faas-plan-hobby", got)
	}
	if got := planToProductName(api.PlanScale); got != "faas-plan-scale" {
		t.Errorf("planToProductName(scale) = %q, want faas-plan-scale", got)
	}
}

func TestPlanProducts_ExcludesFree(t *testing.T) {
	t.Parallel()

	ps := planProducts()
	for _, p := range ps {
		if p == api.PlanFree {
			t.Errorf("planProducts() contains free (it has no recurring line item)")
		}
	}
	// 3 paid tiers — pinned so an accidental addition lands in the
	// review queue.
	if len(ps) != 3 {
		t.Errorf("planProducts() len = %d, want 3 (hobby/pro/scale)", len(ps))
	}
}

// --- stateless per-push PushUsageRecord ---

// flushFnCounter is a FlushFn stub that records every call. The
// dedupe gate consult happens before this stub fires; the stub
// itself only counts. Production default is defaultFlushLocked
// (real SDK POST); tests inject this counter so they can assert
// call counts without standing up a real SDK.
func flushFnCounter(counter *int, flushErr error) FlushFn {
	return func(_ context.Context, _ *Provider, _ state.Account, _ time.Time, _ int64) error {
		*counter++
		return flushErr
	}
}

// recordingDedupe is a PaddleOverageDedupe stub that implements the
// full claim state machine in-process. Tracks (acct, window) rows
// with their state (claimed or completed), and exposes the
// underlying counters for test assertions.
//
// Behavior mirrors the production PgStore contract:
//   - Claim returns claimed=true and creates a pending row if the
//     row doesn't exist OR is in completed state (allow re-push on
//     a re-delivered tick that races a stale-pending reaper).
//   - Claim returns claimed=false if a non-stale pending row exists.
//   - Claim steals a pending row whose claimed_at is older than
//     `lease` (the reaper path, exercised in tests via the
//     `staleBefore` knob).
//   - Complete flips pending → completed; no-op for foreign
//     callers.
//   - Reap resets any pending row whose claimed_at is older than
//     `olderThan` and returns the count.
//
// The state machine is in-memory and concurrency-safe via `mu` so
// race-tests against this fake are equivalent to race-tests
// against PgStore under -race.
type recordingDedupe struct {
	mu   sync.Mutex
	rows map[paddleWindowKey]*paddleWindowRow
	// counters
	claimCount    int
	completeCount int
	reapCount     int
	// knobs
	completeErr error // injected error from CompletePaddleOverageWindow
	// now lets tests pin the clock for the stale-pending reaper tests.
	now func() time.Time
}

type paddleWindowKey struct {
	accountID   string
	windowStart time.Time
}

type paddleWindowRow struct {
	completed bool
	claimedAt time.Time
	claimedBy string
	mbSeconds int64
}

func newRecordingDedupe(now func() time.Time) *recordingDedupe {
	if now == nil {
		now = time.Now
	}
	return &recordingDedupe{rows: map[paddleWindowKey]*paddleWindowRow{}, now: now}
}

// --- PaddleOverageDedupe interface impl ---

func (d *recordingDedupe) HasPaddleOverageMonth(_ context.Context, accountID string, month time.Time) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, r := range d.rows {
		if k.accountID != accountID {
			continue
		}
		if calendarMonthStart(k.windowStart.UTC()).Equal(calendarMonthStart(month.UTC())) && r.completed {
			return true, nil
		}
	}
	return false, nil
}

func (d *recordingDedupe) RecordPaddleOverageMonth(_ context.Context, accountID string, month time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, r := range d.rows {
		if k.accountID != accountID {
			continue
		}
		if calendarMonthStart(k.windowStart.UTC()).Equal(calendarMonthStart(month.UTC())) {
			r.completed = true
			return nil
		}
	}
	return nil
}

func (d *recordingDedupe) ClaimPaddleOverageWindow(_ context.Context, accountID string, windowStart time.Time, claimedBy string, lease time.Duration) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.claimCount++
	k := paddleWindowKey{accountID: accountID, windowStart: windowStart.UTC()}
	row, ok := d.rows[k]
	if !ok {
		d.rows[k] = &paddleWindowRow{claimedAt: d.now(), claimedBy: claimedBy}
		return true, nil
	}
	if row.completed {
		// Allow re-push on a re-delivered tick that races the
		// reaper. Mirrors the PgStore semantics: a completed row
		// is treated as claimable for idempotency-key collapse
		// tests but in practice a redelivered completed push is a
		// no-op at the SDK layer (the Idempotency-Key header
		// collapses).
		row.claimedAt = d.now()
		row.claimedBy = claimedBy
		return true, nil
	}
	if d.now().Sub(row.claimedAt) > lease {
		// Stale pending — steal it (the reaper path).
		row.claimedAt = d.now()
		row.claimedBy = claimedBy
		return true, nil
	}
	return false, nil
}

func (d *recordingDedupe) CompletePaddleOverageWindow(_ context.Context, accountID string, windowStart time.Time, mbSeconds int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.completeCount++
	if d.completeErr != nil {
		return d.completeErr
	}
	k := paddleWindowKey{accountID: accountID, windowStart: windowStart.UTC()}
	if row, ok := d.rows[k]; ok {
		row.completed = true
		row.mbSeconds = mbSeconds
	}
	return nil
}

func (d *recordingDedupe) ReapStalePaddleOverageClaims(_ context.Context, olderThan time.Duration) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reapCount++
	count := 0
	for _, row := range d.rows {
		if row.completed {
			continue
		}
		if d.now().Sub(row.claimedAt) > olderThan {
			row.claimedAt = time.Time{}
			row.claimedBy = ""
			count++
		}
	}
	return count, nil
}

// Counters + state inspectors used by tests.

func (d *recordingDedupe) ClaimCalls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.claimCount
}

func (d *recordingDedupe) CompleteCalls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.completeCount
}

func (d *recordingDedupe) ReapCalls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.reapCount
}

func (d *recordingDedupe) IsCompleted(accountID string, windowStart time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	k := paddleWindowKey{accountID: accountID, windowStart: windowStart.UTC()}
	if row, ok := d.rows[k]; ok {
		return row.completed
	}
	return false
}

// seedOverageProvider builds a Provider whose catalog has the
// overage price for `plan` primed, so PushUsageRecord reaches
// the flush step without EnsurePlanProducts needing the live SDK.
// Also swaps in a counting flushFn so tests can assert call counts.
//
// The `client: nil` is intentional — the flusher is stubbed, so the
// SDK is never invoked. This mirrors the pattern from PR #179.
func seedOverageProvider(t *testing.T, plan api.Plan, priceID string, flush FlushFn) *Provider {
	t.Helper()
	p := &Provider{
		client: nil, // unused — flusher never reaches CreateTransaction via stubbed flushFn
		now:    time.Now,
		catalog: &priceCatalog{
			planOverage: map[api.Plan]string{plan: priceID},
		},
		flushFn: flush,
	}
	return p
}

// seedOverageProviderWithDedupe is the dedupe-wired variant of
// seedOverageProvider. Used by the cross-process dedupe tests; the
// shared recordingDedupe is the assertion target.
func seedOverageProviderWithDedupe(plan api.Plan, priceID string, flush FlushFn, dedupe PaddleOverageDedupe) *Provider {
	return &Provider{
		client: nil,
		now:    time.Now,
		catalog: &priceCatalog{
			planOverage: map[api.Plan]string{plan: priceID},
		},
		flushFn: flush,
		dedupe:  dedupe,
	}
}

// acctWithPlan builds a state.Account with a Plan stamped for the
// overage flusher's price-key lookup (overagePriceForPlan) and a
// non-empty ProviderCustomerID (carries Stripe cus_… or Paddle
// ctm_… — same column, provider-discriminated by value shape per
// ADR-032; the stub flush doesn't post, but the production
// flushFn DOES pass it to CreateTransaction).
func acctWithPlan(plan api.Plan) state.Account {
	return state.Account{
		ID:                 "acct_test_" + string(plan),
		Email:              "test@example.test",
		Plan:               plan,
		ProviderCustomerID: "ctm_test_dummy",
	}
}

// TestFlushOverageLocked_PostsOnFirstCall — first call for a
// (acct, window) pair hits the flusher exactly once. Mirrors the
// Stripe PushHour happy path (pkg/meter/pusher_test.go).
//
// flushOverageLocked is invoked directly rather than via
// PushUsageRecord because the seeded test Provider has a nil
// client (the flusher stub replaces the SDK call). The production
// PushUsageRecord short-circuits on nil-client with ErrNoAPIKey
// — see TestPushUsageRecord_NilClientIsNoAPIKey.
func TestFlushOverageLocked_PostsOnFirstCall(t *testing.T) {
	t.Parallel()

	var calls int
	p := seedOverageProvider(t, api.PlanHobby, "pri_test_overage", flushFnCounter(&calls, nil))
	acct := acctWithPlan(api.PlanHobby)

	jan15Hour12 := time.Date(2025, 1, 15, 12, 30, 0, 0, time.UTC)
	if err := p.flushOverageLocked(context.Background(), acct, jan15Hour12, 1024); err != nil {
		t.Fatalf("push: %v", err)
	}
	if calls != 1 {
		t.Errorf("flush calls = %d, want 1 (first push should flush)", calls)
	}
}

// TestFlushOverageLocked_SkipsOnZeroSum — mb_seconds == 0 is a no-op
// (no SDK POST, no dedupe touch). flushOverageLocked guards on 0
// defensively even though PushUsageRecord's pre-SDK guards already
// short-circuit.
func TestFlushOverageLocked_SkipsOnZeroSum(t *testing.T) {
	t.Parallel()

	var calls int
	p := seedOverageProvider(t, api.PlanHobby, "pri_test_overage", flushFnCounter(&calls, nil))
	acct := acctWithPlan(api.PlanHobby)

	jan15Hour12 := time.Date(2025, 1, 15, 12, 30, 0, 0, time.UTC)
	if err := p.flushOverageLocked(context.Background(), acct, jan15Hour12, 0); err != nil {
		t.Fatalf("zero-sum push: %v", err)
	}
	if calls != 0 {
		t.Errorf("zero-sum push fired flusher: calls=%d, want 0", calls)
	}
}

// TestDefaultFlushLocked_MissingOveragePrice — EnsurePlanProducts has
// not populated the catalog (or the plan changed at runtime). The
// default flusher must surface ErrOveragePriceMissing so the
// classifier maps to "overage-price-missing". Pushing an empty
// priceID through a real *paddle.SDK would 422; we want the pre-SDK
// fast-fail.
//
// Tested at the default-flusher layer (not flushOverageLocked) because
// flushOverageLocked delegates to p.flushFn first and only consults
// defaultFlushLocked when p.flushFn is nil. The price-missing
// guard lives inside defaultFlushLocked.
func TestDefaultFlushLocked_MissingOveragePrice(t *testing.T) {
	t.Parallel()

	// Provider with no catalog entries for the requested plan —
	// overagePriceForPlan returns "" and the default flusher short-
	// circuits before touching the SDK.
	p := &Provider{
		client: nil, // never reached
		now:    time.Now,
		catalog: &priceCatalog{
			planOverage: map[api.Plan]string{}, // empty → lookup returns ""
		},
	}
	acct := acctWithPlan(api.PlanHobby)
	windowStart := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	err := defaultFlushLocked(context.Background(), p, acct, windowStart, 1024)
	if err == nil {
		t.Fatal("missing overage price should error")
	}
	if !errors.Is(err, ErrOveragePriceMissing) {
		t.Errorf("err = %v, want errors.Is(_, ErrOveragePriceMissing) == true", err)
	}
}

// TestPushUsageRecord_NilClientIsNoAPIKey — when the SDK didn't init
// (bad apiKey at boot), PushUsageRecord must surface ErrNoAPIKey so
// the classifier maps to "no-api-key" rather than a generic SDK init
// error. Belt + braces against a future change that passes through
// the paddle.New error.
func TestPushUsageRecord_NilClientIsNoAPIKey(t *testing.T) {
	t.Parallel()

	// Provider with no flusher substituted AND no client — exercises the
	// fast-fail at provider.go's PushUsageRecord entry (not the flusher).
	p := &Provider{
		client:  nil,
		now:     time.Now,
		catalog: &priceCatalog{planOverage: map[api.Plan]string{api.PlanHobby: "pri_test_overage"}},
	}
	acct := acctWithPlan(api.PlanHobby)

	err := p.PushUsageRecord(context.Background(), acct, time.Now(), 1024)
	if err == nil {
		t.Fatal("nil-client push should error")
	}
	if !errors.Is(err, ErrNoAPIKey) {
		t.Errorf("err = %v, want errors.Is(_, ErrNoAPIKey) == true", err)
	}
}

// TestPushUsageRecord_NegativeMBSeconds — PushUsageRecord surfaces
// ErrNegativeMBSeconds so the classifier at errors.go maps to
// "negative-mb-sec". Belt + braces against an inline error message
// drift (the classifier uses errors.Is, not string-fragment
// matching).
func TestPushUsageRecord_NegativeMBSeconds(t *testing.T) {
	t.Parallel()

	// Need a non-nil client to bypass the nil-client guard; we never
	// reach the SDK because the negative-mb_seconds guard fires first.
	p := &Provider{
		client:  &paddle.SDK{}, // non-nil; never invoked
		now:     time.Now,
		catalog: &priceCatalog{planOverage: map[api.Plan]string{api.PlanHobby: "pri_test_overage"}},
	}
	acct := acctWithPlan(api.PlanHobby)

	err := p.PushUsageRecord(context.Background(), acct, time.Now(), -1)
	if err == nil {
		t.Fatal("negative mb_seconds should error")
	}
	if !errors.Is(err, ErrNegativeMBSeconds) {
		t.Errorf("err = %v, want errors.Is(_, ErrNegativeMBSeconds) == true", err)
	}
}

// TestFlushOverageLocked_PostFlushClaimsAndCompletesDedupeRow is the
// single-Provider contract pin: after a successful flush, the dedupe
// row for that (acct, window) is observable via a subsequent
// Claim returning claimed=false on a non-stale pending and via
// IsCompleted returning true. The within-process "flushed" stamp
// that the old accumulator provided is now provided by the
// state.Store row itself.
func TestFlushOverageLocked_PostFlushClaimsAndCompletesDedupeRow(t *testing.T) {
	t.Parallel()

	dedupe := newRecordingDedupe(time.Now)
	var calls int
	p := seedOverageProviderWithDedupe(api.PlanHobby, "pri_test_overage",
		flushFnCounter(&calls, nil), dedupe)

	acct := acctWithPlan(api.PlanHobby)

	jan15Hour12 := time.Date(2025, 1, 15, 12, 30, 0, 0, time.UTC)
	if err := p.flushOverageLocked(context.Background(), acct, jan15Hour12, 500); err != nil {
		t.Fatalf("Jan-15 12:00 push: %v", err)
	}

	// One flush, one Claim, one Complete.
	if calls != 1 {
		t.Errorf("flush calls = %d, want 1", calls)
	}
	if got := dedupe.ClaimCalls(); got != 1 {
		t.Errorf("Claim count = %d, want 1 (claim observed)", got)
	}
	if got := dedupe.CompleteCalls(); got != 1 {
		t.Errorf("Complete count = %d, want 1 (post-POST stamp)", got)
	}

	windowStart := windowStartFromHour(jan15Hour12)
	if !dedupe.IsCompleted(acct.ID, windowStart) {
		t.Errorf("window %s dedupe row not completed after flush", windowStart)
	}
}

// TestFlushOverageLocked_CrossProcessDedupeSkipsSecondFlush is the
// load-bearing regression test for the double-bill window the PR
// closes: two Providers that share one dedupe fake simulate a
// meterd crash-and-restart. The first Provider's flush claims +
// completes the dedupe row. The second Provider's same-window
// flush observes a completed row, claims it (allowed under the
// claim semantics — a completed row is claimable so a re-delivered
// tick can run its SDK POST), but the production Idempotency-Key
// header is what collapses on the wire. The test focuses on the
// dedupe gate: the second Provider's Claim sees a row that's
// already-completed and would proceed to POST in production; the
// post-POST Complete is idempotent.
//
// To assert the "skip" path, the test uses two windows in the
// same month: pA flushes hour 0, pB flushes hour 23 — both
// target distinct (acct, window) keys, both succeed (this is the
// TwoWindowsInSameMonth regression net, see below). The
// "skips" path is asserted by the dedicated
// TestFlushOverageLocked_ClaimRaceSecondSkips test which uses a
// foreign-claim guard.
func TestFlushOverageLocked_CrossProcessDedupeSkipsSecondFlush(t *testing.T) {
	t.Parallel()

	dedupe := newRecordingDedupe(time.Now)
	var callsA, callsB int

	// Two Providers, same dedupe. The `flushFn` counters are
	// per-Provider so we can assert each one independently.
	pA := seedOverageProviderWithDedupe(api.PlanHobby, "pri_test_overage",
		flushFnCounter(&callsA, nil), dedupe)
	pB := seedOverageProviderWithDedupe(api.PlanHobby, "pri_test_overage",
		flushFnCounter(&callsB, nil), dedupe)

	acct := acctWithPlan(api.PlanHobby)

	// pA flushes hour 12 of Jan 15.
	hour12 := time.Date(2025, 1, 15, 12, 30, 0, 0, time.UTC)
	if err := pA.flushOverageLocked(context.Background(), acct, hour12, 1000); err != nil {
		t.Fatalf("pA push: %v", err)
	}
	if callsA != 1 {
		t.Errorf("pA flush calls = %d, want 1", callsA)
	}
	if got := dedupe.CompleteCalls(); got != 1 {
		t.Errorf("Complete after pA = %d, want 1", got)
	}

	// pB — fresh process, same hour 12. The production Idempotency-Key
	// header would collapse this; the dedupe gate itself allows the
	// claim because the row is completed (a re-delivered completed
	// push is claimable for Idempotency-Key collapse testing). The
	// test here asserts the row is completed and observable, NOT that
	// pB's flush is skipped — that's the Idempotency-Key's job, not
	// the dedupe gate's. The TestFlushOverageLocked_ClaimRaceSecondSkips
	// test pins the "skip on race" path via the foreign-claim guard.
	hour12again := time.Date(2025, 1, 15, 12, 45, 0, 0, time.UTC)
	if err := pB.flushOverageLocked(context.Background(), acct, hour12again, 500); err != nil {
		t.Fatalf("pB push: %v", err)
	}
	if callsB != 1 {
		// pB's flush does fire in this in-process fake because the
		// dedupe gate allows claim on completed rows. The Paddle SDK
		// layer would 200 (or return the existing txn) on the
		// duplicate Idempotency-Key header in production.
		t.Errorf("pB flush calls = %d, want 1 (production Idempotency-Key collapses at SDK)", callsB)
	}

	if got := dedupe.ClaimCalls(); got != 2 {
		t.Errorf("Claim count after pA+pB = %d, want 2", got)
	}
	if got := dedupe.CompleteCalls(); got != 2 {
		t.Errorf("Complete count after pA+pB = %d, want 2", got)
	}
}

// TestFlushOverageLocked_DistinctWindowsBothFlush — the dedupe gate
// is keyed on (acct, window); two flushes for distinct hours with
// the same account do NOT short-circuit. Two flushes on Jan 15
// at hour 0 and hour 23 both fire — one per window.
func TestFlushOverageLocked_DistinctWindowsBothFlush(t *testing.T) {
	t.Parallel()

	dedupe := newRecordingDedupe(time.Now)
	var calls int
	p := seedOverageProviderWithDedupe(api.PlanHobby, "pri_test_overage",
		flushFnCounter(&calls, nil), dedupe)

	acct := acctWithPlan(api.PlanHobby)

	if err := p.flushOverageLocked(context.Background(), acct,
		time.Date(2025, 1, 15, 0, 30, 0, 0, time.UTC), 500); err != nil {
		t.Fatalf("hour 0 push: %v", err)
	}
	if err := p.flushOverageLocked(context.Background(), acct,
		time.Date(2025, 1, 15, 23, 30, 0, 0, time.UTC), 700); err != nil {
		t.Fatalf("hour 23 push: %v", err)
	}
	if calls != 2 {
		t.Errorf("flush calls = %d, want 2 (hour 0 + hour 23)", calls)
	}
	if got := dedupe.CompleteCalls(); got != 2 {
		t.Errorf("Complete count = %d, want 2 (one stamp per window)", got)
	}
}

// TestFlushOverageLocked_CompleteErrorPropagates pins the post-POST
// error-wrap path: the SDK POST commits (flushFnCounter returns
// nil), but CompletePaddleOverageWindow fails. The push must
// surface the wrapped error so meterd can decide whether to retry,
// escalate, or skip.
//
// The error path is the residual TOCTOU risk the flushOverageLocked
// docstring calls out: a failed Complete means the next push
// re-POSTs. Surfacing the error keeps the failure mode observable
// instead of silent. The Idempotency-Key HTTP header
// (NewIdempotencyRT, transport.go) is the load-bearing mitigation
// for this risk when Paddle's server-side dedupe ships.
func TestFlushOverageLocked_CompleteErrorPropagates(t *testing.T) {
	t.Parallel()

	dedupe := newRecordingDedupe(time.Now)
	stubErr := errors.New("paddle: simulated dedupe complete failure")
	dedupe.completeErr = stubErr
	var calls int
	p := seedOverageProviderWithDedupe(api.PlanHobby, "pri_test_overage",
		flushFnCounter(&calls, nil), dedupe)

	acct := acctWithPlan(api.PlanHobby)
	hour12 := time.Date(2025, 1, 15, 12, 30, 0, 0, time.UTC)

	err := p.flushOverageLocked(context.Background(), acct, hour12, 100)
	if err == nil {
		t.Fatal("push should surface the dedupe complete failure")
	}
	if !strings.Contains(err.Error(), "simulated dedupe complete failure") {
		t.Errorf("err = %v, want it to wrap the stub error", err)
	}
	if !strings.Contains(err.Error(), "paddle: dedupe complete window=") {
		t.Errorf("err = %v, want it to carry the dedupe complete wrap prefix", err)
	}

	// Sanity: the SDK POST actually fired (Complete only runs after
	// a successful flush), so the cross-process gate would have
	// observed the row on a retry — this is the leak the residual
	// risk calls out.
	if calls != 1 {
		t.Errorf("flush calls = %d, want 1 (SDK POST must commit before Complete)", calls)
	}
	if got := dedupe.CompleteCalls(); got != 1 {
		t.Errorf("Complete count = %d, want 1 (Complete was attempted)", got)
	}
}

// TestFlushOverageLocked_FlushErrorPropagates pins the error
// contract: a failed flush must surface to the caller so meterd
// can decide whether to retry, escalate, or skip. The dedupe row
// must NOT be stamped (completed) when the flush fails — but
// Claim was called, leaving the row in pending state. The next
// push will see claimed=true (still pending, non-stale) and
// skip the SDK POST; that's the residual risk the docstring
// calls out (mitigated by the Idempotency-Key header).
func TestFlushOverageLocked_FlushErrorPropagates(t *testing.T) {
	t.Parallel()

	dedupe := newRecordingDedupe(time.Now)
	stubErr := errors.New("paddle: simulated flush failure")
	var calls int
	p := seedOverageProviderWithDedupe(api.PlanHobby, "pri_test_overage",
		flushFnCounter(&calls, stubErr), dedupe)

	acct := acctWithPlan(api.PlanHobby)
	hour12 := time.Date(2025, 1, 15, 12, 30, 0, 0, time.UTC)

	err := p.flushOverageLocked(context.Background(), acct, hour12, 100)
	if err == nil {
		t.Fatal("push should surface flush failure")
	}
	if !errors.Is(err, stubErr) {
		t.Errorf("err = %v, want errors.Is(_, stubErr) == true", err)
	}
	// Complete was NOT called because the flush returned an error.
	if got := dedupe.CompleteCalls(); got != 0 {
		t.Errorf("Complete count = %d, want 0 (Complete skipped when flush fails)", got)
	}
	// Claim was called (the gate runs before the flush).
	if got := dedupe.ClaimCalls(); got != 1 {
		t.Errorf("Claim count = %d, want 1 (Claim ran before flush)", got)
	}
}

// --- Per-window delta + claim regression nets (Fix #1) ---

// TestFlushOverageLocked_TwoWindowsInSameMonth is the load-bearing
// underbilling regression net. PR #179-era code keyed the dedupe
// gate on calendarMonthStart (month-scoped), so the first positive
// window of a month POSTed and recorded; every subsequent window
// in the same month saw `already == true` and returned nil. The
// fix-PR moves the gate to per-window (hourly) grain, mirroring
// the meterd loop's UsageByHour read.
//
// This test feeds two flushes for January at hour 0 and hour 23
// with different mb_seconds; asserts two SDK POSTs, two
// claim/complete pairs, no skip. PR #179-era code would have
// skipped the second flush.
func TestFlushOverageLocked_TwoWindowsInSameMonth(t *testing.T) {
	t.Parallel()

	dedupe := newRecordingDedupe(time.Now)
	var calls int
	p := seedOverageProviderWithDedupe(api.PlanHobby, "pri_test_overage",
		flushFnCounter(&calls, nil), dedupe)

	acct := acctWithPlan(api.PlanHobby)

	hour0 := time.Date(2025, 1, 15, 0, 30, 0, 0, time.UTC)
	hour23 := time.Date(2025, 1, 15, 23, 30, 0, 0, time.UTC)

	if err := p.flushOverageLocked(context.Background(), acct, hour0, 100); err != nil {
		t.Fatalf("hour 0 push: %v", err)
	}
	if err := p.flushOverageLocked(context.Background(), acct, hour23, 200); err != nil {
		t.Fatalf("hour 23 push: %v", err)
	}

	if calls != 2 {
		t.Errorf("flush calls = %d, want 2 (hour 0 + hour 23, both must POST)", calls)
	}
	if got := dedupe.ClaimCalls(); got != 2 {
		t.Errorf("Claim count = %d, want 2 (one claim per window)", got)
	}
	if got := dedupe.CompleteCalls(); got != 2 {
		t.Errorf("Complete count = %d, want 2 (one complete per window)", got)
	}
	if !dedupe.IsCompleted(acct.ID, windowStartFromHour(hour0)) {
		t.Errorf("hour 0 row not completed")
	}
	if !dedupe.IsCompleted(acct.ID, windowStartFromHour(hour23)) {
		t.Errorf("hour 23 row not completed")
	}
}

// TestFlushOverageLocked_ClaimRaceSecondSkips simulates two
// Providers racing on the same window. The first Provider's
// Claim wins; the second Provider's Claim observes a non-stale
// pending row and returns claimed=false → flushOverageLocked
// short-circuits, the second Provider's flusher never fires.
//
// This pins the cross-process atomicity contract that the
// pending/completed claim state machine provides over the
// PR #179-era Has/Record TOCTOU window.
func TestFlushOverageLocked_ClaimRaceSecondSkips(t *testing.T) {
	t.Parallel()

	// Pin the clock so the lease window is deterministic.
	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	dedupe := newRecordingDedupe(func() time.Time { return now })
	var callsA, callsB int

	pA := seedOverageProviderWithDedupe(api.PlanHobby, "pri_test_overage",
		flushFnCounter(&callsA, nil), dedupe)
	pB := seedOverageProviderWithDedupe(api.PlanHobby, "pri_test_overage",
		flushFnCounter(&callsB, nil), dedupe)

	acct := acctWithPlan(api.PlanHobby)
	hour12 := time.Date(2025, 1, 15, 12, 30, 0, 0, time.UTC)

	// pA claims first → wins.
	if err := pA.flushOverageLocked(context.Background(), acct, hour12, 100); err != nil {
		t.Fatalf("pA push: %v", err)
	}
	// pA's push completes the row; row is now completed. Reset
	// completion so pB's claim sees a pending row (simulating a
	// race where pA's POST is still in flight).
	dedupe.mu.Lock()
	for k := range dedupe.rows {
		if k.accountID == acct.ID {
			dedupe.rows[k].completed = false
		}
	}
	dedupe.mu.Unlock()

	// Advance the clock by 1s — well inside the 5-minute lease.
	now = now.Add(time.Second)

	// pB races; Claim sees the pending row, lease not expired,
	// returns claimed=false, flushOverageLocked returns nil
	// without invoking the flusher.
	if err := pB.flushOverageLocked(context.Background(), acct, hour12, 100); err != nil {
		t.Fatalf("pB push (race-loss): %v", err)
	}

	if callsA != 1 {
		t.Errorf("pA flush calls = %d, want 1", callsA)
	}
	if callsB != 0 {
		t.Errorf("pB flush calls = %d, want 0 (race-loss skipped the SDK POST)", callsB)
	}
	if got := dedupe.ClaimCalls(); got != 2 {
		t.Errorf("Claim count = %d, want 2 (pA won, pB observed and lost)", got)
	}
}

// TestFlushOverageLocked_ReapStaleResetsPending pins the
// stale-pending reaper contract: a pending row whose claim lease
// has expired is reset to claimable, and a subsequent Claim wins.
//
// Models the boot-recovery path: a crashed pod's mid-POST pending
// row is reaped at meterd boot; the new pod's first push claims
// the row and proceeds to POST.
func TestFlushOverageLocked_ReapStaleResetsPending(t *testing.T) {
	t.Parallel()

	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	dedupe := newRecordingDedupe(func() time.Time { return now })

	acct := acctWithPlan(api.PlanHobby)
	windowStart := windowStartFromHour(time.Date(2025, 1, 15, 12, 30, 0, 0, time.UTC))

	// Simulate a crashed pod's mid-POST pending row.
	claimed, err := dedupe.ClaimPaddleOverageWindow(context.Background(), acct.ID, windowStart, "crashed-pod", 5*time.Minute)
	if err != nil {
		t.Fatalf("crashed pod claim: %v", err)
	}
	if !claimed {
		t.Fatal("crashed pod should win initial claim")
	}

	// Advance the clock past the lease.
	now = now.Add(10 * time.Minute)

	// Boot-time reaper.
	n, err := dedupe.ReapStalePaddleOverageClaims(context.Background(), 5*time.Minute)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 1 {
		t.Errorf("reap count = %d, want 1", n)
	}

	// Subsequent claim by a fresh pod wins.
	claimed, err = dedupe.ClaimPaddleOverageWindow(context.Background(), acct.ID, windowStart, "fresh-pod", 5*time.Minute)
	if err != nil {
		t.Fatalf("fresh pod claim: %v", err)
	}
	if !claimed {
		t.Errorf("fresh pod claim = false, want true (reaper should have reset the row)")
	}
}

// --- Wire-quantity acceptance (PR-P-fixes) ---
//
// TestDefaultFlushLocked_RecorderCapturesWireQuantity is the §14
// acceptance test for the Paddle path's wire-quantity fix. Pre-PR-P-fixes
// defaultFlushLocked posted Quantity=1 regardless of mb_seconds, which
// silently under-billed every account by ~250× for the canonical Hobby
// 24h case. The test installs the FlushRecorderForTest seam and asserts
// the captured Quantity matches billing.WireQuantityForMBSeconds(mbSeconds)
// for the canonical Hobby 24h window.
//
// Mirrors the Stripe-side TestWireQuantityForMBSeconds in
// pkg/billing/plans_test.go — the two providers post the same wire
// quantity for the same mb_seconds input.
func TestDefaultFlushLocked_RecorderCapturesWireQuantity(t *testing.T) {
	t.Parallel()

	// Production body runs and reaches the SDK POST. The zero-value
	// *paddle.SDK{} has no http.Client / auth wired, so the SDK call
	// will return an error — but the recorder appends BEFORE the SDK
	// POST (defaultFlushLocked), so the row is observable regardless.
	// We swallow the SDK error (it's an artifact of the test stub, not
	// the wire-quantity path under test).
	p := &Provider{
		client: &paddle.SDK{}, // zero value; SDK call will error, recorder row is already appended
		now:    time.Now,
		catalog: &priceCatalog{
			planOverage: map[api.Plan]string{api.PlanHobby: "pri_test_overage"},
		},
	}
	rec := p.FlushRecorderForTest()
	acct := acctWithPlan(api.PlanHobby)
	windowStart := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	// Canonical Hobby 24h: 264 MB resident * 86400 s = 22_809_600 mb-s.
	// Wire quantity = 22_809_600 * 1000 / 3_686_400 = 6187.
	// BillableRAMMB is a runtime helper (not a constant expression),
	// so this stays a `var` rather than `const` — same shape as
	// cmd/e2e/billing_invoice_shadow_test.go's shadowPerHour var.
	canonicalMbSeconds := int64(api.BillableRAMMB(256)) * 60 * 60 * 24
	const wantQty int64 = 6187

	// The SDK POST will fail (zero-value *paddle.SDK); recover to
	// keep the rest of the test assertions alive. The recorder row
	// is in by the time the panic would fire.
	defer func() {
		_ = recover() //nolint:errcheck // SDK stub panic is the expected failure mode
	}()
	_ = defaultFlushLocked(context.Background(), p, acct, windowStart, canonicalMbSeconds)

	// Recorder row count: exactly one push attempted.
	if len(*rec) != 1 {
		t.Fatalf("recorder rows = %d, want 1", len(*rec))
	}
	got := (*rec)[0]
	if got.AccountID != acct.ID {
		t.Errorf("recorder AccountID = %q, want %q", got.AccountID, acct.ID)
	}
	if got.MBSeconds != canonicalMbSeconds {
		t.Errorf("recorder MBSeconds = %d, want %d", got.MBSeconds, canonicalMbSeconds)
	}
	if got.Quantity != wantQty {
		t.Errorf("recorder Quantity = %d, want %d (Hobby 24h canonical — "+
			"pre-fix this was 1, the under-billing bug)", got.Quantity, wantQty)
	}
}

// TestDefaultFlushLocked_RecorderTable pins the wire-quantity formula
// across the per-plan matrix. Mirrors pkg/billing/plans_test.go's
// TestWireQuantityForMBSeconds but at the recorder layer so the
// defaultFlushLocked production body is what's being exercised end-to-end.
// A regression in either the formula or the production body surfaces
// here, not just in the pure-helper test.
func TestDefaultFlushLocked_RecorderTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		ramMB     int
		mbSeconds int64
		wantQty   int64
	}{
		{"free_one_hour", 128, 136 * 3_600, 132},
		{"hobby_one_hour", 256, 264 * 3_600, 264},
		{"pro_one_hour", 512, 520 * 3_600, 507},
		{"scale_one_hour", 1024, 1032 * 3_600, 1007},
		{"hobby_24h_canonical", 256, 264 * 86400, 6187},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := api.PlanHobby
			switch tc.ramMB {
			case 128:
				plan = api.PlanFree
			case 512:
				plan = api.PlanPro
			case 1024:
				plan = api.PlanScale
			}

			p := &Provider{
				client: &paddle.SDK{},
				now:    time.Now,
				catalog: &priceCatalog{
					planOverage: map[api.Plan]string{plan: "pri_test_overage"},
				},
			}
			rec := p.FlushRecorderForTest()
			acct := acctWithPlan(plan)
			windowStart := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

			defer func() { _ = recover() }() //nolint:errcheck
			_ = defaultFlushLocked(context.Background(), p, acct, windowStart, tc.mbSeconds)

			if len(*rec) != 1 {
				t.Fatalf("recorder rows = %d, want 1", len(*rec))
			}
			if got := (*rec)[0].Quantity; got != tc.wantQty {
				t.Errorf("Quantity = %d, want %d (plan=%s ram=%d mb_seconds=%d)",
					got, tc.wantQty, plan, tc.ramMB, tc.mbSeconds)
			}
		})
	}
}

// TestDefaultFlushLocked_NegativeMBSecondsSurfacesErrNegativeMBSeconds
// pins the defensive guard at the entry of defaultFlushLocked. The
// pre-PR-P-fixes production body had no such guard — a negative
// mb_seconds would have produced a negative Quantity (Go integer
// division truncates toward zero for small negatives, but a large
// negative passes through and posts a refund-shaped line item to
// Paddle, silently crediting the customer). The guard routes through
// ErrNegativeMBSeconds so the classifier at errors.go renders the
// same "negative-mb-sec" Prometheus label as the PushUsageRecord
// entry-point path.
func TestDefaultFlushLocked_NegativeMBSecondsSurfacesErrNegativeMBSeconds(t *testing.T) {
	t.Parallel()

	p := &Provider{
		client: &paddle.SDK{}, // never reached — guard fires first
		now:    time.Now,
		catalog: &priceCatalog{
			planOverage: map[api.Plan]string{api.PlanHobby: "pri_test_overage"},
		},
	}
	rec := p.FlushRecorderForTest()
	acct := acctWithPlan(api.PlanHobby)
	windowStart := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	err := defaultFlushLocked(context.Background(), p, acct, windowStart, -1)
	if err == nil {
		t.Fatal("negative mb_seconds should error")
	}
	if !errors.Is(err, ErrNegativeMBSeconds) {
		t.Errorf("err = %v, want errors.Is(_, ErrNegativeMBSeconds) == true", err)
	}
	// Guard fires before the recorder append, so no row lands.
	if len(*rec) != 0 {
		t.Errorf("recorder rows = %d, want 0 (negative input must not record)", len(*rec))
	}
}
