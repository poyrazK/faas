package meter_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
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

// recordingStripe is the meterd-side test fake for the Stripe pusher.
// Mirrors fakeParker / fakeNotifier in meter_test.go:18-65 — same
// mutex-guarded slice, no production-code touch. Records every
// (acct.ID, hour, gb) the pusher passes through, so the test can
// assert the exact value the SDK would see against the synthetic
// dataset's hand-computed number.
type recordingStripe struct {
	mu    sync.Mutex
	calls []recordedCall
}

type recordedCall struct {
	AccountID string
	Hour      time.Time
	GBHours   float64
}

func (r *recordingStripe) PushUsageRecord(_ context.Context, acct state.Account, hour time.Time, gbHours float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedCall{AccountID: acct.ID, Hour: hour, GBHours: gbHours})
	return nil
}

func (r *recordingStripe) Calls() []recordedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// TestPushHour_Shadow24h is the §14 M7 push-side acceptance gate.
// Mirror of TestInvoiceShadow24h: a 256 MB Hobby instance resident
// for 24 h drives 1440 minute-ticks of sampling, then 24 PushHour
// ticks (one per hour) must collectively hand the SDK 24 (acct, hour)
// tuples whose summed gb matches the hand-computed (264 * 60 * 1440)
// / (1024 * 3600) figure = 6.187500 GB-h to 6 dp, within 0.1 %.
// The "24 h" framing is the spec; the math is the acceptance.
//
// Why 24 PushHour calls instead of one: HourWindow is a one-hour
// window — the production loop pushes the past hour every hour. The
// acceptance scenario mirrors that cadence exactly. Each call must
// see its own hour's worth of usage rows.
//
// Sample layout: starting at HH:00 and stepping `now` BEFORE each
// SampleAndRoll, the 1440 samples land at minutes [HH+1, HH+24h+1].
func TestPushHour_Shadow24h(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()

	now := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC)
	t0 := now
	clock := func() time.Time { return now }

	// Hobby plan: free-tier hard-stop is gated behind 5 GB-h on the
	// Free plan, so Hobby is the canonical "real customer" account
	// for the acceptance scenario. Status defaults to AccountActive.
	acct := makeAccount(t, ctx, s, api.PlanHobby)
	app := newApp(t, ctx, s, acct.ID)
	makeLiveInstance(t, ctx, s, app.ID, acct.ID, 256)

	sampler := meter.NewSampler(s, clock)
	const minutesIn24h = 24 * 60
	for i := 0; i < minutesIn24h; i++ {
		now = now.Add(time.Minute)
		if _, err := sampler.SampleAndRoll(ctx); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
	}
	// After 1440 minute-steps `now` = T0 + 24h + 1min. The samples
	// landed at minutes [T0+1min, T0+24h+1min], spanning 25 distinct
	// hour-buckets from [T0, T0+1h) through [T0+24h, T0+25h). We need
	// 25 PushHour ticks to cover them — one tick per bucket. The
	// PushHour at "now = T0+1h" covers [T0, T0+1h) (the first bucket);
	// at "now = T0+25h" covers [T0+24h, T0+25h) (the last).
	const hoursToPush = 25

	rec := &recordingStripe{}
	pusher := meter.NewPusher(s, rec, discardLog(), clock, nil)

	// pin `now` to the top of the hour after the first sample. The
	// sample loop's first sample was at T0+1min, so its hour bucket is
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
	var totalGB float64
	for i, c := range calls {
		if c.AccountID != acct.ID {
			t.Errorf("call[%d].AccountID = %q, want %q", i, c.AccountID, acct.ID)
		}
		totalGB += c.GBHours
	}
	wantMB := (256 + api.PerVMOverheadMB) * 60 * minutesIn24h
	wantGB := meter.GBHours(int64(wantMB))
	delta := totalGB - wantGB
	if delta < 0 {
		delta = -delta
	}
	if delta/wantGB > 0.001 {
		t.Fatalf("push-side shadow delta %.6f GB (%.4f%%) exceeds 0.1%%: got=%.6f want=%.6f",
			delta, delta/wantGB*100, totalGB, wantGB)
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
