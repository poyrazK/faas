package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbqueryerrors"
)

func cmdPostgresErrorHistory(args []string) int {
	const command = "postgres errors"
	fs := newFlagSet(command, flag.ContinueOnError)
	enable := fs.Bool("enable", false, "enable collection for pooled connections")
	disable := fs.Bool("disable", false, "stop collecting new events")
	since := fs.Duration("since", time.Hour, "lookback from 1s to 24h")
	code := fs.String("sqlstate", "", "filter by PostgreSQL SQLSTATE")
	limit := fs.Int("limit", dbqueryerrors.DefaultResultLimit, "maximum number of events")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	*code = strings.ToUpper(*code)
	if fs.NArg() != 1 || *enable && *disable || *since < time.Second || *since > dbqueryerrors.Retention ||
		*limit < 1 || *limit > dbqueryerrors.MaxResultLimit || *code != "" && !dbqueryerrors.ValidSQLState(*code) {
		PrintUsage(os.Stderr, "usage: gregale "+command+" DATABASE [--since 1h] [--sqlstate CODE] [--limit COUNT] [--enable | --disable]", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	database, err := postgresConditions.resolveRef(ctx, client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	org := client.ProjectOrganization()
	var report api.ManagedPostgresErrorHistory
	if *enable || *disable {
		report, err = client.SetManagedPostgresErrorHistory(ctx, org, database, api.SetManagedPostgresErrorHistoryRequest{Enabled: *enable})
		if err == nil && (*since != time.Hour || *code != "" || *limit != dbqueryerrors.DefaultResultLimit) {
			report, err = client.GetManagedPostgresErrorHistory(ctx, org, database, *since, *code, *limit)
		}
	} else {
		report, err = client.GetManagedPostgresErrorHistory(ctx, org, database, *since, *code, *limit)
	}
	if err != nil {
		return printErr("Could not manage PostgreSQL error history", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	renderPostgresErrorHistory(report)
	return 0
}

func renderPostgresErrorHistory(report api.ManagedPostgresErrorHistory) {
	_, _ = fmt.Fprintf(osStdout, "Collection enabled\t%t\n", report.Enabled)
	if len(report.Events) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No pooled errors in this time range.")
		return
	}
	_, _ = fmt.Fprintln(osStdout, "TIME (UTC)\tSEVERITY\tSQLSTATE\tBINDING")
	for _, event := range report.Events {
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\n", event.ObservedAt.UTC().Format(time.RFC3339Nano), event.Severity, event.SQLState, event.BindingID)
	}
	if report.HasMore {
		_, _ = fmt.Fprintln(osStdout, "More events match. Increase --limit or narrow the time range.")
	}
}
