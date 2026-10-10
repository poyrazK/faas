package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const routesAdviseUsage = "usage: gregale routes advise APP [--since 7d] [--until RFC3339] [--cache-max-age SECONDS] [--apply ID [--enable]]"

// cmdRoutesAdvise lists route advisor suggestions (ADR-940) or applies one by
// creating its edge rules, disabled unless --enable is given.
func cmdRoutesAdvise(args []string) int {
	flags, positional := splitArgsForFlags(args, "enable")
	fs := newFlagSet("routes advise", flag.ContinueOnError)
	since := fs.String("since", "7d", "traffic window to analyze (duration such as 7d or 168h, or RFC3339); clamped to the plan's telemetry retention")
	until := fs.String("until", "", "end of the window (RFC3339; default now)")
	maxAge := fs.Int("cache-max-age", 0, "what-if cache lifetime in seconds for cache suggestions (default 60)")
	apply := fs.String("apply", "", "create the edge rules of this suggestion ID")
	enable := fs.Bool("enable", false, "with --apply, create the rules enabled instead of disabled for review")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) || *enable && *apply == "" {
		return printErr("Invalid route advice command", errors.New(routesAdviseUsage))
	}
	if !validRouteHealthSuggestionSince(*since) {
		return printErr("Invalid route advice command", fmt.Errorf("--since must be a duration such as 7d or 168h, or an RFC3339 time; got %q", *since))
	}
	if *maxAge < 0 || *maxAge > api.ResponseCacheMaxAgeMaxSeconds {
		return printErr("Invalid route advice command", fmt.Errorf("--cache-max-age must be between 1 and %d seconds", api.ResponseCacheMaxAgeMaxSeconds))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*api.RouteCheckTimeout)
	defer cancel()
	advice, err := client.GetRouteAdvice(ctx, positional[0], api.RouteAdviceOptions{Since: *since, Until: *until, CacheMaxAgeSeconds: *maxAge})
	if err != nil {
		return printErr("Could not read route advice", err)
	}
	if *apply != "" {
		return applyRouteAdvice(ctx, client, advice, *apply, *enable)
	}
	if jsonOutput {
		return jsonOut(writeJSON(advice))
	}
	renderRouteAdvice(osStdout, advice)
	return 0
}

// applyRouteAdvice creates every rule of one suggestion through the ordinary
// edge-rules API, so plan quotas and validation apply unchanged.
func applyRouteAdvice(ctx context.Context, client *api.Client, advice api.RouteAdviceResponse, id string, enable bool) int {
	var suggestion *api.RouteAdviceSuggestion
	for i := range advice.Suggestions {
		if advice.Suggestions[i].ID == id {
			suggestion = &advice.Suggestions[i]
		}
	}
	if suggestion == nil {
		return printErr("Unknown route advice", fmt.Errorf("no current suggestion %q for %s; run `gregale routes advise %s` with the same window", id, advice.Slug, advice.Slug))
	}
	created := []api.EdgeRuleResponse{}
	for _, rule := range suggestion.Rules {
		rule.Enabled = &enable
		out, err := client.CreateEdgeRule(ctx, advice.Slug, rule)
		if err != nil {
			if len(created) > 0 {
				_, _ = fmt.Fprintf(osStderr, "Created %d of %d rules before the failure: %s\n", len(created), len(suggestion.Rules), routeAdviceRuleIDs(created))
			}
			return printErr("Could not create edge rule for "+rule.MatchHost, err)
		}
		created = append(created, out)
	}
	if jsonOutput {
		return jsonOut(writeJSON(created))
	}
	state := "disabled; enable each with `gregale edge-rules update ID --enable` after review"
	if enable {
		state = "enabled"
	}
	_, _ = fmt.Fprintf(osStdout, "Created %d %s rules for %s %s (%s): %s\n", len(created), suggestion.Kind, suggestion.Method, suggestion.Route, state, routeAdviceRuleIDs(created))
	return 0
}

func routeAdviceRuleIDs(rules []api.EdgeRuleResponse) string {
	ids := make([]string, 0, len(rules))
	for _, r := range rules {
		ids = append(ids, r.ID)
	}
	return strings.Join(ids, ", ")
}

func renderRouteAdvice(w io.Writer, a api.RouteAdviceResponse) {
	_, _ = fmt.Fprintf(w, "Route advice for %s — traffic %s to %s, %ds cache what-if\n", a.Slug, a.From, a.Until, a.CacheMaxAgeSeconds)
	if a.WindowClamped {
		_, _ = fmt.Fprintln(w, "The window was shortened to the plan's telemetry retention.")
	}
	_, _ = fmt.Fprintf(w, "%d routes analyzed · %d suggestions\n", a.RoutesAnalyzed, len(a.Suggestions))
	if len(a.Suggestions) == 0 {
		_, _ = fmt.Fprintln(w, "\nNo suggestions: no observed route clears the evidence thresholds, or the app already has the matching rules.")
		return
	}
	for _, s := range a.Suggestions {
		_, _ = fmt.Fprintf(w, "\n[%s] %s — %s\n", s.ID, s.Kind, s.Title)
		_, _ = fmt.Fprintf(w, "  Why:     %s\n", s.Rationale)
		_, _ = fmt.Fprintf(w, "  Impact:  %s\n", s.Impact.Summary)
		for _, c := range s.Cautions {
			_, _ = fmt.Fprintf(w, "  Caution: %s\n", c)
		}
		hosts := make([]string, 0, len(s.Rules))
		for _, r := range s.Rules {
			hosts = append(hosts, r.MatchHost)
		}
		_, _ = fmt.Fprintf(w, "  Rules:   %d (%s)\n", len(s.Rules), strings.Join(hosts, ", "))
	}
	_, _ = fmt.Fprintf(w, "\nApply one with `gregale routes advise %s --apply ID` (same --since and --cache-max-age); rules are created disabled unless you add --enable.\n", a.Slug)
}
