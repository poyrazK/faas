// adr: 20261027093220 — object storage is billed in daily steps inside its
// month, and month close finishes it (Hunt 19 H19-1).
package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var objectStorageTestPricing = api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 1000}

func objectStorageProgressFixture(plan api.Plan, gibMonths int64) (*objectStorageBillingStore, *objectStorageLineItemRecorder, *objectStorageBillingAccounts, time.Time) {
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	store := &objectStorageBillingStore{snapshots: map[string]state.ObjectUsageSnapshot{"acct": billableObjectStorageSnapshot("acct", period, gibMonths)}}
	sink := &objectStorageLineItemRecorder{policy: ObjectStorageLineItemPolicy{Provider: "polar", Mode: MeterDeliveryLive, EffectiveFrom: period}}
	accounts := &objectStorageBillingAccounts{accounts: []state.Account{{ID: "acct", Plan: plan}}}
	return store, sink, accounts, period
}

func deliverObjectStorageProgressAt(t *testing.T, store *objectStorageBillingStore, accounts *objectStorageBillingAccounts, sink *objectStorageLineItemRecorder, now time.Time) (int, error) {
	t.Helper()
	store.now = now
	return DeliverObjectStorageProgress(context.Background(), *accounts, store, objectStorageTestPricing, now, sink)
}

func TestObjectStorageIsBilledDailyAndMonthCloseFinishesTheMonth(t *testing.T) {
	store, sink, accounts, period := objectStorageProgressFixture(api.PlanHobby, 3)
	day5 := period.AddDate(0, 0, 4).Add(12 * time.Hour)
	if n, err := deliverObjectStorageProgressAt(t, store, accounts, sink, day5); err != nil || n != 1 {
		t.Fatalf("first step = %d, %v", n, err)
	}
	// Within the interval the account is not due again.
	if n, err := deliverObjectStorageProgressAt(t, store, accounts, sink, day5.Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("step within the interval = %d, %v", n, err)
	}
	store.snapshots["acct"] = billableObjectStorageSnapshot("acct", period, 5)
	if n, err := deliverObjectStorageProgressAt(t, store, accounts, sink, day5.Add(api.ObjectStorageProgressInterval+time.Hour)); err != nil || n != 1 {
		t.Fatalf("next day's step = %d, %v", n, err)
	}
	if len(sink.charges) != 2 || sink.charges[0] != 3000 || sink.charges[1] != 2000 || sink.steps[1].TargetMillicents != 5000 || sink.steps[1].Record != nil {
		t.Fatalf("daily charges = %v steps = %+v, want 3000 then 2000 to a cumulative 5000", sink.charges, sink.steps)
	}

	// Month close bills the rest and records the month's cumulative charge.
	store.snapshots["acct"] = billableObjectStorageSnapshot("acct", period, 9)
	closeAt := period.AddDate(0, 1, 1)
	store.now = closeAt
	records, err := FinalizeObjectStoragePeriods(context.Background(), *accounts, store, objectStorageTestPricing, period, closeAt, sink)
	if err != nil || len(records) != 1 || records[0].TotalMillicents != 9000 {
		t.Fatalf("month close records = %+v, err = %v", records, err)
	}
	if len(sink.charges) != 3 || sink.charges[2] != 4000 || len(sink.records) != 1 || sink.records[0].ID != records[0].ID {
		t.Fatalf("month close charge = %v records = %+v, want 4000 with the record", sink.charges, sink.records)
	}
	if d := store.deliveries["polar"+records[0].ID]; d.Mode != state.ObjectStorageDeliveryLive || d.QuantityMillicents != 9000 {
		t.Fatalf("receipt = %+v, want live 9000", d)
	}
}

