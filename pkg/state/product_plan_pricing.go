package state

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

type productPlanStartCounts struct{ total, trial int64 }

type productPlanHistory struct {
	held    []productcontrol.PlanHeld
	catalog func(string) (api.ProductCatalog, bool)
}

// productAssignmentTrial mirrors the store's assignment rule: an existing
// trial is retained, or the first assignment to a trial offer starts one.
func productAssignmentTrial(customer, prior api.ProductCustomer, days int64, now time.Time) api.ProductCustomer {
	customer.TrialStartsAt, customer.TrialEndsAt = prior.TrialStartsAt, prior.TrialEndsAt
	if customer.TrialStartsAt == nil && days > 0 {
		end := now.AddDate(0, 0, int(days))
		customer.TrialStartsAt, customer.TrialEndsAt = &now, &end
	}
	return customer
}

func (h productPlanHistory) pricing(before, after api.ProductCustomer, current, target api.ProductCatalog, starts map[string]productPlanStartCounts, now time.Time) *api.ProductPlanPricing {
	return h.pricingAt(before, after, current, target, starts, now, now)
}

func (h productPlanHistory) pricingAt(before, after api.ProductCustomer, current, target api.ProductCatalog, starts map[string]productPlanStartCounts, now, effective time.Time) *api.ProductPlanPricing {
	period := productcontrol.Period(now)
	_, next := productMonthBounds(period)
	pricing := &api.ProductPlanPricing{NextPeriod: productcontrol.Period(next), Runs: []api.ProductPlanRunPrice{}}
	oldPlan, newPlan := current.Spec.Plans[before.Plan], target.Spec.Plans[after.Plan]
	after = productAssignmentTrial(after, before, newPlan.TrialDays, effective)
	after.PaymentMethodAt, after.TrialResolvedAt = before.PaymentMethodAt, before.TrialResolvedAt
	pricing.ThisMonthBeforeMillicents = productPlanFeeAmount(h.held, period, before, h.catalog)

	// Model the proposed assignment as the billing ledger would record it.
	proposed := append([]productcontrol.PlanHeld(nil), h.held...)
	if before.Plan != after.Plan || before.Revision != after.Revision {
		if len(proposed) > 0 {
			proposed[len(proposed)-1].Until = effective
		}
		proposed = append(proposed, productcontrol.PlanHeld{Revision: after.Revision, Plan: after.Plan, Since: effective, First: before.Plan == ""})
	}
	catalog := func(revision string) (api.ProductCatalog, bool) {
		if revision == target.Revision {
			return target, true
		}
		return h.catalog(revision)
	}
	pricing.ThisMonthAfterMillicents = productPlanFeeAmount(proposed, period, after, catalog)
	pricing.NextMonthAfterMillicents = productPlanFeeAmount(proposed, pricing.NextPeriod, after, catalog)
	for _, feature := range productPlanFeatureNames(oldPlan, newPlan) {
		count := starts[feature]
		pricing.Runs = append(pricing.Runs, api.ProductPlanRunPrice{
			Feature: feature, Starts: count.total,
			BeforeMillicents: productNextRunPrice(before, oldPlan, feature, count, effective),
			AfterMillicents:  productNextRunPrice(after, newPlan, feature, count, effective),
		})
	}
	return pricing
}

func productPlanFeeAmount(held []productcontrol.PlanHeld, period string, customer api.ProductCustomer, catalog func(string) (api.ProductCatalog, bool)) int64 {
	fee := productMonthFee(held, period, productTrial(customer), catalog)
	if fee == nil {
		return 0
	}
	return fee.AmountMillicents
}

func productPlanFeatureNames(plans ...api.ProductPlan) []string {
	names := map[string]bool{}
	for _, plan := range plans {
		for name := range plan.Features {
			names[name] = true
		}
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func productNextRunPrice(customer api.ProductCustomer, plan api.ProductPlan, feature string, count productPlanStartCounts, now time.Time) *int64 {
	grant, ok := plan.Features[feature]
	if !ok || customer.Suspended || customer.AwaitsPaymentMethod(plan, now) || count.total >= grant.MonthlyStarts {
		return nil
	}
	price, _, _ := productcontrol.StartPrice(grant, customer, count.total-count.trial, now)
	return &price
}
