package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const postgresWakeWithAppUsage = "usage: gregale postgres app-wake set DATABASE on|off"

// cmdPostgresWakeWithApp turns starting a scale-to-zero database when a
// bound app wakes on or off (ADR-20261125105038).
func cmdPostgresWakeWithApp(args []string) int {
	fs := newFlagSet("postgres app-wake", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 2 || fs.Arg(1) != "on" && fs.Arg(1) != "off" {
		PrintUsage(os.Stderr, postgresWakeWithAppUsage, "postgres")
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
	result, err := client.SetManagedPostgresWakeWithApp(context.Background(), database.ID, api.SetManagedPostgresWakeWithAppRequest{Enabled: fs.Arg(1) == "on"})
	if err != nil {
		return printErr("Could not change whether the database starts with its app", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintln(osStdout, renderPostgresWakeWithApp(database.Name, result))
	return 0
}

func renderPostgresWakeWithApp(name string, result api.ManagedPostgresWakeWithApp) string {
	if result.Enabled {
		return fmt.Sprintf("%s: starts when an app bound to it wakes; a wake that never queries it still bills at least one suspend timeout of compute", name)
	}
	return fmt.Sprintf("%s: starts on its app's first query", name)
}
