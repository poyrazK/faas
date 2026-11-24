package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const postgresSuspendTimeoutUsage = "usage: gregale postgres suspend-timeout set DATABASE DURATION|default   (DURATION 1m..1h, e.g. 2m)"

// parsePostgresSuspendTimeout reads 1m..1h in whole seconds, or default (nil).
func parsePostgresSuspendTimeout(value string) (*int64, bool) {
	if value == "default" {
		return nil, true
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration%time.Second != 0 {
		return nil, false
	}
	seconds := int64(duration / time.Second)
	if seconds < api.ManagedPostgresSuspendTimeoutMinSeconds || seconds > api.ManagedPostgresSuspendTimeoutMaxSeconds {
		return nil, false
	}
	return &seconds, true
}

// cmdPostgresSuspendTimeout sets how long a scale-to-zero database stays up
// after its last activity; every wake bills at least this much compute.
func cmdPostgresSuspendTimeout(args []string) int {
	fs := newFlagSet("postgres suspend-timeout", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 2 {
		PrintUsage(os.Stderr, postgresSuspendTimeoutUsage, "postgres")
		return 1
	}
	seconds, ok := parsePostgresSuspendTimeout(fs.Arg(1))
	if !ok {
		PrintUsage(os.Stderr, postgresSuspendTimeoutUsage, "postgres")
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
	result, err := client.SetManagedPostgresSuspendTimeout(context.Background(), database.ID, api.SetManagedPostgresSuspendTimeoutRequest{Seconds: seconds})
	if err != nil {
		return printErr("Could not change when the database suspends", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintln(osStdout, renderPostgresSuspendTimeout(database.Name, result))
	return 0
}

func renderPostgresSuspendTimeout(name string, result api.ManagedPostgresSuspendTimeout) string {
	after := (time.Duration(result.Seconds) * time.Second).String()
	source := ""
	if !result.Custom {
		source = " (platform default)"
	}
	line := fmt.Sprintf("%s: suspends after %s idle%s; each wake bills at least that much compute", name, after, source)
	if !result.Applied {
		line += "\n  Saved, not applied yet: the database's compute takes it the next time it is suspended (usually within minutes of going idle)."
	}
	return line
}
