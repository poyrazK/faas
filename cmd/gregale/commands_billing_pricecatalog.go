// commands_billing_pricecatalog.go — `gregale billing price-catalog` (PR-P3).
//
// Three sub-subcommands backed by the operator-facing admin endpoints:
//
//	list    GET    /v1/admin/billing-paddle-catalog
//	sync    POST   /v1/admin/billing-paddle-catalog/sync
//	reset   DELETE /v1/admin/billing-paddle-catalog
//
// list + sync share the same printer (printBillingStatus renders
// either response identically — same shape). reset prints a
// targeted warning because dashboard-owned provider catalogs cannot be safely
// deleted through this local API.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
)

const (
	billingSubPriceCatalog      = "price-catalog"
	billingSubPriceCatalogList  = "list"
	billingSubPriceCatalogSync  = "sync"
	billingSubPriceCatalogReset = "reset"
	priceCatalogResetWarning    = "The provider catalog is durable on the provider platform — resetting local state does NOT delete products.\n" +
		"Manage products in the provider dashboard, then run `gregale billing price-catalog sync` to revalidate."
)

// cmdBillingPriceCatalog dispatches `gregale billing price-catalog <list|sync|reset>`.
// Bare subcommand prints usage to stderr and exits 1 (matches
// cmdBilling's bare-subcommand behaviour). Unknown sub-subcommand
// prints usage + the error to stderr.
func cmdBillingPriceCatalog(args []string) int {
	if len(args) == 0 {
		printPriceCatalogUsage(os.Stderr)
		return 1
	}
	switch args[0] {
	case billingSubPriceCatalogList:
		return cmdBillingPriceCatalogList(args[1:])
	case billingSubPriceCatalogSync:
		return cmdBillingPriceCatalogSync(args[1:])
	case billingSubPriceCatalogReset:
		return cmdBillingPriceCatalogReset(args[1:])
	case billingSubHelp, flagHelpShort, flagHelpLong:
		printPriceCatalogUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "gregale billing price-catalog: unknown subcommand %q\n\n", args[0])
		printPriceCatalogUsage(os.Stderr)
		return 1
	}
}

// cmdBillingPriceCatalogList renders the cached catalog. Same shape
// as `gregale billing status` — kept as a separate subcommand because
// operators think of "list" and "status" as different verbs
// (list = raw data; status = at-a-glance summary that includes the
// synced-at header). Two surfaces, one printer.
func cmdBillingPriceCatalogList(args []string) int {
	if len(args) != 0 {
		fmt.Fprintf(os.Stderr, "gregale billing price-catalog list: unexpected args\n")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListPaddleCatalog(context.Background())
	if err != nil {
		return printErr("Could not read billing catalog", err)
	}
	printBillingStatus(osStdout, resp)
	return 0
}

// cmdBillingPriceCatalogSync forces an EnsurePlanProducts round-trip.
// The endpoint is idempotent on provider-side products so calling it
// twice in a row is safe (the second call walks only the LIST
// endpoints). The Idempotency-Key header is auto-UUIDv4 by the
// client; a flaky-network retry within 24h replays the original 200.
func cmdBillingPriceCatalogSync(args []string) int {
	if len(args) != 0 {
		fmt.Fprintf(os.Stderr, "gregale billing price-catalog sync: unexpected args\n")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.SyncPaddleCatalog(context.Background(), "")
	if err != nil {
		return printErr("Provider catalog sync failed", err)
	}
	_, _ = fmt.Fprintln(os.Stdout, "Synced. Catalog snapshot:")
	printBillingStatus(os.Stdout, resp)
	return 0
}

// cmdBillingPriceCatalogReset signals a catalog reset. The provider
// handler may be a no-op or unsupported; the CLI prints the warning so the operator
// knows what actually happens. Future PRs that add merchant-side
// cleanup will replace the warning with a success message.
func cmdBillingPriceCatalogReset(args []string) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintf(os.Stderr, "gregale billing price-catalog reset: unexpected args\n")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if _, err := client.ResetPaddleCatalog(context.Background()); err != nil {
		return printErr("Provider catalog reset failed", err)
	}
	_, _ = fmt.Fprintln(os.Stdout, "Reset signal recorded.")
	_, _ = fmt.Fprintln(os.Stdout)
	_, _ = fmt.Fprintln(os.Stdout, priceCatalogResetWarning)
	return 0
}

// printPriceCatalogUsage prints the price-catalog dispatch help.
// Reuses the subcommand names from the const block above so a
// future rename trips one tripwire.
func printPriceCatalogUsage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "usage: gregale billing price-catalog <subcommand>\n\n"+
		"  %s    read the cached provider price + product catalog\n"+
		"  %s    force a provider catalog hydration (idempotent)\n"+
		"  %s   signal a catalog reset (provider-managed; see warning)\n"+
		"\n"+
		"Run 'gregale billing price-catalog help' for this message.\n",
		billingSubPriceCatalogList, billingSubPriceCatalogSync, billingSubPriceCatalogReset)
}
