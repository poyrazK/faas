package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresRestoreCheck(database, rawPoint string) int {
	point, err := time.Parse(time.RFC3339Nano, rawPoint)
	if err != nil {
		return printErr("Invalid restore point", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := postgresConditions.resolveRef(ctx, client, database)
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	var check api.ManagedPostgresRestoreCheck
	if org := client.ProjectOrganization(); org != "" {
		check, err = client.CheckOrgManagedPostgresRestore(ctx, org, id, point)
	} else {
		check, err = client.CheckManagedPostgresRestore(ctx, id, point)
	}
	if err != nil {
		return printErr("Could not check managed PostgreSQL restore point", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(check))
	}
	renderPostgresRestoreCheck(osStdout, check)
	return 0
}

func renderPostgresRestoreCheck(w io.Writer, check api.ManagedPostgresRestoreCheck) {
	label := "coverage unverified"
	switch check.Outcome {
	case "covered":
		label = "archive coverage confirmed"
	case "no_base_backup":
		label = "no suitable base backup"
	case "wal_gap":
		label = "confirmed WAL gap"
	}
	_, _ = fmt.Fprintf(w, "  Restore check for %s: %s\n", check.PointInTime.Format(time.RFC3339Nano), label)
	if check.Outcome == "unverified" && check.Reason != "" {
		_, _ = fmt.Fprintf(w, "  Reason: %s\n", strings.ReplaceAll(check.Reason, "_", " "))
	}
	if check.BackupCompletedAt != nil {
		_, _ = fmt.Fprintf(w, "  Base backup completed: %s\n", check.BackupCompletedAt.Format(time.RFC3339Nano))
	}
	if check.WALStopAt != nil {
		_, _ = fmt.Fprintf(w, "  Archived replay stopping point: %s\n", check.WALStopAt.Format(time.RFC3339Nano))
	}
	if check.RecoveryTimeline != 0 {
		_, _ = fmt.Fprintf(w, "  Recovery timeline: %d\n", check.RecoveryTimeline)
	}
	if check.MissingTimelineHistory != "" {
		_, _ = fmt.Fprintf(w, "  Missing timeline history: %s\n", check.MissingTimelineHistory)
	}
	if check.MissingWALSegment != "" {
		_, _ = fmt.Fprintf(w, "  Missing WAL segment: %s\n", check.MissingWALSegment)
	}
}
