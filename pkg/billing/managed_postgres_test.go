package billing

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestManagedPostgresUsageLineItemsUseStableCodesAndProviderCosts(t *testing.T) {
	policy := managedpostgres.UsagePolicy{
		ComputeUnitHourMillicents: 3600,
		StorageGiBHourMillicents:  2500,
		HistoryGiBHourMillicents:  500,
		EgressGiBMillicents:       1000,
	}
	snapshot := managedpostgres.UsageSnapshot{
		PeriodStart:        time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		ComputeUnitSeconds: 3600,
		StorageByteSeconds: int64(1<<30) * 3600,
		HistoryByteSeconds: int64(1<<30) * 3600,
		EgressBytes:        1 << 30,
	}
	items, err := ManagedPostgresUsageLineItems("acct-1", snapshot, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("items = %+v, want four meters", items)
	}
	want := map[string]int64{
		"managed_postgres.compute":         3600,
		"managed_postgres.storage":         2500,
		"managed_postgres.restore_history": 500,
		"managed_postgres.egress":          1000,
	}
	for _, item := range items {
		if item.AccountID != "acct-1" || item.PeriodStart.IsZero() {
			t.Fatalf("item identity = %+v", item)
		}
		if item.ProviderCostMillicents != want[item.Code] {
			t.Fatalf("item %q cost = %d, want %d", item.Code, item.ProviderCostMillicents, want[item.Code])
		}
		delete(want, item.Code)
	}
	if len(want) != 0 {
		t.Fatalf("missing item codes: %v", want)
	}
}

func TestManagedPostgresUsageLineItemsRejectInvalidSnapshot(t *testing.T) {
	if _, err := ManagedPostgresUsageLineItems("acct-1", managedpostgres.UsageSnapshot{}, managedpostgres.UsagePolicy{}); err == nil {
		t.Fatal("zero snapshot unexpectedly accepted")
	}
}
