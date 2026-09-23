package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdSend implements the application-inbox shorthand:
// `gregale send <target-app> --type TYPE --data JSON`.
func cmdSend(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("send", flag.ContinueOnError)
	typ := fs.String("type", "", "event type (required)")
	data := fs.String("data", "", "JSON event data (inline | @file | -; required)")
	id := fs.String("id", "", "stable event id (generated when omitted)")
	source := fs.String("source", "", "event source (defaults to gregale.send)")
	eventTime := fs.String("time", "", "event time (RFC3339; defaults to server time)")
	queueName := fs.String("queue-name", "", "target logical queue name")
	idempotencyKey := fs.String("idempotency-key", "", "stable key for retrying an uncertain send")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || rejectUnexpectedFlagArgs(fs) || strings.TrimSpace(*typ) == "" || strings.TrimSpace(*data) == "" {
		PrintUsage(os.Stderr, "usage: gregale send <target-app> --type TYPE --data <json|@file|-> [--source SOURCE] [--id ID] [--time RFC3339] [--queue-name QUEUE] [--idempotency-key KEY]", "send")
		return 1
	}
	if err := validateDeployIdempotencyKey(*idempotencyKey); err != nil {
		return printErr("Invalid --idempotency-key", err)
	}
	body, err := resolvePayload(*data)
	if err != nil {
		return printErr("Invalid --data", err)
	}
	if len(body) == 0 || !json.Valid(body) {
		return printErr("Invalid --data", fmt.Errorf("must be a non-empty JSON value"))
	}
	var occurredAt *time.Time
	if strings.TrimSpace(*eventTime) != "" {
		parsed, parseErr := time.Parse(time.RFC3339, *eventTime)
		if parseErr != nil {
			return printErr("Invalid --time", fmt.Errorf("must be RFC3339: %w", parseErr))
		}
		occurredAt = &parsed
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if key := strings.TrimSpace(*idempotencyKey); key != "" {
		ctx = api.ContextWithIdempotencyKey(ctx, key)
	}
	resp, err := client.SendAppMessage(ctx, positional[0], api.SendAppMessageRequest{
		ID:        strings.TrimSpace(*id),
		Source:    strings.TrimSpace(*source),
		Type:      strings.TrimSpace(*typ),
		Time:      occurredAt,
		Data:      json.RawMessage(body),
		QueueName: strings.TrimSpace(*queueName),
	})
	if err != nil {
		return printErr("Application send failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Message %s queued for %s (event %s).", resp.ID, resp.TargetApp, resp.EventID)
	return 0
}

// cmdDeliver implements the application-outbox shorthand. Destination is a
// webhook id or exact URL already registered on the source app.
func cmdDeliver(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("deliver", flag.ContinueOnError)
	typ := fs.String("type", "", "event type (required)")
	data := fs.String("data", "", "JSON event data (inline | @file | -; required)")
	idempotencyKey := fs.String("idempotency-key", "", "stable key for retrying an uncertain delivery")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) || strings.TrimSpace(*typ) == "" || strings.TrimSpace(*data) == "" {
		PrintUsage(os.Stderr, "usage: gregale deliver <source-app> <webhook-id|url> --type TYPE --data <json|@file|-> [--idempotency-key KEY]", "deliver")
		return 1
	}
	if err := validateDeployIdempotencyKey(*idempotencyKey); err != nil {
		return printErr("Invalid --idempotency-key", err)
	}
	body, err := resolvePayload(*data)
	if err != nil {
		return printErr("Invalid --data", err)
	}
	if len(body) == 0 || !json.Valid(body) {
		return printErr("Invalid --data", fmt.Errorf("must be a non-empty JSON value"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if key := strings.TrimSpace(*idempotencyKey); key != "" {
		ctx = api.ContextWithIdempotencyKey(ctx, key)
	}
	resp, err := client.DeliverAppEvent(ctx, positional[0], api.DeliverAppEventRequest{
		Destination: positional[1],
		Type:        strings.TrimSpace(*typ),
		Data:        json.RawMessage(body),
	})
	if err != nil {
		return printErr("Application delivery failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Delivery %s queued for %s.", resp.ID, resp.Destination)
	return 0
}
