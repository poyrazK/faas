package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
)

// cmdEdgeRulesHistory lists an app's recorded edge-rule set versions
// (ADR-831), newest first. With --version it shows that version's rules.
func cmdEdgeRulesHistory(args []string) int {
	fs := newFlagSet("edge-rules history", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	version := fs.Int("version", 0, "show the rules recorded in this version")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale edge-rules history --app <slug> [--version N]", "edge-rules")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if *version > 0 {
		v, err := client.GetEdgeRuleSetVersion(ctx, *slug, *version)
		if err != nil {
			return printErr("History failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(v))
		}
		current := ""
		if v.Current {
			current = " (current)"
		}
		_, _ = fmt.Fprintf(osStdout, "Version %d%s · %d rules · recorded %s\n", v.Version, current, v.RuleCount, v.CreatedAt.Format("2006-01-02 15:04:05 MST"))
		for _, rule := range v.Rules {
			label := ""
			if rule.Name != "" {
				label = "  " + truncate(rule.Name, 40)
			}
			_, _ = fmt.Fprintf(osStdout, "  %-36s %-12s %-6d %-32s %s%s\n", rule.ID, rule.Kind, rule.Priority, truncate(rule.MatchHost, 32), rule.MatchPath, label)
		}
		return 0
	}
	versions, err := client.ListEdgeRuleSetVersions(ctx, *slug)
	if err != nil {
		return printErr("History failed", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(versions))
	}
	if len(versions) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no recorded versions)")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "%-8s %-6s %-24s %s\n", "VERSION", "RULES", "RECORDED", "SHA256")
	for _, v := range versions {
		marker := ""
		if v.Current {
			marker = "  (current)"
		}
		_, _ = fmt.Fprintf(osStdout, "%-8d %-6d %-24s %s%s\n", v.Version, v.RuleCount, v.CreatedAt.Format("2006-01-02 15:04:05 MST"), truncate(v.RulesSHA256, 12), marker)
	}
	return 0
}

// cmdEdgeRulesRollback restores an app's edge rules to a recorded version.
// The restore replaces every current rule, so it asks for typed
// confirmation unless --quiet.
func cmdEdgeRulesRollback(args []string) int {
	flags, positional := splitArgsForFlags(args, "quiet")
	args = append(flags, positional...)
	fs := newFlagSet("edge-rules rollback", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	to := fs.Int("to", 0, "version to restore (required; see edge-rules history)")
	quiet := fs.Bool("quiet", false, "skip the typed confirmation (for scripts)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *to < 1 || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale edge-rules rollback --app <slug> --to <version> [--quiet]", "edge-rules")
		return 1
	}
	if !*quiet {
		_, _ = fmt.Fprintf(osStderr, "About to replace every edge rule on %s with version %d.\n", *slug, *to)
		if !requireTyped("roll back edge rules") {
			return 1
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	rules, err := client.RollbackEdgeRules(context.Background(), *slug, *to)
	if err != nil {
		return printErr("Rollback failed", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(rules))
	}
	PrintOK(osStdout, "Edge rules on %s restored to version %s (%d rules).", *slug, strconv.Itoa(*to), len(rules))
	return 0
}
