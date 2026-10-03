package main

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type previewReportCustomerEvidence struct {
	previewReportEvidence
	DeploymentID    string `json:"deployment_id,omitempty"`
	From            string `json:"from,omitempty"`
	Until           string `json:"until,omitempty"`
	Coverage        string `json:"coverage,omitempty"`
	WindowClamped   bool   `json:"window_clamped,omitempty"`
	RoutesTruncated bool   `json:"routes_truncated,omitempty"`
	DetailsIncluded bool   `json:"details_included"`
}

type previewRouteCustomerImpact struct {
	Status string                  `json:"status"`
	Usage  *api.RouteCustomerUsage `json:"usage,omitempty"`
}

func collectPreviewCustomerUsage(ctx context.Context, client *api.Client, report *previewRouteReport, since string, details bool) {
	report.Customers = previewReportCustomerEvidence{previewReportEvidence: previewReportEvidence{Status: "unavailable", Reason: "baseline_deployment_missing"}}
	if id, err := uuid.Parse(report.BaselineDeployment); err != nil || id == uuid.Nil {
		return
	}
	response, err := client.GetAppRouteCustomerUsage(ctx, report.Parent, api.RouteCustomerUsageOptions{
		DeploymentID: report.BaselineDeployment, Since: since, Until: report.GeneratedAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		report.Customers.Reason = previewReportReadReason(err)
		return
	}
	attachPreviewCustomerUsage(report, response, details)
}

func attachPreviewCustomerUsage(report *previewRouteReport, response api.RouteCustomerUsageResponse, details bool) {
	for i := range report.Routes {
		report.Routes[i].CustomerImpact = nil
	}
	report.Customers = previewReportCustomerEvidence{previewReportEvidence: previewReportEvidence{Status: "unavailable", Reason: "customer_evidence_unbound"}}
	from, fromErr := time.Parse(time.RFC3339Nano, response.From)
	until, untilErr := time.Parse(time.RFC3339Nano, response.Until)
	if response.Slug != report.Parent || response.DeploymentID != report.BaselineDeployment || report.BaselineDeployment == "" ||
		response.Coverage != "observed_only" || fromErr != nil || untilErr != nil || !from.Before(until) ||
		(!report.GeneratedAt.IsZero() && !until.Equal(report.GeneratedAt)) {
		return
	}
	report.Customers = previewReportCustomerEvidence{
		previewReportEvidence: previewReportEvidence{Status: "advisory", Reason: "baseline_observed_exposure"},
		DeploymentID:          response.DeploymentID, From: response.From, Until: response.Until, Coverage: response.Coverage,
		WindowClamped: response.WindowClamped, RoutesTruncated: response.RoutesTruncated, DetailsIncluded: details,
	}
	rows := map[string][]api.RouteCustomerUsage{}
	for _, row := range response.Routes {
		path := strings.TrimPrefix(row.Route, strings.ToUpper(row.Method)+" ")
		key := previewReportRouteKey(row.Method, path)
		rows[key] = append(rows[key], row)
	}
	for i := range report.Routes {
		route := &report.Routes[i]
		if route.RouteSource != "captured_deployment_contract" {
			continue
		}
		impact := &previewRouteCustomerImpact{Status: "no_observations"}
		matches := rows[previewReportRouteKey(route.Method, route.Path)]
		if len(matches) > 1 {
			impact.Status = "ambiguous_route"
		} else if len(matches) == 1 {
			row := matches[0]
			if !details {
				row.Customers = []api.RouteCustomerObservation{}
			}
			impact.Status, impact.Usage = "observed", &row
		} else if response.RoutesTruncated {
			impact.Status = "not_in_bounded_inventory"
		}
		route.CustomerImpact = impact
	}
	report.Notes = append(report.Notes, "Customer impact is observed baseline exposure, not proof that clients will break. Consumer and tenant counts overlap. Anonymous, unresolved, sampled, expired, or missing traffic cannot establish that a route is unused; observation timestamps may be minute buckets.")
}
