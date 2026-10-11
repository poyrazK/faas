package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsPublishAppVerify(args []string) int {
	flags, pos := splitArgsForFlags(args)
	fs := newFlagSet("events publish-app-verify", flag.ContinueOnError)
	path := fs.String("file", "", "original publish JSON with key, type and data")
	expected := fs.String("expected-accepted-at", "", "exact accepted_at from the saved receipt (RFC3339)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 || *path == "" || rejectUnexpectedFlagArgs(fs) {
		return printErr("Invalid arguments", fmt.Errorf("use events publish-app-verify APP --file PATH"))
	}
	req, err := readAppPublishEventRequest(*path)
	if err != nil {
		return printErr("Invalid event", err)
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
	out, err := client.VerifyAppEventPublication(ctx, pos[0], req, guard)
	if err != nil {
		return printErr("Content verification read failed", err)
	}
	if code := jsonOut(writeJSON(out)); code != 0 {
		return code
	}
	if out.Acceptance == "replacement_acceptance" {
		return 3
	}
	switch out.Status {
	case "match":
		return 0
	case "unavailable":
		return 2
	default:
		return 1
	}
}
