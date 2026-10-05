package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func prepareCustomerHealthReport(r *api.RouteHealthReport, opts api.RouteHealthReportOptions) error {
	if !opts.Customers {
		r.Customers = nil
		return nil
	}
	groupBy := opts.CustomerGroupBy
	if groupBy == "" {
		groupBy = "tenant"
	}
	c := r.Customers
	if c == nil || c.GroupBy != groupBy || c.Coverage != "observed_only" || c.CustomersLimit != api.RouteCustomerHealthMaxCustomers || len(c.Routes) != len(r.Routes) {
		return errors.New("missing or mismatched customer evidence")
	}
	if !opts.CustomerDetails {
		c.DetailsIncluded = false
		for i := range c.Routes {
			for j := range c.Routes[i].Customers {
				c.Routes[i].Customers[j].CustomerID = ""
			}
		}
	} else if !c.DetailsIncluded {
		return errors.New("requested customer identities unavailable")
	}
	seen := map[[2]string]bool{}
	for _, route := range c.Routes {
		key := [2]string{route.Method, route.Path}
		idx := slices.IndexFunc(r.Routes, func(f api.RouteHealthFinding) bool { return f.Method == route.Method && f.Path == route.Path })
		if seen[key] || idx < 0 || route.ObservedCustomers < int64(len(route.Customers)) || len(route.Customers) > c.CustomersLimit || route.CustomersTruncated != (route.ObservedCustomers > int64(c.CustomersLimit)) || int64(len(route.Customers)) != min(route.ObservedCustomers, int64(c.CustomersLimit)) {
			return errors.New("invalid bounded customer inventory")
		}
		seen[key] = true
		for _, attribution := range []api.RouteCustomerHealthAttribution{route.Candidate, route.Stable} {
			if attribution.IdentifiedRequests < 0 || attribution.UnattributedRequests < 0 || attribution.UnresolvedIdentityRequests < 0 || attribution.OtherCustomerRequests < 0 || attribution.OtherCustomerRequests > attribution.IdentifiedRequests || !route.CustomersTruncated && attribution.OtherCustomerRequests != 0 {
				return errors.New("invalid customer attribution")
			}
		}
		var candidate, stable int64
		identities := map[string]bool{}
		for _, customer := range route.Customers {
			if opts.CustomerDetails {
				if _, err := uuid.Parse(customer.CustomerID); err != nil || identities[customer.CustomerID] {
					return errors.New("invalid customer identity")
				}
				identities[customer.CustomerID] = true
			}
			f := customer.Health
			selected := r.Routes[idx]
			if f.Method != route.Method || f.Path != route.Path || f.CheckLatency != selected.CheckLatency || f.MaxP95MS != selected.MaxP95MS || !slices.Equal(f.WatchStatuses, selected.WatchStatuses) {
				return errors.New("customer route selectors do not match")
			}
			comparison := *r
			comparison.Customers = nil
			comparison.Routes = []api.RouteHealthFinding{f}
			comparison.Status, comparison.Reason = f.Status, f.Reason
			comparison.ClientErrorStatus, comparison.ClientErrorReason = "", ""
			if f.ClientErrors != nil {
				comparison.ClientErrorStatus, comparison.ClientErrorReason = f.ClientErrors.Status, f.ClientErrors.Reason
			}
			comparison.MinimumLatencyRequests = 0
			if routehealth.LatencyEnabled(f.CheckLatency, f.MaxP95MS) {
				comparison.MinimumLatencyRequests = api.RouteHealthMinLatencyRequests
			}
			if err := validateRouteHealthReport(comparison, r.DeploymentID); err != nil {
				return err
			}
			if err := routehealth.ValidateClientErrors(comparison); err != nil {
				return err
			}
			for _, w := range f.Windows {
				candidate += w.Candidate.Requests
				stable += w.Stable.Requests
			}
		}
		if candidate+route.Candidate.OtherCustomerRequests != route.Candidate.IdentifiedRequests || stable+route.Stable.OtherCustomerRequests != route.Stable.IdentifiedRequests {
			return errors.New("customer counts do not reconcile")
		}
		var totalCandidate, totalStable int64
		for _, w := range r.Routes[idx].Windows {
			totalCandidate += w.Candidate.Requests
			totalStable += w.Stable.Requests
		}
		if route.Candidate.IdentifiedRequests+route.Candidate.UnattributedRequests+route.Candidate.UnresolvedIdentityRequests != totalCandidate || route.Stable.IdentifiedRequests+route.Stable.UnattributedRequests+route.Stable.UnresolvedIdentityRequests != totalStable {
			return errors.New("customer attribution does not match aggregate observations")
		}
	}
	// Recompute advisory summary independently of the server's claimed verdict.
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	var recomputed api.RouteHealthReport
	if err := json.Unmarshal(body, &recomputed); err != nil {
		return err
	}
	unavailable := ""
	if r.StableDeploymentID == "" {
		unavailable = r.Reason
	}
	routehealth.EvaluateCustomers(&recomputed, unavailable)
	if c.Status != recomputed.Customers.Status {
		return errors.New("customer verdict does not match observations")
	}
	return nil
}

func renderRouteCustomerHealth(c *api.RouteCustomerHealthReport) {
	if c == nil {
		return
	}
	_, _ = fmt.Fprintf(osStdout, "\nCustomer health (%s): %s; advisory, observed telemetry only (%s)\n", c.GroupBy, c.Status, previewReportText(c.Reason))
	for _, r := range c.Routes {
		_, _ = fmt.Fprintf(osStdout, "  %s %s: %d observed customers; showing %d; truncated: %t\n", r.Method, previewReportText(r.Path), r.ObservedCustomers, len(r.Customers), r.CustomersTruncated)
		_, _ = fmt.Fprintf(osStdout, "    Candidate/stable requests without %s attribution: %d/%d; unresolved: %d/%d; outside customer limit: %d/%d\n", c.GroupBy, r.Candidate.UnattributedRequests, r.Stable.UnattributedRequests, r.Candidate.UnresolvedIdentityRequests, r.Stable.UnresolvedIdentityRequests, r.Candidate.OtherCustomerRequests, r.Stable.OtherCustomerRequests)
		for i, customer := range r.Customers {
			label := fmt.Sprintf("Customer %d (ID hidden)", i+1)
			if c.DetailsIncluded {
				label = customer.CustomerID
			}
			_, _ = fmt.Fprintf(osStdout, "    %s: %s (%s)\n", label, customer.Health.Status, previewReportText(customer.Health.Reason))
			renderRouteClientErrors(customer.Health.ClientErrors, "      ")
			for _, w := range customer.Health.Windows {
				_, _ = fmt.Fprintf(osStdout, "      %s–%s candidate %d/%d 5xx (%.1f%%), stable %d/%d (%.1f%%): %s (%s)\n", w.Start.Format("15:04:05Z"), w.End.Format("15:04:05Z"), w.Candidate.ServerErrors, w.Candidate.Requests, w.Candidate.ErrorRate*100, w.Stable.ServerErrors, w.Stable.Requests, w.Stable.ErrorRate*100, w.Status, previewReportText(w.Reason))
				if routehealth.LatencyEnabled(customer.Health.CheckLatency, customer.Health.MaxP95MS) {
					renderRouteLatencyWindow(w)
				}
			}
		}
	}
}
