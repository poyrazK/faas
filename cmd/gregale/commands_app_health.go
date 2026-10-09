package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

const subHealth = "health"

func cmdAppHealth(slug string, args []string) int {
	fs := newFlagSet("app health", flag.ContinueOnError)
	history := fs.Bool("history", false, "Show recorded health changes")
	limit := fs.Int("limit", api.AppHealthHistoryPageSize, "History page size")
	before := fs.String("before", "", "History cursor from the preceding page")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
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
	out, err := client.GetAppHealth(context.Background(), slug)
	if err != nil {
		return printErr("Health assessment failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "%s: %s (%s) — %s\n", slug, out.Status, out.Phase, out.Summary)
	_, _ = fmt.Fprintf(osStdout, "Evaluated: %s; scope: %s; valid for %ds\n", out.EvaluatedAt, out.Scope, out.ValidForSeconds)
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
