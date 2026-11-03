// adr: 20261027093220 — apid bills object storage in daily steps under the
// same overage cap, so meterd reserves its live charge for the month.
package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func saveObjectStorageCharge(t *testing.T, store *state.MemStore, account, mode string, period time.Time, delivered, pending int64) {
	t.Helper()
	if _, err := store.SaveObjectStorageBillingProgress(context.Background(), state.ObjectStorageBillingProgress{
		Provider: "polar", AccountID: account, PeriodStart: period, Mode: mode, DeliveredMillicents: delivered, PendingMillicents: pending,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestComputePushReservesLiveObjectStorageAgainstCap(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      string
		wantMBSec int64
	}{
		// A 3-cent cap holding 1,500 millicents of storage leaves 1.5 GB-hours.
		{"live storage keeps its place", state.ObjectStorageDeliveryLive, 3 * api.SecondsPerGBHour / 2},
		{"shadow storage reserves nothing", state.ObjectStorageDeliveryShadow, 2 * api.SecondsPerGBHour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
			store.SetOverageCapCentsForTest(acct.ID, 3)
			start := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
			if err := store.AppendUsage(ctx, acct.ID, "app", "instance", start, api.IncludedMBSeconds(api.PlanHobby)+2*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
				t.Fatal(err)
			}
			saveObjectStorageCharge(t, store, acct.ID, tc.mode, state.ObjectStoragePeriod(start), 1000, 500)
			provider := &recordingOverage{}
			pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return start.Add(time.Hour) }, nil)
			completeUsageThrough(t, ctx, store, start.Add(time.Hour))
			if _, err := pusher.PushPending(ctx, 24*time.Hour); err != nil {
				t.Fatal(err)
			}
			if calls := provider.Calls(); len(calls) != 1 || calls[0].MBSeconds != tc.wantMBSec {
				t.Fatalf("compute calls = %+v, want %d MB-seconds", calls, tc.wantMBSec)
			}
		})
	}
}

func TestEgressPushReservesLiveObjectStorageAgainstCap(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	store.SetOverageCapCentsForTest(acct.ID, 1)
	start := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	// One cent of egress overage (half a GiB past the included GiB) under a
	// one-cent cap that storage already holds half of.
	if err := store.AppendUsage(ctx, acct.ID, "app", "instance", start, 0, 0, 0, 0, 3<<29, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	saveObjectStorageCharge(t, store, acct.ID, state.ObjectStorageDeliveryLive, state.ObjectStoragePeriod(start), 500, 0)
	provider := &combinedOverageProvider{recordingEgressProvider: recordingEgressProvider{mode: billing.MeterDeliveryLive}}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return start.Add(time.Hour) }, nil)
	completeUsageThrough(t, ctx, store, start.Add(time.Hour))
	if _, err := pusher.PushPending(ctx, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if calls := provider.MeterCalls(); len(calls) != 1 || calls[0].Quantity != 1<<28 {
		t.Fatalf("egress calls = %+v, want the quarter GiB that fills the rest of the cap", calls)
	}
}

func TestRunQuotaOnce_LiveObjectStorageCountsTowardSpendCapHold(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	acct := makeAccount(t, ctx, store, api.PlanHobby)
	store.SetClockForTest(func() time.Time { return now })
	store.SetOverageCapCentsForTest(acct.ID, 2)
	// 1,500 millicents is a cent and a half; a part cent counts as reached.
	saveObjectStorageCharge(t, store, acct.ID, state.ObjectStorageDeliveryLive, state.ObjectStoragePeriod(now), 1500, 0)
	cfg := &meter.Config{}
	cfg.Defaults()
	loop := meter.NewLoop(store, nil, &fakeParker{}, &recordingEgressProvider{mode: billing.MeterDeliveryShadow}, &fakeNotifier{}, nil, nil, nil, nil,
		func() time.Time { return now }, discardLog(), cfg, wire.NewOpsMetrics("meter_test_object_storage_cap"))
	loop.RunQuotaOnce(ctx)
	hold, found, err := store.GetSpendCapHold(ctx, acct.ID)
	if err != nil || !found || hold.ObservedCents != 2 || !hold.Holds(2, now) {
		t.Fatalf("spend cap hold = %+v found=%v err=%v; want the object-storage charge to reach the 2-cent cap", hold, found, err)
	}
	store.SetOverageCapCentsForTest(acct.ID, 3)
	loop.RunQuotaOnce(ctx)
	if _, found, err := store.GetSpendCapHold(ctx, acct.ID); err != nil || found {
		t.Fatalf("hold under a 3-cent cap: found=%v err=%v", found, err)
	}
	// A step's pending reservation is an upper bound, not a charge: it never
	// parks the account.
	p, err := store.GetObjectStorageBillingProgress(ctx, "polar", acct.ID, state.ObjectStoragePeriod(now), state.ObjectStorageDeliveryLive)
	if err != nil {
		t.Fatal(err)
	}
	p.PendingMillicents = 1_000_000
	if _, err := store.SaveObjectStorageBillingProgress(ctx, p); err != nil {
		t.Fatal(err)
	}
	loop.RunQuotaOnce(ctx)
	if _, found, err := store.GetSpendCapHold(ctx, acct.ID); err != nil || found {
		t.Fatalf("hold from a pending reservation: found=%v err=%v", found, err)
	}
}
