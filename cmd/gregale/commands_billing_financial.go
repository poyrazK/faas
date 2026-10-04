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
)

func cmdBillingFinancial(command string, args []string) int {
	fs := newFlagSet("billing "+command, flag.ContinueOnError)
	month := fs.String("month", "", "UTC usage month (YYYY-MM; default current)")
	asJSON := fs.Bool("json", false, "print the machine-readable response")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 {
		return printErr("Invalid billing arguments", fmt.Errorf("usage: gregale billing %s [--month YYYY-MM] [--json]", command))
	}
	if *month != "" {
		parsed, err := time.Parse("2006-01", *month)
		if err != nil || parsed.IsZero() || parsed.After(time.Now()) {
			return printErr("Invalid billing month", errors.New("expected a current or historical month in YYYY-MM format"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if command == "forecast" {
		response, err := client.GetFinancialForecast(context.Background(), *month)
		if err != nil {
			return printErr("Could not read financial forecast", err)
		}
		if *asJSON {
			if err := json.NewEncoder(osStdout).Encode(response); err != nil {
				return printErr("Could not print financial forecast", err)
			}
			return 0
		}
		printFinancialForecast(osStdout, response)
		return 0
	}
	response, err := client.GetFinancialCosts(context.Background(), *month)
	if err != nil {
		return printErr("Could not read financial costs", err)
	}
	if *asJSON {
		if err := json.NewEncoder(osStdout).Encode(response); err != nil {
			return printErr("Could not print financial costs", err)
		}
		return 0
	}
	printFinancialCosts(osStdout, response)
	return 0
}

func financialMoney(n int64) string {
	return fmt.Sprintf("EUR %d.%05d", n/(100*api.MillicentsPerCent), n%(100*api.MillicentsPerCent))
}

func printFinancialCosts(w io.Writer, response api.FinancialCostsResponse) {
	_, _ = fmt.Fprintf(w, "Usage period: %s to %s (UTC)\nKnown usage: %s\nAs of: %s\n", response.PeriodStart.Format("2006-01-02"), response.PeriodEnd.Format("2006-01-02"), financialMoney(response.KnownUsageMillicents), response.AsOf.Format(time.RFC3339))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "METER\tAPPLICATION / JOB\tQUANTITY\tNET COST")
	for _, meter := range response.Meters {
		for _, contract := range meter.Accrued.Contracts {
			for _, allocation := range contract.Allocations {
				name := allocation.Attribution.Name
				if name == "" {
					name = allocation.Attribution.AppID
					if name == "" {
						name = allocation.Attribution.JobID
					}
					if name == "" {
						name = "unallocated"
					}
				}
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%d %s\t%s\n", meter.Meter, name, allocation.Quantity, contract.Price.Unit, financialMoney(allocation.NetMillicents))
			}
		}
	}
	_ = tw.Flush()
	for _, meter := range response.Meters {
		if !meter.Coverage.Complete {
			_, _ = fmt.Fprintf(w, "%s coverage: %s\n", meter.Meter, strings.Join(meter.Coverage.Reasons, ", "))
		}
	}
	_, _ = fmt.Fprintf(w, "Missing bill components: %s\nInvoice reconciliation: %s\n", strings.Join(response.MissingBillComponents, ", "), response.InvoiceReconciliation)
}

func printFinancialForecast(w io.Writer, response api.FinancialForecastResponse) {
	_, _ = fmt.Fprintf(w, "Usage period: %s to %s (UTC)\n", response.PeriodStart.Format("2006-01-02"), response.PeriodEnd.Format("2006-01-02"))
	for _, meter := range response.Meters {
		f := meter.Forecast
		if f.Available && f.ProjectedNetMillicents != nil {
			_, _ = fmt.Fprintf(w, "%s: %s (%s)\n", meter.Meter, financialMoney(*f.ProjectedNetMillicents), f.Method)
		} else {
			_, _ = fmt.Fprintf(w, "%s forecast unavailable: %s\n", meter.Meter, f.Reason)
		}
	}
	if !response.BillEstimateAvailable {
		_, _ = fmt.Fprintf(w, "Full bill estimate unavailable: %s\n", strings.Join(response.MissingBillComponents, ", "))
	}
}
