package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale insight ...` (ADR 20261117084505 §4): product analytics over
// track() events. The tree names alias `gregale insights <verb>`;
// `gregale analytics` is request analytics.

const insightsVerbs = "events|funnel|retention|requests|cohorts"

type insightsFlags struct {
	project, environment, from, until, unit, by *string
	interval, window, period, flag              *string
	periods                                     *int
	where                                       []string
}

func newInsightsFlags(fs *flag.FlagSet) *insightsFlags {
	f := &insightsFlags{
		project:     fs.String("project", "", "project slug"),
		environment: fs.String("environment", "production", "named project environment"),
		from:        fs.String("from", "", "start: an RFC 3339 time or a duration ago such as 14d or 36h (default 7 days, 28 for retention)"),
		until:       fs.String("until", "", "end: an RFC 3339 time or a duration ago (default now)"),
		unit:        fs.String("unit", "", "customer (default) or subject"),
		by:          fs.String("by", "", "breakdown: flag:KEY or property:KEY"),
		interval:    fs.String("interval", "", "events: day (default) or hour"),
		window:      fs.String("window", "", "funnel: conversion window, 1h to 720h (default 168h)"),
		period:      fs.String("period", "", "retention: day (default) or week"),
		periods:     fs.Int("periods", 0, "retention: periods after the cohort, 1 to 12 (default 7)"),
		flag:        fs.String("flag", "", "requests: the flag to split by"),
	}
	fs.Func("where", "events: property filter KEY:VALUE (repeatable)", func(v string) error {
		f.where = append(f.where, v)
		return nil
	})
	return f
}

// insightsTime parses an RFC 3339 time or a duration ago (36h, 14d).
func insightsTime(raw string, now time.Time) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return &t, nil
	}
	ago := raw
	days := strings.HasSuffix(raw, "d")
	if days {
		ago = strings.TrimSuffix(raw, "d") + "h"
	}
	d, err := time.ParseDuration(ago)
	if err != nil || d <= 0 {
		return nil, fmt.Errorf("%q is not an RFC 3339 time or a duration such as 36h or 14d", raw)
	}
	if days {
		d *= 24
	}
	t := now.Add(-d)
	return &t, nil
}

func cmdInsights(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "gregale "+cliTreeSpellPath("insights")+" "+insightsVerbs, "insights")
		return 1
	}
	verb, args := args[0], args[1:]
	if verb == "cohorts" {
		return cmdInsightCohorts(args)
	}
	fs := newFlagSet("insights-"+verb, flag.ContinueOnError)
	f := newInsightsFlags(fs)
	positionals, err := parseInterleaved(fs, args)
	if err != nil {
		return 1
	}
	now := time.Now().UTC()
	from, err := insightsTime(*f.from, now)
	if err == nil {
		var until *time.Time
		until, err = insightsTime(*f.until, now)
		if err == nil {
			return runInsights(verb, positionals, f, from, until)
		}
	}
	printCommandValidation(os.Stderr, "gregale %s: %v\n", cliTreeSpellPath("insights "+verb), err)
	return 1
}

func runInsights(verb string, positionals []string, f *insightsFlags, from, until *time.Time) int {
	// Literal counts, so the positional guard sees the check before any call.
	if *f.project == "" || (verb == "events" && len(positionals) != 1) || (verb == "funnel" && len(positionals) < 2) ||
		(verb == "retention" && len(positionals) != 2) || (verb == "requests" && (len(positionals) != 0 || *f.flag == "")) {
		usage := map[string]string{"events": "EVENT", "funnel": "STEP STEP [STEP...]", "retention": "START_EVENT RETURN_EVENT", "requests": "--flag KEY"}[verb]
		if usage == "" {
			printCommandValidation(os.Stderr, "gregale insight: unknown subcommand %q (%s)\n", verb, insightsVerbs)
			return 1
		}
		PrintUsage(os.Stderr, fmt.Sprintf("gregale %s %s --project SLUG", cliTreeSpellPath("insights "+verb), usage), "insights")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch verb {
	case "events":
		q := url.Values{"event": {positionals[0]}, "unit": {*f.unit}, "by": {*f.by}, "interval": {*f.interval}, "where": f.where}
		setTimes(q, from, until)
		out, err := client.ProjectInsightEvents(ctx, *f.project, *f.environment, q)
		return insightsOut(out, err, func() { printInsightEvents(out) })
	case "funnel":
		req := api.InsightFunnelRequest{From: from, Until: until, Window: *f.window, Unit: *f.unit, By: *f.by}
		for _, p := range positionals {
			req.Steps = append(req.Steps, api.InsightStepRequest{Event: p})
		}
		out, err := client.ProjectInsightFunnel(ctx, *f.project, *f.environment, req)
		return insightsOut(out, err, func() { printInsightFunnel(out) })
	case "retention":
		req := api.InsightRetentionRequest{Start: api.InsightStepRequest{Event: positionals[0]}, Return: api.InsightStepRequest{Event: positionals[1]},
			From: from, Until: until, Period: *f.period, Periods: *f.periods, Unit: *f.unit, By: *f.by}
		out, err := client.ProjectInsightRetention(ctx, *f.project, *f.environment, req)
		return insightsOut(out, err, func() { printInsightRetention(out) })
	default:
		q := url.Values{"flag": {*f.flag}}
		setTimes(q, from, until)
		out, err := client.ProjectInsightRequests(ctx, *f.project, *f.environment, q)
		return insightsOut(out, err, func() { printInsightRequests(out) })
	}
}

func setTimes(q url.Values, from, until *time.Time) {
	if from != nil {
		q.Set("from", from.UTC().Format(time.RFC3339))
	}
	if until != nil {
		q.Set("until", until.UTC().Format(time.RFC3339))
	}
}

func insightsOut(out any, err error, human func()) int {
	if err != nil {
		return printErr("Analytics query failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	human()
	return 0
}

func printInsightEvents(out api.InsightEventSeries) {
	_, _ = fmt.Fprintf(osStdout, "%s per %s, %s to %s (unit: %s)\n", out.Event, out.Interval, out.From.Format(time.RFC3339), out.Until.Format(time.RFC3339), out.Unit)
	if len(out.Points) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No events in this range.")
		return
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "START\tBREAKDOWN\tEVENTS\tUNITS\tVALUE")
	for _, p := range out.Points {
		b := p.Breakdown
		if b == "" {
			b = "-"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\n", p.Start.Format(time.RFC3339), b, p.Events, p.Units, p.Value)
	}
	_ = tw.Flush()
}

func printFunnelSteps(tw *tabwriter.Writer, label string, steps []api.InsightFunnelStep) {
	for i, s := range steps {
		_, _ = fmt.Fprintf(tw, "%s\t%d. %s\t%d\t%.1f%%\t%.1f%%\n", label, i+1, s.Event, s.Units, 100*s.ConversionFromFirst, 100*s.ConversionFromPrev)
	}
}

func printInsightFunnel(out api.InsightFunnel) {
	_, _ = fmt.Fprintf(osStdout, "Funnel within %s, %s to %s (unit: %s)\n", out.Window, out.From.Format(time.RFC3339), out.Until.Format(time.RFC3339), out.Unit)
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "BREAKDOWN\tSTEP\tUNITS\tFROM FIRST\tFROM PREVIOUS")
	printFunnelSteps(tw, "all", out.Steps)
	for _, b := range out.Breakdowns {
		printFunnelSteps(tw, b.Value, b.Steps)
	}
	_ = tw.Flush()
	if out.Truncated {
		_, _ = fmt.Fprintf(osStdout, "Only the first %d units were read; narrow the range.\n", api.AnalyticsQueryMaxUnits)
	}
}

func retentionCells(r api.InsightRetentionRow, periods int) string {
	cells := make([]string, periods)
	for k := range cells {
		cells[k] = "-"
		if k < len(r.Rates) {
			cells[k] = fmt.Sprintf("%.0f%%", 100*r.Rates[k])
		}
	}
	return strings.Join(cells, "\t")
}

func printInsightRetention(out api.InsightRetention) {
	_, _ = fmt.Fprintf(osStdout, "Retention by %s, %s to %s (unit: %s; - is a period that has not ended)\n", out.Period,
		out.From.Format(time.RFC3339), out.Until.Format(time.RFC3339), out.Unit)
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	header := []string{"COHORT", "UNITS"}
	for k := 1; k <= out.Periods; k++ {
		header = append(header, fmt.Sprintf("%s %d", strings.ToUpper(out.Period), k))
	}
	_, _ = fmt.Fprintln(tw, strings.Join(header, "\t"))
	_, _ = fmt.Fprintf(tw, "all\t%d\t%s\n", out.Overall.Units, retentionCells(out.Overall, out.Periods))
	for _, c := range out.Cohorts {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\n", c.Start.Format("2006-01-02"), c.Units, retentionCells(c, out.Periods))
	}
	for _, b := range out.Breakdowns {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\n", b.Value, b.Units, retentionCells(b, out.Periods))
	}
	_ = tw.Flush()
	if out.Truncated {
		_, _ = fmt.Fprintf(osStdout, "Only the first %d units were read; narrow the range.\n", api.AnalyticsQueryMaxUnits)
	}
}

func printInsightRequests(out api.InsightRequests) {
	_, _ = fmt.Fprintf(osStdout, "Requests by %s, %s to %s\n", out.Flag, out.From.Format(time.RFC3339), out.Until.Format(time.RFC3339))
	if len(out.Variants) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No requests evaluated this flag in the range.")
		return
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "VALUE\tREQUESTS\tSERVER ERRORS\tERROR RATE\tP95")
	for _, v := range out.Variants {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%.2f%%\t%d ms\n", v.Value, v.Requests, v.ServerErrors, 100*v.ServerErrorRate, v.P95MS)
	}
	_ = tw.Flush()
}
