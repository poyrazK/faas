package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// approvalsFlag reads --approvals (ADR-20261104140058): nil when the flag is
// absent, so the server applies its default; "none" sends an empty list.
func approvalsFlag(fs *flag.FlagSet, value string) (*[]string, error) {
	if !flagWasSet(fs, "approvals") {
		return nil, nil
	}
	categories, err := api.NormalizeAgentApprovalCategories(splitList(value))
	if err != nil {
		return nil, err
	}
	return &categories, nil
}

// cmdMCPApprovalChannels shows or replaces the notification channels that
// also get the account's agents' approval requests (ADR-20261107140000 §3).
func cmdMCPApprovalChannels(args []string) int {
	const usage = "usage: gregale mcp server approval channels [--set NAME,NAME | --clear] [--json]"
	fs := newFlagSet("mcp approval-channels", flag.ContinueOnError)
	set := fs.String("set", "", "replace with these comma-separated notification channel names")
	clear := fs.Bool("clear", false, "remove every channel; requests go to email only")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || (flagWasSet(fs, "set") && *clear) || (flagWasSet(fs, "set") && len(splitList(*set)) == 0) {
		PrintUsage(os.Stderr, usage, "mcp")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var out api.AgentApprovalChannelsResponse
	switch {
	case *clear:
		out, err = client.SetAgentApprovalChannels(ctx, []string{})
	case flagWasSet(fs, "set"):
		out, err = client.SetAgentApprovalChannels(ctx, splitList(*set))
	default:
		out, err = client.GetAgentApprovalChannels(ctx)
	}
	if err != nil {
		return printErr("Could not update the approval channels", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if len(out.Channels) == 0 {
		_, _ = fmt.Fprintln(osStdout, "Agents' approval requests go to email only.")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "Agents' approval requests also go to: %s\n", strings.Join(out.Channels, ", "))
	return 0
}
