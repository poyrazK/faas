package meter_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	paddleclassifier "github.com/onebox-faas/faas/pkg/billing/paddle"
	stripeclassifier "github.com/onebox-faas/faas/pkg/billing/stripe"
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

func makeBillableAccount(t *testing.T, ctx context.Context, s *state.MemStore, plan api.Plan) state.Account {
	t.Helper()
	acct := makeAccount(t, ctx, s, plan)
	if plan == api.PlanFree {
		return acct
	}
	customerID := "customer-" + acct.ID
	subscriptionID := "subscription-" + acct.ID
	if err := s.UpdateAccountProviderCustomerID(ctx, acct.ID, customerID); err != nil {
		t.Fatalf("stamp provider customer: %v", err)
	}
	if err := s.UpdateAccountStripeSubscriptionItem(ctx, acct.ID, subscriptionID); err != nil {
		t.Fatalf("stamp subscription: %v", err)
	}
	acct.ProviderCustomerID = customerID
	acct.StripeSubscriptionItem = subscriptionID
	return acct
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

type recordingOverage struct {
	recordingStripe
}

func (*recordingOverage) UsageMode() billing.UsageMode {
	return billing.UsageModeOverage
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

// Refund is the issue #279 billing.Provider seam. meterd's pusher
// never calls it; returning ErrNotImplemented matches the Paddle
// contract documented in pkg/billing/provider.go.
func (r *recordingStripe) Refund(_ context.Context, _ string, _ int64) (*billing.RefundResult, error) {
	return nil, billing.ErrNotImplemented
}

// ReconcileUsage is the ADR-049 §B.1 drift-detector seam. The
// pusher_shadow tests don't drive the reconciler, so we return
// (0, nil) — the recording stub doesn't need to record drift.
func (r *recordingStripe) ReconcileUsage(_ context.Context, _ state.Account, _, _ time.Time) (int64, error) {
	return 0, nil
}

// RetryLatestCharge / CancelAtPeriodEnd / PaymentMethodSummary (issue #242):
// meterd never drives these surfaces (only apid does). Zero-value
// stubs are enough to satisfy the interface contract.
func (r *recordingStripe) RetryLatestCharge(_ context.Context, _ state.Account) (string, string, error) {
	return "", "", nil
}
func (r *recordingStripe) CancelAtPeriodEnd(_ context.Context, _ state.Account) (time.Time, error) {
	return time.Time{}, nil
}
func (r *recordingStripe) PaymentMethodSummary(_ context.Context, _ state.Account) (billing.PaymentMethod, error) {
	return billing.PaymentMethod{}, nil
}

func (r *recordingStripe) PushUsageRecord(_ context.Context, acct state.Account, hour time.Time, mbSeconds int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedCall{AccountID: acct.ID, Hour: hour, MBSeconds: mbSeconds})
	return r.err
}

// Capabilities returns the Stripe-shaped set the meter pusher
// observes in production for the surfaces this stub actually
// implements. The shadow test exercises the metered usage +
// sandbox surfaces; Refund is intentionally omitted from the
// bitmask because the stub's Refund() returns ErrNotImplemented
// (the real *stripe.Client implements Refund — the stub is
// intentionally minimal).
func (r *recordingStripe) Capabilities() billing.CapabilitySet {
	return billing.CapabilitySet(billing.CapUsageMetered | billing.CapSandbox)
}

// ClassifyPushError opts recordingStripe into the meterd pusher's
// billing.Classifier dispatch (added with the big-Paddle-enablement
// PR). The meterd pusher's classifier seam feeds synthetic errors
// through the same code path as a real *stripe.Client, so tests can
// pin the classifier→wire counter behavior end-to-end. Mirrors the
// Stripe classifier at pkg/billing/stripe/usage_test.go:131 — the
// label set is the same closed set ClassifyPushError uses
// production.
//
// nil → "ok" mirrors stripe.ClassifyPushError; the meterd pusher
// already short-circuits on nil before reaching this method, but the
// guard stays here for defensive callers.
func (r *recordingStripe) ClassifyPushError(err error) string {
	if err == nil {
		return "ok"
	}
	return stripeclassifier.ClassifyPushError(err)
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
	acct := makeBillableAccount(t, ctx, s, api.PlanHobby)
	app := newApp(t, ctx, s, acct.ID)
	makeLiveInstance(t, ctx, s, app.ID, acct.ID, 256)

	sampler := meter.NewSampler(s, nil, clock)
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

	suspendedAcct := makeBillableAccount(t, ctx, s, api.PlanHobby)
	suspendedApp := newAppWithSlug(t, ctx, s, suspendedAcct.ID, "suspended-app")
	makeLiveInstance(t, ctx, s, suspendedApp.ID, suspendedAcct.ID, 256)
	if err := s.UpdateAccountStatus(ctx, suspendedAcct.ID, state.AccountSuspended); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	// Sample one hour so both accounts have non-zero usage_minutes rows.
	sampler := meter.NewSampler(s, nil, clock)
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

	acct := makeBillableAccount(t, ctx, s, api.PlanHobby)
	app := newApp(t, ctx, s, acct.ID)
	makeLiveInstance(t, ctx, s, app.ID, acct.ID, 256)

	// One hour of sampling produces exactly one billable (acct, hour)
	// pair — the simplest setup where PushHour can attempt a single
	// SDK call.
	sampler := meter.NewSampler(s, nil, clock)
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

// --- Paddle dispatch seam regression (PR #204 review Fix #5) ---

// scrapePushDuration pulls a per-provider histogram family out of
// the test registry as a map keyed by `result`. The pusher observes
// into PaddlePushDuration / StripePushDuration histograms; the
// histogram label is `result` (not `code` — see
// pkg/wire/metrics.go:152). This is the assertion target for the
// dispatch tests — confirms the type-switch routed the call to the
// correct histogram (not the other-provider fallback).
func scrapePushDuration(t *testing.T, m *wire.OpsMetrics, suffix string) map[string]int {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("registry gather: %v", err)
	}
	out := map[string]int{}
	for _, fam := range families {
		if !strings.HasSuffix(fam.GetName(), suffix) {
			continue
		}
		for _, mv := range fam.GetMetric() {
			var code string
			for _, l := range mv.GetLabel() {
				if l.GetName() == "result" {
					code = l.GetValue()
				}
			}
			out[code] = int(mv.GetHistogram().GetSampleCount())
		}
	}
	return out
}

// TestPushHour_PaddleDispatchHitsPaddleHistogram pins the
// Paddle-side dispatch seam end-to-end. A real *paddle.Provider
// is constructed via the package's NewProviderForTest seam (a
// nil-client *Provider that satisfies billing.Provider +
// billing.Classifier) so the pusher's providerOpsFor type-switch
// MUST route it onto:
//
//   - opLabel="paddle" (not "stripe") — asserted via the
//     meterd_ops_total counter family.
//   - the _paddle_push_duration_seconds histogram (not the
//     stripe one) — asserted via the histogram family.
//
// providerOpsFor dispatches on concrete type, so a test fake
// satisfying only the Provider interface would land on the Stripe
// fallback. The real *paddle.Provider exercises the correct arm.
//
// This is the load-bearing regression net for Fix #5: pre-fix,
// the type-switch + the classifier lookup were two separate
// seams (pusherDispatch + classifyProviderError) that could drift
// — a Provider whose classifier was implemented but whose
// concrete type was not in pusherDispatch's switch would have
// landed on the Stripe histogram with the provider's own
// classifier label. After Fix #5 the dispatch is a single
// type-switch so the two cannot drift.
func TestPushHour_PaddleDispatchHitsPaddleHistogram(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()

	t0 := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC)
	now := t0
	clock := func() time.Time { return now }

	acct := makeBillableAccount(t, ctx, s, api.PlanHobby)
	app := newApp(t, ctx, s, acct.ID)
	makeLiveInstance(t, ctx, s, app.ID, acct.ID, 256)

	sampler := meter.NewSampler(s, nil, clock)
	for i := 0; i < 60; i++ {
		if _, err := sampler.SampleAndRoll(ctx); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
		now = now.Add(time.Minute)
	}

	// Real *paddle.Provider via the test seam. The push path's pre-SDK
	// guards (ErrNoAPIKey on nil client, ErrNegativeMBSeconds on
	// negative qty, ErrOveragePriceMissing on missing catalog) cover
	// the case where the stub flushFn isn't injected; for the
	// happy-path "flush succeeds, code='ok'" assertion we inject a
	// no-op flushFn + a primed catalog.
	var calls int
	p := paddleclassifier.NewProviderForTest(discardLog())
	p.FlushFnForTest(func(_ context.Context, _ *paddleclassifier.Provider, _ state.Account, _ time.Time, _ int64) error {
		calls++
		return nil
	})
	p.SetOveragePriceForTest(api.PlanHobby, "pri_test_overage")
	p.SetDedupeForTest(nil) // bypass dedupe gate — we want the flushFn to fire

	ops := testOpsMetrics(t)
	pusher := meter.NewPusher(s, p, discardLog(), clock, ops)

	if _, err := pusher.PushHour(ctx); err != nil {
		t.Fatalf("PushHour returned aggregate error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("recorded flush calls = %d, want 1", calls)
	}

	// Paddle histogram: must observe exactly one sample with code="ok".
	pushBody := scrapePushDuration(t, ops, "_paddle_push_duration_seconds")
	if got := pushBody["ok"]; got != 1 {
		t.Errorf("_paddle_push_duration_seconds{code=ok} count = %d, want 1 (Paddle dispatch must hit Paddle histogram)", got)
	}
	// Stripe histogram: must NOT observe a sample with code="ok" —
	// pre-Fix-5, a non-Stripe-shaped Provider could land on the
	// Stripe fallback histogram. Belt + braces.
	stripeBody := scrapePushDuration(t, ops, "_stripe_push_duration_seconds")
	if got := stripeBody["ok"]; got != 0 {
		t.Errorf("_stripe_push_duration_seconds{code=ok} count = %d, want 0 (Paddle dispatch must NOT hit Stripe histogram)", got)
	}

	// Ops counter: op="paddle", code="ok" must be 1.
	opsBody := scrapeOpsTotal(t, ops)
	if got := opsBody[`paddle|ok`]; got != 1 {
		t.Errorf("ops_total{op=paddle,code=ok} = %d, want 1 (op label must be paddle)", got)
	}
	if _, ok := opsBody[`stripe|ok`]; ok {
		t.Errorf("ops_total{op=stripe,code=ok} present; want absent (Paddle dispatch must NOT register a stripe op row)")
	}
}

// --- §14 M7 push-side shadow (Stripe + Paddle dual) ---
//
// The pre-existing TestPushHour_Shadow24h (line 219) covers Stripe
// only via the recordingStripe fake. ADR-032's provider-neutrality
// claim extends the same 24h math to Paddle; the two tests below
// pin the dual shape in-process so a future refactor of the pusher's
// dispatch, the SDK conversion, or the FlushFn signature surfaces
// in the correct provider's test before reaching production.
//
// Per-hour expected value (256 MB Hobby instance, 1 hour resident):
//   api.BillableRAMMB(256) * 60 * 60 = 264 * 3600 = 950_400 mb_seconds
// Sum across 24 hours: 22_809_600 mb_seconds.
// Integer equality (NOT a 0.1% float delta — that tolerance lives on
// the meter-side monthly aggregation in meter_test.go:256; the push-
// side wire math is integer-deterministic).

// TestPushHour_Shadow24h_StripeFake is the Stripe-shaped sibling of
// the existing TestPushHour_Shadow24h. Same math, same sampler
// loop, same recordingStripe fake — the only difference is the
// test name, which routes a future regression to this test rather
// than the canonical one. Belongs next to TestPushHour_Shadow24h
// because the two share a fake; the Paddle sibling below swaps
// the fake.
//
// The 1440 minute-rows / 24 PushHour ticks shape mirrors the
// canonical test exactly. See TestPushHour_Shadow24h (line 219)
// for the math derivation.
func TestPushHour_Shadow24h_StripeFake(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()

	t0 := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC)
	now := t0
	clock := func() time.Time { return now }

	acct := makeBillableAccount(t, ctx, s, api.PlanHobby)
	app := newApp(t, ctx, s, acct.ID)
	makeLiveInstance(t, ctx, s, app.ID, acct.ID, 256)

	sampler := meter.NewSampler(s, nil, clock)
	const hoursIn24h = 24
	const minutesIn24h = hoursIn24h * 60
	for i := 0; i < minutesIn24h; i++ {
		if _, err := sampler.SampleAndRoll(ctx); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
		now = now.Add(time.Minute)
	}

	rec := &recordingStripe{}
	pusher := meter.NewPusher(s, rec, discardLog(), clock, nil)

	now = t0.Add(time.Hour)
	for h := 0; h < hoursIn24h; h++ {
		pushed, err := pusher.PushHour(ctx)
		if err != nil {
			t.Fatalf("PushHour hour %d: %v", h, err)
		}
		if pushed != 1 {
			t.Errorf("PushHour hour %d: pushed = %d, want 1", h, pushed)
		}
		now = now.Add(time.Hour)
	}

	calls := rec.Calls()
	if len(calls) != hoursIn24h {
		t.Fatalf("recorded calls = %d, want %d", len(calls), hoursIn24h)
	}
	wantPerHour := int64(api.BillableRAMMB(256)) * 60 * 60
	wantTotal := wantPerHour * hoursIn24h
	var total int64
	for i, c := range calls {
		if c.AccountID != acct.ID {
			t.Errorf("call[%d].AccountID = %q, want %q", i, c.AccountID, acct.ID)
		}
		if c.MBSeconds != wantPerHour {
			t.Errorf("call[%d].MBSeconds = %d, want %d", i, c.MBSeconds, wantPerHour)
		}
		total += c.MBSeconds
	}
	if total != wantTotal {
		t.Fatalf("Stripe shadow sum = %d mb_sec, want %d (exact integer)", total, wantTotal)
	}
}

