package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdEdgeRulesStats prints per-rule match counts for an app (ADR-960):
// matched for enforced rules, logged for log-mode rules. Rules with no
// matches in the window are listed with zeros so dead rules stand out.
// ADR-20261020105001: a TREND column shows the window's matches over time.
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
	stats, err := client.GetEdgeRuleStatsWithSeries(ctx, *slug, *window)
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
	trends := map[string]string{}
	for _, s := range stats.Rules {
		counts[s.RuleID] = [2]int64{s.Matched, s.Logged}
		trends[s.RuleID] = edgeRuleTrend(stats.Window, s.Series)
	}
	sort.SliceStable(rules, func(i, j int) bool {
		ci, cj := counts[rules[i].ID], counts[rules[j].ID]
		return ci[0]+ci[1] > cj[0]+cj[1]
	})
	_, _ = fmt.Fprintf(osStdout, "Edge-rule matches on %s, last %s (since %s)\n", *slug, stats.Window, stats.Since.Format("2006-01-02 15:04 MST"))
	_, _ = fmt.Fprintf(osStdout, "%-36s %-12s %-8s %10s %10s  %-28s  %s\n", "ID", "KIND", "MODE", "MATCHED", "LOGGED", "TREND", "NAME")
	for _, r := range rules {
		c := counts[r.ID]
		trend := trends[r.ID]
		if trend == "" {
			trend = edgeRuleTrend(stats.Window, nil)
		}
		trend += strings.Repeat(" ", max(0, 28-utf8.RuneCountInString(trend)))
		_, _ = fmt.Fprintf(osStdout, "%-36s %-12s %-8s %10d %10d  %s  %s\n", r.ID, r.Kind, r.Mode, c[0], c[1], trend, truncate(r.Name, 40))
	}
	return 0
}

// edgeRuleTrend draws a window's hourly matches as block characters: one
// per hour for 24h, one per 6 hours for 7d, none for 1h.
func edgeRuleTrend(window string, series []int64) string {
	var values []int64
	switch window {
	case "24h":
		values = api.FoldEdgeRuleSeries(series, 1, 24)
	case "7d":
		values = api.FoldEdgeRuleSeries(series, 6, 28)
	default:
		return "-"
	}
	var peak int64
	for _, v := range values {
		peak = max(peak, v)
	}
	levels := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range values {
		i := 0
		if peak > 0 {
			i = int((v*int64(len(levels)-1) + peak - 1) / peak)
		}
		b.WriteRune(levels[i])
	}
	return b.String()
}
