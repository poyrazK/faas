package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

const (
	routeHealthCorrelationVersion           = 1
	routeHealthCorrelationDefaultLimit      = 10
	routeHealthCorrelationMaxLimit          = 20
	routeHealthCorrelationMaxRoutesPerGroup = 10
	routeHealthCorrelationConcurrency       = 4
)

const routeHealthCorrelationCaveat = "A shared dependency type/kind with higher p95 in both windows is a correlation clue, not proof of root cause. Samples are bounded and observed only; dependency percentiles are independent and cannot be added to route p95."

type routeHealthCorrelationReport struct {
	Version                  int                              `json:"version"`
	App                      string                           `json:"app"`
	DeploymentID             string                           `json:"deployment_id"`
	StableDeploymentID       string                           `json:"stable_deployment_id,omitempty"`
	CheckedAt                time.Time                        `json:"checked_at"`
	Coverage                 string                           `json:"coverage"`
	Status                   string                           `json:"status"`
	Reason                   string                           `json:"reason,omitempty"`
	RoutesConsidered         int                              `json:"latency_routes_considered"`
	RoutesAnalyzed           int                              `json:"latency_routes_analyzed"`
	RoutesUnavailable        int                              `json:"latency_routes_unavailable"`
	UnavailableRoutes        []routeHealthCorrelationIssue    `json:"unavailable_routes"`
	EvidenceIncompleteRoutes int                              `json:"evidence_incomplete_routes"`
	RouteCoverage            []routeHealthCorrelationCoverage `json:"route_coverage"`
	GroupsTruncated          bool                             `json:"groups_truncated"`
	Groups                   []routeHealthCorrelationGroup    `json:"groups"`
	Caveat                   string                           `json:"caveat"`
}

type routeHealthCorrelationGroup struct {
	Type            string                        `json:"type"`
	Kind            string                        `json:"kind,omitempty"`
	AffectedRoutes  int                           `json:"affected_routes"`
	LargestDeltaMS  int64                         `json:"largest_observed_delta_ms"`
	RoutesTruncated bool                          `json:"routes_truncated"`
	Routes          []routeHealthCorrelationRoute `json:"routes"`
}

type routeHealthCorrelationRoute struct {
	Method        string                         `json:"method"`
	Path          string                         `json:"path"`
	LatencyStatus string                         `json:"latency_status"`
	Windows       []routeHealthCorrelationWindow `json:"windows"`
}

type routeHealthCorrelationWindow struct {
	Start               time.Time                      `json:"start"`
	End                 time.Time                      `json:"end"`
	CandidateP95MS      int64                          `json:"candidate_p95_ms"`
	StableP95MS         int64                          `json:"stable_p95_ms"`
	P95DeltaMS          int64                          `json:"p95_delta_ms"`
	CandidateCalls      int64                          `json:"candidate_calls"`
	StableCalls         int64                          `json:"stable_calls"`
	CandidateErrorCalls int64                          `json:"candidate_error_calls"`
	StableErrorCalls    int64                          `json:"stable_error_calls"`
	CandidateExample    *routeHealthCorrelationExample `json:"candidate_example,omitempty"`
}

type routeHealthCorrelationExample struct {
	TelemetryID    string `json:"telemetry_id"`
	TraceAvailable bool   `json:"trace_available"`
}

type routeHealthCorrelationIssue struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type routeHealthCorrelationCoverage struct {
	Method                    string `json:"method"`
	Path                      string `json:"path"`
	CandidateSampledRows      int64  `json:"candidate_sampled_rows"`
	StableSampledRows         int64  `json:"stable_sampled_rows"`
	CandidateSamplesTruncated bool   `json:"candidate_samples_truncated"`
	StableSamplesTruncated    bool   `json:"stable_samples_truncated"`
	CandidateMissingSpanRows  int64  `json:"candidate_missing_span_rows"`
	StableMissingSpanRows     int64  `json:"stable_missing_span_rows"`
	CandidateSpansTruncated   bool   `json:"candidate_spans_truncated"`
	StableSpansTruncated      bool   `json:"stable_spans_truncated"`
	CandidateTimingIncomplete bool   `json:"candidate_timing_incomplete"`
	StableTimingIncomplete    bool   `json:"stable_timing_incomplete"`
	DependencyGroupsTruncated bool   `json:"dependency_groups_truncated"`
}

type routeHealthCorrelationTarget struct {
	index   int
	finding api.RouteHealthFinding
}

type routeHealthCorrelationResult struct {
	method        string
	path          string
	investigation api.RouteHealthInvestigation
	err           error
	invalid       error
}

