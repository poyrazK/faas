package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
)

const edgeRulesSummaryUsage = "usage: gregale edge-rules summary --app SLUG [--range 1h] [--json]"

// cmdEdgeRulesSummary implements `gregale edge-rules summary`: what the edge
// rejected for one app over a range, read from the same per-app counters the
// edge security alert presets evaluate.
func cmdEdgeRulesSummary(args []string) int {
	fs := newFlagSet("edge-rules summary", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	rng := fs.String("range", "1h", "time window ("+strings.Join(appmetrics.Ranges(), ", ")+")")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *slug == "" {
		PrintUsage(os.Stderr, edgeRulesSummaryUsage, "edge-rules")
		return 1
	}
	if !appmetrics.IsValidRange(*rng) {
		return printErr("Invalid --range", fmt.Errorf("must be one of %s; got %q", strings.Join(appmetrics.Ranges(), ", "), *rng))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	summary, err := client.GetAppEdgeProtection(context.Background(), *slug, *rng)
	if err != nil {
		return printErr("Could not fetch the edge summary", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(summary))
	}
	renderEdgeProtection(osStdout, *slug, summary)
	return 0
}

// renderEdgeProtection prints the summary as labelled lines, largest
// rejection first, and says so explicitly when nothing was rejected.
func renderEdgeProtection(w io.Writer, slug string, s api.EdgeProtectionResponse) {
	_, _ = fmt.Fprintf(w, "Edge protection for %s over %s\n", slug, s.Range)
	if s.Source != appmetrics.SourcePrometheus {
		_, _ = fmt.Fprintf(w, "Note: source=%s (counts below are unavailable, not zero)\n", s.Source)
		return
	}
	_, _ = fmt.Fprintf(w, "  %-28s %d blocked, %d would block (observe)\n", "pre-auth source limit:", s.PreAuth.Blocked, s.PreAuth.WouldBlock)
	if len(s.ValidationFailures) == 0 {
		_, _ = fmt.Fprintf(w, "  %-28s none\n", "validation failures:")
	} else {
		parts := make([]string, 0, len(s.ValidationFailures))
		for _, v := range s.ValidationFailures {
			parts = append(parts, fmt.Sprintf("%d %s", v.Count, v.Name))
		}
		_, _ = fmt.Fprintf(w, "  %-28s %s\n", "validation failures:", strings.Join(parts, ", "))
	}
	if len(s.Rejections) == 0 {
		_, _ = fmt.Fprintf(w, "  %-28s none\n", "edge gate rejections:")
	} else {
		_, _ = fmt.Fprintln(w, "  edge gate rejections:")
		for _, r := range s.Rejections {
			_, _ = fmt.Fprintf(w, "    %-16s %-6s %d\n", r.Gate, r.Status, r.Count)
		}
	}
	renderEdgeProtectionWAF(w, s.WAF)
}

// renderEdgeProtectionWAF prints the kind=waf section only when a WAF rule
// saw traffic, so apps without one are not shown an empty row.
func renderEdgeProtectionWAF(w io.Writer, waf api.EdgeProtectionWAF) {
	if waf.Inspected == 0 && waf.NotInspected == 0 && waf.Warned == 0 && waf.Blocked == 0 && waf.InlineSkipped == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, "  %-28s %d inspected, %d detected, %d not inspected (budget or queue)\n",
		"waf (sampled):", waf.Inspected, waf.Detected, waf.NotInspected)
	if waf.Warned > 0 || waf.Blocked > 0 || waf.InlineSkipped > 0 {
		_, _ = fmt.Fprintf(w, "  %-28s %d blocked, %d warned, %d passed unchecked (budget)\n",
			"waf (in-path, headers/URI):", waf.Blocked, waf.Warned, waf.InlineSkipped)
	}
	for _, list := range []struct {
		label  string
		counts []api.EdgeProtectionCount
	}{{"categories", waf.Categories}, {"top CRS rules", waf.TopRules}} {
		if len(list.counts) == 0 {
			continue
		}
		parts := make([]string, 0, len(list.counts))
		for _, c := range list.counts {
			parts = append(parts, fmt.Sprintf("%s %d", c.Name, c.Count))
		}
		_, _ = fmt.Fprintf(w, "    %-26s %s\n", list.label+":", strings.Join(parts, ", "))
	}
}
