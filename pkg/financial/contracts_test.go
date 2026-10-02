package financial

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// adr: 431 — version changes must not multiply the shared monthly allowance.
func TestCostContractsSharedGrantAndRateChanges(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	prices := []Price{{Version: "a", Meter: "compute", Currency: "EUR", Unit: "mb_seconds", UnitQuantity: 1, MillicentsPerUnit: 1, IncludedQuantity: 50}, {Version: "b", Meter: "compute", Currency: "EUR", Unit: "mb_seconds", UnitQuantity: 1, MillicentsPerUnit: 2, IncludedQuantity: 100}}
	entries := []VersionedEvidence{{PriceVersion: "a", Evidence: Evidence{AccountID: "acct", SourceID: "1", Meter: "compute", Quantity: 100, Start: start, End: start.Add(time.Minute), Attribution: Attribution{AppID: "app"}}}, {PriceVersion: "b", Evidence: Evidence{AccountID: "acct", SourceID: "2", Meter: "compute", Quantity: 100, Start: start.Add(time.Minute), End: start.Add(2 * time.Minute), Attribution: Attribution{AppID: "job"}}}}
	got, err := CostContracts("acct", start, end, prices, entries)
	if err != nil || got.Quantity != 200 || got.IncludedQuantity != 100 || got.NetMillicents != 150 {
		t.Fatalf("shared grant: %+v, %v", got, err)
	}
	prices[0], prices[1] = prices[1], prices[0]
	entries[0], entries[1] = entries[1], entries[0]
	entries = append(entries, entries[0])
	replay, err := CostContracts("acct", start, end, prices, entries)
	if err != nil || !reflect.DeepEqual(got, replay) {
		t.Fatalf("replay/order changed cost: %+v, %v", replay, err)
	}
	entries[len(entries)-1].PriceVersion = "a"
	if _, err := CostContracts("acct", start, end, prices, entries); !errors.Is(err, ErrInvalid) {
		t.Fatalf("price-changing replay accepted: %v", err)
	}
}
