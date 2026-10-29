package billing

import "github.com/onebox-faas/faas/pkg/api"

// ProjectedSubtotal is the month's plan fee plus every meter's projected net
// usage charge, in millicents (issue #264). It is nil unless every meter has
// an available forecast: a subtotal over some meters would read as the
// month's spend while leaving cost out. Credits, tax, add-ons and external
// meters are not in it; the response keeps listing them as missing, so it is
// never presented as the bill.
func ProjectedSubtotal(plan api.Plan, meters []api.FinancialMeterCosts) *int64 {
	limits, ok := api.LimitsFor(plan)
	if !ok || len(meters) == 0 {
		return nil
	}
	total := limits.PriceMillicents
	for _, meter := range meters {
		f := meter.Forecast
		if !f.Available {
			return nil
		}
		switch {
		case f.ProjectedNetMillicents != nil:
			total += *f.ProjectedNetMillicents
		case f.ProjectedGrossMillicents != nil:
			total += *f.ProjectedGrossMillicents
		default:
			return nil
		}
	}
	return &total
}

// ForecastMissingComponents is the costs report's missing-component list as
// the forecast states it: once the subtotal carries the plan fee, only
// add-ons remain of "subscription_and_addons".
func ForecastMissingComponents(missing []string, subtotal *int64) []string {
	out := make([]string, 0, len(missing))
	for _, component := range missing {
		if component == "subscription_and_addons" && subtotal != nil {
			component = "addons"
		}
		out = append(out, component)
	}
	return out
}
