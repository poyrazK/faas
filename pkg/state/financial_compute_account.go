// adr: 1361 — account and app authority is atomic.
package state

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

func computeCheckPolicies(tx financialComputeTxn, b financial.ComputeBook, policies []FinancialBudget) error {
	holds, err := tx.holds()
	if err != nil {
		return err
	}
	denied := false
	for _, p := range policies {
		if b.AppID == "" && !financial.StrictAccountCompute(p.Spec) || b.AppID != "" && !financial.StrictAppCompute(p.Spec) {
			continue
		}
		held := false
		for _, h := range holds {
			if h.PolicyID == p.ID && h.Revision == p.Revision && (p.Spec.ResumeRule == "manual" || h.PeriodStart.Equal(b.PeriodStart)) {
				held = true
			}
		}
		if held || b.Committed+b.Reserved > p.Spec.LimitMillicents {
			denied = true
			if err := tx.putHold(financialComputeHold{p.ID, p.Revision, b.PeriodStart}); err != nil {
				return err
			}
		}
	}
	if denied {
		return financial.ErrComputeBudget
	}
	return nil
}

func computeAccountFundingBook(tx financialComputeTxn, l *financial.ComputeLease, policies []FinancialBudget) (*financial.ComputeBook, error) {
	for _, p := range policies {
		if financial.StrictAccountCompute(p.Spec) {
			b, err := tx.book("", l.PeriodStart)
			if err != nil {
				return nil, errors.Join(financial.ErrComputeUnavailable, err)
			}
			active, err := tx.active("")
			if err != nil {
				return nil, err
			}
			for _, old := range active {
				if !old.AccountFunded || !old.PeriodStart.Equal(l.PeriodStart) {
					return nil, financial.ErrComputeUnavailable
				}
			}
			l.AccountFunded = true
			return &b, nil
		}
	}
	return nil, nil
}

func computeAccountReprice(b *financial.ComputeBook) error {
	if b.QuantityCommitted < 0 || b.QuantityReserved < 0 || b.QuantityCommitted > api.FinancialComputeQuantityMax-b.QuantityReserved || b.AllowanceGranted < 0 || b.AllowanceGranted > api.FinancialComputeQuantityMax {
		return financial.ErrOverflow
	}
	committed, err := financial.ComputeOverageBound(b.QuantityCommitted, b.AllowanceGranted, api.OverageMillicentsPerGBHour, api.SecondsPerGBHour)
	if err != nil {
		return err
	}
	total, err := financial.ComputeOverageBound(b.QuantityCommitted+b.QuantityReserved, b.AllowanceGranted, api.OverageMillicentsPerGBHour, api.SecondsPerGBHour)
	if err != nil {
		return err
	}
	// A larger grant may reduce future reserves, but never erases paid authority
	// already committed. Refunds and negative corrections cannot mint funding.
	b.Committed = max(b.Committed, committed)
	total = max(b.Committed, total)
	if total > api.FinancialBudgetMoneyMax {
		return financial.ErrOverflow
	}
	b.Reserved = total - b.Committed
	return nil
}

func computeFundAccount(tx financialComputeTxn, b *financial.ComputeBook, l *financial.ComputeLease, policies []FinancialBudget) error {
	grant, err := tx.allowance(b.PeriodStart)
	if err != nil {
		return err
	}
	b.AllowanceGranted = max(b.AllowanceGranted, grant)
	total, err := financial.ComputeQuantity(l.BillableMB, l.StartedAt, l.FundedUntil)
	if err != nil {
		return err
	}
	without := b.QuantityReserved - l.QuantityReserved
	if without < 0 || total < l.QuantityCommitted {
		return financial.ErrComputeUnavailable
	}
	wanted := total - l.QuantityCommitted
	if wanted > api.FinancialComputeQuantityMax-b.QuantityCommitted || without > api.FinancialComputeQuantityMax-b.QuantityCommitted-wanted {
		return financial.ErrOverflow
	}
	b.QuantityReserved = without + wanted
	if err := computeAccountReprice(b); err != nil {
		return err
	}
	if err := computeCheckPolicies(tx, *b, policies); err != nil {
		return err
	}
	l.QuantityReserved = wanted
	return nil
}

func computeProgressAccount(tx financialComputeTxn, l *financial.ComputeLease, until time.Time, settle bool) (*financial.ComputeBook, error) {
	if !l.AccountFunded {
		return nil, nil
	}
	b, err := tx.book("", l.PeriodStart)
	if err != nil {
		return nil, err
	}
	spent, err := financial.ComputeQuantity(l.BillableMB, l.StartedAt, until)
	if err != nil {
		return nil, err
	}
	delta := spent - l.QuantityCommitted
	if delta < 0 || delta > l.QuantityReserved || b.QuantityReserved < delta {
		return nil, financial.ErrComputeUnavailable
	}
	b.QuantityCommitted += delta
	b.QuantityReserved -= delta
	l.QuantityCommitted = spent
	l.QuantityReserved -= delta
	if settle {
		b.QuantityReserved -= l.QuantityReserved
		l.QuantityReserved = 0
	}
	if err := computeAccountReprice(&b); err != nil {
		return nil, err
	}
	return &b, nil
}

func computeAllowance(prices []FinancialPriceSnapshot, month, now time.Time) (int64, error) {
	var grant int64
	found := false
	for _, p := range prices {
		if p.Price.Meter != "compute" || !p.PeriodStart.Equal(month) || p.EffectiveFrom.After(now) {
			continue
		}
		if !computeAccountContractFits(p.Price) || p.Price.IncludedQuantity < 0 || p.Price.IncludedQuantity > api.FinancialComputeQuantityMax {
			return 0, financial.ErrComputeUnavailable
		}
		found = true
		grant = max(grant, p.Price.IncludedQuantity)
	}
	if !found {
		return 0, financial.ErrComputeUnavailable
	}
	return grant, nil
}

// Account allowance cannot be initialized from the billing fallback rate: a
// missing historical contract must remain an explicit coverage failure.
func computeAccountContractFits(p financial.Price) bool {
	return p.Version != "" && p.Meter == "compute" && p.Currency == "EUR" && p.Unit == "mb_seconds" && p.UnitQuantity > 0 && p.MillicentsPerUnit >= 0 && computeRateFits(p, financial.ComputeLease{RateMillicents: api.OverageMillicentsPerGBHour, RateQuantity: api.SecondsPerGBHour})
}