func cmdRoutesHealthCorrelate(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("routes health correlate", flag.ContinueOnError)
	deployment := fs.String("deployment", "", "candidate deployment UUID")
	limit := fs.Int("limit", routeHealthCorrelationDefaultLimit, "number of shared dependency groups to show (maximum 20)")
	output := fs.String("out", "", "save correlation JSON to a new file")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) || !canonicalRouteHealthID(*deployment) || *limit < 1 || *limit > routeHealthCorrelationMaxLimit || rejectUnexpectedFlagArgs(fs) {
		return printErr("Invalid route health correlation", errors.New("supply an app, canonical --deployment UUID, and a --limit from 1 to 20"))
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
	health, err := client.GetRouteHealthReport(ctx, positional[0], *deployment)
	if err != nil {
		return printErr("Could not read route health", err)
	}
	if err := validateRouteHealthReport(health, *deployment); err != nil {
		return printErr("Invalid route health response", err)
	}
	if err := routehealth.ValidateClientErrors(health); err != nil {
		return printErr("Invalid client error evidence", err)
	}
	correlation := correlateRouteHealthDependencies(ctx, client, positional[0], health, *limit)
	if *output != "" {
		body, err := json.MarshalIndent(correlation, "", "  ")
		if err != nil {
			return printErr("Could not encode route correlation", err)
		}
		if err := writeRoutePolicyPlan(*output, append(body, '\n')); err != nil {
			return printErr("Could not save route correlation", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(correlation))
	}
	renderRouteHealthCorrelation(correlation, positional[0])
	return 0
}

func correlateRouteHealthDependencies(ctx context.Context, client *api.Client, slug string, health api.RouteHealthReport, limit int) routeHealthCorrelationReport {
	report := routeHealthCorrelationReport{
		Version: routeHealthCorrelationVersion, App: slug, DeploymentID: health.DeploymentID, StableDeploymentID: health.StableDeploymentID,
		CheckedAt: health.CheckedAt, Coverage: "observed_only", Status: "no_shared_dependency_slowdown_observed",
		Groups: []routeHealthCorrelationGroup{}, UnavailableRoutes: []routeHealthCorrelationIssue{}, RouteCoverage: []routeHealthCorrelationCoverage{}, Caveat: routeHealthCorrelationCaveat,
	}
	targets := make([]routeHealthCorrelationTarget, 0, len(health.Routes))
	for _, finding := range health.Routes {
		if routehealth.LatencyEnabled(finding.CheckLatency, finding.MaxP95MS) {
			targets = append(targets, routeHealthCorrelationTarget{index: len(targets), finding: finding})
		}
	}
	report.RoutesConsidered = len(targets)
	if health.StableDeploymentID == "" {
		report.Status, report.Reason = "unavailable", "stable_deployment_unavailable"
		report.RoutesUnavailable = len(targets)
		for _, target := range targets {
			report.UnavailableRoutes = append(report.UnavailableRoutes, routeHealthCorrelationIssue{Method: target.finding.Method, Path: target.finding.Path, Reason: "stable_deployment_unavailable"})
		}
		return report
	}
	if len(targets) == 0 {
		report.Status, report.Reason = "no_latency_routes", "configure_latency_checks_on_routes"
		return report
	}
	results := queryRouteHealthCorrelationTargets(ctx, client, slug, health.DeploymentID, targets)
	groups := map[string]*routeHealthCorrelationGroup{}
	for _, result := range results {
		if result.err != nil {
			report.RoutesUnavailable++
			report.UnavailableRoutes = append(report.UnavailableRoutes, routeHealthCorrelationIssue{Method: result.method, Path: result.path, Reason: "investigation_unavailable"})
			continue
		}
		if result.invalid != nil {
			// Withhold malformed route evidence and make the aggregate explicitly incomplete.
			report.RoutesUnavailable++
			report.UnavailableRoutes = append(report.UnavailableRoutes, routeHealthCorrelationIssue{Method: result.method, Path: result.path, Reason: "invalid_investigation_response"})
			continue
		}
		if !routeHealthCorrelationSnapshotMatches(health, result.investigation.Report) {
			report.RoutesUnavailable++
			report.UnavailableRoutes = append(report.UnavailableRoutes, routeHealthCorrelationIssue{Method: result.method, Path: result.path, Reason: "health_snapshot_changed"})
			continue
		}
		report.RoutesAnalyzed++
		coverage := routeHealthCorrelationCoverageFor(result.investigation)
		report.RouteCoverage = append(report.RouteCoverage, coverage)
		if routeHealthCorrelationCoverageIncomplete(coverage) {
			report.EvidenceIncompleteRoutes++
		}
		for key, route := range repeatedDependencySlowdowns(result.investigation) {
			group := groups[key]
			if group == nil {
				group = &routeHealthCorrelationGroup{Type: keyType(key), Kind: keyKind(key), Routes: []routeHealthCorrelationRoute{}}
				groups[key] = group
			}
			group.Routes = append(group.Routes, route)
			group.AffectedRoutes++
			for _, window := range route.Windows {
				if window.P95DeltaMS > group.LargestDeltaMS {
					group.LargestDeltaMS = window.P95DeltaMS
				}
			}
		}
	}
	for _, group := range groups {
		if group.AffectedRoutes >= 2 {
			sort.Slice(group.Routes, func(i, j int) bool {
				if group.Routes[i].Method != group.Routes[j].Method {
					return group.Routes[i].Method < group.Routes[j].Method
				}
				return group.Routes[i].Path < group.Routes[j].Path
			})
			if len(group.Routes) > routeHealthCorrelationMaxRoutesPerGroup {
				group.RoutesTruncated = true
				group.Routes = group.Routes[:routeHealthCorrelationMaxRoutesPerGroup]
			}
			report.Groups = append(report.Groups, *group)
		}
	}
	sort.Slice(report.Groups, func(i, j int) bool {
		if report.Groups[i].AffectedRoutes != report.Groups[j].AffectedRoutes {
			return report.Groups[i].AffectedRoutes > report.Groups[j].AffectedRoutes
		}
		if report.Groups[i].LargestDeltaMS != report.Groups[j].LargestDeltaMS {
			return report.Groups[i].LargestDeltaMS > report.Groups[j].LargestDeltaMS
		}
		if report.Groups[i].Type != report.Groups[j].Type {
			return report.Groups[i].Type < report.Groups[j].Type
		}
		return report.Groups[i].Kind < report.Groups[j].Kind
	})
	if len(report.Groups) > limit {
		report.GroupsTruncated = true
		report.Groups = report.Groups[:limit]
	}
	if report.RoutesUnavailable > 0 {
		report.Status, report.Reason = "incomplete", "some_latency_routes_unavailable_or_snapshot_changed"
	} else if report.EvidenceIncompleteRoutes > 0 {
		report.Status, report.Reason = "incomplete", "latency_diagnostic_samples_incomplete"
	} else if len(report.Groups) > 0 {
		report.Status = "shared_dependency_slowdown_observed"
	}
	return report
}

func routeHealthCorrelationCoverageFor(investigation api.RouteHealthInvestigation) routeHealthCorrelationCoverage {
	coverage := routeHealthCorrelationCoverage{Method: investigation.Selection.Method, Path: investigation.Selection.Path}
	for _, window := range investigation.Windows {
		if window.Diagnostics == nil {
			coverage.CandidateTimingIncomplete, coverage.StableTimingIncomplete = true, true
			continue
		}
		d := window.Diagnostics
		coverage.CandidateSampledRows += d.Candidate.SampledRows
		coverage.StableSampledRows += d.Stable.SampledRows
		coverage.CandidateSamplesTruncated = coverage.CandidateSamplesTruncated || d.Candidate.SamplesTruncated
		coverage.StableSamplesTruncated = coverage.StableSamplesTruncated || d.Stable.SamplesTruncated
		coverage.CandidateMissingSpanRows += d.Candidate.MissingSpanRows
		coverage.StableMissingSpanRows += d.Stable.MissingSpanRows
		coverage.CandidateSpansTruncated = coverage.CandidateSpansTruncated || d.Candidate.SpansTruncated
		coverage.StableSpansTruncated = coverage.StableSpansTruncated || d.Stable.SpansTruncated
		coverage.CandidateTimingIncomplete = coverage.CandidateTimingIncomplete || d.Candidate.TimingIncomplete
		coverage.StableTimingIncomplete = coverage.StableTimingIncomplete || d.Stable.TimingIncomplete
		coverage.DependencyGroupsTruncated = coverage.DependencyGroupsTruncated || d.DependenciesTruncated
	}
	return coverage
}

func routeHealthCorrelationCoverageIncomplete(coverage routeHealthCorrelationCoverage) bool {
	return coverage.CandidateSamplesTruncated || coverage.StableSamplesTruncated || coverage.CandidateMissingSpanRows > 0 || coverage.StableMissingSpanRows > 0 ||
		coverage.CandidateSpansTruncated || coverage.StableSpansTruncated || coverage.CandidateTimingIncomplete || coverage.StableTimingIncomplete || coverage.DependencyGroupsTruncated
}

func queryRouteHealthCorrelationTargets(ctx context.Context, client *api.Client, slug, deployment string, targets []routeHealthCorrelationTarget) []routeHealthCorrelationResult {
	results := make([]routeHealthCorrelationResult, len(targets))
	workers := min(routeHealthCorrelationConcurrency, len(targets))
	jobs := make(chan routeHealthCorrelationTarget)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for target := range jobs {
				opts := api.RouteHealthInvestigationOptions{Method: target.finding.Method, Path: target.finding.Path, Signal: "latency"}
				investigation, err := client.GetRouteHealthInvestigation(ctx, slug, deployment, opts)
				result := routeHealthCorrelationResult{method: target.finding.Method, path: target.finding.Path, investigation: investigation, err: err}
				if err == nil {
					result.invalid = validateCLIInvestigation(investigation, opts, slug, deployment)
				}
				results[target.index] = result
			}
		}()
	}
	for _, target := range targets {
		jobs <- target
	}
	close(jobs)
	wait.Wait()
	return results
}

