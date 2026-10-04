package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Configuration sends no provider requests. The integration must already allow the selected route.
func cmdBindingsProbePolicy(args []string) int {
	fs := newFlagSet("bindings probe-policy", flag.ContinueOnError)
	method := fs.String("method", "GET", "safe HTTP method: GET or HEAD")
	path := fs.String("path", "", "provider path declared safe to probe")
	status := fs.Int("expect-status", 200, "expected successful response status")
	remove := fs.Bool("delete", false, "remove the configured probe")
	flags, pos := splitArgsForFlags(args)
	if fs.Parse(flags) != nil || len(pos) != 1 {
		return printErr("Invalid probe policy", fmt.Errorf("usage: gregale bindings probe-policy <integration-id> [--path /health --method GET --expect-status 200 | --delete]"))
	}
	id, err := uuid.Parse(pos[0])
	if err != nil {
		return printErr("Invalid integration ID", err)
	}
	policy := api.OutboundBindingProbePolicy{Method: *method, Path: *path, ExpectedStatus: *status}
	if *remove && (*path != "" || *method != "GET" || *status != 200) || !*remove && !policy.Valid() {
		return printErr("Invalid probe policy", fmt.Errorf("use GET/HEAD, a canonical path without query parameters, and a 2xx expected status"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *remove {
		if err = client.DeleteOutboundBindingProbePolicy(context.Background(), id.String()); err != nil {
			return printErr("Could not delete probe policy", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"integration_id": id.String(), "deleted": true}))
		}
		PrintOK(osStdout, "Outbound probe policy removed.")
		return 0
	}
	saved, err := client.SetOutboundBindingProbePolicy(context.Background(), id.String(), policy)
	if err != nil {
		return printErr("Could not configure probe policy", err)
	}
	if saved != policy {
		return printErr("Probe policy was not confirmed", fmt.Errorf("server did not confirm the requested outbound probe policy"))
	}
	if jsonOutput {
		return jsonOut(writeJSON(saved))
	}
	PrintOK(osStdout, "Configured outbound probe: %s %s, expected HTTP %d.", saved.Method, saved.Path, saved.ExpectedStatus)
	return 0
}
