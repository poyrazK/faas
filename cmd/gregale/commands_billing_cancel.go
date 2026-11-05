// commands_billing_cancel.go — `gregale billing cancel` (issue #242).
//
// Sets cancel_at_period_end on the active subscription. Account
// keeps running until period end, then downgrades to Free
// (spec §4.7).
//
// Destructive — gated by the shared y/N prompt (confirmYesNo).
// apid itself does not gate (MFA-on-cancel is a tier-3 follow-up);
// headless callers pass --yes. Without it, JSON output,
// --non-interactive and a stdin that is not a terminal refuse instead
// of cancelling: JSON mode used to skip the prompt and cancel (CLI
// audit 2026-10-31).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

const billingSubCancelUsage = `usage: gregale billing cancel [--yes]

  Set cancel_at_period_end on the active subscription. Account
  keeps running until period end, then downgrades to Free
  (spec §4.7).

  Destructive — y/N confirmation prompt by default. --yes skips
  the prompt (intended for non-interactive shells / CI only;
  still POSTs the same /v1/billing/cancel route server-side).
  Without --yes, --json, --non-interactive and a stdin that is
  not a terminal refuse with "Confirmation required".

Exit codes:
  0   cancellation scheduled
  1   user error (no active subscription, already cancelled)
  3   vendor failure (Stripe / Paddle SDK error)
`

func cmdBillingCancel(args []string) int {
	fs := newFlagSet("billing cancel", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "skip the y/N confirmation prompt (non-interactive shells only)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, billingSubCancelUsage, "billing")
		return 1
	}

	if !*yes {
		question := "This will schedule your subscription for cancellation at the end of the current period. " +
			"Your apps will stop running on that date and your account will downgrade to Free. Continue?"
		if code := confirmYesNo(question, "--yes", "billing cancel"); code != 0 {
			return code
		}
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.PostBillingCancel(context.Background())
	if err != nil {
		// Map Problem codes to friendly CLI hints. apid returns
		// 409 with code=billing_already_cancelled when the
		// account has no active subscription; render the hint
		// instead of the SDK error verbatim.
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.Problem.Code == "billing_already_cancelled" {
			return printErr("No active subscription to cancel", err)
		}
		return printErr("Cancel failed", err)
	}

	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Cancellation scheduled.")
	_, _ = fmt.Fprintf(osStdout, "  effective: %s\n", resp.EffectiveAt.Format("2006-01-02"))
	_, _ = io.WriteString(osStdout, "  your apps will stop on the date above.\n")
	return 0
}
