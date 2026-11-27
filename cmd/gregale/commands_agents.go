package main

// `gregale agent run` (dispatched as `agents runs`; ADR-20261027090016
// amendment 2): list the calls a hosted agent run is waiting on, and approve
// or deny them. Hosted agents are the internal agents capability, so the
// command stays out of customer discovery until the capability is released
// to the account.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	agentsUsage            = "usage: gregale agent run <approval list|approve|deny> [args]"
	agentRunApprovalsUsage = "usage: gregale agent run approval list <run-id> [--json]"
	// agentRunArgumentsWidth caps the untrusted arguments summary in the
	// approvals table so one row stays on one terminal line.
	agentRunArgumentsWidth = 60
)

func cmdAgents(args []string) int {
	parent, _ := lookupCliCommand("agents")
	if len(args) == 0 {
		PrintUsage(os.Stderr, agentsUsage, "agents")
		return 1
	}
	if args[0] == "runs" {
		return cmdAgentsRuns(args[1:])
	}
	return printUnknownSubcommand(os.Stderr, "agents", parent, args[0])
}

func cmdAgentsRuns(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, agentsUsage, "agents")
		return 1
	}
	switch args[0] {
	case "approvals":
		return cmdAgentRunApprovals(args[1:])
	case api.AgentRunApprovalDecisionApprove, api.AgentRunApprovalDecisionDeny:
		return cmdAgentRunDecide(args[0], args[1:])
	}
	return printUnknownSubcommand(os.Stderr, "agents runs", nestedCliCommand("agents", "runs"), args[0])
}

func cmdAgentRunApprovals(args []string) int {
	fs := newFlagSet("agents runs approvals", flag.ContinueOnError)
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || fs.Arg(0) == "" {
		PrintUsage(os.Stderr, agentRunApprovalsUsage, "agents")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListAgentRunApprovals(context.Background(), fs.Arg(0))
	if err != nil {
		return printErr("Could not list the run's approvals", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if len(out.Items) == 0 {
		_, _ = fmt.Fprintf(osStdout, "Run %s has no approvals.\n", terminalSafe(fs.Arg(0)))
		return 0
	}
	printAgentRunApprovals(out.Items)
	return 0
}

func printAgentRunApprovals(items []api.AgentRunApproval) {
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tTOOL\tSTATE\tEXPIRES\tARGUMENTS")
	for _, a := range items {
		// The summary and tool name are agent text: one line, no escapes.
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", terminalSafe(a.ID), terminalSafe(terminalSafeLine(a.Tool, 40)),
			terminalSafe(a.State), a.ExpiresAt.UTC().Format(time.RFC3339),
			terminalSafe(terminalSafeLine(a.ArgumentsSummary, agentRunArgumentsWidth)))
	}
	_ = tw.Flush()
}

// cmdAgentRunDecide approves or denies one pending approval of a run.
func cmdAgentRunDecide(decision string, args []string) int {
	usage := "usage: gregale agent run " + decision + " <run-id> <approval-id> [--note TEXT] [--json]"
	fs := newFlagSet("agents runs "+decision, flag.ContinueOnError)
	note := fs.String("note", "", "note recorded with the decision")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 2 || fs.Arg(0) == "" || fs.Arg(1) == "" {
		PrintUsage(os.Stderr, usage, "agents")
		return 1
	}
	runID, approvalID := fs.Arg(0), fs.Arg(1)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	approval, err := client.DecideAgentRunApproval(context.Background(), runID, approvalID,
		api.DecideAgentRunApprovalRequest{Decision: decision, Note: *note})
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Problem.Code == api.CodeAgentRunApprovalClosed {
			closed := *apiErr
			closed.Problem.Hint = "Only a pending approval takes a decision; see the run's approvals with `gregale agent run approval list " + runID + "`."
			err = &closed
		}
		return printErr("Could not "+decision+" the approval", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(approval))
	}
	PrintOK(osStdout, "Approval %s for %s is %s", terminalSafe(approval.ID), terminalSafe(terminalSafeLine(approval.Tool, 40)), terminalSafe(approval.State))
	return 0
}
