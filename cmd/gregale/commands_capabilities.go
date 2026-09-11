package main

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const capabilitiesUsage = "usage: gregale capabilities [--json]"

// cmdCapabilities prints the account's canonical feature maturity and plan
// availability matrix. The same endpoint powers future dashboard/docs
// projections, so scripts can discover entitlement without trial requests.
func cmdCapabilities(args []string) int {
	if len(args) != 0 {
		PrintUsage(os.Stderr, capabilitiesUsage, "capabilities")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	capabilities, err := client.GetCapabilities(context.Background())
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(capabilities))
	}

	_, _ = fmt.Fprintf(osStdout, "Plan: %s\nRegistry: %d\n\n", capabilities.Plan, capabilities.RegistryVersion)
	_, _ = fmt.Fprintln(osStdout, "CAPABILITY                MATURITY   PLANS                 STATUS")
	for _, capability := range capabilities.Capabilities {
		status := "unavailable"
		if capability.Enabled {
			status = "available"
		}
		_, _ = fmt.Fprintf(osStdout, "%-25s %-10s %-21s %s\n", capability.Key, capability.Maturity, strings.Join(capability.Plans, ","), status)
	}
	return 0
}
