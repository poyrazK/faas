package main

import "sort"

type previewRouteReview struct {
	Method                string                `json:"method,omitempty"`
	Path                  string                `json:"path,omitempty"`
	Scope                 string                `json:"scope"`
	Priority              string                `json:"priority"`
	Reasons               []string              `json:"reasons"`
	RequestCompatibility  string                `json:"request_compatibility,omitempty"`
	SecurityCompatibility string                `json:"security_compatibility,omitempty"`
	SourceChange          string                `json:"source_change,omitempty"`
	CandidateChecks       string                `json:"candidate_checks"`
	BaselineTraffic       *previewReportTraffic `json:"baseline_traffic,omitempty"`
	NextActions           []string              `json:"next_actions"`
}

func prioritizePreviewRouteReview(report *previewRouteReport) {
	report.ReviewPriorities = []previewRouteReview{}
	source := report.SourceImpact
	if source == nil && report.Requests.Status == "" && report.Security.Status == "" && report.Requirements == nil {
		return
	}
	if requirements := report.Requirements; requirements != nil && requirements.Coverage != nil {
		if requirements.Coverage.Status != "available" {
			report.ReviewPriorities = append(report.ReviewPriorities, previewRouteReview{Scope: "route_coverage", Priority: "needs_evidence", Reasons: []string{requirements.Coverage.Code}, CandidateChecks: "not_established", NextActions: []string{"Obtain a complete captured candidate inventory and resolve coverage bounds or unsupported operations."}})
		}
		for _, group := range requirements.Groups {
			if group.Code == "group_no_routes" {
				report.ReviewPriorities = append(report.ReviewPriorities, previewRouteReview{Scope: "route_coverage", Priority: "blocker", Reasons: []string{"group_no_routes"}, CandidateChecks: "not_established", NextActions: []string{"Review the unmatched group in the requirements section; correct its prefix/methods or capture the missing operations."}})
			}
		}
		// Candidate inventory can be available independently of the baseline
		// comparison. Keep those route findings visible in the review queue.
		presentRoutes := map[string]bool{}
		for _, row := range report.Routes {
			presentRoutes[previewReportRouteKey(row.Method, row.Path)] = true
		}
		for _, requirement := range requirements.Routes {
			present := presentRoutes[previewReportRouteKey(requirement.Method, requirement.Path)]
			if !present && requirement.Status != "satisfied" {
				row := previewReportRoute{Method: requirement.Method, Path: requirement.Path, RouteSource: "captured_deployment_contract"}
				if review, relevant := previewSourceReviewRoute(row, requirement.Status); relevant {
					review.Scope = "route_coverage"
					report.ReviewPriorities = append(report.ReviewPriorities, review)
				}
			}
		}
	}
	if report.Requests.Status == "unavailable" {
		report.ReviewPriorities = append(report.ReviewPriorities, previewRouteReview{Scope: "request_comparison", Priority: "needs_evidence", Reasons: []string{"request_comparison_unavailable"}, CandidateChecks: "not_established", NextActions: []string{"Obtain both complete captured contracts and resolve request-comparison bounds or unavailable evidence."}})
	}
	if report.Security.Status == "unavailable" {
		report.ReviewPriorities = append(report.ReviewPriorities, previewRouteReview{Scope: "security_comparison", Priority: "needs_evidence", Reasons: []string{"security_comparison_unavailable"}, CandidateChecks: "not_established", NextActions: []string{"Obtain both complete captured contracts and resolve security-comparison bounds or unavailable evidence."}})
	}
	if source != nil && source.Status != "aligned" {
		report.ReviewPriorities = append(report.ReviewPriorities, previewRouteReview{
			Scope: "source_binding", Priority: "needs_evidence", Reasons: []string{"source_unbound"}, CandidateChecks: "unbound",
			NextActions: []string{"Regenerate source impact with --base and --head set to the selected deployment commits, in the declared repository and build root; ensure both deployments declare matching provenance."},
		})
	}
	if source != nil && source.AnalysisStatus != "complete" {
		report.ReviewPriorities = append(report.ReviewPriorities, previewRouteReview{
			Scope: "source_analysis", Priority: "needs_evidence", Reasons: []string{"source_analysis_incomplete"}, CandidateChecks: "not_established",
			NextActions: []string{"Inspect unresolved registrations and references in the original source impact report; static analysis may omit routes."},
		})
	}
	var unmatchedSources []previewUnmatchedSource
	if source != nil {
		unmatchedSources = source.Unmatched
	}
	for _, unmatched := range unmatchedSources {
		if unmatched.Reason == "source_unbound" {
			continue // One binding action covers the whole unbound artifact.
		}
		report.ReviewPriorities = append(report.ReviewPriorities, previewRouteReview{
			Method: unmatched.Method, Path: unmatched.Path, Scope: "unmatched_source", Priority: "needs_evidence",
			SourceChange: unmatched.Source.Change, Reasons: []string{unmatched.Reason}, CandidateChecks: "unbound",
			NextActions: []string{"Inspect the source registration and both captured deployment contracts; resolve the missing, ambiguous, or conflicting route mapping before combining evidence."},
		})
	}
	for _, row := range report.Routes {
		if review, relevant := previewSourceReviewRoute(row, previewReviewRequirementStatus(report, row)); relevant {
			report.ReviewPriorities = append(report.ReviewPriorities, review)
		}
	}
	sort.SliceStable(report.ReviewPriorities, func(i, j int) bool {
		left, right := report.ReviewPriorities[i], report.ReviewPriorities[j]
		if previewReviewPriority(left.Priority) != previewReviewPriority(right.Priority) {
			return previewReviewPriority(left.Priority) < previewReviewPriority(right.Priority)
		}
		if (left.BaselineTraffic != nil) != (right.BaselineTraffic != nil) {
			return left.BaselineTraffic != nil
		}
		if left.BaselineTraffic != nil && left.BaselineTraffic.Requests != right.BaselineTraffic.Requests {
			return left.BaselineTraffic.Requests > right.BaselineTraffic.Requests
		}
		return left.Scope+" "+previewReportRouteKey(left.Method, left.Path) < right.Scope+" "+previewReportRouteKey(right.Method, right.Path)
	})
	if report.Outcome != "no_findings" && report.Outcome != "review_required" {
		return // Preserve contract breaks, test failures, and policy violations.
	}
	for _, item := range report.ReviewPriorities {
		if item.Priority == "needs_evidence" {
			report.Outcome = "incomplete"
			return
		}
	}
	if len(report.ReviewPriorities) > 0 {
		report.Outcome = "review_required"
	}
}

