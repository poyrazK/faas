package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

const routesAwakeUsage = "usage: gregale routes awake APP [--since 7d] [--until RFC3339]"

// cmdRoutesAwake shows which routes keep a scale-to-zero app awake and what
// each costs in awake time (ADR-1187).
func cmdRoutesAwake(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("routes awake", flag.ContinueOnError)
	since := fs.String("since", "7d", "window to analyze (duration such as 7d or 168h, or RFC3339); clamped to the plan's telemetry retention")
	until := fs.String("until", "", "end of the window (RFC3339; default now)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || !validCLISlug(positional[0]) {
		return printErr("Invalid route awake command", errors.New(routesAwakeUsage))
	}
	if !validRouteHealthSuggestionSince(*since) {
		return printErr("Invalid route awake command", fmt.Errorf("--since must be a duration such as 7d or 168h, or an RFC3339 time; got %q", *since))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*api.RouteCheckTimeout)
	defer cancel()
	report, err := client.GetRouteAwake(ctx, positional[0], api.RouteAwakeOptions{Since: *since, Until: *until})
	if err != nil {
		return printErr("Could not read the awake report", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	renderRouteAwake(osStdout, report)
	return 0
}

func renderRouteAwake(w io.Writer, r api.RouteAwakeResponse) {
	window := awakeDuration(r.WindowSeconds)
	if r.WindowClamped {
		window += " (clamped to telemetry retention)"
	}
	_, _ = fmt.Fprintf(w, "%s was awake %.0f%% of the last %s: %s running, %.1f GB-h, %d wakes. Idle timeout: %ds.\n",
		r.Slug, 100*r.AwakeShare, window, awakeDuration(r.AwakeSeconds), r.GBHours, r.Wakes, r.IdleTimeoutSeconds)
	if r.AwakeSeconds == 0 {
		_, _ = fmt.Fprintln(w, "\nIt stayed parked the whole window.")
		return
	}
	_, _ = fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ROUTE\tREQUESTS\tWAKES\tAWAKE\tSHARE\tPATTERN\tWITHOUT IT")
	for _, route := range r.Routes {
		_, _ = fmt.Fprintf(tw, "%s %s\t%d\t%d\t%s\t%.0f%%\t%s\t%s\n", route.Method, route.Route, route.Requests, route.Wakes,
			awakeDuration(route.AwakeSeconds), 100*route.AwakeShare, awakePattern(route), awakeWhatIf(route.IfRemoved))
	}
	if r.OtherRoutesSeconds > 0 {
		_, _ = fmt.Fprintf(tw, "Other routes\t\t\t%s\t%.0f%%\t\t\n", awakeDuration(r.OtherRoutesSeconds), awakeShare(r.OtherRoutesSeconds, r.AwakeSeconds))
	}
	if r.UnattributedSeconds > 0 {
		_, _ = fmt.Fprintf(tw, "Unattributed\t\t\t%s\t%.0f%%\tprobes, prewarm, min instances, cron, queue\t\n", awakeDuration(r.UnattributedSeconds), awakeShare(r.UnattributedSeconds, r.AwakeSeconds))
	}
	_ = tw.Flush()
	printed := false
	for _, route := range r.Routes {
		if route.Suggestion == nil {
			continue
		}
		if !printed {
			_, _ = fmt.Fprintln(w, "\nSuggestions")
			printed = true
		}
		_, _ = fmt.Fprintf(w, "  %s\n", route.Suggestion.Message)
	}
	_, _ = fmt.Fprintln(w, "\nFigures are estimates at minute resolution. WITHOUT IT assumes every other request is unchanged.")
}

func awakePattern(r api.RouteAwakeRoute) string {
	if r.Pattern == api.RouteAwakePatternPeriodic && r.IntervalSeconds > 0 {
		return fmt.Sprintf("every ~%ds", r.IntervalSeconds)
	}
	return r.Pattern
}

func awakeWhatIf(w api.RouteAwakeWhatIf) string {
	if w.AwakeSecondsSaved == 0 && w.WakesChange == 0 {
		return "no change"
	}
	out := fmt.Sprintf("-%s awake", awakeDuration(w.AwakeSecondsSaved))
	if w.WakesChange != 0 {
		out += fmt.Sprintf(", %+d wakes", w.WakesChange)
	}
	return out
}

func awakeShare(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(part) / float64(total)
}

// awakeDuration renders whole seconds as "88h 1m", "45m" or "30s".
func awakeDuration(seconds int64) string {
	h, m, s := seconds/3600, seconds%3600/60, seconds%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm", m)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
