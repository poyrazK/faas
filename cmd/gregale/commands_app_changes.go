package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const subChanges = "changes"

// cmdAppChanges implements `gregale app <slug> changes [--since] [--until]`
// (ADR-741): the app's change timeline, newest first.
func cmdAppChanges(slug string, args []string) int {
	fs := newFlagSet("app changes", flag.ContinueOnError)
	sinceRaw := fs.String("since", "", "Window start, RFC3339 (default: 24h before until)")
	untilRaw := fs.String("until", "", "Window end, RFC3339 (default: now)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	since, err := parseOptionalRFC3339(*sinceRaw)
	if err != nil {
		return printErr("Invalid --since", err)
	}
	until, err := parseOptionalRFC3339(*untilRaw)
	if err != nil {
		return printErr("Invalid --until", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.GetAppChangeTimeline(context.Background(), slug, since, until)
	if err != nil {
		return printErr("Change timeline failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	renderAppChanges(osStdout, out)
	return 0
}

func parseOptionalRFC3339(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("want RFC3339, e.g. 2026-10-08T12:00:00Z: %w", err)
	}
	return t, nil
}

func renderAppChanges(w io.Writer, out api.AppChangeTimelineResponse) {
	_, _ = fmt.Fprintf(w, "%s: changes from %s to %s\n", out.AppSlug,
		out.Since.Format(time.RFC3339), out.Until.Format(time.RFC3339))
	for _, source := range out.UnavailableSources {
		_, _ = fmt.Fprintf(w, "Warning: %s changes could not be read; they may be missing below.\n", source)
	}
	if len(out.Events) == 0 {
		_, _ = fmt.Fprintln(w, "(no recorded changes in this window)")
		return
	}
	for _, e := range out.Events {
		_, _ = fmt.Fprintf(w, "%s  %-14s %s\n", e.At.Format("2006-01-02 15:04:05Z07:00"), e.Source, e.Summary)
	}
	if out.Truncated {
		_, _ = fmt.Fprintf(w, "(showing the newest %d changes; narrow --since/--until to see older ones)\n", len(out.Events))
	}
}
