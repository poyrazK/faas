package meter_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	stripe "github.com/stripe/stripe-go"
)

// newAppWithSlug is the parameterized sibling of newApp for tests
// that need multiple apps in the same MemStore (the slugs collide on
// the "test-app" default). The free/suspended skip test below seeds
// two apps in one store and would otherwise fail at the second
// CreateApp with "slug already taken".
func newAppWithSlug(t *testing.T, ctx context.Context, s *state.MemStore, accountID, slug string) state.App {
	t.Helper()
	a, err := s.CreateApp(ctx, state.App{
		AccountID: accountID,
		Slug:      slug,
		Type:      state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	return a
}

// --- §14 M7 acceptance (push-side) ---
//
// TestInvoiceShadow24h (meter_test.go:232) is the meter-side half of
// the §14 M7 invoice-shadow acceptance gate. The push-side half lives
// here: the GB-hours handed to the SDK must match the same hand-
// computed figure within 0.1 %, so an operator running the acceptance
// suite end-to-end sees the local meter and the value that would be
// pushed to Stripe agree.

// recordingStripe is the meterd-side test fake for the billing.Provider
// the pusher dispatches through (PR #3 / ADR-025). Satisfies the
// full billing.Provider surface — the meterd loop only exercises
// PushUsageRecord, but the conformance assertion in the test file
// guards against accidental partial implementations leaking into
// other tests that need CreateCustomer / VerifyWebhook / etc.
//
// Mirrors fakeParker / fakeNotifier in meter_test.go:18-65 — same
// mutex-guarded slice, no production-code touch. Records every
// (acct.ID, hour, mbSeconds) the pusher passes through, so the test
// can assert the exact integer value the SDK would see against the
// synthetic dataset's hand-computed number.
//
// err is an optional return-error knob — when set, every
// PushUsageRecord returns it (wrapped or unwrapped) before recording
// the call. The TestPushHour_RecordsStripeError test sets err to a
// *stripe.Error so the classifier seam (stripe.ClassifyPushError) is
// exercised through the pusher rather than directly. When err is nil
// the fake returns nil — same behavior as the production stripex
// Client on success.
type recordingStripe struct {
	mu    sync.Mutex
	calls []recordedCall
	err   error
}

type recordedCall struct {
	AccountID string
	Hour      time.Time
	MBSeconds int64
}

// EnsurePlanProducts / CreateCustomer / VerifyWebhook / CreateUpgradeTransaction
// are no-op stubs here — the meterd pusher loop only calls PushUsageRecord.
// Returning (empty, nil) / (empty, empty, nil) for the methods that have
// empty-string "no provider" semantics matches the production shapes
// (stripe.Client returns "" for CreateUpgradeTransaction; the dunning
// state machine is the only caller of VerifyWebhook and never goes
// through this fake).
func (r *recordingStripe) EnsurePlanProducts(_ context.Context) error {
	return nil
}

func (r *recordingStripe) CreateCustomer(_ context.Context, _ state.Account) (string, error) {
	return "", nil
}

func (r *recordingStripe) VerifyWebhook(_ []byte, _ map[string]string, _ time.Duration) (billing.Event, error) {
	return billing.Event{}, nil
}

func (r *recordingStripe) CreateUpgradeTransaction(_ context.Context, _ state.Account, _ api.Plan) (string, string, error) {
	return "", "", nil
}

func (r *recordingStripe) PushUsageRecord(_ context.Context, acct state.Account, hour time.Time, mbSeconds int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedCall{AccountID: acct.ID, Hour: hour, MBSeconds: mbSeconds})
	return r.err
}

func (r *recordingStripe) Calls() []recordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// testOpsMetrics returns a fresh pkg/wire.OpsMetrics registry the test
// can scrape for the stripe-push counter. Lives here (not as a
// package-global) so two tests registering "_stripe_push_total" don't
// collide on the global Prometheus default registry.
func testOpsMetrics(t *testing.T) *wire.OpsMetrics {
	t.Helper()
	return wire.NewOpsMetrics("meter_test_" + t.Name())
}

// scrapeOpsTotal pulls the `_ops_total` counter family out of the test
// registry as a map keyed by `op|code`. Used by TestPushHour_RecordsStripeError
// to assert the classifier→wire seam produced the right label without
// standing up an HTTP handler — the wire package's underlying
// registry is exposed for exactly this in-process test style.
func scrapeOpsTotal(t *testing.T, m *wire.OpsMetrics) map[string]int {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("registry gather: %v", err)
	}
	out := make(map[string]int)
	for _, fam := range families {
		if !strings.HasSuffix(fam.GetName(), "_ops_total") {
			continue
		}
		for _, mv := range fam.GetMetric() {
			var op, code string
			for _, l := range mv.GetLabel() {
				switch l.GetName() {
				case "op":
					op = l.GetValue()
				case "code":
					code = l.GetValue()
				}
			}
			out[op+"|"+code] = int(mv.GetCounter().GetValue())
		}
	}
	return out
}

