package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
)

func cmdIssueTriage(args []string) int {
	usage := "usage: gregale issues compare-releases --app SLUG --baseline UUID --candidate UUID [--environment application]\n       gregale issues bulk --app SLUG --issues UUID,UUID --action assign|resolve|ignore|reopen [--assignee UUID --deployment UUID --until RFC3339]"
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	fs := newFlagSet("issues "+args[0], flag.ContinueOnError)
	app := fs.String("app", "", "application slug")
	baseline := fs.String("baseline", "", "baseline deployment UUID")
	candidate := fs.String("candidate", "", "candidate deployment UUID")
	environment := fs.String("environment", "application", "issue environment")
	ids := fs.String("issues", "", "comma-separated issue UUIDs")
	action := fs.String("action", "", "bulk lifecycle action")
	team := fs.String("team", "", "team UUID")
	assignee := fs.String("assignee", "", "account UUID; empty unassigns")
	dep := fs.String("deployment", "", "fixed deployment UUID")
	until := fs.String("until", "", "ignore expiry, RFC3339")
	flags, positional := normalizeDebugFlagArgs(args[1:], map[string]bool{"app": true, "baseline": true, "candidate": true, "environment": true, "issues": true, "action": true, "team": true, "assignee": true, "deployment": true, "until": true})
	if err := fs.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if *app == "" || len(positional) != 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	var in api.BulkIssueActionRequest
	switch args[0] {
	case "bulk":
		in = api.BulkIssueActionRequest{IssueIDs: strings.Split(*ids, ","), Action: api.IssueActionRequest{Action: *action, AssigneeAccountID: *assignee, AssigneeTeamID: *team, FixedDeploymentID: *dep}}
		for i := range in.IssueIDs {
			in.IssueIDs[i] = strings.TrimSpace(in.IssueIDs[i])
		}
		if *until != "" {
			t, err := time.Parse(time.RFC3339, *until)
			if err != nil {
				return printErr("Invalid ignore expiry", err)
			}
			in.Action.IgnoredUntil = &t
		}
		var err error
		in, err = api.NormalizeBulkIssueAction(in, time.Now())
		if err != nil {
			return printErr("Invalid bulk action", err)
		}
	case "compare-releases":
		b, e1 := uuid.Parse(*baseline)
		c, e2 := uuid.Parse(*candidate)
		if e1 != nil || e2 != nil || b == c || b == uuid.Nil || c == uuid.Nil {
			return printErr("Invalid release comparison", fmt.Errorf("choose two distinct deployment UUIDs"))
		}
	default:
		fmt.Fprintln(os.Stderr, usage)
		return 1
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if args[0] == "bulk" {
		out, err := c.BulkActOnIssues(context.Background(), *app, in)
		if err != nil {
			return printErr("Could not update selected issues", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		_, _ = fmt.Fprintf(osStdout, "Updated %d canonical issue groups.\n", len(out.Items))
		return 0
	}
	out, err := c.CompareIssueReleases(context.Background(), *app, *baseline, *candidate, *environment)
	if err != nil {
		return printErr("Could not compare releases", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	view := dashboard.IssueReleaseComparisonPresentation(out)
	for _, row := range view.Rows {
		_, _ = fmt.Fprintf(osStdout, "%s %s: %d observed requests, %d HTTP 5xx (%s); %d issue events, %d groups, %d new, %d regressed.\n", row.Label, row.Health.DeploymentID, row.Health.ObservedRequests, row.Health.HTTP5xxRequests, row.Rate, row.Health.ObservedIssueEvents, row.Health.ObservedIssueGroups, row.Health.NewIssueGroups, row.Health.RegressedIssueGroups)
	}
	_, _ = fmt.Fprintf(osStdout, "Observed HTTP 5xx rate delta: %s. Retained observations; traffic exposure and coverage may differ.\n", view.Delta)
	return 0
}
