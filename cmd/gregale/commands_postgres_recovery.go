package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

func cmdPostgresRecovery(args []string) int {
	fs := newFlagSet("postgres recovery", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale postgres recovery DATABASE", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	database, err := resolveManagedPostgresDatabase(context.Background(), client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	status, err := client.GetManagedPostgresRecoveryStatus(context.Background(), database.ID)
	if err != nil {
		return printErr("Could not load managed PostgreSQL recovery status", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(status))
	}
	_, _ = fmt.Fprintf(osStdout, "recovery %s: %s (fresh: %t)\n", database.Name, status.Status, status.Fresh)
	if status.EarliestPossibleTime != "" && status.LatestPossibleTime != "" {
		_, _ = fmt.Fprintf(osStdout, "  possible range: %s through %s\n", status.EarliestPossibleTime, status.LatestPossibleTime)
	}
	if status.Status == "limits_known" {
		_, _ = fmt.Fprintln(osStdout, "Retention limits are known; recoverability of every timestamp is unconfirmed.")
	}
	if status.LastErrorCode != "" {
		_, _ = fmt.Fprintf(osStdout, "  last_error_code: %s\n", status.LastErrorCode)
	}
	return 0
}