// TestPushHour_Shadow24h is the §14 M7 push-side acceptance gate.
// Mirror of TestInvoiceShadow24h: a 256 MB Hobby instance resident
// for 24 h drives 1440 minute-ticks of sampling (one row per minute,
// each row = BillableRAMMB(256) * 60 mb_seconds), then 24 PushHour
// ticks (one per hour) must collectively hand the SDK 24 (acct, hour)
// tuples whose summed mb_seconds matches the hand-computed
// 264 * 60 * 60 * 24 = 22_809_600 mb_seconds exactly.
//
// The assertion is two-level on purpose:
//   - per-call: each of the 24 PushHour calls hands the SDK exactly
//     one HourWindow's worth (60 minute-rows summed). Catches a
//     regression in the pusher's per-hour window shape — e.g. if
//     someone refactors PushHour to read across the full 24h at once
//     instead of walking per-hour SourceWindow, the per-call check
//     surfaces it before the total check does.
//   - total: the sum across the 24 calls equals the spec's 24h bill
//     exactly. This is the M7 acceptance.
//
// Drop the per-call check only when the wire-quantity path stops
// being per-hour — i.e. not before PushHour is renamed and
// production cadence becomes daily.
//
// The "24 h" framing is the spec; the math is the acceptance.
//
// Why 24 PushHour calls instead of one: HourWindow is a one-hour
// window — the production loop pushes the past 24h once per day
// (cfg.StripeInterval = 24h) but the pusher's *internal* logic walks
// per-hour SourceWindow. The 24-call test mirrors the per-hour
// exercise of the SDK-bound interface so any per-hour drift bug
// surfaces in the unit test before the live-sandbox job.
//
// The assertion is integer equality, not a percentage tolerance. The
// integer-wire path (pkg/billing/stripe/usage.go) is deterministic — any
// drift here means the meter's mb_seconds accumulator is broken.
//
// Sample layout: starting at T0 (top of hour) and stepping `now`
// AFTER each SampleAndRoll, the 1440 samples land at minutes
// [T0, T0+23h+59min] = [T0, T0+24h). That spans exactly 24 distinct
// hour-buckets from [T0, T0+1h) through [T0+23h, T0+24h) with no
// spillover, and 24 PushHour ticks at `now = T0+1h, T0+2h, …,
// T0+24h` cover them one-for-one — 60 samples per bucket.
func TestPushHour_Shadow24h(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()

	t0 := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC)
	now := t0
	clock := func() time.Time { return now }

	// Hobby plan: free-tier hard-stop is gated behind 5 GB-h on the
	// Free plan, so Hobby is the canonical "real customer" account
	// for the acceptance scenario. Status defaults to AccountActive.
	acct := makeAccount(t, ctx, s, api.PlanHobby)
	app := newApp(t, ctx, s, acct.ID)
	makeLiveInstance(t, ctx, s, app.ID, acct.ID, 256)

	sampler := meter.NewSampler(s, clock)
	const hoursIn24h = 24
	const minutesIn24h = hoursIn24h * 60
	for i := 0; i < minutesIn24h; i++ {
		if _, err := sampler.SampleAndRoll(ctx); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
		now = now.Add(time.Minute)
	}
	// After 1440 minute-steps `now` = T0 + 24h. The samples landed at
	// minutes [T0, T0+23h+59min] = [T0, T0+24h), spanning exactly 24
	// hour-buckets from [T0, T0+1h) through [T0+23h, T0+24h). The
	// PushHour at "now = T0+1h" covers [T0, T0+1h) (the first
	// bucket); at "now = T0+24h" covers [T0+23h, T0+24h) (the last).
	const hoursToPush = hoursIn24h

	rec := &recordingStripe{}
	pusher := meter.NewPusher(s, rec, discardLog(), clock, nil)

	// pin `now` to the top of the hour after the first sample. The
	// sample loop's first sample was at T0, so its hour bucket is
	// [T0, T0+1h) — PushHour at T0+1h covers it.
	now = t0.Add(time.Hour)
	for h := 0; h < hoursToPush; h++ {
		pushed, err := pusher.PushHour(ctx)
		if err != nil {
			t.Fatalf("PushHour hour %d: %v", h, err)
		}
		if pushed != 1 {
			t.Errorf("PushHour hour %d (now=%s): pushed = %d, want 1",
				h, now.UTC().Format(time.RFC3339), pushed)
		}
		now = now.Add(time.Hour)
	}

	calls := rec.Calls()
	if len(calls) != hoursToPush {
		t.Fatalf("recorded calls = %d, want %d (one per hour)", len(calls), hoursToPush)
	}
	var totalMB int64
	// Per-hour mb_seconds: the sampler stamps 60 rows of
	// api.BillableRAMMB(256) * 60 mb_seconds each (one per minute),
	// and UsageByHour sums across the [start, end) window. So one
	// hour-window total = 60 samples × per-minute = 60 × 60 × billable
	// = 3600 × billable. For a 256 MB Hobby instance: 3600 × 264 =
	// 950_400 mb_seconds per hour.
	wantPerHour := int64(api.BillableRAMMB(256)) * 60 * 60 // 264 * 3600 = 950_400
	for i, c := range calls {
		if c.AccountID != acct.ID {
			t.Errorf("call[%d].AccountID = %q, want %q", i, c.AccountID, acct.ID)
		}
		if c.MBSeconds != wantPerHour {
			t.Errorf("call[%d].MBSeconds = %d, want %d (one hour of 256 MB Hobby = 60 minute-rows summed)",
				i, c.MBSeconds, wantPerHour)
		}
		totalMB += c.MBSeconds
	}
	// Hand-computed sum across 24 hours: billable * 60 * 60 * 24.
	// Uses BillableRAMMB so a future PerVMOverheadMB change keeps
	// the test equation in sync.
	wantTotal := int64(api.BillableRAMMB(256)) * 60 * 60 * hoursIn24h
	if totalMB != wantTotal {
		t.Fatalf("push-side shadow sum = %d mb_sec, want %d (exact integer equality)",
			totalMB, wantTotal)
	}
}

