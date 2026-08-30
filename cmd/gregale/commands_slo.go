// commands_slo.go — Move 2 PR-A: customer-facing CLI twins for
// GET /v1/apps/{slug}/slo and GET /v1/account/slo (issue #696 /
// ADR-082). Mirrors the structure of commands_metrics.go (the
// 5m-window dashboard panel) but swaps the `range` flag for the
// SLO `window` flag and the closed-set vocabulary is a strict
// subset of the /metrics one.
//
// Output shape mirrors the dashboard SLO card: window / as_of /
// source header, then a labelled block (not a table — the values
// are heterogeneous widths and latency percentiles read better as
// a `p50/p95/p99` triple).
//
// `--json` follows the global jsonOutput convention (json_flag.go):
// NDJSON for slices, indented JSON for scalars. The DTOs are
// AppSLOResponse / AccountSLOResponse (pkg/api/dto.go); no
// client-side reshaping.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
)

// sloCmdUsage is the top-of-failure-line shown for
// `gregale slo` errors. Mirrors PrintUsage's docs URL convention
// (output.go:144) so the line carries the stable docs site
// pointer.
const sloCmdUsage = "usage: gregale slo <slug> [--window 24h]"

// sloCmdDocsTopic is the docs topic slug passed to PrintUsage
// when PrintUsage emits the trailing "Docs:" row. Keeps the CLI's
// help line stable across command additions.
const sloCmdDocsTopic = "slo"

// sloAccountCmdUsage is the top-of-failure-line for
// `gregale account slo`.
const sloAccountCmdUsage = "usage: gregale account slo [--window 24h]"

// sloAccountCmdDocsTopic is the sibling docs topic for the
// account-scoped command.
const sloAccountCmdDocsTopic = "account-slo"

// cmdSLO implements `gregale slo <slug> [--window 24h]`. Mirrors
// the read shape of cmdMetrics (commands_metrics.go:44) — single
// positional slug + one flag, JSON single record, human
// multi-line detail block.
func cmdSLO(args []string) int {
	fs := flag.NewFlagSet("slo", flag.ContinueOnError)
	window := fs.String("window", "24h", "SLO window (1h, 24h, 7d)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, sloCmdUsage, sloCmdDocsTopic)
		return 1
	}
	slug := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	s, err := client.GetAppSLO(context.Background(), slug, *window)
	if err != nil {
		return printErr("Could not fetch SLO", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(s))
	}
	renderAppSLO(osStdout, s)
	return 0
}

// cmdAccountSLO implements `gregale account slo [--window 24h]`.
// No slug — the rollup is account-wide. Mirrors cmdSLO above.
func cmdAccountSLO(args []string) int {
	fs := flag.NewFlagSet("account slo", flag.ContinueOnError)
	window := fs.String("window", "24h", "SLO window (1h, 24h, 7d)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, sloAccountCmdUsage, sloAccountCmdDocsTopic)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	s, err := client.GetAccountSLO(context.Background(), *window)
	if err != nil {
		return printErr("Could not fetch account SLO", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(s))
	}
	renderAccountSLO(osStdout, s)
	return 0
}

// renderAppSLO writes the human-mode labelled block for an
// AppSLOResponse. Mirrors the dashboard SLO card so a customer
// toggling between terminal and browser sees the same numbers.
//
// When Source is "degraded: <reason>" we render a one-line warning
// before the values so the customer understands the zeroes are
// real (Prometheus isn't reachable), not a bug. The same line
// shape is used by the /metrics renderer in commands_metrics.go.
func renderAppSLO(w io.Writer, s api.AppSLOResponse) {
	if s.Source != "" && s.Source != appmetrics.SourcePrometheus {
		_, _ = fmt.Fprintf(w, "Note: source=%s (some values may be zero — Prometheus and/or Postgres may be unavailable)\n", s.Source)
	}
	_, _ = fmt.Fprintf(w, "App:         %s\n", s.AppID)
	_, _ = fmt.Fprintf(w, "Slug:        %s\n", s.AppSlug)
	_, _ = fmt.Fprintf(w, "Window:      %s\n", s.Window)
	if s.AsOf != "" {
		_, _ = fmt.Fprintf(w, "As of:       %s\n", s.AsOf)
	}
	if s.Source != "" {
		_, _ = fmt.Fprintf(w, "Source:      %s\n", s.Source)
	}
	_, _ = fmt.Fprintf(w, "Latency:     p50=%.1fms p95=%.1fms p99=%.1fms (2xx class)\n",
		s.RequestDuration.P50MS, s.RequestDuration.P95MS, s.RequestDuration.P99MS)
	_, _ = fmt.Fprintf(w, "Error rate:  %.2f%%\n", s.ErrorRatePct)
	_, _ = fmt.Fprintf(w, "Cold boot:   %.2f%%\n", s.ColdBootRatePct)
	_, _ = fmt.Fprintf(w, "Requests:    %d (total in window)\n", s.RequestsTotal)
	_, _ = fmt.Fprintf(w, "Throttled:   %d (rate-limited in window)\n", s.ThrottledTotal)
	if s.WakeQueueP95MS > 0 {
		_, _ = fmt.Fprintf(w, "Wake queue:  %.0fms p95 (fleet-wide)\n", s.WakeQueueP95MS)
	}
	_, _ = fmt.Fprintf(w, "Instance-h:  %.3f\n", s.InstanceHours)
	_, _ = fmt.Fprintf(w, "GB-hours:    %.4f\n", s.GBHours)
}

// renderAccountSLO is the account-scoped sibling of renderAppSLO.
// Same shape, no AppID/AppSlug, GBHours/InstanceHours are summed
// across the account.
func renderAccountSLO(w io.Writer, s api.AccountSLOResponse) {
	if s.Source != "" && s.Source != appmetrics.SourcePrometheus {
		_, _ = fmt.Fprintf(w, "Note: source=%s (some values may be zero — Prometheus and/or Postgres may be unavailable)\n", s.Source)
	}
	_, _ = fmt.Fprintf(w, "Window:      %s\n", s.Window)
	if s.AsOf != "" {
		_, _ = fmt.Fprintf(w, "As of:       %s\n", s.AsOf)
	}
	if s.Source != "" {
		_, _ = fmt.Fprintf(w, "Source:      %s\n", s.Source)
	}
	_, _ = fmt.Fprintf(w, "Latency:     p50=%.1fms p95=%.1fms p99=%.1fms (2xx class, fleet-wide)\n",
		s.RequestDuration.P50MS, s.RequestDuration.P95MS, s.RequestDuration.P99MS)
	_, _ = fmt.Fprintf(w, "Error rate:  %.2f%%\n", s.ErrorRatePct)
	_, _ = fmt.Fprintf(w, "Cold boot:   %.2f%%\n", s.ColdBootRatePct)
	_, _ = fmt.Fprintf(w, "Requests:    %d (total in window)\n", s.RequestsTotal)
	_, _ = fmt.Fprintf(w, "Throttled:   %d (rate-limited in window)\n", s.ThrottledTotal)
	if s.WakeQueueP95MS > 0 {
		_, _ = fmt.Fprintf(w, "Wake queue:  %.0fms p95 (fleet-wide)\n", s.WakeQueueP95MS)
	}
	_, _ = fmt.Fprintf(w, "Instance-h:  %.3f (sum across all apps)\n", s.InstanceHours)
	_, _ = fmt.Fprintf(w, "GB-hours:    %.4f (sum across all apps)\n", s.GBHours)
}
