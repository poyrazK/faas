package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdOrgsSupportAccess lists, revokes and declines Gregale support access to
// an organization's apps (ADR-20261116163508). Granting and approving need a
// signed-in browser, so `approve` prints the dashboard page instead.
func cmdOrgsSupportAccess(args []string) int {
	const usage = "usage: gregale org support <list|revoke|decline|approve> --org <slug> [ID]"
	if len(args) == 0 {
		PrintUsage(os.Stderr, usage, "orgs")
		return 1
	}
	verb := args[0]
	if verb == "ls" {
		verb = "list"
	}
	fs := newFlagSet("orgs support-access "+verb, flag.ContinueOnError)
	slug := fs.String("org", "", "org slug (required)")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	wantID := verb == "revoke" || verb == "decline"
	if *slug == "" || (wantID && fs.NArg() != 1) || (!wantID && fs.NArg() != 0) {
		PrintUsage(os.Stderr, usage, "orgs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch verb {
	case "list":
		access, err := client.GetOrgSupportAccess(ctx, *slug)
		if err != nil {
			return printErr("Could not read support access", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(access))
		}
		printSupportAccess(access)
		return 0
	case "revoke":
		if err := client.RevokeSupportGrant(ctx, *slug, fs.Arg(0)); err != nil {
			return printErr("Could not revoke support access", err)
		}
		_, _ = fmt.Fprintln(osStdout, "Revoked. Gregale support no longer has access under that grant.")
		return 0
	case "decline":
		if err := client.DeclineSupportAccessRequest(ctx, *slug, fs.Arg(0)); err != nil {
			return printErr("Could not decline the request", err)
		}
		_, _ = fmt.Fprintln(osStdout, "Declined.")
		return 0
	case "approve":
		_, _ = fmt.Fprintf(osStdout, "Granting or approving support access needs a signed-in browser. Open:\n  %s/dashboard/support-access\n", dashboardBaseURL(client.BaseURL()))
		return 0
	default:
		PrintUsage(os.Stderr, usage, "orgs")
		return 1
	}
}

func printSupportAccess(access api.SupportAccess) {
	if !access.Enabled {
		_, _ = fmt.Fprintln(osStdout, "Support access is not available for this organization yet.")
	}
	open := 0
	for _, req := range access.Requests {
		if req.Status == "open" {
			open++
			_, _ = fmt.Fprintf(osStdout, "Request %s from %s: %s (expires %s)\n", req.ID, req.Operator, req.Reason, req.ExpiresAt)
		}
	}
	if open > 0 {
		_, _ = fmt.Fprintln(osStdout, "Approve on the dashboard (gregale org support approve), or decline with gregale org support decline.")
		_, _ = fmt.Fprintln(osStdout)
	}
	if len(access.Grants) == 0 {
		_, _ = fmt.Fprintln(osStdout, "Gregale support has no access.")
		return
	}
	tw := tabwriter.NewWriter(osStdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "GRANT\tAPPS\tSTATUS\tUNTIL\tGRANTED BY")
	for _, g := range access.Grants {
		apps := strings.Join(g.Apps, ",")
		if g.AllApps {
			apps = "(all)"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", g.ID, apps, g.Status, g.ExpiresAt, g.CreatedBy)
	}
	_ = tw.Flush()
}
