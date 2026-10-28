// gregale crons info — single-cron read surface (issue #791
// PR-E / ADR-090 §"Sub-decision 7").
//
// One subcommand, one HTTP round-trip. The id is validated locally
// against cronIDPattern (commands2.go) BEFORE the network round-trip
// — a bad id returns 1 with zero server calls, same posture as
// cmdCronsRuns / cmdCronsUpdate. The server returns a byte-identical
// 404 on missing or cross-account (handlers_ext.go::getCron), so the
// CLI never invents a local branch that could leak existence.
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

// cmdCronsInfo implements `gregale crons info <id>`. Mirrors the
// shape of cmdCronsUpdate (commands_crons_update.go) and cmdCronsRuns
// (commands_crons_runs.go): flag set, positional id, local id regex
// pre-check, single SDK call, JSON-or-human output branch.
//
// UX shape (human mode):
//
//	$ gregale crons info 0123...cdef
//	cron 0123...cdef
//	  schedule: */5 * * * *
//	  path:     /cleanup
//	  enabled:  true
//	  app:      4567...89ab
//	  last:     2026-08-10T09:00:00Z
//
//	$ gregale crons info 0123...cdef --json
//	{"id":"0123...cdef","app_id":"4567...89ab","schedule":"*/5 * * * *","path":"/cleanup","enabled":true,"last_fired_at":"2026-08-10T09:00:00Z","created_at":"2026-08-01T12:00:00Z"}
func cmdCronsInfo(args []string) int {
	fs := newFlagSet("crons-info", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		printCommandValidation(os.Stderr, "usage: gregale cron get <id>\n")
		return 1
	}
	id := fs.Arg(0)
	if !cronIDPattern.MatchString(id) {
		printCommandValidation(os.Stderr, "usage: gregale cron get <id>\n")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetCron(context.Background(), id)
	if err != nil {
		return printErr("Could not load cron", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderCronInfo(osStdout, resp)
	// The cron row says only "enabled"; a schedule failing every run for
	// days looked healthy here (H10-70). A runs read failure leaves the
	// block out rather than failing the command.
	if runs, runsErr := client.ListCronRuns(context.Background(), id, "", cronInfoRecentRuns); runsErr == nil {
		renderCronRecentRuns(osStdout, id, runs.Runs)
	}
	return 0
}

// cronInfoRecentRuns is how many runs crons info summarizes.
const cronInfoRecentRuns = 10

// renderCronRecentRuns adds the newest run's outcome and, when any of the
// recent runs did not succeed, how many, with the command that lists them.
func renderCronRecentRuns(w io.Writer, id string, runs []api.CronRun) {
	if len(runs) == 0 {
		return
	}
	newest := runs[0]
	line := fmt.Sprintf("%s at %s", newest.Outcome, newest.StartedAt.UTC().Format(time.RFC3339))
	if newest.Error != "" {
		line += " — " + truncateReason(newest.Error, 120)
	}
	_, _ = fmt.Fprintf(w, "  last run: %s\n", line)
	unsuccessful := 0
	for _, run := range runs {
		if run.Outcome != api.CronRunSuccess && run.Outcome != api.CronRunRunning {
			unsuccessful++
		}
	}
	if unsuccessful > 0 {
		_, _ = fmt.Fprintf(w, "  recent:   %d of the last %d runs did not succeed; see `gregale cron runs %s`\n", unsuccessful, len(runs), id)
	}
}

// renderCronInfo writes one human-mode block for a single cron.
// Column order mirrors `crons list` (schedule / state / path) plus
// the identity fields (id, app) the customer needs to recognise the
// rule they're looking at. last_fired_at renders as "—" when the
// cron has never fired (zero value).
func renderCronInfo(w io.Writer, c api.CronResponse) {
	_, _ = fmt.Fprintf(w, "cron %s\n", c.ID)
	_, _ = fmt.Fprintf(w, "  schedule: %s\n", c.Schedule)
	if c.Kind == "command" {
		_, _ = fmt.Fprintf(w, "  command:  %s\n", formatCronCommand(c))
	} else {
		_, _ = fmt.Fprintf(w, "  path:     %s\n", c.Path)
	}
	_, _ = fmt.Fprintf(w, "  enabled:  %t\n", c.Enabled)
	if c.SuspendedReason != "" {
		_, _ = fmt.Fprintf(w, "  suspended: %s (deploy the app to reactivate this schedule)\n", c.SuspendedReason)
	}
	_, _ = fmt.Fprintf(w, "  timezone: %s\n", c.Timezone)
	_, _ = fmt.Fprintf(w, "  skip_if_running: %t\n", c.SkipIfRunning)
	if c.Kind == "command" {
		_, _ = fmt.Fprintf(w, "  retries: %d (base delay %ds)\n", c.RetryMax, c.RetryBackoffSeconds)
	}
	_, _ = fmt.Fprintf(w, "  app:      %s\n", c.AppID)
	last := c.LastFiredAt
	if last == "" {
		last = "—"
	}
	// A manual run does not move last_fired_at, so a bare "last: —" sat
	// above "last run: success" on production-us rc.294 (Hunt 14 H14-64).
	_, _ = fmt.Fprintf(w, "  last scheduled fire: %s\n", last)
}
