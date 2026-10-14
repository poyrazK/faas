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
	enable := fs.Bool("enable", false, "delegate future Gregale lifecycle pushes to the native tracker worker")
	provider := fs.String("provider", "github", "github or linear")
	openState := fs.String("linear-open-state", "", "selected Linear open workflow UUID")
	resolvedState := fs.String("linear-resolved-state", "", "selected Linear resolved workflow UUID")
	disable := fs.Bool("disable", false, "disable future automatic pushes")
	flags, pos := normalizeDebugFlagArgs(args, map[string]bool{"app": true, "issue": true, "link": true, "provider": true, "linear-open-state": true, "linear-resolved-state": true})
	if err := fs.Parse(flags); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}
	if *app == "" || len(pos) > 0 || (*enable && *disable) {
		return printErr("Invalid automatic sync request", fmt.Errorf("require --app, --issue and --link; choose at most one of --enable or --disable"))
	}
	if *provider != "github" && *provider != "linear" {
		return printErr("Invalid automatic sync request", fmt.Errorf("provider must be github or linear"))
	}
	if (*openState != "" || *resolvedState != "") && (!*enable || *provider != "linear") {
		return printErr("Invalid workflow mapping", fmt.Errorf("workflow flags require --enable --provider linear"))
	}
	if *enable && *provider == "linear" {
		for _, value := range []*string{openState, resolvedState} {
			id, e := uuid.Parse(*value)
			if e != nil || id == uuid.Nil {
				return printErr("Invalid workflow mapping", fmt.Errorf("select nonzero open and resolved workflow UUIDs"))
			}
			*value = id.String()
		}
		if *openState == *resolvedState {
			return printErr("Invalid workflow mapping", fmt.Errorf("open and resolved workflows must differ"))
		}
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
			var observedIssueID, observedState string
			if *provider == "linear" {
				observed, e := client.GetNativeLinearIssueTrackerStatus(ctx, *app, *issue, *link)
				if e != nil {
					return printErr("Cannot observe native Linear status", e)
				}
				in.ExpectedLinearStatus = &observed
				in.LinearOpenStateID = *openState
				in.LinearResolvedStateID = *resolvedState
				observedIssueID, observedState = observed.IssueID, observed.Status.State
			} else {
				observed, e := client.GetNativeIssueTrackerStatus(ctx, *app, *issue, *link)
				if e != nil {
					return printErr("Cannot observe native GitHub status", e)
				}
				in.ExpectedStatus = &observed
				observedIssueID, observedState = observed.IssueID, observed.Status.State
			}
			detail, e := client.GetIssue(ctx, *app, *issue, "", "")
			if e != nil {
				return printErr("Cannot read issue", e)
			}
			if detail.Issue.ID != observedIssueID || detail.Issue.State != observedState || (detail.Issue.State != "open" && detail.Issue.State != "resolved") {
				return printErr("Align issue lifecycle before enabling", fmt.Errorf("explicitly synchronize the stored reference before automatic push"))
			}
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