func previewReviewPriority(priority string) int {
	switch priority {
	case "blocker":
		return 0
	case "needs_evidence":
		return 1
	default:
		return 2
	}
}

// Only exact operation identities join. Version 2 already establishes the
// family result; a version 1 concrete path is never promoted to a template.
func previewReviewRequirementStatus(report *previewRouteReport, row previewReportRoute) string {
	if report.Requirements == nil {
		return "not_supplied"
	}
	for _, requirement := range report.Requirements.Routes {
		if previewReportRouteKey(requirement.Method, requirement.Path) == previewReportRouteKey(row.Method, row.Path) {
			return requirement.Status
		}
	}
	return "not_established"
}

func previewSourceReviewRoute(row previewReportRoute, requirementStatus string) (previewRouteReview, bool) {
	item := previewRouteReview{Method: row.Method, Path: row.Path, Scope: "captured_route", Priority: "review", Reasons: []string{}, NextActions: []string{}, BaselineTraffic: row.BaselineTraffic}
	if row.RouteSource != "captured_deployment_contract" {
		item.Scope, item.BaselineTraffic = "current_route", nil
	}
	if row.SourceImpact != nil {
		item.SourceChange = row.SourceImpact.Change
		if row.SourceImpact.Change != "no_linked_changes" {
			item.Reasons = append(item.Reasons, "source_"+row.SourceImpact.Change)
			item.NextActions = append(item.NextActions, "Review the handler locations and static reference chains; validate affected behavior with customer HTTP assertions.")
		}
		if row.SourceImpact.Change == "unknown" || row.SourceImpact.UncertaintyCount > 0 {
			item.Priority = "needs_evidence"
			item.Reasons = append(item.Reasons, "source_route_uncertain")
			item.NextActions = append(item.NextActions, "Resolve route uncertainties in the original source report.")
		}
	}
	if row.RequestCompatibility != nil {
		item.RequestCompatibility = row.RequestCompatibility.Status
		if row.RequestCompatibility.Status == "breaking" {
			item.Priority = "blocker"
			item.Reasons = append(item.Reasons, "request_contract_break")
			item.NextActions = append(item.NextActions, "Preserve previously accepted request shapes or coordinate a versioned client migration; inspect the request findings.")
		}
		if row.RequestCompatibility.Status != "not_compared" && !row.RequestCompatibility.Complete {
			if item.Priority != "blocker" {
				item.Priority = "needs_evidence"
			}
			item.Reasons = append(item.Reasons, "request_comparison_unknown")
			item.NextActions = append(item.NextActions, "Review unsupported request schemas, references, or serialization in the original captured contracts.")
		}
	}
	if row.RequestContractChanged && (row.RequestCompatibility == nil || !row.RequestCompatibility.Complete) {
		item.Reasons = append(item.Reasons, "request_contract_changed")
		item.NextActions = append(item.NextActions, "Review request and parameter compatibility for existing callers.")
	}
	if security := row.SecurityCompatibility; security != nil {
		item.SecurityCompatibility = security.Status
		if security.Regression || security.ClientBreaking {
			item.Priority = "blocker"
			if security.Regression {
				item.Reasons = append(item.Reasons, "declared_security_regression")
				item.NextActions = append(item.NextActions, "Review the broadened authentication declaration and confirm intended access and runtime enforcement.")
			}
			if security.ClientBreaking {
				item.Reasons = append(item.Reasons, "security_client_break")
				item.NextActions = append(item.NextActions, "Preserve supported credentials/scopes or coordinate a client migration.")
			}
		}
		if security.Status != "not_compared" && !security.Complete {
			if item.Priority != "blocker" {
				item.Priority = "needs_evidence"
			}
			item.Reasons = append(item.Reasons, "security_comparison_unknown")
			item.NextActions = append(item.NextActions, "Inspect unresolved security requirements and credential definitions in the captured contracts.")
		}
	}
	if row.Change == "added" {
		item.Reasons = append(item.Reasons, "contract_route_added")
	}
	if len(row.Breaks) > 0 || row.Change == "removed" {
		item.Priority = "blocker"
		item.Reasons = append(item.Reasons, "contract_compatibility_break")
		item.NextActions = append(item.NextActions, "Review the removed route or response-schema compatibility break before release.")
	}
	switch requirementStatus {
	case "violated":
		item.Priority = "blocker"
		item.Reasons = append(item.Reasons, "declared_requirement_violated")
		item.NextActions = append(item.NextActions, "Review the declared requirement findings and current app policy.")
	case "unknown":
		if item.Priority != "blocker" {
			item.Priority = "needs_evidence"
		}
		item.Reasons = append(item.Reasons, "declared_requirement_unknown")
		item.NextActions = append(item.NextActions, "Inspect unknown declared requirements in the requirements section; obtain missing policy evidence or trace the concrete request.")
	}
	passed, failed := 0, 0
	for _, profile := range row.TestProfiles {
		passed += profile.Passed
		failed += profile.Failed
	}
	switch {
	case failed > 0:
		item.CandidateChecks = "failed"
		item.Priority = "blocker"
		item.Reasons = append(item.Reasons, "candidate_checks_failed")
		item.NextActions = append(item.NextActions, "Inspect the failing candidate checks in the original test report.")
	case row.Change == "removed":
		item.CandidateChecks = "route_removed"
	case passed > 0:
		item.CandidateChecks = "passed_samples"
	default:
		item.CandidateChecks = "missing"
	}
	if len(item.Reasons) == 0 {
		return item, false
	}
	if item.CandidateChecks == "missing" {
		if item.Priority != "blocker" {
			item.Priority = "needs_evidence"
		}
		item.Reasons = append(item.Reasons, "candidate_checks_missing")
		item.NextActions = append(item.NextActions, "Run customer HTTP assertions against the selected candidate deployment and supply its test receipts; separate-environment receipts are supplemental.")
	}
	return item, true
}
