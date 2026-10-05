package main

import (
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

func renderPreviewCustomers(w io.Writer, report previewRouteReport, markdown bool) {
	if markdown {
		_, _ = fmt.Fprint(w, "\n### Observed customer exposure\n\n")
	} else {
		_, _ = fmt.Fprint(w, "\nObserved customer exposure\n")
	}
	evidence := report.Customers
	_, _ = fmt.Fprintf(w, "%s (%s)\n", previewSourceDisplay(evidence.Status, markdown), previewSourceDisplay(evidence.Reason, markdown))
	if evidence.Status != "advisory" {
		return
	}
	_, _ = fmt.Fprintf(w, "Baseline deployment: %s; window [%s, %s); coverage: %s\n", previewSourceDisplay(evidence.DeploymentID, markdown), previewSourceDisplay(evidence.From, markdown), previewSourceDisplay(evidence.Until, markdown), evidence.Coverage)
	if evidence.WindowClamped || evidence.RoutesTruncated {
		_, _ = fmt.Fprintf(w, "Window clamped: %t; route inventory truncated: %t\n", evidence.WindowClamped, evidence.RoutesTruncated)
	}
	_, _ = fmt.Fprintln(w, "Observed usage identifies possible exposure; it does not establish that a client will break or that an unobserved route is unused.")
	if markdown {
		_, _ = fmt.Fprint(w, "\n| Route | Evidence | Consumers / tenants | Requests: identified / anonymous / unresolved | Last observed |\n|---|---|---|---|---|\n")
	}
	for _, route := range report.Routes {
		impact := route.CustomerImpact
		if impact == nil {
			continue
		}
		label := previewSourceDisplay(previewReportRouteKey(route.Method, route.Path), markdown)
		counts, requests, last := "unavailable", "unavailable", "unavailable"
		if row := impact.Usage; row != nil {
			counts = fmt.Sprintf("%d / %d (overlapping)", row.ConsumerCount, row.PlatformTenantCount)
			requests = fmt.Sprintf("%d / %d / %d", row.IdentifiedRequests, row.AnonymousRequests, row.UnresolvedIdentityRequests)
			last = previewSourceDisplay(row.LastObservedAt, markdown)
		}
		if markdown {
			_, _ = fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n", label, impact.Status, counts, requests, last)
		} else {
			_, _ = fmt.Fprintf(w, "%s: %s; consumers / tenants %s; requests identified / anonymous / unresolved %s; last observed %s\n", label, impact.Status, counts, requests, last)
		}
	}
	if evidence.DetailsIncluded {
		for _, route := range report.Routes {
			if route.CustomerImpact != nil && route.CustomerImpact.Usage != nil {
				renderPreviewCustomerDetails(w, previewSourceDisplay(previewReportRouteKey(route.Method, route.Path), markdown), *route.CustomerImpact.Usage, markdown)
			}
		}
	}
}

func renderPreviewCustomerDetails(w io.Writer, label string, usage api.RouteCustomerUsage, markdown bool) {
	for _, customer := range usage.Customers {
		_, _ = fmt.Fprintf(w, "\n- %s: consumer %s; tenant %s; %d observed requests; last observed %s\n", label,
			previewSourceDisplay(customer.ConsumerID, markdown), previewSourceDisplay(customer.PlatformTenantID, markdown),
			customer.Requests, previewSourceDisplay(customer.LastObservedAt, markdown))
	}
	if usage.CustomersTruncated {
		_, _ = fmt.Fprintf(w, "\n- %s: customer details truncated; %d identified requests belong to omitted identity groups.\n", label, usage.OtherCustomerRequests)
	}
}
