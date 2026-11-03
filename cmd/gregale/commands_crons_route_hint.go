package main

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cronRouteMissingStatus reports the status of a run the app answered with
// 404 or 405, or "": it has no handler for the cron's POST to its path. Most
// failed cron runs in production are this misconfiguration, not a delivery
// failure; a handler that serves only GET answers 405.
func cronRouteMissingStatus(errText string) string {
	for _, status := range []string{"404", "405"} {
		if strings.Contains(errText, "returned HTTP "+status) {
			return status
		}
	}
	return ""
}

// cronRunRouteMissing reports a run the app answered with 404 or 405.
func cronRunRouteMissing(errText string) bool {
	return cronRouteMissingStatus(errText) != ""
}

// printCronRouteMissingHint explains runs that failed with 404 or 405
// (answered, as "404", "405" or "404 or 405") and names the fix. It reads the
// cron for its path and falls back to a generic sentence when the read fails.
func printCronRouteMissingHint(ctx context.Context, client *Client, cronID string, failed, total int, answered string) {
	target := "the cron's path"
	if client != nil {
		if cron, err := client.GetCron(ctx, cronID); err == nil && cron.Path != "" {
			target = "POST " + cron.Path
		}
	}
	PrintWarn(osStdout, "%d of %d runs failed because the app answered %s for %s: it has no handler for that method and path.", failed, total, answered, target)
	PrintProgress(osStdout, "Crons send POST requests. Serve POST on that route in the app, or point the cron at the right path with `gregale cron update %s --path /...`.", cronID)
}

// cronRunsRouteMissing counts the failed runs on a page the app answered
// with 404 or 405, and names the statuses it saw.
func cronRunsRouteMissing(runs []api.CronRun) (int, string) {
	n := 0
	seen := map[string]bool{}
	for _, r := range runs {
		if r.Outcome != api.CronRunFailed {
			continue
		}
		if status := cronRouteMissingStatus(r.Error); status != "" {
			n++
			seen[status] = true
		}
	}
	switch {
	case seen["404"] && seen["405"]:
		return n, "404 or 405"
	case seen["405"]:
		return n, "405"
	}
	return n, "404"
}
