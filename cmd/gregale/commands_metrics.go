// commands_metrics.go — Move 1 PR-A: customer-facing CLI twin for
// GET /v1/apps/{slug}/metrics (issue #273 / ADR-042).
//
// The HTTP endpoint has shipped since the dashboard work (cmd/apid
// renders the same data at /dashboard/apps/{slug}), but the CLI had
// no entry point. A customer in their editor hits a 30-second
// latency regression and has to context-switch into a browser tab;
// this command lands that read in the terminal where the rest of
// the debugging already happens.
//
// Output shape mirrors the dashboard panel: range / as_of / source
// header, then a labelled block (not a table — the values are
// heterogeneous widths and labels are clearer than columns here).
//
// `--json` follows the global jsonOutput convention (json_flag.go):
// NDJSON for slices, indented JSON for scalars. The DTO is
// AppMetricsResponse (pkg/api/dto.go:1087); no client-side reshaping.
//
// Tier C extension: --account flips to GET /v1/apps/metrics (the
// account-wide rollup, issue #393) and renders the per-slug map.
// --account is mutually exclusive with the positional <slug>.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
)

// metricsCmdUsage is the top-of-failure-line shown for `gregale metrics`
// errors. Mirrors PrintUsage's docs URL convention (output.go:144) so
// the line carries the stable docs site pointer.
const metricsCmdUsage = "usage: gregale metrics <slug> [--range 5m] | --account [--range 5m]"

// metricsCmdDocsTopic is the docs topic slug passed to PrintUsage
// when PrintUsage emits the trailing "Docs:" row. Keeps the CLI's
// help line stable across command additions.
const metricsCmdDocsTopic = "metrics"

