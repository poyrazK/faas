package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func renderPostgresBackupStatus(w io.Writer, status api.ManagedPostgresBackupStatus) {
	retention := "off"
	if status.RestoreWindowSeconds > 0 {
		retention = formatPostgresDuration(status.RestoreWindowSeconds)
	}
	_, _ = fmt.Fprintf(w, "  Restore window: %s\n", retention)

	backup := "none recorded"
	if status.LastBaseBackupAt != nil {
		backup = formatBackupTimestamp(*status.LastBaseBackupAt)
	}
	_, _ = fmt.Fprintf(w, "  Last recorded base backup: %s\n", backup)

	if drill := status.LatestRestoreDrill; drill != nil {
		_, _ = fmt.Fprintf(w, "  Latest restore drill: %s (started %s",
			drill.Outcome, formatBackupTimestamp(drill.StartedAt))
		if drill.DrillID != "" {
			_, _ = fmt.Fprintf(w, ", drill %s", drill.DrillID)
		}
		if !drill.TargetAt.IsZero() {
			_, _ = fmt.Fprintf(w, ", target %s", formatBackupTimestamp(drill.TargetAt))
		}
		if drill.FinishedAt != nil {
			_, _ = fmt.Fprintf(w, ", finished %s", formatBackupTimestamp(*drill.FinishedAt))
		}
		if drill.RecoveredTo != nil {
			_, _ = fmt.Fprintf(w, ", replayed to %s", formatBackupTimestamp(*drill.RecoveredTo))
		}
		if drill.DurationSeconds != nil {
			_, _ = fmt.Fprintf(w, ", elapsed %s", time.Duration(*drill.DurationSeconds)*time.Second)
		}
		if drill.ErrorCode != "" {
			_, _ = fmt.Fprintf(w, ", reason %s", strings.ReplaceAll(drill.ErrorCode, "_", " "))
		}
		_, _ = fmt.Fprintln(w, ")")
	}
	if recovery := status.Recovery; recovery != nil {
		_, _ = fmt.Fprintf(w, "  Recovery region: %s (%s)\n", recovery.Region, recovery.Reason)
		if recovery.RecoveryTimeline != 0 {
			_, _ = fmt.Fprintf(w, "  Recovery timeline: %d\n", recovery.RecoveryTimeline)
		}
		if recovery.MissingTimelineHistory != "" {
			_, _ = fmt.Fprintf(w, "  Missing timeline history: %s\n", recovery.MissingTimelineHistory)
		}
		if recovery.RecoveryPoint != nil {
			_, _ = fmt.Fprintf(w, "  Latest verified recovery point: %s (%ds behind)\n",
				formatBackupTimestamp(*recovery.RecoveryPoint), recovery.RecoveryLagSeconds)
		}
	}
	verified := status.LastVerifiedRestore
	if verified == nil {
		_, _ = fmt.Fprintln(w, "  Restore verification: unverified")
		return
	}
	if status.LatestRestoreDrill != nil && status.LatestRestoreDrill.Outcome == "succeeded" {
		return
	}
	_, _ = fmt.Fprintf(w, "  Last verified restore: target %s", formatBackupTimestamp(verified.TargetAt))
	if verified.RecoveredTo != nil {
		_, _ = fmt.Fprintf(w, ", replayed to %s", formatBackupTimestamp(*verified.RecoveredTo))
	}
	if verified.FinishedAt != nil {
		_, _ = fmt.Fprintf(w, " (checked %s)", formatBackupTimestamp(*verified.FinishedAt))
	}
	_, _ = fmt.Fprintln(w)
}

func formatBackupTimestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
