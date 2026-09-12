// spec: §10
package meter_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

type recordedMeterCall struct {
	AccountID string
	Hour      time.Time
	Meter     state.BillingMeter
	Quantity  int64
}

type recordingEgressProvider struct {
	recordingStripe
	mode          billing.MeterDeliveryMode
	effectiveFrom time.Time
	meterMu       sync.Mutex
	meterCalls    []recordedMeterCall
}

func (r *recordingEgressProvider) Capabilities() billing.CapabilitySet {
	caps := r.recordingStripe.Capabilities()
	if r.mode == billing.MeterDeliveryLive {
		caps |= billing.CapabilitySet(billing.CapEgressUsage)
	}
	return caps
}

func (r *recordingEgressProvider) MeterUsagePolicy(plan api.Plan, meter state.BillingMeter) (billing.MeterUsagePolicy, bool) {
	if meter != state.BillingMeterEgress || plan == api.PlanFree {
		return billing.MeterUsagePolicy{}, false
	}
	effectiveFrom := r.effectiveFrom
	if effectiveFrom.IsZero() {
		effectiveFrom = time.Unix(0, 0).UTC()
	}
	return billing.MeterUsagePolicy{
		Mode:              r.mode,
		EffectiveFrom:     effectiveFrom,
		IncludedQuantity:  1 << 30,
		UnitQuantity:      1 << 30,
		MillicentsPerUnit: 2_000,
	}, true
}

func (r *recordingEgressProvider) PushMeterUsageRecord(_ context.Context, acct state.Account, hour time.Time, meter state.BillingMeter, quantity int64) error {
	r.meterMu.Lock()
	defer r.meterMu.Unlock()
	r.meterCalls = append(r.meterCalls, recordedMeterCall{AccountID: acct.ID, Hour: hour, Meter: meter, Quantity: quantity})
	return nil
}

func (r *recordingEgressProvider) MeterCalls() []recordedMeterCall {
	r.meterMu.Lock()
	defer r.meterMu.Unlock()
	return append([]recordedMeterCall(nil), r.meterCalls...)
}

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

func TestPushPendingLeavesPeerHeldWindowUndelivered(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-a", now.Add(-time.Hour), 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	provider := &recordingStripe{err: billing.ErrUsageDeliveryInProgress}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); pushed != 0 || err != nil {
		t.Fatalf("held PushPending = (%d, %v), want quiet pending result", pushed, err)
	}
	provider.err = nil
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); pushed != 1 || err != nil {
		t.Fatalf("retry PushPending = (%d, %v), want window to remain deliverable", pushed, err)
	}
}

func TestPushPendingHonorsLookback(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "old", now.Add(-25*time.Hour), 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "recent", now.Add(-time.Hour), 200, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	provider := &recordingStripe{}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	pushed, err := pusher.PushPending(ctx, 24*time.Hour)
	if err != nil || pushed != 1 {
		t.Fatalf("PushPending = (%d, %v), want one in-range window", pushed, err)
	}
	calls := provider.Calls()
	if len(calls) != 1 || calls[0].MBSeconds != 200 {
		t.Fatalf("provider calls = %+v, want only recent usage", calls)
	}
}

func TestPushPendingDoesNotRebillUsageBeforeProviderIdentity(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeAccount(t, ctx, store, api.PlanHobby)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	switchAt := now.Add(-90 * time.Minute)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "before-switch", now.Add(-105*time.Minute), 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "after-switch", now.Add(-75*time.Minute), 200, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	// recordingStripe is a provider test double, so the pusher retains the
	// legacy account cache while PendingBillingUsageWindows uses the explicit
	// Stripe identity below.
	if err := store.UpdateAccountProviderCustomerID(ctx, acct.ID, "ctm_legacy_cache"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, acct.ID, "si_switch"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{
		AccountID: acct.ID, Provider: "stripe", CustomerID: "cus_switch",
		SubscriptionID: "si_switch", BillingFrom: switchAt,
	}); err != nil {
		t.Fatal(err)
	}

	provider := &recordingStripe{}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	pushed, err := pusher.PushPending(ctx, 24*time.Hour)
	if err != nil || pushed != 1 {
		t.Fatalf("PushPending = (%d, %v), want one post-switch window", pushed, err)
	}
	calls := provider.Calls()
	if len(calls) != 1 || calls[0].MBSeconds != 200 {
		t.Fatalf("provider calls = %+v, want only post-switch usage", calls)
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

func TestPushPendingEgressLiveSubtractsMonthlyAllowance(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	const gib = int64(1 << 30)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "included", now.Add(-2*time.Hour+time.Minute), 0, 0, 0, 0, gib, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "overage", now.Add(-time.Hour+time.Minute), 0, 0, 0, 0, 2*gib, 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	provider := &recordingEgressProvider{mode: billing.MeterDeliveryLive}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	pushed, err := pusher.PushPending(ctx, 24*time.Hour)
	if err != nil || pushed != 1 {
		t.Fatalf("PushPending = (%d, %v), want one live egress event", pushed, err)
	}
	calls := provider.MeterCalls()
	if len(calls) != 1 || calls[0].Meter != state.BillingMeterEgress || calls[0].Quantity != 2*gib {
		t.Fatalf("egress calls = %+v, want one two-GiB overage", calls)
	}
}

func TestPushPendingEgressShadowNeverCallsProviderOrReplaysOnLive(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	const gib = int64(1 << 30)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "shadow", now.Add(-time.Hour+time.Minute), 0, 0, 0, 0, 3*gib, 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	provider := &recordingEgressProvider{mode: billing.MeterDeliveryShadow}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != 0 {
		t.Fatalf("shadow PushPending = (%d, %v), want no external push", pushed, err)
	}
	if calls := provider.MeterCalls(); len(calls) != 0 {
		t.Fatalf("shadow provider calls = %+v, want none", calls)
	}

	provider.mode = billing.MeterDeliveryLive
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != 0 {
		t.Fatalf("post-shadow live PushPending = (%d, %v), want no retroactive push", pushed, err)
	}
	if calls := provider.MeterCalls(); len(calls) != 0 {
		t.Fatalf("post-shadow provider calls = %+v, want none", calls)
	}
}

func TestPushPendingEgressLiveHonorsCombinedOverageCap(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	store.SetOverageCapCentsForTest(acct.ID, 1)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	const gib = int64(1 << 30)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "egress-cap", now.Add(-time.Hour+time.Minute), 0, 0, 0, 0, 2*gib, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	provider := &recordingEgressProvider{mode: billing.MeterDeliveryLive}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != 0 {
		t.Fatalf("PushPending = (%d, %v), want cap-safe no-op", pushed, err)
	}
	if calls := provider.MeterCalls(); len(calls) != 0 {
		t.Fatalf("capped egress calls = %+v, want none", calls)
	}
}

func TestPushPendingEgressLiveNeverBackfillsBeforeActivation(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	activation := now.Add(-time.Hour)
	const gib = int64(1 << 30)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "pre-activation", activation.Add(-time.Hour+time.Minute), 0, 0, 0, 0, 50*gib, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "post-activation", activation.Add(time.Minute), 0, 0, 0, 0, 2*gib, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	provider := &recordingEgressProvider{mode: billing.MeterDeliveryLive, effectiveFrom: activation}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != 1 {
		t.Fatalf("PushPending = (%d, %v), want only post-activation event", pushed, err)
	}
	calls := provider.MeterCalls()
	if len(calls) != 1 || calls[0].Hour != activation || calls[0].Quantity != gib {
		t.Fatalf("post-activation calls = %+v, want one GiB after fresh allowance", calls)
	}
}
