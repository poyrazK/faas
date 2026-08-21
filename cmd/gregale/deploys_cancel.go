// `gregale deploys cancel <id> [--reason <r>]` — ADR-124 user-facing
// cancel verb. Mirrors the `delayed-task cancel` shape from
// commands_delayed_task.go:67. The CLI is intentionally thin — the
// pgstore.CancelDeploymentTx orchestrator handles the row flip +
// build cascade, and the apid handler fires build_changed pg_notify
// per cascade-cancelled build row so builderd's LISTEN goroutine
// can call VM.Cancel.
//
// Idempotency: cancelling a row already in {cancelled, superseded,
// failed} returns ErrInvalidStateTransition → 409 with the canonical
// "deployment_cancel_not_cancellable" Problem. The CLI surfaces
// this as exit 1 + the server message; no special handling needed.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

const deploysCancelUsage = "usage: gregale deploys cancel <id> [--reason <user|auto_quota|auto_health|system>] [--app <slug>] [--json]"

func cmdDeploysCancel(args []string) int {
	fs := flag.NewFlagSet("deploys cancel", flag.ContinueOnError)
	reason := fs.String("reason", "user", "cancel reason (user|auto_quota|auto_health|system)")
	appSlug := fs.String("app", "", "app slug (required; used as IDOR-gate path segment)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *appSlug == "" {
		PrintUsage(os.Stderr, deploysCancelUsage+"   (--app is required)", "deploys")
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, deploysCancelUsage, "deploys")
		return 1
	}
	id := fs.Arg(0)
	if !deploymentIDPattern.MatchString(id) {
		PrintUsage(os.Stderr, deploysCancelUsage+"   (id is 32 hex chars)", "deploys")
		return 1
	}
	switch *reason {
	case "user", "auto_quota", "auto_health", "system":
		// valid
	default:
		return printErr("Invalid --reason", fmt.Errorf("--reason must be one of user|auto_quota|auto_health|system; got %q", *reason))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.CancelDeployment(context.Background(), *appSlug, id, *reason)
	if err != nil {
		return printErr("Cancel failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Deployment %s cancelled (reason=%s).", resp.ID, *reason)
	return 0
}
