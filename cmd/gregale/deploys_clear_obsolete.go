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
	fs := newFlagSet("deploys clear-obsolete", flag.ContinueOnError)
	appSlug := fs.String("app", "", "app slug (required)")
	olderThan := fs.Duration("older-than", 168*time.Hour, "cutoff duration; rows older than this are eligible")
	dryRun := fs.Bool("dry-run", false, "report the count without modifying rows")
	force := fs.Bool("force", false, "skip the confirmation prompt")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *appSlug == "" {
		PrintUsage(os.Stderr, deploysClearObsoleteUsage, "deploys")
		return 1
	}
	if *olderThan < 0 {
		return printErr("Invalid --older-than", fmt.Errorf("--older-than must be >= 0; got %s", olderThan.String()))
	}
	if code := requireAutomationConfirmation(*force || *dryRun, "--force (or --dry-run)"); code != 0 {
		return code
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *dryRun || !*force {
		preview, err := previewObsoleteDeployments(context.Background(), client, *appSlug, *olderThan)
		if err != nil {
			return printErr("Could not preview obsolete deployment cleanup", err)
		}
		if *dryRun {
			return writeDestructivePreview(preview)
		}
		if jsonOutput {
			return printErr("Confirmation required", fmt.Errorf("obsolete cleanup requires --force in JSON mode; inspect --dry-run first"))
		}
		renderDestructivePreview(osStderr, preview)
		if _, err := fmt.Fprint(osStderr, "Clear eligible obsolete deployments? [y/N] "); err != nil {
			return printErr("Could not display confirmation", err)
		}
		answer, err := readConfirmationLine(osStdin)
		if err != nil || (answer != "y" && answer != "Y") {
			return printErr("Aborted", &exitErr{msg: "obsolete cleanup was not confirmed", code: 130})
		}
	}
	report, err := client.ClearObsoleteDeployments(context.Background(), *appSlug, *olderThan)
	if err != nil {
		return printErr("Clear-obsolete failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	PrintOK(osStdout, "Cleared %d obsolete deployment(s) older than %s from %s.", report.Count, report.OlderThan, report.AppSlug)
	return 0
}
