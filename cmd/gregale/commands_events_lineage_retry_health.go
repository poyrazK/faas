package main

import (
	"fmt"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

func printLineageRetryJobHealth(health *api.EventExecutionLineageRetryJobHealth) {
	if health == nil {
		return
	}
	_, _ = fmt.Fprintf(osStdout, "Lineage retry jobs: pending %d | paused %d | remaining cases %d | stalled pending %d | retention risk %d | unknown progress %d | counts complete %t\n", health.PendingJobs, health.PausedJobs, health.RemainingCases, health.StalledPendingJobs, health.RetentionRiskJobs, health.UnknownProgressJobs, health.CountsComplete)
	_, _ = fmt.Fprintf(osStdout, "Warnings: inactivity %ds | retention %ds | observed %d/%d jobs | earliest observed expiry %v\n", health.InactivityWarningSeconds, health.RetentionWarningSeconds, health.ObservedJobs, health.JobLimit, health.NextRetainUntil)
	for _, job := range health.Jobs {
		progress := "unknown (legacy evaluated job)"
		if job.ProgressAgeSeconds != nil {
			progress = strconv.FormatFloat(*job.ProgressAgeSeconds, 'f', 0, 64) + "s"
		}
		_, _ = fmt.Fprintf(osStdout, "Retry job %s | %s | processed %d/%d | inactive %.0fs | progress age %s | stalled %t | retention risk %t | expires %s | created %s\n", oneLine(job.RequestID), oneLine(job.Status), job.Processed, job.Selected, job.InactivityAgeSeconds, progress, job.StalledPending, job.RetentionRisk, job.RetainUntil.Format("2006-01-02T15:04:05.999999999Z07:00"), job.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"))
	}
}
