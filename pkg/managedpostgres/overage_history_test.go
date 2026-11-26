// adr: 20261123062111 — restore history and snapshot storage past the plan's
// included history are billed on the overage step at the provider's price
// plus 5%.
package managedpostgres

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const testHistoryMonth = api.ManagedPostgresHistoryMonthSeconds

func TestHistoryOveragePrices(t *testing.T) {
	// Neon's price plus 5%: $0.21 and $0.0945 per GB-month.
	if api.ManagedPostgresRestoreHistoryOverageMillicentsPerGBMonth != 21_000 || api.ManagedPostgresSnapshotOverageMillicentsPerGBMonth != 9_450 {
		t.Fatalf("history prices = %d, %d millicents per GB-month", api.ManagedPostgresRestoreHistoryOverageMillicentsPerGBMonth, api.ManagedPostgresSnapshotOverageMillicentsPerGBMonth)
	}
	if testHistoryMonth != 744*3600 {
		t.Fatalf("history month = %d s, want Neon's 744 hours", testHistoryMonth)
	}
	for _, pool := range []int64{testGiB, 5 * testGiB, 20 * testGiB} {
		if got := api.ManagedPostgresIncludedHistoryBytes(pool); got != pool {
			t.Fatalf("included history for a %d-byte pool = %d, want the pool", pool, got)
		}
	}
}

func TestHistoryOverageCharge(t *testing.T) {
	historyPrice, snapshotPrice := int64(api.ManagedPostgresRestoreHistoryOverageMillicentsPerGBMonth), int64(api.ManagedPostgresSnapshotOverageMillicentsPerGBMonth)
	for _, tc := range []struct {
		name                        string
		history, snapshots, include int64
		billedHistory, billedSnaps  int64
		millicents                  int64
	}{
		{name: "inside the included history", history: testGiB * testHistoryMonth, include: testGiB},
		{name: "history and snapshots share the included history", history: testGiB * testHistoryMonth / 2, snapshots: testGiB * testHistoryMonth / 2, include: testGiB},
		{name: "one GB-month of restore history", history: testGB * testHistoryMonth, billedHistory: testGB * testHistoryMonth, millicents: historyPrice},
		{name: "one GB-month of snapshots", snapshots: testGB * testHistoryMonth, billedSnaps: testGB * testHistoryMonth, millicents: snapshotPrice},
		{name: "restore history takes the included history first", history: testGiB * testHistoryMonth / 2, snapshots: testGiB * testHistoryMonth, include: testGiB,
			billedSnaps: testGiB * testHistoryMonth / 2, millicents: testGiB / 2 * snapshotPrice / testGB},
		{name: "history past the included leaves snapshots all billed", history: 2 * testGiB * testHistoryMonth, snapshots: testGB * testHistoryMonth, include: testGiB,
			billedHistory: testGiB * testHistoryMonth, billedSnaps: testGB * testHistoryMonth, millicents: testGiB*historyPrice/testGB + snapshotPrice},
		// ADR-20261123062111's worked example: Pro (5 GiB included), 12 GB
		// of restore history and 1 GB of snapshots: $1.49.
		{name: "worked example", history: 12 * testGB * testHistoryMonth, snapshots: testGB * testHistoryMonth, include: 5 * testGiB,
			billedHistory: (12*testGB - 5*testGiB) * testHistoryMonth, billedSnaps: testGB * testHistoryMonth, millicents: 139_257 + 9_450},
		{name: "no ceiling", history: 1000 * testGB * testHistoryMonth, include: testGiB,
			billedHistory: (1000*testGB - testGiB) * testHistoryMonth, millicents: (1000*testGB - testGiB) * historyPrice / testGB},
		{name: "a fraction of a millicent is not charged", history: 1, billedHistory: 1},
		{name: "negative usage reads as none", history: -1, snapshots: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			billedHistory, billedSnaps, millicents := HistoryOverageCharge(tc.history, tc.snapshots, tc.include)
			if billedHistory != tc.billedHistory || billedSnaps != tc.billedSnaps || millicents != tc.millicents {
				t.Fatalf("HistoryOverageCharge = %d, %d byte-s, %d millicents; want %d, %d, %d",
					billedHistory, billedSnaps, millicents, tc.billedHistory, tc.billedSnaps, tc.millicents)
			}
		})
	}
}

// historyBefore answers UsageBefore with restore history at each span end.
type historyBefore map[time.Time]int64

