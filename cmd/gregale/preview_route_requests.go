package main

import "github.com/onebox-faas/faas/pkg/openapidiff"

func attachPreviewRequestComparison(rows []previewReportRoute, before, after *openapidiff.Spec) previewReportEvidence {
	comparison, err := openapidiff.CompareRequests(before, after)
	if err != nil {
		for index := range rows {
			if rows[index].Change != "added" && rows[index].Change != "removed" {
				rows[index].RequestCompatibility = &openapidiff.RequestRoute{Method: rows[index].Method, Path: rows[index].Path, Status: "unknown", Findings: []openapidiff.RequestFinding{{Severity: "unknown", Code: "comparison_unavailable"}}}
			}
		}
		return previewReportEvidence{Status: "unavailable", Reason: "comparison_limit_or_invalid_evidence"}
	}
	byKey := map[string]openapidiff.RequestRoute{}
	for _, route := range comparison.Routes {
		byKey[previewReportRouteKey(route.Method, route.Path)] = route
	}
	for index := range rows {
		row := &rows[index]
		result, found := byKey[previewReportRouteKey(row.Method, row.Path)]
		if !found {
			continue
		}
		row.RequestCompatibility = &result
		row.RequestContractChanged = result.Changed
		if result.Changed && row.Change == "unchanged" {
			row.Change = "changed"
		}
	}
	return previewReportEvidence{Status: "available", Reason: "bounded_declared_input_comparison"}
}

func previewReportHasRequestBreaks(report previewRouteReport) bool {
	for _, row := range report.Routes {
		if row.RequestCompatibility != nil && row.RequestCompatibility.Status == "breaking" {
			return true
		}
	}
	return false
}

func finishPreviewRequestComparison(report *previewRouteReport) {
	incomplete := report.Requests.Status == "unavailable"
	for index := range report.Routes {
		row := &report.Routes[index]
		request := row.RequestCompatibility
		if request == nil || request.Status == "not_compared" {
			continue
		}
		if request.Status == "breaking" {
			row.NextActions = append(row.NextActions, "Preserve previously accepted request inputs or coordinate a versioned client migration; inspect the request findings.")
		}
		if !request.Complete {
			incomplete = true
			row.NextActions = append(row.NextActions, "Review unsupported request schemas, references, or serialization in the original captured contracts.")
		}
	}
	if previewReportHasRequestBreaks(*report) && (report.Outcome == "no_findings" || report.Outcome == "incomplete" || report.Outcome == "review_required") {
		report.Outcome = "request_breaking_changes"
	} else if incomplete && (report.Outcome == "no_findings" || report.Outcome == "review_required") {
		report.Outcome = "incomplete"
	}
}
