package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func cmdAutomationsReport(args []string) int {
	fs := newFlagSet("automations report", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	workflow := fs.String("workflow", "", "workflow name filter")
	version := fs.Int64("policy-version", 0, "policy version filter")
	since := fs.String("since", "7d", "lookback duration (e.g. 7d or 24h), at most 30 days")
	start := fs.String("created-after", "", "inclusive RFC3339 start; cannot combine with --since")
	end := fs.String("created-before", "", "inclusive RFC3339 end")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *app == "" {
		return printErr("Invalid report scope", errors.New("provide --app"))
	}
	o := api.WorkflowRecoveryReportOptions{Workflow: *workflow, PolicyVersion: *version}
	var err error
	if *end != "" {
		o.CreatedBefore, err = time.Parse(time.RFC3339Nano, *end)
		if err != nil {
			return printErr("Invalid report end", err)
		}
	}
	explicitSince := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "since" {
			explicitSince = true
		}
	})
	if *start != "" {
		if explicitSince {
			return printErr("Invalid report window", errors.New("choose --since or --created-after"))
		}
		o.CreatedAfter, err = time.Parse(time.RFC3339Nano, *start)
		if err != nil {
			return printErr("Invalid report start", err)
		}
	} else {
		d, err := recoveryReportDuration(*since)
		if err != nil {
			return printErr("Invalid report lookback", err)
		}
		at := o.CreatedBefore
		if at.IsZero() {
			at = time.Now().UTC()
			o.CreatedBefore = at
		}
		o.CreatedAfter = at.Add(-d)
	}
	if err = o.Validate(); err != nil {
		return printErr("Invalid report filters", err)
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	out, err := c.GetWorkflowRecoveryReport(ctx, *app, o)
	if err != nil {
		return printErr("Could not inspect automation report", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Recovery automation report | %s to %s\n", out.CreatedAfter.Format(time.RFC3339), out.CreatedBefore.Format(time.RFC3339))
	printRecoveryReportTotals("All workflows", out.Totals)
	for _, g := range out.Groups {
		printRecoveryReportTotals(fmt.Sprintf("%s | policy %d", oneLine(g.Workflow), g.PolicyVersion), g.Totals)
	}
	for _, b := range out.Blockers {
		_, _ = fmt.Fprintf(osStdout, "Blocker %s | %d distinct actions\n", oneLine(b.Reason), b.Actions)
		for _, e := range b.Evidence {
			printRecoveryReportEvidence(*app, e)
		}
	}
	for _, e := range out.Evidence {
		printRecoveryReportEvidence(*app, e)
	}
	_, _ = fmt.Fprintf(osStdout, "Evidence: %d entries (%d durable), truncated: %t\n", out.ScannedEntries, out.DurableEntries, out.Truncated)
	for _, note := range out.CoverageNotes {
		_, _ = fmt.Fprintln(osStdout, oneLine(note))
	}
	return 0
}
func recoveryReportDuration(value string) (time.Duration, error) {
	var d time.Duration
	var err error
	if strings.HasSuffix(value, "d") {
		var days int
		days, err = strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err == nil && days > 0 && days <= int(api.WorkflowAutomationHealthMaxRange/(24*time.Hour)) {
			d = time.Duration(days) * 24 * time.Hour
		} else {
			return 0, errors.New("days must be between 1 and 30")
		}
	} else {
		d, err = time.ParseDuration(value)
	}
	if err != nil {
		return 0, err
	}
	if d <= 0 || d > api.WorkflowAutomationHealthMaxRange {
		return 0, errors.New("lookback must be positive and at most 30 days")
	}
	return d, nil
}
func printRecoveryReportTotals(label string, t api.WorkflowRecoveryReportTotals) {
	_, _ = fmt.Fprintf(osStdout, "%s | attempts %d | succeeded %d | failed %d | cancelled %d | other results %d | without observed result %d | pauses %d\n", label, t.Attempts, t.Succeeded, t.Failed, t.Cancelled, t.OtherResults, t.WithoutObservedResult, t.Pauses)
	if t.MeanRetryResultSeconds != nil {
		_, _ = fmt.Fprintf(osStdout, "Mean retry-to-recorded-result: %.3fs (%d matched results)\n", *t.MeanRetryResultSeconds, t.TimedResults)
	}
}
func printRecoveryReportEvidence(app string, e api.WorkflowRecoveryHistoryEntry) {
	link := "/v1/apps/" + url.PathEscape(app) + "/recovery-policy/history"
	q := url.Values{}
	if e.RunID != "" {
		q.Set("run_id", e.RunID)
	}
	if e.PolicyVersion > 0 {
		q.Set("policy_version", strconv.FormatInt(e.PolicyVersion, 10))
	}
	q.Set("created_after", e.OccurredAt.Format(time.RFC3339Nano))
	q.Set("created_before", e.OccurredAt.Format(time.RFC3339Nano))
	_, _ = fmt.Fprintf(osStdout, "Evidence %s | run %s | review %s | child %s | %s?%s\n", oneLine(e.ID), oneLine(e.RunID), oneLine(e.ReviewID), oneLine(e.ChildJobID), link, q.Encode())
}
