package financial

import (
	"errors"
	"math"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

// adr: 566 — shared allowances and allocations reconcile with exact money.
func TestCostMeterSharedAllowanceAndReplay(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	price := Price{Version: "compute-v1", Meter: "compute", Currency: "EUR", Unit: "mb_seconds", UnitQuantity: 10, MillicentsPerUnit: 100, IncludedQuantity: 5}
	entries := []Evidence{evidence(start, "one", "app-a", 4), evidence(start, "two", "app-b", 6)}
	entries = append(entries, entries[0])
	got, err := CostMeter("account", start, end, price, entries)
	if err != nil {
		t.Fatal(err)
	}
	if got.Quantity != 10 || got.NetMillicents != 50 || got.AllowanceMillicents != 50 || got.GrossMillicents != 100 {
		t.Fatalf("cost=%+v", got)
	}
	if got.Allocations[0].NetMillicents != 20 || got.Allocations[1].NetMillicents != 30 {
		t.Fatalf("allocations=%+v", got.Allocations)
	}
	entries[2].Quantity++
	if _, err := CostMeter("account", start, end, price, entries); !errors.Is(err, ErrInvalid) {
		t.Fatalf("conflicting replay=%v", err)
	}
}

// adr: 566 — largest-remainder allocation is exact and input-order independent.
func TestCostMeterAllocationProperties(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	rng := rand.New(rand.NewSource(431))
	for trial := 0; trial < 500; trial++ {
		p := Price{Version: "v1", Meter: "compute", Currency: "EUR", Unit: "unit", UnitQuantity: int64(rng.Intn(100) + 1), MillicentsPerUnit: int64(rng.Intn(1000)), IncludedQuantity: int64(rng.Intn(100))}
		es := []Evidence{evidence(start, "a", "one", int64(rng.Intn(100))), evidence(start, "b", "two", int64(rng.Intn(100))), evidence(start, "c", "three", int64(rng.Intn(100)))}
		got, err := CostMeter("account", start, end, p, es)
		if err != nil {
			t.Fatal(err)
		}
		var quantity, gross, allowance, net int64
		for _, a := range got.Allocations {
			quantity += a.Quantity
			gross += a.GrossMillicents
			allowance += a.AllowanceMillicents
			net += a.NetMillicents
			if a.GrossMillicents != a.NetMillicents+a.AllowanceMillicents || a.NetMillicents < 0 {
				t.Fatalf("invalid allocation=%+v", a)
			}
		}
		if quantity != got.Quantity || gross != got.GrossMillicents || allowance != got.AllowanceMillicents || net != got.NetMillicents {
			t.Fatalf("non-reconciling cost=%+v", got)
		}
		rng.Shuffle(len(es), func(i, j int) { es[i], es[j] = es[j], es[i] })
		replayed, err := CostMeter("account", start, end, p, es)
		if err != nil || !reflect.DeepEqual(got, replayed) {
			t.Fatalf("order changed cost: %v", err)
		}
	}
}

// adr: 566 — corrections retain source lineage and cannot cross tenant/scope.
func TestCostMeterCorrectionAndOwnership(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	p := Price{Version: "v1", Meter: "compute", Currency: "EUR", Unit: "unit", UnitQuantity: 1, MillicentsPerUnit: 1}
	original := evidence(start, "original", "deleted-app", 10)
	correction := original
	correction.SourceID = "correction"
	correction.CorrectsSourceID = "original"
	correction.Quantity = -3
	got, err := CostMeter("account", start, end, p, []Evidence{original, correction})
	if err != nil || got.NetMillicents != 7 || got.Allocations[0].Attribution.AppID != "deleted-app" {
		t.Fatalf("correction=%+v %v", got, err)
	}
	tests := map[string][]Evidence{
		"missing original":         {correction},
		"cross account":            {func() Evidence { e := original; e.AccountID = "other"; return e }()},
		"cross app correction":     {original, func() Evidence { e := correction; e.Attribution.AppID = "other"; return e }()},
		"excess correction":        {original, func() Evidence { e := correction; e.Quantity = -11; return e }(), evidence(start, "other", "deleted-app", 100)},
		"wrong interval":           {original, func() Evidence { e := correction; e.End = e.End.Add(time.Minute); return e }()},
		"negative without lineage": {evidence(start, "negative", "app", -1)},
	}
	for name, es := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := CostMeter("account", start, end, p, es); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

// adr: 566 — exact intermediate arithmetic avoids overflow without hiding it.
func TestCostMeterOverflow(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	p := Price{Version: "v1", Meter: "compute", Currency: "EUR", Unit: "unit", UnitQuantity: math.MaxInt64, MillicentsPerUnit: math.MaxInt64}
	got, err := CostMeter("account", start, end, p, []Evidence{evidence(start, "a", "app", math.MaxInt64)})
	if err != nil || got.NetMillicents != math.MaxInt64 {
		t.Fatalf("exact boundary=%+v %v", got, err)
	}
	p.UnitQuantity = 1
	if _, err := CostMeter("account", start, end, p, []Evidence{evidence(start, "a", "app", 2)}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("money overflow=%v", err)
	}
	p.MillicentsPerUnit = 1
	if _, err := CostMeter("account", start, end, p, []Evidence{evidence(start, "a", "one", math.MaxInt64), evidence(start, "b", "two", 1)}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("quantity overflow=%v", err)
	}
}

func evidence(start time.Time, source, app string, quantity int64) Evidence {
	return Evidence{AccountID: "account", SourceID: source, Meter: "compute", Quantity: quantity, Start: start, End: start.Add(time.Minute), ObservedAt: start.Add(time.Minute), Attribution: Attribution{AppID: app}}
}
