// commands_billing_reconcile_paddle.go —
// `gregale billing reconcile-paddle-overage` (B4 / Tier 1).
//
// Operator-side pre-flight for the Paddle overage pusher's
// per-window claim state machine. Drives GET
// /v1/admin/billing-paddle-overage/preflight and reports:
//
//   - Whether the paddle_overage_dedupe table is present (migrations
//     00034 + 00041 both unapplied => table_exists=false).
//   - Which of the four migration-00041 columns are present (any
//     missing => remediation hint names them by column name).
//   - Per-state row counts so the operator can see whether the
//     meterd loop has in-flight pending claims to reap.
//
// Output is a single line:
//
//	paddle_overage_dedupe: pending=<n> completed=<n> columns=ok
//	paddle_overage_dedupe: pending=<n> completed=<n> columns=missing=window_start,state
//	paddle_overage_dedupe: pending=<n> completed=<n> table=missing
//
// machine-friendly for shell composition (echo/grep/awk) and the
// same shape as `gregale billing reconcile`. Operators who want JSON
// can pipe through `jq`; we keep the default plain-text because
// that is what shell history shows.
//
// Exit codes:
//
//	0  schema OK (all four 00041 columns present)
//	1  schema missing columns OR table entirely absent — remediation
//	   hint printed to stderr
//	2  not logged in / transport failure
//
// The exit code matters because CI scripts that gate a meterd
// deploy on the pre-flight can short-circuit on non-zero rather
// than grepping stdout.

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const billingSubReconcilePaddleOverage = "reconcile-paddle-overage"

// cmdBillingReconcilePaddleOverage runs the B4 pre-flight against
// the live apid. On a Paddle deployment that has not yet applied
// migration 00041 the CLI exits 1 with a clear remediation hint;
// on a healthy deployment the CLI exits 0 with the per-state row
// counts. The HTTP transport surfaces 5xx as exit 2 (transport-
// class failure, distinct from schema failure) so a CI script can
// distinguish "you forgot to migrate" from "the apid is down".
func cmdBillingReconcilePaddleOverage(args []string) int {
	if len(args) != 0 {
		fmt.Fprintf(os.Stderr, "usage: gregale billing reconcile-paddle-overage\n")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetBillingPaddleOveragePreflight(context.Background())
	if err != nil {
		return printErr("Pre-flight failed", err)
	}

	// Collect the missing columns in a stable order so the output
	// is deterministic across runs (CI scripts grep on this string).
	var missing []string
	if resp.TableExists {
		if !resp.HasWindowStart {
			missing = append(missing, "window_start")
		}
		if !resp.HasState {
			missing = append(missing, "state")
		}
		if !resp.HasClaimedAt {
			missing = append(missing, "claimed_at")
		}
		if !resp.HasClaimedBy {
			missing = append(missing, "claimed_by")
		}
	}

	switch {
	case !resp.TableExists:
		_, _ = fmt.Fprintf(os.Stdout,
			"paddle_overage_dedupe: pending=0 completed=0 table=missing\n")
		_, _ = fmt.Fprintf(os.Stderr,
			"Pre-flight: paddle_overage_dedupe table is absent — apply migrations 00034 then 00041 in order before running the meterd Paddle pusher.\n")
		return 1
	case len(missing) > 0:
		_, _ = fmt.Fprintf(os.Stdout,
			"paddle_overage_dedupe: pending=%d completed=%d columns=missing=%s\n",
			resp.PendingRows, resp.CompletedRows, strings.Join(missing, ","))
		_, _ = fmt.Fprintf(os.Stderr,
			"Pre-flight: migration 00041 was not (fully) applied — columns missing: %s. Re-apply 00041.\n",
			strings.Join(missing, ", "))
		return 1
	default:
		_, _ = fmt.Fprintf(os.Stdout,
			"paddle_overage_dedupe: pending=%d completed=%d columns=ok\n",
			resp.PendingRows, resp.CompletedRows)
		return 0
	}
}
