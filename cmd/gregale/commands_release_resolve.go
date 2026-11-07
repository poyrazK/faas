package main

// ADR-1448 amendment 1: record a database check after a release command
// whose outcome is unknown.

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDeploymentResolve records that the database was checked after a
// release command ended without an exit code, so later release commands of
// the app scope run again.
func cmdDeploymentResolve(args []string) int {
	fs := newFlagSet("deployment resolve", flag.ContinueOnError)
	app := fs.String("app", "", "app slug, to resolve vN revisions (defaults to linked project app)")
	note := fs.String("note", "", "what you checked in the database")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *note == "" {
		PrintUsage(os.Stderr, "usage: gregale release resolve <id|vN> --note TEXT [--app <slug>]", "deployment")
		return 1
	}
	req := api.RecordReleaseOutcomeCheckRequest{Note: *note}
	if err := req.Validate(); err != nil {
		return printErr("Invalid note", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	id, err := resolveDeploymentArg(ctx, client, *app, fs.Arg(0))
	if err != nil {
		return printErr("Could not resolve the deployment", err)
	}
	check, err := client.RecordReleaseOutcomeCheck(ctx, id, req)
	if err != nil {
		return printErr("Could not record the check", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(check))
	}
	_, _ = fmt.Fprintf(osStdout, "Recorded the check of release %s's command (%s). Later release commands run again.\n", check.DeploymentID, check.TaskStatus)
	return 0
}
