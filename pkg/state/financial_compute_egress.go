// adr: 1748 — byte and compute candidates commit atomically under account lock.
package state

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

func computePolicyMoney(b financial.ComputeBook, p financial.BudgetSpec) (int64, int64, error) {
	c, r := b.Committed, b.Reserved
	if financial.StrictAccountEgress(p) {
		if b.Egress.Committed < 0 || b.Egress.Reserved < 0 || c > api.FinancialBudgetMoneyMax-b.Egress.Committed || r > api.FinancialBudgetMoneyMax-b.Egress.Reserved {
			return 0, 0, financial.ErrOverflow
		}
		c += b.Egress.Committed
		r += b.Egress.Reserved
	}
	if c < 0 || r < 0 || c > api.FinancialBudgetMoneyMax-r {
		return 0, 0, financial.ErrOverflow
	}
	return c, r, nil
}

func computeEgressTerms(prices []FinancialPriceSnapshot, month, now time.Time, b financial.EgressBook) (financial.EgressBook, error) {
	found := false
	grantFound := false
	for _, row := range prices {
		p := row.Price
		if p.Meter != "egress" || !row.PeriodStart.Equal(month) {
			continue
		}
		if p.Version == "" || p.Currency != financial.BillingCurrency || p.Unit != "interface_bytes" || p.UnitQuantity <= 0 || p.MillicentsPerUnit < 0 || p.IncludedQuantity < 0 || p.IncludedQuantity > api.FinancialComputeQuantityMax {
			return b, financial.ErrComputeUnavailable
		}
		if b.Initialized {
			if !financial.EgressContractFits(p, b) || p.IncludedQuantity < b.AllowanceGranted {
				return b, financial.ErrComputeUnavailable
			}
		} else if b.RateQuantity == 0 || !financial.EgressContractFits(p, b) {
			b.RateMillicents = p.MillicentsPerUnit
			b.RateQuantity = p.UnitQuantity
		}
		if !b.Initialized {
			if !grantFound || p.IncludedQuantity < b.AllowanceGranted {
				b.AllowanceGranted = p.IncludedQuantity
			}
			grantFound = true
		}
		if !row.EffectiveFrom.After(now) {
			found = true
		}
	}
	if !found {
		return b, financial.ErrComputeUnavailable
	}
	b.Initialized = true
	err := computeEgressReprice(&b)
	return b, err
}

func computeEgressReprice(b *financial.EgressBook) error {
	if b.QuantityCommitted < 0 || b.QuantityReserved < 0 || b.QuantityCommitted > api.FinancialComputeQuantityMax-b.QuantityReserved || b.AllowanceGranted < 0 || b.AllowanceGranted > api.FinancialComputeQuantityMax {
		return financial.ErrOverflow
	}
	c, err := financial.ComputeOverageBound(b.QuantityCommitted, b.AllowanceGranted, b.RateMillicents, b.RateQuantity)
	if err != nil {
		return err
	}
	total, err := financial.ComputeOverageBound(b.QuantityCommitted+b.QuantityReserved, b.AllowanceGranted, b.RateMillicents, b.RateQuantity)
	if err != nil {
		return err
	}
	b.Committed = max(b.Committed, c)
	total = max(b.Committed, total)
	if total > api.FinancialBudgetMoneyMax {
		return financial.ErrOverflow
	}
	b.Reserved = total - b.Committed
	return nil
}

func computeFundEgress(tx financialComputeTxn, b *financial.ComputeBook, l *financial.ComputeLease, policies []FinancialBudget, refill bool) error {
	prices, err := tx.egressPrices(b.PeriodStart)
	if err != nil {
		return err
	}
	b.Egress, err = computeEgressTerms(prices, b.PeriodStart, tx.clock(), b.Egress)
	if err != nil {
		return err
	}
	// Existing funded attempts keep their byte gate after a policy is removed.
	// Record uncapped additional credits so disable can resume traffic while
	// the original quota and retirement receipts remain intact.
	if !refill {
		return nil
	}
	candidate := func(q int64) (financial.ComputeBook, error) {
		c := *b
		if q < 0 || q > api.FinancialComputeQuantityMax-c.Egress.QuantityCommitted-c.Egress.QuantityReserved || q > api.FinancialComputeQuantityMax-l.EgressGranted {
			return c, financial.ErrOverflow
		}
		c.Egress.QuantityReserved += q
		err := computeEgressReprice(&c.Egress)
		return c, err
	}
	fits := func(c financial.ComputeBook) bool {
		for _, p := range policies {
			if financial.StrictAccountEgress(p.Spec) {
				paid, reserved, err := computePolicyMoney(c, p.Spec)
				if err != nil || paid+reserved > p.Spec.LimitMillicents {
					return false
				}
			}
		}
		return true
	}
	lo, hi := int64(0), api.FinancialEgressCreditBytes
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		c, err := candidate(mid)
		if err == nil && fits(c) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo < api.FinancialEgressMinimumCreditBytes {
		c, err := candidate(api.FinancialEgressMinimumCreditBytes)
		if err != nil {
			return err
		}
		return errors.Join(financial.ErrComputeBudget, computeCheckPolicies(tx, c, policies))
	}
	c, err := candidate(lo)
	if err != nil {
		return err
	}
	if err = computeCheckPolicies(tx, c, policies); err != nil {
		return err
	}
	*b = c
	l.EgressGranted += lo
	l.EgressGrant = lo
	l.EgressSequence++
	return nil
}
