package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale postgres restore <database> --switch <app>`: restore the database
// and move the app to the copy without changing its code (ADR-20261023181931
// amendment 3). It runs the switch a rollback that restores the database
// runs, with the live release as its target.

type postgresRestoreSwitch struct {
	database, name, pointInTime, app, scope string
	confirmed, noWait                       bool
	timeout, interval                       time.Duration
}

func cmdPostgresRestoreSwitch(o postgresRestoreSwitch) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	sourceID, err := postgresDatabaseID(context.Background(), client, o.database)
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	if !o.confirmed {
		if jsonOutput || nonInteractive {
			return printErr("Confirmation required", errors.New("pass --yes to restore the database and switch the app"))
		}
		writePostgresRestoreSwitchPlan(osStderr, o)
		if !requireTyped(o.app) {
			return 1
		}
	}
	sw, err := client.CreateDatabaseSwitch(context.Background(), o.app, api.CreateDatabaseSwitchRequest{
		DatabaseID: sourceID, Name: o.name, PointInTime: o.pointInTime, Scope: o.scope})
	if err != nil {
		return printErr("Could not restore and switch", err)
	}
	if o.noWait {
		if jsonOutput {
			return jsonOut(writeJSON(sw))
		}
		PrintOK(osStdout, "Database switch %s started; follow it with: gregale release rollback status %s %s --wait", sw.ID, o.app, sw.ID)
		return 0
	}
	return followDatabaseSwitch(client, o.app, api.MigrationDatabaseSwitch{DatabaseSwitch: sw}, o.timeout, o.interval)
}

// writePostgresRestoreSwitchPlan explains the switch before it is confirmed:
// the restore, the writes left in the source and the downtime.
func writePostgresRestoreSwitchPlan(w io.Writer, o postgresRestoreSwitch) {
	_, _ = fmt.Fprintf(w, "%s is restored to %s as a new database, %s, and %s moves to it; its code does not change.\n",
		o.database, o.pointInTime, o.name, o.app)
	_, _ = fmt.Fprintf(w, "Writes to %s after that moment stay only in %s, which keeps every write and is no longer bound to %s.\n",
		o.database, o.database, o.app)
	_, _ = fmt.Fprintf(w, "%s stops serving while it switches (usually under a minute, then a cold start).\n", o.app)
	_, _ = fmt.Fprintln(w, "Pass --yes to skip this prompt.")
}
