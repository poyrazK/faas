// adr: 1642 — one paid account ceiling covers compute and interface egress.
package financial

import "math/big"

func StrictAccountEgress(p BudgetSpec) bool {
	return StrictAccountCompute(p) && len(p.Meters) == 2 && p.Meters[1] == "egress"
}

// EgressBook holds quantities independently of compute's MB-second allowance.
// Credits stay reserved until physical absence, then commit in full. Lost or
// unused credits never create fresh authority; this is deliberately an upper
// bound on invoice quantities rather than a second usage meter.
type EgressBook struct {
	Initialized       bool  `json:"initialized"`
	QuantityCommitted int64 `json:"quantity_committed"`
	QuantityReserved  int64 `json:"quantity_reserved"`
	AllowanceGranted  int64 `json:"allowance_granted"`
	Committed         int64 `json:"committed"`
	Reserved          int64 `json:"reserved"`
	RateMillicents    int64 `json:"rate_millicents"`
	RateQuantity      int64 `json:"rate_quantity"`
}

type EgressBalance struct {
	Initialized         bool  `json:"initialized"`
	CommittedBytes      int64 `json:"committed_bytes"`
	ReservedBytes       int64 `json:"reserved_bytes"`
	AllowanceGranted    int64 `json:"allowance_granted_bytes"`
	AllowanceCommitted  int64 `json:"allowance_committed_bytes"`
	AllowanceReserved   int64 `json:"allowance_reserved_bytes"`
	AllowanceRemaining  int64 `json:"allowance_remaining_bytes"`
	CommittedMillicents int64 `json:"committed_millicents"`
	ReservedMillicents  int64 `json:"reserved_millicents"`
}

func EgressContractFits(p Price, b EgressBook) bool {
	if p.Version == "" || p.Meter != "egress" || p.Currency != BillingCurrency || p.Unit != "interface_bytes" || p.UnitQuantity <= 0 || p.MillicentsPerUnit < 0 || b.RateQuantity <= 0 || b.RateMillicents < 0 {
		return false
	}
	actual := new(big.Int).Mul(big.NewInt(p.MillicentsPerUnit), big.NewInt(b.RateQuantity))
	bound := new(big.Int).Mul(big.NewInt(b.RateMillicents), big.NewInt(p.UnitQuantity))
	return actual.Cmp(bound) <= 0
}
