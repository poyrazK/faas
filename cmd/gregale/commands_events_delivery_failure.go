package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsDeliveryFailure(args []string, action string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events "+action, flag.ContinueOnError)
	path := fs.String("request-file", "", "protected JSON file containing token and non-sensitive reason")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || *path == "" || rejectUnexpectedFlagArgs(fs) {
		PrintUsage(os.Stderr, "usage: gregale events "+action+" <app> <invocation-id> --request-file PATH", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[1]); err != nil {
		return printErr("Invalid invocation ID", err)
	}
	raw, err := readSimulationJSONFile(*path, api.EventAcknowledgmentFailureRequestMaxBytes)
	if err != nil {
		return printErr("Cannot read acknowledgment request", err)
	}
	var req api.EventAcknowledgmentFailureRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return printErr("Invalid acknowledgment request", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventSubscriptionControlTimeout)
	defer cancel()
	var out api.EventAcknowledgmentStatus
	if action == "delivery-retry" {
		out, err = client.RetryEventAcknowledgment(ctx, positional[0], positional[1], req)
	} else {
		out, err = client.DeadLetterEventAcknowledgment(ctx, positional[0], positional[1], req)
	}
	if err != nil {
		return printErr("Disposition unconfirmed; repeat the same request file", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "Delivery %s: %s (%s)\n", out.InvocationID, out.ProcessingState, out.Disposition)
	return 0
}
