// adr: 1320 — the overage cap covers month-close object storage.
package billing

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// egressPolicySink is a line-item sink that also prices egress, as the Polar
// provider does.
type egressPolicySink struct {
	*objectStorageLineItemRecorder
	egress MeterUsagePolicy
}

func (s egressPolicySink) MeterUsagePolicy(api.Plan, state.BillingMeter) (MeterUsagePolicy, bool) {
	return s.egress, s.egress.Mode != ""
}

// capFreeBillingStore exposes only the billing seam, without cap reads.
type capFreeBillingStore struct {
	state.ObjectStorageBillingStore
}

func TestFinalizeObjectStoragePeriodsHoldsChargeUnderOverageCap(t *testing.T) {
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	gib := int64(1 << 30)
	computeOver := func(gbHours int64) state.Usage {
		return state.Usage{Month: period.Add(time.Hour), MBSeconds: api.IncludedMBSeconds(api.PlanHobby) + gbHours*api.SecondsPerGBHour}
	}
	egressRow := state.Usage{Month: period.Add(2 * time.Hour), NetTxBytes: 2 * gib}
	liveEgress := MeterUsagePolicy{Mode: MeterDeliveryLive, EffectiveFrom: period, IncludedQuantity: gib, UnitQuantity: gib, MillicentsPerUnit: 2000}
	shadowEgress := liveEgress
	shadowEgress.Mode = MeterDeliveryShadow
	lateEgress := liveEgress
	lateEgress.EffectiveFrom = period.AddDate(0, 0, 10)
	// Usage is Hobby's included storage (ADR-1430) plus 9 GiB-months, which
	// the record prices at 1000 millicents each: 9000 millicents.
	hobbyIncluded := api.MustLimitsFor(api.PlanHobby).ObjectStorage.Allowance().StoredByteHours
	for _, tc := range []struct {
		name     string
		capped   bool
		capCents int64
		usage    []state.Usage
		egress   MeterUsagePolicy
		want     int64
	}{
		{name: "uncapped bills in full", usage: []state.Usage{computeOver(40)}, want: 9000},
		{name: "cap above every meter bills in full", capped: true, capCents: 100, usage: []state.Usage{computeOver(4)}, want: 9000},
		{name: "billable compute keeps its place", capped: true, capCents: 10, usage: []state.Usage{computeOver(4)}, want: 6000},
		{name: "live egress keeps its place", capped: true, capCents: 10, usage: []state.Usage{computeOver(4), egressRow}, egress: liveEgress, want: 4000},
		{name: "shadow egress reserves nothing", capped: true, capCents: 10, usage: []state.Usage{computeOver(4), egressRow}, egress: shadowEgress, want: 6000},
		{name: "egress before its activation reserves nothing", capped: true, capCents: 10, usage: []state.Usage{computeOver(4), egressRow}, egress: lateEgress, want: 6000},
		{name: "compute over the cap leaves nothing", capped: true, capCents: 10, usage: []state.Usage{computeOver(20)}, want: 0},
		{name: "zero cap bills nothing", capped: true, want: 0},
		{name: "next month's usage reserves nothing", capped: true, capCents: 10, usage: []state.Usage{{Month: period.AddDate(0, 1, 0), MBSeconds: 1 << 50}}, want: 9000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := objectStorageBillingSnapshot("acct", "backend", period)
			snapshot.Reports[0].StoredByteHours = hobbyIncluded + 9*gib*api.ObjectStorageBillingHoursPerMonth
			store := &objectStorageBillingStore{
				snapshots: map[string]state.ObjectUsageSnapshot{"acct": snapshot},
				usage:     map[string][]state.Usage{"acct": tc.usage},
			}
			if tc.capped {
				store.caps = map[string]int64{"acct": tc.capCents}
			}
			recorder := &objectStorageLineItemRecorder{}
			accounts := objectStorageBillingAccounts{accounts: []state.Account{{ID: "acct", Plan: api.PlanHobby}}}
			pricing := api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 1000}
			records, err := FinalizeObjectStoragePeriods(context.Background(), accounts, store, pricing, period, period.AddDate(0, 1, 5), egressPolicySink{recorder, tc.egress})
			if err != nil || len(records) != 1 || records[0].TotalMillicents != 9000 {
				t.Fatalf("records = %+v, err = %v; want one record keeping the full 9000 millicents", records, err)
			}
			if len(recorder.charges) != 1 || recorder.charges[0] != tc.want {
				t.Fatalf("published charges = %v, want [%d]", recorder.charges, tc.want)
			}
			if d := store.deliveries["test"+records[0].ID]; d.Mode != state.ObjectStorageDeliveryLive || d.QuantityMillicents != tc.want {
				t.Fatalf("delivery = %+v, want live %d", d, tc.want)
			}
		})
	}
}

func TestFinalizeObjectStoragePeriodsFailsClosedWithoutCapReads(t *testing.T) {
	period := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	snapshot := objectStorageBillingSnapshot("acct", "backend", period)
	snapshot.Reports[0].StoredByteHours = api.MustLimitsFor(api.PlanHobby).ObjectStorage.Allowance().StoredByteHours + int64(1<<30)*api.ObjectStorageBillingHoursPerMonth
	inner := &objectStorageBillingStore{snapshots: map[string]state.ObjectUsageSnapshot{"acct": snapshot}}
	recorder := &objectStorageLineItemRecorder{}
	accounts := objectStorageBillingAccounts{accounts: []state.Account{{ID: "acct", Plan: api.PlanHobby}}}
	pricing := api.ObjectStoragePricing{Currency: "USD", StorageMillicentsPerGiBMonth: 1000}
	if _, err := FinalizeObjectStoragePeriods(context.Background(), accounts, capFreeBillingStore{inner}, pricing, period, period.AddDate(0, 1, 5), recorder); err == nil {
		t.Fatal("month close without cap reads returned nil error")
	}
	if len(recorder.charges) != 0 || len(inner.deliveries) != 0 {
		t.Fatalf("published %v with receipts %+v, want neither", recorder.charges, inner.deliveries)
	}
}

func TestCapRemainingMillicentsReservesEveryMeter(t *testing.T) {
	egress := &MeterUsagePolicy{UnitQuantity: 3, MillicentsPerUnit: 1}
	remaining, ok := CapRemainingMillicents(1, api.SecondsPerGBHour/2, 1, egress)
	// 1000 millicents less half a GB-hour (500) and a third of a millicent.
	if !ok || FloorCapQuantity(remaining, 1, 1) != 499 {
		t.Fatalf("remaining = %v, %v; want 499 whole millicents", remaining, ok)
	}
	for _, in := range [][3]int64{{-1, 0, 0}, {0, -1, 0}, {0, 0, -1}} {
		if _, ok := CapRemainingMillicents(in[0], in[1], in[2], nil); ok {
			t.Fatalf("CapRemainingMillicents%v accepted a negative input", in)
		}
	}
	if _, ok := CapRemainingMillicents(1, 0, 1, &MeterUsagePolicy{}); ok {
		t.Fatal("an unpriced egress reservation did not fail closed")
	}
}
