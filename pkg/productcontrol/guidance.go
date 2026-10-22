package productcontrol

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// UsageGuidance never includes prices, outcomes, operation bindings, or roles.
// Roles with neither run nor billing access get no feature usage projection.
func UsageGuidance(account, app string, customer api.ProductCustomer, catalog, head api.ProductCatalog, permissions api.ProductPermissions, usage *api.ProductUsage, bindings map[string]api.ProductResolvedBinding, now time.Time) (api.ProductUsageGuidance, error) {
	now = now.UTC()
	out := api.ProductUsageGuidance{AccountID: account, AppID: app, TenantID: customer.TenantID, Revision: customer.Revision, PolicyRevision: head.Revision, ObservedAt: now, Period: Period(now), ResetsAt: time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC), NearLimitPercent: api.ProductControlGuidanceNearLimitPercent, RunPermission: permissions.RunWork, Suspended: customer.Suspended, PlanAction: guidancePlanAction(customer, head, permissions), Features: []api.ProductFeatureUsageGuidance{}}
	if !permissions.RunWork && !permissions.ViewBilling {
		return out, nil
	}
	plan, ok := catalog.Spec.Plans[customer.Plan]
	if !ok || catalog.Revision != customer.Revision || len(plan.Features) > api.ProductControlMaxFeatures || usage == nil || usage.Period != out.Period {
		return out, fmt.Errorf("usage guidance does not match the assigned plan and current UTC month")
	}
	starts := map[string]int64{}
	for _, line := range usage.Lines {
		if line.Starts < 0 || starts[line.Feature] > math.MaxInt64-line.Starts {
			return out, fmt.Errorf("invalid usage guidance admission count")
		}
		starts[line.Feature] += line.Starts
	}
	for name, grant := range plan.Features {
		if grant.MonthlyStarts < 0 || grant.MonthlyStarts > api.ProductControlMaxMonthlyStarts {
			return out, fmt.Errorf("invalid usage guidance allowance")
		}
		row := api.ProductFeatureUsageGuidance{Feature: name, Label: catalog.Spec.Features[name].Label, Limit: grant.MonthlyStarts, Starts: starts[name], Remaining: max(0, grant.MonthlyStarts-starts[name])}
		row.AllowanceState = guidanceAllowanceState(row.Starts, row.Limit)
		row.State = guidanceFeatureState(row, permissions.RunWork, customer.Suspended, bindings[name].Available)
		out.Features = append(out.Features, row)
	}
	sort.Slice(out.Features, func(i, j int) bool { return out.Features[i].Feature < out.Features[j].Feature })
	return out, nil
}

// HoldGuidance marks guidance for a customer held for an overdue invoice
// (ADR-20261018212918). A suspended workspace or a role that cannot run keeps
// its own state; every other feature waits for the invoice to be paid.
func HoldGuidance(g *api.ProductUsageGuidance) {
	g.InvoiceOverdue = true
	for i := range g.Features {
		if state := g.Features[i].State; state != "access_paused" && state != "permission_required" {
			g.Features[i].State = "invoice_overdue"
		}
	}
}

func guidanceAllowanceState(starts, limit int64) string {
	switch {
	case limit == 0:
		return "no_allowance"
	case starts >= limit:
		return "exhausted"
	case starts*100 >= limit*api.ProductControlGuidanceNearLimitPercent:
		return "near_limit"
	default:
		return "available"
	}
}

func guidanceFeatureState(row api.ProductFeatureUsageGuidance, run, suspended, binding bool) string {
	switch {
	case suspended:
		return "access_paused"
	case !run:
		return "permission_required"
	case !binding:
		return "temporarily_unavailable"
	case row.Remaining == 0:
		return "allowance_exhausted"
	case row.AllowanceState == "near_limit":
		return "near_limit"
	default:
		return "available"
	}
}

func guidancePlanAction(customer api.ProductCustomer, head api.ProductCatalog, permissions api.ProductPermissions) string {
	if customer.Suspended {
		return "contact_product_team"
	}
	if !permissions.ManagePlan {
		return "ask_workspace_admin"
	}
	if head.Spec.SelfService != nil {
		for _, target := range head.Spec.SelfService.Transitions[customer.Plan] {
			if _, exists := head.Spec.Plans[target]; exists && (target != customer.Plan || customer.Revision != head.Revision) {
				return "review_plans"
			}
		}
	}
	return "contact_product_team"
}
