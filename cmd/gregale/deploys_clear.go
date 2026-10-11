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

const deploysClearUsage = "usage: gregale deploys clear <id> [--app <slug>] [--dry-run] [--force] [--json]"

func cmdDeploysClear(args []string) int {
	fs := newFlagSet("deploys clear", flag.ContinueOnError)
	appSlug := fs.String("app", "", "app slug (defaults to the linked project, or the deployment's own app)")
	dryRun := fs.Bool("dry-run", false, "preview deletion without changing resources")
	force := fs.Bool("force", false, "skip the confirmation prompt")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, deploysClearUsage, "deploys")
		return 1
	}
	id := fs.Arg(0)
	if !validDeploymentRef(id) {
		PrintUsage(os.Stderr, deploysClearUsage+"   (id is 32 hex chars)", "deploys")
		return 1
	}
	if code := requireAutomationConfirmation(*force || *dryRun, "--force"); code != 0 {
		return code
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	id, err = resolveDeploymentArg(context.Background(), client, *appSlug, id)
	if err != nil {
		return printErr("Could not resolve deployment", err)
	}
	if *dryRun || !*force {
		preview, err := previewSingleDeployment(context.Background(), client, id)
		if err != nil {
			return printErr("Could not preview deployment cleanup", err)
		}
		if *dryRun {
			return writeDestructivePreview(preview)
		}
		if jsonOutput {
			return printErr("Confirmation required", fmt.Errorf("deployment cleanup requires --force in JSON mode; inspect --dry-run first"))
		}
		renderDestructivePreview(osStderr, preview)
		if _, err := fmt.Fprint(osStderr, "Clear this deployment? [y/N] "); err != nil {
			return printErr("Could not display confirmation", err)
		}
		answer, err := readConfirmationLine(osStdin)
		if err != nil || (answer != "y" && answer != "Y") {
			return printErr("Aborted", &exitErr{msg: "deployment cleanup was not confirmed", code: 130})
		}
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