// TestPushHour_SkipsZeroGB pins the skip semantics: an account with
// no usage rows in the past hour must not produce an SDK call. The
// dashboard's "pushed-this-hour" counter depends on this — a card
// with zero usage pushing 0.000 GB would inflate the count and
// silently mask real push failures.
func TestPushHour_SkipsZeroGB(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	// No app, no instance, no sampled usage — the push path runs over
	// zero rows and must produce zero SDK calls.

	rec := &recordingStripe{}
	pusher := meter.NewPusher(s, rec, discardLog(), clock, nil)

	pushed, err := pusher.PushHour(ctx)
	if err != nil {
		t.Fatalf("PushHour: %v", err)
	}
	if pushed != 0 {
		t.Errorf("pushed = %d, want 0", pushed)
	}
	if got := len(rec.Calls()); got != 0 {
		t.Errorf("recorded calls = %d, want 0 (no usage rows ⇒ no SDK call)", got)
	}
}

// TestPushHour_SkipsFreeAndSuspended pins the two structural skip
// branches. Free plan has no Stripe customer so no push (no overage
// billing for free-tier accounts); suspended accounts are exempt
// because their billing is frozen. Both must NOT produce an SDK call
// — a leaked push to a suspended account would be a billing bug.
func TestPushHour_SkipsFreeAndSuspended(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	freeAcct := makeAccount(t, ctx, s, api.PlanFree)
	freeApp := newAppWithSlug(t, ctx, s, freeAcct.ID, "free-app")
	makeLiveInstance(t, ctx, s, freeApp.ID, freeAcct.ID, 128)

	suspendedAcct := makeAccount(t, ctx, s, api.PlanHobby)
	suspendedApp := newAppWithSlug(t, ctx, s, suspendedAcct.ID, "suspended-app")
	makeLiveInstance(t, ctx, s, suspendedApp.ID, suspendedAcct.ID, 256)
	if err := s.UpdateAccountStatus(ctx, suspendedAcct.ID, state.AccountSuspended); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	// Sample one hour so both accounts have non-zero usage_minutes rows.
	sampler := meter.NewSampler(s, clock)
	for i := 0; i < 60; i++ {
		now = now.Add(time.Minute)
		if _, err := sampler.SampleAndRoll(ctx); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
	}
	now = now.Add(time.Hour)

	rec := &recordingStripe{}
	pusher := meter.NewPusher(s, rec, discardLog(), clock, nil)
	pushed, err := pusher.PushHour(ctx)
	if err != nil {
		t.Fatalf("PushHour: %v", err)
	}
	if pushed != 0 {
		t.Errorf("pushed = %d, want 0 (Free + suspended both skip)", pushed)
	}
	if got := len(rec.Calls()); got != 0 {
		t.Errorf("recorded calls = %d, want 0 (Free + suspended both skip)", got)
	}
}

