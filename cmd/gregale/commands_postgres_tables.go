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
)

func cmdPostgresTables(args []string) int {
	const usage = "usage: gregale postgres tables DATABASE [--sort size|dead-rows|xid-age] [--limit N]"
	fs := newFlagSet("postgres tables", 0)
	sort := fs.String("sort", "size", "rank by size, dead-rows, or xid-age")
	limit := fs.Int("limit", dbguest.TableHealthDefaultLimit, "maximum number of tables")
	if err := fs.Parse(normalizePostgresArgs(args)); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	options := dbguest.TableHealthOptions{Sort: *sort, Limit: *limit}
	if err := options.Validate(); err != nil {
		printCommandValidation(os.Stderr, "gregale postgres tables: %v\n", err)
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
	var report api.ManagedPostgresTableHealth
	if org := client.ProjectOrganization(); org != "" {
		report, err = client.GetOrgManagedPostgresTableHealth(ctx, org, id, options)
	} else {
		report, err = client.GetManagedPostgresTableHealth(ctx, id, options)
	}
	if err != nil {
		return printErr("Could not inspect PostgreSQL tables", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(report))
	}
	renderPostgresTables(osStdout, report)
	return 0
}

func renderPostgresTables(w io.Writer, report api.ManagedPostgresTableHealth) {
	if report.State == dbguest.DiagnosticsParked {
		_, _ = fmt.Fprintln(w, "Database is parked. A table health sample is available while it is running.")
		return
	}
	sample := report.Sample
	if report.State != dbguest.DiagnosticsAvailable || sample == nil {
		_, _ = fmt.Fprintln(w, "The primary could not provide a table health sample.")
		return
	}
	_, _ = fmt.Fprintf(w, "Sampled at %s (primary)\n", formatBackupTimestamp(sample.SampledAt))
	if len(sample.Tables) == 0 {
		_, _ = fmt.Fprintln(w, "No stored user tables.")
		return
	}
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "TABLE\tTABLE SIZE\tINDEX SIZE\tEST. DEAD ROWS\tCHANGES SINCE ANALYZE\tXID AGE\tLAST VACUUM\tLAST ANALYZE")
	for _, row := range sample.Tables {
		_, _ = fmt.Fprintf(table, "%s.%s\t%s\t%s\t%d\t%d\t%d\t%s\t%s\n",
			strconv.Quote(row.Schema), strconv.Quote(row.Name), formatIECBytes(row.TableBytes), formatIECBytes(row.IndexBytes),
			row.EstimatedDeadRows, row.ChangesSinceAnalyze, row.XIDAge, tableHealthTime(row.LastVacuumAt), tableHealthTime(row.LastAnalyzeAt))
	}
	_ = table.Flush()
	if sample.HasMore {
		_, _ = fmt.Fprintf(w, "Showing %d tables. More tables are available.\n", len(sample.Tables))
	}
}

func tableHealthTime(at *time.Time) string {
	if at == nil {
		return "-"
	}
	return formatBackupTimestamp(*at)
}
