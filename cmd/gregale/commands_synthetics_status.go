package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// cmdSyntheticsStatus implements `gregale synthetics status --app <slug>
// <check-id>` (ADR-748): uptime, latency, and the most recent runs.
func cmdSyntheticsStatus(args []string) int {
	fs := newFlagSet("synthetics status", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale alert check status --app <slug> <check-id|name>", "synthetics")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	id, err := resolveSyntheticCheckRef(context.Background(), client, *slug, fs.Arg(0))
	if err != nil {
		return printErr("Could not fetch synthetic check", err)
	}
	check, err := client.GetSyntheticCheck(context.Background(), *slug, id)
	if err != nil {
		return printErr("Could not fetch synthetic check", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(check))
	}
	renderSyntheticStatus(osStdout, check)
	return 0
}

func renderSyntheticStatus(w io.Writer, c api.SyntheticCheckResponse) {
	_, _ = fmt.Fprintf(w, "%s — %s %s every %d minutes, expecting %s\n", c.Name, c.Method, c.URL, c.IntervalSeconds/60, describeExpected(c.ExpectedStatus))
	if !c.Enabled {
		_, _ = fmt.Fprintln(w, "  Paused.")
	}
	res := c.Results
	if res == nil || len(res.Recent) == 0 {
		_, _ = fmt.Fprintln(w, "  No runs yet; the first runs within one interval of creation.")
		return
	}
	_, _ = fmt.Fprintf(w, "  Uptime:   %s (24h, %d runs) · %s (7d)\n", fmtUptime(res.Uptime24hPct), res.Runs24h, fmtUptime(res.Uptime7dPct))
	// The p95 covers successful runs only and is 0 when there were none; a
	// failing check printed "p95 0 ms" (Hunt 12 H12-25).
	if res.P95LatencyMS24h > 0 {
		_, _ = fmt.Fprintf(w, "  Latency:  p95 %.0f ms over 24h of successful runs, including wakes\n", res.P95LatencyMS24h)
	} else {
		_, _ = fmt.Fprintln(w, "  Latency:  — (no successful runs in 24h)")
	}
	_, _ = fmt.Fprintln(w, "  Recent runs:")
	for _, run := range res.Recent {
		outcome := "ok"
		if !run.OK {
			outcome = "FAIL " + run.ErrorClass
		}
		status := "—"
		if run.StatusCode != 0 {
			status = fmt.Sprint(run.StatusCode)
		}
		_, _ = fmt.Fprintf(w, "    %s  %-14s  %-3s  %d ms\n", run.StartedAt, outcome, status, run.LatencyMS)
	}
}

func fmtUptime(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.2f%%", *v)
}

// resolveSyntheticCheckRef accepts a check's id or its name: `check list`
// shows names first and status refused them (production, 2026-11-09).
func resolveSyntheticCheckRef(ctx context.Context, client *api.Client, slug, ref string) (string, error) {
	if uuid.Validate(ref) == nil {
		return ref, nil
	}
	checks, err := client.ListSyntheticChecks(ctx, slug)
	if err != nil {
		return "", err
	}
	for _, c := range checks {
		if c.Name == ref {
			return c.ID, nil
		}
	}
	return "", fmt.Errorf("no check named %q on %s; see gregale alert check list --app %s", ref, slug, slug)
}
