package main

import (
	"context"
	"flag"
	"os"
)

func cmdDeploymentRetryHold(args []string) int {
	flags, pos := splitArgsForFlags(args)
	fs := newFlagSet("deployment retry-hold", flag.ContinueOnError)
	app := fs.String("app", "", "app slug, to resolve a vN revision")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 || !validDeploymentRef(pos[0]) {
		PrintUsage(os.Stderr, "usage: gregale deployment retry-hold <id|vN> [--app SLUG]", "deployment")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	id, err := resolveDeploymentArg(ctx, client, *app, pos[0])
	if err != nil {
		return printErr("Could not resolve deployment", err)
	}
	d, err := client.RetryDeploymentMCPTaskHold(ctx, id)
	if err != nil {
		return printErr("Could not schedule hold retry", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(d))
	}
	PrintOK(osStdout, "Promotion retry requested for deployment %s. Inspect with gregale deployment %s.", d.ID, d.ID)
	return 0
}