// TestPushHour_Shadow24h_Paddle is the Paddle-shaped sibling of
// TestPushHour_Shadow24h_StripeFake. Same math, different fake —
// the paddle.Provider test seam (NewProviderForTest +
// FlushFnForTest + SetOveragePriceForTest + SetDedupeForTest) is
// the canonical pin for the per-window FlushFn contract.
//
// Per-tick assertion: each FlushFn call sees the integer
// mb_seconds the pusher computed for one 1h window. Per-call
// distinctness: 24 distinct windowStart values (top-of-hour
// boundaries [t0, t0+1h), [t0+1h, t0+2h), …, [t0+23h, t0+24h)).
// Sum: 22_809_600 mb_seconds.
//
// SetDedupeForTest(nil) bypasses the claim state machine because
// the math is the assertion target, not the dedupe gate (the e2e
// TestInvoiceShadow_24h covers the dedupe path against real
// Postgres).
func TestPushHour_Shadow24h_Paddle(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()

	t0 := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC)
	now := t0
	clock := func() time.Time { return now }

	acct := makeBillableAccount(t, ctx, s, api.PlanHobby)
	app := newApp(t, ctx, s, acct.ID)
	makeLiveInstance(t, ctx, s, app.ID, acct.ID, 256)

	sampler := meter.NewSampler(s, nil, clock)
	const hoursIn24h = 24
	const minutesIn24h = hoursIn24h * 60
	for i := 0; i < minutesIn24h; i++ {
		if _, err := sampler.SampleAndRoll(ctx); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
		now = now.Add(time.Minute)
	}

	type flushCall struct {
		AccountID string
		Hour      time.Time
		MBSeconds int64
	}
	var (
		mu    sync.Mutex
		calls []flushCall
	)
	p := paddleclassifier.NewProviderForTest(discardLog())
	p.FlushFnForTest(func(_ context.Context, _ *paddleclassifier.Provider, a state.Account, hour time.Time, mbSeconds int64) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, flushCall{AccountID: a.ID, Hour: hour, MBSeconds: mbSeconds})
		return nil
	})
	p.SetOveragePriceForTest(api.PlanHobby, "pri_test_overage")
	p.SetDedupeForTest(nil)

	pusher := meter.NewPusher(s, p, discardLog(), clock, nil)

	now = t0.Add(time.Hour)
	for h := 0; h < hoursIn24h; h++ {
		pushed, err := pusher.PushHour(ctx)
		if err != nil {
			t.Fatalf("PushHour hour %d: %v", h, err)
		}
		if pushed != 1 {
			t.Errorf("PushHour hour %d: pushed = %d, want 1", h, pushed)
		}
		now = now.Add(time.Hour)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != hoursIn24h {
		t.Fatalf("recorded flush calls = %d, want %d", len(calls), hoursIn24h)
	}
	wantPerHour := int64(api.BillableRAMMB(256)) * 60 * 60
	wantTotal := wantPerHour * hoursIn24h
	seenHours := make(map[time.Time]bool, hoursIn24h)
	var total int64
	for i, c := range calls {
		if c.AccountID != acct.ID {
			t.Errorf("call[%d].AccountID = %q, want %q", i, c.AccountID, acct.ID)
		}
		if c.MBSeconds != wantPerHour {
			t.Errorf("call[%d].MBSeconds = %d, want %d", i, c.MBSeconds, wantPerHour)
		}
		if seenHours[c.Hour] {
			t.Errorf("call[%d].Hour = %s: duplicate windowStart across 24 ticks", i, c.Hour.UTC().Format(time.RFC3339))
		}
		seenHours[c.Hour] = true
		total += c.MBSeconds
	}
	if total != wantTotal {
		t.Fatalf("Paddle shadow sum = %d mb_sec, want %d (exact integer)", total, wantTotal)
	}
}

