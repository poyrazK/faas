package meter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPushPendingRetriesFailedWindowsFromDurableUsage(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	t0 := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-a", t0.Add(5*time.Minute), 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-a", t0.Add(65*time.Minute), 200, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	retryErr := errors.New("temporary provider outage")
	provider := &recordingStripe{err: retryErr}
	now := t0.Add(2 * time.Hour)
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	if pushed, err := pusher.PushPending(ctx, 30*24*time.Hour); pushed != 0 || !errors.Is(err, retryErr) {
		t.Fatalf("first PushPending = (%d, %v), want (0, temporary error)", pushed, err)
	}

	provider.err = nil
	pushed, err := pusher.PushPending(ctx, 30*24*time.Hour)
	if err != nil || pushed != 2 {
		t.Fatalf("retry PushPending = (%d, %v), want (2, nil)", pushed, err)
	}
	if got := len(provider.Calls()); got != 4 {
		t.Fatalf("provider calls = %d, want 4 (two failed + two replayed windows)", got)
	}
}

func TestPushHourOverageProviderExcludesIncludedCalendarMonthUsage(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	now := time.Date(2026, 8, 31, 11, 0, 0, 0, time.UTC)
	start, _ := meter.HourWindow(now)

	// The prior hour consumes the entire Hobby allowance. The current
	// hour is therefore the first one Polar should receive, and only its
	// two billable GB-hours should be sent.
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-a", start.Add(-time.Minute), 50*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-a", start.Add(5*time.Minute), 2*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	provider := &recordingOverage{}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	pushed, err := pusher.PushHour(ctx)
	if err != nil || pushed != 1 {
		t.Fatalf("PushHour = (%d, %v), want (1, nil)", pushed, err)
	}
	calls := provider.Calls()
	if len(calls) != 1 || calls[0].MBSeconds != 2*api.SecondsPerGBHour {
		t.Fatalf("Polar billable calls = %+v, want one call with two GB-hours", calls)
	}
}

func TestPushPendingOverageCapDoesNotSendCrossingWindow(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	store.SetOverageCapCentsForTest(acct.ID, 1)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	allowance := int64(acct.Plan.PlanIncludedGBHours()) * api.SecondsPerGBHour
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-a", now.Add(-3*time.Hour), allowance, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	halfGBHour := api.SecondsPerGBHour / 2
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-a", now.Add(-2*time.Hour+5*time.Minute), halfGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-a", now.Add(-time.Hour+5*time.Minute), api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	provider := &recordingOverage{}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	pushed, err := pusher.PushPending(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("PushPending = (%d, %v), want successful partial replay", pushed, err)
	}
	if pushed != 1 {
		t.Fatalf("PushPending pushed = %d, want 1 window below cap", pushed)
	}
	calls := provider.Calls()
	if len(calls) != 1 || calls[0].MBSeconds != halfGBHour {
		t.Fatalf("provider calls = %+v, want only the half-GB-hour window", calls)
	}
}

func TestPushHourOverageCapZeroSkipsProvider(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	store.SetOverageCapCentsForTest(acct.ID, 0)
	now := time.Date(2026, 8, 31, 11, 0, 0, 0, time.UTC)
	start, _ := meter.HourWindow(now)
	allowance := int64(acct.Plan.PlanIncludedGBHours()) * api.SecondsPerGBHour
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-a", start.Add(-time.Minute), allowance, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-a", start.Add(5*time.Minute), api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	provider := &recordingOverage{}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	pushed, err := pusher.PushHour(ctx)
	if err != nil {
		t.Fatalf("PushHour = (%d, %v)", pushed, err)
	}
	if pushed != 0 || len(provider.Calls()) != 0 {
		t.Fatalf("PushHour = (%d, %+v), want no provider call at zero cap", pushed, provider.Calls())
	}
}

func TestPushPendingSkipsIncompleteBillingIdentity(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeAccount(t, ctx, store, api.PlanHobby)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-a", now.Add(-time.Hour+5*time.Minute), api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	provider := &recordingOverage{}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	pushed, err := pusher.PushPending(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("PushPending = (%d, %v)", pushed, err)
	}
	if pushed != 0 || len(provider.Calls()) != 0 {
		t.Fatalf("PushPending = (%d, %+v), want no provider call before webhook identity binding", pushed, provider.Calls())
	}
}
