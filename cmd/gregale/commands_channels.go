package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const channelsUsage = "usage: gregale channels <list|add|test|rm>"

// cmdChannels implements `gregale channels` (ADR-749): account-level alert
// destinations in Slack, PagerDuty and email.
func cmdChannels(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, channelsUsage, "channels")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdChannelsList(args[1:])
	case subAdd:
		return cmdChannelsAdd(args[1:])
	case "test":
		return cmdChannelsTest(args[1:])
	case subRm:
		return cmdChannelsRm(args[1:])
	}
	printCommandValidation(os.Stderr, "unknown channels subcommand %q\n", args[0])
	PrintUsage(os.Stderr, channelsUsage, "channels")
	return 1
}

func cmdChannelsList(args []string) int {
	fs := newFlagSet("channels list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale channels list", "channels")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	chans, err := client.ListNotificationChannels(context.Background())
	if err != nil {
		return printErr("Could not list notification channels", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(chans))
	}
	renderChannels(osStdout, chans)
	return 0
}

func renderChannels(w io.Writer, chans []api.NotificationChannelResponse) {
	if len(chans) == 0 {
		_, _ = fmt.Fprintln(w, "No notification channels. Add one with `gregale channels add`.")
		return
	}
	_, _ = fmt.Fprintf(w, "%-36s  %-20s  %-9s  %-34s  %s\n", "ID", "NAME", "KIND", "TARGET", "LAST DELIVERY")
	for _, c := range chans {
		last := "never"
		switch {
		case c.LastErrorAt != "" && c.LastErrorAt >= c.LastDeliveredAt:
			last = "FAILED " + c.LastErrorAt + ": " + c.LastError
		case c.LastDeliveredAt != "":
			last = "ok " + c.LastDeliveredAt
		}
		_, _ = fmt.Fprintf(w, "%-36s  %-20s  %-9s  %-34s  %s\n", c.ID, c.Name, c.Kind, c.Target, last)
	}
}

func cmdChannelsAdd(args []string) int {
	fs := newFlagSet("channels add", flag.ContinueOnError)
	name := fs.String("name", "", "channel name (required)")
	kind := fs.String("kind", "", "slack, pagerduty or email (required)")
	stdin := fs.Bool("secret-stdin", false, "read the Slack webhook URL or PagerDuty routing key from stdin")
	region := fs.String("pagerduty-region", "", "PagerDuty service region: us (default) or eu")
	email := fs.String("email", "", "your account email (kind email)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *name == "" || *kind == "" || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale channels add --name <name> --kind slack|pagerduty|email [--secret-stdin] [--pagerduty-region us|eu] [--email you@example.com]", "channels")
		return 1
	}
	req := api.CreateNotificationChannelRequest{Name: *name, Kind: *kind, PagerDutyRegion: *region, Email: *email}
	if *kind == "slack" || *kind == "pagerduty" {
		if !*stdin {
			return printErr("Missing destination", fmt.Errorf("pipe the Slack webhook URL or PagerDuty routing key with --secret-stdin so it stays out of your shell history"))
		}
		secret, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && err != io.EOF {
			return printErr("Could not read destination", err)
		}
		if *kind == "slack" {
			req.SlackWebhookURL = strings.TrimSpace(secret)
		} else {
			req.PagerDutyRoutingKey = strings.TrimSpace(secret)
		}
	}
	if _, prob := api.NormalizeNotificationChannelRequest(req); prob != nil {
		return printErr("Invalid channel", fmt.Errorf("%s", prob.Detail))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ch, err := client.CreateNotificationChannel(context.Background(), req)
	if err != nil {
		return printErr("Could not add notification channel", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(ch))
	}
	_, _ = fmt.Fprintf(osStdout, "Added %s channel %s (%s) → %s\nSend a test with: gregale channels test %s\n", ch.Kind, ch.Name, ch.ID, ch.Target, ch.ID)
	return 0
}

func cmdChannelsTest(args []string) int {
	fs := newFlagSet("channels test", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale channels test <channel-id>", "channels")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	res, err := client.TestNotificationChannel(context.Background(), fs.Arg(0))
	if err != nil {
		return printErr("Could not send test notification", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(res))
	}
	if !res.Delivered {
		return printErr("Test notification was not delivered", fmt.Errorf("%s", res.Error))
	}
	_, _ = fmt.Fprintln(osStdout, "Test notification delivered.")
	return 0
}

func cmdChannelsRm(args []string) int {
	fs := newFlagSet("channels rm", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale channels rm <channel-id>", "channels")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteNotificationChannel(context.Background(), fs.Arg(0)); err != nil {
		return printErr("Could not delete notification channel", err)
	}
	_, _ = fmt.Fprintf(osStdout, "Deleted channel %s\n", fs.Arg(0))
	return 0
}
