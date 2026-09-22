// spec: §10 — the account overage cap covers compute and live egress together.
package meter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

type combinedOverageProvider struct{ recordingEgressProvider }

func (*combinedOverageProvider) UsageMode() billing.UsageMode { return billing.UsageModeOverage }

func TestComputePushReservesEarlierLiveEgressAgainstCap(t *testing.T) {
	for _, pending := range []bool{false, true} {
		ctx := context.Background()
		store := state.NewMemStore()
		acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
		store.SetOverageCapCentsForTest(acct.ID, 2)
		start := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
		// Hour 0 has no compute overage but two cents of egress. Hour 1's
		// one-cent compute window cannot be sent against the same two-cent cap.
		if err := store.AppendUsage(ctx, acct.ID, "app", "instance", start, 50*api.SecondsPerGBHour, 0, 0, 0, 2<<30, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendUsage(ctx, acct.ID, "app", "instance", start.Add(time.Hour), api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		provider := &combinedOverageProvider{recordingEgressProvider: recordingEgressProvider{mode: billing.MeterDeliveryLive}}
		pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return start.Add(2 * time.Hour) }, nil)
		if pending {
			if _, err := pusher.PushPending(ctx, 24*time.Hour); err != nil {
				t.Fatal(err)
			}
			if calls := provider.MeterCalls(); len(calls) != 1 || calls[0].Quantity != 1<<30 {
				t.Errorf("egress calls = %+v, want the earlier two-cent window", calls)
			}
		} else if _, err := pusher.PushHour(ctx); err != nil {
			t.Fatal(err)
		}
		if calls := provider.Calls(); len(calls) != 0 {
			t.Errorf("pending=%v: compute calls = %+v, would exceed the shared cap", pending, calls)
		}
	}
}

func TestEgressCapDoesNotRoundFractionalComputeDown(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	store.SetOverageCapCentsForTest(acct.ID, 1)
	start := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	// Half a cent of compute plus one cent of egress exceeds a one-cent cap.
	if err := store.AppendUsage(ctx, acct.ID, "app", "instance", start, 50*api.SecondsPerGBHour+api.SecondsPerGBHour/2, 0, 0, 0, 3<<29, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	provider := &combinedOverageProvider{recordingEgressProvider: recordingEgressProvider{mode: billing.MeterDeliveryLive}}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return start.Add(time.Hour) }, nil)
	if _, err := pusher.PushPending(ctx, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if calls := provider.MeterCalls(); len(calls) != 0 {
		t.Fatalf("egress calls = %+v, would push combined cost above the cap", calls)
	}
}

func TestCombinedFractionalUsageCanExactlyFillCap(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	store.SetOverageCapCentsForTest(acct.ID, 1)
	start := time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC)
	// Each meter costs half a cent. Do not round each one up independently.
	if err := store.AppendUsage(ctx, acct.ID, "app", "instance", start, 50*api.SecondsPerGBHour+api.SecondsPerGBHour/2, 0, 0, 0, 5<<28, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	provider := &combinedOverageProvider{recordingEgressProvider: recordingEgressProvider{mode: billing.MeterDeliveryLive}}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return start.Add(time.Hour) }, nil)
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != 2 {
		t.Fatalf("PushPending = (%d, %v), want both half-cent windows even at month end", pushed, err)
	}
	if calls := provider.Calls(); len(calls) != 1 || calls[0].MBSeconds != api.SecondsPerGBHour/2 {
		t.Fatalf("compute calls = %+v", calls)
	}
	if calls := provider.MeterCalls(); len(calls) != 1 || calls[0].Quantity != 1<<28 {
		t.Fatalf("egress calls = %+v", calls)
	}
}

type failedCapReadStore struct {
	state.Store
	err error
}

func (s *failedCapReadStore) UsageByHour(ctx context.Context, accountID string, start, end time.Time) ([]state.Usage, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.Store.UsageByHour(ctx, accountID, start, end)
}

func TestCombinedCapReadFailureLeavesUsagePending(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, mem, api.PlanHobby)
	mem.SetOverageCapCentsForTest(acct.ID, 2)
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if err := mem.AppendUsage(ctx, acct.ID, "app", "instance", start, 51*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	readErr := errors.New("cannot read egress liability")
	store := &failedCapReadStore{Store: mem, err: readErr}
	provider := &combinedOverageProvider{recordingEgressProvider: recordingEgressProvider{mode: billing.MeterDeliveryLive}}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return start.Add(time.Hour) }, nil)
	if pushed, err := pusher.PushPending(ctx, time.Hour); pushed != 0 || !errors.Is(err, readErr) {
		t.Fatalf("failed read = (%d, %v), want no push and the read error", pushed, err)
	}
	if len(provider.Calls()) != 0 {
		t.Fatal("billed before verifying the shared cap")
	}
	store.err = nil
	if pushed, err := pusher.PushPending(ctx, time.Hour); pushed != 1 || err != nil {
		t.Fatalf("retry = (%d, %v), want retained usage to deliver", pushed, err)
	}
}
