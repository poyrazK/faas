package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// isNoRunningInstanceLogs reports the degraded frame the runtime log stream
// sends when the app has no running instance.
func isNoRunningInstanceLogs(data string) bool {
	return appLogsDegradedMessage(data) != "Log stream degraded: the scheduler is temporarily unavailable"
}

// parkedLogsSelection picks the archive that holds a parked app's most recent
// output: its newest instance and the UTC day that instance started. A
// scale-to-zero app is parked most of the time, so `gregale logs <slug>` (the
// documented `--since 1h --level error` example included) used to answer only
// "No running instance … read a parked instance's retained logs with --archive
// --instance <id> --date <day>" (H10-72). ok is false when no instance is
// known or the newest one had no activity since `since`.
func parkedLogsSelection(instances []api.InstanceResponse, since time.Time) (api.InstanceResponse, api.ArchiveLogSelector, bool) {
	var newest api.InstanceResponse
	var newestStart time.Time
	for _, ins := range instances {
		started, err := time.Parse(time.RFC3339, ins.StartedAt)
		if err != nil {
			continue
		}
		if newestStart.IsZero() || started.After(newestStart) {
			newest, newestStart = ins, started
		}
	}
	if newestStart.IsZero() {
		return api.InstanceResponse{}, api.ArchiveLogSelector{}, false
	}
	if !since.IsZero() {
		lastActive := newestStart
		for _, raw := range []string{newest.LastRequestAt, newest.ParkedAt} {
			if at, err := time.Parse(time.RFC3339, raw); err == nil && at.After(lastActive) {
				lastActive = at
			}
		}
		if lastActive.Before(since) {
			return api.InstanceResponse{}, api.ArchiveLogSelector{}, false
		}
	}
	return newest, api.ArchiveLogSelector{InstanceID: newest.ID, Date: newestStart.UTC().Format("2006-01-02")}, true
}

// runParkedAppLogs prints the newest instance's retained logs for a parked
// app, with a header naming what is shown. --grep applies to the retained
// lines here; --level does not, and the header says so.
func runParkedAppLogs(ctx context.Context, client *Client, slug, deployment string, filter api.LogFilter, explain bool) int {
	var since time.Time
	if filter.Since != "" {
		since, _ = time.Parse(time.RFC3339Nano, filter.Since)
	}
	instances, err := client.ListInstancesWithHistory(ctx, slug, true)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, appLogsDegradedMessage(`{"code":"not_found"}`))
		return 3
	}
	instance, selector, ok := parkedLogsSelection(instances, since)
	if !ok {
		_, _ = fmt.Fprintf(osStderr, "%s has no running instance and no instance with activity in the requested window; wake it with `gregale app wake %s`.\n", slug, slug)
		return 3
	}
	header := fmt.Sprintf("%s is parked; showing the retained logs of its most recent instance %s (started %s, UTC day %s).", slug, instance.ID, instance.StartedAt, selector.Date)
	header += parkedArchivePendingNote(instance, time.Now())
	if filter.Level != "" {
		header += " --level is not applied to retained logs."
	}
	_, _ = fmt.Fprintln(osStderr, header)
	return runLogsExplainingMissingArchive(ctx, slug, deployment, api.LogFilter{Grep: filter.Grep}, &selector, false, explain,
		parkedNoOutputMessage(instance))
}

// parkedNoOutputMessage explains a parked instance with no retained logs. It
// usually printed nothing: an instance restored from a snapshot does not
// repeat the startup output printed before the snapshot was taken, so a
// scale-to-zero app's newest instance often has none. The generic reply
// blamed an archive gap and retention (Hunt 14 H14-62). The header already
// carries the upload-delay note for an instance that parked minutes ago.
func parkedNoOutputMessage(ins api.InstanceResponse) string {
	return fmt.Sprintf("No retained output from instance %s: it printed nothing while it ran. An instance restored from a snapshot does not repeat the startup output printed before the snapshot was taken.", ins.ID)
}

// retainedLineMatches applies --grep to an archived line the way the live
// stream does: a case-insensitive literal substring.
func retainedLineMatches(line, grep string) bool {
	return grep == "" || strings.Contains(strings.ToLower(line), strings.ToLower(grep))
}

// parkedArchiveUploadWindow covers vmmd's archive flush interval
// (logarchive.DefaultFlushInterval, 5 minutes) with margin.
const parkedArchiveUploadWindow = 10 * time.Minute

// parkedArchivePendingNote explains a missing archive right after a park. vmmd
// ships a parked instance's logs on its next flush, so for a few minutes the
// archive does not exist yet and the reply read "archive gap … check archive
// retention" (production-us hunt #10, H10-122).
func parkedArchivePendingNote(ins api.InstanceResponse, now time.Time) string {
	stopped, err := time.Parse(time.RFC3339, ins.ParkedAt)
	if err != nil {
		stopped, err = time.Parse(time.RFC3339, ins.LastRequestAt)
	}
	if err != nil || stopped.After(now) || now.Sub(stopped) > parkedArchiveUploadWindow {
		return ""
	}
	return fmt.Sprintf(" It stopped at %s; retained logs reach the archive up to 5 minutes after a park, so if none appear, retry shortly.", stopped.UTC().Format("15:04:05 UTC"))
}
