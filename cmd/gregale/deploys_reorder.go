// `gregale deploys reorder <id> --priority <0..1000>` — ADR-124
// queue reorder. Priority semantics: 0 = deploy immediately (top
// of queue), 100 = FIFO default, 1000 = background rebuild. Free
// plan returns 402 plan_reorder_disabled; Hobby/Pro/Scale allowed.
// Only DeployPending rows are reorderable — anything past the
// builderd claim path returns 409 deployment_reorder_not_pending.
package main

import (
	"context"
	"flag"
	"os"
)

const deploysReorderUsage = "usage: gregale deploys reorder <id> --priority <int 0..1000> [--json]"

func cmdDeploysReorder(args []string) int {
	fs := flag.NewFlagSet("deploys reorder", flag.ContinueOnError)
	priority := fs.Int("priority", -1, "new priority (0=deploy-immediately, 100=FIFO default, 1000=background)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *priority < 0 || *priority > 1000 {
		PrintUsage(os.Stderr, deploysReorderUsage+"   (--priority must be in [0, 1000])", "deploys")
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, deploysReorderUsage, "deploys")
		return 1
	}
	id := fs.Arg(0)
	if !deploymentIDPattern.MatchString(id) {
		PrintUsage(os.Stderr, deploysReorderUsage+"   (id is 32 hex chars)", "deploys")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ReorderDeployment(context.Background(), id, *priority)
	if err != nil {
		return printErr("Reorder failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Deployment %s reordered to priority=%d.", resp.ID, *priority)
	return 0
}
