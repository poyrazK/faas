package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresUpgrade(args []string) int {
	fs := newFlagSet("postgres upgrade", flag.ContinueOnError)
	major := fs.Int("major", 0, "target PostgreSQL major, later than the database's")
	request := fs.String("request-id", "", "stable nonzero UUID; reuse after an uncertain response")
	if err := fs.Parse(normalizePostgresAttachArgs(args)); err != nil {
		return 1
	}
	id, err := uuid.Parse(*request)
	if fs.NArg() != 1 || *major <= 0 || err != nil || id == uuid.Nil || id.String() != *request {
		PrintUsage(os.Stderr, "usage: gregale postgres upgrade DATABASE --major N --request-id UUID", "postgres")
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
	result, err := client.UpgradeManagedPostgresDatabase(context.Background(), database.ID, api.UpgradeManagedPostgresDatabaseRequest{RequestID: *request, PostgresMajor: *major})
	if err != nil {
		return printErr("Could not request managed PostgreSQL upgrade", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresUpgrade(result)
	return 0
}

func cmdPostgresUpgradeStatus(args []string) int {
	fs := newFlagSet("postgres upgrade-status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 2 {
		PrintUsage(os.Stderr, "usage: gregale postgres upgrade-status DATABASE REQUEST_UUID", "postgres")
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
	result, err := client.GetManagedPostgresMajorUpgrade(context.Background(), database.ID, fs.Arg(1))
	if err != nil {
		return printErr("Could not load managed PostgreSQL upgrade", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresUpgrade(result)
	return 0
}

func renderPostgresUpgrade(result api.ManagedPostgresMajorUpgrade) {
	_, _ = fmt.Fprintf(osStdout, "upgrade %s: PostgreSQL %d → %d (%s, generation %d)\n", result.ID, result.FromPostgresMajor, result.TargetPostgresMajor, result.State, result.Generation)
	switch {
	case result.State == "failed" && result.LastErrorCode == "upgrade_refused":
		_, _ = fmt.Fprintf(osStdout, "PostgreSQL refused the upgrade; the database stays on %d, unchanged. Remove what it reported, then request a new upgrade.\n", result.FromPostgresMajor)
	case result.State == "failed":
		_, _ = fmt.Fprintf(osStdout, "The upgrade failed (%s); restore the database to a point before it, which recovers on %d.\n", result.LastErrorCode, result.FromPostgresMajor)
	case result.ConnectionInterruptionExpected && result.State != "succeeded":
		_, _ = fmt.Fprintln(osStdout, "Clients disconnect once while the database restarts on the new major; reconnect using existing credentials.")
	}
	if result.State == "pending" && result.LastErrorCode != "" {
		_, _ = fmt.Fprintf(osStdout, "  last_error_code: %s\n", result.LastErrorCode)
	}
}
