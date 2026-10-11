package routemonitor

import (
	"errors"
	"math/big"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

func initializeCustomers(r *api.RouteMonitorReport) {
	if r.CustomerGroupBy == "" {
		return
	}
	r.Customers = &api.RouteMonitorCustomerReport{GroupBy: r.CustomerGroupBy, DetailsIncluded: true, Routes: []api.RouteMonitorCustomerRoute{}}
	for _, f := range r.Routes {
		c := api.RouteMonitorCustomerRoute{Method: f.Route.Method, Path: f.Route.Path, Customers: []api.RouteMonitorCustomerCohort{}, Windows: []api.RouteMonitorCustomerWindow{}}
		for _, w := range f.Windows {
			c.Windows = append(c.Windows, api.RouteMonitorCustomerWindow{Start: w.Start, End: w.End})
		}
		r.Customers.Routes = append(r.Customers.Routes, c)
	}
}

func evaluateCustomers(r *api.RouteMonitorReport, unavailable string) {
	c := r.Customers
	if c == nil {
		return
	}
	c.Coverage, c.CustomersLimit = "observed_only", api.RouteMonitorCustomersPerRoute
	c.Status, c.Reason = "healthy", "observed_customer_budgets_satisfied"
	if unavailable != "" {
		c.Status, c.Reason = "unknown", unavailable
	}
	if c.RecoveryInventoryIncomplete {
		c.Status, c.Reason = "unknown", "recovery_customer_inventory_incomplete"
	}
	for j := range c.Routes {
		cr := &c.Routes[j]
		if cr.ObservedCustomers == 0 || cr.UnknownCustomers > 0 {
			c.Status = combine(c.Status, "unknown")
			c.Reason = "customer_evidence_incomplete"
		}
		if cr.RecoveryRemainingCustomers > 0 {
			c.Status = combine(c.Status, "unknown")
			c.Reason = "recovery_customers_not_healthy"
		}
		if cr.RecoveryMissingCustomers > 0 {
			c.Status = combine(c.Status, "unknown")
			c.Reason = "recovery_customers_unobserved"
		}
		for _, w := range cr.Windows {
			if w.UnattributedRequests > 0 || w.UnresolvedIdentityRequests > 0 {
				c.Status = combine(c.Status, "unknown")
				c.Reason = "customer_attribution_incomplete"
			}
		}
		for k := range cr.Customers {
			cohort := &cr.Customers[k]
			f := api.RouteMonitorFinding{Route: r.Routes[j].Route, Windows: cohort.Windows}
			evaluateFinding(&f, r.ObservationAnchor, unavailable)
			cohort.Windows, cohort.Status, cohort.Reason, cohort.ErrorStatus, cohort.LatencyStatus = f.Windows, f.Status, f.Reason, f.ErrorStatus, f.LatencyStatus
		}
		if cr.ViolatedCustomers > 0 {
			c.Status = combine(c.Status, "violated")
		}
	}
	if c.Status == "violated" {
		c.Reason = "sustained_customer_budget_violation"
	}
	if c.Status == "healthy" {
		c.Reason = "observed_customer_budgets_satisfied"
	}
	if !r.Enabled {
		c.Status, c.Reason = "disabled", "monitor_disabled"
		return
	}
	previous := r.Status
	r.Status = combine(r.Status, c.Status)
	if previous != "violated" && c.Status == "violated" {
		r.Reason = "sustained_customer_budget_violation"
	}
	if previous == "healthy" && c.Status == "unknown" {
		r.Reason = c.Reason
	}
}

func cloneCustomers(c *api.RouteMonitorCustomerReport) *api.RouteMonitorCustomerReport {
	if c == nil {
		return nil
	}
	out := *c
	out.Routes = slices.Clone(c.Routes)
	for j := range out.Routes {
		r := &out.Routes[j]
		r.Windows = slices.Clone(r.Windows)
		r.ViolatingCustomerIDs = slices.Clone(r.ViolatingCustomerIDs)
		r.Customers = slices.Clone(r.Customers)
		for k := range r.Customers {
			r.Customers[k].Windows = slices.Clone(r.Customers[k].Windows)
		}
	}
	return &out
}

// ProjectReport copies before hiding identity fields; worker and saved evidence
// always retain original request-time identities for comparable recovery.
func ProjectReport(r api.RouteMonitorReport, details bool) api.RouteMonitorReport {
	r.Customers = cloneCustomers(r.Customers)
	if r.Customers == nil {
		return r
	}
	r.Customers.DetailsIncluded = details
	if !details {
		for j := range r.Customers.Routes {
			r.Customers.Routes[j].ViolatingCustomerIDs = nil
			for k := range r.Customers.Routes[j].Customers {
				r.Customers.Routes[j].Customers[k].CustomerID = ""
			}
		}
	}
	return r
}
func ProjectIncident(i api.RouteMonitorIncident, details bool) api.RouteMonitorIncident {
	EnsureIncidentTimeline(&i)
	i.OpeningReport = ProjectReport(i.OpeningReport, details)
	if i.RecoveryReport != nil {
		r := ProjectReport(*i.RecoveryReport, details)
		i.RecoveryReport = &r
	}
	if !details {
		for j := range i.Evidence {
			i.Evidence[j].CustomerID = ""
		}
		for j := range i.Escalations {
			for k := range i.Escalations[j].Evidence {
				i.Escalations[j].Evidence[k].CustomerID = ""
			}
		}
	}
	return i
}

func validateCustomers(r api.RouteMonitorReport) error {
	c := r.Customers
	if r.CustomerGroupBy == "" {
		if c != nil {
			return errors.New("customer evidence without selected dimension")
		}
		return nil
	}
	if r.CustomerGroupBy != "tenant" && r.CustomerGroupBy != "consumer" || c == nil || c.GroupBy != r.CustomerGroupBy || c.Coverage != "observed_only" || c.CustomersLimit != api.RouteMonitorCustomersPerRoute || len(c.Routes) != len(r.Routes) {
		return errors.New("invalid customer monitor scope or bounds")
	}
	var observed, violated, unknown, recoveryRemaining int64
	for j, cr := range c.Routes {
		f := r.Routes[j]
		if cr.Method != f.Route.Method || cr.Path != f.Route.Path || cr.ObservedCustomers < 0 || cr.ViolatedCustomers < 0 || cr.UnknownCustomers < 0 || cr.RecoveryMissingCustomers < 0 || cr.RecoveryRemainingCustomers < cr.ViolatedCustomers || !sumEquals(cr.ObservedCustomers, cr.ViolatedCustomers, cr.UnknownCustomers, cr.ObservedCustomers-cr.ViolatedCustomers-cr.UnknownCustomers) || cr.ViolatedCustomers > cr.ObservedCustomers-cr.UnknownCustomers || len(cr.Customers) > api.RouteMonitorCustomersPerRoute || len(cr.Windows) != len(f.Windows) || cr.CustomersTruncated != (cr.ObservedCustomers+cr.RecoveryMissingCustomers > int64(len(cr.Customers))) || cr.ViolatingCustomersTruncated != (cr.ViolatedCustomers > api.RouteMonitorRecoveryCustomersPerRoute) {
			return errors.New("invalid customer route inventory")
		}
		if c.DetailsIncluded {
			if len(cr.ViolatingCustomerIDs) != int(min(cr.ViolatedCustomers, api.RouteMonitorRecoveryCustomersPerRoute)) {
				return errors.New("invalid recovery identity inventory")
			}
		} else if len(cr.ViolatingCustomerIDs) > 0 {
			return errors.New("customer identities present without details")
		}
		seen := map[string]bool{}
		for _, id := range cr.ViolatingCustomerIDs {
			if !validUUID(id) || seen[id] {
				return errors.New("invalid recovery identity")
			}
			seen[id] = true
		}
		seen = map[string]bool{}
		var displayedViolations, displayedUnknown, displayedMissing int64
		for _, cohort := range cr.Customers {
			if len(cohort.Windows) != len(f.Windows) || c.DetailsIncluded && (!validUUID(cohort.CustomerID) || seen[cohort.CustomerID]) || !c.DetailsIncluded && cohort.CustomerID != "" {
				return errors.New("invalid cohort identity or windows")
			}
			seen[cohort.CustomerID] = true
			positive := false
			for k, w := range cohort.Windows {
				if !w.Start.Equal(f.Windows[k].Start) || !w.End.Equal(f.Windows[k].End) || w.Observed.Requests < 0 || w.Observed.ServerErrors < 0 || w.Observed.ServerErrors > w.Observed.Requests || w.Observed.ServerErrors > f.Windows[k].Observed.ServerErrors {
					return errors.New("invalid customer observations")
				}
				positive = positive || w.Observed.Requests > 0
				// Use the aggregate count validator without creating another verdict.
				if !validMonitorCounts(w.Observed) {
					return errors.New("invalid customer rates or latency")
				}
			}
			if cohort.Observed != positive {
				return errors.New("invalid observed cohort flag")
			}
			if !positive {
				displayedMissing++
			} else if cohort.Status == "violated" {
				displayedViolations++
			} else if cohort.Status == "unknown" {
				displayedUnknown++
			}
		}
		if displayedViolations > cr.ViolatedCustomers || displayedUnknown > cr.UnknownCustomers || displayedMissing > cr.RecoveryMissingCustomers {
			return errors.New("customer verdict totals do not reconcile")
		}
		for k, w := range cr.Windows {
			if !w.Start.Equal(f.Windows[k].Start) || !w.End.Equal(f.Windows[k].End) || w.IdentifiedRequests < 0 || w.UnattributedRequests < 0 || w.UnresolvedIdentityRequests < 0 || w.OtherCustomerRequests < 0 || !sumEquals(f.Windows[k].Observed.Requests, w.IdentifiedRequests, w.UnattributedRequests, w.UnresolvedIdentityRequests) {
				return errors.New("customer attribution does not reconcile")
			}
			parts := []int64{w.OtherCustomerRequests}
			for _, cohort := range cr.Customers {
				parts = append(parts, cohort.Windows[k].Observed.Requests)
			}
			if !sumEquals(w.IdentifiedRequests, parts...) {
				return errors.New("bounded customer requests do not reconcile")
			}
		}
		observed += cr.ObservedCustomers
		violated += cr.ViolatedCustomers
		unknown += cr.UnknownCustomers
		recoveryRemaining += cr.RecoveryRemainingCustomers
		if c.ObservedCustomers < cr.ObservedCustomers || c.ViolatedCustomers < cr.ViolatedCustomers || c.UnknownCustomers < cr.UnknownCustomers {
			return errors.New("distinct customer totals below route totals")
		}
	}
	if c.ObservedCustomers < 0 || c.ObservedCustomers > observed || c.ViolatedCustomers < 0 || c.ViolatedCustomers > violated || c.ViolatedCustomers > c.ObservedCustomers || c.UnknownCustomers < 0 || c.UnknownCustomers > unknown || c.UnknownCustomers > c.ObservedCustomers || c.RecoveryRemainingCustomers < c.ViolatedCustomers || c.RecoveryRemainingCustomers > recoveryRemaining {
		return errors.New("invalid distinct customer totals")
	}
	return nil
}
func sumEquals(total int64, parts ...int64) bool {
	sum := new(big.Int)
	for _, p := range parts {
		if p < 0 {
			return false
		}
		sum.Add(sum, big.NewInt(p))
	}
	return sum.Cmp(big.NewInt(total)) == 0
}
