package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdBillingRefreshInvoice(args []string) int {
	fs := newFlagSet("billing refresh-invoice", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || fs.Arg(0) == "" {
		return printErr("Invalid invoice ID", fmt.Errorf("usage: gregale billing refresh-invoice ID"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.InvoiceRefreshTimeout)
	defer cancel()
	result, err := client.RefreshInvoiceFacts(ctx, fs.Arg(0))
	if err != nil {
		return printErr("Could not refresh invoice facts", err)
	}
	if err := json.NewEncoder(osStdout).Encode(result); err != nil {
		return printErr("Could not print invoice refresh", err)
	}
	return 0
}
