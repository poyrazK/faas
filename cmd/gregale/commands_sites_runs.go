package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/hosting/activity"
)

func cmdSitesRuns(args []string) int {
	enabled := os.Getenv("FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED")
	if enabled != "true" && enabled != "1" {
		return printErr("Deployment activity is disabled", errors.New("set FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED=1; the API opt-in is separate"))
	}
	flags := newFlagSet("sites-runs", flag.ContinueOnError)
	site := flags.String("site", "", "owned site UUID (required)")
	before := flags.String("before", "", "exclusive next_cursor returned by the previous page")
	if err := flags.Parse(args); err != nil || rejectUnexpectedFlagArgs(flags) {
		return 1
	}
	if !hosting.ValidCIRunID(*site) {
		return printErr("Invalid activity request", errors.New("provide a canonical site UUID"))
	}
	if _, _, err := activity.ParseCursor(*before); err != nil {
		return printErr("Invalid activity request", err)
	}
	// Operator authentication only; the CI bearer endpoint allowlist stays closed.
	client, err := hostingDiagnosticClient(*site, "")
	if err != nil {
		return printErr("Operator connection unavailable", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, api.HostingActivityTimeout)
	defer cancel()
	page, err := client.ListHostingActivity(ctx, *site, *before)
	if err != nil {
		return printErr("Deployment activity unavailable", err)
	}
	if page.SiteID != *site || page.ServingHealth != "not_checked" || len(page.Entries) > api.HostingActivityPageSize {
		return printErr("Invalid activity response", errors.New("site or evidence does not match the activity request"))
	}
	if _, _, err = activity.ParseCursor(page.NextCursor); err != nil || page.NextCursor != "" && page.NextCursor == *before {
		return printErr("Invalid activity response", errors.New("invalid or non-advancing pagination cursor"))
	}
	return jsonOut(writeJSON(page))
}
