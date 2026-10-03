package routehealth

import "github.com/onebox-faas/faas/pkg/api"

// EvaluateCustomers deliberately leaves the aggregate report and its decision
// unchanged. The same thresholds apply independently to each identity cohort.
func EvaluateCustomers(report *api.RouteHealthReport, unavailable string) {
	c := report.Customers
	c.Coverage, c.CustomersLimit = "observed_only", api.RouteCustomerHealthMaxCustomers
	c.Status, c.Reason = "healthy", "observed_customer_comparisons_healthy"
	if len(report.Routes) == 0 {
		c.Status, c.Reason = "disabled", "no_routes_selected"
		return
	}
	if unavailable != "" {
		c.Status, c.Reason = "unknown", unavailable
	}
	for i := range c.Routes {
		r := &c.Routes[i]
		if len(r.Customers) == 0 || r.CustomersTruncated || r.Candidate.UnattributedRequests+r.Stable.UnattributedRequests+r.Candidate.UnresolvedIdentityRequests+r.Stable.UnresolvedIdentityRequests > 0 {
			c.Status, c.Reason = combine(c.Status, c.Reason, "unknown", "customer_evidence_incomplete")
		}
		for j := range r.Customers {
			cohort := &r.Customers[j]
			for _, selected := range report.Routes {
				if selected.Method == r.Method && selected.Path == r.Path {
					cohort.Health.CheckLatency, cohort.Health.MaxP95MS = selected.CheckLatency, selected.MaxP95MS
				}
			}
			comparison := api.RouteHealthReport{Routes: []api.RouteHealthFinding{cohort.Health}}
			Evaluate(&comparison, report.ObservationAnchor, unavailable)
			cohort.Health = comparison.Routes[0]
			c.Status, c.Reason = combine(c.Status, c.Reason, cohort.Health.Status, cohort.Health.Reason)
			if !c.DetailsIncluded {
				cohort.CustomerID = ""
			}
		}
	}
}
