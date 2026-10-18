package main

import (
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

func productPilotLineKey(tenant, period string, line api.ProductBillingLine) string {
	return tenant + "/" + period + "/" + line.Feature + "/" + line.Revision + "/" + line.Plan + "/" + strconv.FormatInt(line.UnitPriceMillicents, 10)
}

// A single aggregate success cannot cover several referenced operations. Use
// the last read of each month, compare minimum coverage for all selected cases,
// and make no claim that this selected set is the customer's entire activity.
func verifyProductPilotCoverage(r *api.ProductPilotReport) {
	required := map[string]api.ProductBillingLine{}
	for _, view := range r.Operations {
		e := view.Evidence
		p := e.Receipt
		line := api.ProductBillingLine{Feature: p.Feature, Revision: p.Revision, Plan: p.Plan, UnitPriceMillicents: p.UnitPriceMillicents}
		key := productPilotLineKey(e.TenantID, p.Period, line)
		count := required[key]
		count.Starts++
		switch {
		case e.WaivedAt != nil:
			count.Waived++
		case e.ResolvedState == api.OperationSucceeded:
			count.Succeeded++
			count.AmountMillicents += p.UnitPriceMillicents
		case e.ResolvedState == api.OperationFailed:
			count.Failed++
		case e.ResolvedState == api.OperationCancelled:
			count.Cancelled++
		default:
			count.Unresolved++
		}
		required[key] = count
	}
	latest := map[string]api.ProductBillingReview{}
	for _, review := range r.Reviews {
		latest[review.TenantID+"/"+review.Period] = review
	}
	matched := map[string]bool{}
	for _, review := range latest {
		for _, line := range review.Lines {
			key := productPilotLineKey(review.TenantID, review.Period, line)
			need, ok := required[key]
			if !ok {
				continue
			}
			matched[key] = line.Starts >= need.Starts && line.Succeeded >= need.Succeeded && line.Failed >= need.Failed && line.Cancelled >= need.Cancelled && line.Waived >= need.Waived && line.Unresolved >= need.Unresolved && line.AmountMillicents >= need.AmountMillicents
		}
	}
	status, code := "observed", "selected_receipts_covered"
	for key := range required {
		if !matched[key] {
			status, code = "blocked", "selected_receipts_not_covered"
			r.LedgerState = "blocked"
		}
	}
	r.Checks = append(r.Checks, api.ProductDoctorCheck{Check: "billing_coverage", Status: status, Impact: "ledger", Code: code, Message: "Latest month projections cover the counts and charges of all referenced receipts; concurrent changes can require another observation. No total is inferred from the selected cases."})
}
