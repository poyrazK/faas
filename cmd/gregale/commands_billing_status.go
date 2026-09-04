// commands_billing_status.go — `faas billing status` (PR-P3) +
// `faas billing status --watch` (PR-P4).
//
// Prints the active billing Provider name + the cached catalog
// snapshot. Backs the operator's at-a-glance "is billing wired up
// correctly?" check. The endpoint is admin-scoped + email-allowlist
// gated server-side; this CLI just renders the response.
//
// On a provider without a catalog surface the handler returns 501 with
// code billing_op_unsupported — the CLI surfaces that as a typed error
// instead of an empty or misleading catalog.
//
// PR-P4 additions:
//   - --watch N     re-poll the catalog every 5 s for N seconds
//                   (default 60). Used to watch the cache fill during
//                   `faas billing price-catalog sync`. Clears the
//                   terminal between ticks so the operator sees a
//                   moving snapshot, not a scrolling log.
//   - --json        emit the raw JSON response (machine-readable;
//                   for piping into jq or into a CI smoke check).
//   - --no-clear    with --watch, append ticks instead of clearing.
//                   Useful when the operator wants a journal-style
//                   audit trail.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const billingSubStatus = "status"

// billingStatusWatchDefault is the default `--watch` duration when
// the operator passes `--watch` without an explicit count.
const billingStatusWatchDefault = 60 * time.Second

// billingStatusTickInterval is the poll cadence. 5 s matches the
// meterd push cadence so the operator sees fresh catalog data within
// one push tick.
const billingStatusTickInterval = 5 * time.Second

// cmdBillingStatus renders the operator-facing billing status.
//
// Flag parsing is intentionally minimal — the CLI flag package
// (cmd/gregale) stops at the first non-flag token, so positional
// handling here is by design. PR-P4's --watch / --json are the
// only operator-facing knobs; future growth would justify a real
// flag.FlagSet, but that adds ~30 LoC for one or two flags.
func cmdBillingStatus(args []string) int {
	watch, watchDur, asJSON, noClear, err := parseBillingStatusFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "faas billing status: %v\n", err)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if !watch {
		resp, err := client.ListPaddleCatalog(context.Background())
		if err != nil {
			// The 501 path renders a hint specific to "this is a
			// provider-scoped surface, not a transport failure".
			// Branching on the problem code keeps the UX targeted.
			return printErr("Could not read billing catalog", err)
		}
		if asJSON {
			if err := printBillingStatusJSON(osStdout, resp); err != nil {
				return printErr("Could not render status JSON", err)
			}
			return 0
		}
		printBillingStatus(osStdout, resp)
		return 0
	}
	return runBillingStatusWatch(context.Background(), client, watchDur, asJSON, noClear)
}

// parseBillingStatusFlags walks args for the four PR-P4 flags.
// Returns (watch, duration, asJSON, noClear). --watch without a
// value uses billingStatusWatchDefault; --watch with a value parses
// as seconds. Bad values fail loudly so the operator notices typos
// instead of getting silent default behaviour.
func parseBillingStatusFlags(args []string) (bool, time.Duration, bool, bool, error) {
	var (
		watch         bool
		watchDur      = billingStatusWatchDefault
		asJSON        bool
		noClear       bool
		watchValueSet bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--watch":
			watch = true
			// --watch N (value in next arg) OR --watch=N (inline).
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				d, err := time.ParseDuration(args[i+1] + "s")
				if err != nil {
					return false, 0, false, false, fmt.Errorf("--watch value %q not a duration in seconds: %w", args[i+1], err)
				}
				watchDur = d
				watchValueSet = true
				i++
			}
		case strings.HasPrefix(a, "--watch="):
			watch = true
			d, err := time.ParseDuration(strings.TrimPrefix(a, "--watch=") + "s")
			if err != nil {
				return false, 0, false, false, fmt.Errorf("--watch value %q not a duration in seconds: %w", strings.TrimPrefix(a, "--watch="), err)
			}
			watchDur = d
			watchValueSet = true
		case a == "--json":
			asJSON = true
		case a == "--no-clear":
			noClear = true
		default:
			return false, 0, false, false, fmt.Errorf("unexpected arg %q (expected --watch [N], --json, --no-clear)", a)
		}
	}
	if watchValueSet && watchDur <= 0 {
		return false, 0, false, false, fmt.Errorf("--watch duration must be positive (got %s)", watchDur)
	}
	return watch, watchDur, asJSON, noClear, nil
}

