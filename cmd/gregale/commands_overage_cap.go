// commands_overage_cap.go — `gregale overage-cap` (Tier B audit gap).
// PATCH /v1/account/overage-cap lets the customer set or clear the
// per-account overage cap (€0.01/GB-h above the plan's included GB-h).
// schedd refuses new wakes once the current-month overage meets the
// cap; the CLI is the only path to flip the cap outside the dashboard.
//
// Auth: self (no admin scope required).

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
)

func cmdOverageCap(args []string) int {
	// Two equivalent forms:
	//   gregale overage-cap <cents>          (positional cents, >= 0)
	//   gregale overage-cap --clear          (boolean flag, no positional)
	// The previous design encoded `--clear` as a positional sentinel
	// (`<cents>|--clear`) but Go's flag.Parse eats any token starting
	// with `-` (except literal `--`) before our arg handler sees it,
	// so a user typing `gregale overage-cap --clear` got
	// "flag provided but not defined: -clear" and exit 1. A real
	// `--clear` flag is the idiomatic fix; the positional variant
	// stays for symmetry with `<cents>`.
	fs := newFlagSet("overage-cap", flag.ContinueOnError)
	clear := fs.Bool("clear", false, "clear the per-account overage cap (no limit)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *clear && fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale overage-cap [--clear] [<cents>]", "overage-cap")
		return 1
	}
	if !*clear && fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale overage-cap [--clear] [<cents>]", "overage-cap")
		return 1
	}
	var cap *int64
	if *clear {
		cap = nil
	} else {
		n, err := strconv.ParseInt(fs.Arg(0), 10, 64)
		if err != nil || n < 0 {
			return printErr("Invalid cap", fmt.Errorf("cents must be a non-negative integer; got %q", fs.Arg(0)))
		}
		cap = &n
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	acct, err := client.RaiseOverageCap(context.Background(), cap)
	if err != nil {
		return printErr("Set overage-cap failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(acct))
	}
	if cap == nil {
		PrintOK(osStdout, "Overage cap cleared (no limit).")
	} else {
		PrintOK(osStdout, "Overage cap set to %d cents (€%.2f).", *cap, float64(*cap)/100)
	}
	return 0
}
