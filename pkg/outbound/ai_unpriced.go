package outbound

import "net/http"

// unpricedRejection refuses a billable call whose model has no price while a
// cost cap applies to it (ADR 20261027132936): the cap could not count it.
var unpricedRejection = &aiCallRejection{http.StatusForbidden, "outbound_ai_model_unpriced",
	"The model has no price, so the integration's cost caps cannot count it; add it to the integration's price list or allow unmetered calls"}

// aiCostCapApplies reports whether a cost cap counts a call: the
// integration's daily or monthly cost limit, the end-user cost limit when the
// call names an end user, a tag cost limit on one of its tags, or a trace
// cost limit when it has a trace.
func aiCostCapApplies(integration Integration, endUser string, tags []string, traceID string) bool {
	switch {
	case integration.DailyAICostLimit != nil || integration.MonthlyAICostLimit != nil:
		return true
	case endUser != "" && integration.AIUserDailyCostLimit != nil:
		return true
	case len(tagDailyCostLimits(integration, tags)) > 0:
		return true
	default:
		return traceID != "" && integration.traceLimits().cost > 0
	}
}

// aiModelPriced reports whether a call's model has a price, in the
// integration's list or the catalog.
func aiModelPriced(integration Integration, usage AIUsage) bool {
	_, ok := aiPriceFor(integration, usage.priceModel())
	return ok
}

// checkAICallPriced refuses a billable call the cost caps could not count.
// allow_unmetered lets it through outside them, as it does other calls the
// gateway cannot meter (ADR-1432).
func checkAICallPriced(integration Integration, plan *aiCallPlan, endUser string, tags []string, traceID string) *aiCallRejection {
	if plan == nil || plan.unmetered || !plan.endpoint.billable() || integration.AIAllowUnmetered ||
		!aiCostCapApplies(integration, endUser, tags, traceID) || aiModelPriced(integration, plan.reserve.estimate) {
		return nil
	}
	return unpricedRejection
}

// aiSettledCost prices a settled call. An answer whose own model has no
// price is priced as the model the call asked for. The second result is
// false when neither has a price.
func aiSettledCost(integration Integration, usage AIUsage, requestModel string) (int64, bool) {
	if price, ok := aiPriceFor(integration, usage.priceModel()); ok {
		return priceCostMillicents(price, usage), true
	}
	if requestModel != "" && requestModel != usage.Model {
		asked := usage
		asked.Model = requestModel
		if price, ok := aiPriceFor(integration, asked.priceModel()); ok {
			return priceCostMillicents(price, usage), true
		}
	}
	return 0, false
}
