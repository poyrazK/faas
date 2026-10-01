package main

import (
	"context"
	"fmt"
	"os"
)

func cmdRegions(args []string) int {
	if len(args) == 1 && args[0] == "list" {
		args = nil
	}
	if len(args) != 0 {
		PrintUsage(os.Stderr, "usage: gregale regions [list] [--json]", "regions")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	regions, err := client.ListComputeRegions(context.Background())
	if err != nil {
		return printErr("Could not list compute regions", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(regions))
	}
	for _, region := range regions {
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\tnew apps=%t\t%d eligible node(s)\thealth=%s\n", region.ID, region.DisplayName, region.Availability, region.AcceptingNewApps, region.EligibleNodes, region.ObservedHealth)
	}
	return 0
}
