package state

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

func (t *memFinancialComputeTxn) allowance(month time.Time) (int64, error) {
	prices := []FinancialPriceSnapshot{}
	for _, p := range t.m.financialPrices {
		if p.AccountID == t.account {
			prices = append(prices, p)
		}
	}
	return computeAllowance(prices, month, t.now)
}

func (t *memFinancialComputeTxn) ensureAccount(policies []FinancialBudget) error {
	for _, p := range policies {
		if p.Spec.Enabled && financial.StrictAccountCompute(p.Spec) {
			b, err := t.book("", financial.ComputeMonth(t.now))
			if errors.Is(err, ErrNotFound) {
				if err = t.initializeAccount(); err != nil {
					return err
				}
				b, err = t.book("", financial.ComputeMonth(t.now))
			}
			if err != nil {
				return err
			}
			if financial.StrictAccountEgress(p.Spec) && !b.Egress.Initialized {
				return t.initializeEgress()
			}
			// Another account policy may add the egress meter.
			continue
		}
	}
	return nil
}

func (t *memFinancialComputeTxn) accountReady() bool {
	m := t.m
	since := maxComputeTime(financial.ComputeMonth(t.now), m.accounts[t.account].CreatedAt)
	if m.financialRetainedFrom.After(since) {
		return false
	}
	active, err := t.active("")
	if err != nil || len(active) != 0 {
		return false
	}
	owners := 0
	for id, n := range m.computeNodes {
		if !n.Active {
			continue
		}
		owners++
		o, ok := m.financialComputeOwners[id]
		if !ok || o.Incarnation != m.computeNodeRuntimeIncarnations[id] || t.now.Sub(o.ObservedAt) > 3*api.FinancialComputeOwnerHeartbeat {
			return false
		}
	}
	if owners == 0 {
		return false
	}
	for run, tasks := range m.jobTasks {
		if m.jobRuns[run].AccountID != t.account {
			continue
		}
		for _, task := range tasks {
			if task.Status == "claimed" {
				return false
			}
		}
	}
	for _, i := range m.instances {
		if (m.apps[i.AppID].AccountID == t.account || m.jobs[i.JobID].AccountID == t.account) && State(i.State).CountsForRAM() {
			return false
		}
	}
	for _, task := range m.appTasks {
		if m.apps[task.AppID].AccountID == t.account && (task.Status == "running" || task.Status == "restoring") {
			return false
		}
	}
	for at := since.Truncate(time.Minute); at.Before(t.now.Truncate(time.Minute)); at = at.Add(time.Minute) {
		if !m.financialSamplingWindows[at].compute {
			return false
		}
	}
	return true
}

func maxComputeTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func (t *memFinancialComputeTxn) initializeAccount() error {
	if !t.accountReady() {
		return financial.ErrComputeUnavailable
	}
	month := financial.ComputeMonth(t.now)
	b, err := t.book("", month)
	if errors.Is(err, ErrNotFound) {
		b = financial.ComputeBook{AccountID: t.account, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0)}
	} else if err != nil {
		return err
	}
	grant, err := t.allowance(month)
	if err != nil {
		return err
	}
	var quantity int64
	for _, row := range t.m.financialEvidence {
		e := row.Evidence
		if e.AccountID != t.account || e.Meter != "compute" || e.Quantity <= 0 || e.Start.Before(month) || !e.Start.Before(b.PeriodEnd) {
			continue
		}
		var price financial.Price
		for _, p := range t.m.financialPrices {
			if p.AccountID == t.account && p.Price.Meter == "compute" && p.Price.Version == row.PriceVersion && p.PeriodStart.Equal(month) {
				price = p.Price
				break
			}
		}
		if !computeAccountContractFits(price) {
			return financial.ErrComputeUnavailable
		}
		if e.Quantity > api.FinancialComputeQuantityMax-quantity {
			return financial.ErrOverflow
		}
		quantity += e.Quantity
	}
	b.QuantityCommitted = max(b.QuantityCommitted, quantity)
	b.AllowanceGranted = max(b.AllowanceGranted, grant)
	if err := computeAccountReprice(&b); err != nil {
		return err
	}
	return t.putBook(b)
}
