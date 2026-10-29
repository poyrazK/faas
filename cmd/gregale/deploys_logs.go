package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/onebox-faas/faas/pkg/api"
)

const deploysLogsUsage = "usage: gregale release logs <id|vN> [--app SLUG] [--limit N]"

// cmdDeploysLogs prints one deployment's build and deploy log, whatever its
// status. `gregale logs --deployment` reads that log only for a deployment
// that never served (it shows runtime logs otherwise), so a successful
// build's log had no CLI command although the API keeps it (H10-5).
func cmdDeploysLogs(args []string) int {
	fs := newFlagSet("deploys logs", flag.ContinueOnError)
	appFlag := fs.String("app", "", "app slug; only needed to resolve a vN revision outside a linked project")
	limit := fs.Int("limit", 500, "most recent log lines to print (1-5000)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !validDeploymentRef(fs.Arg(0)) || *limit < 1 || *limit > 5000 {
		PrintUsage(os.Stderr, deploysLogsUsage, "deploys")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	id, err := resolveDeploymentArg(ctx, client, *appFlag, fs.Arg(0))
	if err != nil {
		return printErr("Could not resolve deployment", err)
	}
	body, err := client.StreamDeploymentLogs(ctx, id, nil, *limit, false)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) {
			renderAPIError(os.Stderr, ae)
			return exitCodeForStatus(ae.Problem.Status)
		}
		return printErr("Could not reach the API", err)
	}
	defer func() { _ = body.Close() }()
	dec := api.NewDecoder(body)
	dec.SetCloseFn(body.Close)
	defer func() { _ = dec.Close() }()
	printed := 0
	for e := range dec.Events() {
		if e.Event == "end" {
			break
		}
		var entry struct {
			Line string `json:"line"`
		}
		if e.Event == "log" && json.Unmarshal([]byte(e.Data), &entry) == nil && entry.Line != "" {
			_, _ = fmt.Fprintln(osStdout, entry.Line)
			printed++
		}
	}
	if printed == 0 {
		_, _ = fmt.Fprintf(osStderr, "deployment %s has no retained build or deploy log lines\n", id)
	}
	return 0
}
