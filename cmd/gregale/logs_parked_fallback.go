package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/loglevel"
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
// app, with a header naming what is shown. --grep and --level apply to the
// retained lines as they do to the live stream.
func runParkedAppLogs(ctx context.Context, client *Client, slug, deployment string, filter api.LogFilter, explain bool) int {
	var since time.Time
	if filter.Since != "" {
		since, _ = time.Parse(time.RFC3339Nano, filter.Since)
	}
	instances, err := client.ListInstancesWithHistory(ctx, slug, true)
	if err != nil {
		if code, handled := noLiveDeploymentLogs(ctx, client, slug); handled {
			return code
		}
		return parkedLogsUnavailable(appLogsDegradedMessage(`{"code":"not_found"}`))
	}
	instance, selector, ok := parkedLogsSelection(instances, since)
	if !ok {
		if code, handled := noLiveDeploymentLogs(ctx, client, slug); handled {
			return code
		}
		return parkedLogsUnavailable(fmt.Sprintf("%s has no running instance and no instance with activity in the requested window; wake it with `gregale app wake %s`.", slug, slug))
	}
	header := fmt.Sprintf("%s is parked; showing the retained logs of its most recent instance %s (started %s, UTC day %s).", slug, instance.ID, instance.StartedAt, selector.Date)
	header += parkedArchivePendingNote(instance, time.Now())
	_, _ = fmt.Fprintln(osStderr, header)
	return runLogsExplainingMissingArchive(ctx, slug, deployment, api.LogFilter{Grep: filter.Grep, Level: filter.Level}, &selector, false, explain,
		parkedNoOutputMessage(instance))
}

// noLiveDeploymentLogs explains missing runtime logs for an app with no live
// deployment. Waking cannot help such an app, yet logs told the customer to
// wake one whose first release was still building (CLI journey 2026-11-05,
// #6627). It names the deployment to follow instead: an in-flight one
// (503, exit 3: retry once it serves), else the latest failed one or a first
// deploy (409, exit 5). handled is false when the app has a live deployment
// or its state cannot be read, so the parked-app reply stands.
func noLiveDeploymentLogs(ctx context.Context, client *Client, slug string) (code int, handled bool) {
	app, err := client.GetApp(ctx, slug)
	if err != nil || app.DeploymentAvailability != api.AppDeploymentAvailabilityMissing {
		return 0, false
	}
	page, err := client.ListAppDeployments(ctx, slug, "", 1)
	if err != nil {
		return 0, false
	}
	status, message := http.StatusConflict, fmt.Sprintf("%s has no deployment yet, so it has no runtime logs. Deploy it with `gregale deploy`.", slug)
	if len(page.Items) > 0 {
		dep := page.Items[0]
		switch {
		case dep.Status == statusLive:
			message = fmt.Sprintf("%s has no deployment receiving traffic, so it has no runtime logs. Deployment %s is live at 0%%: check it with `gregale inspect %s`.", slug, dep.ID, slug)
		case isTerminalDeploymentStatus(dep.Status):
			message = fmt.Sprintf("%s has no live deployment, so it has no runtime logs; waking it cannot help. Its latest deployment %s is %s: read why with `gregale logs %s --deployment %s`.", slug, dep.ID, dep.Status, slug, dep.ID)
		default:
			status = http.StatusServiceUnavailable
			message = fmt.Sprintf("%s has no live deployment yet: deployment %s is %s. Runtime logs start once it serves. Follow its build with `gregale logs %s --deployment %s --follow`, or wait for it with `gregale release wait %s`.", slug, dep.ID, dep.Status, slug, dep.ID, dep.ID)
		}
	}
	if jsonOutput {
		_ = writeJSONProblem(*api.NewProblem(status, api.CodeNoLiveDeployment, "No live deployment", message).WithDocs(cliDocsURL))
	} else {
		_, _ = fmt.Fprintln(osStderr, message)
	}
	return exitCodeForStatus(status), true
}

// parkedLogsUnavailable reports why no retained logs can be shown: under
// --json as the RFC 7807 problem the live stream's degraded frame becomes,
// otherwise as text.
func parkedLogsUnavailable(message string) int {
	if jsonOutput {
		_ = writeJSONProblem(*api.NewProblem(http.StatusServiceUnavailable, "app_logs_unavailable", "App logs unavailable", message).WithDocs(cliDocsURL))
		return 3
	}
	_, _ = fmt.Fprintln(osStderr, message)
	return 3
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

// retainedLineLevelMatches applies --level to an archived log frame the way
// the live stream does (scheddgrpc.LevelMatcher): the level parsed at ring
// intake when the frame carries one, else the shared line heuristic, at or
// above the requested floor. A parked app is the usual state of a
// scale-to-zero app, and its logs used to ignore --level entirely
// (ADR-20261110090000).
func retainedLineLevelMatches(data, level string) bool {
	floor := loglevel.Rank(strings.ToLower(level))
	if level == "" || floor < 0 {
		return true
	}
	var event api.LogEvent
	if json.Unmarshal([]byte(data), &event) != nil {
		return false
	}
	detected := event.Level
	if detected == "" {
		detected = loglevel.Detect(event.Line)
	}
	return detected != "" && loglevel.Rank(detected) >= floor
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
