package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/productcap"
)

const capabilitiesUsage = "usage: gregale capabilities [--json]"

// cmdCapabilities prints the account's canonical feature maturity and plan
// availability matrix. The same endpoint powers future dashboard/docs
// projections, so scripts can discover entitlement without trial requests.
// Logged out, the human view shows the public catalog bundled with the CLI;
// --json stays account-scoped so scripts never mistake it for entitlement.
func cmdCapabilities(args []string) int {
	if len(args) != 0 {
		PrintUsage(os.Stderr, capabilitiesUsage, "capabilities")
		return 1
	}
	if !jsonOutput && loadTokenWithDiagnostics(io.Discard) == "" {
		return printPublicCapabilities()
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	capabilities, err := client.GetCapabilities(context.Background())
	if err != nil {
		return printErr("Could not read capabilities", err)
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
		} else if capability.UnavailableDetail != "" {
			status += ": " + capability.UnavailableDetail
		}
		_, _ = fmt.Fprintf(osStdout, "%-25s %-10s %-21s %s\n", capability.Key, capability.Maturity, strings.Join(capability.Plans, ","), status)
	}
	return 0
}

// printPublicCapabilities renders the customer capabilities of the catalog
// embedded in this CLI. It carries no account state, so it names plans
// instead of availability and points at login for the account view.
func printPublicCapabilities() int {
	catalog, err := productcap.Load()
	if err != nil {
		return printErr("Could not read the capability catalog", err)
	}
	_, _ = fmt.Fprintln(osStdout, "Public capability catalog bundled with this CLI.")
	_, _ = fmt.Fprintln(osStdout)
	_, _ = fmt.Fprintln(osStdout, "CAPABILITY                MATURITY   PLANS")
	for _, capability := range catalog.Capabilities {
		if capability.Maturity == productcap.MaturityInternal {
			continue
		}
		_, _ = fmt.Fprintf(osStdout, "%-25s %-10s %s\n", capability.ID, capability.Maturity, strings.Join(capability.Plans, ","))
	}
	_, _ = fmt.Fprintf(osStdout, "\nSee what your plan includes: %s login, then %s capabilities\n", recoveryCLI(), recoveryCLI())
	return 0
}
