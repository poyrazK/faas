package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDeploymentSummary renders the app-scoped release cockpit. The explicit
// --app flag keeps the slug-to-deployment ownership check visible to callers
// and avoids an extra account-wide app lookup in the CLI.
func cmdDeploymentSummary(args []string) int {
	flags, pos := splitArgsForFlags(args)
	fs := flag.NewFlagSet("deployment summary", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 || *app == "" || !validCLISlug(*app) || !deploymentIDPattern.MatchString(pos[0]) {
		PrintUsage(os.Stderr, "usage: gregale deployment summary <id> --app SLUG", "deployment")
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	summary, err := client.GetAppDeploymentSummary(context.Background(), *app, pos[0])
	if err != nil {
		return printErr("Could not fetch deployment summary", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}

	_, _ = fmt.Fprintf(osStdout, "deployment: %s (%s)\n", summary.Deployment.ID, summary.Deployment.Status)
	if summary.Previous == nil {
		_, _ = fmt.Fprintln(osStdout, "previous: none (initial deployment)")
	} else {
		_, _ = fmt.Fprintf(osStdout, "previous: %s (%s)\n", summary.Previous.ID, summary.Previous.Status)
	}
	if summary.RollbackTargetID == "" {
		_, _ = fmt.Fprintln(osStdout, "rollback_target: none")
	} else {
		_, _ = fmt.Fprintf(osStdout, "rollback_target: %s\n", summary.RollbackTargetID)
	}
	if len(summary.Changes) == 0 {
		_, _ = fmt.Fprintln(osStdout, "changes: none")
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "changes:")
	for _, change := range summary.Changes {
		_, _ = fmt.Fprintf(osStdout, "  %-18s %s -> %s\n", change.Field,
			formatSummaryValue(change.Before), formatSummaryValue(change.After))
	}
	return 0
}

func formatSummaryValue(value any) string {
	if value == nil {
		return "null"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(encoded)
}