func routeHealthCorrelationSnapshotMatches(expected, actual api.RouteHealthReport) bool {
	if expected.AppID != actual.AppID || expected.DeploymentID != actual.DeploymentID || !strings.EqualFold(expected.CandidateCommitSHA, actual.CandidateCommitSHA) ||
		expected.StableDeploymentID != actual.StableDeploymentID || !strings.EqualFold(expected.StableCommitSHA, actual.StableCommitSHA) || expected.Revision != actual.Revision ||
		expected.Mode != actual.Mode || expected.OnRegression != actual.OnRegression || expected.CanaryStep != actual.CanaryStep ||
		expected.MinimumRequests != actual.MinimumRequests || expected.MinimumLatencyRequests != actual.MinimumLatencyRequests ||
		(expected.ObservationAnchor == nil) != (actual.ObservationAnchor == nil) || len(expected.Routes) == 0 || len(actual.Routes) == 0 {
		return false
	}
	if expected.ObservationAnchor != nil && !expected.ObservationAnchor.Equal(*actual.ObservationAnchor) {
		return false
	}
	first := expected.Routes[0].Windows
	actualWindows := actual.Routes[0].Windows
	if len(actualWindows) != len(first) {
		return false
	}
	for i := range first {
		if !actualWindows[i].Start.Equal(first[i].Start) || !actualWindows[i].End.Equal(first[i].End) {
			return false
		}
	}
	return true
}

