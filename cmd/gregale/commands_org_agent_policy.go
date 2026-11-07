package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdOrgsAgentPolicy shows or sets whether agents may act in an
// organization, and with which capabilities (ADR-20261101225122).
func cmdOrgsAgentPolicy(args []string) int {
	const usage = "usage: gregale org agent policy get --org <slug> | set --org <slug> --agents allow|deny [--capabilities LIST] [--approvals LIST|none]"
	if len(args) == 0 || (args[0] != "get" && args[0] != "set") {
		PrintUsage(os.Stderr, usage, "orgs")
		return 1
	}
	fs := newFlagSet("orgs agent-policy "+args[0], flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required)")
	var agents, capabilities, approvals string
	if args[0] == "set" {
		fs.StringVar(&agents, "agents", "", "allow or deny agents in the org")
		fs.StringVar(&capabilities, "capabilities", "", "comma-separated capabilities agents may get; empty for read-only")
		fs.StringVar(&approvals, "approvals", "", "kinds of action every agent must have a person approve: delete,production,credentials,billing, all or none")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || *slug == "" || (args[0] == "set" && agents != "allow" && agents != "deny") {
		PrintUsage(os.Stderr, usage, "orgs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	policy, err := client.GetOrgAgentPolicy(ctx, *slug)
	if err != nil {
		return printErr("Could not read the agent policy", err)
	}
	if args[0] == "set" {
		allowed, scopes := agents == "allow", policy.AllowedScopes
		if flagWasSet(fs, "capabilities") {
			// Never nil: --capabilities "" sends [] (read-only), not null.
			scopes = append([]string{}, splitList(capabilities)...)
		}
		floor, err := approvalsFlag(fs, approvals)
		if err != nil {
			return printErr("Invalid approvals", err)
		}
		if policy, err = client.SetOrgAgentPolicy(ctx, *slug, api.OrgAgentPolicyRequest{AgentsAllowed: &allowed, AllowedScopes: &scopes, ApprovalCategories: floor}); err != nil {
			return printErr("Could not set the agent policy", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(policy))
	}
	printAgentPolicy(policy)
	return 0
}

func printAgentPolicy(policy api.OrgAgentPolicyResponse) {
	if !policy.AgentsAllowed {
		_, _ = fmt.Fprintln(osStdout, "Agents: not allowed")
	} else if len(policy.AllowedScopes) == 0 {
		_, _ = fmt.Fprintln(osStdout, "Agents: allowed, read-only")
	} else {
		_, _ = fmt.Fprintf(osStdout, "Agents: allowed, with %s\n", strings.Join(policy.AllowedScopes, ", "))
	}
	if len(policy.ApprovalCategories) == 0 {
		_, _ = fmt.Fprintln(osStdout, "Approvals required for every agent: none beyond each connection's own")
	} else {
		_, _ = fmt.Fprintf(osStdout, "Approvals required for every agent: %s\n", strings.Join(policy.ApprovalCategories, ", "))
	}
	if policy.Revoked > 0 {
		_, _ = fmt.Fprintf(osStdout, "Revoked %d agent connection(s) the policy no longer allows\n", policy.Revoked)
	}
}
