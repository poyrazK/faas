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
	fs := flag.NewFlagSet("crons-info", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: gregale crons info <id>")
		return 1
	}
	id := fs.Arg(0)
	if !cronIDPattern.MatchString(id) {
		fmt.Fprintln(os.Stderr, "usage: gregale crons info <id>")
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
	return 0
}

// renderCronInfo writes one human-mode block for a single cron.
// Column order mirrors `crons list` (schedule / state / path) plus
// the identity fields (id, app) the customer needs to recognise the
// rule they're looking at. last_fired_at renders as "—" when the
// cron has never fired (zero value).
func renderCronInfo(w io.Writer, c api.CronResponse) {
	_, _ = fmt.Fprintf(w, "cron %s\n", c.ID)
	_, _ = fmt.Fprintf(w, "  schedule: %s\n", c.Schedule)
	_, _ = fmt.Fprintf(w, "  path:     %s\n", c.Path)
	_, _ = fmt.Fprintf(w, "  enabled:  %t\n", c.Enabled)
	_, _ = fmt.Fprintf(w, "  timezone: %s\n", c.Timezone)
	_, _ = fmt.Fprintf(w, "  skip_if_running: %t\n", c.SkipIfRunning)
	_, _ = fmt.Fprintf(w, "  app:      %s\n", c.AppID)
	last := c.LastFiredAt
	if last == "" {
		last = "—"
	}
	_, _ = fmt.Fprintf(w, "  last:     %s\n", last)
}
