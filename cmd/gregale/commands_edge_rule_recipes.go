package main

// ADR-986: protection recipes from the CLI. A recipe expands locally to an
// ordinary create request (pkg/api) and goes through the normal create
// endpoint, so the server's validation, plan gates and quotas apply.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const edgeRuleRecipeUsage = "usage: gregale edge-rules recipe <recipe> [--app <slug>] [--host <hostname>] [--set name=value]... [--enforce]"

// edgeRuleRecipeJSON is the --json shape of one catalog entry.
type edgeRuleRecipeJSON struct {
	ID      string                    `json:"id"`
	Title   string                    `json:"title"`
	Summary string                    `json:"summary"`
	LogNote string                    `json:"log_note"`
	Kind    string                    `json:"kind"`
	Params  []edgeRuleRecipeParamJSON `json:"params"`
}

type edgeRuleRecipeParamJSON struct {
	Name     string `json:"name"`
	Help     string `json:"help"`
	Default  string `json:"default,omitempty"`
	Required bool   `json:"required,omitempty"`
}

// cmdEdgeRulesRecipes lists the protection recipes.
func cmdEdgeRulesRecipes(args []string) int {
	fs := newFlagSet("edge-rules recipes", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if jsonOutput {
		out := make([]edgeRuleRecipeJSON, 0, len(api.EdgeRuleRecipes))
		for _, r := range api.EdgeRuleRecipes {
			entry := edgeRuleRecipeJSON{ID: r.ID, Title: r.Title, Summary: r.Summary, LogNote: r.LogNote, Kind: r.Kind}
			for _, p := range r.Params {
				entry.Params = append(entry.Params, edgeRuleRecipeParamJSON{Name: p.Name, Help: p.Help, Default: p.Default, Required: p.Required})
			}
			out = append(out, entry)
		}
		return jsonOut(writeJSON(out))
	}
	for _, r := range api.EdgeRuleRecipes {
		_, _ = fmt.Fprintf(osStdout, "%s — %s\n  %s\n  %s\n", r.ID, r.Title, r.Summary, r.LogNote)
		for _, p := range r.Params {
			hint := p.Default
			if p.Required {
				hint = "required"
			}
			if hint == "" {
				hint = "optional"
			}
			_, _ = fmt.Fprintf(osStdout, "  --set %s=…  %s (%s)\n", p.Name, p.Help, hint)
		}
		_, _ = fmt.Fprintln(osStdout)
	}
	return 0
}

// cmdEdgeRulesRecipe adds one recipe's rule to an app, in log mode unless
// --enforce is given.
func cmdEdgeRulesRecipe(args []string) int {
	fs := newFlagSet("edge-rules recipe", flag.ContinueOnError)
	appFlag := fs.String("app", "", "app slug (defaults to linked project context)")
	hostFlag := fs.String("host", "", "restrict the rule to one hostname (default: every host the app serves)")
	var sets multiFlag
	fs.Var(&sets, "set", "recipe parameter as name=value (repeat; see gregale edge-rules recipes)")
	enforce := fs.Bool("enforce", false, "enforce immediately instead of starting in log mode")
	flags, positional := splitArgsForFlags(args, "enforce")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 {
		PrintUsage(os.Stderr, edgeRuleRecipeUsage, "edge-rules")
		return 1
	}
	recipe, ok := api.EdgeRuleRecipeByID(positional[0])
	if !ok {
		return printErr("Unknown recipe", fmt.Errorf("%q is not a recipe (%s)", positional[0], strings.Join(api.EdgeRuleRecipeIDs(), ", ")))
	}
	values := map[string]string{}
	for _, s := range sets {
		name, value, found := strings.Cut(s, "=")
		if !found || strings.TrimSpace(name) == "" {
			return printErr("Invalid --set", fmt.Errorf("%q is not name=value", s))
		}
		values[strings.TrimSpace(name)] = value
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
	// ADR-1191: by default the rule covers every host the app serves,
	// including custom domains added later; --host restricts it to one.
	host := api.EdgeRuleAppHosts
	if raw := strings.TrimSpace(*hostFlag); raw != "" {
		if host, err = normalizeCacheHost(raw); err != nil {
			return printErr("Invalid host", err)
		}
	}
	req, prob := api.BuildEdgeRuleRecipe(recipe.ID, host, values, *enforce)
	if prob != nil {
		return printErr("Invalid recipe input", errToError(prob))
	}
	out, err := client.CreateEdgeRule(context.Background(), slug, req)
	if err != nil {
		return printErr("Recipe failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if *enforce {
		PrintOK(osStdout, "Added %q on %s %s (rule %s), enforced.", recipe.Title, recipeHostLabel(host), req.MatchPath, out.ID)
		return 0
	}
	PrintOK(osStdout, "Added %q on %s %s (rule %s) in log mode: it is counted but never acts.", recipe.Title, recipeHostLabel(host), req.MatchPath, out.ID)
	_, _ = fmt.Fprintf(osStdout, "  %s\n  Watch:   gregale edge-rules stats --app %s\n  Enforce: gregale edge-rules update %s --mode enforce\n", recipe.LogNote, slug, out.ID)
	return 0
}

// recipeHostLabel names the hosts a recipe rule covers.
func recipeHostLabel(host string) string {
	if api.IsEdgeRuleAppHosts(host) {
		return "every host of the app"
	}
	return host
}