func (u historyBefore) UsageBefore(_ context.Context, _ string, month, before time.Time) (UsageSnapshot, error) {
	return UsageSnapshot{PeriodStart: monthStart(month), HistoryByteSeconds: u[before]}, nil
}

func TestChargeBySpanBillsHistoryAgainstEachPlan(t *testing.T) {
	month := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	change, at := month.Add(15*24*time.Hour), month.Add(20*24*time.Hour)
	giBMonth := testGiB * testHistoryMonth
	for _, tc := range []struct {
		name          string
		before, after int64 // included history (GiB) before and after the change
		atChange      int64 // GiB-months of history before the change
		total         int64 // GiB-months of history through at
		wantBilled    int64 // GiB-months
	}{
		// Pro → Hobby: the 4 GiB-months Pro included stay included; Hobby
		// bills what grew past its 1 GiB after the change.
		{"downgrade bills only history after it", 5, 1, 4, 6, 2},
		// Hobby → Pro: the 2 GiB-months billed on Hobby stay billed.
		{"upgrade keeps history already billed", 1, 5, 3, 4, 2},
		{"within every plan", 5, 5, 1, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spans := []AllowanceSpan{{End: change, StorageAllowance: tc.before * testGiB}, {End: at, StorageAllowance: tc.after * testGiB}}
			got, err := chargeBySpan(context.Background(), historyBefore{change: tc.atChange * giBMonth}, "acct", month, spans,
				UsageSnapshot{HistoryByteSeconds: tc.total * giBMonth}, false, true)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.wantBilled * giBMonth
			if got.billedHistoryByteSeconds != want || got.millicents != historyOverageMillicents(want, 0) || got.includedHistory != tc.after*testGiB {
				t.Fatalf("history billed %d byte-s for %d millicents (included %d); want %d byte-s", got.billedHistoryByteSeconds, got.millicents, got.includedHistory, want)
			}
		})
	}
}

func TestOverageBillerChargesRestoreHistory(t *testing.T) {
	december := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	over := testGB * testHistoryMonth // history past the included GiB
	price := int64(api.ManagedPostgresRestoreHistoryOverageMillicentsPerGBMonth)
	for _, tc := range []struct {
		name        string
		historyFrom time.Time
		overage     bool
		pool        int64
		history     int64
		wantBilled  int64
		wantCharge  int64
	}{
		{name: "past the included history with overage on", historyFrom: december, overage: true, pool: testGiB,
			history: testGiB*testHistoryMonth + over, wantBilled: over, wantCharge: price},
		{name: "a later start month charges this month nothing", historyFrom: december.AddDate(0, 1, 0), overage: true, pool: testGiB,
			history: testGiB*testHistoryMonth + over},
		{name: "an unset start month charges nothing", overage: true, pool: testGiB, history: testGiB*testHistoryMonth + over},
		{name: "overage off charges nothing", historyFrom: december, pool: testGiB, history: testGiB*testHistoryMonth + over},
		{name: "inside the included history", historyFrom: december, overage: true, pool: testGiB, history: testGiB * testHistoryMonth},
		{name: "a plan without a pool charges nothing", historyFrom: december, overage: true, history: over},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := newOverageFixture(t, december.Add(10*24*time.Hour))
			f.limits.Overage, f.limits.StorageAllowance = tc.overage, tc.pool
			if tc.overage {
				if err := f.store.SetOverage(ctx, "acct", true, f.now); err != nil {
					t.Fatal(err)
				}
			} else {
				f.reachAllowance(t, "acct", december, false)
			}
			f.store.used["acct2026-12"] = testAllowance / 2
			f.store.history = map[string]int64{"acct2026-12": tc.history}
			biller := f.biller(t, true, december)
			biller.options.HistoryFrom = tc.historyFrom
			if _, err := biller.Deliver(ctx); err != nil {
				t.Fatal(err)
			}
			if tc.wantCharge == 0 {
				if len(f.published) != 0 {
					t.Fatalf("history charged: %+v", f.published)
				}
				return
			}
			if len(f.published) != 1 {
				t.Fatalf("published %+v", f.published)
			}
			step := f.published[0]
			if step.BilledHistoryByteSeconds != tc.wantBilled || step.TargetMillicents != tc.wantCharge || step.IncludedHistoryBytes != tc.pool ||
				step.UsedHistoryByteSeconds != tc.history || step.BilledUnitSeconds != 0 {
				t.Fatalf("history step %+v", step)
			}
			delivery, err := f.store.OverageDelivered(ctx, "polar", "acct", december)
			if err != nil || delivery.BilledHistoryByteSeconds != tc.wantBilled || delivery.Millicents != tc.wantCharge {
				t.Fatalf("delivery %+v, %v", delivery, err)
			}
		})
	}
}