func repeatedDependencySlowdowns(investigation api.RouteHealthInvestigation) map[string]routeHealthCorrelationRoute {
	type windowPairs [api.RouteHealthWindows]*api.RouteHealthDependencyComparison
	perKey := map[string]windowPairs{}
	for windowIndex, window := range investigation.Windows {
		if window.Diagnostics == nil {
			continue
		}
		for index := range window.Diagnostics.Dependencies {
			dependency := &window.Diagnostics.Dependencies[index]
			key := routeHealthDependencyKey(dependency.Type, dependency.Kind)
			pairs := perKey[key]
			pairs[windowIndex] = dependency
			perKey[key] = pairs
		}
	}
	out := map[string]routeHealthCorrelationRoute{}
	for key, pairs := range perKey {
		if pairs[0] == nil || pairs[1] == nil || !positiveComparableDependencyDelta(pairs[0]) || !positiveComparableDependencyDelta(pairs[1]) {
			continue
		}
		route := routeHealthCorrelationRoute{
			Method: investigation.Selection.Method, Path: investigation.Selection.Path, LatencyStatus: investigation.Finding.LatencyStatus,
			Windows: []routeHealthCorrelationWindow{},
		}
		for index, dependency := range pairs {
			window := investigation.Windows[index]
			entry := routeHealthCorrelationWindow{
				Start: window.Start, End: window.End,
				CandidateP95MS: *dependency.Candidate.P95MS, StableP95MS: *dependency.Stable.P95MS, P95DeltaMS: *dependency.P95DeltaMS,
				CandidateCalls: dependency.Candidate.RepresentedCalls, StableCalls: dependency.Stable.RepresentedCalls,
				CandidateErrorCalls: dependency.Candidate.ErrorCalls, StableErrorCalls: dependency.Stable.ErrorCalls,
				CandidateExample: firstRouteHealthCorrelationExample(dependency.Candidate.Examples),
			}
			route.Windows = append(route.Windows, entry)
		}
		out[key] = route
	}
	return out
}

