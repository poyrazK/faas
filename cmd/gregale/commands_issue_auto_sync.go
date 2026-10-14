package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdIssueAutoSync(args []string) int {
	fs := newFlagSet("issues tickets auto-sync", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	issue := fs.String("issue", "", "issue UUID")
	link := fs.String("link", "", "reference UUID")
	enable := fs.Bool("enable", false, "delegate future Gregale lifecycle pushes to the native GitHub worker")
	disable := fs.Bool("disable", false, "disable future automatic pushes")
	flags, pos := normalizeDebugFlagArgs(args, map[string]bool{"app": true, "issue": true, "link": true})
	if err := fs.Parse(flags); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}
	if *app == "" || len(pos) > 0 || (*enable && *disable) {
		return printErr("Invalid automatic sync request", fmt.Errorf("require --app, --issue and --link; choose at most one of --enable or --disable"))
	}
	for _, id := range []*string{issue, link} {
		parsed, err := uuid.Parse(*id)
		if err != nil || parsed == uuid.Nil {
			return printErr("Invalid automatic sync request", fmt.Errorf("issue and link must be nonzero UUIDs"))
		}
		*id = parsed.String()
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	policy, err := client.GetIssueTrackerAutoSync(ctx, *app, *issue, *link)
	if err != nil {
		return printErr("Cannot read automatic sync policy", err)
	}
	if *enable || *disable {
		in := api.UpdateIssueTrackerAutoSyncRequest{Enabled: *enable, ExpectedRevision: policy.Revision}
		if *enable {
			observed, e := client.GetNativeIssueTrackerStatus(ctx, *app, *issue, *link)
			if e != nil {
				return printErr("Cannot observe native GitHub status", e)
			}
			detail, e := client.GetIssue(ctx, *app, *issue, "", "")
			if e != nil {
				return printErr("Cannot read issue", e)
			}
			if detail.Issue.ID != observed.IssueID || detail.Issue.State != observed.Status.State || (detail.Issue.State != "open" && detail.Issue.State != "resolved") {
				return printErr("Align issue lifecycle before enabling", fmt.Errorf("explicitly synchronize the stored reference before automatic push"))
			}
			in.ExpectedStatus = &observed
			in.ExpectedIssueState = detail.Issue.State
		}
		policy, err = client.UpdateIssueTrackerAutoSync(ctx, *app, *issue, *link, in)
		if err != nil {
			return printErr("Cannot configure automatic sync", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(policy))
	}
	fmt.Fprintf(osStdout, "enabled=%t\t%s\trevision=%d\t%s\n", policy.Enabled, policy.Outcome, policy.Revision, policy.Reason)
	return 0
}
