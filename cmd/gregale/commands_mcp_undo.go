package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const mcpUndoUsage = "usage: gregale mcp connection undo <id> [--since 2h | --entry ID ...] [--force] [--org SLUG] [--yes] [--json]"

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// cmdMCPConnectionUndo undoes what an agent changed (ADR-20261112080601): it
// prints the plan, then applies it once confirmed.
func cmdMCPConnectionUndo(args []string) int {
	fs := newFlagSet("mcp-connections-undo", flag.ContinueOnError)
	since := fs.String("since", "", "undo changes from this long ago (for example 2h or 30m) or since an RFC 3339 time; default 24h")
	var entries stringList
	fs.Var(&entries, "entry", "undo only this activity entry (repeatable)")
	force := fs.Bool("force", false, "also undo changes someone changed again since")
	org := fs.String("org", "", "an organization's agent, for its owners and admins")
	yes := fs.Bool("yes", false, "apply the plan without asking")
	planOnly := fs.Bool("plan", false, "print the plan and stop")
	flags, positional := splitArgsForFlags(args, "force", "yes", "plan")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 {
		return printErr("Agent undo", errors.New("name one connection id; "+mcpUndoUsage))
	}
	req := api.AgentUndoRequest{Entries: entries, Force: *force}
	if *since != "" {
		if len(entries) > 0 {
			return printErr("Agent undo", errors.New("use --since or --entry, not both"))
		}
		at, err := undoSince(*since, time.Now())
		if err != nil {
			return printErr("Agent undo", err)
		}
		req.Since = at
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	id, ctx := positional[0], context.Background()
	plan, err := planUndo(ctx, client, *org, id, req)
	if err != nil {
		return printErr("Could not plan the undo", err)
	}
	if *planOnly || plan.Undoable == 0 {
		if jsonOutput {
			return jsonOut(writeJSON(plan))
		}
		printUndoPlan(plan)
		return 0
	}
	if !jsonOutput {
		printUndoPlan(plan)
	}
	if !*yes {
		if code := confirmYesNo(fmt.Sprintf("Undo %d change(s)?", plan.Undoable), "--yes", "the undo"); code != 0 {
			return code
		}
	}
	req.PlanDigest = plan.PlanDigest
	result, err := applyUndo(ctx, client, *org, id, req)
	if err != nil {
		return printErr("Could not undo", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	printUndoResult(result)
	if result.Failed > 0 {
		return 1
	}
	return 0
}

// undoSince reads a duration back from now, or an RFC 3339 time.
func undoSince(value string, now time.Time) (string, error) {
	if d, err := time.ParseDuration(value); err == nil {
		if d <= 0 {
			return "", errors.New("--since takes a positive duration such as 2h")
		}
		return now.Add(-d).UTC().Format(time.RFC3339), nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	return "", fmt.Errorf("--since takes a duration such as 2h or an RFC 3339 time, got %q", value)
}

func planUndo(ctx context.Context, client *Client, org, id string, req api.AgentUndoRequest) (api.AgentUndoPlan, error) {
	if org != "" {
		return client.PlanOrgAgentUndo(ctx, org, id, req)
	}
	return client.PlanAgentUndo(ctx, id, req)
}

func applyUndo(ctx context.Context, client *Client, org, id string, req api.AgentUndoRequest) (api.AgentUndoResult, error) {
	if org != "" {
		return client.ApplyOrgAgentUndo(ctx, org, id, req)
	}
	return client.ApplyAgentUndo(ctx, id, req)
}

var undoStatusText = map[string]string{
	api.AgentUndoUndoable:     "will undo",
	api.AgentUndoNotUndoable:  "cannot undo",
	api.AgentUndoChangedSince: "changed since",
	api.AgentUndoUndone:       "already undone",
}

func printUndoPlan(plan api.AgentUndoPlan) {
	if len(plan.Items) == 0 {
		_, _ = fmt.Fprintf(osStdout, "The agent changed nothing since %s.\n", plan.Since)
		return
	}
	_, _ = fmt.Fprintf(osStdout, "The agent's changes since %s, newest first:\n", plan.Since)
	for _, item := range plan.Items {
		printUndoItem(item, undoStatusText[item.Status])
	}
	_, _ = fmt.Fprintf(osStdout, "%d of %d can be undone.\n", plan.Undoable, len(plan.Items))
}

var undoResultText = map[string]string{
	api.AgentUndoResultUndone:       "undone",
	api.AgentUndoResultChangedSince: "left: changed since",
	api.AgentUndoResultFailed:       "failed",
	api.AgentUndoResultSkipped:      "left",
}

func printUndoResult(result api.AgentUndoResult) {
	for _, item := range result.Items {
		if item.Result == "" {
			continue
		}
		printUndoItem(item, undoResultText[item.Result])
	}
	_, _ = fmt.Fprintf(osStdout, "Undid %d change(s); %d failed.\n", result.Undone, result.Failed)
}

func printUndoItem(item api.AgentUndoItem, status string) {
	_, _ = fmt.Fprintf(osStdout, "  %-15s %s  %s  (%s)\n", status, item.CreatedAt, item.Summary, item.ActivityID)
	switch {
	case item.Error != "":
		_, _ = fmt.Fprintf(osStdout, "  %15s %s\n", "", item.Error)
	case item.Reason != "":
		_, _ = fmt.Fprintf(osStdout, "  %15s %s\n", "", item.Reason)
	}
}
