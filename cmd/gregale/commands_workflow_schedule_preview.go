package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdWorkflowSchedulePreview(args []string) int {
	flags := newFlagSet("workflows-schedules-preview", flag.ContinueOnError)
	app := flags.String("app", "", "app slug")
	workflow := flags.String("workflow", "", "workflow name")
	at := flags.String("at", "", "hypothetical evaluator time (RFC3339)")
	since := flags.String("since", "", "simulated previous evaluation time (RFC3339)")
	count := flags.Int("count", 0, "number of upcoming fires (1..20, default 5)")
	if flags.Parse(args) != nil || rejectUnexpectedFlagArgs(flags) {
		return 1
	}
	if *app == "" || *workflow == "" {
		PrintUsage(os.Stderr, "usage: gregale workflows schedules preview --app <slug> --workflow <name> [--at <RFC3339>] [--since <RFC3339>] [--count <1..20>]", "workflows")
		return 1
	}
	options := api.WorkflowSchedulePreviewOptions{Count: *count}
	if *at != "" {
		value, err := time.Parse(time.RFC3339Nano, *at)
		if err != nil {
			printCommandValidation(os.Stderr, "error: --at must be an RFC3339 timestamp\n")
			return 1
		}
		options.At = value
	}
	if *since != "" {
		value, err := time.Parse(time.RFC3339Nano, *since)
		if err != nil {
			printCommandValidation(os.Stderr, "error: --since must be an RFC3339 timestamp\n")
			return 1
		}
		options.Since = &value
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	preview, err := client.GetWorkflowSchedulePreview(context.Background(), *app, *workflow, options)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(preview))
	}
	renderWorkflowSchedulePreview(osStdout, preview)
	return 0
}

func renderWorkflowSchedulePreview(w io.Writer, preview api.WorkflowSchedulePreviewResponse) {
	_, _ = fmt.Fprintf(w, "Workflow:      %s\nSchedule:      %s\nTimezone:      %s\nOverlap:       %s\nEnabled:       %t\nEvaluated at:  %s\nObserved at:   %s\n",
		preview.WorkflowName, preview.Schedule, preview.Timezone, preview.Overlap, preview.Enabled,
		preview.EvaluationAt.Format(time.RFC3339), preview.ObservedAt.Format(time.RFC3339))
	_, _ = fmt.Fprintf(w, "DST:           %s; spring gap %s; fall fold %s\n\n", preview.DSTBehavior.Mode, preview.DSTBehavior.SpringGap, preview.DSTBehavior.FallFold)
	_, _ = fmt.Fprintln(w, "UPCOMING FIRE TIMES")
	if len(preview.Upcoming) == 0 {
		_, _ = fmt.Fprintln(w, "No occurrences in the cron evaluator horizon.")
	}
	for index, fire := range preview.Upcoming {
		_, _ = fmt.Fprintf(w, "%2d. %s  (UTC %s)", index+1, fire.LocalTime, fire.ScheduledFor.Format(time.RFC3339))
		if fire.DSTAdjustment != "" {
			_, _ = fmt.Fprintf(w, "  [%s]", fire.DSTAdjustment)
		}
		_, _ = fmt.Fprintln(w)
	}
	catchUp := preview.CatchUp
	_, _ = fmt.Fprintf(w, "\nCATCH-UP\nPolicy:        %s", catchUp.Policy)
	if catchUp.Window != "" {
		_, _ = fmt.Fprintf(w, " (%s window)", catchUp.Window)
	}
	_, _ = fmt.Fprintf(w, "\nOutcome:       %s\n", catchUp.Outcome)
	if catchUp.Since != nil {
		_, _ = fmt.Fprintf(w, "Since:         %s\n", catchUp.Since.Format(time.RFC3339))
	}
	if catchUp.EligibleOccurrences > 0 {
		_, _ = fmt.Fprintf(w, "Eligible fires: %d\n", catchUp.EligibleOccurrences)
	}
	if catchUp.CoalescedOccurrences > 0 {
		_, _ = fmt.Fprintf(w, "Coalesced:      %d older fires\n", catchUp.CoalescedOccurrences)
	}
	if catchUp.MissedOccurrencesNotRecovered {
		_, _ = fmt.Fprintln(w, "Earlier missed fires are skipped by this policy.")
	}
	if catchUp.MissedOutsideWindow {
		_, _ = fmt.Fprintln(w, "At least one missed fire falls outside the catch-up window.")
	}
	if catchUp.Selected != nil {
		_, _ = fmt.Fprintf(w, "Selected fire:  %s (UTC %s)\n", catchUp.Selected.LocalTime, catchUp.Selected.ScheduledFor.Format(time.RFC3339))
	}
	_, _ = fmt.Fprintln(w, "\nPreview only: no run is created and no capacity is reserved.")
}
