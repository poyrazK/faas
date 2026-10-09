package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdEdgeRulesEvents prints sampled requests edge rules matched (ADR-908),
// newest first. --rule / --outcome filter; --cursor continues a listing.
func cmdEdgeRulesEvents(args []string) int {
	fs := newFlagSet("edge-rules events", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	rule := fs.String("rule", "", "only this rule id")
	outcome := fs.String("outcome", "", "matched or logged")
	since := fs.String("since", "24h", "how far back, e.g. 1h, 24h, 7d (clamped to the plan window)")
	limit := fs.Int("limit", 50, "events per page (1..200)")
	cursor := fs.String("cursor", "", "continue from a previous page's next cursor")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale edge-rules events --app <slug> [--rule ID] [--outcome matched|logged] [--since 24h] [--limit N] [--cursor C]", "edge-rules")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListEdgeRuleEvents(context.Background(), *slug, api.EdgeRuleEventsQuery{
		RuleID: *rule, Outcome: *outcome, Since: *since, Limit: *limit, Cursor: *cursor,
	})
	if err != nil {
		return printErr("Events failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	printEdgeRuleEvents(*slug, out)
	return 0
}

func printEdgeRuleEvents(slug string, out api.EdgeRuleEventsResponse) {
	if len(out.Events) == 0 {
		_, _ = fmt.Fprintf(osStdout, "(no edge-rule events on %s since %s)\n", slug, out.Since.Format("2006-01-02 15:04 MST"))
		return
	}
	_, _ = fmt.Fprintf(osStdout, "%-20s %-8s %-24s %-7s %-39s %-3s %s\n", "TIME", "OUTCOME", "RULE", "METHOD", "CLIENT", "CC", "HOST+PATH")
	for _, e := range out.Events {
		rule := e.RuleName
		if rule == "" {
			rule = e.RuleID
		}
		_, _ = fmt.Fprintf(osStdout, "%-20s %-8s %-24s %-7s %-39s %-3s %s\n",
			e.OccurredAt.Local().Format("2006-01-02 15:04:05"), e.Outcome, truncate(rule, 24), e.Method,
			e.ClientIP, e.Country, truncate(e.Host+e.Path, 80))
	}
	if out.NextCursor != "" {
		_, _ = fmt.Fprintf(osStdout, "more: --cursor %s\n", out.NextCursor)
	}
}
