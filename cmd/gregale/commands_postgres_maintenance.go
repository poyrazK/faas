package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresMaintenance(args []string) int {
	const usage = "usage: gregale [--org ORG] postgres maintenance DATABASE (--day WEEKDAY --start HH:MM --duration DURATION | --off)"
	fs := newFlagSet("postgres maintenance", flag.ContinueOnError)
	day := fs.String("day", "", "UTC weekday")
	start := fs.String("start", "", "UTC start time (HH:MM)")
	duration := fs.Duration("duration", 0, "window length (for example, 1h)")
	off := fs.Bool("off", false, "remove the weekly window")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || (*off && fs.NFlag() != 1) {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	var request api.SetManagedPostgresMaintenanceRequest
	if !*off {
		startTime, err := time.Parse("15:04", *start)
		weekday := -1
		for d := time.Sunday; d <= time.Saturday; d++ {
			if strings.EqualFold(d.String(), *day) {
				weekday = int(d)
				break
			}
		}
		window := api.ManagedPostgresMaintenanceWindow{Weekday: weekday, StartMinute: startTime.Hour()*60 + startTime.Minute(), DurationMinutes: int(*duration / time.Minute)}
		if err != nil || len(*start) != 5 || *duration%time.Minute != 0 || !window.Valid() {
			return printErr("Invalid maintenance window", fmt.Errorf("provide a weekday, a UTC start in HH:MM, and a whole-minute duration between 1m and 24h"))
		}
		request.Window = &window
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := postgresConditions.resolveRef(ctx, client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	result, err := client.SetManagedPostgresMaintenance(ctx, id, request)
	if err != nil {
		return printErr("Could not set the maintenance window", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresMaintenance(osStdout, result)
	return 0
}

func renderPostgresMaintenance(w io.Writer, status api.ManagedPostgresMaintenanceStatus) {
	window := status.Window
	if window == nil {
		_, _ = fmt.Fprintln(w, "  Maintenance window: off")
	} else {
		_, _ = fmt.Fprintf(w, "  Maintenance window: %s %02d:%02d UTC for %s\n",
			time.Weekday(window.Weekday), window.StartMinute/60, window.StartMinute%60, time.Duration(window.DurationMinutes)*time.Minute)
		if status.WindowStart != nil && status.WindowEnd != nil {
			label := "Next window"
			if status.WindowOpen {
				label = "Current window"
			}
			_, _ = fmt.Fprintf(w, "  %s: %s to %s\n", label, status.WindowStart.Format(time.RFC3339), status.WindowEnd.Format(time.RFC3339))
		}
		if status.StaleSince != nil {
			_, _ = fmt.Fprintf(w, "  Base refresh pending since %s\n", status.StaleSince.Format(time.RFC3339))
		}
	}
	if r := status.LastRefresh; r != nil {
		if r.Phase != "" && r.Outcome != "succeeded" {
			_, _ = fmt.Fprintf(w, "  Rolling refresh phase: %s\n", strings.ReplaceAll(r.Phase, "_", " "))
		}
		if r.Reason != "" {
			_, _ = fmt.Fprintf(w, "  Refresh detail: %s\n", strings.ReplaceAll(r.Reason, "_", " "))
		}
		_, _ = fmt.Fprintf(w, "  Last base refresh: %s (started %s)\n", strings.ReplaceAll(r.Outcome, "_", " "), r.StartedAt.Format(time.RFC3339))
	}
}
