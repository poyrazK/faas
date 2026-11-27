package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
)

func cmdPostgresDiagnose(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale postgres diagnose DATABASE", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	id, err := postgresConditions.resolveRef(ctx, client, args[0])
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	var report api.ManagedPostgresDiagnostics
	if org := client.ProjectOrganization(); org != "" {
		report, err = client.GetOrgManagedPostgresDiagnostics(ctx, org, id)
	} else {
		report, err = client.GetManagedPostgresDiagnostics(ctx, id)
	}
	if err != nil {
		return printErr("Could not diagnose managed PostgreSQL", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	renderPostgresDiagnostics(osStdout, report)
	return 0
}

func renderPostgresDiagnostics(w io.Writer, report api.ManagedPostgresDiagnostics) {
	if report.State == dbguest.DiagnosticsParked {
		_, _ = fmt.Fprintln(w, "Database is parked. A sample is available while it is running.")
		return
	}
	sample := report.Sample
	if report.State != dbguest.DiagnosticsAvailable || sample == nil {
		_, _ = fmt.Fprintln(w, "The primary could not provide an activity sample.")
		return
	}
	_, _ = fmt.Fprintf(w, "Sampled at %s (primary)\n", formatBackupTimestamp(sample.SampledAt))
	pressure := ""
	if sample.Connections >= sample.ConnectionLimit {
		pressure = " (at or above capacity)"
	}
	_, _ = fmt.Fprintf(w, "Connections: %d / %d ordinary slots%s\n", sample.Connections, sample.ConnectionLimit, pressure)
	writePostgresTransactionAge(w, "Active transactions", sample.ActiveTransactions, sample.OldestActiveTransactionMS)
	writePostgresTransactionAge(w, "Idle in transaction", sample.IdleInTransaction, sample.OldestIdleTransactionMS)
	_, _ = fmt.Fprintf(w, "Sessions waiting on a lock: %d\n", sample.LockWaits)
	for _, wait := range sample.BlockedSessions {
		_, _ = fmt.Fprintf(w, "  PID %d", wait.PID)
		if len(wait.BlockingPIDs) > 0 {
			_, _ = fmt.Fprint(w, " blocked by")
			for _, pid := range wait.BlockingPIDs {
				if pid == 0 {
					_, _ = fmt.Fprint(w, " prepared transaction")
				} else {
					_, _ = fmt.Fprintf(w, " PID %d", pid)
				}
			}
		}
		_, _ = fmt.Fprintln(w)
	}
	if int64(len(sample.BlockedSessions)) < sample.LockWaits {
		_, _ = fmt.Fprintf(w, "  Showing %d of %d waiting sessions\n", len(sample.BlockedSessions), sample.LockWaits)
	}
}

func writePostgresTransactionAge(w io.Writer, label string, count, ageMS int64) {
	_, _ = fmt.Fprintf(w, "%s: %d", label, count)
	if count > 0 {
		_, _ = fmt.Fprintf(w, " (oldest %s)", time.Duration(ageMS)*time.Millisecond)
	}
	_, _ = fmt.Fprintln(w)
}
