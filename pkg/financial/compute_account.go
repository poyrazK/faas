package financial

import (
	"math/big"
	"time"
)

// ComputeQuantity ceilings physical seconds before multiplying billable RAM.
// Unlike money, allowance is a quantity and cannot be allocated per price.
func ComputeQuantity(mb int, from, until time.Time) (int64, error) {
	if mb <= 0 || from.IsZero() || until.Before(from) {
		return 0, ErrInvalid
	}
	d := until.Sub(from)
	seconds := int64(d / time.Second)
	if d%time.Second != 0 {
		seconds++
	}
	n := new(big.Int).Mul(big.NewInt(seconds), big.NewInt(int64(mb)))
	if !n.IsInt64() {
		return 0, ErrOverflow
	}
	return n.Int64(), nil
}

// ComputeOverageBound upper-bounds the sum of floored net contract costs even
// when allowance is reallocated proportionally across historical price versions:
// sum(floor(rate_i * net_quantity_i)) <= ceil(max_rate * sum(net_quantity_i)).
// This avoids assigning free seconds to a particular lease or price version.
func ComputeOverageBound(quantity, allowance, rate, unit int64) (int64, error) {
	if quantity < 0 || allowance < 0 || rate < 0 || unit <= 0 {
		return 0, ErrInvalid
	}
	n := new(big.Int).Mul(big.NewInt(max(int64(0), quantity-allowance)), big.NewInt(rate))
	d := big.NewInt(unit)
	n.Add(n, new(big.Int).Sub(d, big.NewInt(1))).Quo(n, d)
	if !n.IsInt64() {
		return 0, ErrOverflow
	}
	return n.Int64(), nil
}
