package main

import "github.com/onebox-faas/faas/pkg/openapidiff"

func attachPreviewSecurityComparison(rows []previewReportRoute, before, after *openapidiff.Spec) previewReportEvidence {
	comparison, err := openapidiff.CompareSecurity(before, after)
	if err != nil {
		for index := range rows {
			if rows[index].Change != "added" && rows[index].Change != "removed" {
				rows[index].SecurityCompatibility = &openapidiff.SecurityRoute{Method: rows[index].Method, Path: rows[index].Path, Status: "unknown", Findings: []openapidiff.RequestFinding{{Severity: "unknown", Code: "comparison_unavailable", Location: "/security"}}}
			}
		}
		return previewReportEvidence{Status: "unavailable", Reason: "comparison_limit_or_invalid_evidence"}
	}
	byKey := map[string]openapidiff.SecurityRoute{}
	for _, route := range comparison.Routes {
		byKey[previewReportRouteKey(route.Method, route.Path)] = route
	}
	for index := range rows {
		row := &rows[index]
		result, found := byKey[previewReportRouteKey(row.Method, row.Path)]
		if !found {
			continue
		}
		row.SecurityCompatibility = &result
		if result.Changed && row.Change == "unchanged" {
			row.Change = "changed"
		}
	}
	return previewReportEvidence{Status: "available", Reason: "bounded_declared_security_comparison"}
}

func previewReportHasSecurityRegressions(report previewRouteReport) bool {
	for _, row := range report.Routes {
		if row.SecurityCompatibility != nil && row.SecurityCompatibility.Regression {
			return true
		}
	}
	return false
}
func previewReportHasSecurityBreaks(report previewRouteReport) bool {
	for _, row := range report.Routes {
		if row.SecurityCompatibility != nil && row.SecurityCompatibility.ClientBreaking {
			return true
		}
	}
	return false
}

func finishPreviewSecurityComparison(report *previewRouteReport) {
	incomplete := report.Security.Status == "unavailable"
	for index := range report.Routes {
		row := &report.Routes[index]
		security := row.SecurityCompatibility
		if security == nil || security.Status == "not_compared" {
			continue
		}
		if security.Regression {
			row.NextActions = append(row.NextActions, "Review the broadened declared authentication requirements; confirm intended access and actual enforcement before release.")
		}
		if security.ClientBreaking {
			row.NextActions = append(row.NextActions, "Preserve previously supported credentials and scopes or coordinate a client migration; inspect the security findings.")
		}
		if !security.Complete {
			incomplete = true
			row.NextActions = append(row.NextActions, "Review unresolved security requirements and credential definitions in the original captured contracts.")
		}
	}
	if report.Outcome != "no_findings" && report.Outcome != "incomplete" && report.Outcome != "review_required" {
		return
	}
	switch {
	case previewReportHasSecurityRegressions(*report):
		report.Outcome = "security_regressions"
	case previewReportHasSecurityBreaks(*report):
		report.Outcome = "security_client_breaking_changes"
	case incomplete:
		report.Outcome = "incomplete"
	}
}