func positiveComparableDependencyDelta(dependency *api.RouteHealthDependencyComparison) bool {
	return dependency != nil && dependency.Status == "compared" && dependency.Candidate.P95MS != nil && dependency.Stable.P95MS != nil && dependency.P95DeltaMS != nil && *dependency.P95DeltaMS > 0
}

func firstRouteHealthCorrelationExample(examples []api.RouteHealthInvestigationExample) *routeHealthCorrelationExample {
	if len(examples) == 0 {
		return nil
	}
	selected := examples[0]
	for _, example := range examples {
		if example.TraceID != "" {
			selected = example
			break
		}
	}
	return &routeHealthCorrelationExample{TelemetryID: selected.TelemetryID, TraceAvailable: selected.TraceID != ""}
}

func routeHealthDependencyKey(typ, kind string) string { return typ + "\x00" + kind }

func keyType(key string) string {
	typ, _, _ := strings.Cut(key, "\x00")
	return typ
}

func keyKind(key string) string {
	_, kind, _ := strings.Cut(key, "\x00")
	return kind
}

func renderRouteHealthCorrelation(report routeHealthCorrelationReport, slug string) {
	_, _ = fmt.Fprintf(osStdout, "Route dependency correlation: %s", report.Status)
	if report.Reason != "" {
		_, _ = fmt.Fprintf(osStdout, " (%s)", report.Reason)
	}
	_, _ = fmt.Fprintf(osStdout, "\nCandidate: %s; stable: %s\nLatency routes: %d considered, %d analyzed, %d unavailable; sampled evidence incomplete on %d routes; coverage: observed only\n", report.DeploymentID, report.StableDeploymentID, report.RoutesConsidered, report.RoutesAnalyzed, report.RoutesUnavailable, report.EvidenceIncompleteRoutes)
	for _, group := range report.Groups {
		kind := group.Kind
		if kind == "" {
			kind = "unspecified"
		}
		_, _ = fmt.Fprintf(osStdout, "\n%s/%s: shared across %d routes; largest observed per-window delta +%dms", group.Type, previewReportText(kind), group.AffectedRoutes, group.LargestDeltaMS)
		if group.RoutesTruncated {
			_, _ = fmt.Fprintf(osStdout, " (showing first %d routes)", len(group.Routes))
		}
		_, _ = fmt.Fprintln(osStdout)
		for _, route := range group.Routes {
			_, _ = fmt.Fprintf(osStdout, "  %s %s (route latency %s)\n", route.Method, previewReportText(route.Path), route.LatencyStatus)
			for _, window := range route.Windows {
				_, _ = fmt.Fprintf(osStdout, "    %s–%s: candidate %dms; stable %dms; change +%dms; calls %d/%d; errors %d/%d\n", window.Start.UTC().Format("15:04:05Z"), window.End.UTC().Format("15:04:05Z"), window.CandidateP95MS, window.StableP95MS, window.P95DeltaMS, window.CandidateCalls, window.StableCalls, window.CandidateErrorCalls, window.StableErrorCalls)
				if window.CandidateExample != nil {
					_, _ = fmt.Fprintf(osStdout, "      gregale debug requests inspect %s %s\n", slug, window.CandidateExample.TelemetryID)
					if window.CandidateExample.TraceAvailable {
						_, _ = fmt.Fprintf(osStdout, "      gregale debug requests trace %s %s\n", slug, window.CandidateExample.TelemetryID)
					}
				}
			}
		}
	}
	for _, coverage := range report.RouteCoverage {
		_, _ = fmt.Fprintf(osStdout, "\nCoverage %s %s: sampled rows %d/%d; missing spans %d/%d; samples truncated %t/%t; spans truncated %t/%t; span timing incomplete %t/%t; dependency groups truncated %t\n", coverage.Method, previewReportText(coverage.Path), coverage.CandidateSampledRows, coverage.StableSampledRows, coverage.CandidateMissingSpanRows, coverage.StableMissingSpanRows, coverage.CandidateSamplesTruncated, coverage.StableSamplesTruncated, coverage.CandidateSpansTruncated, coverage.StableSpansTruncated, coverage.CandidateTimingIncomplete, coverage.StableTimingIncomplete, coverage.DependencyGroupsTruncated)
	}
	if report.GroupsTruncated {
		_, _ = fmt.Fprintln(osStdout, "\nAdditional shared dependency groups were omitted by --limit.")
	}
	for _, unavailable := range report.UnavailableRoutes {
		_, _ = fmt.Fprintf(osStdout, "\nUnavailable: %s %s (%s)\n", unavailable.Method, previewReportText(unavailable.Path), unavailable.Reason)
	}
	_, _ = fmt.Fprintf(osStdout, "\n%s\n", report.Caveat)
}
