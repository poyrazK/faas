package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting"
)

func cmdSitesCancel(args []string) int {
	enabled := os.Getenv("FAAS_FRONTEND_HOSTING_CANCELLATION_ENABLED")
	if enabled != "1" && enabled != "true" {
		return printErr("CI cancellation is disabled", errors.New("set FAAS_FRONTEND_HOSTING_CANCELLATION_ENABLED=1; API opt-in is separate"))
	}
	flags := newFlagSet("sites-cancel", flag.ContinueOnError)
	site := flags.String("site", "", "owned site UUID (required)")
	run := flags.String("run-id", "", "exact CI run UUID to permanently cancel (required)")
	if err := flags.Parse(args); err != nil || rejectUnexpectedFlagArgs(flags) {
		return 1
	}
	if !hosting.ValidCIRunID(*site) || !hosting.ValidCIRunID(*run) {
		return printErr("Invalid cancellation request", errors.New("provide canonical site and exact run UUIDs"))
	}
	client, err := hostingDiagnosticClient(*site, "")
	if err != nil {
		return printErr("Operator connection unavailable", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, api.HostingCancellationTimeout)
	defer cancel()
	out, err := client.CancelHostingCIRun(ctx, *site, *run)
	if err != nil {
		return printErr("Cancellation uncertain or rejected; inspect this exact run before retrying cancellation", err)
	}
	if out.ID != *run || out.SiteID != *site || !out.Canceled || !hosting.ValidCIRunID(out.ReleaseID) || !hosting.ValidCIRunID(out.PreviewID) {
		return printErr("Invalid cancellation acknowledgment; inspect this exact run", errors.New("canceled intent does not match the request"))
	}
	return jsonOut(writeJSON(out))
}
