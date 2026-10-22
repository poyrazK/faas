package main

// ADR-20261022120744: the release page and its timeline.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDeploymentTimeline prints one release's phase and what happened to it.
func cmdDeploymentTimeline(args []string) int {
	fs := newFlagSet("deployment timeline", flag.ContinueOnError)
	app := fs.String("app", "", "app slug, to resolve vN revisions (defaults to linked project app)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale deployment timeline <id|vN> [--app <slug>]", "deployment")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	id, err := resolveDeploymentArg(ctx, client, *app, fs.Arg(0))
	if err != nil {
		return printErr("Could not resolve the deployment", err)
	}
	timeline, err := client.GetReleaseTimeline(ctx, id)
	if err != nil {
		return printErr("Could not read the release's history", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(timeline))
	}
	lines := releaseTimelineLines(timeline)
	lines = append(lines, "", "Page: "+dashboardBaseURL(client.BaseURL())+"/dashboard/releases/"+timeline.DeploymentID)
	_, _ = fmt.Fprint(osStdout, strings.Join(lines, "\n")+"\n")
	return 0
}

// releaseTimelineLines renders a timeline for a terminal: the release, its
// gates and approvals, then one line per event, oldest first.
func releaseTimelineLines(t api.ReleaseTimeline) []string {
	name := func(revision int, id string) string {
		if revision > 0 {
			return fmt.Sprintf("v%d", revision)
		}
		return id
	}
	lines := []string{fmt.Sprintf("%s of %s · %s · %s", name(t.Revision, t.DeploymentID), t.App, strings.ReplaceAll(t.Phase, "_", " "), t.Summary)}
	var facts []string
	if t.StableDeploymentID != "" {
		facts = append(facts, "replaces "+name(t.StableRevision, t.StableDeploymentID))
	}
	if t.ReplacedByDeploymentID != "" {
		facts = append(facts, "replaced by "+name(t.ReplacedByRevision, t.ReplacedByDeploymentID))
	}
	if t.DeployedBy != "" {
		facts = append(facts, "deployed by "+t.DeployedBy)
	}
	if t.CommitSHA != "" {
		facts = append(facts, "commit "+shortCommit(t.CommitSHA))
	}
	if t.WatchUntil != "" {
		facts = append(facts, "watched until "+releaseEventTime(t.WatchUntil))
	}
	if len(facts) > 0 {
		lines = append(lines, strings.Join(facts, " · "))
	}
	if a := t.Approval; a != nil {
		lines = append(lines, fmt.Sprintf("Approvals: %s, %d of %d", strings.ReplaceAll(a.State, "_", " "), a.Approvals, a.RequiredApprovals))
	}
	for _, g := range t.Gates {
		result := "passed"
		if !g.Passed {
			result = "failed"
		}
		lines = append(lines, fmt.Sprintf("Gate %s %s: %s", g.Name, result, g.Summary))
	}
	lines = append(lines, "")
	for _, e := range t.Events {
		line := fmt.Sprintf("%s  %-18s %s", releaseEventTime(e.At), e.Kind, e.Summary)
		if e.Repeats > 0 {
			line += fmt.Sprintf(" (%d times)", e.Repeats+1)
		}
		if e.Actor != "" {
			line += " · " + e.Actor
		}
		lines = append(lines, line)
	}
	if t.Truncated {
		lines = append(lines, "(older history is not shown)")
	}
	return lines
}

func releaseEventTime(rfc3339 string) string {
	at, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return at.UTC().Format("2006-01-02 15:04:05")
}
