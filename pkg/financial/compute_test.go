package financial

import (
	"errors"
	"math/big"
	"testing"
	"time"
)

// adr: 566 — funding must dominate exact prices and independent allocation
// rounding for every UTC-minute split, without floats or cumulative overflow.
func TestFinancialComputeChargeBoundsMinuteAllocation(t *testing.T) {
	base := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	for offset := range 60 {
		for _, seconds := range []int{0, 1, 29, 60, 121, 150, 3600} {
			from := base.Add(time.Duration(offset) * time.Second)
			until := from.Add(time.Duration(seconds) * time.Second)
			got, err := ComputeCharge(264, from, until, 1000, 1024*3600)
			if err != nil {
				t.Fatal(err)
			}
			exact := new(big.Rat).SetFrac(big.NewInt(int64(264*seconds*1000)), big.NewInt(1024*3600))
			if new(big.Rat).SetInt64(got).Cmp(exact) < 0 {
				t.Fatalf("underfunded %d/%d: %d < %s", offset, seconds, got, exact)
			}
			if seconds == 0 && got != 0 {
				t.Fatal("zero execution charged")
			}
			previous := int64(0)
			for tick := 0; tick <= seconds; tick += 7 {
				amount, err := ComputeCharge(264, from, from.Add(time.Duration(tick)*time.Second), 1000, 1024*3600)
				if err != nil || amount < previous || amount > got {
					t.Fatalf("nonmonotonic funding: %d %v", amount, err)
				}
				previous = amount
			}
		}
	}
}

func TestFinancialComputeChargeOverflowAndSubsecond(t *testing.T) {
	start := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	got, err := ComputeCharge(1, start, start.Add(time.Nanosecond), 1, 1)
	if err != nil || got != 2 {
		t.Fatalf("subsecond bound: %d %v", got, err)
	}
	if _, err := ComputeCharge(1<<30, start, start.Add(31*24*time.Hour), 1<<62, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflow: %v", err)
	}
}
