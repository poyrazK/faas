// commands_billing_reconcile.go — `gregale billing reconcile` (PR-P3).
//
// Drives POST /v1/admin/billing-reconcile/{id}. The endpoint calls
// billing.Provider.ReconcileUsage for the rolling 30-day window
// [start, end); Stripe implements it, Paddle returns 501 with code
// billing_reconcile_unsupported. The CLI surfaces both as typed
// errors so the operator knows which provider they hit.
//
// Output is a single line:
//
//	account=<uuid> window=[<start>,<end>] mb_seconds=<int>
//
// machine-friendly for diffing against the local usage_minutes sum.
// Operators who want JSON can pipe through `jq`; we keep the
// default plain-text because that is what shell history shows.

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
)

const billingSubReconcile = "reconcile"

// cmdBillingReconcile runs a single-account reconcile against the
// active billing Provider. The handler 501s on Paddle; the CLI
// surfaces that with a clear "Paddle does not implement
// ReconcileUsage" hint so the operator knows the failure is
// provider-scoped, not a transport bug.
func cmdBillingReconcile(args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: gregale billing reconcile <account-id>\n")
		return 1
	}
	accountID := args[0]
	if _, err := uuid.Parse(accountID); err != nil {
		return printErr("Bad account id", fmt.Errorf("expected UUID: %w", err))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ReconcileAccount(context.Background(), accountID)
	if err != nil {
		return printErr("Reconcile failed", err)
	}
	// Single-line, machine-friendly output. The window is in UTC so
	// an operator in a different timezone doesn't have to second-guess
	// the timestamp; the server's handler emits the same UTC format.
	_, _ = fmt.Fprintf(os.Stdout,
		"account=%s window=[%s,%s] mb_seconds=%d\n",
		resp.AccountID,
		resp.Start.UTC().Format("2006-01-02T15:04:05Z"),
		resp.End.UTC().Format("2006-01-02T15:04:05Z"),
		resp.MBSeconds,
	)
	return 0
}
