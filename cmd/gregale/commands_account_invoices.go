package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const accountInvoicesUsage = "usage: gregale account invoices [--status unpaid|overdue|draft|open|paid|void|uncollectible|deleted]"

// cmdAccountInvoices lists the invoices Gregale exported from usage
// statements, with their payment status (ADR-1504).
func cmdAccountInvoices(args []string) int {
	fs := newFlagSet("account invoices", flag.ContinueOnError)
	status := fs.String("status", "", "unpaid, overdue, or one payment status (default: all)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(osStderr, accountInvoicesUsage, "account")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var invoices []api.StatementInvoiceExportResponse
	for cursor := ""; ; {
		page, err := client.ListStatementInvoices(context.Background(), *status, cursor, api.StatementInvoiceListMax)
		if err != nil {
			return printErr("Could not list invoices", err)
		}
		invoices = append(invoices, page.Invoices...)
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(api.StatementInvoiceListResponse{Invoices: invoices}))
	}
	tw := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PROVIDER\tINVOICE\tCUSTOMER\tPAYMENT\tAMOUNT\tREMAINING\tDUE\tPAID")
	for _, invoice := range invoices {
		payment := dashIfEmpty(invoice.PaymentStatus)
		if invoice.Overdue {
			payment += " (overdue)"
		}
		remaining := "-"
		if invoice.AmountRemainingMinor != nil {
			remaining = formatMillicents(invoice.Currency, api.MinorToMillicents(invoice.Currency, *invoice.AmountRemainingMinor))
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", dashIfEmpty(invoice.Provider), invoice.InvoiceID, invoice.Customer, payment,
			formatMillicents(invoice.Currency, api.MinorToMillicents(invoice.Currency, invoice.AmountMinor)), remaining, formatDate(invoice.DueAt), formatDate(invoice.PaidAt))
	}
	_ = tw.Flush()
	return 0
}

func formatDate(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.DateOnly)
}
