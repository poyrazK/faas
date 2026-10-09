package billing

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

type appAllocationKey struct {
	attribution financial.Attribution
	unit        string
}

type appAllocationTotal struct {
	quantity *big.Int
	net      *big.Int
}

// ScopeFinancialCostsToApp projects the account report onto allocations whose
// stable application identity matches appID. It preserves account-wide source
// coverage and missing bill components while excluding invoices, unallocated
// costs, other applications, and account-level forecasts.
func ScopeFinancialCostsToApp(report api.FinancialCostsResponse, appID, appSlug string) (api.FinancialAppCostsResponse, error) {
	if appID == "" || appSlug == "" {
		return api.FinancialAppCostsResponse{}, fmt.Errorf("application identity is required")
	}
	out := api.FinancialAppCostsResponse{
		AppID:                 appID,
		AppSlug:               appSlug,
		Currency:              report.Currency,
		PeriodStart:           report.PeriodStart,
		PeriodEnd:             report.PeriodEnd,
		AsOf:                  report.AsOf,
		Meters:                make([]api.FinancialAppMeterCosts, 0, len(report.Meters)),
		Scope:                 "application_attributed_retained_compute_and_interface_egress",
		ScopeDescription:      "Only retained usage allocations whose stable app ID matches this app. Net costs include the app's allocated share of the account's included usage allowance. Separately attributed jobs and account-level charges are excluded. Meter coverage describes account-wide sampling windows.",
		BillEstimateAvailable: false,
		MissingBillComponents: append([]string(nil), report.MissingBillComponents...),
	}
	known := new(big.Int)
	for _, meter := range report.Meters {
		view := api.FinancialAppMeterCosts{
			Meter:       meter.Meter,
			Coverage:    meter.Coverage,
			Allocations: []api.FinancialAppCostAllocation{},
		}
		totals := make(map[appAllocationKey]*appAllocationTotal)
		meterQuantity, meterNet := new(big.Int), new(big.Int)
		for _, contract := range meter.Accrued.Contracts {
			if view.Unit == "" {
				view.Unit = contract.Price.Unit
			}
			for _, allocation := range contract.Allocations {
				if allocation.Attribution.AppID != appID {
					continue
				}
				key := appAllocationKey{attribution: allocation.Attribution, unit: contract.Price.Unit}
				total := totals[key]
				if total == nil {
					total = &appAllocationTotal{quantity: new(big.Int), net: new(big.Int)}
					totals[key] = total
				}
				total.quantity.Add(total.quantity, big.NewInt(allocation.Quantity))
				total.net.Add(total.net, big.NewInt(allocation.NetMillicents))
			}
		}
		for key, total := range totals {
			if !total.quantity.IsInt64() || !total.net.IsInt64() {
				return api.FinancialAppCostsResponse{}, fmt.Errorf("application financial allocation exceeds supported range")
			}
			view.Allocations = append(view.Allocations, api.FinancialAppCostAllocation{
				Attribution:   key.attribution,
				Unit:          key.unit,
				Quantity:      total.quantity.Int64(),
				NetMillicents: total.net.Int64(),
			})
			meterQuantity.Add(meterQuantity, total.quantity)
			meterNet.Add(meterNet, total.net)
		}
		if !meterQuantity.IsInt64() || !meterNet.IsInt64() {
			return api.FinancialAppCostsResponse{}, fmt.Errorf("application financial meter total exceeds supported range")
		}
		view.Quantity, view.NetMillicents = meterQuantity.Int64(), meterNet.Int64()
		sort.Slice(view.Allocations, func(i, j int) bool {
			a, b := view.Allocations[i], view.Allocations[j]
			if a.NetMillicents != b.NetMillicents {
				return a.NetMillicents > b.NetMillicents
			}
			if a.Attribution.Name != b.Attribution.Name {
				return a.Attribution.Name < b.Attribution.Name
			}
			if a.Attribution.DeploymentID != b.Attribution.DeploymentID {
				return a.Attribution.DeploymentID < b.Attribution.DeploymentID
			}
			return a.Unit < b.Unit
		})
		known.Add(known, big.NewInt(view.NetMillicents))
		out.Meters = append(out.Meters, view)
	}
	if !known.IsInt64() {
		return api.FinancialAppCostsResponse{}, fmt.Errorf("application known usage exceeds supported range")
	}
	out.KnownUsageMillicents = known.Int64()
	return out, nil
}
