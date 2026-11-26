package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbqueryinsights"
)

func cmdPostgresQueries(args []string) int {
	const usage = "usage: gregale postgres queries DATABASE [--since 1h | --enable | --disable]"
	fs := newFlagSet("postgres queries", 0)
	since := fs.Duration("since", time.Hour, "history window from 1 second to 24 hours")
	enable := fs.Bool("enable", false, "enable collection at the next eligible restart")
	disable := fs.Bool("disable", false, "stop collecting statement statistics")
	if err := fs.Parse(normalizePostgresArgs(args)); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *enable && *disable || *since < time.Second || *since > dbqueryinsights.Retention ||
		(*enable || *disable) && flagWasSet(fs, "since") {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	id, err := postgresConditions.resolveRef(ctx, client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	org := client.ProjectOrganization()
	var report api.ManagedPostgresQueryInsights
	switch {
	case *enable || *disable:
		req := api.SetManagedPostgresQueryInsightsRequest{Enabled: *enable}
		if org != "" {
			report, err = client.SetOrgManagedPostgresQueryInsights(ctx, org, id, req)
		} else {
			report, err = client.SetManagedPostgresQueryInsights(ctx, id, req)
		}
	case org != "":
		report, err = client.GetOrgManagedPostgresQueryInsights(ctx, org, id, *since)
	default:
		report, err = client.GetManagedPostgresQueryInsights(ctx, id, *since)
	}
	if err != nil {
		return printErr("Could not access PostgreSQL Query Insights", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	renderPostgresQueryInsights(osStdout, report)
	return 0
}

func renderPostgresQueryInsights(w io.Writer, report api.ManagedPostgresQueryInsights) {
	switch {
	case !report.Enabled:
		_, _ = fmt.Fprintln(w, "Query Insights collection is disabled.")
	case report.LastSampleAt == nil:
		_, _ = fmt.Fprintln(w, "Query Insights is enabled. Waiting for a sample from a configured primary.")
	default:
		_, _ = fmt.Fprintf(w, "Last sample: %s\n", formatBackupTimestamp(*report.LastSampleAt))
	}
	if report.ObservedThrough == nil {
		_, _ = fmt.Fprintln(w, "No complete observation intervals in this window.")
		return
	}
	_, _ = fmt.Fprintf(w, "Observed %s since %s, through %s\n", time.Duration(report.ObservedSeconds)*time.Second,
		formatBackupTimestamp(report.Since), formatBackupTimestamp(*report.ObservedThrough))
	for _, q := range report.Queries {
		_, _ = fmt.Fprintf(w, "  %s (boot %s)  %d calls  %.2f ms total  %.2f ms mean\n",
			q.QueryID, formatBackupTimestamp(q.BootAt), q.Calls, q.TotalExecutionMS, q.MeanExecutionMS)
	}
}
