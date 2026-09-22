// spec: §10 — calendar-month allowances and caps include delivered usage.
package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPushPendingComputeIncludesDeliveredGaps(t *testing.T) {
	for _, tc := range []struct {
		name       string
		usage      [3]int64
		delivered  int64
		capCents   int64
		wantHour   int
		wantGBHour int64
	}{
		{"allowance", [3]int64{20, 30, 10}, 0, -1, 2, 10},
		{"spending cap", [3]int64{51, 9, 1}, 9, 10, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
			start := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
			if tc.capCents >= 0 {
				store.SetOverageCapCentsForTest(acct.ID, tc.capCents)
			}
			for hour, gbHours := range tc.usage {
				if err := store.AppendUsage(ctx, acct.ID, "app", "instance", start.Add(time.Duration(hour)*time.Hour), gbHours*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
					t.Fatal(err)
				}
			}
			// A prior sweep delivered the middle hour but the earlier hour
			// still needs retry. Only hours 0 and 2 appear in the pending list.
			if err := store.RecordBillingUsageDelivery(ctx, "stripe", acct.ID, start.Add(time.Hour), tc.delivered*api.SecondsPerGBHour); err != nil {
				t.Fatal(err)
			}
			provider := &recordingOverage{}
			pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return start.Add(3 * time.Hour) }, nil)
			if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != 1 {
				t.Fatalf("PushPending = (%d, %v), want one billable window", pushed, err)
			}
			calls := provider.Calls()
			if len(calls) != 1 || !calls[0].Hour.Equal(start.Add(time.Duration(tc.wantHour)*time.Hour)) || calls[0].MBSeconds != tc.wantGBHour*api.SecondsPerGBHour {
				t.Fatalf("calls = %+v, want hour %d with %d GB-hours", calls, tc.wantHour, tc.wantGBHour)
			}
		})
	}
}

func TestPushPendingEgressIncludesDeliveredGaps(t *testing.T) {
	const gib = int64(1 << 30)
	for _, tc := range []struct {
		name      string
		usage     [3]int64
		delivered int64
		capCents  int64
		wantHour  int
	}{
		{"allowance", [3]int64{gib / 4, 3 * gib / 4, gib / 2}, 0, -1, 2},
		{"spending cap", [3]int64{3 * gib / 2, gib, gib / 2}, gib, 3, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
			start := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
			if tc.capCents >= 0 {
				store.SetOverageCapCentsForTest(acct.ID, tc.capCents)
			}
			for hour, bytes := range tc.usage {
				if err := store.AppendUsage(ctx, acct.ID, "app", "instance", start.Add(time.Duration(hour)*time.Hour), 0, 0, 0, 0, bytes, 0, 0, 0); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.RecordBillingMeterUsageDelivery(ctx, "stripe", acct.ID, state.BillingMeterEgress, start.Add(time.Hour), tc.delivered); err != nil {
				t.Fatal(err)
			}
			provider := &recordingEgressProvider{mode: billing.MeterDeliveryLive}
			pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return start.Add(3 * time.Hour) }, nil)
			if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != 1 {
				t.Fatalf("PushPending = (%d, %v), want one billable window", pushed, err)
			}
			calls := provider.MeterCalls()
			if len(calls) != 1 || !calls[0].Hour.Equal(start.Add(time.Duration(tc.wantHour)*time.Hour)) || calls[0].Quantity != gib/2 {
				t.Fatalf("calls = %+v, want hour %d with half a GiB", calls, tc.wantHour)
			}
		})
	}
}

func TestPushPendingNeverReceiptsPartialLookbackHour(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	start := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	for i, minute := range []int{15, 45, 75} {
		if err := store.AppendUsage(ctx, acct.ID, "app", "instance", start.Add(time.Duration(minute)*time.Minute), int64(100*(i+1)), 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	provider := &recordingStripe{}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return start.Add(2 * time.Hour) }, nil)
	if pushed, err := pusher.PushPending(ctx, 90*time.Minute); err != nil || pushed != 1 {
		t.Fatalf("90m lookback = (%d, %v), want only the complete last hour", pushed, err)
	}
	if pushed, err := pusher.PushPending(ctx, 2*time.Hour); err != nil || pushed != 1 {
		t.Fatalf("expanded lookback = (%d, %v), want the whole earlier hour still pending", pushed, err)
	}
	calls := provider.Calls()
	if len(calls) != 2 || calls[0].MBSeconds != 300 || calls[1].MBSeconds != 300 || !calls[1].Hour.Equal(start) {
		t.Fatalf("calls = %+v, want two complete 300-MB-second hours", calls)
	}
}
