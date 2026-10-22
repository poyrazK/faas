package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// billingRunVerbs are the `gregale account billing-run` verbs (ADR-1503),
// mapped to the flags each accepts.
var billingRunVerbs = map[string][]string{
	"start":           {"month", "sources", "no-finalize", "export", "no-hold-on-gaps", "wait"},
	"list":            {"limit"},
	"show":            {"outcome"},
	"schedule":        {},
	"schedule-set":    {"sources", "no-finalize", "export", "no-hold-on-gaps", "delay-hours"},
	"schedule-delete": {},
}

const billingRunUsage = "usage: gregale account billing-run {start|list|show RUN_ID|schedule|schedule-set|schedule-delete} [flags]"

// billingRunPollInterval is how often `start --wait` polls; tests shorten it.
var billingRunPollInterval = 5 * time.Second

type billingRunFlags struct {
	month, sources, export, outcome string
	noFinalize, noHoldOnGaps, wait  bool
	limit, delayHours               int
}

// cmdAccountBillingRun bills a month for every customer, or manages the
// monthly schedule that does it (ADR-1503).
func cmdAccountBillingRun(args []string) int {
	if len(args) == 0 {
		PrintUsage(osStderr, billingRunUsage, "account")
		return 1
	}
	verb := args[0]
	allowed, ok := billingRunVerbs[verb]
	if !ok {
		PrintUsage(osStderr, billingRunUsage, "account")
		return 1
	}
	fs := newFlagSet("account billing-run "+verb, flag.ContinueOnError)
	var f billingRunFlags
	fs.StringVar(&f.month, "month", "", "UTC calendar month to bill, YYYY-MM (default: last month)")
	fs.StringVar(&f.sources, "sources", "", "comma-separated app_consumers,platform_tenants,product_customers (default: app_consumers,platform_tenants)")
	fs.BoolVar(&f.noFinalize, "no-finalize", false, "only draft statements")
	fs.StringVar(&f.export, "export", "", "none, stripe, paddle or lago (default none)")
	fs.BoolVar(&f.noHoldOnGaps, "no-hold-on-gaps", false, "finalize consumers even when request telemetry shows unbilled requests")
	fs.BoolVar(&f.wait, "wait", false, "wait until the run completes and print held and failed customers")
	fs.IntVar(&f.limit, "limit", 0, "runs to list (1-100, default 20)")
	fs.StringVar(&f.outcome, "outcome", "", "only customers with this outcome, e.g. held or failed")
	fs.IntVar(&f.delayHours, "delay-hours", 0, "hours after month end before a scheduled run starts (1-240, default 24)")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	wantArgs := 0
	if verb == "show" {
		wantArgs = 1
	}
	if fs.NArg() != wantArgs || !flagsWithin(fs, allowed) {
		PrintUsage(osStderr, billingRunUsage, "account")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	return runBillingRunVerb(context.Background(), client, verb, fs.Args(), f)
}

func runBillingRunVerb(ctx context.Context, client *api.Client, verb string, args []string, f billingRunFlags) int {
	switch verb {
	case "start":
		return billingRunStart(ctx, client, f)
	case "list":
		runs, err := client.ListBillingRuns(ctx, f.limit)
		if err != nil {
			return printErr("Could not list billing runs", err)
		}
		return printBillingRuns(runs)
	case "show":
		return billingRunShow(ctx, client, args[0], f.outcome)
	case "schedule":
		sched, err := client.GetBillingRunSchedule(ctx)
		if err != nil {
			return printErr("No billing run schedule", err)
		}
		return printBillingRunSchedule(sched)
	case "schedule-set":
		req := api.PutBillingRunScheduleRequest{Sources: splitSources(f.sources), Export: f.export}
		req.Finalize, req.HoldOnGaps = negatedFlag(f.noFinalize), negatedFlag(f.noHoldOnGaps)
		if f.delayHours != 0 {
			req.DelayHours = &f.delayHours
		}
		sched, err := client.PutBillingRunSchedule(ctx, req)
		if err != nil {
			return printErr("Could not save the billing run schedule", err)
		}
		return printBillingRunSchedule(sched)
	default: // schedule-delete
		if err := client.DeleteBillingRunSchedule(ctx); err != nil {
			return printErr("Could not delete the billing run schedule", err)
		}
		_, _ = fmt.Fprintln(osStdout, "Billing run schedule deleted.")
		return 0
	}
}

func splitSources(raw string) []string {
	var out []string
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// negatedFlag turns a --no-X flag into an optional X: unset leaves the
// server default.
func negatedFlag(no bool) *bool {
	if !no {
		return nil
	}
	v := false
	return &v
}

func lastMonth(now time.Time) string {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0).Format("2006-01")
}

func billingRunStart(ctx context.Context, client *api.Client, f billingRunFlags) int {
	req := api.CreateBillingRunRequest{Month: f.month, Sources: splitSources(f.sources), Export: f.export}
	if req.Month == "" {
		req.Month = lastMonth(time.Now())
	}
	req.Finalize, req.HoldOnGaps = negatedFlag(f.noFinalize), negatedFlag(f.noHoldOnGaps)
	run, err := client.CreateBillingRun(ctx, req)
	if err != nil {
		return printErr("Could not start the billing run", err)
	}
	for f.wait && run.Status != "completed" {
		time.Sleep(billingRunPollInterval)
		if run, err = client.GetBillingRun(ctx, run.ID); err != nil {
			return printErr("Could not read the billing run", err)
		}
	}
	if !f.wait {
		return printBillingRun(run)
	}
	return billingRunShow(ctx, client, run.ID, "")
}

func billingRunShow(ctx context.Context, client *api.Client, runID, outcome string) int {
	run, err := client.GetBillingRun(ctx, runID)
	if err != nil {
		return printErr("Could not read the billing run", err)
	}
	var items []api.BillingRunItemResponse
	for cursor := ""; ; {
		page, err := client.ListBillingRunItems(ctx, runID, outcome, cursor, api.BillingRunItemListMax)
		if err != nil {
			return printErr("Could not list the billing run's customers", err)
		}
		items = append(items, page.Items...)
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(struct {
			Run   api.BillingRunResponse       `json:"run"`
			Items []api.BillingRunItemResponse `json:"items"`
		}{run, items}))
	}
	printBillingRun(run)
	_, _ = fmt.Fprintln(osStdout)
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KIND\tCUSTOMER\tOUTCOME\tAMOUNT\tINVOICE\tPAYMENT\tREASON")
	for _, item := range items {
		amount := "-"
		if item.AmountMillicents != nil {
			amount = formatMillicents(item.Currency, *item.AmountMillicents)
		}
		customer := item.ExternalRef
		if item.SourceKind == "product_control" && len(item.AppID) >= 8 {
			// A tenant is a customer of each Product Control app separately.
			customer += " · app " + item.AppID[:8]
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", item.SourceKind, customer, item.Outcome, amount,
			dashIfEmpty(item.InvoiceID), dashIfEmpty(item.PaymentStatus), item.Reason)
	}
	_ = tw.Flush()
	return 0
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func billingRunCounts(c api.BillingRunCounts) string {
	return fmt.Sprintf("exported %d · finalized %d · drafted %d · up to date %d · held %d · skipped %d · failed %d · pending %d",
		c.Exported, c.Finalized, c.Drafted, c.UpToDate, c.Held, c.Skipped, c.Failed, c.Pending)
}

func printBillingRun(run api.BillingRunResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(run))
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Run\t%s\nMonth\t%s\nStatus\t%s (%s)\nSources\t%s\nFinalize\t%t\nExport\t%s\nHold on gaps\t%t\nCustomers\t%s\n",
		run.ID, run.Month, run.Status, run.Trigger, strings.Join(run.Sources, ", "), run.Finalize, run.Export, run.HoldOnGaps,
		billingRunCounts(run.Counts))
	_ = tw.Flush()
	return 0
}

func printBillingRuns(runs api.BillingRunListResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(runs))
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "RUN\tMONTH\tSTATUS\tTRIGGER\tEXPORT\tEXPORTED\tFINALIZED\tHELD\tFAILED\tCREATED")
	for _, run := range runs.Runs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\n", run.ID, run.Month, run.Status, run.Trigger, run.Export,
			run.Counts.Exported, run.Counts.Finalized, run.Counts.Held, run.Counts.Failed, run.CreatedAt.Format(time.RFC3339))
	}
	_ = tw.Flush()
	return 0
}

func printBillingRunSchedule(sched api.BillingRunScheduleResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(sched))
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "Next month\t%s\nNext run\t%s\nDelay\t%dh after month end\nSources\t%s\nFinalize\t%t\nExport\t%s\nHold on gaps\t%t\n",
		sched.NextMonth, sched.NextRunAt.Format(time.RFC3339), sched.DelayHours, strings.Join(sched.Sources, ", "),
		sched.Finalize, sched.Export, sched.HoldOnGaps)
	if sched.LastMonth != "" {
		_, _ = fmt.Fprintf(tw, "Last month\t%s\n", sched.LastMonth)
	}
	_ = tw.Flush()
	return 0
}
