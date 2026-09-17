package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

// cmdDLQ is the app-scoped operator surface for the unified dead-letter
// ledger. Queue- and trigger-specific commands remain available for legacy
// workflows; this command is the single place to inspect, replay, or purge
// either source kind.
func cmdDLQ(args []string) int {
	parent, _ := lookupCliCommand("dlq")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale dlq <list|inspect|replay|purge>", "dlq")
		return 1
	}
	if args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stdout, "usage: gregale dlq <list|inspect|replay|purge>", "dlq")
		return 0
	}
	switch args[0] {
	case "list", "ls":
		return cmdDLQList(args[1:])
	case "inspect", "get", "info":
		return cmdDLQInspect(args[1:])
	case "replay":
		return cmdDLQReplay(args[1:])
	case "purge", "rm", "delete":
		return cmdDLQPurge(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregale dlq: unknown subcommand %q\n", args[0])
		sug, _ := suggestSubcommand(args[0], parent)
		maybeSuggestSub(sug)
		return 1
	}
}

func dlqFlagSet(name, usage string) *flag.FlagSet {
	fs := newFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() {
		if jsonOutput && !jsonUsageHelp {
			return
		}
		PrintUsage(os.Stderr, usage, "dlq")
		fs.PrintDefaults()
	}
	return fs
}

func cmdDLQList(args []string) int {
	flags, pos := splitArgsForFlags(args)
	usage := "usage: gregale dlq list <app> [--limit N] [--before ID]"
	fs := dlqFlagSet("dlq list", usage)
	limit := fs.Int("limit", 50, "max events (1..200)")
	before := fs.String("before", "", "pagination cursor (next_before from a prior page)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		PrintUsage(os.Stderr, usage, "dlq")
		return 1
	}
	if err := validateCLILimit("limit", *limit, 200); err != nil {
		PrintUsage(os.Stderr, usage, "dlq")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListDeadLetterEvents(context.Background(), pos[0], *limit, *before)
	if err != nil {
		return printErr("Could not list dead-letter events", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Events) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no dead-letter events)")
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "ID\tSOURCE\tORIGIN\tRETRIES\tFAILED AT\tREPLAYED")
	for _, event := range resp.Events {
		replayed := "-"
		if event.ReplayedAt != nil {
			replayed = event.ReplayedAt.Format("2006-01-02T15:04:05Z07:00")
		}
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%d\t%s\t%s\n", event.ID, event.Source, event.Origin, event.RetryCount, event.LastFailedAt.Format("2006-01-02T15:04:05Z07:00"), replayed)
	}
	if resp.NextBefore != "" {
		_, _ = fmt.Fprintf(osStdout, "... more — pass --before %s\n", resp.NextBefore)
	}
	return 0
}

func cmdDLQInspect(args []string) int {
	flags, pos := splitArgsForFlags(args)
	usage := "usage: gregale dlq inspect <app> <event-id>"
	fs := dlqFlagSet("dlq inspect", usage)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 2 {
		PrintUsage(os.Stderr, usage, "dlq")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	event, err := client.GetDeadLetterEvent(context.Background(), pos[0], pos[1])
	if err != nil {
		return printErr("Could not inspect dead-letter event", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(event))
	}
	_, _ = fmt.Fprintf(osStdout, "id:          %s\nsource:      %s\nsource_id:   %s\norigin:      %s\nerror_kind:  %s\nretry_count: %d\nfailed_at:   %s\n", event.ID, event.Source, event.SourceID, event.Origin, event.ErrorKind, event.RetryCount, event.LastFailedAt.Format(time.RFC3339))
	if event.ReplayedAt != nil {
		_, _ = fmt.Fprintf(osStdout, "replayed_at: %s\n", event.ReplayedAt.Format(time.RFC3339))
	}
	_, _ = fmt.Fprintf(osStdout, "payload:     %s\nheaders:     %s\nerror_detail: %s\n", event.Payload, event.Headers, event.ErrorDetail)
	return 0
}

func cmdDLQReplay(args []string) int {
	flags, pos := splitArgsForFlags(args, "all")
	usage := "usage: gregale dlq replay <app> [<event-id> | --all] [--limit N]"
	fs := dlqFlagSet("dlq replay", usage)
	all := fs.Bool("all", false, "replay up to --limit pending events")
	limit := fs.Int("limit", 200, "maximum events to replay (1..200)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if *all {
		if len(pos) != 1 {
			PrintUsage(os.Stderr, usage, "dlq")
			return 1
		}
	} else if len(pos) != 2 {
		PrintUsage(os.Stderr, usage, "dlq")
		return 1
	}
	if err := validateCLILimit("limit", *limit, 200); err != nil {
		PrintUsage(os.Stderr, usage, "dlq")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if *all {
		resp, err := client.ReplayAllDeadLetterEvents(ctx, pos[0], *limit)
		if err != nil {
			return printErr("Could not replay dead-letter events", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(resp))
		}
		_, _ = fmt.Fprintf(osStdout, "replayed %d dead-letter events\n", resp.Replayed)
		return 0
	}
	event, err := client.ReplayDeadLetterEvent(ctx, pos[0], pos[1])
	if err != nil {
		return printErr("Could not replay dead-letter event", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(event))
	}
	_, _ = fmt.Fprintf(osStdout, "replayed %s\n", event.ID)
	return 0
}

func cmdDLQPurge(args []string) int {
	flags, pos := splitArgsForFlags(args, "all")
	usage := "usage: gregale dlq purge <app> [<event-id> | --all] [--limit N]"
	fs := dlqFlagSet("dlq purge", usage)
	all := fs.Bool("all", false, "purge all events, repeating until the app is empty")
	limit := fs.Int("limit", 200, "page size while purging all (1..200)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if *all {
		if len(pos) != 1 {
			PrintUsage(os.Stderr, usage, "dlq")
			return 1
		}
	} else if len(pos) != 2 {
		PrintUsage(os.Stderr, usage, "dlq")
		return 1
	}
	if err := validateCLILimit("limit", *limit, 200); err != nil {
		PrintUsage(os.Stderr, usage, "dlq")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if !*all {
		if err := client.DeleteDeadLetterEvent(ctx, pos[0], pos[1]); err != nil {
			return printErr("Could not purge dead-letter event", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"app_slug": pos[0], "purged": 1}))
		}
		_, _ = fmt.Fprintln(osStdout, "purged 1 dead-letter event")
		return 0
	}
	count := 0
	for {
		page, err := client.PurgeDeadLetterEvents(ctx, pos[0], *limit)
		if err != nil {
			return printErr("Could not purge dead-letter events", err)
		}
		if page.Purged == 0 {
			break
		}
		count += page.Purged
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"app_slug": pos[0], "purged": count}))
	}
	_, _ = fmt.Fprintf(osStdout, "purged %d dead-letter events\n", count)
	return 0
}
