package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func cmdRoutesHealthExplain(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("routes health explain", flag.ContinueOnError)
	deployment := fs.String("deployment", "", "candidate deployment UUID")
	decision := fs.String("decision", "", "read one saved decision UUID")
	before := fs.String("before", "", "page before a retained decision UUID")
	limit := fs.Int("limit", api.RouteHealthHistoryPageSize, "number of saved decisions in the timeline")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	limitProvided := false
	fs.Visit(func(f *flag.Flag) { limitProvided = limitProvided || f.Name == "limit" })
	if len(positional) != 1 || !validCLISlug(positional[0]) || !canonicalRouteHealthID(*deployment) ||
		*decision != "" && !canonicalRouteHealthID(*decision) || *before != "" && !canonicalRouteHealthID(*before) ||
		*limit < 1 || *limit > api.RouteHealthHistoryMaxPage || *decision != "" && (limitProvided || *before != "") {
		return printErr("Invalid saved route health lookup", errors.New("supply APP and --deployment UUID; use --decision UUID or --limit 1..10 with optional --before UUID"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.RouteCheckTimeout)
	defer cancel()
	if *decision != "" {
		entry, err := client.GetRouteHealthHistoryEntry(ctx, positional[0], *deployment, *decision)
		if err != nil {
			return printErr("Could not read saved route health decision", err)
		}
		if err := validateRouteHealthHistoryEntry(entry, *deployment); err != nil {
			return printErr("Invalid saved route health evidence", err)
		}
		if entry.ID != *decision {
			return printErr("Invalid saved route health evidence", errors.New("decision identity or evidence is inconsistent"))
		}
		if jsonOutput {
			return jsonOut(writeJSON(entry))
		}
		renderRouteHealthExplanation(entry, positional[0])
		return 0
	}
	page, err := client.ListRouteHealthHistory(ctx, positional[0], *deployment, *limit, *before)
	if err != nil {
		return printErr("Could not read route health decision history", err)
	}
	if err := validateRouteHealthHistoryPage(page, *deployment, *limit, *before); err != nil {
		return printErr("Invalid saved route health evidence", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	if len(page.Entries) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No saved route health decisions in this page. Evidence is recorded when a selected-route guard evaluates an advance or commits automatic recovery.")
		return 0
	}
	renderRouteHealthExplanation(page.Entries[0], positional[0])
	_, _ = fmt.Fprintln(osStdout, "\nSaved decision timeline (newest first):")
	for i, entry := range page.Entries {
		transition := ""
		if i+1 < len(page.Entries) {
			transition = routeHealthHistoryTransition(page.Entries[i+1], entry)
		}
		_, _ = fmt.Fprintf(osStdout, "  %s %s step %d revision %d: %s, %s %s\n", entry.CheckedAt.UTC().Format(time.RFC3339), entry.ID, entry.Report.CanaryStep, entry.Report.Revision, entry.Decision.Status, entry.Report.Status, transition)
	}
	if page.NextCursor != "" {
		_, _ = fmt.Fprintf(osStdout, "Next page: --before %s\n", page.NextCursor)
	}
	return 0
}

func canonicalRouteHealthID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id.String() == value
}

func validateRouteHealthHistoryEntry(entry api.RouteHealthHistoryEntry, deployment string) error {
	if entry.Version != api.RouteHealthHistoryVersion || entry.Policy != routehealth.HistoryPolicy() {
		return errors.New("unsupported saved health evaluation policy; update the CLI")
	}
	if !canonicalRouteHealthID(entry.ID) || entry.Decision.HistoryID != entry.ID || entry.CheckedAt.IsZero() ||
		!entry.CheckedAt.Equal(entry.Report.CheckedAt) || !entry.CheckedAt.Equal(entry.Decision.CheckedAt) ||
		entry.Source != "manual" && entry.Source != "worker" || entry.TrafficPercent < 0 || entry.TrafficPercent > 100 || entry.RequestedTrafficPercent < 0 || entry.RequestedTrafficPercent > 100 || len(entry.Report.Routes) == 0 {
		return errors.New("saved decision identity or transition is invalid")
	}
	if err := validateRouteHealthReport(entry.Report, deployment); err != nil {
		return err
	}
	expected := routehealth.Decision(entry.Report)
	if entry.Purpose != "" && entry.Purpose != "abort" {
		return errors.New("invalid saved health purpose")
	}
	if entry.Purpose == "abort" {
		if entry.Source != "worker" || entry.TrafficPercent <= 0 || entry.RequestedTrafficPercent != 0 || !routehealth.AbortEligible(entry.Report) {
			return errors.New("invalid saved health recovery evidence")
		}
		expected = routehealth.AbortDecision(entry.Report)
	}
	expected.HistoryID = entry.ID
	if entry.Decision != expected || entry.Report.Status == "healthy" && entry.Report.ObservationAnchor == nil {
		return errors.New("saved decision does not match captured route evidence")
	}
	return nil
}

func validateRouteHealthHistoryPage(page api.RouteHealthHistoryPage, deployment string, limit int, before string) error {
	if !canonicalRouteHealthID(page.AppID) || page.DeploymentID != deployment || page.Entries == nil || len(page.Entries) > limit {
		return errors.New("invalid saved decision page identity or size")
	}
	seen := map[string]bool{}
	for i, entry := range page.Entries {
		if err := validateRouteHealthHistoryEntry(entry, deployment); err != nil {
			return err
		}
		if entry.Report.AppID != page.AppID || seen[entry.ID] || entry.ID == before {
			return errors.New("saved decision crossed page identity or cursor")
		}
		if i > 0 {
			prior := page.Entries[i-1]
			if entry.CheckedAt.After(prior.CheckedAt) || entry.CheckedAt.Equal(prior.CheckedAt) && entry.ID >= prior.ID {
				return errors.New("saved decisions are not ordered newest first")
			}
		}
		seen[entry.ID] = true
	}
	if page.NextCursor != "" && (len(page.Entries) != limit || page.NextCursor != page.Entries[len(page.Entries)-1].ID || page.NextCursor == before) {
		return errors.New("invalid saved decision pagination cursor")
	}
	return nil
}

func routeHealthHistoryTransition(before, after api.RouteHealthHistoryEntry) string {
	if after.Purpose == "abort" {
		return "(automatic rollback committed)"
	}
	a, b := before.Report, after.Report
	anchorMatches := a.ObservationAnchor == nil && b.ObservationAnchor == nil || a.ObservationAnchor != nil && b.ObservationAnchor != nil && a.ObservationAnchor.Equal(*b.ObservationAnchor)
	if a.Revision != b.Revision || a.CanaryStep != b.CanaryStep || a.StableDeploymentID != b.StableDeploymentID || !anchorMatches {
		return "(observation context changed)"
	}
	if before.Decision.Status == "blocked" && after.Decision.Status == "allowed" && b.Status == "healthy" {
		return "(healthy evidence restored; advance allowed)"
	}
	if before.Decision.Status != "blocked" && after.Decision.Status == "blocked" {
		return "(advance held)"
	}
	return ""
}

func renderRouteHealthExplanation(entry api.RouteHealthHistoryEntry, appSlug string) {
	_, _ = fmt.Fprintf(osStdout, "Saved rollout decision %s: %s\nObserved at: %s; source: %s\nTraffic: %d%%; requested: %d%%\n", entry.ID, entry.Decision.Status, entry.CheckedAt.UTC().Format(time.RFC3339), entry.Source, entry.TrafficPercent, entry.RequestedTrafficPercent)
	if entry.Report.ObservationAnchor != nil {
		_, _ = fmt.Fprintf(osStdout, "Observation anchor: %s\n", entry.Report.ObservationAnchor.UTC().Format(time.RFC3339Nano))
	}
	_, _ = fmt.Fprintln(osStdout, "Saved evidence describes this decision; routes health report shows current observations.")
	switch entry.Decision.Status {
	case "aborted":
		_, _ = fmt.Fprintln(osStdout, "Confirmed critical route 5xx regression triggered automatic rollback; stable traffic was restored:")
	case "blocked":
		_, _ = fmt.Fprintln(osStdout, "Traffic was held because selected route evidence was regressed or unknown:")
	case "report_only":
		_, _ = fmt.Fprintln(osStdout, "The guard was in report mode; this evidence did not enforce a hold.")
	default:
		_, _ = fmt.Fprintln(osStdout, "Every selected route was healthy in both windows; this advance committed.")
	}
	for _, finding := range entry.Report.Routes {
		if finding.Status == "healthy" {
			continue
		}
		_, _ = fmt.Fprintf(osStdout, "  %s %s: %s\n", finding.Method, previewReportText(finding.Path), healthEvidenceReason(finding.Reason))
		for _, window := range finding.Windows {
			if window.LatencyStatus == "regressed" && window.Candidate.P95LatencyMS != nil && window.Stable.P95LatencyMS != nil {
				_, _ = fmt.Fprintf(osStdout, "    %s p95 %.1fms vs stable %.1fms", window.Start.UTC().Format(time.RFC3339), *window.Candidate.P95LatencyMS, *window.Stable.P95LatencyMS)
				if finding.MaxP95MS > 0 && strings.Contains(window.LatencyReason, "budget") {
					_, _ = fmt.Fprintf(osStdout, "; above %dms budget", finding.MaxP95MS)
				}
				_, _ = fmt.Fprintf(osStdout, ": %s\n", healthEvidenceReason(window.LatencyReason))
			}
			if window.Status == "unknown" {
				_, _ = fmt.Fprintf(osStdout, "    %s: %s\n", window.Start.UTC().Format(time.RFC3339), healthEvidenceReason(window.Reason))
			}
		}
	}
	_, _ = fmt.Fprintf(osStdout, "Saved thresholds: 5xx minimum %d requests, %d errors, %.0f%% rate, %.1fx stable and +%.0f percentage points; selected p95 minimum %d requests, %.1fx stable and +%.0fms.\n", entry.Policy.MinimumRequests, entry.Policy.MinimumErrors, entry.Policy.ErrorRateFloor*100, entry.Policy.ErrorRateFactor, entry.Policy.ErrorRateDelta*100, entry.Policy.MinLatencyRequests, entry.Policy.LatencyFactor, entry.Policy.LatencyDeltaMS)
	renderRouteHealthReport(entry.Report, appSlug)
}

func healthEvidenceReason(reason string) string {
	switch reason {
	case "consecutive_latency_violation":
		return "latency violated selected checks in both windows"
	case "consecutive_server_error_regression", "server_error_rate_increased":
		return "candidate 5xx rate increased beyond the saved comparison thresholds"
	case "latency_budget_exceeded":
		return "candidate p95 exceeded the absolute budget"
	case "latency_budget_and_regression":
		return "candidate p95 exceeded the budget and relative slowdown thresholds"
	case "p95_latency_increased":
		return "candidate p95 exceeded the relative slowdown thresholds"
	case "insufficient_requests", "insufficient_latency_requests":
		return "too few observed requests on at least one deployment in this window"
	case "latency_evidence_unavailable", "telemetry_unavailable", "telemetry_not_entitled":
		return "required telemetry evidence was unavailable"
	case "observation_window_not_elapsed":
		return "the window began before the current stage or configuration anchor"
	case "stable_deployment_ambiguous_or_missing":
		return "a unique serving stable deployment was unavailable"
	case "comparisons_incomplete_or_unsettled":
		return "the selected signals were missing, sparse or differed between windows"
	default:
		return previewReportText(reason)
	}
}
