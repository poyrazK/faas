package financial

import (
	"errors"
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

func TestComputeAccountBoundAcrossPriceVersions(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	rng := rand.New(rand.NewPCG(13, 57))
	for range 1000 {
		prices := []Price{}
		entries := []VersionedEvidence{}
		var quantity, grant int64
		for _, version := range []string{"free", "old", "new"} {
			q := rng.Int64N(1000000)
			p := Price{Version: version, Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: 3600, MillicentsPerUnit: rng.Int64N(1001), IncludedQuantity: rng.Int64N(1000000)}
			prices = append(prices, p)
			quantity += q
			grant = max(grant, p.IncludedQuantity)
			entries = append(entries, VersionedEvidence{PriceVersion: version, Evidence: Evidence{AccountID: "account", SourceID: version, Meter: "compute", Quantity: q, Start: start, End: start.Add(time.Minute)}})
		}
		cost, err := CostContracts("account", start, start.AddDate(0, 1, 0), prices, entries)
		if err != nil {
			t.Fatal(err)
		}
		bound, err := ComputeOverageBound(quantity, grant, 1000, 3600)
		if err != nil || cost.NetMillicents > bound {
			t.Fatalf("net=%d bound=%d: %v", cost.NetMillicents, bound, err)
		}
	}
}

func TestComputeQuantityAndOverageOverflow(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	q, err := ComputeQuantity(136, start, start.Add(time.Nanosecond))
	if err != nil || q != 136 {
		t.Fatalf("subsecond quantity: %d %v", q, err)
	}
	if _, err := ComputeOverageBound(math.MaxInt64, 0, math.MaxInt64, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflow: %v", err)
	}
	if _, err := ComputeQuantity(136, start, start.Add(-time.Second)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative duration: %v", err)
	}
}
