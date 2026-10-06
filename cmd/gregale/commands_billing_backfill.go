package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdBillingBackfillInvoices(args []string) int {
	fs := newFlagSet("billing backfill-invoices", flag.ContinueOnError)
	cursor := fs.String("cursor", "", "resume from the next provider page")
	limit := fs.Int("limit", api.MaxInvoiceHistoryPageSize, "provider records to scan (1-25)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *limit < 1 || *limit > api.MaxInvoiceHistoryPageSize {
		return printErr("Invalid invoice history backfill", fmt.Errorf("usage: gregale billing backfill-invoices [--cursor TOKEN] [--limit 1..%d]", api.MaxInvoiceHistoryPageSize))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.InvoiceHistoryTimeout)
	defer cancel()
	result, err := client.BackfillInvoiceHistory(ctx, *cursor, *limit)
	if err != nil {
		return printErr("Could not backfill invoice history", err)
	}
	if err := json.NewEncoder(osStdout).Encode(result); err != nil {
		return printErr("Could not print invoice history backfill", err)
	}
	return 0
}
