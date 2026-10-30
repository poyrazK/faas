package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

const routesInsightsUsage = "usage: gregale api route insights APP [--fix INSIGHT_ID]"

// cmdRoutesInsights lists an app's proactive route insights (ADR-1198), or
// applies one insight's fix (ADR-20261020161310).
func cmdRoutesInsights(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("routes insights", flag.ContinueOnError)
	fix := fs.String("fix", "", "apply the fix this insight lists now")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) {
		return printErr("Invalid route insights command", errors.New(routesInsightsUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*api.RouteCheckTimeout)
	defer cancel()
	insights, err := client.GetRouteInsights(ctx, positional[0])
	if err != nil {
		return printErr("Could not read route insights", err)
	}
	if *fix != "" {
		return fixRouteInsight(ctx, client, insights, *fix)
	}
	if jsonOutput {
		return jsonOut(writeJSON(insights))
	}
	renderRouteInsights(osStdout, insights)
	return 0
}

// fixRouteInsight applies the fix the listing just showed, so the server
// refuses it if anything changed in between.
func fixRouteInsight(ctx context.Context, client *api.Client, insights api.RouteInsightsResponse, id string) int {
	var insight *api.RouteInsight
	for i := range insights.Insights {
		if insights.Insights[i].ID == id {
			insight = &insights.Insights[i]
		}
	}
	switch {
	case insight == nil:
		return printErr("Unknown route insight", fmt.Errorf("%s has no route insight %q; run `gregale api route insights %s`", insights.Slug, id, insights.Slug))
	case insight.Fix == nil:
		return printErr("No fix to apply", fmt.Errorf("insight %s has no fix the platform can apply; its message says what to change", id))
	}
	out, err := client.FixRouteInsight(ctx, insights.Slug, id, insight.Fix.ID)
	if err != nil {
		return printErr("Could not apply the fix", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Applied: %s\nEdge rule: %s\n", out.Fix.Summary, out.Rule.ID)
	switch out.CachePurge {
	case api.RouteInsightPurgeRequested:
		_, _ = fmt.Fprintf(osStdout, "Cache purge requested for %s %s.\n", insight.Method, insight.Route)
	case api.RouteInsightPurgeFailed:
		_, _ = fmt.Fprintf(osStdout, "The cache purge could not be requested; run `gregale edge cache purge %s --path <glob>` to drop stale responses now.\n", insights.Slug)
	}
	return 0
}

func renderRouteInsights(w io.Writer, r api.RouteInsightsResponse) {
	if len(r.Insights) == 0 {
		_, _ = fmt.Fprintf(w, "No route insights for %s. Apps that ran in the last day are evaluated every %s.\n", r.Slug, api.RouteInsightInterval)
		return
	}
	_, _ = fmt.Fprintf(w, "Route insights for %s\n", r.Slug)
	fixable := false
	for _, insight := range r.Insights {
		status := fmt.Sprintf("active since %s", insight.FirstDetectedAt)
		if insight.State == api.RouteInsightResolved {
			status = fmt.Sprintf("resolved %s", insight.ResolvedAt)
			if insight.FixedAt != "" {
				status = fmt.Sprintf("fixed %s (%s)", insight.FixedAt, insight.FixAction)
			}
		}
		_, _ = fmt.Fprintf(w, "\n[%s] %s %s — %s\n", insight.Kind, insight.Method, insight.Route, status)
		_, _ = fmt.Fprintf(w, "  %s\n", insight.Message)
		if insight.Fix != nil {
			fixable = true
			_, _ = fmt.Fprintf(w, "  Fix:  %s\n  ID:   %s\n", insight.Fix.Summary, insight.ID)
		}
	}
	if fixable {
		_, _ = fmt.Fprintf(w, "\nApply a fix with `gregale api route insights %s --fix ID`.\n", r.Slug)
	}
	_, _ = fmt.Fprintln(w, "\nSubscribe an app webhook to routes.insight.detected and routes.insight.resolved, or enable the \"New route insights\" alert preset, to be told when these change.")
}
