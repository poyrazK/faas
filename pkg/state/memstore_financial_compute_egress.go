package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

func (t *memFinancialComputeTxn) egressOwner(node, incarnation string) (bool, error) {
	valid, err := t.owner(node, incarnation)
	return valid && t.m.financialComputeOwners[node].EgressIncarnation == incarnation, err
}
func (t *memFinancialComputeTxn) egressPrices(month time.Time) ([]FinancialPriceSnapshot, error) {
	prices := []FinancialPriceSnapshot{}
	for _, p := range t.m.financialPrices {
		if p.AccountID == t.account && p.PeriodStart.Equal(month) {
			prices = append(prices, p)
		}
	}
	return prices, nil
}
func (t *memFinancialComputeTxn) egressReady() error {
	if !t.accountReady() {
		return financial.ErrComputeUnavailable
	}
	for node, n := range t.m.computeNodes {
		if n.Active {
			valid, _ := t.egressOwner(node, t.m.computeNodeRuntimeIncarnations[node])
			if !valid {
				return financial.ErrComputeUnavailable
			}
		}
	}
	since := maxComputeTime(financial.ComputeMonth(t.now), t.m.accounts[t.account].CreatedAt)
	for at := since.Truncate(time.Minute); at.Before(t.now.Truncate(time.Minute)); at = at.Add(time.Minute) {
		if !t.m.financialSamplingWindows[at].egress {
			return financial.ErrComputeUnavailable
		}
	}
	return nil
}

func (t *memFinancialComputeTxn) initializeEgress() error {
	if err := t.egressReady(); err != nil {
		return err
	}
	b, err := t.book("", financial.ComputeMonth(t.now))
	if err != nil {
		return err
	}
	prices, _ := t.egressPrices(b.PeriodStart)
	b.Egress, err = computeEgressTerms(prices, b.PeriodStart, t.now, b.Egress)
	if err != nil {
		return err
	}
	var quantity int64
	for _, row := range t.m.financialEvidence {
		e := row.Evidence
		if e.AccountID != t.account || e.Meter != "egress" || e.Quantity <= 0 || e.Start.Before(b.PeriodStart) || !e.Start.Before(b.PeriodEnd) {
			continue
		}
		found := false
		for _, p := range prices {
			if p.Price.Meter == "egress" && p.Price.Version == row.PriceVersion && financial.EgressContractFits(p.Price, b.Egress) {
				found = true
				break
			}
		}
		if !found {
			return financial.ErrComputeUnavailable
		}
		if e.Quantity > api.FinancialComputeQuantityMax-quantity {
			return financial.ErrOverflow
		}
		quantity += e.Quantity
	}
	b.Egress.QuantityCommitted = max(b.Egress.QuantityCommitted, quantity)
	if err = computeEgressReprice(&b.Egress); err != nil {
		return err
	}
	return t.putBook(b)
}

func (m *MemStore) FinancialEgressReady(ctx context.Context, account string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[account]; !ok {
		return false, ErrNotFound
	}
	t := newMemComputeTxn(m, account)
	err := t.egressReady()
	if err == nil {
		prices, _ := t.egressPrices(financial.ComputeMonth(t.now))
		_, err = computeEgressTerms(prices, financial.ComputeMonth(t.now), t.now, financial.EgressBook{})
	}
	if errors.Is(err, financial.ErrComputeUnavailable) {
		return false, nil
	}
	return err == nil, err
}
