package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routerequirements"
)

func cmdRoutesResults(args []string) int {
	flags, positional := splitArgsForFlags(args, "refresh", "wait", "fail-on-requirements", "changes")
	fs := newFlagSet("routes results", flag.ContinueOnError)
	deployment := fs.String("deployment", "", "deployment UUID whose latest automatic check is read")
	revision := fs.Int64("expected-revision", 0, "require this current saved intent revision")
	changes := fs.Bool("changes", false, "show finding changes against prior known evidence")
	refresh := fs.Bool("refresh", false, "queue a check of current intent, capture and policy")
	wait := fs.Bool("wait", false, "wait for pending work to complete")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum lookup/wait duration")
	output := fs.String("out", "", "export the result to a new JSON file")
	fail := fs.Bool("fail-on-requirements", false, "require a completed, current and satisfied result")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	pinned := false
	fs.Visit(func(f *flag.Flag) { pinned = pinned || f.Name == "expected-revision" })
	id, err := uuid.Parse(*deployment)
	if len(positional) != 1 || !validCLISlug(positional[0]) || err != nil || id.String() != *deployment || *timeout <= 0 || *timeout > api.RouteCheckMaxWait || pinned && (*revision < 1 || *revision > api.RouteRequirementsMaxRevision) {
		return printErr("Invalid route result lookup", errors.New("usage: gregale routes results <slug> --deployment ID [--expected-revision N] [--refresh] [--wait] [--changes] [--timeout 2m] [--out PATH] [--fail-on-requirements] [--json]"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if *refresh {
		if err := client.RefreshAutomaticRouteCheck(ctx, positional[0], *deployment); err != nil {
			return printErr("Could not queue a route check", err)
		}
	}
	result, timedOut, err := readAutomaticRouteResult(ctx, client, positional[0], *deployment, *revision, pinned, *wait)
	if err != nil {
		return printErr("Could not read automatic route results", err)
	}
	if code := exportAutomaticRouteResult(result, *output); code != 0 {
		return code
	}
	if *changes && !jsonOutput {
		renderRouteCheckChanges(result)
	}
	if timedOut || *fail && (result.State != "complete" || result.Freshness != "current" || result.Check == nil || result.Check.Report.Status != "satisfied") {
		return 1
	}
	return 0
}

func readAutomaticRouteResult(ctx context.Context, client *Client, slug, deployment string, revision int64, pinned, wait bool) (api.AutomaticRouteCheck, bool, error) {
	var latest api.AutomaticRouteCheck
	for {
		result, err := client.GetAutomaticRouteCheck(ctx, slug, deployment)
		if err != nil {
			if latest.Version != 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return latest, true, nil
			}
			return latest, false, err
		}
		if err := validateAutomaticRouteResult(result, slug, deployment, revision, pinned); err != nil {
			return latest, false, err
		}
		latest = result
		if !wait || latest.State == "complete" {
			return latest, false, nil
		}
		timer := time.NewTimer(api.RouteCheckPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return latest, true, nil
		case <-timer.C:
		}
	}
}

func validateAutomaticRouteResult(result api.AutomaticRouteCheck, slug, deployment string, revision int64, pinned bool) error {
	if result.Version != 1 || result.App != slug || result.AppID == "" || result.DeploymentID != deployment || result.CurrentRequirementsRevision < 1 || result.CurrentRequirementsRevision > api.RouteRequirementsMaxRevision || len(result.CurrentRequirementsSHA256) != 64 || pinned && revision != result.CurrentRequirementsRevision || result.Attempts < 0 || result.Attempts > api.RouteCheckMaxAttempts {
		return errors.New("automatic result identity or current intent revision does not match the request")
	}
	switch result.State {
	case "pending", "running", "retrying", "complete":
	default:
		return errors.New("unrecognized automatic route check state")
	}
	if result.Changes != nil && (result.Check == nil || result.CheckID == "" || result.Changes.Version != 1 || result.Changes.CheckID != result.CheckID) {
		return errors.New("automatic changes are not bound to the completed check")
	}
	if result.Check == nil {
		if result.State == "complete" || result.Freshness != "unavailable" || len(result.StaleReasons) > 0 {
			return errors.New("automatic result has no verdict evidence")
		}
		return nil
	}
	if result.CheckedAt == nil || result.Check.AppID != result.AppID {
		return errors.New("stored route check identity or timestamp is unavailable")
	}
	if err := validateRouteCheckResult(*result.Check, result.Check.App, api.CheckRouteRequirementsRequest{DeploymentID: deployment}); err != nil {
		return err
	}
	if result.Freshness == "current" {
		if len(result.StaleReasons) != 0 || result.Check.App != slug || result.Check.RequirementsRevision != result.CurrentRequirementsRevision || result.Check.RequirementsSHA256 != result.CurrentRequirementsSHA256 {
			return errors.New("current automatic result is not bound to current intent")
		}
	} else if result.Freshness != "stale" || len(result.StaleReasons) == 0 {
		return errors.New("automatic result freshness is invalid")
	}
	for _, reason := range result.StaleReasons {
		if reason != "requirements_changed" && reason != "capture_changed" && reason != "configuration_changed" {
			return errors.New("unrecognized route check staleness reason")
		}
	}
	return nil
}

func exportAutomaticRouteResult(result api.AutomaticRouteCheck, path string) int {
	if path != "" {
		body, err := json.MarshalIndent(result, "", "  ")
		if err == nil {
			err = writeRoutePolicyPlan(path, append(body, '\n'))
		}
		if err != nil {
			return printErr("Could not export automatic route result", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "Automatic route check for %s\nDeployment: %s\nQueue: %s; freshness: %s\nCurrent requirements revision: %d\n", result.App, result.DeploymentID, result.State, result.Freshness, result.CurrentRequirementsRevision)
	if len(result.StaleReasons) > 0 {
		_, _ = fmt.Fprintf(osStdout, "Stale reasons: %s\n", strings.Join(result.StaleReasons, ", "))
	}
	if result.Check != nil {
		_, _ = fmt.Fprintf(osStdout, "Checked: %s; checked requirements revision: %d\n", result.CheckedAt.Format(time.RFC3339), result.Check.RequirementsRevision)
		report := routerequirements.WrapReport(result.Check.Report)
		renderPreviewRequirements(osStdout, &report, false)
	}
	return 0
}

func renderRouteCheckChanges(result api.AutomaticRouteCheck) {
	changes := result.Changes
	if changes == nil {
		_, _ = fmt.Fprintln(osStdout, "\nFinding changes: unavailable; refresh to retain a check with comparison evidence")
		return
	}
	_, _ = fmt.Fprintf(osStdout, "\nFinding changes for check %s: %s; freshness: %s\n", previewReportText(changes.CheckID), changes.Status, result.Freshness)
	totals := changes.Summary
	_, _ = fmt.Fprintf(osStdout, "New violations: %d; resolved: %d; changed: %d; unknown: %d; removed: %d; observed: %d\n", totals.NewlyViolated, totals.Resolved, totals.Changed, totals.Unknown, totals.Removed, totals.Observed)
	if changes.Status == "requirements_changed" {
		_, _ = fmt.Fprintln(osStdout, "Saved intent changed; findings establish a new baseline, not confirmed fixes.")
	}
	for _, row := range changes.Findings {
		_, _ = fmt.Fprintf(osStdout, "  %s %s %s / %s", row.Kind, row.Method, previewReportText(row.Path), previewReportText(row.Requirement))
		if row.Before != nil {
			_, _ = fmt.Fprintf(osStdout, "\n    before: %s; %s", row.Before.Status, previewReportText(row.Before.Actual))
		}
		if row.After != nil {
			_, _ = fmt.Fprintf(osStdout, "\n    after: %s; %s", row.After.Status, previewReportText(row.After.Actual))
		}
		_, _ = fmt.Fprintln(osStdout)
	}
	if changes.Truncated {
		_, _ = fmt.Fprintln(osStdout, "Finding detail truncated; summary counts cover the complete comparison.")
	}
}
