package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

const routeHealthReviewVersion = 1

type routeHealthReviewUnmatched struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type routeHealthReviewReleaseScope struct {
	Preview                     string                       `json:"preview"`
	SourceImpactStatus          string                       `json:"source_impact_status"`
	SourceAnalysisStatus        string                       `json:"source_analysis_status"`
	SourceMappingStatus         string                       `json:"source_mapping_status"`
	BaselineMatchesStable       bool                         `json:"baseline_matches_stable_deployment"`
	CandidateMatchesPreview     bool                         `json:"candidate_matches_preview_deployment"`
	Bound                       bool                         `json:"release_bound"`
	SourceIssueCount            int                          `json:"source_issue_count"`
	MappedSourceRoutes          int                          `json:"mapped_source_routes"`
	AffectedExistingRoutes      int                          `json:"affected_existing_routes"`
	SelectedAffectedRoutes      int                          `json:"selected_affected_routes"`
	UnselectedAffectedRoutes    int                          `json:"unselected_affected_routes"`
	HealthyAffectedRoutes       int                          `json:"healthy_affected_routes"`
	RegressedAffectedRoutes     int                          `json:"regressed_affected_routes"`
	InsufficientAffectedRoutes  int                          `json:"insufficient_evidence_routes"`
	UnobservedAffectedRoutes    int                          `json:"unobserved_affected_routes"`
	StructuralRoutesSkipped     int                          `json:"added_or_removed_routes_skipped"`
	UnselectableAffectedRoutes  int                          `json:"unselectable_affected_routes"`
	SelectedOutsideReleaseScope int                          `json:"selected_outside_release_scope"`
	UnmatchedSourceRoutes       []routeHealthReviewUnmatched `json:"unmatched_source_routes"`
}

type routeHealthReviewRoute struct {
	Method              string                  `json:"method"`
	Path                string                  `json:"path"`
	ContractChange      string                  `json:"contract_change"`
	SourceChange        string                  `json:"source_change"`
	SourcePrecision     string                  `json:"source_precision"`
	SourceMatch         string                  `json:"source_match"`
	CustomerObservation string                  `json:"baseline_customer_observation"`
	Coverage            string                  `json:"gate_coverage"`
	Status              string                  `json:"status"`
	Reason              string                  `json:"reason"`
	Evidence            *api.RouteHealthFinding `json:"health_evidence,omitempty"`
}

type routeHealthReviewReport struct {
	Version            int                           `json:"version"`
	App                string                        `json:"app"`
	DeploymentID       string                        `json:"deployment_id"`
	StableDeploymentID string                        `json:"stable_deployment_id,omitempty"`
	CheckedAt          string                        `json:"checked_at"`
	Coverage           string                        `json:"coverage"`
	HealthStatus       string                        `json:"health_status"`
	Status             string                        `json:"status"`
	Mode               string                        `json:"mode"`
	Revision           int64                         `json:"revision"`
	ReleaseGate        routeHealthReviewGate         `json:"release_gate"`
	ReleaseScope       routeHealthReviewReleaseScope `json:"release_scope"`
	Routes             []routeHealthReviewRoute      `json:"routes"`
	Caveats            []string                      `json:"caveats,omitempty"`
}

type routeHealthReviewGate struct {
	Status  string   `json:"status"`
	Reasons []string `json:"reasons"`
}

