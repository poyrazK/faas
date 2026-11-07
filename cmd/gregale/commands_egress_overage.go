// commands_egress_overage.go — `gregale billing egress-overage [on|off]`
// (ADR-20261103131742 amendment 2). With egress overage off, the default,
// apps stop at the plan's monthly egress; with it on, egress past it is
// billed per GiB under the spend cap.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const egressOverageUsage = "usage: gregale billing egress-overage [--org <slug>] [on|off]"

func cmdEgressOverage(args []string) int {
	fs := newFlagSet("egress-overage", flag.ContinueOnError)
	org := fs.String("org", "", "act for this organization")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	set := fs.NArg() > 0
	if set && (fs.NArg() != 1 || (fs.Arg(0) != "on" && fs.Arg(0) != "off")) {
		PrintUsage(os.Stderr, egressOverageUsage, "egress-overage")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var out api.EgressOverageResponse
	switch {
	case !set && *org == "":
		out, err = client.GetEgressOverage(ctx)
	case !set:
		out, err = client.GetOrgEgressOverage(ctx, *org)
	case *org == "":
		out, err = client.SetEgressOverage(ctx, fs.Arg(0) == "on")
	default:
		out, err = client.SetOrgEgressOverage(ctx, *org, fs.Arg(0) == "on")
	}
	if err != nil {
		return printErr("Egress overage failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintln(osStdout, egressOverageSummary(out))
	return 0
}

// egressOverageSummary says what happens past the plan's monthly egress.
func egressOverageSummary(out api.EgressOverageResponse) string {
	price := api.FormatBillingCents(out.MillicentsPerGiB / api.MillicentsPerCent)
	switch {
	case out.Enabled && out.Billed:
		return fmt.Sprintf("Egress overage: on. Past the %d GiB included each month, egress costs %s per GiB, up to your spend cap.", out.IncludedGiB, price)
	case out.Enabled:
		return fmt.Sprintf("Egress overage: on. Egress isn't billed yet, so apps still stop at the %d GiB included each month.", out.IncludedGiB)
	default:
		return fmt.Sprintf("Egress overage: off. Apps stop at the %d GiB of egress included each month until the month ends. To keep serving at %s per GiB: gregale billing egress-overage on", out.IncludedGiB, price)
	}
}
