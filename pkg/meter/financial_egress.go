// adr: 1748 — provider delivery cannot outrun the funded meter contract.
package meter

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func (p *Pusher) checkFundedEgressDelivery(ctx context.Context, acct state.Account, window state.BillingMeterWindow, policy billing.MeterUsagePolicy, netTotal int64) error {
	if window.Meter != state.BillingMeterEgress {
		return nil
	}
	authority, ok := p.store.(state.FinancialComputeStore)
	if !ok {
		return nil
	} // Such adapters cannot activate strict funding.
	// This read-only projection also retains books after policy deletion. No
	// synthetic customer intent is persisted and no money is minted here.
	scope := state.FinancialBudget{AccountID: acct.ID, Spec: financial.BudgetSpec{Name: "funded egress delivery audit", Currency: financial.BillingCurrency, ResumeRule: "manual", Scope: financial.BudgetScope{Kind: "account"}, Basis: "net_usage", Mode: "strict", Action: "suspend_workloads", Meters: []string{"compute", "egress"}}}
	balance, err := authority.FinancialComputeBalance(ctx, scope, window.Hour)
	if err != nil {
		return err
	}
	b := balance.Egress
	if b == nil || !b.Initialized {
		return nil
	}
	policies, err := authority.FinancialComputePolicies(ctx, acct.ID, "")
	if err != nil {
		return err
	}
	capped := false
	for _, policy := range policies {
		capped = capped || policy.DeletedAt == nil && policy.Spec.Enabled && financial.StrictAccountEgress(policy.Spec)
	}
	// Once disabled, ordinary unbudgeted workloads can add legitimate usage.
	// Period pricing stays pinned; the byte ceiling applies to active caps.
	if netTotal < 0 || policy.IncludedQuantity < b.AllowanceGranted || (capped && netTotal > max(int64(0), b.CommittedBytes+b.ReservedBytes-policy.IncludedQuantity)) {
		return financial.ErrComputeUnavailable
	}
	retained, ok := p.store.(state.FinancialStore)
	if !ok {
		return financial.ErrComputeUnavailable
	}
	prices, err := retained.ListFinancialPriceSnapshots(ctx, acct.ID, financial.ComputeMonth(window.Hour))
	if err != nil {
		return err
	}
	var ceiling financial.EgressBook
	for _, row := range prices {
		rate := row.Price
		// Pre-switch snapshots in the legacy currency cannot bound a USD rate.
		if rate.Meter != "egress" || rate.Currency != financial.BillingCurrency {
			continue
		}
		if ceiling.RateQuantity == 0 || !financial.EgressContractFits(rate, ceiling) {
			ceiling.RateMillicents = rate.MillicentsPerUnit
			ceiling.RateQuantity = rate.UnitQuantity
		}
	}
	configured := financial.Price{Version: "provider-delivery", Meter: "egress", Currency: financial.BillingCurrency, Unit: "interface_bytes", UnitQuantity: policy.UnitQuantity, MillicentsPerUnit: policy.MillicentsPerUnit}
	if !financial.EgressContractFits(configured, ceiling) {
		return errors.Join(financial.ErrComputeUnavailable, errors.New("provider egress rate exceeds funded period ceiling"))
	}
	return nil
}
