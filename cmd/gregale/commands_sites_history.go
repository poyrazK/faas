package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"strings"

	"github.com/google/uuid"
)

func hostingHistoryWorkstationEnabled() bool {
	v := strings.ToLower(os.Getenv("FAAS_FRONTEND_HOSTING_HISTORY_ENABLED"))
	return v == "1" || v == "true"
}
func cmdSitesHistory(args []string) int {
	if !hostingHistoryWorkstationEnabled() {
		return printErr("Release history is disabled", errors.New("set FAAS_FRONTEND_HOSTING_HISTORY_ENABLED=1; the API also requires its independent opt-in"))
	}
	flags := newFlagSet("sites-history", flag.ContinueOnError)
	site := flags.String("site", "", "site UUID (required)")
	before := flags.Int64("before-revision", 0, "exclusive pagination cursor from the previous page")
	if err := flags.Parse(args); err != nil || rejectUnexpectedFlagArgs(flags) {
		return 1
	}
	if uuid.Validate(*site) != nil || *before < 0 || *before > (1<<53)-1 {
		return printErr("Invalid history request", errors.New("site UUID and a valid nonnegative before-revision are required"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	page, err := client.ListHostingHistory(context.Background(), *site, *before)
	if err != nil {
		return printErr("Release history unavailable", err)
	}
	return jsonOut(writeJSON(page))
}