// TestPushHour_ExcludesTailSeconds (issue #667 / ADR-078) is the
// load-bearing permanent guard: the per-instance waitUntil tail
// counter accumulates wall-clock seconds a wake spends draining
// `waitUntil(promise)` tasks AFTER its HTTP response has flushed.
// ADR-078 §"Decisions" #4 pins this metric as INFORMATIONAL ONLY —
// it MUST NOT enter Math.GBHours, Provider.PushUsageRecord, or any
// other billing path. The customership is predictable: customers
// pay exactly the plan RAM × resident-seconds that the synchronous
// request envelope covered; tail drains are a free operational
// primitive (a Cloudflare Workers / Vercel Edge / AWS Lambda
// parity feature).
//
// If a future PR reverses this invariant — e.g. by adding a
// `tailSeconds` parameter to billing.Provider.PushUsageRecord, or
// by adding tail_seconds to Math.GBHours, or by introducing a
// TailOverageProvider — this test fires. Removing it requires
// removing the ADR-078 §"Tail is informational" decision (i.e.
// a new ADR), so the only way to land an inverted-billing change
// is to also argue the case in writing. That is the load-bearing
// shape: the test, not the docs, is the spec.
//
// The assertion is two-level on purpose:
//  1. The pusher still attempts the SDK call. A non-zero tail_seconds
//     on the usage_minutes row must not poison the push path into
//     skipping the (acct, hour) tuple. This pins the "informational
//     only" wording: the column coexists with mb_seconds without
//     affecting the skip semantics at pusher.go:150 (mbSec <= 0 skip).
//  2. The SDK sees exactly the billable (acct, hour, mb_seconds)
//     triple — the recorded MBSeconds equals the hand-computed
//     plan-RAM × resident-seconds figure with zero contamination
//     from the tail_seconds accumulator.
//
// Why this is a "permanent guard" rather than a one-shot regression
// test: it lives next to TestPushHour_Shadow24h (the §14 M7
// acceptance) and uses the same rec + pusher + synthetic dataset
// wiring, but its assertion is forward-looking — the assertion
// shape ("TailSeconds MUST NOT reach the SDK call") is the
// contractual shape, not a specific number. A future refactor of
// the pusher that surfaces tail_seconds to billing will fail this
// test regardless of how the new billing field is named.
func TestPushHour_ExcludesTailSeconds(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()

	// Compile-time pin 1: the billing.Provider.PushUsageRecord signature
	// must remain (ctx, acct, hour, mbSeconds). A future PR that adds
	// a `tailSeconds int64` parameter to PushUsageRecord (mirroring
	// the AppendUsage tail_seconds column) would break this pin and
	// force a deliberate change to the test file. ADR-078 §"Tail is
	// informational" is the load-bearing invariant; a follow-up ADR
	// would have to remove this.
	//
	// The pattern is a function-type alias pinned to the method's
	// signature via a typed assignment. If PushUsageRecord's
	// signature drifts (parameter added / removed / retyped), the
	// compiler rejects the assignment below because the function
	// type no longer matches. We use a recordingStripe-conforming
	// cast that does not invoke the method at runtime — the conformance
	// is enforced at the type-system level only.
	type pushUsageRecordSignature func(ctx context.Context, acct state.Account, hour time.Time, mbSeconds int64) error
	var _ pushUsageRecordSignature = (*recordingStripe)(nil).PushUsageRecord

	// Compile-time pin 2: the recordedCall struct must have exactly
	// three fields (AccountID, Hour, MBSeconds). A future PR that
	// adds a `TailSeconds int64` field (mirroring DailyUsage.TailSeconds)
	// would either fire the reflection assertion below or break the
	// struct literal at recordingStripe.PushUsageRecord. We use
	// `reflect` here so the check is mechanical, not a happy-path
	// reading.
	recShape := &recordedCall{}
	rt := reflect.TypeOf(*recShape)
	if rt.NumField() != 3 {
		t.Fatalf("recordedCall has %d fields, want exactly 3 (AccountID, Hour, MBSeconds); a future PR that added TailSeconds would break the load-bearing contract that tail_seconds is informational only — see ADR-078 §\"Tail is informational\"", rt.NumField())
	}
	want := map[string]bool{"AccountID": true, "Hour": true, "MBSeconds": true}
	for i := 0; i < rt.NumField(); i++ {
		if !want[rt.Field(i).Name] {
			t.Errorf("recordedCall.%s = unexpected field; want fields are exactly AccountID, Hour, MBSeconds", rt.Field(i).Name)
		}
	}

	t0 := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC)
	now := t0
	clock := func() time.Time { return now }

	acct := makeBillableAccount(t, ctx, s, api.PlanHobby)
	app := newApp(t, ctx, s, acct.ID)
	ins := makeLiveInstance(t, ctx, s, app.ID, acct.ID, 256)

	// Sample one hour so usage_minutes has 60 rows for this instance.
	// Each row carries the canonical billable (api.BillableRAMMB(256) *
	// 60) mb_seconds AND a non-zero tail_seconds (the runner's tail
	// host observed the instance draining waitUntil tasks in the
	// background). The synthetic dataset deliberately saturates both
	// axes — a 30-second tail per minute, scaled by 60 minutes, is
	// 1,800 tail_seconds. If the pusher were to forward that figure
	// into the SDK call it would show up in the recorded MBSeconds.
	const nonZeroTailSecondsPerMinute int64 = 30
	for i := 0; i < 60; i++ {
		// Stamp the per-minute row with a non-zero tail_seconds. The
		// sampler reads from a TailSecondsSource closure (cmd/meterd
		// wires pkg/fcvm.Manager.ReadAndResetTailSeconds). For this
		// unit test we go around the sampler and stamp directly:
		// the contract we are pinning is the pusher's read path, not
		// the sampler's write path (covered by sampler_tail_test.go).
		if err := s.AppendUsage(ctx, acct.ID, app.ID, ins.ID,
			now, int64(api.BillableRAMMB(256))*60, 1,
			0, 0, 0, 0, 0, nonZeroTailSecondsPerMinute,
		); err != nil {
			t.Fatalf("sample %d AppendUsage: %v", i, err)
		}
		now = now.Add(time.Minute)
	}
	// Pin now = T0+1h so HourWindow = [T0, T0+1h).
	now = t0.Add(time.Hour)

	rec := &recordingStripe{}
	pusher := meter.NewPusher(s, rec, discardLog(), clock, nil)
	pushed, err := pusher.PushHour(ctx)
	if err != nil {
		t.Fatalf("PushHour: %v", err)
	}
	// Assertion 1: the push still happens — tail_seconds is not a
	// skip-axis. A non-zero tail must not poison the skip path at
	// pusher.go:150 (mbSec <= 0 skip).
	if pushed != 1 {
		t.Errorf("pushed = %d, want 1 (tail_seconds is informational — must not skip push)", pushed)
	}

	calls := rec.Calls()
	if len(calls) != 1 {
		t.Fatalf("recorded calls = %d, want 1", len(calls))
	}
	// Assertion 2: the SDK saw exactly the billable figure.
	// Plan-RAM-resident-seconds = api.BillableRAMMB(256) * 60 * 60
	// (256 MB Hobby, 60 minutes resident). 1,800 tail_seconds is a
	// red herring — the SDK call must NOT include it. If a future
	// PR adds tail_seconds to the SDK call, this assertion is the
	// load-bearing pin that fires.
	wantMB := int64(api.BillableRAMMB(256)) * 60 * 60
	if calls[0].MBSeconds != wantMB {
		t.Errorf("PushUsageRecord MBSeconds = %d, want %d (tail_seconds MUST NOT contaminate the SDK call)",
			calls[0].MBSeconds, wantMB)
	}
	// Assertion 3: the (acct, hour) tuple is the canonical one —
	// not a synthesised "tail-only" call that would be a billing
	// bypass.
	if calls[0].AccountID != acct.ID {
		t.Errorf("PushUsageRecord AccountID = %q, want %q", calls[0].AccountID, acct.ID)
	}
	if !calls[0].Hour.Equal(t0) {
		t.Errorf("PushUsageRecord Hour = %s, want %s (HourWindow[T0+1h).start = T0)",
			calls[0].Hour.UTC().Format(time.RFC3339), t0.UTC().Format(time.RFC3339))
	}
}
