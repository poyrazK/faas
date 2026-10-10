package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

const routeHealthSuggestionVersion = 1

type routeHealthSuggestionObservation struct {
	From            string `json:"from"`
	Until           string `json:"until"`
	AsOf            string `json:"as_of"`
	Coverage        string `json:"coverage"`
	WindowClamped   bool   `json:"window_clamped"`
	RoutesScanned   int    `json:"routes_scanned"`
	RouteLimit      int    `json:"route_limit"`
	RoutesTruncated bool   `json:"routes_truncated"`
}

type routeHealthSuggestionReleaseScope struct {
	Preview                  string `json:"preview"`
	SourceImpactStatus       string `json:"source_impact_status"`
	SourceAnalysisStatus     string `json:"source_analysis_status"`
	SourceMappingStatus      string `json:"source_mapping_status"`
	SourceIssueCount         int    `json:"source_issue_count"`
	UnmatchedSourceRoutes    int    `json:"unmatched_source_routes"`
	MappedSourceRoutes       int    `json:"mapped_source_routes"`
	AffectedSourceRoutes     int    `json:"affected_source_routes"`
	ObservedAffectedRoutes   int    `json:"observed_affected_routes"`
	UnobservedAffectedRoutes int    `json:"unobserved_affected_routes"`
	StructuralRoutesSkipped  int    `json:"structural_routes_skipped"`
	UnselectableRoutes       int    `json:"unselectable_routes"`
	ObservedRoutesOmitted    int    `json:"observed_routes_omitted_by_limit"`
}

type routeHealthSuggestion struct {
	Rank                       int                  `json:"rank"`
	Selector                   api.RouteHealthRoute `json:"selector"`
	Requests                   int64                `json:"requests"`
	CustomerGroupBy            string               `json:"customer_group_by"`
	CustomerCount              int64                `json:"customer_count"`
	IdentifiedRequests         int64                `json:"identified_requests"`
	AnonymousRequests          int64                `json:"anonymous_requests"`
	UnresolvedIdentityRequests int64                `json:"unresolved_identity_requests"`
	LastObservedAt             string               `json:"last_observed_at"`
	CustomerDetailsTruncated   bool                 `json:"customer_details_truncated"`
	OtherCustomerRequests      int64                `json:"other_customer_requests"`
	SampleAssessment           string               `json:"sample_assessment"`
	SourceChange               string               `json:"source_change,omitempty"`
	SourcePrecision            string               `json:"source_precision,omitempty"`
	SourceMatch                string               `json:"source_match,omitempty"`
}

type routeHealthSuggestionReport struct {
	Version         int                                `json:"version"`
	App             string                             `json:"app"`
	DeploymentID    string                             `json:"deployment_id"`
	CustomerGroupBy string                             `json:"customer_group_by"`
	RankedBy        string                             `json:"ranked_by"`
	Observation     routeHealthSuggestionObservation   `json:"observation"`
	SampleNote      string                             `json:"sample_note"`
	Suggestions     []routeHealthSuggestion            `json:"suggestions"`
	Selectors       []api.RouteHealthRoute             `json:"selectors"`
	ReleaseScope    *routeHealthSuggestionReleaseScope `json:"release_scope,omitempty"`
	Caveats         []string                           `json:"caveats,omitempty"`
}

// routeHealthTargetError names the one problem with the app and deployment
// the routes health leaves need. production-us hunt #4: `routes health suggest
// <slug>` without --deployment answered with every requirement at once.
func routeHealthTargetError(positional []string, deployment string) error {
	switch {
	case len(positional) == 0:
		return errors.New("pass the app slug as the first argument")
	case len(positional) > 1:
		return fmt.Errorf("pass exactly one app slug; got %q", positional)
	case !validCLISlug(positional[0]):
		return fmt.Errorf("%q is not a valid app slug", positional[0])
	case strings.TrimSpace(deployment) == "":
		return fmt.Errorf("--deployment is required: pass the deployment UUID to inspect (list them with `gregale deployments --app %s`)", positional[0])
	case !canonicalRouteHealthID(deployment):
		return fmt.Errorf("--deployment must be a canonical deployment UUID; got %q", deployment)
	}
	return nil
}

