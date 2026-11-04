package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// cmdPostgresPreviews sets what a database gives PR preview environments
// (ADR-20261102123523): each preview environment of an app bound to it gets
// its own empty database, or none.
func cmdPostgresPreviews(args []string) int {
	fs := newFlagSet("postgres previews", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 2 || (fs.Arg(1) != "empty" && fs.Arg(1) != "off") {
		PrintUsage(os.Stderr, "usage: gregale postgres previews set DATABASE empty|off", "postgres")
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
	updated, err := client.SetManagedPostgresPreviews(context.Background(), database.ID, fs.Arg(1))
	if err != nil {
		return printErr("Could not change the database's PR previews", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(updated))
	}
	if updated.Previews == "" {
		_, _ = fmt.Fprintf(osStdout, "%s: PR previews get no database; existing preview databases go with their pull requests\n", updated.Name)
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "%s: each PR preview environment gets its own empty database, bound like production and removed with the preview\n", updated.Name)
	return 0
}
