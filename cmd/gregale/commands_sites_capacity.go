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

func hostingPreviewReservationsWorkstationEnabled() bool {
	v := os.Getenv("FAAS_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED")
	return v == "1" || v == "true"
}
func cmdSitesCapacity(args []string) int {
	if !hostingPreviewReservationsWorkstationEnabled() {
		return printErr("Preview capacity is disabled", errors.New("set FAAS_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED=1; the API opt-in is separate"))
	}
	flags := newFlagSet("sites-capacity", flag.ContinueOnError)
	site := flags.String("site", "", "owned site UUID (required)")
	if err := flags.Parse(args); err != nil || rejectUnexpectedFlagArgs(flags) {
		return 1
	}
	if !hosting.ValidCIRunID(*site) {
		return printErr("Invalid capacity request", errors.New("provide a canonical site UUID"))
	}
	client, err := hostingDiagnosticClient(*site, "")
	if err != nil {
		return printErr("Operator connection unavailable", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, api.HostingPreviewCapacityTimeout)
	defer cancel()
	out, err := client.GetHostingPreviewCapacity(ctx, *site)
	if err != nil {
		return printErr("Preview capacity unavailable", err)
	}
	if out.SiteID != *site || out.Limit != api.HostingPreviewsPerSite || out.Previews < 0 || out.Reservations < 0 || out.Previews+out.Reservations > out.Limit || out.Available != out.Limit-out.Previews-out.Reservations || out.ObservedAt.IsZero() {
		return printErr("Invalid capacity response", errors.New("capacity evidence does not match the requested site and limits"))
	}
	return jsonOut(writeJSON(out))
}