func cmdRoutesHealthReview(args []string) int {
	flags, positional := splitArgsForFlags(args, "json", "fail-on-incomplete")
	fs := newFlagSet("routes health review", flag.ContinueOnError)
	deployment := fs.String("deployment", "", "candidate deployment UUID currently receiving canary traffic")
	previewReportPath := fs.String("preview-report", "", "bound preview report containing source-affected routes")
	failOnIncomplete := fs.Bool("fail-on-incomplete", false, "exit nonzero unless release impact, route coverage and candidate health are complete")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) || !canonicalRouteHealthID(*deployment) || *previewReportPath == "" {
		return printErr("Invalid route health review", errors.New("supply an app, canonical candidate --deployment, and --preview-report"))
	}
	preview, err := readRouteHealthPreviewReport(*previewReportPath, positional[0])
	if err != nil {
		return printErr("Invalid --preview-report", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.RouteCheckTimeout)
	defer cancel()
	health, err := client.GetRouteHealthReport(ctx, positional[0], *deployment)
	if err != nil {
		return printErr("Could not read candidate route health", err)
	}
	if err := validateRouteHealthReport(health, *deployment); err != nil {
		return printErr("Invalid candidate route health response", err)
	}
	if err := routehealth.ValidateClientErrors(health); err != nil {
		return printErr("Invalid candidate client error evidence", err)
	}
	report, err := buildReleaseRouteHealthReviewReport(health, positional[0], *deployment, preview)
	if err != nil {
		return printErr("Could not review route health evidence", err)
	}
	exitCode := 0
	if *failOnIncomplete && report.ReleaseGate.Status != "ready" {
		exitCode = 1
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
		return exitCode
	}
	renderRouteHealthReview(osStdout, report)
	return exitCode
}

