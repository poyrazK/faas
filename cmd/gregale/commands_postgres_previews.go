package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

// cmdPostgresPreviews sets what a database gives PR preview environments
// (ADR-20261102123523): each preview environment of an app bound to it gets
// its own empty database, a copy of this one, or none.
func cmdPostgresPreviews(args []string) int {
	fs := newFlagSet("postgres previews", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 2 || (fs.Arg(1) != "empty" && fs.Arg(1) != "copy" && fs.Arg(1) != "off") {
		PrintUsage(os.Stderr, "usage: gregale postgres previews set DATABASE empty|copy|off", "postgres")
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
	switch updated.Previews {
	case "":
		_, _ = fmt.Fprintf(osStdout, "%s: PR previews get no database; existing preview databases go with their pull requests\n", updated.Name)
	case "copy":
		_, _ = fmt.Fprintf(osStdout, "%s: each PR preview environment gets a copy of this database as of its setup, bound like production and removed with the preview. Code under review can read the copy.\n", updated.Name)
	default:
		_, _ = fmt.Fprintf(osStdout, "%s: each PR preview environment gets its own empty database, bound like production and removed with the preview\n", updated.Name)
	}
	return 0
}
