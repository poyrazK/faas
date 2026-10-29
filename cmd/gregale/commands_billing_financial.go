package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdBillingFinancial(command string, args []string) int {
	fs := newFlagSet("billing "+command, flag.ContinueOnError)
	month := fs.String("month", "", "UTC usage month (YYYY-MM; default current)")
	asJSON := fs.Bool("json", false, "print the machine-readable response")
	// The global --json is consumed before dispatch and sets jsonOutput,
	// so `billing costs --json` printed the table (Hunt 12 H12-42).
	wantJSON := func() bool { return *asJSON || jsonOutput }
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
		if wantJSON() {
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
	if wantJSON() {
		if err := json.NewEncoder(osStdout).Encode(response); err != nil {
			return printErr("Could not print financial costs", err)
		}
		return 0
	}
	printFinancialCosts(osStdout, response)
	return 0
}

func financialMoney(n int64) string {
	return fmt.Sprintf("%s %d.%05d", api.BillingCurrency, n/(100*api.MillicentsPerCent), n%(100*api.MillicentsPerCent))
}

func printFinancialCosts(w io.Writer, response api.FinancialCostsResponse) {
	_, _ = fmt.Fprintf(w, "Usage period: %s to %s (UTC)\nKnown usage: %s\nAs of: %s\n", response.PeriodStart.Format("2006-01-02"), response.PeriodEnd.Format("2006-01-02"), financialMoney(response.KnownUsageMillicents), response.AsOf.Format(time.RFC3339))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "METER\tAPPLICATION / JOB\tQUANTITY\tNET COST")
	for _, meter := range response.Meters {
		for _, row := range financialCostRows(meter) {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d %s\t%s\n", meter.Meter, row.name, row.quantity, row.unit, financialMoney(row.net))
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

type financialCostRow struct {
	key, name, unit string
	quantity, net   int64
}

// financialCostRows is one row per application or job and unit. The
// allocations are per deployment, and an app with six deployments this
// month was listed six times under the same name (Hunt 12 H12-43).
func financialCostRows(meter api.FinancialMeterCosts) []financialCostRow {
	var rows []financialCostRow
	index := map[string]int{}
	for _, contract := range meter.Accrued.Contracts {
		for _, allocation := range contract.Allocations {
			a := allocation.Attribution
			name, key := a.Name, ""
			switch {
			case a.AppID != "":
				key = "app:" + a.AppID
			case a.JobID != "":
				key = "job:" + a.JobID
			default:
				key = "name:" + a.Name
			}
			if name == "" {
				name = a.AppID
				if name == "" {
					name = a.JobID
				}
				if name == "" {
					name = "unallocated"
				}
			}
			key += "\x00" + contract.Price.Unit
			i, ok := index[key]
			if !ok {
				i = len(rows)
				index[key] = i
				rows = append(rows, financialCostRow{key: key, name: name, unit: contract.Price.Unit})
			}
			rows[i].quantity += allocation.Quantity
			rows[i].net += allocation.NetMillicents
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].net != rows[j].net {
			return rows[i].net > rows[j].net
		}
		return rows[i].quantity > rows[j].quantity
	})
	return rows
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
	if response.ProjectedSubtotalMillicents != nil {
		_, _ = fmt.Fprintf(w, "Projected subtotal (plan fee + usage): %s\n", financialMoney(*response.ProjectedSubtotalMillicents))
	}
	if !response.BillEstimateAvailable {
		_, _ = fmt.Fprintf(w, "Full bill estimate unavailable: %s\n", strings.Join(response.MissingBillComponents, ", "))
	}
}
