package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdEvents exposes the customer-facing internal event fabric. Publishing is
// deliberately a single leaf for now: subscriptions are declared in the
// deployment manifest, while this command is the producer-side smoke path.
func cmdEvents(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale events <publish>", "events")
		return 1
	}
	switch args[0] {
	case "publish":
		return cmdEventsPublish(args[1:])
	default:
		PrintUsage(os.Stderr, fmt.Sprintf("unknown events subcommand: %s", args[0]), "events")
		return 1
	}
}

// cmdEventsPublish implements `gregale events publish`. The server owns
// account_id and accepts only JSON event data; --data follows the same
// inline/@file/stdin convention as invoke and queue commands.
func cmdEventsPublish(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events publish", flag.ContinueOnError)
	id := fs.String("id", "", "stable event id (defaults to a generated UUID)")
	source := fs.String("source", "", "event source (or the first positional argument)")
	typ := fs.String("type", "", "event type (or the second positional argument)")
	data := fs.String("data", "", "JSON event data (inline | @file | - for stdin; required)")
	occurredAt := fs.String("time", "", "event time (RFC3339; defaults to server time)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) > 2 {
		PrintUsage(os.Stderr, "usage: gregale events publish [SOURCE TYPE] --data <json|@file|-> [--id ID] [--time RFC3339]", "events")
		return 1
	}
	if len(positional) >= 1 {
		if *source != "" {
			return printErr("Invalid arguments", fmt.Errorf("source provided both positionally and with --source"))
		}
		*source = positional[0]
	}
	if len(positional) == 2 {
		if *typ != "" {
			return printErr("Invalid arguments", fmt.Errorf("type provided both positionally and with --type"))
		}
		*typ = positional[1]
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*source) == "" || strings.TrimSpace(*typ) == "" || strings.TrimSpace(*data) == "" {
		PrintUsage(os.Stderr, "usage: gregale events publish [SOURCE TYPE] --data <json|@file|-> [--id ID] [--time RFC3339]", "events")
		return 1
	}
	eventID := strings.TrimSpace(*id)
	if eventID == "" {
		eventID = uuid.NewString()
	}
	body, err := resolvePayload(*data)
	if err != nil {
		return printErr("Invalid --data", err)
	}
	if len(body) == 0 || !json.Valid(body) {
		return printErr("Invalid --data", fmt.Errorf("must be a non-empty JSON value"))
	}

	var eventTime *time.Time
	if strings.TrimSpace(*occurredAt) != "" {
		parsed, parseErr := time.Parse(time.RFC3339, *occurredAt)
		if parseErr != nil {
			return printErr("Invalid --time", fmt.Errorf("must be RFC3339: %w", parseErr))
		}
		eventTime = &parsed
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.PublishEvent(context.Background(), api.PublishEventRequest{
		ID:     eventID,
		Source: strings.TrimSpace(*source),
		Type:   strings.TrimSpace(*typ),
		Time:   eventTime,
		Data:   json.RawMessage(body),
	})
	if err != nil {
		return printErr("Event publish failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Event %s accepted for account %s.", resp.ID, resp.AccountID)
	return 0
}
