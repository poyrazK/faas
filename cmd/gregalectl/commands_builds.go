// commands_builds.go — operator-side build-recovery primitive.
//
// P2c (reclaim-stuck-build) CLI wrapper. Mirrors the `instances
// force-park` subcommand pattern (commands_instances.go:86):
// opens a state.Store via FAAS_PG_DSN + calls
// state.Store.SweepStuckRunningBuilds directly. The Store method
// is public (pkg/state/store.go:2370) and is also called by the
// in-process reaper at pkg/builderd/reaper.go:48 — standing up a
// builderd gRPC server for this one method would inflate review
// surface, so per user decision the CLI takes the direct path
// (matches the apid handler's behaviour).
//
// Audit row emission is the apid handler's job (the CLI is
// optional — typical on-call usage is via apid, which carries
// the admin actor identity through MFA + FAAS_ADMIN_EMAILS).
// When the CLI is used directly, no audit row is emitted; the
// operator's stdout trace is the only record. Instance recovery no
// longer makes this trade-off during routine operation; migrating this
// build command to the authenticated apid path is the remaining follow-up.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

const dispatchBuilds = "builds"

func cmdBuildsDispatch(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "gregalectl builds: missing subcommand; want sweep-stuck")
		return 2
	}
	switch args[0] {
	case "sweep-stuck":
		return cmdBuildsSweepStuck(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl builds: unknown subcommand %q\n", args[0])
		return 2
	}
}

func cmdBuildsSweepStuck(args []string) int {
	fs := flag.NewFlagSet("sweep-stuck", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	olderThan := fs.Duration("older-than", 15*time.Minute, "threshold (clamped to [1m, 60m] by apid; default 15m)")
	ack := fs.Bool("yes", false, "acknowledge that rows older than the threshold will be flipped to 'failed/timeout'")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*ack {
		fmt.Fprintln(os.Stderr, "gregalectl builds sweep-stuck: --yes required (rows older than the threshold will be flipped)")
		return 2
	}
	if *olderThan < time.Minute {
		fmt.Fprintln(os.Stderr, "gregalectl builds sweep-stuck: --older-than must be >= 1m")
		return 2
	}
	if *olderThan > time.Hour {
		fmt.Fprintln(os.Stderr, "gregalectl builds sweep-stuck: --older-than must be <= 1h")
		return 2
	}

	st, closeFn, err := computeNodesStoreOpener()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeFn()

	threshold := time.Now().Add(-*olderThan)
	swept, err := st.SweepStuckRunningBuilds(context.Background(), threshold)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl builds sweep-stuck:", err)
		return 1
	}
	//nolint:errcheck // final stdout write; best-effort status line
	fmt.Fprintf(os.Stdout, "swept=%d older_than=%s threshold_iso=%s\n",
		swept, olderThan.String(), threshold.UTC().Format(time.RFC3339))
	return 0
}
