package main

// adr: 20261126082046 — `gregale mcp server stats`: a sold MCP server's
// customer funnel, revenue, tools and customers near their limit.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdMCPStats(args []string) int {
	fs := newFlagSet("mcp-stats", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug")
	months := fs.Int("months", 1, "calendar months to read, 1-12 (1 = this month)")
	timeout := fs.Duration("timeout", 60*time.Second, "API deadline")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *slug == "" {
		return printErr("MCP stats", errors.New("--app is required"))
	}
	if *months < 1 || *months > api.MCPSellerStatsMaxMonths {
		return printErr("MCP stats", fmt.Errorf("--months must be from 1 to %d", api.MCPSellerStatsMaxMonths))
	}
	if *timeout <= 0 {
		return printErr("MCP stats", errors.New("--timeout must be positive"))
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	resp, err := c.GetMCPSellerStats(ctx, *slug, *months)
	if err != nil {
		return printErr("Could not fetch MCP stats", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderMCPStats(osStdout, resp)
	return 0
}

func renderMCPStats(w io.Writer, r api.MCPSellerStatsResponse) {
	_, _ = fmt.Fprintf(w, "App:        %s\nCustomers:  %d open, %d closed\n\n", r.App, r.Customers.Open, r.Customers.Closed)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "MONTH\tSIGNED UP\tOAUTH/SIGNUP/OWNER\tACTIVE\tPAYING\tCLOSED\tUNITS\tREVENUE")
	for _, m := range r.Months {
		month := m.Month
		if m.Current {
			month += " (so far)"
		}
		by := fmt.Sprintf("%d/%d/%d", m.SignedUpBySource[api.MCPSellerSourceOAuth], m.SignedUpBySource[api.MCPSellerSourceSignup], m.SignedUpBySource[api.MCPSellerSourceOwner])
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%d\t%d\t%d\t%d\t%s\n", month, m.SignedUp, by, m.Active, m.Paying, m.Closed, m.Units, formatStatsRevenue(m.Revenue))
	}
	_ = tw.Flush()
	if !r.EstimateComplete {
		_, _ = fmt.Fprintln(w, "This month's estimate leaves some customers out (too many to price, or one could not be priced).")
	}
	if len(r.Tools) > 0 {
		_, _ = fmt.Fprintln(w)
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "TOOL\tUNITS\tCUSTOMERS\tREVENUE THIS MONTH")
		for _, t := range r.Tools {
			_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%s\n", t.Tool, t.Units, t.Customers, formatStatsRevenue(t.Revenue))
		}
		_ = tw.Flush()
	}
	if len(r.NearLimit) > 0 {
		_, _ = fmt.Fprintln(w, "\nNear their plan's monthly units:")
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "CUSTOMER\tPLAN\tUNITS\tUSED")
		for _, n := range r.NearLimit {
			_, _ = fmt.Fprintf(tw, "%s (%s)\t%s\t%d / %d\t%d%%\n", n.Name, n.ConsumerID, n.Plan, n.Units, n.MaxUnitsPerMonth, n.Percent)
		}
		_ = tw.Flush()
	}
	_, _ = fmt.Fprintln(w, "\nBilled revenue is finalized statements before credits; the current month is an estimate.")
}

// formatStatsRevenue prints billed and estimated amounts per currency.
func formatStatsRevenue(amounts []api.MCPSellerStatsAmount) string {
	if len(amounts) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(amounts))
	for _, a := range amounts {
		switch {
		case a.BilledMillicents > 0 && a.EstimatedMillicents > 0:
			parts = append(parts, formatMillicents(a.Currency, a.BilledMillicents)+" billed + ~"+formatMillicents(a.Currency, a.EstimatedMillicents))
		case a.EstimatedMillicents > 0:
			parts = append(parts, "~"+formatMillicents(a.Currency, a.EstimatedMillicents))
		default:
			parts = append(parts, formatMillicents(a.Currency, a.BilledMillicents))
		}
	}
	return strings.Join(parts, ", ")
}