func TestObjectStorageMonthCloseKeepsWhatAChurnedAccountWasBilled(t *testing.T) {
	store, sink, accounts, period := objectStorageProgressFixture(api.PlanHobby, 3)
	if _, err := deliverObjectStorageProgressAt(t, store, accounts, sink, period.AddDate(0, 0, 9)); err != nil {
		t.Fatal(err)
	}
	// The subscription ended: the account is Free, and Free accounts are
	// never stepped.
	accounts.accounts[0].Plan = api.PlanFree
	store.snapshots["acct"] = billableObjectStorageSnapshot("acct", period, 9)
	if n, err := deliverObjectStorageProgressAt(t, store, accounts, sink, period.AddDate(0, 0, 20)); err != nil || n != 0 {
		t.Fatalf("step after churn = %d, %v", n, err)
	}
	closeAt := period.AddDate(0, 1, 1)
	store.now = closeAt
	records, err := FinalizeObjectStoragePeriods(context.Background(), *accounts, store, objectStorageTestPricing, period, closeAt, sink)
	if err != nil || len(records) != 1 {
		t.Fatalf("month close = %+v, %v", records, err)
	}
	if len(sink.charges) != 1 || sink.charges[0] != 3000 {
		t.Fatalf("charges = %v, want only the 3000 billed while paid", sink.charges)
	}
	if d := store.deliveries["polar"+records[0].ID]; d.Mode != state.ObjectStorageDeliveryLive || d.QuantityMillicents != 3000 {
		t.Fatalf("receipt = %+v, want live 3000, not a zero plan_ineligible", d)
	}
}

func TestObjectStorageFailedStepIsRetriedUnderTheSameTarget(t *testing.T) {
	store, sink, accounts, period := objectStorageProgressFixture(api.PlanHobby, 3)
	sink.err = errors.New("provider unavailable")
	at := period.AddDate(0, 0, 4)
	if _, err := deliverObjectStorageProgressAt(t, store, accounts, sink, at); err == nil {
		t.Fatal("failed publish returned nil error")
	}
	// A reservation younger than the retry interval is left alone.
	if n, err := deliverObjectStorageProgressAt(t, store, accounts, sink, at.Add(time.Minute)); err != nil || n != 0 {
		t.Fatalf("step within the retry interval = %d, %v", n, err)
	}
	sink.err = nil
	store.snapshots["acct"] = billableObjectStorageSnapshot("acct", period, 5)
	if n, err := deliverObjectStorageProgressAt(t, store, accounts, sink, at.Add(api.ObjectStorageProgressRetry)); err != nil || n != 1 {
		t.Fatalf("retry = %d, %v", n, err)
	}
	// The fixed step is published again under its target before the new
	// usage is billed.
	if len(sink.steps) != 3 || sink.steps[0].TargetMillicents != 3000 || sink.steps[1].TargetMillicents != 3000 || sink.steps[2].TargetMillicents != 5000 {
		t.Fatalf("publish attempts = %+v, want 3000, 3000 again, then 5000", sink.steps)
	}
	p, err := store.GetObjectStorageBillingProgress(context.Background(), "polar", "acct", period, state.ObjectStorageDeliveryLive)
	if err != nil || p.DeliveredMillicents != 5000 || p.PendingMillicents != 0 || p.PendingFixed {
		t.Fatalf("progress = %+v, err = %v", p, err)
	}
}

func TestObjectStorageStepLosesARaceWithoutPublishing(t *testing.T) {
	store, sink, accounts, period := objectStorageProgressFixture(api.PlanHobby, 3)
	store.caps = map[string]int64{"acct": 100}
	writes := 0
	store.beforeSave = func(p state.ObjectStorageBillingProgress) {
		writes++
		if writes == 2 {
			// Another apid replaces the reservation before this one fixes it.
			other := store.progress[objectStorageProgressKey(p.Provider, p.AccountID, p.PeriodStart, p.Mode)]
			other.Version++
			store.progress[objectStorageProgressKey(p.Provider, p.AccountID, p.PeriodStart, p.Mode)] = other
		}
	}
	if _, err := deliverObjectStorageProgressAt(t, store, accounts, sink, period.AddDate(0, 0, 4)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("lost race error = %v, want ErrConflict", err)
	}
	if len(sink.steps) != 0 {
		t.Fatalf("a step that lost the race published %+v", sink.steps)
	}
}

