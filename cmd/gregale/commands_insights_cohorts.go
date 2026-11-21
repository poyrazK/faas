package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale insight cohort ...` (ADR 20261119102741 §3): saved cohorts that
// flag rules target with the gregale.cohort attribute.

const insightCohortVerbs = "list|create|get|delete|refresh"

func cmdInsightCohorts(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "gregale "+cliTreeSpellPath("insights cohorts")+" "+insightCohortVerbs, "insights")
		return 1
	}
	verb, args := args[0], args[1:]
	fs := newFlagSet("insights-cohorts-"+verb, flag.ContinueOnError)
	project := fs.String("project", "", "project slug")
	environment := fs.String("environment", "production", "named project environment")
	kind := fs.String("kind", "", "create: did, did_not or funnel")
	event := fs.String("event", "", "create: the event (did, did_not)")
	days := fs.Int("days", 0, "create: look back this many days")
	minCount := fs.Int("min-count", 0, "create: did: at least this many events (default 1)")
	window := fs.String("window", "", "create: funnel: completion window, 1h to 720h")
	var steps, where repeatedFlag
	fs.Var(&steps, "step", "create: funnel step event (repeatable, 2 to 6)")
	fs.Var(&where, "where", "create: event property filter KEY:VALUE (repeatable)")
	positionals, err := parseInterleaved(fs, args)
	if err != nil {
		return 1
	}
	want := 1
	if verb == "list" {
		want = 0
	}
	// Literal counts, so the positional guard sees the check before any call.
	if *project == "" || (want == 0 && len(positionals) != 0) || (want == 1 && len(positionals) != 1) {
		needs := map[bool]string{true: "", false: " and a cohort KEY"}[want == 0]
		PrintUsage(os.Stderr, fmt.Sprintf("gregale %s needs --project%s", cliTreeSpellPath("insights cohorts "+verb), needs), "insights")
		return 1
	}
	var req api.InsightCohortRequest
	if verb == "create" {
		filter, err := parseCohortWhere(where)
		if err != nil {
			printCommandValidation(os.Stderr, "gregale %s: %v\n", cliTreeSpellPath("insights cohorts create"), err)
			return 1
		}
		req = api.InsightCohortRequest{Key: positionals[0], Kind: *kind, Event: *event, Where: filter, WithinDays: *days, MinCount: *minCount, Window: *window}
		for _, s := range steps {
			req.Steps = append(req.Steps, api.InsightStepRequest{Event: s})
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	return runInsightCohorts(context.Background(), client, verb, *project, *environment, positionals, req)
}

func parseCohortWhere(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for _, item := range raw {
		k, v, ok := strings.Cut(item, ":")
		if !ok || k == "" {
			return nil, fmt.Errorf("--where %q: use KEY:VALUE", item)
		}
		out[k] = v
	}
	return out, nil
}

func runInsightCohorts(ctx context.Context, client *Client, verb, project, environment string, positionals []string, req api.InsightCohortRequest) int {
	var (
		c   api.InsightCohort
		err error
	)
	switch verb {
	case "list":
		out, err := client.ListProjectInsightCohorts(ctx, project, environment)
		return insightsOut(out, err, func() { printInsightCohorts(out.Cohorts, project) })
	case "create":
		c, err = client.CreateProjectInsightCohort(ctx, project, environment, req)
	case "get":
		c, err = client.GetProjectInsightCohort(ctx, project, environment, positionals[0])
	case "refresh":
		c, err = client.RefreshProjectInsightCohort(ctx, project, environment, positionals[0])
	case "delete":
		if err := client.DeleteProjectInsightCohort(ctx, project, environment, positionals[0]); err != nil {
			return printErr("Cohort operation failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"deleted": positionals[0]}))
		}
		_, _ = fmt.Fprintf(osStdout, "Deleted cohort %s; rules that target it no longer match its customers\n", positionals[0])
		return 0
	default:
		printCommandValidation(os.Stderr, "gregale insight cohort: unknown subcommand %q (%s)\n", verb, insightCohortVerbs)
		return 1
	}
	return insightsOut(c, err, func() { printInsightCohort(c, verb) })
}

func cohortDescription(c api.InsightCohort) string {
	where := ""
	if len(c.Where) > 0 {
		parts := make([]string, 0, len(c.Where))
		for k, v := range c.Where {
			parts = append(parts, k+":"+v)
		}
		where = " where " + strings.Join(parts, ",")
	}
	switch c.Kind {
	case "did":
		return fmt.Sprintf("sent %s%s at least %d times in %d days", c.Event, where, max(c.MinCount, 1), c.WithinDays)
	case "did_not":
		return fmt.Sprintf("active but did not send %s%s in %d days", c.Event, where, c.WithinDays)
	}
	names := make([]string, 0, len(c.Steps))
	for _, s := range c.Steps {
		names = append(names, s.Event)
	}
	return fmt.Sprintf("completed %s within %s, started in %d days", strings.Join(names, " > "), c.Window, c.WithinDays)
}

func cohortRefreshed(c api.InsightCohort) string {
	switch {
	case c.RefreshError != "":
		return "failed: " + c.RefreshError
	case c.RefreshedAt == nil:
		return "pending"
	}
	return c.RefreshedAt.UTC().Format(time.RFC3339)
}

func printInsightCohorts(cohorts []api.InsightCohort, project string) {
	if len(cohorts) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No cohorts. Create one with gregale %s KEY --kind did --event EVENT --days 30 --project %s\n", cliTreeSpellPath("insights cohorts create"), project)
		return
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KEY\tMEMBERS\tREFRESHED\tDEFINITION")
	for _, c := range cohorts {
		members := fmt.Sprint(c.Members)
		if c.Truncated {
			members += " (max)"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Key, members, cohortRefreshed(c), cohortDescription(c))
	}
	_ = tw.Flush()
}

func printInsightCohort(c api.InsightCohort, verb string) {
	_, _ = fmt.Fprintf(osStdout, "Cohort %s: customers who %s\n", c.Key, cohortDescription(c))
	_, _ = fmt.Fprintf(osStdout, "Members: %d of at most %d; refreshed: %s\n", c.Members, c.MaxMembers, cohortRefreshed(c))
	if c.Truncated {
		_, _ = fmt.Fprintf(osStdout, "More customers matched than the plan keeps; the lowest customer IDs were kept.\n")
	}
	if verb == "create" || verb == "refresh" {
		_, _ = fmt.Fprintf(osStdout, "Members are computed within a minute. Target it with --attr cohort=%s on a flag rule.\n", c.Key)
	}
}
