package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbquerycontrol"
)

func cmdPostgresSessions(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale postgres sessions DATABASE", "postgres")
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
	var report api.ManagedPostgresSessions
	if org := client.ProjectOrganization(); org != "" {
		report, err = client.ListOrgManagedPostgresSessions(ctx, org, id)
	} else {
		report, err = client.ListManagedPostgresSessions(ctx, id)
	}
	if err != nil {
		return printErr("Could not list PostgreSQL sessions", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	renderPostgresSessions(osStdout, report.Sessions)
	return 0
}

func renderPostgresSessions(w io.Writer, report dbguest.Sessions) {
	switch {
	case report.State == dbguest.DiagnosticsParked:
		_, _ = fmt.Fprintln(w, "Database is parked. Sessions are available while it is running.")
	case report.Sample == nil:
		_, _ = fmt.Fprintln(w, "The primary could not provide a session sample.")
	case len(report.Sample.Sessions) == 0:
		_, _ = fmt.Fprintln(w, "No client backends on the primary.")
	default:
		_, _ = fmt.Fprintf(w, "Sampled at %s (primary)\n", formatBackupTimestamp(report.Sample.SampledAt))
		table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(table, "PID\tSTATE\tTRANSACTION AGE\tWAIT EVENT\tAPPLICATION\tCANCEL TARGET")
		for _, row := range report.Sample.Sessions {
			wait := row.WaitEvent
			if row.WaitEventType != "" {
				wait = row.WaitEventType + "/" + wait
			}
			_, _ = fmt.Fprintf(table, "%d\t%s\t%s\t%s\t%s\t%s\n", row.PID,
				sessionField(row.State), time.Duration(row.TransactionAgeMS)*time.Millisecond,
				sessionField(wait), sessionField(row.ApplicationName), row.TargetID)
		}
		_ = table.Flush()
		if report.Sample.HasMore {
			_, _ = fmt.Fprintf(w, "Showing %d client backends.\n", len(report.Sample.Sessions))
		}
	}
}

func sessionField(value string) string {
	if value == "" {
		return "-"
	}
	return strconv.Quote(value)
}

func cmdPostgresCancelQuery(args []string) int {
	if len(args) != 2 || !dbquerycontrol.ValidID(args[1]) {
		PrintUsage(os.Stderr, "usage: gregale postgres cancel-query DATABASE TARGET_UUID", "postgres")
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
	request := api.CancelManagedPostgresQueryRequest{TargetID: args[1]}
	var report api.ManagedPostgresQueryCancellation
	if org := client.ProjectOrganization(); org != "" {
		report, err = client.CancelOrgManagedPostgresQuery(ctx, org, id, request)
	} else {
		report, err = client.CancelManagedPostgresQuery(ctx, id, request)
	}
	if err != nil {
		return printErr("Could not cancel PostgreSQL query", err)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		switch report.Outcome {
		case dbquerycontrol.SignalSent:
			_, _ = fmt.Fprintln(osStdout, "Sent the PostgreSQL cancel request.")
		case dbquerycontrol.Stale:
			_, _ = fmt.Fprintln(osStdout, "The query target is stale. Refresh the session list.")
		default:
			_, _ = fmt.Fprintln(osStdout, "The proxy could not confirm sending the cancel request.")
		}
	}
	if report.Outcome != dbquerycontrol.SignalSent {
		return 1
	}
	return 0
}
