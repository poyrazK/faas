package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

const wakeAheadUsage = "usage: gregale wake-ahead <slug> [on|off]"

// cmdWakeAhead reads or changes an app's service wake-ahead opt-in (ADR-956).
func cmdWakeAhead(args []string) int {
	if len(args) < 1 || len(args) > 2 || !validCLISlug(args[0]) {
		return printErr("Invalid wake-ahead command", errors.New(wakeAheadUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.RouteCheckTimeout)
	defer cancel()
	var out api.ServiceWakeAheadResponse
	switch {
	case len(args) == 1:
		out, err = client.GetServiceWakeAhead(ctx, args[0])
	case args[1] == "on" || args[1] == "off":
		out, err = client.SetServiceWakeAhead(ctx, args[0], args[1] == "on")
	default:
		return printErr("Invalid wake-ahead command", errors.New(wakeAheadUsage))
	}
	if err != nil {
		return printErr("Could not reach service wake-ahead", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	renderWakeAhead(osStdout, out)
	return 0
}

func renderWakeAhead(w io.Writer, s api.ServiceWakeAheadResponse) {
	if !s.Enabled {
		_, _ = fmt.Fprintf(w, "Service wake-ahead is off for %s: services it calls wake only when called.\n", s.Slug)
		return
	}
	_, _ = fmt.Fprintf(w, "Service wake-ahead is on for %s: when it wakes, Gregale also starts the services it is measured to call soon after waking.\n", s.Slug)
	_, _ = fmt.Fprintln(w, "Those services run, and are billed, from that moment; an unused one parks again after its idle timeout.")
}
