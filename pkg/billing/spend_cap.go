package billing

import (
	"math"
	"math/big"

	"github.com/onebox-faas/faas/pkg/api"
)

// The overage cap is a hard ceiling on what a calendar month is billed
// (ADR-1162). Every meter reserves the month's billable cost of the others, so
// the cap can be under-used but never exceeded. The arithmetic is exact:
// costs are compared as rationals before any rounding, without overflow for
// large but valid quantities.

// CapRemainingMillicents is the cap in exact millicents less the billable
// compute (mb_seconds), egress and object storage (millicents, delivered or
// pending, ADR-20261027093220) already reserved against it. A nil egress
// policy reserves no egress. ok=false (negative input or an invalid egress
// price) fails the caller closed.
func CapRemainingMillicents(capCents, computeMBSeconds, egress int64, egressPolicy *MeterUsagePolicy, objectStorageMillicents int64) (*big.Rat, bool) {
	if capCents < 0 || computeMBSeconds < 0 || egress < 0 || objectStorageMillicents < 0 {
		return nil, false
	}
	remaining := new(big.Rat).SetInt(new(big.Int).Mul(big.NewInt(capCents), big.NewInt(api.MillicentsPerCent)))
	remaining.Sub(remaining, new(big.Rat).SetInt64(objectStorageMillicents))
	remaining.Sub(remaining, new(big.Rat).SetFrac(
		new(big.Int).Mul(big.NewInt(computeMBSeconds), big.NewInt(api.OverageMillicentsPerGBHour)),
		big.NewInt(api.SecondsPerGBHour)))
	if egressPolicy == nil || egress == 0 {
		return remaining, true
	}
	if egressPolicy.UnitQuantity <= 0 || egressPolicy.MillicentsPerUnit <= 0 {
		return nil, false
	}
	remaining.Sub(remaining, new(big.Rat).SetFrac(
		new(big.Int).Mul(big.NewInt(egress), big.NewInt(egressPolicy.MillicentsPerUnit)),
		big.NewInt(egressPolicy.UnitQuantity)))
	return remaining, true
}

// FloorCapQuantity converts remaining millicents to whole units of a meter
// priced at millicentsPer per unitQuantity, rounding down and saturating at
// MaxInt64. FloorCapQuantity(remaining, 1, 1) is whole millicents.
func FloorCapQuantity(remaining *big.Rat, unitQuantity, millicentsPer int64) int64 {
	if remaining.Sign() <= 0 {
		return 0
	}
	q := new(big.Rat).Mul(remaining, new(big.Rat).SetFrac(big.NewInt(unitQuantity), big.NewInt(millicentsPer)))
	n := new(big.Int).Quo(q.Num(), q.Denom())
	if !n.IsInt64() {
		return math.MaxInt64
	}
	return n.Int64()
}
