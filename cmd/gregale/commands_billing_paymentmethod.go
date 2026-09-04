// commands_billing_paymentmethod.go — `gregale billing payment-method`
// (issue #242).
//
// Shows the card-on-file summary (brand, last-4, expiry) and
// opens the operator-configured billing portal in the browser
// so the customer can update the card. The portal handles the
// actual edit (spec §4.7); this subcommand is the read-side
// preview + open-the-editor-path.
//
// The card-on-file summary comes from GET /v1/billing/portal's
// payment_method block (issue #242 extension). The CLI and the
// dashboard render from the same round-trip — no separate
// endpoint needed.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/browser"
)

func cmdBillingPaymentMethod(args []string) int {
	fs := flag.NewFlagSet("billing payment-method", flag.ContinueOnError)
	printOnly := fs.Bool("print", false, "print card-on-file summary to stdout; do not open browser")
	noOpen := fs.Bool("no-open", false, "alias of --print")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "usage: gregale billing payment-method [--print|--no-open]\n")
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetBillingPortalFull(context.Background())
	if err != nil {
		return printErr("Could not fetch billing portal", err)
	}

	// Render the card-on-file summary first (so a no-card-on-file
	// customer sees a friendly hint instead of an empty browser).
	if resp.PaymentMethod == nil || resp.PaymentMethod.Brand == "" {
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{
				"payment_method": nil,
				"portal_url":     resp.URL,
			}))
		}
		PrintFail(os.Stderr, "No payment method on file.")
		if resp.URL != "" {
			_, _ = fmt.Fprintf(os.Stderr, "Open this URL to add one:\n  %s\n", resp.URL)
			if !*printOnly && !*noOpen {
				_, _ = fmt.Fprintf(osStdout, "Opening %s\n", resp.URL)
				if err := browser.Open(resp.URL); err != nil {
					_, _ = fmt.Fprintf(os.Stderr, "  (could not open browser: %v)\n", err)
				}
			}
		}
		return 1
	}

	pm := resp.PaymentMethod
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{
			"payment_method": pm,
			"portal_url":     resp.URL,
		}))
	}
	PrintOK(osStdout, "Payment method on file:")
	_, _ = fmt.Fprintf(osStdout, "  brand:    %s\n", pm.Brand)
	_, _ = fmt.Fprintf(osStdout, "  last4:    %s\n", pm.Last4)
	_, _ = fmt.Fprintf(osStdout, "  expires:  %02d/%04d\n", pm.ExpMonth, pm.ExpYear)

	if resp.URL == "" {
		// No portal URL = operator has not configured
		// FAAS_BILLING_PORTAL_URL. Print a friendly hint; do
		// not attempt the browser open.
		_, _ = io.WriteString(osStdout, "  (operator has not configured the billing portal; contact support to update)\n")
		return 0
	}
	if *printOnly || *noOpen {
		_, _ = fmt.Fprintf(osStdout, "  portal:   %s\n", resp.URL)
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "Opening %s\n", resp.URL)
	if err := browser.Open(resp.URL); err != nil {
		PrintFail(os.Stderr, "Could not open browser: %v", err)
		_, _ = fmt.Fprintf(os.Stderr, "  Open this URL manually:\n  %s\n", resp.URL)
		return 0
	}
	return 0
}