// A month that charged history keeps it: turning overage off, or an upgrade
// that raises the included history, never lowers the cumulative charge.
func TestOverageBillerKeepsChargedHistory(t *testing.T) {
	ctx := context.Background()
	december := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	price := int64(api.ManagedPostgresRestoreHistoryOverageMillicentsPerGBMonth)
	f := newOverageFixture(t, december.Add(10*24*time.Hour))
	f.limits.StorageAllowance = testGiB
	if err := f.store.SetOverage(ctx, "acct", true, f.now); err != nil {
		t.Fatal(err)
	}
	f.store.history = map[string]int64{"acct2026-12": testGiB*testHistoryMonth + testGB*testHistoryMonth}
	deliver := func() {
		t.Helper()
		biller := f.biller(t, true, december)
		biller.options.HistoryFrom = december
		if _, err := biller.Deliver(ctx); err != nil {
			t.Fatal(err)
		}
	}
	deliver()
	if len(f.published) != 1 || f.published[0].TargetMillicents != price {
		t.Fatalf("first step %+v", f.published)
	}

	// Overage off and an upgrade: the history already charged stays, and
	// compute past the allowance is charged on top.
	f.limits.Overage, f.limits.StorageAllowance = false, 5*testGiB
	f.store.used["acct2026-12"] = testAllowance + 3600
	deliver()
	if len(f.published) != 2 || f.published[1].TargetMillicents != price+api.ManagedPostgresOverageMillicentsPerComputeUnitHour ||
		f.published[1].BilledHistoryByteSeconds != testGB*testHistoryMonth {
		t.Fatalf("after overage off: %+v", f.published)
	}
}

func TestNewOverageBillerRejectsAMidMonthHistoryStart(t *testing.T) {
	december := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	f := newOverageFixture(t, december)
	for _, from := range []time.Time{december.Add(time.Hour), december.Add(24 * time.Hour)} {
		_, err := NewOverageBiller(f.store, OverageBillerOptions{Provider: "polar", EffectiveFrom: december, HistoryFrom: from,
			Allowance: func(context.Context, string, time.Time) (CapLimits, error) { return f.limits, nil }})
		if err == nil {
			t.Fatalf("history start %s accepted", from)
		}
	}
}

func TestMemoryStoreSplitsRestoreHistoryAndSnapshots(t *testing.T) {
	month := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	snapshot := UsageSnapshot{PeriodStart: month, HistoryByteSeconds: 3, SnapshotByteSeconds: 5}
	items, err := (UsagePolicy{HistoryGiBHourMillicents: 1}).LineItems(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, item := range items {
		got[item.Code] = item.Quantity
	}
	if got["managed_postgres.restore_history"] != 3 || got["managed_postgres.snapshots"] != 5 {
		t.Fatalf("line items %+v", items)
	}
	if !validMeter(MeterSnapshotByteSeconds) {
		t.Fatal("snapshot meter rejected")
	}
}

// The operator's history ceiling bounds restore history and snapshots
// together, as the single history meter did before the split.
func TestHistoryCeilingCountsSnapshots(t *testing.T) {
	policy := UsagePolicy{Enabled: true, MaxMonthlyCostMillicents: 1 << 50, MaxMonthlyComputeUnitSeconds: 1 << 50,
		MaxMonthlyStorageByteSeconds: 1 << 50, MaxMonthlyEgressBytes: 1 << 50, MaxMonthlyHistoryByteSeconds: 1000}
	for _, tc := range []struct {
		name               string
		history, snapshots int64
		exceeds, headroom  bool
	}{
		{name: "well below", history: 100, snapshots: 100, headroom: true},
		{name: "snapshots alone reach it", history: 0, snapshots: 1000, exceeds: true},
		{name: "together reach it", history: 600, snapshots: 400, exceeds: true},
		{name: "together near it leave no headroom", history: 500, snapshots: 450},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := UsageSnapshot{HistoryByteSeconds: tc.history, SnapshotByteSeconds: tc.snapshots}
			if got := s.Exceeds(policy); got != tc.exceeds {
				t.Fatalf("Exceeds = %v, want %v", got, tc.exceeds)
			}
			if got := s.hasHeadroom(policy); got != tc.headroom {
				t.Fatalf("hasHeadroom = %v, want %v", got, tc.headroom)
			}
		})
	}
}
