package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const analyticsCmdUsage = "usage: gregale analytics <slug> [--since 24h] [--until RFC3339] [--by route|country|referrer_host|ua_family|status]"
const analyticsCmdDocsTopic = "analytics"

var analyticsGroupBys = map[string]struct{}{
	"route": {}, "country": {}, "referrer_host": {}, "ua_family": {}, "status": {},
}

func cmdAnalytics(args []string) int {
	fs := flag.NewFlagSet("analytics", flag.ContinueOnError)
	since := fs.String("since", "24h", "lookback window (for example 24h or 7d)")
	until := fs.String("until", "", "exclusive RFC3339 end timestamp")
	by := fs.String("by", "route", "grouping dimension")
	if err := fs.Parse(normalizeAnalyticsArgs(args)); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, analyticsCmdUsage, analyticsCmdDocsTopic)
		return 1
	}
	if _, ok := analyticsGroupBys[*by]; !ok {
		PrintUsage(os.Stderr, analyticsCmdUsage+"\nerror: --by must be one of route, country, referrer_host, ua_family, status", analyticsCmdDocsTopic)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	response, err := client.GetAppRequestAnalyticsOpts(context.Background(), fs.Arg(0), api.AppRequestAnalyticsOptions{
		Since: *since, Until: *until, GroupBy: *by,
	})
	if err != nil {
		return printErr("Could not fetch analytics", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	renderRequestAnalytics(osStdout, response)
	return 0
}

// normalizeAnalyticsArgs permits the documented slug-first form while keeping
// the traditional flags-first spelling. flag.FlagSet stops parsing at the
// first positional, so move the one slug behind the known value flags.
func normalizeAnalyticsArgs(args []string) []string {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, 1)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--since=") || strings.HasPrefix(arg, "--until=") || strings.HasPrefix(arg, "--by=") {
			flags = append(flags, arg)
			continue
		}
		if arg == "--since" || arg == "--until" || arg == "--by" {
			flags = append(flags, arg)
			if i+1 >= len(args) {
				// Preserve flag.FlagSet's native "needs an argument" error
				// without letting an earlier slug become the missing value.
				return flags
			}
			i++
			flags = append(flags, args[i])
			continue
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			continue
		}
		positionals = append(positionals, arg)
	}
	return append(flags, positionals...)
}

func renderRequestAnalytics(w io.Writer, response api.RequestAnalyticsResponse) {
	_, _ = fmt.Fprintf(w, "App:        %s\n", response.Slug)
	_, _ = fmt.Fprintf(w, "Group by:   %s\n", response.GroupBy)
	_, _ = fmt.Fprintf(w, "Window:     %s (%s to %s)\n", response.Since, response.From, response.Until)
	_, _ = fmt.Fprintf(w, "Requests:   %d\n", response.Requests)
	_, _ = fmt.Fprintf(w, "Errors:     %d (%.2f%%)\n", response.ErrorRequests, response.ErrorRatePct)
	_, _ = fmt.Fprintf(w, "Cold boots: %d\n", response.ColdBoots)
	_, _ = fmt.Fprintf(w, "Latency:    p50=%dms p95=%dms p99=%dms\n", response.P50MS, response.P95MS, response.P99MS)
	if len(response.Groups) == 0 {
		_, _ = fmt.Fprintln(w, "Groups:     (none)")
		return
	}
	_, _ = fmt.Fprintln(w, "\nValue\tMethod\tRequests\tErrors\tp50\tp95\tp99")
	for _, group := range response.Groups {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%d (%.2f%%)\t%dms\t%dms\t%dms\n",
			group.Value, group.Method, group.Requests, group.ErrorRequests,
			group.ErrorRatePct, group.P50MS, group.P95MS, group.P99MS)
	}
	if response.GroupsTruncated {
		_, _ = fmt.Fprintf(w, "\nShowing the top %d groups plus __other__.\n", response.GroupsLimit)
	}
	if response.WindowClamped {
		_, _ = fmt.Fprintln(w, "Note: requested window was clamped to plan telemetry retention.")
	}
}
