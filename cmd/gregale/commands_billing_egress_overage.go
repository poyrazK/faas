// commands_billing_egress_overage.go — `gregale billing egress-overage`
// (ADR-20261104204500).
//
//	gregale billing egress-overage [--org SLUG]          show the election
//	gregale billing egress-overage on|off [--org SLUG]   turn it on or off
//
// Without the election every plan stops at its monthly egress allowance;
// with it, egress above the allowance is billed while egress billing is
// live. Changing it needs an admin-scoped key or a session with a recent
// step-up.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const billingEgressOverageUsage = "usage: gregale billing egress-overage [on|off] [--org SLUG]"

func cmdBillingEgressOverage(args []string) int {
	fs := newFlagSet("billing egress-overage", flag.ContinueOnError)
	org := fs.String("org", "", "use the election of an organization that owns its resources")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	var enabled *bool
	switch {
	case fs.NArg() == 0:
	case fs.NArg() == 1 && (fs.Arg(0) == "on" || fs.Arg(0) == "off"):
		on := fs.Arg(0) == "on"
		enabled = &on
	default:
		PrintUsage(os.Stderr, billingEgressOverageUsage, "billing")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var out api.EgressOverageResponse
	switch {
	case enabled == nil && *org == "":
		out, err = client.GetEgressOverage(ctx)
	case enabled == nil:
		out, err = client.GetOrgEgressOverage(ctx, *org)
	case *org == "":
		out, err = client.SetEgressOverage(ctx, *enabled)
	default:
		out, err = client.SetOrgEgressOverage(ctx, *org, *enabled)
	}
	if err != nil {
		return printErr("Egress overage failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	printEgressOverage(out)
	return 0
}

func printEgressOverage(out api.EgressOverageResponse) {
	switch {
	case !out.Electable:
		_, _ = fmt.Fprintf(osStdout, "Egress overage: not available on this plan. Apps stop at the %d GiB monthly egress allowance.\n", out.IncludedGiB)
	case !out.Enabled:
		_, _ = fmt.Fprintf(osStdout, "Egress overage: off. Apps stop at the %d GiB monthly egress allowance. Turn it on with: gregale billing egress-overage on\n", out.IncludedGiB)
	case out.Billing != api.EgressOverageBillingLive:
		_, _ = fmt.Fprintf(osStdout, "Egress overage: on, but egress is not billed yet; apps still stop at the %d GiB allowance until it is.\n", out.IncludedGiB)
	default:
		_, _ = fmt.Fprintf(osStdout, "Egress overage: on. Egress above the %d GiB monthly allowance is billed at %s per GiB, within your spend cap.\n",
			out.IncludedGiB, formatMillicents(out.Currency, out.PriceMillicentsPerGiB))
	}
}
