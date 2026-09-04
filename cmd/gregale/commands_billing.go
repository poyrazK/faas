// commands_billing.go — `faas billing …` family (issue #253).
//
//   faas billing                       dispatch help (prints subcommand list)
//   faas billing portal                open the active billing provider's portal
//                                      (or print the URL when --print is set / DISPLAY
//                                      is unavailable)
//   faas billing status                read the active billing Provider's cached
//                                      catalog (PR-P3)
//   faas billing status --watch N     re-poll the catalog every 5 s for N seconds (PR-P4)
//   faas billing price-catalog         list | sync | reset the Paddle catalog (PR-P3)
//   faas billing reconcile             run a single-account reconcile via the active
//                                      billing Provider (PR-P3)
//   faas billing webhook-test          signed round-trip POST to a webhook URL (PR-P4,
//                                      operator-only; mirrors the production signer)
//
// This is the CLI companion to GET /dashboard/billing's provider-neutral
// billing portal button. Same URL, same auth chain (Bearer via
// FAAS_TOKEN / OS keychain). Future subcommands (`retry`, `cancel`,
// `add-card`, `plan-via-portal`) land here per issue #265 without
// touching the top-level dispatch in main.go.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/browser"
)

const (
	billingSubPortal        = "portal"
	billingSubRetry         = "retry"
	billingSubCancel        = "cancel"
	billingSubPaymentMethod = "payment-method"
	billingSubHelp          = "help"
	flagHelpLong            = "--help"
	flagHelpShort           = "-h"
)

// cmdBilling dispatches `faas billing <subcommand>` to the right
// handler. Bare `faas billing` and `faas billing help` print the
// subcommand list (POSIX-style). Unknown subcommands return 1 (user
// error per UX §3.2), not 2 (auth), because no token was attempted.
func cmdBilling(args []string) int {
	parent, _ := lookupCliCommand("billing")
	if len(args) == 0 {
		printBillingUsage(os.Stderr)
		PrintUsage(os.Stderr, "usage: gregale billing <subcommand>", "billing")
		return 1
	}
	switch args[0] {
	case billingSubPortal:
		return cmdBillingPortal(args[1:])
	case billingSubRetry:
		return cmdBillingRetry(args[1:])
	case billingSubCancel:
		return cmdBillingCancel(args[1:])
	case billingSubPaymentMethod:
		return cmdBillingPaymentMethod(args[1:])
	case billingSubStatus:
		return cmdBillingStatus(args[1:])
	case billingSubPriceCatalog:
		return cmdBillingPriceCatalog(args[1:])
	case billingSubReconcile:
		return cmdBillingReconcile(args[1:])
	case billingSubReconcilePaddleOverage:
		return cmdBillingReconcilePaddleOverage(args[1:])
	case billingSubWebhookTest:
		return cmdBillingWebhookTest(args[1:])
	case billingSubHelp, flagHelpShort, flagHelpLong:
		printBillingUsage(osStdout)
		return 0
	default:
		sug, _ := suggestSubcommand(args[0], parent)
		fmt.Fprintf(os.Stderr, "faas billing: unknown subcommand %q\n\n", args[0])
		printBillingUsage(os.Stderr)
		maybeSuggestSub(sug)
		return 1
	}
}

func printBillingUsage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "usage: faas billing <subcommand>\n\n"+
		"  portal              open the active billing provider's portal in your browser\n"+
		"                      (--print  print URL to stdout only; --no-open  skip browser)\n"+
		"  payment-method      show the card-on-file summary; open the portal to update\n"+
		"  retry               retry the latest unpaid charge (issue #242; closes the\n"+
		"                      dunning-email lie at pkg/mail/account.go:107,150)\n"+
		"  cancel              set cancel_at_period_end on the active subscription;\n"+
		"                      y/N confirm (--yes for non-interactive shells)\n"+
		"  status              read the active billing Provider's catalog snapshot\n"+
		"                      (--watch N  re-poll every 5 s for N seconds; --json  emit JSON)\n"+
		"  price-catalog       list | sync | reset the provider price + product catalog\n"+
		"  reconcile <id>      run a single-account reconcile via the active billing Provider\n"+
		"  reconcile-paddle-overage\n"+
		"                      pre-flight the paddle_overage_dedupe schema for migration 00041\n"+
		"  webhook-test        signed round-trip POST to a webhook URL (operator-only)\n"+
		"                      (faas billing webhook-test paddle --url … --secret …)\n"+
		"\n"+
		"Run 'faas billing help' for this message.\n")
}

// cmdBillingPortal fetches the operator-configured billing portal URL
// from GET /v1/billing/portal and either prints it or opens it in the
// default browser.
//
// Flags:
//
//	--print      print URL to stdout only and exit (no browser attempt)
//	--no-open    equivalent to --print for shell-completion friendliness
//
// The browser-open failure path mirrors cmdDashboard (commands5.go:
// "open the URL, fall back gracefully"): we print the URL on stderr
// with a friendly hint and exit 0. CI scripts and `&&`-chained shell
// commands should not treat a missing $DISPLAY as a hard failure —
// that would break `faas billing portal && faas plan <new>` flows on
// headless boxes.
func cmdBillingPortal(args []string) int {
	fs := flag.NewFlagSet("billing portal", flag.ContinueOnError)
	printOnly := fs.Bool("print", false, "print URL to stdout only; do not open browser")
	noOpen := fs.Bool("no-open", false, "alias of --print")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		printBillingUsage(os.Stderr)
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	url, err := client.GetBillingPortal(context.Background())
	if err != nil {
		return printErr("Could not fetch billing portal", err)
	}
	if url == "" {
		// Same exit code as "missing config" elsewhere in the CLI:
		// 1 (user error) because the operator can fix this on the
		// box (FAAS_BILLING_PORTAL_URL). Distinct from "no auth"
		// (2) and from "platform failure" (3).
		return printErr("Billing portal is not configured on this box",
			errors.New("set FAAS_BILLING_PORTAL_URL on the box (see deploy/ansible)"))
	}

	if *printOnly || *noOpen {
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{
				"url":     url,
				"service": "billing",
			}))
		}
		_, _ = fmt.Fprintln(osStdout, url)
		return 0
	}

	_, _ = fmt.Fprintf(osStdout, "Opening %s\n", url)
	if err := browser.Open(url); err != nil {
		PrintFail(os.Stderr, "Could not open browser: %v", err)
		_, _ = fmt.Fprintf(os.Stderr, "  Open this URL manually:\n  %s\n", url)
		return 0
	}
	return 0
}
