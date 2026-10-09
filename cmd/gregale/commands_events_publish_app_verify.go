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
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	out, err := client.VerifyAppEventPublication(ctx, pos[0], req)
	if err != nil {
		return printErr("Content verification read failed", err)
	}
	if code := jsonOut(writeJSON(out)); code != 0 {
		return code
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
