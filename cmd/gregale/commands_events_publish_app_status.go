package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsPublishAppStatus(args []string) int {
	flags, pos := splitArgsForFlags(args)
	fs := newFlagSet("events publish-app-status", flag.ContinueOnError)
	key := fs.String("key", "", "original exact producer key")
	after := fs.String("after", "", "next recipient page cursor")
	limit := fs.Int("limit", api.AppEventPublishStatusRecipientsDefault, "recipients per page (1..200)")
	expected := fs.String("expected-accepted-at", "", "exact accepted_at from the saved receipt (RFC3339)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 || rejectUnexpectedFlagArgs(fs) || *limit < 1 {
		return printErr("Invalid arguments", fmt.Errorf("use events publish-app-status APP --key KEY [--after CURSOR] [--limit N]"))
	}
	query := api.AppEventPublishStatusQuery{Key: *key, After: *after, Limit: *limit}
	if err := query.Normalize(); err != nil {
		return printErr("Invalid status query", err)
	}
	guard, err := api.ParseAppEventAcceptanceGuard(*expected)
	if err != nil {
		return printErr("Invalid acceptance guard", err)
	}
	guardSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "expected-accepted-at" {
			guardSet = true
		}
	})
	if guardSet && *expected == "" {
		return printErr("Invalid acceptance guard", fmt.Errorf("expected-accepted-at must not be empty"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	query.ExpectedAcceptedAt = guard.ExpectedAcceptedAt
	out, err := client.GetAppEventPublishStatus(ctx, pos[0], query)
	if err != nil {
		return printErr("Publish reconciliation read failed", err)
	}
	if code := jsonOut(writeJSON(out)); code != 0 {
		return code
	}
	if out.Acceptance == "replacement_acceptance" {
		return 3
	}
	if out.Status == "unavailable" {
		return 2
	}
	return 0
}