func buildReleaseRouteHealthReviewReport(health api.RouteHealthReport, slug, deployment string, preview previewRouteReport) (routeHealthReviewReport, error) {
	if err := validateRouteHealthReport(health, deployment); err != nil {
		return routeHealthReviewReport{}, err
	}
	if preview.Version != 7 || preview.Parent != slug || preview.SourceImpact == nil ||
		!canonicalRouteHealthID(preview.BaselineDeployment) || preview.Routes == nil ||
		len(preview.Routes) > api.RouteImpactMaxComparedRoutes ||
		len(preview.SourceImpact.Unmatched) > api.RouteImpactMaxComparedRoutes {
		return routeHealthReviewReport{}, errors.New("preview identity, source impact or route bounds are invalid")
	}
	baseMatches := health.StableDeploymentID != "" && preview.BaselineDeployment == health.StableDeploymentID
	candidateMatches := preview.CandidateDeployment == "" || preview.CandidateDeployment == deployment
	bound := preview.SourceImpact.Status == "aligned" && baseMatches && candidateMatches
	scope := routeHealthReviewReleaseScope{
		Preview: preview.Preview, SourceImpactStatus: preview.SourceImpact.Status,
		SourceAnalysisStatus: preview.SourceImpact.AnalysisStatus, SourceMappingStatus: preview.SourceImpact.MappingStatus,
		BaselineMatchesStable: baseMatches, CandidateMatchesPreview: candidateMatches, Bound: bound,
		SourceIssueCount:      preview.SourceImpact.IssueCount,
		UnmatchedSourceRoutes: []routeHealthReviewUnmatched{},
	}
	for _, route := range preview.SourceImpact.Unmatched {
		if route.Method == "" || route.Path == "" || route.Reason == "" {
			return routeHealthReviewReport{}, errors.New("preview contains an invalid unmatched source route")
		}
		scope.UnmatchedSourceRoutes = append(scope.UnmatchedSourceRoutes, routeHealthReviewUnmatched{Method: route.Method, Path: route.Path, Reason: route.Reason})
	}
	sort.Slice(scope.UnmatchedSourceRoutes, func(i, j int) bool {
		left, right := scope.UnmatchedSourceRoutes[i], scope.UnmatchedSourceRoutes[j]
		if left.Method != right.Method {
			return left.Method < right.Method
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.Reason < right.Reason
	})

	findings := make(map[string]api.RouteHealthFinding, len(health.Routes))
	for _, finding := range health.Routes {
		findings[routeHealthSuggestionRouteKey(finding.Method, finding.Path)] = finding
	}
	previewRouteCounts := map[string]int{}
	for _, route := range preview.Routes {
		if route.RouteSource == "captured_deployment_contract" {
			previewRouteCounts[routeHealthSuggestionRouteKey(route.Method, route.Path)]++
		}
		if route.RouteSource == "captured_deployment_contract" && route.SourceImpact != nil {
			scope.MappedSourceRoutes++
		}
	}
	reviewRoutes := make([]routeHealthReviewRoute, 0, len(preview.Routes))
	eligible := map[string]bool{}
	for _, route := range preview.Routes {
		if route.RouteSource != "captured_deployment_contract" || route.SourceImpact == nil {
			continue
		}
		zero := int64(0)
		selector := api.RouteHealthRoute{Method: route.Method, Path: route.Path}
		source := route.SourceImpact
		switch source.Change {
		case "source_changed", "potentially_affected", "unknown":
		case "added", "removed":
			scope.StructuralRoutesSkipped++
			continue
		case "no_linked_changes":
			continue
		default:
			return routeHealthReviewReport{}, errors.New("preview contains an invalid source change classification")
		}
		if route.Change == "added" || route.Change == "removed" {
			scope.StructuralRoutesSkipped++
			continue
		}
		scope.AffectedExistingRoutes++
		customerObservation := "unavailable"
		if route.CustomerImpact != nil {
			customerObservation = route.CustomerImpact.Status
		}
		if !slices.Contains([]string{"unavailable", "observed", "no_observations", "not_in_bounded_inventory", "ambiguous_route"}, customerObservation) {
			return routeHealthReviewReport{}, errors.New("preview contains an invalid baseline customer observation status")
		}
		if customerObservation == "no_observations" || customerObservation == "not_in_bounded_inventory" {
			scope.UnobservedAffectedRoutes++
		}
		row := routeHealthReviewRoute{
			Method: route.Method, Path: route.Path, ContractChange: route.Change,
			SourceChange: source.Change, SourcePrecision: source.Precision, SourceMatch: source.Match,
			CustomerObservation: customerObservation,
		}
		selectorErr := routehealth.Validate(api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{selector}})
		if selectorErr != nil || !slices.Contains([]string{"exact", "parameter_names"}, source.Match) ||
			!slices.Contains([]string{"function", "mixed", "module_fallback"}, source.Precision) ||
			previewRouteCounts[routeHealthSuggestionRouteKey(route.Method, route.Path)] != 1 {
			row.Coverage, row.Status, row.Reason = "unavailable", "insufficient_evidence", "route_cannot_be_selected_uniquely"
			scope.UnselectableAffectedRoutes++
			scope.InsufficientAffectedRoutes++
			reviewRoutes = append(reviewRoutes, row)
			continue
		}
		key := routeHealthSuggestionRouteKey(route.Method, route.Path)
		eligible[key] = true
		finding, selected := findings[key]
		switch {
		case !bound:
			row.Coverage, row.Status, row.Reason = "unknown", "insufficient_evidence", "release_identity_or_source_binding_mismatch"
		case !selected:
			row.Coverage, row.Status, row.Reason = "unselected", "insufficient_evidence", "route_not_in_configured_health_gate"
			scope.UnselectedAffectedRoutes++
			scope.InsufficientAffectedRoutes++
		case finding.Status == "healthy":
			row.Coverage, row.Status = "selected", "healthy"
			scope.SelectedAffectedRoutes++
			scope.HealthyAffectedRoutes++
		case finding.Status == "regressed":
			row.Coverage, row.Status = "selected", "regressed"
			row.Reason = finding.Reason
			scope.SelectedAffectedRoutes++
			scope.RegressedAffectedRoutes++
		default:
			row.Coverage, row.Status = "selected", "insufficient_evidence"
			row.Reason = finding.Reason
			scope.SelectedAffectedRoutes++
			scope.InsufficientAffectedRoutes++
		}
		if bound && selected {
			copyFinding := finding
			row.Evidence = &copyFinding
		}
		reviewRoutes = append(reviewRoutes, row)
	}
	sort.Slice(reviewRoutes, func(i, j int) bool {
		if reviewRoutes[i].Method != reviewRoutes[j].Method {
			return reviewRoutes[i].Method < reviewRoutes[j].Method
		}
		return reviewRoutes[i].Path < reviewRoutes[j].Path
	})
	for key := range findings {
		if !eligible[key] {
			scope.SelectedOutsideReleaseScope++
		}
	}
	insufficient := scope.InsufficientAffectedRoutes
	if !bound && scope.AffectedExistingRoutes > 0 {
		insufficient = scope.AffectedExistingRoutes
		scope.InsufficientAffectedRoutes = scope.AffectedExistingRoutes
	}
	status := "healthy"
	if scope.RegressedAffectedRoutes > 0 && bound {
		status = "regressed"
	} else if !bound || insufficient > 0 || scope.AffectedExistingRoutes == 0 {
		status = "insufficient_evidence"
	}
	if !bound {
		// Unselected is not meaningful until the preview and canary identities align.
		scope.UnselectedAffectedRoutes = 0
		for i := range reviewRoutes {
			if reviewRoutes[i].Reason == "release_identity_or_source_binding_mismatch" {
				reviewRoutes[i].Coverage = "unknown"
			}
		}
	}
	report := routeHealthReviewReport{
		Version: routeHealthReviewVersion, App: slug, DeploymentID: deployment,
		StableDeploymentID: health.StableDeploymentID, CheckedAt: health.CheckedAt.UTC().Format(time.RFC3339Nano),
		Coverage: health.Coverage, HealthStatus: health.Status, Status: status, Mode: health.Mode, Revision: health.Revision,
		ReleaseScope: scope, Routes: reviewRoutes,
	}
	report.Caveats = releaseRouteHealthReviewCaveats(report)
	report.ReleaseGate = evaluateRouteHealthReviewGate(report)
	return report, nil
}