// runBillingStatusWatch polls the catalog endpoint on
// billingStatusTickInterval for watchDur total. Returns 0 on
// clean exit (Ctrl-C handled by os.Interrupt → SIGINT → context
// cancel via the harness's signal.NotifyContext; see
// cmd/gregale/main.go). Returns non-zero if the FIRST poll fails
// — a 501 on the first tick is a hard fail (a provider without a
// catalog has nothing to watch); a 501 on a later tick prints a warning
// but keeps the loop running, since a transient provider flip mid-
// watch is a legitimate operator scenario.
func runBillingStatusWatch(ctx context.Context, client *api.Client, watchDur time.Duration, asJSON, noClear bool) int {
	deadline := time.Now().Add(watchDur)
	tick := 0
	for {
		select {
		case <-ctx.Done():
			return 0
		default:
		}
		resp, err := client.ListPaddleCatalog(ctx)
		if err != nil {
			if tick == 0 {
				return printErr("Could not read billing catalog", err)
			}
			fmt.Fprintf(os.Stderr, "  tick %d: %v (continuing)\n", tick, err)
		} else {
			if !noClear {
				// ANSI clear-screen + cursor-home. Falls back to
				// "\n\n\n" on dumb terminals — not perfect, but
				// the operator can pipe to `less -R` if they want
				// a cleaner view.
				_, _ = fmt.Fprint(osStdout, "\033[2J\033[H")
			} else {
				_, _ = fmt.Fprintf(osStdout, "--- tick %d @ %s ---\n", tick, time.Now().UTC().Format(time.RFC3339))
			}
			if asJSON {
				_ = printBillingStatusJSON(osStdout, resp)
			} else {
				printBillingStatus(osStdout, resp)
			}
		}
		tick++
		if !time.Now().Before(deadline) {
			return 0
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(billingStatusTickInterval):
		}
	}
}

// printBillingStatusJSON marshals resp to osStdout + newline. Used
// by --watch --json so the operator can pipe to jq and watch the
// catalog fill per-tick.
func printBillingStatusJSON(w io.Writer, resp api.BillingCatalogResponse) error {
	b, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(w, string(b))
	return nil
}

// printBillingStatus renders the catalog as a tab-aligned table.
// The first column is "plan / kind" so the operator can scan a row
// per (plan, kind) pair; the second column is the provider-side
// handle; the third is the SyncedAt timestamp.
//
// An empty catalog (no hydration yet) renders the active provider,
// SyncedAt: never synced" header followed by a one-line hint to run
// `faas billing price-catalog sync`. We do not gate that hint on
// the response — the operator's CLI subcommand is the right place
// for actionable guidance.
func printBillingStatus(w io.Writer, resp api.BillingCatalogResponse) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	defer func() { _ = tw.Flush() }()

	_, _ = fmt.Fprintf(tw, "Provider:\t%s\n", resp.Provider)
	syncedAt := "never synced"
	if resp.SyncedAt != "" {
		syncedAt = resp.SyncedAt
	}
	_, _ = fmt.Fprintf(tw, "SyncedAt:\t%s\n", syncedAt)
	if len(resp.Entries) == 0 {
		_, _ = fmt.Fprintln(tw, "\nCatalog:\t<empty>")
		_, _ = fmt.Fprintln(tw, "\nRun `faas billing price-catalog sync` to hydrate.")
		return
	}
	_, _ = fmt.Fprintln(tw, "\nCatalog:")
	_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\n", "PLAN/KIND", "HANDLE", "SYNCED AT")
	for _, e := range resp.Entries {
		synced := GlyphEmDash
		if !e.SyncedAt.IsZero() && !e.SyncedAt.Equal(time.Time{}) {
			synced = e.SyncedAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		_, _ = fmt.Fprintf(tw, "  %s/%s\t%s\t%s\n", e.Plan, e.Kind, e.Handle, synced)
	}
}
