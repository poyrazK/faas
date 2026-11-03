package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	subLogsSearch = "search"
	subLogsFacets = "facets"
)

// logSearchFlags are the ledger filters `gregale logs search` and
// `gregale logs facets` share (ADR-20261018083536).
type logSearchFlags struct {
	app, query, since, until, source, level, route, status, deployment, request, trace, saved *string
}

func newLogSearchFlags(fs *flag.FlagSet) logSearchFlags {
	return logSearchFlags{
		app:        fs.String("app", "", appSlugFlagUsage),
		query:      fs.String("q", "", "case-insensitive text the message contains (2..256 characters; windows up to 24h)"),
		since:      fs.String("since", "", "window start: lookback (15m, 6h, 3d) or RFC3339 (default 1h)"),
		until:      fs.String("until", "", "window end, RFC3339 (default now)"),
		source:     fs.String("source", "", "runtime, build, deploy, http, network or dns"),
		level:      fs.String("level", "", "comma list of levels, e.g. error,warn"),
		route:      fs.String("route", "", "exact route template, e.g. /checkout/{id}"),
		status:     fs.String("status", "", "HTTP status (502) or class (5xx)"),
		deployment: fs.String("deployment", "", "deployment id"),
		request:    fs.String("request", "", "request id or trace id"),
		trace:      fs.String("trace", "", "W3C trace id"),
		saved:      fs.String("saved", "", "apply a saved search's filters (gregale logs saved); other flags override them"),
	}
}

func (f logSearchFlags) values() url.Values {
	q := url.Values{}
	for key, v := range map[string]*string{
		"q": f.query, "since": f.since, "until": f.until, "source": f.source, "level": f.level, "route": f.route,
		"status": f.status, "deployment_id": f.deployment, "request_id": f.request, "trace_id": f.trace, "saved": f.saved,
	} {
		if s := strings.TrimSpace(*v); s != "" {
			q.Set(key, s)
		}
	}
	return q
}

// storedValues are the filters a saved search keeps: no window, no saved name.
func (f logSearchFlags) storedValues() url.Values {
	q := f.values()
	for _, key := range []string{"since", "until", "saved"} {
		q.Del(key)
	}
	return q
}

// pageValues adds paging to the filters; the default limit is left to the server.
func (f logSearchFlags) pageValues(limit int, cursor string) url.Values {
	q := f.values()
	if limit != 100 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return q
}

// parseLogSearchArgs parses flags around an optional app positional and
// resolves the target app (explicit, --app, or the linked project app).
func parseLogSearchArgs(fs *flag.FlagSet, f logSearchFlags, args []string, usage string) (string, bool) {
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return "", false
	}
	if rejectUnexpectedFlagArgs(fs) {
		return "", false
	}
	pos, err := mergeAppFlag(pos, *f.app, 1)
	if err != nil {
		printErr("Invalid app target", err)
		PrintUsage(os.Stderr, usage, "logs")
		return "", false
	}
	target := ""
	if len(pos) == 1 {
		target = pos[0]
	}
	slug, err := resolveLogsAppTarget(target, false)
	if err != nil {
		printErr("Invalid app target", err)
		return "", false
	}
	return slug, true
}

// cmdLogsSearch implements `gregale logs search [<slug>]`.
func cmdLogsSearch(args []string) int {
	const usage = "usage: gregale logs search [<slug>] [--q TEXT] [--since 1h] [--until RFC3339] [--source S] [--level error,warn] [--route R] [--status 5xx] [--release ID] [--request ID] [--trace ID] [--saved NAME] [--limit N] [--cursor C]"
	fs := newFlagSet("logs search", flag.ContinueOnError)
	f := newLogSearchFlags(fs)
	limit := fs.Int("limit", 100, "events per page (1..200)")
	cursor := fs.String("cursor", "", "next_cursor from the previous page")
	slug, ok := parseLogSearchArgs(fs, f, args, usage)
	if !ok {
		return 1
	}
	q := f.pageValues(*limit, *cursor)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.SearchAppLogEvents(context.Background(), slug, q)
	if err != nil {
		return printErr("Log search failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	renderLogSearch(osStdout, slug, out)
	return 0
}

// cmdLogsFacets implements `gregale logs facets [<slug>]`.
func cmdLogsFacets(args []string) int {
	const usage = "usage: gregale logs facets [<slug>] [--q TEXT] [--since 1h] [--until RFC3339] [--source S] [--level error,warn] [--route R] [--status 5xx] [--release ID] [--request ID] [--trace ID] [--saved NAME]"
	fs := newFlagSet("logs facets", flag.ContinueOnError)
	f := newLogSearchFlags(fs)
	slug, ok := parseLogSearchArgs(fs, f, args, usage)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.GetAppLogEventFacets(context.Background(), slug, f.values())
	if err != nil {
		return printErr("Log facets failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	renderLogFacets(osStdout, slug, out)
	return 0
}

func renderLogSearch(w io.Writer, slug string, out api.LogEventsResponse) {
	_, _ = fmt.Fprintf(w, "%s: log events from %s to %s\n", slug, out.Since.Format(time.RFC3339), out.Until.Format(time.RFC3339))
	if len(out.Events) == 0 {
		_, _ = fmt.Fprintln(w, "(no matching events)")
		return
	}
	for _, e := range out.Events {
		detail := e.Level
		if e.Source == api.LogSourceHTTP {
			detail = strings.TrimSpace(fmt.Sprintf("%d %s %s", e.Status, e.Method, e.Route))
		}
		count := ""
		if e.Count > 1 {
			count = fmt.Sprintf(" (x%d)", e.Count)
		}
		_, _ = fmt.Fprintf(w, "%s  %-7s %-24s %s%s\n", e.Timestamp, e.Source, detail, e.Message, count)
	}
	if out.NextCursor != "" {
		_, _ = fmt.Fprintf(w, "(more events: add --cursor %s)\n", out.NextCursor)
	}
}

func renderLogFacets(w io.Writer, slug string, out api.LogEventFacetsResponse) {
	_, _ = fmt.Fprintf(w, "%s: %d events from %s to %s\n", slug, out.Total, out.Since.Format(time.RFC3339), out.Until.Format(time.RFC3339))
	for _, facet := range []struct {
		name   string
		counts []api.LogFacetCount
	}{{"Sources", out.Sources}, {"Levels", out.Levels}, {"Status", out.StatusClasses}, {"Routes", out.Routes}} {
		if len(facet.counts) == 0 {
			continue
		}
		parts := make([]string, 0, len(facet.counts))
		for _, c := range facet.counts {
			parts = append(parts, fmt.Sprintf("%s %d", c.Value, c.Count))
		}
		_, _ = fmt.Fprintf(w, "  %-8s %s\n", facet.name+":", strings.Join(parts, ", "))
	}
	if out.RoutesTruncated {
		_, _ = fmt.Fprintf(w, "  (showing the %d busiest routes)\n", len(out.Routes))
	}
}
