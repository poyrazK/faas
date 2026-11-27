package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/pgextension"
)

func cmdPostgresExtensions(args []string) int {
	if len(args) >= 2 && args[0] == "list" {
		fs := newFlagSet("postgres extensions list", flag.ContinueOnError)
		major := fs.Int("target-major", 0, "check extension compatibility with a later PostgreSQL major")
		if err := fs.Parse(normalizePostgresAttachArgs(args[1:])); err != nil {
			return 1
		}
		if fs.NArg() == 1 && *major >= 0 {
			return runPostgresExtensions(fs.Arg(0), "", *major)
		}
	}
	if len(args) == 3 && args[0] == "enable" {
		if !pgextension.Managed(args[2]) {
			return printErr("Unknown managed PostgreSQL extension", fmt.Errorf("choose pg_trgm, btree_gist, or vector"))
		}
		return runPostgresExtensions(args[1], args[2], 0)
	}
	PrintUsage(os.Stderr, "usage: gregale postgres extensions list DATABASE [--target-major N] | enable DATABASE EXTENSION", "postgres")
	return 1
}

func runPostgresExtensions(database, name string, targetMajor int) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	id, err := postgresConditions.resolveRef(ctx, client, database)
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	var result api.ManagedPostgresExtensions
	if name == "" {
		result, err = client.ListManagedPostgresExtensions(ctx, client.ProjectOrganization(), id, targetMajor)
	} else {
		result, err = client.EnableManagedPostgresExtension(ctx, client.ProjectOrganization(), id, name)
	}
	if err != nil {
		return printErr("Could not manage PostgreSQL extensions", err)
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else {
		_, _ = fmt.Fprintln(osStdout, "Installed extensions")
		for _, e := range result.Installed {
			_, _ = fmt.Fprintf(osStdout, "  %s %s\n", e.Name, e.Version)
		}
		_, _ = fmt.Fprintln(osStdout, "Available managed extension versions")
		for _, e := range result.Available {
			marker := ""
			if e.Default {
				marker = " (default)"
			}
			_, _ = fmt.Fprintf(osStdout, "  %s %s%s\n", e.Name, e.Version, marker)
		}
		if targetMajor > 0 && len(result.IncompatibleExtensions) == 0 {
			_, _ = fmt.Fprintf(osStdout, "No extension version blockers found for PostgreSQL %d.\n", targetMajor)
		}
		if len(result.IncompatibleExtensions) != 0 {
			_, _ = fmt.Fprintf(osStdout, "Incompatible with PostgreSQL %d\n", targetMajor)
			for _, e := range result.IncompatibleExtensions {
				_, _ = fmt.Fprintf(osStdout, "  %s %s\n", e.Name, e.Version)
			}
		}
	}
	if len(result.IncompatibleExtensions) != 0 {
		return 1
	}
	return 0
}
