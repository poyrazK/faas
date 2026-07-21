package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSampler_RedeliveredMinuteIsIdempotent pins the interaction between the
// sampler and AppendUsage: two SampleAndRoll calls inside the same minute
// must produce one minute of billable usage, not two. This is the load-bearing
// guard against the pre-M7-hardening double-bill risk — a meterd restart
// redelivers the same minute, and the system must not charge for it twice.
//
// Lives next to TestAppendUsagePerInstanceMinute which pins the MemStore
// contract directly; together they cover both the storage seam and the
// sampler loop.
func TestSampler_RedeliveredMinuteIsIdempotent(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()
	// Clock parked at minute boundary 12:00:00.000 — first SampleAndRoll
	// writes into the 12:00 minute. We advance by 30s (still inside 12:00)
	// and SampleAndRoll again — must not append a second row.
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	acct := makeAccount(t, ctx, s, api.PlanHobby)
	app := newApp(t, ctx, s, acct.ID)
	_ = makeLiveInstance(t, ctx, s, app.ID, acct.ID, 256)

	sampler := meter.NewSampler(s, clock)
	if _, err := sampler.SampleAndRoll(ctx); err != nil {
		t.Fatalf("first sample: %v", err)
	}
	// Advance 30s, same minute.
	now = now.Add(30 * time.Second)
	if _, err := sampler.SampleAndRoll(ctx); err != nil {
		t.Fatalf("redelivered sample: %v", err)
	}

	usages, err := s.UsageByMonth(ctx, acct.ID, meter.AccountMonthKey(now))
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if len(usages) != 1 {
		t.Fatalf("usage rows = %d, want 1 (sampler must not double-bill on redelivery)", len(usages))
	}
	// 256 MB plan + 8 MB overhead = 264 admission MB; one minute at resident
	// RAM = 264 * 60 mb_seconds. The sampler stamps exactly one row even on
	// redelivery; we never see 2 × 264 * 60 = 31680.
	want := int64(api.BillableRAMMB(256)) * 60
	if usages[0].MBSeconds != want {
		t.Fatalf("MBSeconds = %d, want %d (one minute at %d MB admission)", usages[0].MBSeconds, want, api.BillableRAMMB(256))
	}
}

// TestSampler_AdvancesAcrossMinutes pins the inverse: two SampleAndRoll calls
// across a minute boundary must produce two rows whose sum matches the total
// billable duration. Confirms the idempotency fix didn't accidentally collapse
// different minutes.
func TestSampler_AdvancesAcrossMinutes(t *testing.T) {
	t.Parallel()
	s := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	acct := makeAccount(t, ctx, s, api.PlanHobby)
	app := newApp(t, ctx, s, acct.ID)
	_ = makeLiveInstance(t, ctx, s, app.ID, acct.ID, 256)

	sampler := meter.NewSampler(s, clock)
	if _, err := sampler.SampleAndRoll(ctx); err != nil {
		t.Fatalf("first sample: %v", err)
	}
	// Cross the minute boundary.
	now = now.Add(61 * time.Second)
	if _, err := sampler.SampleAndRoll(ctx); err != nil {
		t.Fatalf("next minute sample: %v", err)
	}

	// UsageByHour aggregates per (account, app) for the window — one row,
	// regardless of how many minutes contributed. Asserting on the aggregate
	// confirms the sampler wrote two distinct minute rows (collapse would
	// show a single minute's worth).
	rows, err := s.UsageByHour(ctx, acct.ID,
		time.Date(2026, 7, 17, 11, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("usage_by_hour: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (per app rollup)", len(rows))
	}
	// Two distinct minutes at 264 MB admission = 2 * 264 * 60 = 31680 mb_seconds.
	want := int64(api.BillableRAMMB(256)) * 60 * 2
	if rows[0].MBSeconds != want {
		t.Fatalf("MBSeconds = %d, want %d (two minutes at %d MB)", rows[0].MBSeconds, want, api.BillableRAMMB(256))
	}
}
