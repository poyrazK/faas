package billing

import (
	"math/big"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// ProjectFinancialBudget projects a budget's amount to the end of its UTC
// month from each selected meter's run-rate forecast (ADR-20261019193500).
// Gross usage scales the scope's gross amount by the forecast's
// projected-to-observed quantity. Net usage takes the account's projected net
// charge in the scope's share of the observed quantity, which is how the
// ledger allocates a net charge. It returns nil and a reason when any
// selected meter has no available forecast.
func ProjectFinancialBudget(report api.FinancialCostsResponse, spec financial.BudgetSpec) (*int64, string, error) {
	total := new(big.Int)
	for _, name := range spec.Meters {
		meter, ok := financialMeter(report, name)
		if !ok {
			return nil, name + ":missing_meter", nil
		}
		f := meter.Forecast
		if !f.Available || f.ProjectedQuantity == nil || f.ProjectedNetMillicents == nil {
			reason := f.Reason
			if reason == "" {
				reason = "forecast_unavailable"
			}
			return nil, name + ":" + reason, nil
		}
		observed := meter.Accrued.Quantity
		if observed <= 0 {
			continue // nothing used yet, so the run rate projects nothing
		}
		scopeQuantity, scopeGross := new(big.Int), new(big.Int)
		for _, contract := range meter.Accrued.Contracts {
			for _, a := range contract.Allocations {
				if spec.Scope.Matches(a.Attribution) {
					scopeQuantity.Add(scopeQuantity, big.NewInt(a.Quantity))
					scopeGross.Add(scopeGross, big.NewInt(a.GrossMillicents))
				}
			}
		}
		part := new(big.Int)
		if spec.Basis == "gross_usage" {
			part.Mul(scopeGross, big.NewInt(*f.ProjectedQuantity))
		} else {
			part.Mul(big.NewInt(*f.ProjectedNetMillicents), scopeQuantity)
		}
		total.Add(total, part.Quo(part, big.NewInt(observed)))
	}
	if !total.IsInt64() {
		return nil, "", financial.ErrOverflow
	}
	projected := total.Int64()
	return &projected, "", nil
}

func financialMeter(report api.FinancialCostsResponse, name string) (api.FinancialMeterCosts, bool) {
	for _, meter := range report.Meters {
		if meter.Meter == name {
			return meter, true
		}
	}
	return api.FinancialMeterCosts{}, false
}
