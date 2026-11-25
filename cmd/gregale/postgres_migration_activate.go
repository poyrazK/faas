package main

import (
	"context"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
)

func activatePostgresMigration(args []string) int {
	const usage = "usage: gregale postgres migrate activate TARGET --source SOURCE --app APP --request-id UUID --verification-id UUID --source-writes-paused [--env SCOPE] [--no-wait]"
	fs := newFlagSet("postgres migrate activate", 0)
	source := fs.String("source", "", "managed source database")
	app := fs.String("app", "", "app whose managed PostgreSQL bindings move")
	scope := fs.String("env", "", "environment whose managed bindings move")
	operation := fs.String("request-id", "", "original migration operation UUID")
	verification := fs.String("verification-id", "", "verification UUID for the current write pause")
	noWait := fs.Bool("no-wait", false, "return after recording the switch")
	paused := fs.Bool("source-writes-paused", false, "confirm source writers remain paused and drained")
	if err := fs.Parse(normalizePostgresArgs(args)); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *source == "" || *app == "" || !*paused ||
		!dbguest.ValidOperationID(*operation) || !dbguest.ValidOperationID(*verification) {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	sourceID, err := postgresConditions.resolveRef(ctx, client, *source)
	if err != nil {
		return printErr("Could not find source database", err)
	}
	targetID, err := postgresConditions.resolveRef(ctx, client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find target database", err)
	}
	sw, err := client.ActivateManagedPostgresMigration(ctx, *app, api.ActivateManagedPostgresMigrationRequest{
		SourceDatabaseID: sourceID, TargetDatabaseID: targetID, OperationID: *operation,
		VerificationID: *verification, SourceWritesPaused: *paused, Scope: *scope,
	})
	if err != nil {
		return printErr("Could not start PostgreSQL migration activation", err)
	}
	if *noWait {
		if jsonOutput {
			return jsonOut(writeJSON(sw))
		}
		PrintOK(osStdout, "Database switch %s started. Follow it with gregale release rollback status %s %s --wait.", sw.ID, *app, sw.ID)
		return 0
	}
	return followDatabaseSwitch(client, *app, sw, databaseSwitchDefaultTimeout, 2*time.Second)
}
