package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresBackupSchedule(args []string) int {
	const command = "postgres backup-schedule"
	fs := newFlagSet(command, flag.ContinueOnError)
	at := fs.String("daily-at", "", "daily UTC time in HH:MM format")
	app := fs.String("bucket-app", "", "app that owns the destination bucket")
	bucket := fs.String("bucket", "", "private destination bucket name or ID")
	off := fs.Bool("off", false, "disable the schedule")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	setting := *at != "" || *app != "" || *bucket != ""
	clock, clockErr := time.Parse("15:04", *at)
	if fs.NArg() != 1 || *off && setting ||
		setting && (clockErr != nil || clock.Format("15:04") != *at || !api.ValidAppSlug(*app) || *bucket == "") {
		PrintUsage(os.Stderr, "usage: gregale "+command+" DATABASE [--daily-at HH:MM --bucket-app APP --bucket BUCKET | --off]", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	database, err := postgresConditions.resolveRef(ctx, client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	org := client.ProjectOrganization()
	if *off {
		if err := client.DeleteManagedPostgresBackupSchedule(ctx, org, database); err != nil {
			return printErr("Could not disable PostgreSQL backup schedule", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]string{"state": "disabled"}))
		}
		_, _ = fmt.Fprintln(osStdout, "Backup schedule disabled.")
		return 0
	}
	var schedule api.ManagedPostgresBackupSchedule
	if setting {
		id, err := resolveBucketRef(ctx, client, *app, *bucket)
		if err != nil {
			return printErr("Could not find destination bucket", err)
		}
		schedule, err = client.SetManagedPostgresBackupSchedule(ctx, org, database, api.SetManagedPostgresBackupScheduleRequest{
			DailyAtUTC: *at, BucketApp: *app, BucketID: id,
		})
		if err != nil {
			return printErr("Could not set PostgreSQL backup schedule", err)
		}
	} else {
		schedule, err = client.GetManagedPostgresBackupSchedule(ctx, org, database)
		if err != nil {
			return printErr("Could not read PostgreSQL backup schedule", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(schedule))
	}
	renderPostgresBackupSchedule(schedule)
	return 0
}

func renderPostgresBackupSchedule(schedule api.ManagedPostgresBackupSchedule) {
	_, _ = fmt.Fprintf(osStdout, "State\t%s\nDaily at\t%s UTC\nBucket\t%s\nNext run\t%s\n",
		schedule.State, schedule.DailyAtUTC, schedule.BucketID, schedule.NextRunAt.UTC().Format(time.RFC3339))
	if schedule.LastSuccessAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Last successful backup\t%s\nArchive key\t%s\n", schedule.LastSuccessAt.UTC().Format(time.RFC3339), schedule.LastArchive)
	}
	if schedule.LastError != "" {
		_, _ = fmt.Fprintf(osStdout, "Last error\t%s\n", schedule.LastError)
	}
}
