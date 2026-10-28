package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const subHealth = "health"

func cmdAppHealth(slug string, args []string) int {
	fs := newFlagSet("app health", flag.ContinueOnError)
	history := fs.Bool("history", false, "Show recorded health changes")
	limit := fs.Int("limit", api.AppHealthHistoryPageSize, "History page size")
	before := fs.String("before", "", "History cursor from the preceding page")
	wait := fs.Bool("wait", false, "wait for a fresh healthy default-scope assessment")
	check := fs.Bool("check", false, "exit nonzero unless a single assessment is fresh, healthy, and default-scope")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum health wait (requires --wait)")
	interval := fs.Duration("poll-interval", 2*time.Second, "health poll interval (requires --wait)")
	environment := fs.String("environment", "", "assess this project environment's serving releases instead of the default scope")
	// ADR-20261026145832: app health is the one place for an app's health;
	// --explain and --errors show what `inspect` shows.
	explain := fs.Bool("explain", false, "explain health from runtime, release, data and scaling")
	errorsFlag := fs.Bool("errors", false, "explain the latest failed deployment")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *explain || *errorsFlag {
		alone := true
		fs.Visit(func(f *flag.Flag) { alone = alone && (f.Name == "explain" || f.Name == "errors") })
		if !alone || *explain && *errorsFlag {
			return printErr("Invalid health options", fmt.Errorf("--explain and --errors each stand alone; drop the other health options"))
		}
		if *errorsFlag {
			return cmdInspectErrors(slug)
		}
		return cmdInspectSummary(slug)
	}
	var waitOptions, historyOptions bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "timeout", "poll-interval":
			waitOptions = true
		case "limit", "before":
			historyOptions = true
		}
	})
	if *environment != "" && (*history || historyOptions) {
		return printErr("Invalid health options", fmt.Errorf("--environment applies to a current assessment; history covers the default scope"))
	}
	if *environment != "" && !api.ValidProjectEnvironmentSlug(*environment) {
		return printErr("Invalid health options", fmt.Errorf("--environment must be a project environment slug"))
	}
	if *check && (*wait || *history || historyOptions || waitOptions) {
		return printErr("Invalid health check options", fmt.Errorf("--check cannot be combined with --wait, history options, --timeout, or --poll-interval"))
	}
	if (*wait && (*history || historyOptions)) || (waitOptions && !*wait) || (*wait && (*timeout <= 0 || *interval < time.Second || *interval > time.Hour)) {
		return printErr("Invalid health wait options", fmt.Errorf("--wait excludes history options; --timeout and --poll-interval require --wait, a positive timeout, and a poll interval between 1s and 1h"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *history {
		if *limit < 1 || *limit > api.AppHealthHistoryMaxPage {
			return printErr("Invalid history page size", fmt.Errorf("limit must be between 1 and %d", api.AppHealthHistoryMaxPage))
		}
		return printAppHealthHistory(client, slug, *limit, *before)
	}
	if *wait {
		return waitAppHealth(client, slug, *environment, *timeout, *interval)
	}
	ctx := context.Background()
	if *check {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	out, err := readAppHealth(ctx, client, slug, *environment)
	if err != nil {
		return printErr("Health assessment failed", err)
	}
	passed := appHealthCheckPassed(out, *environment, time.Now())
	if code := renderAppHealth(slug, out); code != 0 {
		return code
	}
	if *check && !passed {
		return printErr("Health check failed", fmt.Errorf("the assessment is not fresh, healthy, and %s-scope (status=%s; scope=%s; fresh=%t); inspect the returned assessment before proceeding", appHealthScope(*environment), compactInspectFailureText(out.Status), compactInspectFailureText(out.Scope), appHealthFresh(out, time.Now())))
	}
	return 0
}

func renderAppHealth(slug string, out api.AppHealthResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "%s: %s (%s) — %s\n", slug, out.Status, out.Phase, out.Summary)
	_, _ = fmt.Fprintf(osStdout, "Evaluated: %s; scope: %s; valid for %ds\n", out.EvaluatedAt, out.Scope, out.ValidForSeconds)
	renderAppHealthVerification(osStdout, slug, out)
	for _, check := range out.Checks {
		_, _ = fmt.Fprintf(osStdout, "  [%s] %s: %s\n", check.Status, check.Code, check.Detail)
		if check.Reason != "" {
			_, _ = fmt.Fprintf(osStdout, "    Reason: %s\n", check.Reason)
		}
		for _, finding := range check.Findings {
			_, _ = fmt.Fprintf(osStdout, "    [%s] %s: %s (%s)\n", finding.Status, finding.Source, finding.Detail, finding.Reason)
			_, _ = fmt.Fprintf(osStdout, "      Deployment: %s; replica: %s\n", finding.DeploymentID, finding.InstanceID)
			if finding.ObservedAt != "" {
				_, _ = fmt.Fprintf(osStdout, "      Recorded evidence: %s\n", finding.ObservedAt)
			}
		}
		if check.FindingsTruncated {
			_, _ = fmt.Fprintln(osStdout, "    Additional replica findings omitted; capacity counts include the complete scan.")
		}
		if check.Action != "" {
			_, _ = fmt.Fprintf(osStdout, "    Inspect: %s\n", check.Action)
		}
	}
	if out.Requests != nil {
		p := out.Requests.Policy
		_, _ = fmt.Fprintf(osStdout, "Request severity policy: at least %d requests and %d server errors; warning %.2f%%, unhealthy %.2f%%.\n", p.MinimumRequests, p.MinimumServerErrors, p.WarningErrorRatePct, p.UnhealthyErrorRatePct)
	}
	return 0
}

func printAppHealthHistory(client *api.Client, slug string, limit int, before string) int {
	out, err := client.ListAppHealthHistory(context.Background(), slug, limit, before)
	if err != nil {
		return printErr("Health history failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "%s: recorded health observations (default scope)\n", slug)
	if !out.CollectorFresh {
		_, _ = fmt.Fprintln(osStdout, "Background collection is unconfirmed; historical entries do not establish current health.")
	}
	for _, e := range out.Entries {
		_, _ = fmt.Fprintf(osStdout, "  %s [%s] %s (%s): %s\n", e.ObservedAt, e.Kind, e.Assessment.Status, e.Assessment.Phase, e.Assessment.Summary)
	}
	if len(out.Entries) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No retained health changes recorded.")
	}
	if out.NextCursor != "" {
		_, _ = fmt.Fprintf(osStdout, "Next page: --history --before %s\n", out.NextCursor)
	}
	_, _ = fmt.Fprintln(osStdout, "Times describe observations or evidence expiry, not exact incident start/end times.")
	return 0
}