func cmdRoutesHealthSuggest(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("routes health suggest", flag.ContinueOnError)
	deployment := fs.String("deployment", "", "immutable deployment UUID to inspect")
	since := fs.String("since", "168h", "observed usage window (default 168h; also accepts 7d or RFC3339)")
	groupBy := fs.String("customer-group-by", "tenant", "rank by distinct tenant or consumer count")
	limit := fs.Int("limit", 10, "number of route selectors to suggest (1–20)")
	previewReportPath := fs.String("preview-report", "", "limit suggestions to source-affected routes in a bound preview report")
	output := fs.String("out", "", "save a JSON selector array ready for routes health set")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if err := routeHealthTargetError(positional, *deployment); err != nil {
		return printErr("Invalid route health suggestion", err)
	}
	switch {
	case !validRouteHealthSuggestionSince(*since):
		return printErr("Invalid route health suggestion", fmt.Errorf("--since must be a duration such as 168h or 7d, or an RFC3339 time; got %q", *since))
	case !slices.Contains([]string{"tenant", "consumer"}, *groupBy):
		return printErr("Invalid route health suggestion", fmt.Errorf("--customer-group-by must be tenant or consumer; got %q", *groupBy))
	case *limit < 1 || *limit > api.RouteHealthMaxRoutes:
		return printErr("Invalid route health suggestion", fmt.Errorf("--limit must be between 1 and %d; got %d", api.RouteHealthMaxRoutes, *limit))
	}
	var releasePreview *previewRouteReport
	if *previewReportPath != "" {
		if _, err := os.Lstat(*previewReportPath); err != nil {
			return printErr("Invalid --preview-report", errors.New("choose a readable preview report file"))
		}
		preview, err := readRouteHealthSuggestionPreviewReport(*previewReportPath, positional[0], *deployment)
		if err != nil {
			return printErr("Invalid --preview-report", err)
		}
		releasePreview = &preview
	}
	if *output != "" {
		if _, err := os.Lstat(*output); err == nil || !errors.Is(err, os.ErrNotExist) {
			return printErr("Invalid --out", errors.New("choose a new path; existing files and symlinks are not replaced"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.RouteCheckTimeout)
	defer cancel()
	usage, err := client.GetAppRouteCustomerUsage(ctx, positional[0], api.RouteCustomerUsageOptions{
		DeploymentID: *deployment, Since: *since,
	})
	if err != nil {
		return printErr("Could not read route usage", err)
	}
	var report routeHealthSuggestionReport
	if releasePreview != nil {
		report, err = buildReleaseRouteHealthSuggestionReport(usage, positional[0], *deployment, *groupBy, *limit, *releasePreview)
		if err != nil {
			return printErr("Could not scope route suggestions to the release", err)
		}
	} else {
		report, err = buildRouteHealthSuggestionReport(usage, positional[0], *deployment, *groupBy, *limit)
		if err != nil {
			return printErr("Invalid route usage response", err)
		}
	}
	if *output != "" {
		body, err := json.MarshalIndent(report.Selectors, "", "  ")
		if err != nil {
			return printErr("Could not encode route selectors", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return printErr("Could not save route selectors", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	renderRouteHealthSuggestions(osStdout, report, *output)
	return 0
}

func readRouteHealthSuggestionPreviewReport(path, slug, deployment string) (previewRouteReport, error) {
	report, err := readRouteHealthPreviewReport(path, slug)
	if err != nil {
		return previewRouteReport{}, err
	}
	if report.BaselineDeployment != deployment {
		return previewRouteReport{}, errors.New("preview, parent app, baseline deployment, source impact or route bounds do not match")
	}
	return report, nil
}

func readRouteHealthPreviewReport(path, slug string) (previewRouteReport, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return previewRouteReport{}, errors.New("use a readable regular file without symlinks")
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, api.RouteImpactReportMaxBytes+1))
	if err != nil {
		return previewRouteReport{}, errors.New("could not read preview report")
	}
	if int64(len(body)) > api.RouteImpactReportMaxBytes {
		return previewRouteReport{}, errors.New("preview report exceeds the size limit")
	}
	var report previewRouteReport
	if err := json.Unmarshal(body, &report); err != nil {
		return previewRouteReport{}, errors.New("preview report is not valid JSON")
	}
	if report.Version != 7 || !validCLISlug(report.Preview) || report.Parent != slug ||
		!canonicalRouteHealthID(report.BaselineDeployment) ||
		report.SourceImpact == nil || report.Routes == nil || len(report.Routes) > api.RouteImpactMaxComparedRoutes {
		return previewRouteReport{}, errors.New("preview, parent app, baseline deployment, source impact or route bounds do not match")
	}
	source := report.SourceImpact
	if !slices.Contains([]string{"aligned", "unbound"}, source.Status) ||
		!slices.Contains([]string{"complete", "incomplete"}, source.AnalysisStatus) ||
		!slices.Contains([]string{"complete", "partial", "not_attempted"}, source.MappingStatus) || source.IssueCount < 0 {
		return previewRouteReport{}, errors.New("preview report has invalid source impact status")
	}
	if source.Status == "aligned" && (source.Base.Status != "declared_match" || source.Candidate.Status != "declared_match") ||
		source.Status == "unbound" && source.Base.Status == "declared_match" && source.Candidate.Status == "declared_match" {
		return previewRouteReport{}, errors.New("preview source binding summary is inconsistent")
	}
	switch source.MappingStatus {
	case "complete":
		if source.Status != "aligned" || len(source.Unmatched) != 0 {
			return previewRouteReport{}, errors.New("preview route mapping summary is inconsistent")
		}
	case "partial":
		if source.Status != "aligned" || len(source.Unmatched) == 0 {
			return previewRouteReport{}, errors.New("preview route mapping summary is inconsistent")
		}
	case "not_attempted":
		if source.Status != "unbound" {
			return previewRouteReport{}, errors.New("preview route mapping summary is inconsistent")
		}
	}
	return report, nil
}

type routeHealthSuggestionSource struct {
	change    string
	precision string
	match     string
}

func buildReleaseRouteHealthSuggestionReport(usage api.RouteCustomerUsageResponse, slug, deployment, groupBy string, limit int, preview previewRouteReport) (routeHealthSuggestionReport, error) {
	// Validate the complete API response before filtering it, so malformed rows
	// cannot hide outside the source-impacted subset.
	if _, err := buildRouteHealthSuggestionReport(usage, slug, deployment, groupBy, api.RouteCustomerUsageMaxRoutes); err != nil {
		return routeHealthSuggestionReport{}, err
	}
	scope := &routeHealthSuggestionReleaseScope{
		Preview: preview.Preview, SourceImpactStatus: preview.SourceImpact.Status,
		SourceAnalysisStatus: preview.SourceImpact.AnalysisStatus, SourceMappingStatus: preview.SourceImpact.MappingStatus,
		SourceIssueCount: preview.SourceImpact.IssueCount, UnmatchedSourceRoutes: len(preview.SourceImpact.Unmatched),
	}
	eligible := map[string]routeHealthSuggestionSource{}
	routeCounts := map[string]int{}
	for _, route := range preview.Routes {
		if route.RouteSource != "captured_deployment_contract" || route.SourceImpact == nil {
			continue
		}
		scope.MappedSourceRoutes++
		key := routeHealthSuggestionRouteKey(route.Method, route.Path)
		routeCounts[key]++
	}
	if preview.SourceImpact.Status == "aligned" {
		for _, route := range preview.Routes {
			if route.RouteSource != "captured_deployment_contract" || route.SourceImpact == nil {
				continue
			}
			source := route.SourceImpact
			switch source.Change {
			case "source_changed", "potentially_affected", "unknown":
			case "added", "removed":
				scope.StructuralRoutesSkipped++
				continue
			default:
				continue
			}
			if route.Change == "added" || route.Change == "removed" {
				scope.StructuralRoutesSkipped++
				continue
			}
			if routeCounts[routeHealthSuggestionRouteKey(route.Method, route.Path)] != 1 {
				scope.UnselectableRoutes++
				continue
			}
			zero := int64(0)
			selector := api.RouteHealthRoute{Method: route.Method, Path: route.Path}
			if err := routehealth.Validate(api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{selector}}); err != nil ||
				!slices.Contains([]string{"exact", "parameter_names"}, source.Match) ||
				!slices.Contains([]string{"function", "mixed", "module_fallback"}, source.Precision) {
				scope.UnselectableRoutes++
				continue
			}
			key := routeHealthSuggestionRouteKey(route.Method, route.Path)
			eligible[key] = routeHealthSuggestionSource{change: source.Change, precision: source.Precision, match: source.Match}
			scope.AffectedSourceRoutes++
		}
	}

	filtered := usage
	filtered.Routes = []api.RouteCustomerUsage{}
	seenObserved := map[string]bool{}
	for _, row := range usage.Routes {
		path, ok := strings.CutPrefix(row.Route, row.Method+" ")
		if !ok {
			continue // The full response was already validated above.
		}
		key := routeHealthSuggestionRouteKey(row.Method, path)
		if _, ok := eligible[key]; ok {
			filtered.Routes = append(filtered.Routes, row)
			seenObserved[key] = true
		}
	}
	for key := range eligible {
		if !seenObserved[key] {
			scope.UnobservedAffectedRoutes++
		}
	}
	scope.ObservedAffectedRoutes = len(filtered.Routes)
	report, err := buildRouteHealthSuggestionReport(filtered, slug, deployment, groupBy, limit)
	if err != nil {
		return routeHealthSuggestionReport{}, err
	}
	report.Observation.RoutesScanned = len(usage.Routes)
	report.Observation.RouteLimit = usage.RoutesLimit
	report.Observation.RoutesTruncated = usage.RoutesTruncated
	report.ReleaseScope = scope
	report.RankedBy = fmt.Sprintf("among source-affected captured routes: distinct %s groups descending, then observed requests descending, then method/path ascending", groupBy)
	if scope.ObservedAffectedRoutes > len(report.Suggestions) {
		scope.ObservedRoutesOmitted = scope.ObservedAffectedRoutes - len(report.Suggestions)
	}
	for i := range report.Suggestions {
		key := routeHealthSuggestionRouteKey(report.Suggestions[i].Selector.Method, report.Suggestions[i].Selector.Path)
		source := eligible[key]
		report.Suggestions[i].SourceChange = source.change
		report.Suggestions[i].SourcePrecision = source.precision
		report.Suggestions[i].SourceMatch = source.match
	}
	report.Caveats = releaseRouteHealthSuggestionCaveats(scope, usage.RoutesTruncated)
	return report, nil
}

func routeHealthSuggestionRouteKey(method, path string) string {
	return strings.ToUpper(method) + "\x00" + path
}

func releaseRouteHealthSuggestionCaveats(scope *routeHealthSuggestionReleaseScope, inventoryTruncated bool) []string {
	caveats := []string{}
	if scope.SourceImpactStatus != "aligned" {
		caveats = append(caveats, "Source impact is unbound, so no route can be safely scoped to this release.")
	}
	if scope.SourceAnalysisStatus != "complete" {
		caveats = append(caveats, "Source analysis is incomplete; suggestions cover mapped findings only.")
	}
	if scope.SourceIssueCount > 0 && scope.SourceAnalysisStatus == "complete" {
		caveats = append(caveats, fmt.Sprintf("The source report contains %d analysis issues; inspect them before relying on the mapped findings.", scope.SourceIssueCount))
	}
	if scope.SourceMappingStatus != "complete" || scope.UnmatchedSourceRoutes > 0 {
		caveats = append(caveats, "Some source routes did not map to a captured deployment route; see the preview report before relying on this selector set.")
	}
	if scope.UnobservedAffectedRoutes > 0 {
		message := fmt.Sprintf("%d mapped affected routes had no matching observed route in this window and were omitted.", scope.UnobservedAffectedRoutes)
		if inventoryTruncated {
			message += " The observed route inventory was truncated, so some may be outside its returned top routes."
		}
		caveats = append(caveats, message)
	}
	if scope.StructuralRoutesSkipped > 0 {
		caveats = append(caveats, fmt.Sprintf("%d added or removed routes were omitted because selectors are scoped to routes present in both captured revisions.", scope.StructuralRoutesSkipped))
	}
	if scope.UnselectableRoutes > 0 {
		caveats = append(caveats, fmt.Sprintf("%d mapped affected routes could not be represented as exact route-health selectors and were omitted.", scope.UnselectableRoutes))
	}
	if scope.ObservedRoutesOmitted > 0 {
		caveats = append(caveats, fmt.Sprintf("%d observed affected routes were omitted by the suggestion limit; raise --limit to include more.", scope.ObservedRoutesOmitted))
	}
	if scope.AffectedSourceRoutes == 0 && scope.SourceImpactStatus == "aligned" {
		caveats = append(caveats, "No mapped existing routes have source changes that qualify for route-health suggestions.")
	}
	return caveats
}

func validRouteHealthSuggestionSince(value string) bool {
	if _, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return true
	}
	if duration, err := time.ParseDuration(value); err == nil {
		return duration > 0
	}
	if len(value) > 1 && value[len(value)-1] == 'd' {
		days, err := strconv.ParseInt(value[:len(value)-1], 10, 64)
		maxDays := int64(math.MaxInt64 / int64(24*time.Hour))
		return err == nil && days > 0 && days <= maxDays
	}
	return false
}

func buildRouteHealthSuggestionReport(usage api.RouteCustomerUsageResponse, slug, deployment, groupBy string, limit int) (routeHealthSuggestionReport, error) {
	from, fromErr := time.Parse(time.RFC3339Nano, usage.From)
	until, untilErr := time.Parse(time.RFC3339Nano, usage.Until)
	asOf, asOfErr := time.Parse(time.RFC3339Nano, usage.AsOf)
	if usage.Slug != slug || usage.DeploymentID != deployment || usage.Coverage != "observed_only" ||
		usage.Routes == nil || usage.RoutesLimit != api.RouteCustomerUsageMaxRoutes || usage.CustomersLimit != api.RouteCustomerUsageMaxCustomers ||
		len(usage.Routes) > usage.RoutesLimit || fromErr != nil || untilErr != nil || asOfErr != nil ||
		!from.Before(until) || until.After(asOf) {
		return routeHealthSuggestionReport{}, errors.New("route usage identity, coverage, time window or bounds are invalid")
	}
	seen := map[string]bool{}
	zero := int64(0)
	for _, row := range usage.Routes {
		path, ok := strings.CutPrefix(row.Route, row.Method+" ")
		if !ok || row.Requests <= 0 || row.IdentifiedRequests < 0 || row.AnonymousRequests < 0 ||
			row.UnresolvedIdentityRequests < 0 || row.ConsumerCount < 0 || row.PlatformTenantCount < 0 ||
			row.OtherCustomerRequests < 0 || len(row.Customers) > usage.CustomersLimit {
			return routeHealthSuggestionReport{}, errors.New("route usage row is invalid")
		}
		selector := api.RouteHealthRoute{Method: row.Method, Path: path}
		if err := routehealth.Validate(api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{selector}}); err != nil {
			return routeHealthSuggestionReport{}, errors.New("route usage contains an unsupported method or path")
		}
		key := selector.Method + "\x00" + selector.Path
		if seen[key] {
			return routeHealthSuggestionReport{}, errors.New("route usage contains duplicate route labels")
		}
		seen[key] = true
		last, err := time.Parse(time.RFC3339Nano, row.LastObservedAt)
		if err != nil || last.Before(from) || !last.Before(until) {
			return routeHealthSuggestionReport{}, errors.New("route usage has an invalid last observation time")
		}
		for _, customer := range row.Customers {
			if customer.Requests < 0 {
				return routeHealthSuggestionReport{}, errors.New("route usage has invalid customer counts")
			}
		}
	}
	// Validated rows rank identically to apid's default seeding (ADR-942).
	candidates := routehealth.RankRouteUsage(usage.Routes, groupBy)
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	report := routeHealthSuggestionReport{
		Version: routeHealthSuggestionVersion, App: slug, DeploymentID: deployment,
		CustomerGroupBy: groupBy,
		RankedBy:        fmt.Sprintf("distinct %s groups descending, then observed requests descending, then method/path ascending", groupBy),
		Observation: routeHealthSuggestionObservation{
			From: usage.From, Until: usage.Until, AsOf: usage.AsOf, Coverage: usage.Coverage,
			WindowClamped: usage.WindowClamped, RoutesScanned: len(usage.Routes),
			RouteLimit: usage.RoutesLimit, RoutesTruncated: usage.RoutesTruncated,
		},
		SampleNote:  "Historical request totals do not establish canary sample readiness. Route health requires at least 20 requests per route on both deployments in each of two closed one-minute windows; aggregate usage cannot prove that per-window distribution.",
		Suggestions: []routeHealthSuggestion{}, Selectors: []api.RouteHealthRoute{},
	}
	for i, item := range candidates {
		assessment := "window_distribution_unknown"
		if item.Usage.Requests < api.RouteHealthMinRequests*int64(api.RouteHealthWindows) {
			assessment = "below_historical_two_window_request_floor"
		}
		selector := item.Selector
		report.Selectors = append(report.Selectors, selector)
		report.Suggestions = append(report.Suggestions, routeHealthSuggestion{
			Rank: i + 1, Selector: selector, Requests: item.Usage.Requests, CustomerGroupBy: groupBy,
			CustomerCount: item.CustomerCount, IdentifiedRequests: item.Usage.IdentifiedRequests,
			AnonymousRequests: item.Usage.AnonymousRequests, UnresolvedIdentityRequests: item.Usage.UnresolvedIdentityRequests,
			LastObservedAt: item.Usage.LastObservedAt, CustomerDetailsTruncated: item.Usage.CustomersTruncated,
			OtherCustomerRequests: item.Usage.OtherCustomerRequests, SampleAssessment: assessment,
		})
	}
	return report, nil
}

func renderRouteHealthSuggestions(w io.Writer, report routeHealthSuggestionReport, output string) {
	_, _ = fmt.Fprintf(w, "Route health suggestions for %s\nDeployment: %s\nObserved: %s to %s; coverage: %s\nRank: %s\n",
		report.App, report.DeploymentID, report.Observation.From, report.Observation.Until, report.Observation.Coverage, report.RankedBy)
	if report.Observation.WindowClamped {
		_, _ = fmt.Fprintln(w, "The requested lookback was clamped to the retained telemetry window.")
	}
	if report.Observation.RoutesTruncated {
		if report.ReleaseScope != nil {
			_, _ = fmt.Fprintf(w, "Observed route inventory was truncated at %d routes; %d rows were returned before release filtering.\n", report.Observation.RouteLimit, report.Observation.RoutesScanned)
		} else {
			_, _ = fmt.Fprintf(w, "The source inventory is bounded to its top %d routes; ranking covers only the %d returned routes.\n", report.Observation.RouteLimit, report.Observation.RoutesScanned)
		}
	}
	if scope := report.ReleaseScope; scope != nil {
		_, _ = fmt.Fprintf(w, "Release scope: preview %s; source binding %s; analysis %s; route mapping %s\n", scope.Preview, scope.SourceImpactStatus, scope.SourceAnalysisStatus, scope.SourceMappingStatus)
		_, _ = fmt.Fprintf(w, "Mapped source routes: %d; affected existing routes: %d; observed: %d; without matching observations: %d; unmatched source routes: %d; source issues: %d; added/removed skipped: %d\n",
			scope.MappedSourceRoutes, scope.AffectedSourceRoutes, scope.ObservedAffectedRoutes, scope.UnobservedAffectedRoutes, scope.UnmatchedSourceRoutes, scope.SourceIssueCount, scope.StructuralRoutesSkipped)
	}
	if len(report.Suggestions) == 0 {
		if report.ReleaseScope != nil {
			_, _ = fmt.Fprintln(w, "No source-affected route with matching observed requests qualified for a selector.")
		} else {
			_, _ = fmt.Fprintln(w, "No routes had observed requests in this window.")
		}
	}
	for _, suggestion := range report.Suggestions {
		_, _ = fmt.Fprintf(w, "\n%d. %s %s — %d %s groups, %d observed requests; last observed %s; sample: %s\n",
			suggestion.Rank, suggestion.Selector.Method, previewReportText(suggestion.Selector.Path), suggestion.CustomerCount,
			suggestion.CustomerGroupBy, suggestion.Requests, suggestion.LastObservedAt, suggestion.SampleAssessment)
		_, _ = fmt.Fprintf(w, "   Identity: %d identified, %d anonymous, %d unresolved requests\n",
			suggestion.IdentifiedRequests, suggestion.AnonymousRequests, suggestion.UnresolvedIdentityRequests)
		if suggestion.SourceChange != "" {
			_, _ = fmt.Fprintf(w, "   Source impact: %s (%s precision; %s route match)\n", suggestion.SourceChange, suggestion.SourcePrecision, suggestion.SourceMatch)
		}
	}
	for _, caveat := range report.Caveats {
		_, _ = fmt.Fprintf(w, "\nEvidence: %s\n", caveat)
	}
	if len(report.Suggestions) > 0 {
		_, _ = fmt.Fprintf(w, "\n%s\n", report.SampleNote)
	}
	if output != "" {
		_, _ = fmt.Fprintf(w, "\nSaved %d reviewable selectors to %s; pass that file to `gregale routes health set --routes`.\n", len(report.Selectors), previewReportText(output))
	}
}
