package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresResize(args []string) int {
	fs := newFlagSet("postgres resize", flag.ContinueOnError)
	class := fs.String("class", "", "target service class")
	request := fs.String("request-id", "", "stable nonzero UUID; reuse after an uncertain response")
	if err := fs.Parse(normalizePostgresAttachArgs(args)); err != nil {
		return 1
	}
	id, err := uuid.Parse(*request)
	if fs.NArg() != 1 || (*class != "development" && *class != "burstable" && *class != "production") || err != nil || id == uuid.Nil || id.String() != *request {
		PrintUsage(os.Stderr, "usage: gregale postgres resize DATABASE --class CLASS --request-id UUID", "postgres")
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
	result, err := client.ResizeManagedPostgresDatabase(context.Background(), database.ID, api.ResizeManagedPostgresDatabaseRequest{RequestID: *request, ServiceClass: *class})
	if err != nil {
		return printErr("Could not request managed PostgreSQL resize", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresResize(result)
	return 0
}

func cmdPostgresResizeStatus(args []string) int {
	fs := newFlagSet("postgres resize-status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 2 {
		PrintUsage(os.Stderr, "usage: gregale postgres resize-status DATABASE REQUEST_UUID", "postgres")
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
	result, err := client.GetManagedPostgresResize(context.Background(), database.ID, fs.Arg(1))
	if err != nil {
		return printErr("Could not load managed PostgreSQL resize", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresResize(result)
	return 0
}

func renderPostgresResize(result api.ManagedPostgresResize) {
	_, _ = fmt.Fprintf(osStdout, "resize %s: %s → %s (%s, generation %d)\n", result.ID, result.FromClass, result.TargetClass, result.State, result.Generation)
	if result.ConnectionInterruptionExpected {
		_, _ = fmt.Fprintln(osStdout, "Clients may disconnect during resizing; reconnect using existing credentials.")
	}
	if result.LastErrorCode != "" {
		_, _ = fmt.Fprintf(osStdout, "  last_error_code: %s\n", result.LastErrorCode)
	}
}
