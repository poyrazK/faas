package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDebugRunning renders the customer-facing answer to "why is this app
// still running?". Human output emphasizes observed causes; --json keeps the
// complete evidence shape for automation.
func cmdDebugRunning(args []string) int {
	fs := flag.NewFlagSet("debug running", flag.ContinueOnError)
	since := fs.String("since", "", "lookback window (e.g. 30m, 24h, 3d)")
	limit := fs.Int("limit", 20, "max recent observations (1..100)")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{"since": true, "limit": true})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale debug running [--since D] [--limit N] <slug>", debugCmdDocsTopic)
		return 1
	}
	if *limit < 1 || *limit > 100 {
		fmt.Fprintln(os.Stderr, "--limit must be between 1 and 100")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetAppDebugRunningWithLimit(context.Background(), positional[0], *since, *limit)
	if err != nil {
		return printErr("Could not explain why the app is running", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderDebugRunning(osStdout, positional[0], resp)
	return 0
}

func renderDebugRunning(w io.Writer, slug string, resp api.DebugRunningResponse) {
	fmt.Fprintf(w, "Why is %s running?\n", slug)
	if resp.CurrentObservedAt == "" {
		fmt.Fprintln(w, "No scheduler observation is available in the selected window.")
	} else {
		fmt.Fprintf(w, "Observed at %s\n", resp.CurrentObservedAt)
		if len(resp.Current) == 0 {
			fmt.Fprintln(w, "Current blockers: none observed")
		} else {
			fmt.Fprintln(w, "Current observed causes:")
			for _, cause := range resp.Current {
				fmt.Fprintf(w, "  - %s: %s\n", cause.Code, cause.Summary)
			}
		}
		if len(resp.History) > 0 && resp.History[0].Degraded {
			fmt.Fprintln(w, "Signal status: degraded (one or more scheduler signals were unavailable)")
		}
	}
	fmt.Fprintf(w, "Configuration: idle timeout %ds; configured minimum %d; effective minimum %d",
		resp.Config.IdleTimeoutSeconds, resp.Config.ConfiguredMinInstances, resp.Config.EffectiveMinInstances)
	if resp.Config.PrewarmMinInstances > 0 {
		fmt.Fprintf(w, "; temporary prewarm floor %d", resp.Config.PrewarmMinInstances)
	}
	fmt.Fprintln(w)
	if len(resp.History) == 0 {
		return
	}
	fmt.Fprintln(w, "Recent observations:")
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "OBSERVED AT\tCAUSES\tSTATUS")
	for _, observation := range resp.History {
		codes := make([]string, 0, len(observation.Causes))
		for _, cause := range observation.Causes {
			codes = append(codes, cause.Code)
		}
		status := "complete"
		if observation.Degraded {
			status = "degraded"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", observation.ObservedAt, strings.Join(codes, ", "), status)
	}
	_ = tw.Flush()
	if resp.HistoryTruncated {
		fmt.Fprintln(w, "History is truncated; narrow or widen --since to inspect another window.")
	}
}
