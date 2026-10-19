package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
)

const realtimeOccupancyUsage = "usage: gregale realtime occupancy APP_SLUG ENDPOINT_ID CHANNEL [CHANNEL...]"

// cmdRealtimeOccupancy shows how many subscribers and presence members each
// channel has across the fleet (ADR-1324).
func cmdRealtimeOccupancy(args []string) int {
	fs := newFlagSet("realtime occupancy", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() < 3 || strings.TrimSpace(fs.Arg(0)) == "" || strings.TrimSpace(fs.Arg(1)) == "" {
		PrintUsage(osStderr, realtimeOccupancyUsage, "realtime")
		return 1
	}
	channels := fs.Args()[2:]
	if len(channels) > api.RealtimeOccupancyChannelsMax {
		return printErr("Too many channels", fmt.Errorf("occupancy reads at most %d channels at once; got %d", api.RealtimeOccupancyChannelsMax, len(channels)))
	}
	for _, channel := range channels {
		if !realtime.ValidateChannel(channel) {
			return printErr("Invalid channel", fmt.Errorf("channel %q must be non-empty, at most 256 bytes, and contain no '/', '?', '#', or whitespace padding", channel))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	response, err := client.GetManagedRealtimeOccupancy(context.Background(), fs.Arg(0), fs.Arg(1), channels)
	if err != nil {
		return printErr("Could not read realtime occupancy", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	if response.Partial {
		PrintWarn(osStderr, fmt.Sprintf("Realtime occupancy is partial: %d node(s) unavailable; counts cover the nodes that answered.", response.NodesUnavailable))
	}
	printRealtimeOccupancy(response)
	return 0
}

func printRealtimeOccupancy(response api.ManagedRealtimeOccupancyResponse) {
	width := len("CHANNEL")
	for _, channel := range response.Channels {
		width = max(width, len(channel.Channel))
	}
	_, _ = fmt.Fprintf(osStdout, "%-*s %12s %17s\n", width, "CHANNEL", "SUBSCRIBERS", "PRESENCE MEMBERS")
	for _, channel := range response.Channels {
		_, _ = fmt.Fprintf(osStdout, "%-*s %12s %17s\n", width, channel.Channel, groupDigits(int64(channel.Subscribers)), groupDigits(int64(channel.PresenceMembers)))
	}
}
