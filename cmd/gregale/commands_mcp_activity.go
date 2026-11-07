package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

const mcpActivityUsage = "usage: gregale mcp connection activity <id> | --self [--org SLUG] [--before CURSOR] [--limit N] [--json]"

// cmdMCPConnectionActivity lists an agent's write requests of the last 7 days
// (ADR-20261107220000), newest first.
func cmdMCPConnectionActivity(args []string) int {
	fs := newFlagSet("mcp-connections-activity", flag.ContinueOnError)
	self := fs.Bool("self", false, "the calling agent's own activity (with its agent key)")
	org := fs.String("org", "", "an agent acting in this organization, for its owners and admins")
	before := fs.String("before", "", "next_before cursor from an earlier page")
	limit := fs.Int("limit", 0, fmt.Sprintf("rows per page, 1 to %d (default %d)", api.MaxAgentActivityPage, api.DefaultAgentActivityPage))
	flags, positional := splitArgsForFlags(args, "self")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	id := ""
	switch {
	case *self && (len(positional) != 0 || *org != ""):
		return printErr("Agent activity", errors.New("--self takes no id and no --org; "+mcpActivityUsage))
	case *self:
		id = "self"
	case len(positional) == 1:
		id = positional[0]
	default:
		return printErr("Agent activity", errors.New("name one connection id, or --self; "+mcpActivityUsage))
	}
	if *limit < 0 || *limit > api.MaxAgentActivityPage {
		return printErr("Agent activity", fmt.Errorf("--limit must be between 1 and %d", api.MaxAgentActivityPage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	q := api.AgentActivityQuery{Before: *before, Limit: *limit}
	var out api.AgentActivityResponse
	if *org != "" {
		out, err = client.OrgAgentConnectionActivity(context.Background(), *org, id, q)
	} else {
		out, err = client.AgentConnectionActivity(context.Background(), id, q)
	}
	if err != nil {
		return printErr("Could not read the agent's activity", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	printAgentActivity(out)
	return 0
}

func printAgentActivity(out api.AgentActivityResponse) {
	if len(out.Activity) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No write requests in the last 7 days.")
		return
	}
	_, _ = fmt.Fprintf(osStdout, "%-20s  %-6s  %-6s  %-36s  %s\n", "TIME", "METHOD", "STATUS", "APPROVAL", "PATH")
	for _, e := range out.Activity {
		approval := e.ApprovalID
		if approval == "" {
			approval = "-"
		}
		_, _ = fmt.Fprintf(osStdout, "%-20s  %-6s  %-6d  %-36s  %s\n", oneLine(e.CreatedAt), e.Method, e.Status, approval, oneLine(e.Path))
	}
	if out.NextBefore != "" {
		_, _ = fmt.Fprintf(osStdout, "More: gregale mcp connection activity ... --before %s\n", out.NextBefore)
	}
}
