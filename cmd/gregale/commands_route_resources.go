package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeresources"
)

const routeResourcesCmdUsage = "usage: gregale metric resources [<slug>] [--since 24h]"

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
	if r.Split != nil {
		_, _ = fmt.Fprintln(w)
		renderRouteSplit(w, r.Slug, "", r.Split)
	}
}

const appScaleSuggestUsage = "usage: gregale app scale <slug> --suggest [--since 24h] [--apply-split] | --split-status | --undo-split ID"

// isAppScaleSplitArg reports whether an app scale argument selects the
// route split mode.
func isAppScaleSplitArg(arg string) bool {
	name := strings.TrimLeft(arg, "-")
	if i := strings.IndexByte(name, '='); i >= 0 {
		name = name[:i]
	}
	return strings.HasPrefix(arg, "-") && (name == "suggest" || name == "apply-split" || name == "split-status" || name == "undo-split")
}

// cmdAppScaleSuggest is `gregale app scale <slug> --suggest`: the split the
// route resource report suggests, as commands (ADR-20261121170000
// amendment 1). --apply-split applies it on the server, --split-status
// follows it and --undo-split reverses it (amendment 2).
func cmdAppScaleSuggest(slug string, args []string) int {
	fs := newFlagSet("app scale --suggest", flag.ContinueOnError)
	_ = fs.Bool("suggest", true, "suggest moving a heavy route to its own app")
	since := fs.String("since", "24h", "lookback window, at most 336h")
	apply := fs.Bool("apply-split", false, "apply the suggested split on the server")
	status := fs.Bool("split-status", false, "show the app's route splits")
	undo := fs.String("undo-split", "", "reverse a finished split by ID")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		PrintUsage(os.Stderr, appScaleSuggestUsage, analyticsCmdDocsTopic)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	switch {
	case *status:
		return appRouteSplitStatus(client, slug)
	case *undo != "":
		return appRouteSplitUndo(client, slug, *undo)
	}
	response, err := client.GetAppRouteResources(context.Background(), slug, api.RouteResourcesOptions{Since: *since})
	if err != nil {
		return printErr("Could not fetch route resource peaks", err)
	}
	if response.Split != nil && *apply {
		return appRouteSplitApply(client, slug, *since, response.Split)
	}
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	if response.Split == nil {
		_, _ = fmt.Fprintf(osStdout, "No split suggested for %s over the last %s: no route holds most of its memory or CPU peaks while running in few minutes.\n", slug, *since)
		_, _ = fmt.Fprintf(osStdout, "Peaks: %d memory, p95 %d MB of %d MB; %d CPU. Details: gregale metric resources %s --since %s\n",
			response.HighMemoryMinutes, response.MemPeakP95MB, response.MemLimitMB, response.HighCPUMinutes, slug, *since)
		if *apply {
			return 1
		}
		return 0
	}
	host := ""
	if app, err := client.GetApp(context.Background(), slug); err == nil {
		if u, perr := url.Parse(app.URL); perr == nil {
			host = u.Hostname()
		}
	}
	renderRouteSplit(osStdout, slug, host, response.Split)
	_, _ = fmt.Fprintf(osStdout, "\nOr let Gregale do all of it: gregale app scale %s --suggest --since %s --apply-split\n", slug, *since)
	return 0
}

// appRouteSplitApply applies the suggestion the read returned; the server
// refuses with route_split_changed when it moved since.
func appRouteSplitApply(client *api.Client, slug, since string, s *routeresources.Split) int {
	kind := s.Kind
	if kind == "" {
		kind = routeresources.SplitMemory
	}
	split, err := client.CreateAppRouteSplit(context.Background(), slug, api.CreateRouteSplitRequest{
		Kind: kind, Route: s.Route, Method: s.Method, RouteRAMMB: s.RouteRAMMB, MainRAMMB: s.MainRAMMB, Since: since,
	})
	if err != nil {
		return printErr("Could not apply the split", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(split))
	}
	_, _ = fmt.Fprintf(osStdout, "Split %s started: %s moves to %s.\n", split.ID, split.Route, split.TargetSlug)
	_, _ = fmt.Fprintf(osStdout, "  %s gets the app's live build, variables and secrets; once it is live, an edge rule sends %s %s to it.\n",
		split.TargetSlug, orAny(split.Method), split.PathGlob)
	if split.Kind == routeresources.SplitMemory {
		_, _ = fmt.Fprintf(osStdout, "  Then %s is sized down from %d MB to %d MB.\n", slug, split.PreviousRAMMB, split.MainRAMMB)
	}
	_, _ = fmt.Fprintf(osStdout, "Follow it: gregale app scale %s --split-status\nUndo it once done: gregale app scale %s --undo-split %s\n", slug, slug, split.ID)
	return 0
}

