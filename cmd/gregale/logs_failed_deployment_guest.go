package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// failedDeploymentGuestDays bounds how many UTC days of a failed deployment's
// instance archive one `logs --deployment` reads.
const failedDeploymentGuestDays = 3

// printFailedDeploymentGuestLogs prints the retained output of the instances a
// failed deployment booted. A deployment that failed after its build (a
// snapshot prime that timed out, an app that crashed at start) wrote its boot
// and app output there, but `logs --deployment` showed only the build log, so
// the line that explained the failure was never on screen (prod hunt r71). It
// returns how many lines it printed; a missing archive is a note, not an error.
func printFailedDeploymentGuestLogs(ctx context.Context, client parkedWindowLogsClient, slug string, dep api.DeploymentResponse, now time.Time) int {
	if dep.Status != deploymentStatusFailed || slug == "" {
		return 0
	}
	instances, err := client.ListInstancesWithHistory(ctx, slug, true)
	if err != nil {
		return 0
	}
	days := failedDeploymentDays(dep.CreatedAt, now)
	printed := 0
	for _, ins := range instances {
		if ins.DeploymentID != dep.ID || liveLogStates[ins.State] {
			continue
		}
		header := false
		for _, day := range days {
			body, err := client.StreamAppArchivedLogs(ctx, slug, api.ArchiveLogSelector{InstanceID: ins.ID, Date: day})
			if err != nil {
				continue
			}
			dec := api.NewDecoder(body)
			dec.SetCloseFn(body.Close)
			_, _ = consumeLogStream(ctx, dec.Events(), dec.Errors(), func(e api.Event) (bool, int) {
				if e.Event == "end" {
					return true, 0
				}
				if e.Data == "" {
					return false, 0
				}
				if jsonOutput {
					_ = json.NewEncoder(osStdout).Encode(unrunDeploymentLogRecord{Type: "log", Deployment: dep.ID, Source: "instance " + ins.ID, Line: formatRuntimeLogLine(e.Data)})
				} else {
					if !header {
						_, _ = fmt.Fprintf(osStderr, "Retained output of instance %s, which this deployment booted:\n", ins.ID)
						header = true
					}
					_ = printTerminalSafeLines(osStdout, "", formatRuntimeLogLine(e.Data))
				}
				printed++
				return false, 0
			})
			_ = dec.Close()
		}
	}
	return printed
}

// failedDeploymentDays lists the UTC days from a deployment's creation to now,
// newest last, at most failedDeploymentGuestDays of them.
func failedDeploymentDays(createdAt string, now time.Time) []string {
	start, err := time.Parse(time.RFC3339, createdAt)
	if err != nil || start.After(now) {
		start = now
	}
	end := now.UTC().Truncate(24 * time.Hour)
	first := start.UTC().Truncate(24 * time.Hour)
	if limit := end.Add(-time.Duration(failedDeploymentGuestDays-1) * 24 * time.Hour); first.Before(limit) {
		first = limit
	}
	var out []string
	for d := first; !d.After(end); d = d.Add(24 * time.Hour) {
		out = append(out, d.Format("2006-01-02"))
	}
	return out
}
