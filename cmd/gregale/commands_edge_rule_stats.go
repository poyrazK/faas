package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
)

// cmdEdgeRulesStats prints per-rule match counts for an app (ADR-960):
// matched for enforced rules, logged for log-mode rules. Rules with no
// matches in the window are listed with zeros so dead rules stand out.
func cmdEdgeRulesStats(args []string) int {
	fs := newFlagSet("edge-rules stats", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	window := fs.String("window", "24h", "window: 1h, 24h or 7d")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale edge-rules stats --app <slug> [--window 1h|24h|7d]", "edge-rules")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	stats, err := client.GetEdgeRuleStats(ctx, *slug, *window)
	if err != nil {
		return printErr("Stats failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(stats))
	}
	rules, err := client.ListEdgeRulesForApp(ctx, *slug)
	if err != nil {
		return printErr("List failed", err)
	}
	counts := map[string][2]int64{}
	for _, s := range stats.Rules {
		counts[s.RuleID] = [2]int64{s.Matched, s.Logged}
	}
	sort.SliceStable(rules, func(i, j int) bool {
		ci, cj := counts[rules[i].ID], counts[rules[j].ID]
		return ci[0]+ci[1] > cj[0]+cj[1]
	})
	_, _ = fmt.Fprintf(osStdout, "Edge-rule matches on %s, last %s (since %s)\n", *slug, stats.Window, stats.Since.Format("2006-01-02 15:04 MST"))
	_, _ = fmt.Fprintf(osStdout, "%-36s %-12s %-8s %10s %10s  %s\n", "ID", "KIND", "MODE", "MATCHED", "LOGGED", "NAME")
	for _, r := range rules {
		c := counts[r.ID]
		_, _ = fmt.Fprintf(osStdout, "%-36s %-12s %-8s %10d %10d  %s\n", r.ID, r.Kind, r.Mode, c[0], c[1], truncate(r.Name, 40))
	}
	return 0
}
