// `gregale deploys clear <id>` — ADR-124 single-deployment soft
// delete. Free-allowed (safety valve; no plan gate). Live
// deployments return 409 with the cancel-live hint pointing at
// `gregale deploys rollback` — protecting the §6.2 INV 3 invariant
// (always a live snapshot OR a cold-bootable rootfs).
//
// Soft delete: status unchanged (admin audit trail). The row's
// deleted_at / deleted_by_principal columns are stamped by
// state.ClearDeployment.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

const deploysClearUsage = "usage: gregale deploys clear <id> [--app <slug>] [--force] [--json]"

func cmdDeploysClear(args []string) int {
	fs := flag.NewFlagSet("deploys clear", flag.ContinueOnError)
	appSlug := fs.String("app", "", "app slug (required; used as IDOR-gate path segment)")
	force := fs.Bool("force", false, "skip the confirmation prompt")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *appSlug == "" {
		PrintUsage(os.Stderr, deploysClearUsage+"   (--app is required)", "deploys")
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, deploysClearUsage, "deploys")
		return 1
	}
	id := fs.Arg(0)
	if !deploymentIDPattern.MatchString(id) {
		PrintUsage(os.Stderr, deploysClearUsage+"   (id is 32 hex chars)", "deploys")
		return 1
	}
	if !*force {
		fmt.Fprintf(os.Stderr, "Clear deployment %s from app %s? This hides the row from the list. [y/N] ", id, *appSlug)
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
	if err := client.ClearDeployment(context.Background(), id); err != nil {
		return printErr("Clear failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"id": id, "deleted": true}))
	}
	PrintOK(osStdout, "Deployment %s cleared.", id)
	return 0
}
