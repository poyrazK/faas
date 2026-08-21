// `gregale deploys clear-obsolete [--app <slug>] [--older-than 168h]
// [--dry-run] [--force]` — ADR-124 bulk soft-delete for terminal
// rows (status ∈ {superseded, failed, cancelled}). Plan-gated
// (Free returns 402). Retention cap enforced inside the store so
// INV 3 (always a current deployment) stays satisfied.
//
// Defaults: --app required, --older-than 168h (7d, matching imaged
// nightly GC), --dry-run false.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

const deploysClearObsoleteUsage = "usage: gregale deploys clear-obsolete --app <slug> [--older-than 168h] [--dry-run] [--force] [--json]"

func cmdDeploysClearObsolete(args []string) int {
	fs := flag.NewFlagSet("deploys clear-obsolete", flag.ContinueOnError)
	appSlug := fs.String("app", "", "app slug (required)")
	olderThan := fs.Duration("older-than", 168*time.Hour, "cutoff duration; rows older than this are eligible")
	dryRun := fs.Bool("dry-run", false, "report the count without modifying rows")
	force := fs.Bool("force", false, "skip the confirmation prompt")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *appSlug == "" {
		PrintUsage(os.Stderr, deploysClearObsoleteUsage, "deploys")
		return 1
	}
	if *olderThan < 0 {
		return printErr("Invalid --older-than", fmt.Errorf("--older-than must be >= 0; got %s", olderThan.String()))
	}
	if !*force && !*dryRun {
		fmt.Fprintf(os.Stderr, "Clear all obsolete deployments older than %s for app %s? [y/N] ", olderThan.String(), *appSlug)
		var answer string
		if _, err := fmt.Scanln(&answer); err != nil || (answer != "y" && answer != "Y") {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return 1
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	// The SDK always sends older_than. For dry-run mode the server
	// still applies the cutoff but never writes; mirroring the
	// production call keeps the dry-run output comparable to the
	// real-run output (same shape, same count semantics).
	report, err := client.ClearObsoleteDeployments(context.Background(), *appSlug, *olderThan)
	if err != nil {
		return printErr("Clear-obsolete failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	if *dryRun {
		PrintOK(osStdout, "Would clear %d obsolete deployment(s) older than %s from %s.", report.Count, report.OlderThan, report.AppSlug)
		return 0
	}
	PrintOK(osStdout, "Cleared %d obsolete deployment(s) older than %s from %s.", report.Count, report.OlderThan, report.AppSlug)
	return 0
}
