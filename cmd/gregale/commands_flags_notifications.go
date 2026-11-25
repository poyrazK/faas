package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdFlagsNotifications reads and sets the notification channels an
// environment's experiments and flag lifecycle notify (ADR-20261125085913).
func cmdFlagsNotifications(args []string) int {
	if len(args) == 0 || (args[0] != "get" && args[0] != "set" && args[0] != "clear") {
		PrintUsage(os.Stderr, "gregale "+cliTreeSpellPath("flags notifications")+" <get|set|clear> --project SLUG [--environment ENV] [--channel ID ...]", "flags")
		return 1
	}
	verb := args[0]
	fs := newFlagSet("flags-notifications-"+verb, flag.ContinueOnError)
	project := fs.String("project", "", "project slug")
	environment := fs.String("environment", "production", "named project environment")
	var channels repeatedFlag
	fs.Var(&channels, "channel", "set: notification channel ID (repeatable; gregale alert channel list)")
	positionals, err := parseInterleaved(fs, args[1:])
	if err != nil {
		return 1
	}
	if *project == "" || len(positionals) != 0 || (verb == "set" && len(channels) == 0) {
		needs := "--project"
		if verb == "set" {
			needs += " and at least one --channel"
		}
		PrintUsage(os.Stderr, fmt.Sprintf("gregale %s needs %s", cliTreeSpellPath("flags notifications "+verb), needs), "flags")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var out api.FlagNotificationSettings
	switch verb {
	case "get":
		out, err = client.ProjectFlagNotifications(ctx, *project, *environment)
	case "set":
		out, err = client.SetProjectFlagNotifications(ctx, *project, *environment, channels)
	case "clear":
		out, err = client.SetProjectFlagNotifications(ctx, *project, *environment, nil)
	}
	if err != nil {
		return printErr("Flag notification operation failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if len(out.ChannelIDs) == 0 {
		_, _ = fmt.Fprintf(osStdout, "Flag notifications are off for %s/%s. Turn them on with gregale %s --channel ID --project %s --environment %s\n",
			*project, *environment, cliTreeSpellPath("flags notifications set"), *project, *environment)
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "Experiment decisions and problems, a weekly digest of flags that need a decision and flag expiries in %s/%s go to: %s\n",
		*project, *environment, strings.Join(out.ChannelIDs, ", "))
	return 0
}
