package main

// ADR-20261020105000: `gregale edge-rules suggest` prints the protection recipes the
// server recommends from the app's own traffic, each with the
// `gregale edge-rules recipe` command that adds it in log mode. Read-only.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const edgeRuleSuggestUsage = "usage: gregale edge rule suggest [--app <slug>] [--rate-limits [--range <window>] [--dry-run --candidate-rps <n> [--candidate-burst <n>]]]"

// cmdEdgeRulesSuggest lists suggested protection recipes for an app. With
// --rate-limits it lists per-route rate limits instead, as
// `throttle-suggestions` does (ADR-20261026145832: one suggest command).
func cmdEdgeRulesSuggest(args []string) int {
	if rest, ok := takeBoolFlag(args, "rate-limits"); ok {
		app, rest, err := takeAppTarget(rest)
		if err != nil {
			return printErr("Could not resolve app slug", fmt.Errorf("pass --app <slug> or run inside a linked project: %w", err))
		}
		return cmdThrottleSuggestions(append([]string{app}, rest...))
	}
	fs := newFlagSet("edge-rules suggest", flag.ContinueOnError)
	appFlag := fs.String("app", "", "app slug (defaults to linked project context)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, edgeRuleSuggestUsage, "edge-rules")
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
	out, err := client.GetEdgeRuleRecipeSuggestions(context.Background(), slug)
	if err != nil {
		return printErr("Suggestions failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	rateLimits := fmt.Sprintf("Per-route rate limits: gregale edge rule suggest --app %s --rate-limits\n", slug)
	if len(out.Suggestions) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No suggested protections for %s from the last %s of traffic.\n  Browse every recipe: gregale edge rule recipes\n%s", slug, out.Window, rateLimits)
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "Suggested protections for %s, from the last %s of traffic:\n\n", slug, out.Window)
	for _, sg := range out.Suggestions {
		_, _ = fmt.Fprintf(osStdout, "%s — %s\n  %s\n  Add in log mode: %s\n", sg.Recipe, sg.Title, sg.Reason, edgeRuleSuggestCommand(slug, sg))
		if alt := sg.Alternative; alt != nil {
			_, _ = fmt.Fprintf(osStdout, "  Or %s: %s\n", strings.ToLower(alt.Title[:1])+alt.Title[1:],
				edgeRuleSuggestCommand(slug, api.EdgeRuleRecipeSuggestion{Recipe: alt.Recipe, Params: alt.Params}))
		}
		_, _ = fmt.Fprintln(osStdout)
	}
	_, _ = fmt.Fprint(osStdout, rateLimits)
	return 0
}

// edgeRuleSuggestCommand is the recipe command that adds sg, with its
// parameters in name order and a placeholder for each required one the
// suggestion cannot fill.
func edgeRuleSuggestCommand(slug string, sg api.EdgeRuleRecipeSuggestion) string {
	parts := []string{"gregale edge rule recipe", sg.Recipe, "--app", slug}
	names := make([]string, 0, len(sg.Params))
	for name := range sg.Params {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		parts = append(parts, "--set", quoteCLIArg(name+"="+sg.Params[name]))
	}
	if recipe, ok := api.EdgeRuleRecipeByID(sg.Recipe); ok {
		for _, p := range recipe.Params {
			if _, set := sg.Params[p.Name]; p.Required && !set {
				parts = append(parts, "--set", p.Name+"=<"+p.Name+">")
			}
		}
	}
	return strings.Join(parts, " ")
}

// quoteCLIArg single-quotes arg for a POSIX shell unless it is plainly safe,
// so a suggested path such as /admin/** is not glob-expanded.
func quoteCLIArg(arg string) string {
	if arg != "" && strings.IndexFunc(arg, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
			(r < '0' || r > '9') && !strings.ContainsRune("_@%+=:,./-", r)
	}) == -1 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
}
