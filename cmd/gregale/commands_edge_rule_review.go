package main

// ADR-20261020105001: `gregale edge-rules review` — which log-mode rules are ready to
// enforce, which are still collecting traffic, and which rules matched
// nothing for a week, each with the command to act on it. Read-only.

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const edgeRuleReviewUsage = "usage: gregale edge-rules review [--app <slug>]"

// edgeRuleReviewJSON is the --json shape of one reviewed rule.
type edgeRuleReviewJSON struct {
	RuleID        string    `json:"rule_id"`
	Name          string    `json:"name,omitempty"`
	Kind          string    `json:"kind"`
	Mode          string    `json:"mode"`
	Status        string    `json:"status"`
	Since         time.Time `json:"since"`
	ObservedHours int64     `json:"observed_hours"`
	Matches       int64     `json:"matches"`
	PerDay        int64     `json:"per_day"`
}

// cmdEdgeRulesReview prints the rule review for an app.
func cmdEdgeRulesReview(args []string) int {
	fs := newFlagSet("edge-rules review", flag.ContinueOnError)
	appFlag := fs.String("app", "", "app slug (defaults to linked project context)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, edgeRuleReviewUsage, "edge-rules")
		return 1
	}
	slug, err := resolveAppFlagOrContext(strings.TrimSpace(*appFlag))
	if err != nil {
		return printErr("Could not resolve app slug", fmt.Errorf("pass --app <slug> or run inside a linked project: %w", err))
	}
	if !validCLISlug(slug) {
		return printErr("Invalid app slug", fmt.Errorf("%q is not a valid app slug", slug))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	rules, err := client.ListEdgeRulesForApp(ctx, slug)
	if err != nil {
		return printErr("List failed", err)
	}
	stats, err := client.GetEdgeRuleStatsWithSeries(ctx, slug, "7d")
	if err != nil {
		return printErr("Stats failed", err)
	}
	items := reviewEdgeRules(rules, stats, time.Now().UTC())
	if jsonOutput {
		return jsonOut(writeJSON(items))
	}
	if len(items) == 0 {
		_, _ = fmt.Fprintf(osStdout, "Nothing to review on %s: no rules in log mode and none quiet for 7 days.\n", slug)
		return 0
	}
	for _, it := range items {
		printEdgeRuleReview(slug, it)
	}
	return 0
}

// reviewEdgeRules reviews every enabled, unexpired rule: ready first (most
// matches first), then collecting, then quiet.
func reviewEdgeRules(rules []api.EdgeRuleResponse, stats api.EdgeRuleStatsResponse, now time.Time) []edgeRuleReviewJSON {
	series := map[string][]int64{}
	for _, st := range stats.Rules {
		series[st.RuleID] = st.Series
	}
	items := []edgeRuleReviewJSON{}
	for _, r := range rules {
		if r.ExpiresAt != nil && !r.ExpiresAt.After(now) {
			continue
		}
		rv := api.ReviewEdgeRule(api.EdgeRuleReviewInput{Mode: r.Mode, Enabled: r.Enabled, ChangedAt: r.UpdatedAt, SeriesStart: stats.Since, Series: series[r.ID], Now: now})
		if rv.Status == "" {
			continue
		}
		items = append(items, edgeRuleReviewJSON{
			RuleID: r.ID, Name: r.Name, Kind: r.Kind, Mode: r.Mode, Status: rv.Status, Since: rv.Since,
			ObservedHours: int64(rv.Observed / time.Hour), Matches: rv.Matches, PerDay: rv.PerDay,
		})
	}
	rank := map[string]int{api.EdgeRuleReviewReady: 0, api.EdgeRuleReviewCollecting: 1, api.EdgeRuleReviewQuiet: 2}
	slices.SortStableFunc(items, func(a, b edgeRuleReviewJSON) int {
		return cmp.Or(cmp.Compare(rank[a.Status], rank[b.Status]), cmp.Compare(b.Matches, a.Matches))
	})
	return items
}

func printEdgeRuleReview(slug string, it edgeRuleReviewJSON) {
	label := cmp.Or(it.Name, it.Kind+" rule")
	since := it.Since.Format("2006-01-02 15:04 MST")
	switch it.Status {
	case api.EdgeRuleReviewReady:
		if it.Matches > 0 {
			_, _ = fmt.Fprintf(osStdout, "READY       %s (%s)\n  Matched %d requests in log mode since %s (about %d a day).\n  Samples: gregale edge-rules events --app %s --rule %s --outcome logged --since 7d\n", label, it.RuleID, it.Matches, since, it.PerDay, slug, it.RuleID)
		} else {
			_, _ = fmt.Fprintf(osStdout, "READY       %s (%s)\n  No matches since %s: enforcing it would not have changed any request so far.\n", label, it.RuleID, since)
		}
		_, _ = fmt.Fprintf(osStdout, "  Enforce: gregale edge-rules update %s --mode enforce\n\n", it.RuleID)
	case api.EdgeRuleReviewCollecting:
		_, _ = fmt.Fprintf(osStdout, "COLLECTING  %s (%s)\n  In log mode for %d h; give it a day of traffic before deciding.\n\n", label, it.RuleID, it.ObservedHours)
	default:
		_, _ = fmt.Fprintf(osStdout, "QUIET       %s (%s)\n  No matches in 7 days. Check what it matches, or remove it: gregale edge-rules rm %s\n\n", label, it.RuleID, it.RuleID)
	}
}