// TestPushHour_RecordsStripeError is the classifier-seam integration
// test. The cmd/meterd daemon-subprocess test exercises the same
// code path, but only when Postgres is available; this test pins the
// pusher-to-wire contract in-process so the seam can't drift without
// CI catching it.
//
// The fake StripePusher returns a wrapped *stripe.Error{Type:
// ErrorTypeCard} on every call — the canonical "customer's card
// declined" failure. The pusher must:
//  1. still attempt the SDK call (recordingStripe saw the call),
//  2. invoke stripe.ClassifyPushError on the returned error,
//  3. feed the resulting "card-error" code into ops.ObserveCode so
//     `meterd_ops_total{op="stripe",code="card-error"}` increments.
//
// Why Card and not RateLimit: card-error is the most operator-
// actionable bucket (route to customer's billing UI, not a meterd
// backoff). The rate-limit path is structurally identical and covered
// by the stripe unit tests at pkg/billing/stripe/usage_test.go.
func TestPushHour_RecordsStripeError(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()

	t0 := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC)
	now := t0
	clock := func() time.Time { return now }

	acct := makeAccount(t, ctx, s, api.PlanHobby)
	app := newApp(t, ctx, s, acct.ID)
	makeLiveInstance(t, ctx, s, app.ID, acct.ID, 256)

	// One hour of sampling produces exactly one billable (acct, hour)
	// pair — the simplest setup where PushHour can attempt a single
	// SDK call.
	sampler := meter.NewSampler(s, clock)
	for i := 0; i < 60; i++ {
		if _, err := sampler.SampleAndRoll(ctx); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
		now = now.Add(time.Minute)
	}
	// After 60 minute-steps now = T0 + 1h. HourWindow(T0+1h) returns
	// [T0, T0+1h) — exactly the span the 60 samples landed in.
	// No further advance: pushing the clock past T0+1h would shift the
	// window into [T0+1h, T0+2h) and find no samples.

	rec := &recordingStripe{
		err: fmt.Errorf("stripe: UsageRecords.New account %s hour %s: %w",
			acct.ID, now.UTC().Format(time.RFC3339),
			&stripe.Error{Type: stripe.ErrorTypeCard, HTTPStatusCode: 402}),
	}
	ops := testOpsMetrics(t)
	pusher := meter.NewPusher(s, rec, discardLog(), clock, ops)

	pushed, err := pusher.PushHour(ctx)
	if err != nil {
		t.Fatalf("PushHour returned aggregate error: %v (per-account errors must not surface)", err)
	}
	if pushed != 0 {
		t.Errorf("pushed = %d, want 0 (Stripe returned an error → push did not complete)", pushed)
	}
	if got := len(rec.Calls()); got != 1 {
		t.Fatalf("recorded calls = %d, want 1 (the pusher must still attempt the SDK call before classifying)", got)
	}

	// Scrape the test registry directly — the wire package exposes
	// the underlying registry so tests can assert metric shape without
	// scraping via HTTP. The contract pinned here is:
	//   meterd_ops_total{op="stripe",code="card-error"} = 1
	// The loop-tick path uses code="ok"|"err" only — the per-push
	// classification feeds into the same `ops` counter, but here we
	// observe only the push itself (no Loop wrapping), so the counter
	// should land at 1 with the classified label.
	body := scrapeOpsTotal(t, ops)
	if got := body[`stripe|card-error`]; got != 1 {
		t.Errorf("ops_total{op=stripe,code=card-error} = %d, want 1 (classifier seam must feed the wire counter)", got)
	}
}
