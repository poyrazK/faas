package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"os"
	"strings"
)

func cmdEventsSubscriptionVersions(args []string, action string) int {
	flags, positional := splitArgsForFlags(args, "yes")
	fs := newFlagSet("events subscription-versions-"+action, flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm changing selection for future events")
	var versions *string
	if action == "set" {
		versions = fs.String("versions", "", "comma-separated schema versions")
	}
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 2 || rejectUnexpectedFlagArgs(fs) || action != "status" && !*yes {
		PrintUsage(os.Stderr, "usage: gregale events subscription-versions-"+action+" <app> <subscription-id> [--versions v1,v2] [--yes]", "events")
		return 1
	}
	if _, err := uuid.Parse(positional[1]); err != nil {
		return printErr("Invalid subscription ID", err)
	}
	var selected []string
	if versions != nil {
		if *versions == "" {
			return printErr("Invalid schema versions", fmt.Errorf("--versions is required; use subscription-versions-reset to accept all versions"))
		}
		var err error
		selected, err = api.NormalizeEventSchemaVersions(strings.Split(*versions, ","))
		if err != nil {
			return printErr("Invalid schema versions", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.EventSubscriptionSchemaVersionsResponse
	switch action {
	case "set":
		out, err = client.SetEventSubscriptionSchemaVersions(context.Background(), positional[0], positional[1], selected)
	case "reset":
		out, err = client.ResetEventSubscriptionSchemaVersions(context.Background(), positional[0], positional[1])
	default:
		out, err = client.GetEventSubscriptionSchemaVersions(context.Background(), positional[0], positional[1])
	}
	if err != nil {
		return printErr("Schema version selection failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	selection := "all"
	if len(out.SchemaVersions) > 0 {
		selection = strings.Join(out.SchemaVersions, ",")
	}
	_, _ = fmt.Fprintf(osStdout, "Subscription %s | schema versions %s\n", oneLine(out.SubscriptionID), oneLine(selection))
	return 0
}

func eventSchemaVersionsLabel(versions []string) string {
	if len(versions) == 0 {
		return "all"
	}
	return strings.Join(versions, ",")
}
