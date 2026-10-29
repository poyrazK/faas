package main

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cronRunRouteMissing reports a run the app answered with 404: it has no
// handler for the cron's method and path. Most failed cron runs in
// production are this misconfiguration, not a delivery failure.
func cronRunRouteMissing(errText string) bool {
	return strings.Contains(errText, "returned HTTP 404")
}

// printCronRouteMissingHint explains runs that failed with 404 and names the
// fix. It reads the cron for its path and falls back to a generic sentence
// when the read fails.
func printCronRouteMissingHint(client *Client, cronID string, failed, total int) {
	target := "the cron's path"
	if client != nil {
		if cron, err := client.GetCron(context.Background(), cronID); err == nil && cron.Path != "" {
			target = "POST " + cron.Path
		}
	}
	PrintWarn(osStdout, "%d of %d runs failed because the app answered 404 for %s: it has no handler for that method and path.", failed, total, target)
	PrintProgress(osStdout, "Serve that route in the app, or point the cron at the right path with `gregale cron update %s --path /...`.", cronID)
}

// cronRunsRouteMissing counts the failed runs on a page the app answered
// with 404.
func cronRunsRouteMissing(runs []api.CronRun) int {
	n := 0
	for _, r := range runs {
		if r.Outcome == api.CronRunFailed && cronRunRouteMissing(r.Error) {
			n++
		}
	}
	return n
}
