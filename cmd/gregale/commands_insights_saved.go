package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale insight saved ...` (ADR-20261122190000): saved insights that
// anyone who can read the project runs and exports, with optional digests
// and threshold alerts sent to notification channels.

const insightSavedVerbs = "list|save|get|run|delete"

type savedInsightFlags struct {
	project, environment, name, kind, event, measure, interval, unit, by *string
	window, startEvent, returnEvent, period, digest, alert, format       *string
	days, periods                                                        *int
	replace                                                              *bool
	steps, where, channels                                               repeatedFlag
}

func newSavedInsightFlags(fs *flag.FlagSet) *savedInsightFlags {
	f := &savedInsightFlags{
		project:     fs.String("project", "", "project slug"),
		environment: fs.String("environment", "production", "named project environment"),
		name:        fs.String("name", "", "save: display name (default the key)"),
		kind:        fs.String("kind", "", "save: events, funnel or retention"),
		event:       fs.String("event", "", "save: events: the event"),
		measure:     fs.String("measure", "", "save: events: events (default) or value"),
		interval:    fs.String("interval", "", "save: events: day (default) or hour"),
		unit:        fs.String("unit", "", "save: customer (default) or subject"),
		by:          fs.String("by", "", "save: breakdown flag:KEY or property:KEY"),
		window:      fs.String("window", "", "save: funnel: conversion window, 1h to 720h"),
		startEvent:  fs.String("start-event", "", "save: retention: the start event"),
		returnEvent: fs.String("return-event", "", "save: retention: the return event"),
		period:      fs.String("period", "", "save: retention: day (default) or week"),
		digest:      fs.String("digest", "", "save: send a daily or weekly digest"),
		alert:       fs.String("alert", "", "save: alert when the value crosses OP:THRESHOLD, OP one of gt, gte, lt, lte"),
		format:      fs.String("format", "", "run: table (default), json or csv"),
		days:        fs.Int("days", 0, "save: cover the last N days (default 7, 28 for retention)"),
		periods:     fs.Int("periods", 0, "save: retention: periods after the cohort, 1 to 12"),
		replace:     fs.Bool("replace", false, "save: replace the saved insight with this key"),
	}
	fs.Var(&f.steps, "step", "save: funnel step event (repeatable, 2 to 6)")
	fs.Var(&f.where, "where", "save: events: property filter KEY:VALUE (repeatable)")
	fs.Var(&f.channels, "channel", "save: notification channel ID for digests and alerts (repeatable)")
	return f
}

func cmdInsightSaved(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "gregale "+cliTreeSpellPath("insights saved")+" "+insightSavedVerbs, "insights")
		return 1
	}
	verb, args := args[0], args[1:]
	if verb == "create" {
		verb = "save"
	}
	fs := newFlagSet("insights-saved-"+verb, flag.ContinueOnError)
	f := newSavedInsightFlags(fs)
	positionals, err := parseInterleaved(fs, args)
	if err != nil {
		return 1
	}
	want := 1
	if verb == "list" {
		want = 0
	}
	// Literal counts, so the positional guard sees the check before any call.
	if *f.project == "" || (want == 0 && len(positionals) != 0) || (want == 1 && len(positionals) != 1) {
		needs := map[bool]string{true: "", false: " and a saved insight KEY"}[want == 0]
		PrintUsage(os.Stderr, fmt.Sprintf("gregale %s needs --project%s", cliTreeSpellPath("insights saved "+verb), needs), "insights")
		return 1
	}
	var req api.SavedInsightRequest
	if verb == "save" {
		if req, err = savedInsightRequest(positionals[0], f); err != nil {
			printCommandValidation(os.Stderr, "gregale %s: %v\n", cliTreeSpellPath("insights saved save"), err)
			return 1
		}
	}
	switch *f.format {
	case "", "table", "json", "csv":
	default:
		printCommandValidation(os.Stderr, "gregale %s: --format is table, json or csv\n", cliTreeSpellPath("insights saved "+verb))
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	return runInsightSaved(context.Background(), client, verb, positionals, f, req)
}

// savedInsightRequest builds a save request from the flags.
func savedInsightRequest(key string, f *savedInsightFlags) (api.SavedInsightRequest, error) {
	req := api.SavedInsightRequest{Key: key, Name: *f.name, Kind: *f.kind, RangeDays: *f.days, Digest: *f.digest, ChannelIDs: f.channels}
	switch *f.kind {
	case api.SavedInsightKindEvents:
		where, err := parseCohortWhere(f.where)
		if err != nil {
			return req, err
		}
		req.Events = &api.InsightEventsQuery{Event: *f.event, Where: where, Interval: *f.interval, Unit: *f.unit, By: *f.by, Measure: *f.measure}
	case api.SavedInsightKindFunnel:
		req.Funnel = &api.InsightFunnelRequest{Window: *f.window, Unit: *f.unit, By: *f.by}
		for _, s := range f.steps {
			req.Funnel.Steps = append(req.Funnel.Steps, api.InsightStepRequest{Event: s})
		}
	case api.SavedInsightKindRetention:
		req.Retention = &api.InsightRetentionRequest{Start: api.InsightStepRequest{Event: *f.startEvent},
			Return: api.InsightStepRequest{Event: *f.returnEvent}, Period: *f.period, Periods: *f.periods, Unit: *f.unit, By: *f.by}
	default:
		return req, fmt.Errorf("--kind is events, funnel or retention")
	}
	if *f.alert != "" {
		op, raw, ok := strings.Cut(*f.alert, ":")
		threshold, err := strconv.ParseFloat(raw, 64)
		if !ok || err != nil || (op != "gt" && op != "gte" && op != "lt" && op != "lte") {
			return req, fmt.Errorf("--alert %q: use OP:THRESHOLD, such as lt:0.2 or gt:1000", *f.alert)
		}
		req.Alert = &api.SavedInsightAlert{Comparison: op, Threshold: threshold}
	}
	return req, nil
}

func runInsightSaved(ctx context.Context, client *Client, verb string, positionals []string, f *savedInsightFlags, req api.SavedInsightRequest) int {
	project, environment := *f.project, *f.environment
	var (
		si  api.SavedInsight
		err error
	)
	switch verb {
	case "list":
		out, err := client.ListProjectSavedInsights(ctx, project, environment)
		return insightsOut(out, err, func() { printSavedInsights(out.Insights, project) })
	case "save":
		si, err = client.SaveProjectInsight(ctx, project, environment, req, *f.replace)
	case "get":
		si, err = client.GetProjectSavedInsight(ctx, project, environment, positionals[0])
	case "run":
		res, err := client.RunProjectSavedInsight(ctx, project, environment, positionals[0])
		if err != nil {
			return printErr("Analytics query failed", err)
		}
		switch {
		case *f.format == "csv":
			if err := api.WriteSavedInsightCSV(osStdout, res); err != nil {
				return printErr("Write CSV", err)
			}
			return 0
		case jsonOutput || *f.format == "json":
			return jsonOut(writeJSON(res))
		}
		printSavedInsightResult(res)
		return 0
	case "delete":
		if err := client.DeleteProjectSavedInsight(ctx, project, environment, positionals[0]); err != nil {
			return printErr("Saved insight operation failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"deleted": positionals[0]}))
		}
		_, _ = fmt.Fprintf(osStdout, "Deleted saved insight %s; its digests and alert stop\n", positionals[0])
		return 0
	default:
		printCommandValidation(os.Stderr, "gregale insight saved: unknown subcommand %q (%s)\n", verb, insightSavedVerbs)
		return 1
	}
	if err != nil {
		return printErr("Saved insight operation failed", err)
	}
	return insightsOut(si, nil, func() { printSavedInsight(si, project, environment, verb) })
}

// savedInsightValue renders a value the way digests do.
func savedInsightValue(kind string, v float64) string {
	if kind == api.SavedInsightKindFunnel || kind == api.SavedInsightKindRetention {
		return strconv.FormatFloat(v*100, 'f', 1, 64) + "%"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func savedInsightValueOf(kind string, v *float64) string {
	if v == nil {
		return "no data"
	}
	return savedInsightValue(kind, *v)
}

func savedInsightSchedule(si api.SavedInsight) string {
	var parts []string
	if si.Digest != "" {
		parts = append(parts, si.Digest+" digest")
	}
	if si.Alert != nil {
		parts = append(parts, fmt.Sprintf("alert %s %s (%s)", si.Alert.Comparison, savedInsightValue(si.Kind, si.Alert.Threshold), si.AlertState))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func printSavedInsights(list []api.SavedInsight, project string) {
	if len(list) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No saved insights. Save one with gregale %s KEY --kind events --event EVENT --project %s\n", cliTreeSpellPath("insights saved save"), project)
		return
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KEY\tKIND\tDAYS\tLAST VALUE\tSCHEDULE\tNAME")
	for _, si := range list {
		last := "-"
		if si.LastValue != nil {
			last = savedInsightValue(si.Kind, *si.LastValue)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\n", si.Key, si.Kind, si.RangeDays, last, savedInsightSchedule(si), si.Name)
	}
	_ = tw.Flush()
}

func printSavedInsight(si api.SavedInsight, project, environment, verb string) {
	_, _ = fmt.Fprintf(osStdout, "Saved insight %s (%s): %s over the last %d days\n", si.Key, si.Name, si.Kind, si.RangeDays)
	_, _ = fmt.Fprintf(osStdout, "Schedule: %s\n", savedInsightSchedule(si))
	if si.LastRunAt != nil {
		last := "-"
		if si.LastValue != nil {
			last = savedInsightValue(si.Kind, *si.LastValue)
		}
		_, _ = fmt.Fprintf(osStdout, "Last scheduled run: %s, value %s\n", si.LastRunAt.UTC().Format(time.RFC3339), last)
	}
	if si.LastError != "" {
		_, _ = fmt.Fprintf(osStdout, "Last error: %s\n", si.LastError)
	}
	if verb == "save" {
		_, _ = fmt.Fprintf(osStdout, "Run it with gregale %s %s --project %s --environment %s (add --format csv to export)\n",
			cliTreeSpellPath("insights saved run"), si.Key, project, environment)
	}
}

func printSavedInsightResult(res api.SavedInsightResult) {
	_, _ = fmt.Fprintf(osStdout, "%s (%s), %s to %s: value %s\n", res.Key, res.Kind, res.From.Format(time.RFC3339), res.Until.Format(time.RFC3339),
		savedInsightValueOf(res.Kind, res.Value))
	switch {
	case res.Events != nil:
		printInsightEvents(*res.Events)
	case res.Funnel != nil:
		printInsightFunnel(*res.Funnel)
	case res.Retention != nil:
		printInsightRetention(*res.Retention)
	}
}