func TestObjectStorageStepReservesBeforeReadingTheCapAndStaysUnderIt(t *testing.T) {
	store, sink, accounts, period := objectStorageProgressFixture(api.PlanHobby, 9)
	// A 10-cent cap with 4 GB-hours of compute overage leaves 6000 millicents.
	store.caps = map[string]int64{"acct": 10}
	store.usage = map[string][]state.Usage{"acct": {{Month: period.Add(time.Hour), MBSeconds: api.IncludedMBSeconds(api.PlanHobby) + 4*api.SecondsPerGBHour}}}
	if _, err := deliverObjectStorageProgressAt(t, store, accounts, sink, period.AddDate(0, 0, 4)); err != nil {
		t.Fatal(err)
	}
	if len(store.reservedAtCapRead) == 0 || store.reservedAtCapRead[0] != 9000 {
		t.Fatalf("reservation seen by the cap read = %v, want the step's 9000 upper bound", store.reservedAtCapRead)
	}
	if len(sink.charges) != 1 || sink.charges[0] != 6000 {
		t.Fatalf("charges = %v, want 6000 under the cap", sink.charges)
	}
	if reserved := store.liveReserved(context.Background(), "acct", period); reserved != 6000 {
		t.Fatalf("live reserved after the step = %d, want 6000", reserved)
	}
}

func TestObjectStorageAdoptedMonthBillsOnlyBeyondTheCredit(t *testing.T) {
	store, sink, accounts, period := objectStorageProgressFixture(api.PlanHobby, 3)
	store.progress = map[string]state.ObjectStorageBillingProgress{
		objectStorageProgressKey("polar", "acct", period, state.ObjectStorageDeliveryLive): {
			Provider: "polar", AccountID: "acct", PeriodStart: period, Mode: state.ObjectStorageDeliveryLive, CreditedMillicents: 2000, Version: 1,
		},
	}
	if _, err := deliverObjectStorageProgressAt(t, store, accounts, sink, period.AddDate(0, 0, 4)); err != nil {
		t.Fatal(err)
	}
	if len(sink.charges) != 1 || sink.charges[0] != 1000 {
		t.Fatalf("charges = %v, want the 1000 members were not charged", sink.charges)
	}
}

func TestObjectStorageShadowStepsRecordWithoutPublishing(t *testing.T) {
	store, sink, accounts, period := objectStorageProgressFixture(api.PlanHobby, 3)
	sink.policy.Mode = MeterDeliveryShadow
	if n, err := deliverObjectStorageProgressAt(t, store, accounts, sink, period.AddDate(0, 0, 4)); err != nil || n != 1 {
		t.Fatalf("shadow step = %d, %v", n, err)
	}
	if len(sink.steps) != 0 {
		t.Fatalf("shadow published %+v", sink.steps)
	}
	p, err := store.GetObjectStorageBillingProgress(context.Background(), "polar", "acct", period, state.ObjectStorageDeliveryShadow)
	if err != nil || p.DeliveredMillicents != 3000 {
		t.Fatalf("shadow progress = %+v, err = %v", p, err)
	}
	if reserved := store.liveReserved(context.Background(), "acct", period); reserved != 0 {
		t.Fatalf("shadow reserved %d of the live cap", reserved)
	}
}

func TestObjectStorageProgressSkipsMonthsBeforeActivation(t *testing.T) {
	store, sink, accounts, period := objectStorageProgressFixture(api.PlanHobby, 3)
	sink.policy.EffectiveFrom = period.AddDate(0, 1, 0)
	if n, err := deliverObjectStorageProgressAt(t, store, accounts, sink, period.AddDate(0, 0, 4)); err != nil || n != 0 || len(store.progress) != 0 {
		t.Fatalf("pre-activation step = %d, %v, progress %+v", n, err, store.progress)
	}
}
