package main

// ADR-831 amendment 13: `gregale edge-rules waf-review` groups an app's WAF
// detections for a false-positive review; `gregale edge-rules waf-exclude`
// adds or removes CRS rule IDs on a rule's exclusions without restating its
// action.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	edgeWAFReviewUsage  = "usage: gregale edge-rules waf-review [--app <slug>] [--rule ID] [--since 7d]"
	edgeWAFExcludeUsage = "usage: gregale edge-rules waf-exclude <rule-id> [--add ID,...] [--remove ID,...]"
)

// cmdEdgeRulesWAFReview prints the WAF false-positive review for an app.
func cmdEdgeRulesWAFReview(args []string) int {
	fs := newFlagSet("edge-rules waf-review", flag.ContinueOnError)
	appFlag := fs.String("app", "", "app slug (defaults to linked project context)")
	rule := fs.String("rule", "", "only this kind=waf rule id")
	since := fs.String("since", "", "how far back, e.g. 24h or 7d (default 7d, clamped to the plan window)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, edgeWAFReviewUsage, "edge-rules")
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
	out, err := client.GetEdgeWAFReview(context.Background(), slug, *rule, *since)
	if err != nil {
		return printErr("WAF review failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	printEdgeWAFReview(slug, out)
	return 0
}

func printEdgeWAFReview(slug string, out api.EdgeWAFReviewResponse) {
	if len(out.Groups) == 0 {
		_, _ = fmt.Fprintf(osStdout, "(no WAF detections on %s since %s)\n", slug, out.Since.UTC().Format("2006-01-02 15:04 MST"))
		return
	}
	_, _ = fmt.Fprintf(osStdout, "%-22s %-7s %-9s %6s %7s  %-24s %s\n", "ASSESSMENT", "CRS", "CATEGORY", "EVENTS", "CLIENTS", "RULE", "PATH")
	for _, g := range out.Groups {
		rule := g.RuleName
		if rule == "" {
			rule = g.RuleID
		}
		if g.RuleMode != "" {
			rule = truncate(rule, 24-len(g.RuleMode)-3) + " (" + g.RuleMode + ")"
		}
		_, _ = fmt.Fprintf(osStdout, "%-22s %-7d %-9s %6d %7d  %-24s %s\n",
			g.Assessment, g.CRSRuleID, g.Category, g.Events, g.Clients, truncate(rule, 24), truncate(g.Path, 60))
		_, _ = fmt.Fprintf(osStdout, "  %s\n", g.Reason)
		if g.Assessment == api.EdgeWAFReviewLikelyFalsePositive {
			_, _ = fmt.Fprintf(osStdout, "  exclude: gregale edge-rules waf-exclude %s --add %d\n", g.RuleID, g.CRSRuleID)
		}
		if len(g.RequestIDs) > 0 {
			_, _ = fmt.Fprintf(osStdout, "  example: gregale edge-rules events --app %s --request-id %s\n", slug, g.RequestIDs[0])
		}
	}
	_, _ = fmt.Fprintf(osStdout, "%d detections since %s; detections are sampled, so counts are lower bounds.\n",
		out.Scanned, out.Since.UTC().Format("2006-01-02 15:04 MST"))
	if out.Truncated {
		_, _ = fmt.Fprintln(osStdout, "More detections than one review reads: the oldest were left out. Narrow --since or --rule.")
	}
}

// cmdEdgeRulesWAFExclude adds and removes CRS rule IDs on a kind=waf rule's
// exclusions.
func cmdEdgeRulesWAFExclude(args []string) int {
	flags, positional := splitArgsForFlags(args)
	args = append(flags, positional...)
	fs := newFlagSet("edge-rules waf-exclude", flag.ContinueOnError)
	add := fs.String("add", "", "CRS rule IDs to exclude, comma-separated, e.g. 942190,942100")
	remove := fs.String("remove", "", "CRS rule IDs to stop excluding, comma-separated")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, edgeWAFExcludeUsage, "edge-rules")
		return 1
	}
	req, err := edgeWAFExclusionsRequest(*add, *remove)
	if err != nil {
		return printErr("Invalid rule IDs", err)
	}
	if prob := req.Validate(); prob != nil {
		return printErr("Invalid rule IDs", errToError(prob))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	rule, err := client.UpdateEdgeWAFExclusions(context.Background(), fs.Arg(0), req)
	if err != nil {
		return printErr("Exclusions update failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(rule))
	}
	_, _ = fmt.Fprintf(osStdout, "Rule %s now excludes: %s\n", rule.ID, edgeWAFExcludedList(rule))
	return 0
}

func edgeWAFExclusionsRequest(add, remove string) (api.EdgeWAFExclusionsRequest, error) {
	var req api.EdgeWAFExclusionsRequest
	for _, part := range []struct {
		flag, raw string
		into      *[]int
	}{{"--add", add, &req.Add}, {"--remove", remove, &req.Remove}} {
		for _, field := range strings.Split(part.raw, ",") {
			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}
			id, err := strconv.Atoi(field)
			if err != nil {
				return req, fmt.Errorf("%s: %q is not a rule ID", part.flag, field)
			}
			*part.into = append(*part.into, id)
		}
	}
	return req, nil
}

// edgeWAFExcludedList renders a rule's exclude_rule_ids, or "nothing". The
// response's action is the stored union, {"kind":"waf","waf":{...}}.
func edgeWAFExcludedList(rule api.EdgeRuleResponse) string {
	var stored struct {
		WAF api.EdgeRuleWAFAction `json:"waf"`
	}
	if err := json.Unmarshal(rule.Action, &stored); err != nil || len(stored.WAF.ExcludeRuleIDs) == 0 {
		return "nothing"
	}
	ids := make([]string, len(stored.WAF.ExcludeRuleIDs))
	for i, id := range stored.WAF.ExcludeRuleIDs {
		ids[i] = strconv.Itoa(id)
	}
	return strings.Join(ids, ", ")
}
