package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

const routesInsightsUsage = "usage: gregale routes insights APP"

// cmdRoutesInsights lists an app's proactive route insights (ADR-1198).
func cmdRoutesInsights(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("routes insights", flag.ContinueOnError)
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
	if jsonOutput {
		return jsonOut(writeJSON(insights))
	}
	renderRouteInsights(osStdout, insights)
	return 0
}

func renderRouteInsights(w io.Writer, r api.RouteInsightsResponse) {
	if len(r.Insights) == 0 {
		_, _ = fmt.Fprintf(w, "No route insights for %s. Apps that ran in the last day are evaluated every %s.\n", r.Slug, api.RouteInsightInterval)
		return
	}
	_, _ = fmt.Fprintf(w, "Route insights for %s\n", r.Slug)
	for _, insight := range r.Insights {
		status := fmt.Sprintf("active since %s", insight.FirstDetectedAt)
		if insight.State == api.RouteInsightResolved {
			status = fmt.Sprintf("resolved %s", insight.ResolvedAt)
		}
		_, _ = fmt.Fprintf(w, "\n[%s] %s %s — %s\n", insight.Kind, insight.Method, insight.Route, status)
		_, _ = fmt.Fprintf(w, "  %s\n", insight.Message)
	}
	_, _ = fmt.Fprintln(w, "\nSubscribe an app webhook to routes.insight.detected and routes.insight.resolved to be told when these change.")
}
