package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
)

const appCostsUsage = "usage: gregale app <slug> costs [--month YYYY-MM] [--json]"

func cmdAppCosts(slug string, args []string) int {
	if hasHelpFlag(args) {
		PrintUsage(osStdout, appCostsUsage, "apps")
		return 0
	}
	fs := newFlagSet("app costs", flag.ContinueOnError)
	month := fs.String("month", "", "UTC usage month (YYYY-MM; default current)")
	asJSON := fs.Bool("json", false, "print the machine-readable cost report")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 || !validCLISlug(slug) {
		return printErr("Invalid app cost arguments", fmt.Errorf("%s", appCostsUsage))
	}
	if *month != "" {
		parsed, err := time.Parse("2006-01", *month)
		if err != nil || parsed.IsZero() || parsed.After(time.Now()) {
			return printErr("Invalid cost month", errors.New("expected a current or historical month in YYYY-MM format"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	app, err := client.GetApp(context.Background(), slug)
	if err != nil {
		return printErr("Could not read app", err)
	}
	report, err := client.GetFinancialCosts(context.Background(), *month)
	if err != nil {
		return printErr("Could not read app costs", err)
	}
	response, err := billing.ScopeFinancialCostsToApp(report, app.ID, app.Slug)
	if err != nil {
		return printErr("Could not prepare app cost report", err)
	}
	if *asJSON || jsonOutput {
		if err := json.NewEncoder(osStdout).Encode(response); err != nil {
			return printErr("Could not print app cost report", err)
		}
		return 0
	}
	printFinancialAppCosts(osStdout, response)
	return 0
}

func printFinancialAppCosts(w io.Writer, response api.FinancialAppCostsResponse) {
	_, _ = fmt.Fprintf(w, "App: %s\nUsage period: %s to %s (UTC)\nKnown attributed usage: %s\nAs of: %s\n",
		response.AppSlug, response.PeriodStart.Format("2006-01-02"), response.PeriodEnd.Format("2006-01-02"), financialMoney(response.KnownUsageMillicents), response.AsOf.Format(time.RFC3339))
	_, _ = fmt.Fprintf(w, "%s\n", response.ScopeDescription)
	for _, meter := range response.Meters {
		_, _ = fmt.Fprintf(w, "\n%s: %s (%s)\n", meter.Meter, financialMoney(meter.NetMillicents), billing.FormatFinancialQuantity(meter.Quantity, meter.Unit))
		if len(meter.Allocations) != 0 {
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "USAGE DRIVER\tQUANTITY\tNET COST")
			for _, allocation := range meter.Allocations {
				name := allocation.Attribution.Name
				if name == "" {
					name = allocation.Attribution.DeploymentID
				}
				if name == "" {
					name = "application workload"
				}
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", name, billing.FormatFinancialQuantity(allocation.Quantity, allocation.Unit), financialMoney(allocation.NetMillicents))
			}
			_ = tw.Flush()
		}
		coverage := "complete"
		if !meter.Coverage.Complete {
			coverage = "partial"
		}
		if !meter.Coverage.Fresh {
			coverage += ", stale"
		} else {
			coverage += ", fresh"
		}
		_, _ = fmt.Fprintf(w, "Coverage: %s (%d of %d minutes)", coverage, meter.Coverage.CompleteMinutes, meter.Coverage.ExpectedMinutes)
		if len(meter.Coverage.Reasons) != 0 {
			_, _ = fmt.Fprintf(w, " — %s", strings.Join(meter.Coverage.Reasons, ", "))
		}
		_, _ = fmt.Fprintln(w)
	}
	_, _ = fmt.Fprintf(w, "\nFull bill estimate unavailable: %s\n", strings.Join(response.MissingBillComponents, ", "))
}