// cmdMetrics implements `gregale metrics <slug> [--range 5m]` and
// `gregale metrics --account [--range 5m]`. Mirrors the read shape
// of cmdDeployment (commands_deployments.go:139) — single positional
// slug + a few flags, JSON single record, human multi-line detail
// block. --account (Tier C) hits the account-wide rollup endpoint
// instead and renders one labelled block per app.
func cmdMetrics(args []string) int {
	fs := flag.NewFlagSet("metrics", flag.ContinueOnError)
	rng := fs.String("range", "5m", "time window (5m, 15m, 1h, 6h, 24h)")
	account := fs.Bool("account", false, "account-wide rollup (GET /v1/apps/metrics) — mutually exclusive with <slug>")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *account && fs.NArg() != 0 {
		PrintUsage(os.Stderr, metricsCmdUsage, metricsCmdDocsTopic)
		return 1
	}
	if !*account && fs.NArg() != 1 {
		PrintUsage(os.Stderr, metricsCmdUsage, metricsCmdDocsTopic)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *account {
		m, err := client.GetAppsMetrics(context.Background(), *rng)
		if err != nil {
			return printErr("Could not fetch metrics", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(m))
		}
		renderAppsMetrics(osStdout, m)
		return 0
	}
	slug := fs.Arg(0)
	m, err := client.GetAppMetrics(context.Background(), slug, *rng)
	if err != nil {
		return printErr("Could not fetch metrics", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(m))
	}
	renderAppMetrics(osStdout, m)
	return 0
}

// renderAppMetrics writes the human-mode labelled block for an
// AppMetricsResponse. Mirrors the dashboard panel (cmd/apid renders
// the same fields) so a customer toggling between terminal and
// browser sees the same numbers.
//
// When Source is "degraded: <reason>" we render a one-line warning
// before the values so the customer understands the zeroes are
// real (Prometheus isn't reachable), not a bug.
func renderAppMetrics(w io.Writer, m api.AppMetricsResponse) {
	if m.Source != "" && m.Source != appmetrics.SourcePrometheus {
		_, _ = fmt.Fprintf(w, "Note: source=%s (values below are zero — Prometheus is unavailable)\n", m.Source)
	}
	_, _ = fmt.Fprintf(w, "App:        %s\n", m.AppID)
	_, _ = fmt.Fprintf(w, "Range:      %s\n", m.Range)
	if m.AsOf != "" {
		_, _ = fmt.Fprintf(w, "As of:      %s\n", m.AsOf)
	}
	if m.Source != "" {
		_, _ = fmt.Fprintf(w, "Source:     %s\n", m.Source)
	}
	_, _ = fmt.Fprintf(w, "Requests:   %d (in window)\n", m.RequestCount)
	_, _ = fmt.Fprintf(w, "Latency:    p50=%.1fms p95=%.1fms p99=%.1fms\n", m.LatencyP50MS, m.LatencyP95MS, m.LatencyP99MS)
	_, _ = fmt.Fprintf(w, "Error rate: %.2f%%\n", m.ErrorRatePct)
	_, _ = fmt.Fprintf(w, "Cold boot:  %.2f%%\n", m.ColdStartPct)
	if m.WakeP95MS > 0 {
		_, _ = fmt.Fprintf(w, "Wake p95:   %.0fms (fleet-wide)\n", m.WakeP95MS)
	}
}

// renderAppsMetrics writes the human-mode labelled block for an
// account-wide AppsMetricsResponse (issue #393). One range/as_of/
// source header, then one App: <slug> block per row in the Apps
// map. Sort order follows the dashboard's table view — alphabetical
// by slug — so terminal output is stable across calls.
//
// When Source is degraded we render the warning once at the top so
// every zero the operator sees below is interpreted correctly
// (Prometheus isn't reachable, not a customer app bug).
func renderAppsMetrics(w io.Writer, m api.AppsMetricsResponse) {
	if m.Source != "" && m.Source != appmetrics.SourcePrometheus {
		_, _ = fmt.Fprintf(w, "Note: source=%s (values below are zero — Prometheus is unavailable)\n", m.Source)
	}
	_, _ = fmt.Fprintf(w, "Range:      %s\n", m.Range)
	if m.AsOf != "" {
		_, _ = fmt.Fprintf(w, "As of:      %s\n", m.AsOf)
	}
	if m.Source != "" {
		_, _ = fmt.Fprintf(w, "Source:     %s\n", m.Source)
	}
	if len(m.Apps) == 0 {
		_, _ = fmt.Fprintln(w, "(no apps with metrics in window)")
		return
	}
	slugs := make([]string, 0, len(m.Apps))
	for s := range m.Apps {
		slugs = append(slugs, s)
	}
	sortStrings(slugs)
	for _, s := range slugs {
		row := m.Apps[s]
		_, _ = fmt.Fprintf(w, "\nApp:        %s\n", s)
		_, _ = fmt.Fprintf(w, "  Requests:   %d\n", row.RequestCount)
		_, _ = fmt.Fprintf(w, "  Latency:    p50=%.1fms p95=%.1fms p99=%.1fms\n", row.LatencyP50MS, row.LatencyP95MS, row.LatencyP99MS)
		_, _ = fmt.Fprintf(w, "  Error rate: %.2f%%\n", row.ErrorRatePct)
		_, _ = fmt.Fprintf(w, "  Cold boot:  %.2f%%\n", row.ColdStartPct)
	}
}

// sortStrings delegates to stdlib sort.Strings (pdqsort, O(n log n)).
// Extracted so renderAppsMetrics reads cleanly — the wrapper is one
// line vs. the call site's naked sort.Strings(xs) which would scan
// the diff for "why is sort imported here?"
func sortStrings(xs []string) { sort.Strings(xs) }

// throttleSuggestionsCmdUsage is the top-of-failure-line shown for
// `gregale throttle-suggestions` errors. Mirrors the metricsCmdUsage
// pattern (commands_metrics.go:39) so the help text is stable
// across command additions.
const throttleSuggestionsCmdUsage = "usage: gregale throttle-suggestions <slug> [--range 5m] [--dry-run --candidate-rps N --candidate-burst N]"

// throttleSuggestionsCmdDocsTopic is the docs topic slug appended
// when PrintUsage emits the trailing "Docs:" row.
const throttleSuggestionsCmdDocsTopic = "throttle-suggestions"

// cmdThrottleSuggestions implements
// `gregale throttle-suggestions <slug> [--range 5m]
// [--dry-run --candidate-rps N --candidate-burst N]` (ADR-091
// D20.5 amendment 5 inline + ADR-104 amendment 5, issue #881
// Phase 4 D2). The base command is the read-only recommender
// (Phase 1+2+3 behaviour, byte-identical to GET /v1/apps/{slug}/
// throttle-suggestions). --dry-run flips the server into the
// preview pass: one extra PromQL per route counts sub-windows
// where observed rate exceeded the candidate. The customer uses
// this as a guard-rail BEFORE committing a throttle rule — not
// auto-apply (the recommender remains INTENTIONALLY ADVICE-ONLY).
//
// Validation mirrors cmd/apid/handlers_metrics.go: dry_run=true
// requires a positive --candidate-rps; --candidate-burst is
// optional (defaults to 0). The CLI rejects negative values
// locally so a malformed command doesn't waste an HTTP round-trip.
func cmdThrottleSuggestions(args []string) int {
	fs := flag.NewFlagSet("throttle-suggestions", flag.ContinueOnError)
	rng := fs.String("range", "5m", "time window (5m, 15m, 1h, 6h, 24h)")
	dryRun := fs.Bool("dry-run", false, "preview pass: ask the server to count sub-windows where observed rps exceeds --candidate-rps")
	candidateRPS := fs.Float64("candidate-rps", 0, "candidate rps (required when --dry-run; positive float)")
	candidateBurst := fs.Int("candidate-burst", 0, "candidate burst (optional when --dry-run; non-negative int)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, throttleSuggestionsCmdUsage, throttleSuggestionsCmdDocsTopic)
		return 1
	}
	// Local validation — fail fast before the HTTP round-trip so a
	// malformed command doesn't burn an auth + Prometheus budget.
	if *dryRun {
		if *candidateRPS <= 0 {
			PrintUsage(os.Stderr, throttleSuggestionsCmdUsage+"\nerror: --dry-run requires --candidate-rps > 0", throttleSuggestionsCmdDocsTopic)
			return 1
		}
		if *candidateBurst < 0 {
			PrintUsage(os.Stderr, throttleSuggestionsCmdUsage+"\nerror: --candidate-burst must be >= 0", throttleSuggestionsCmdDocsTopic)
			return 1
		}
	} else {
		// Without --dry-run the candidates are ignored; warn if
		// the user set them anyway so they don't get silent
		// acceptance of a probe that produced no preview.
		if *candidateRPS > 0 || *candidateBurst > 0 {
			_, _ = fmt.Fprintln(os.Stderr, "warning: --candidate-rps / --candidate-burst are ignored without --dry-run")
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	slug := fs.Arg(0)
	resp, err := client.GetAppThrottleSuggestionsOpts(context.Background(), slug, *rng, api.ThrottleSuggestionsOpts{
		DryRun:         *dryRun,
		CandidateRPS:   *candidateRPS,
		CandidateBurst: *candidateBurst,
	})
	if err != nil {
		return printErr("Could not fetch throttle suggestions", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderThrottleSuggestions(osStdout, resp)
	return 0
}

// renderThrottleSuggestions writes the human-mode labelled block
// for a ThrottleSuggestionsResponse. The recommendation slice
// mirrors the dashboard's suggestions card (route / observed /
// suggested rps / suggested burst). When DryRun=true, a "Dry-run
// preview" section follows with one line per route showing the
// would-have-rejected count + the per-consumer limitation note
// (gateway_requests_by_route_total has no per-consumer labels
// today; see ADR-104 amendment 5). Empty suggestions / empty
// preview render as "(none)" so a customer reading the output
// doesn't mistake silence for a bug.
func renderThrottleSuggestions(w io.Writer, r api.ThrottleSuggestionsResponse) {
	if r.Source != "" && r.Source != "prometheus" {
		_, _ = fmt.Fprintf(w, "Note: source=%s (values below may be zero — Prometheus is unavailable)\n", r.Source)
	}
	_, _ = fmt.Fprintf(w, "App:           %s\n", r.AppID)
	_, _ = fmt.Fprintf(w, "Range:         %s\n", r.Range)
	if r.AsOf != "" {
		_, _ = fmt.Fprintf(w, "As of:         %s\n", r.AsOf)
	}
	if r.Source != "" {
		_, _ = fmt.Fprintf(w, "Source:        %s\n", r.Source)
	}
	_, _ = fmt.Fprintf(w, "Plan ceiling:  rps=%d burst=%d\n", r.PlanCeilingRPS, r.PlanCeilingBurst)
	_, _ = fmt.Fprintf(w, "Multiplier:    %.1fx\n", r.Multiplier)
	if r.RouteMetricsDisabled {
		_, _ = fmt.Fprintln(w, "(route metrics disabled on this app — Free plan)")
		return
	}
	if r.RoutesCollapsed > 0 {
		_, _ = fmt.Fprintf(w, "Collapsed:     %d routes beyond RouteMetricsPerAppCap (covered by __route_other__)\n", r.RoutesCollapsed)
	}
	if len(r.Suggestions) == 0 {
		_, _ = fmt.Fprintln(w, "\nSuggestions:   (none)")
	} else {
		_, _ = fmt.Fprintln(w, "\nSuggestions:")
		for _, s := range r.Suggestions {
			_, _ = fmt.Fprintf(w, "  %-40s observed=%-6.2f rps  suggested=%-6.2f rps (burst=%d)\n",
				s.Route, s.ObservedRPS, s.SuggestedRPS, s.SuggestedBurst)
		}
	}
	if !r.DryRun {
		return
	}
	// Dry-run preview section (ADR-104 amendment 5, issue #881
	// Phase 4 D2). The candidate echo + per-consumer limitation
	// note appear FIRST so a customer reading the dry-run output
	// knows what the preview is and isn't before scanning the
	// per-route counts.
	_, _ = fmt.Fprintf(w, "\nDry-run preview (candidate rps=%.2f burst=%d):\n", r.CandidateRPS, r.CandidateBurst)
	if r.PerConsumerLimitNote != "" {
		_, _ = fmt.Fprintf(w, "  Note: %s\n", r.PerConsumerLimitNote)
	}
	if len(r.WouldHaveRejected) == 0 {
		_, _ = fmt.Fprintln(w, "  Would have rejected: (no per-route over-cap counts — degraded source or empty window)")
		return
	}
	_, _ = fmt.Fprintln(w, "  Would have rejected:")
	for _, p := range r.WouldHaveRejected {
		_, _ = fmt.Fprintf(w, "    %-40s over-cap count=%.2f\n", p.Route, p.OverCapCount)
	}
}