func orAny(method string) string {
	if method == "" {
		return "requests to"
	}
	return method
}

func appRouteSplitStatus(client *api.Client, slug string) int {
	out, err := client.ListAppRouteSplits(context.Background(), slug)
	if err != nil {
		return printErr("Could not read route splits", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	renderRouteSplits(osStdout, slug, out.Splits)
	return 0
}

func renderRouteSplits(w io.Writer, slug string, splits []api.RouteSplitResponse) {
	if len(splits) == 0 {
		_, _ = fmt.Fprintf(w, "%s has no route splits. Suggest one: gregale app scale %s --suggest\n", slug, slug)
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tROUTE\tAPP\tKIND\tPHASE\tNOTE")
	for _, s := range splits {
		note := s.Error
		if note == "" {
			note = s.Warning
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Route, s.TargetSlug, s.Kind, s.Phase, note)
	}
	_ = tw.Flush()
}

func appRouteSplitUndo(client *api.Client, slug, id string) int {
	out, err := client.UndoAppRouteSplit(context.Background(), slug, id)
	if err != nil {
		return printErr("Could not undo the split", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Split %s is %s: %s serves %s again", out.ID, out.Phase, slug, out.Route)
	if out.MainResized {
		_, _ = fmt.Fprintf(osStdout, " at %d MB", out.PreviousRAMMB)
	}
	_, _ = fmt.Fprintf(osStdout, ", and %s is deleted.\n", out.TargetSlug)
	return 0
}

func renderRouteSplit(w io.Writer, slug, host string, s *routeresources.Split) {
	target := splitTargetSlug(slug, s.PathGlob)
	if host == "" {
		host = "<" + slug + " host>"
	}
	_, _ = fmt.Fprintf(w, "Suggested split: %s\n", s.Reason)
	cpu := s.Kind == routeresources.SplitCPU
	if cpu {
		_, _ = fmt.Fprintf(w, "  Both apps keep %d MB; billing is by RAM, so this split isolates the route rather than saving money.\n", s.CurrentRAMMB)
	} else {
		_, _ = fmt.Fprintf(w, "  %s now: %d MB. Without the route: %d MB. The route on its own: %d MB.\n", slug, s.CurrentRAMMB, s.MainRAMMB, s.RouteRAMMB)
	}
	if s.RouteRAMOverPlan {
		_, _ = fmt.Fprintf(w, "  The route needs more memory than your plan's largest app; upgrade the plan or send it with --kind async to a job.\n")
	}
	_, _ = fmt.Fprintln(w, "\n  1. Deploy the same code as a second app:")
	_, _ = fmt.Fprintf(w, "     gregale deploy --name %s\n", target)
	step := 2
	if !cpu {
		_, _ = fmt.Fprintln(w, "  2. Size it for the route:")
		_, _ = fmt.Fprintf(w, "     gregale app scale %s --plan --ram %d\n", target, s.RouteRAMMB)
		step++
	}
	_, _ = fmt.Fprintf(w, "  %d. Send the route to it:\n", step)
	method := ""
	if s.Method != "" {
		// Only the method the peaks came with moves; the path's other
		// methods stay on the app.
		method = " --match-method " + s.Method
	}
	_, _ = fmt.Fprintf(w, "     gregale edge rule create --app %s --kind route --match-host %s --match-path '%s'%s --route-target-slug %s --enabled\n",
		slug, host, s.PathGlob, method, target)
	if cpu {
		return
	}
	_, _ = fmt.Fprintf(w, "  %d. Size the app down once the rule is live:\n", step+1)
	_, _ = fmt.Fprintf(w, "     gregale app scale %s --plan --ram %d\n", slug, s.MainRAMMB)
}

// splitTargetSlug names the route's app after the route's last static
// segment: shop + /exports/* → shop-exports.
func splitTargetSlug(slug, glob string) string {
	name := "heavy"
	segments := strings.Split(strings.Trim(glob, "/"), "/")
	for i := len(segments) - 1; i >= 0; i-- {
		seg := strings.ToLower(segments[i])
		clean := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				return r
			}
			return -1
		}, seg)
		if clean != "" && !strings.Contains(seg, "*") {
			name = clean
			break
		}
	}
	out := slug + "-" + name
	if len(out) > 63 {
		out = strings.TrimRight(out[:63], "-")
	}
	return out
}

func startsWithMethod(route, method string) bool {
	return len(route) > len(method) && route[:len(method)] == method && route[len(method)] == ' '
}