func evaluateRouteHealthReviewGate(report routeHealthReviewReport) routeHealthReviewGate {
	reasons := []string{}
	scope := report.ReleaseScope
	if !scope.Bound {
		reasons = append(reasons, "release_identity_or_source_binding_mismatch")
	}
	if scope.SourceAnalysisStatus != "complete" || scope.SourceIssueCount > 0 {
		reasons = append(reasons, "source_analysis_incomplete")
	}
	if scope.SourceMappingStatus != "complete" || len(scope.UnmatchedSourceRoutes) > 0 {
		reasons = append(reasons, "source_route_mapping_incomplete")
	}
	if scope.StructuralRoutesSkipped > 0 {
		reasons = append(reasons, "added_or_removed_routes_not_health_comparable")
	}
	if scope.AffectedExistingRoutes == 0 {
		reasons = append(reasons, "no_reviewable_affected_routes")
	}
	if scope.UnselectableAffectedRoutes > 0 {
		reasons = append(reasons, "affected_routes_unselectable")
	}
	if scope.UnselectedAffectedRoutes > 0 {
		reasons = append(reasons, "affected_routes_unselected")
	}
	if scope.RegressedAffectedRoutes > 0 {
		reasons = append(reasons, "affected_routes_regressed")
	}
	if scope.InsufficientAffectedRoutes > 0 {
		reasons = append(reasons, "affected_route_evidence_insufficient")
	}
	if report.HealthStatus != "healthy" {
		reasons = append(reasons, "candidate_route_health_not_healthy")
	}
	status := "ready"
	if len(reasons) > 0 {
		status = "not_ready"
	}
	return routeHealthReviewGate{Status: status, Reasons: reasons}
}

func releaseRouteHealthReviewCaveats(report routeHealthReviewReport) []string {
	caveats := []string{}
	scope := report.ReleaseScope
	if !scope.Bound {
		caveats = append(caveats, "The preview source binding or deployment pair does not match this canary report; route verdicts are withheld until identities align.")
	}
	if scope.SourceAnalysisStatus != "complete" {
		caveats = append(caveats, "Source analysis is incomplete; this review covers mapped findings only.")
	}
	if scope.SourceIssueCount > 0 {
		caveats = append(caveats, fmt.Sprintf("The source analysis contains %d issues; inspect the preview report before relying on mapped findings.", scope.SourceIssueCount))
	}
	if scope.SourceMappingStatus != "complete" || len(scope.UnmatchedSourceRoutes) > 0 {
		caveats = append(caveats, fmt.Sprintf("%d source routes did not map to a captured deployment route; see release_scope.unmatched_source_routes.", len(scope.UnmatchedSourceRoutes)))
	}
	if scope.UnobservedAffectedRoutes > 0 {
		caveats = append(caveats, fmt.Sprintf("%d affected routes have no baseline customer observations (or fell outside the bounded inventory); this does not prove they are unused.", scope.UnobservedAffectedRoutes))
	}
	if scope.UnselectableAffectedRoutes > 0 {
		caveats = append(caveats, fmt.Sprintf("%d affected captured routes cannot be represented as unique exact route-health selectors.", scope.UnselectableAffectedRoutes))
	}
	if scope.StructuralRoutesSkipped > 0 {
		caveats = append(caveats, fmt.Sprintf("%d added or removed routes were skipped because current health selectors require a route present in both captured revisions.", scope.StructuralRoutesSkipped))
	}
	if scope.SelectedOutsideReleaseScope > 0 {
		caveats = append(caveats, fmt.Sprintf("The configured health gate includes %d routes outside this source-affected release scope.", scope.SelectedOutsideReleaseScope))
	}
	if scope.AffectedExistingRoutes == 0 {
		caveats = append(caveats, "No mapped existing source-affected routes qualified for this review.")
	}
	return caveats
}

