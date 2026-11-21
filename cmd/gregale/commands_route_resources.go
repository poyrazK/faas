package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

const routeResourcesCmdUsage = "usage: gregale metric route-resources [<slug>] [--since 24h]"

// cmdRouteResources prints which routes an app's memory and CPU peaks come
// from (ADR-20261121170000).
func cmdRouteResources(args []string) int {
	fs := newFlagSet("route-resources", flag.ContinueOnError)
	since := fs.String("since", "24h", "lookback window, at most 336h")
	app := fs.String("app", "", appSlugFlagUsage)
	window := fs.String("range", "", "lookback window; same as --since")
	if err := fs.Parse(normalizeAnalyticsArgs(args)); err != nil {
		return 1
	}
	if *window != "" {
		*since = *window
	}
	pos, err := mergeAppFlag(fs.Args(), *app, 1)
	if err != nil || len(pos) > 1 {
		PrintUsage(os.Stderr, routeResourcesCmdUsage, analyticsCmdDocsTopic)
		return 1
	}
	slug := ""
	if len(pos) == 1 {
		slug = pos[0]
	} else {
		var resolveErr error
		slug, resolveErr = resolveRequiredAppSlug("")
		if resolveErr != nil {
			if errors.Is(resolveErr, errProjectContextNotFound) {
				PrintUsage(os.Stderr, routeResourcesCmdUsage+"\nOr run `gregale link <project-slug>` in this checkout.", analyticsCmdDocsTopic)
				return 1
			}
			return printErr("Could not read local project context", resolveErr)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	response, err := client.GetAppRouteResources(context.Background(), slug, api.RouteResourcesOptions{Since: *since})
	if err != nil {
		return printErr("Could not fetch route resource peaks", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	renderRouteResources(osStdout, response)
	return 0
}

func renderRouteResources(w io.Writer, r api.RouteResourcesResponse) {
	_, _ = fmt.Fprintf(w, "App:     %s\n", r.Slug)
	_, _ = fmt.Fprintf(w, "Window:  %s to %s\n", analyticsWindowTime(r.From), analyticsWindowTime(r.Until))
	_, _ = fmt.Fprintf(w, "Memory:  p50 %d MB, p95 %d MB, max %d MB", r.MemPeakP50MB, r.MemPeakP95MB, r.MemPeakMaxMB)
	if r.MemLimitMB > 0 {
		_, _ = fmt.Fprintf(w, " of %d MB", r.MemLimitMB)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "Peaks:   %d memory (rise >= %d MB, %d with no request in flight), %d CPU\n",
		r.HighMemoryMinutes, r.HighMemoryRiseMB, r.UnattributedHighMemoryMinutes, r.HighCPUMinutes)
	_, _ = fmt.Fprintf(w, "Minutes: %d with samples, %d serving requests", r.ResourceMinutes, r.ActiveMinutes)
	if r.RouteMinutesWithoutResources > 0 {
		_, _ = fmt.Fprintf(w, ", %d serving without samples (left out)", r.RouteMinutesWithoutResources)
	}
	_, _ = fmt.Fprintln(w)
	if r.Truncated {
		_, _ = fmt.Fprintln(w, "Note:    the window held more minutes than one read analyses; the newest were used.")
	}
	if len(r.Routes) == 0 {
		_, _ = fmt.Fprintln(w, "\nNo route activity with samples in this window.")
		return
	}
	_, _ = fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ROUTE\tMEM PEAKS\tSOLE\tRUNS IN\tP95 WITH\tP95 WITHOUT\tCPU PEAKS\tREQUESTS")
	for _, rt := range r.Routes {
		name := rt.Route
		if rt.Method != "" && !startsWithMethod(rt.Route, rt.Method) {
			name = rt.Method + " " + rt.Route
		}
		_, _ = fmt.Fprintf(tw, "%s\t%.0f%%\t%d\t%.0f%%\t%d MB\t%d MB\t%.0f%%\t%d\n", name,
			rt.HighMemoryShare*100, rt.SoleHighMemoryMinutes, rt.PresenceShare*100,
			rt.MemPeakP95WithMB, rt.MemPeakP95WithoutMB, rt.HighCPUShare*100, rt.Requests)
	}
	_ = tw.Flush()
	if r.RoutesOmitted > 0 {
		_, _ = fmt.Fprintf(w, "(%d more routes omitted)\n", r.RoutesOmitted)
	}
	_, _ = fmt.Fprintln(w, "\nMEM PEAKS is the share of memory peaks the route was in flight for; RUNS IN the share of minutes it ran in at all.")
	_, _ = fmt.Fprintln(w, "A route with a high peak share that runs in few minutes is the one sizing the app.")
}

func startsWithMethod(route, method string) bool {
	return len(route) > len(method) && route[:len(method)] == method && route[len(method)] == ' '
}