func renderRouteHealthReview(w io.Writer, report routeHealthReviewReport) {
	_, _ = fmt.Fprintf(w, "Release route health review: %s\nApp: %s; preview: %s\nCandidate: %s; stable: %s\nCanary health: %s (%s, revision %d); coverage: %s\nRelease binding: %t (source %s; baseline matches stable %t; candidate matches preview %t)\n",
		report.Status, report.App, previewReportText(report.ReleaseScope.Preview), report.DeploymentID,
		previewReportText(report.StableDeploymentID), report.HealthStatus, report.Mode, report.Revision, report.Coverage,
		report.ReleaseScope.Bound, report.ReleaseScope.SourceImpactStatus,
		report.ReleaseScope.BaselineMatchesStable, report.ReleaseScope.CandidateMatchesPreview)
	_, _ = fmt.Fprintf(w, "Release gate: %s", report.ReleaseGate.Status)
	for _, reason := range report.ReleaseGate.Reasons {
		_, _ = fmt.Fprintf(w, "; %s", reason)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "Affected existing routes: %d; selected %d; unselected %d; healthy %d; regressed %d; insufficient evidence %d; baseline-unobserved %d\n",
		report.ReleaseScope.AffectedExistingRoutes, report.ReleaseScope.SelectedAffectedRoutes,
		report.ReleaseScope.UnselectedAffectedRoutes, report.ReleaseScope.HealthyAffectedRoutes,
		report.ReleaseScope.RegressedAffectedRoutes, report.ReleaseScope.InsufficientAffectedRoutes,
		report.ReleaseScope.UnobservedAffectedRoutes)
	if len(report.Routes) == 0 {
		_, _ = fmt.Fprintln(w, "No mapped source-affected existing routes were available to review.")
	}
	for _, route := range report.Routes {
		_, _ = fmt.Fprintf(w, "\n%s %s: %s (%s; %s)\n", previewReportText(route.Method), previewReportText(route.Path), route.Status, route.Coverage, previewReportText(route.Reason))
		_, _ = fmt.Fprintf(w, "  Source: %s (%s precision; %s match); baseline customer observations: %s\n", previewReportText(route.SourceChange), previewReportText(route.SourcePrecision), previewReportText(route.SourceMatch), previewReportText(route.CustomerObservation))
		if route.Evidence != nil {
			for _, window := range route.Evidence.Windows {
				_, _ = fmt.Fprintf(w, "  %s–%s candidate %d/%d 5xx; stable %d/%d 5xx: %s (%s)\n",
					window.Start.Format("15:04:05Z"), window.End.Format("15:04:05Z"),
					window.Candidate.ServerErrors, window.Candidate.Requests, window.Stable.ServerErrors,
					window.Stable.Requests, window.Status, previewReportText(window.Reason))
			}
		}
	}
	for _, unmatched := range report.ReleaseScope.UnmatchedSourceRoutes {
		_, _ = fmt.Fprintf(w, "\nUnmapped source route %s %s: %s\n", previewReportText(unmatched.Method), previewReportText(unmatched.Path), previewReportText(unmatched.Reason))
	}
	for _, caveat := range report.Caveats {
		_, _ = fmt.Fprintf(w, "\nEvidence: %s\n", caveat)
	}
}
