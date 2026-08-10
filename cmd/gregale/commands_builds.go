package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// commands_builds.go — the `gregale build` parent command and its
// `provenance` subcommand (ADR-038, Tier 3 / issue #197 B3.10-read
// half). The parent is the dispatch entry point; future build-
// surface subcommands (`gregale build logs`, `gregale build sbom` from
// Phase 3) land here without touching main.go's switch.
//
// build id shape mirrors deploymentIDPattern: 32-hex UUID without
// dashes. The server-side `text -> uuid` cast accepts the unhyphenated
// form; the dashboard / CLI tooling expects the compact shape so
// customers can paste a row id from the dashboard into `gregale build
// provenance <id>` without manual fixing-up.
var buildIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// dispatchBuild is the registered parent command name. Mirrors the
// dispatchDeployments / dispatchDeployment convention; main.go
// dispatches `gregale build <subcommand>` via this constant.
const dispatchBuild = "build"

// cmdBuild is the parent dispatcher. With zero arguments it prints
// usage; with `provenance` (or future subcommands) it fans to the
// matching helper. Unknown subcommands print a usage hint + return 1
// so the customer's "gregale build provanence" typo surfaces
// immediately rather than silently invoking a sibling.
func cmdBuild(args []string) int {
	parent, _ := lookupCliCommand("build")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale build <subcommand> [flags]\n  subcommands: status, list, provenance, sbom", "build")
		return 1
	}
	switch args[0] {
	case statusLiteral:
		return cmdBuildStatus(args[1:])
	case "provenance":
		return cmdBuildProvenance(args[1:])
	case "sbom":
		return cmdBuildSbom(args[1:])
	case "list":
		return cmdBuildList(args[1:])
	default:
		sug, _ := suggestSubcommand(args[0], parent)
		fmt.Fprintf(os.Stderr, "gregale build: unknown subcommand %q (known: status, provenance, sbom, list)\n", args[0])
		maybeSuggestSub(sug)
		return 1
	}
}

// cmdBuildSbom streams the CycloneDX SBOM for a build id (issue
// #299 / ADR-038 Phase 3). `gregale build sbom <id>` writes the raw
// SBOM JSON to stdout so the operator can pipe it straight to
// `cyclonedx-cli validate` or to a CI artifact publisher. No -j
// flag — the SBOM IS the JSON; the binary form is the only shape
// we ever serve.
//
// Errors:
//   - 401/unauthenticated → "Not logged in"
//   - 404 build_provenance_not_found → "no SBOM for this build"
//     (Phase-3 populator hasn't landed; or build predates the
//     schema column)
//   - other 4xx/5xx → server-supplied code + message.
func cmdBuildSbom(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale build sbom <id>", "build")
		return 1
	}
	id := args[0]
	if !buildIDPattern.MatchString(id) {
		PrintUsage(os.Stderr, "usage: gregale build sbom <id>   (id is 32 hex chars)", "build")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	body, err := client.GetBuildsIdSbom(context.Background(), id)
	if err != nil {
		return printErr("Could not fetch build SBOM", err)
	}
	if len(body) == 0 {
		return printErr("Could not fetch build SBOM", fmt.Errorf("no SBOM for build %s (Phase-3 populator may not have run)", id))
	}
	// Write raw bytes — preserves formatting for downstream tools
	// (jq, cyclonedx-cli validate, etc.). Avoid Fprintf's string
	// conversion since the body may contain non-UTF-8 bytes
	// (CycloneDX's package-purl strings are pure UTF-8 today, but
	// binary SBOM consumers shouldn't depend on that).
	_, _ = osStdout.Write(body)
	return 0
}

// cmdBuildProvenance renders the ADR-038 build_provenance row for
// a build id. `gregale build provenance <id>` is the customer-facing
// shape; mirrors `gregale deployment <id>` for cross-resource parity.
// With -j/--json it emits the raw BuildProvenanceResponse JSON;
// without it it prints a one-line-per-field text summary so the
// terminal-friendly output (the platform's universal default) keeps
// flow.
//
// Errors:
//   - 401/unauthenticated → "Not logged in"
//   - 404 build_provenance_not_found → propagates as the SDK's
//     *APIError with code build_provenance_not_found (the populator
//     WARN-logged path; the build row itself may exist)
//   - 404 generic → "no such build"
//   - other 4xx/5xx → server-supplied code + message.
func cmdBuildProvenance(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale build provenance <id> [-j]", "build")
		return 1
	}
	id := args[0]
	if !buildIDPattern.MatchString(id) {
		PrintUsage(os.Stderr, "usage: gregale build provenance <id>   (id is 32 hex chars)", "build")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	p, err := client.GetBuildsIdProvenance(context.Background(), id)
	if err != nil {
		return printErr("Could not fetch build provenance", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(p))
	}
	printProvenance(osStdout, p)
	return 0
}

// printProvenance writes a label-aligned text view. Kept as a
// helper so a future `gregale build list` (not in this PR) can reuse
// the rendering without re-deriving the column list. `w` is an
// io.Writer so the test harness can capture with a *bytes.Buffer
// (memory note: captureStdout race under -race; safeBuffer vs bare
// *bytes.Buffer with mutex).
//
// Empty fields still render so a pre-Phase-3 cache-hit build shows
// blank buildkit_version rather than skipping the line — auditors
// want consistency. Field order is the column order of the DB
// table with the PK + build_id first, the two timestamps last.
func printProvenance(w io.Writer, p api.BuildProvenanceResponse) {
	rows := []struct {
		label, value string
	}{
		{"id", p.ID},
		{"build_id", p.BuildID},
		{"buildkit_version", p.BuildkitVer},
		{"railpack_version", p.RailpackVer},
		{"framework_version", p.FrameworkVer},
		{"base_digest", p.BaseDigest},
		{"source_sha256", p.SourceSHA256},
		{"source_url", p.SourceURL},
		{"commit_sha", p.CommitSHA},
		{"plan", p.Plan},
		{"runner_digest", p.RunnerDigest},
		{"builder_node_id", p.BuilderNodeID},
		{"started_at", p.StartedAt},
		{"finished_at", p.FinishedAt},
		{"sbom_storage_key", p.SBOMStorageKey},
	}
	for _, r := range rows {
		//nolint:errcheck // tabular printer writes to a typed writer; a failed Fprintf
		// at the tab stop is no different from a panic mid-row for the operator — both
		// show up as a malformed output and the CLI will exit non-zero on the parse below.
		fmt.Fprintf(w, "%-22s %s\n", r.label+":", r.value)
	}
}

// cmdBuildStatus renders the current status row for a build id
// (DEPLOY-PROV-6 / ADR-089, issue #741). `gregale build status <id>`
// is the customer-facing surface for CI scripts ("is my build
// done yet?") and replaces the one-shot pollDeploymentFinal
// fallback that streamDeployLogs uses today.
//
// With -j/--json it emits the raw BuildResponse JSON; without it
// prints a one-line-per-field text summary mirroring printProvenance.
//
// Errors:
//   - 401/unauthenticated → "Not logged in"
//   - 404 build_not_found  → "no such build"
//   - other 4xx/5xx → server-supplied code + message.
func cmdBuildStatus(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale build status <id> [-j]", "build")
		return 1
	}
	id := args[0]
	if !buildIDPattern.MatchString(id) {
		PrintUsage(os.Stderr, "usage: gregale build status <id>   (id is 32 hex chars)", "build")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	b, err := client.GetBuildsId(context.Background(), id)
	if err != nil {
		return printErr("Could not fetch build status", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(b))
	}
	printBuildStatus(osStdout, b)
	return 0
}

// printBuildStatus writes a label-aligned text view. Kept as a
// helper so a future `gregale build list` (not in this PR) can
// reuse the rendering without re-deriving the column list. `w`
// is an io.Writer so the test harness can capture with a
// *bytes.Buffer.
//
// Field order: PK + FKs first (id, deployment_id, kind), then
// the lifecycle bits customers parse off stdout (status,
// failure_class, duration_seconds), then the source-marker
// detail (source_bytes), then the three timestamps.
//
// Optional/lifecycle fields render as blank rows when empty
// rather than the Go zero value — mirrors printProvenance's
// "auditor wants consistency, not skipped rows" rule. The
// BuildResponse DTO uses `omitempty` for duration_seconds +
// started_at + finished_at, so a queued/running build arrives
// with `0` for duration_seconds (Go int zero) and `""` for
// the timestamps. Stringifying the int unconditionally would
// render a misleading `duration_seconds: 0` for a build that's
// been running for 30s.
func printBuildStatus(w io.Writer, b api.BuildResponse) {
	rows := []struct {
		label, value string
	}{
		{"id", b.ID},
		{"deployment_id", b.DeploymentID},
		{"kind", b.Kind},
		{"status", b.Status},
		{"failure_class", b.FailureClass},
		{"source_bytes", strconv.FormatInt(b.SourceBytes, 10)},
		{"enqueued_at", b.EnqueuedAt},
		{"started_at", b.StartedAt},
		{"finished_at", b.FinishedAt},
		// duration_seconds is rendered only when the build reached
		// a terminal status (server stamps 0 + omitempty otherwise).
		// Showing a literal `0` here would suggest "this build took
		// zero seconds" rather than "this build hasn't finished
		// yet" — both are valid interpretations of the JSON shape,
		// but only one is the auditor-friendly one.
		{"duration_seconds", durationSecondsForDisplay(b)},
	}
	for _, r := range rows {
		//nolint:errcheck // tabular printer writes to a typed writer; a failed Fprintf
		// at the tab stop is no different from a panic mid-row for the operator — both
		// show up as a malformed output and the CLI will exit non-zero on the parse below.
		fmt.Fprintf(w, "%-22s %s\n", r.label+":", r.value)
	}
}

// durationSecondsForDisplay returns "" when the build is still
// queued or running (server omits the field via omitempty, leaving
// the Go int zero) and the integer string when terminal. Mirrors
// the "blank row, not zero row" rule printProvenance follows for
// the buildkit_version + framework_version columns (DEPLOY-PROV-5,
// PR #736).
func durationSecondsForDisplay(b api.BuildResponse) string {
	if b.Status != buildStatusSucceeded && b.Status != buildStatusFailed {
		return ""
	}
	return strconv.Itoa(b.DurationSeconds)
}

// buildListRowFmt is the human list-table column layout (DEPLOY-PROV-6
// follow-up / ADR-091, issue #741 close-out). 6 columns: ID
// (32-hex prefix), DEPLOY (32-hex prefix), STATUS (10), KIND (10),
// SOURCE_BYTES (right-aligned 10), STARTED (RFC3339). Mirrors the
// deployment row format string's column count so a customer
// pasting both into a spreadsheet sees the same shape.
const buildListRowFmt = "%-32s %-32s %-10s %-10s %10s %s\n"

// renderBuildListRow writes one build row to w. The Fprintf
// return is intentionally discarded (matching renderDeploymentRow's
// convention): writer failures (closed pipe, broken TTY) are
// unrecoverable here.
func renderBuildListRow(w io.Writer, b api.BuildResponse) {
	started := b.StartedAt
	if started == "" {
		// Queued builds have no started_at — surface the dash
		// explicitly so the column stays aligned.
		started = GlyphEmDash
	}
	_, _ = fmt.Fprintf(w, buildListRowFmt,
		b.ID, b.DeploymentID, b.Status, b.Kind,
		strconv.FormatInt(b.SourceBytes, 10), started)
}

// cmdBuildList implements `gregale build list [--app SLUG] [--status S] [--limit N] [--before C] [--all]`.
// Wire shape: GET /v1/builds. Mirrors cmdDeployments exactly
// (commands_deployments.go:65) — pagination defaults to 50,
// --all walks every page via Client.GetBuildsAll.
//
// Filter validation: status must be one of queued|running|succeeded|
// failed (matches the API's CHECK constraint + the BuildStatus*
// constants in pkg/api/dto.go). Bad values → usage hint, exit 1,
// no API round-trip.
//
// JSON output emits the page envelope (items + next_before) so
// automation can re-issue the cursor — mirrors cmdDeployments'
// deliberate break from the apps/crons/keys NDJSON convention.
func cmdBuildList(args []string) int {
	fs := flag.NewFlagSet("build-list", flag.ContinueOnError)
	app := fs.String("app", "", "filter to one app slug")
	status := fs.String("status", "", "filter to status (queued|running|succeeded|failed)")
	limit := fs.Int("limit", 50, "page size (1-200)")
	before := fs.String("before", "", "pagination cursor (opaque token from NextBefore)")
	all := fs.Bool("all", false, "walk every page (ignores --limit/--before)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale build list [--app SLUG] [--status S] [--limit N] [--before C] [--all]", "build")
		return 1
	}
	if *status != "" {
		switch *status {
		case api.BuildStatusQueued, api.BuildStatusRunning,
			api.BuildStatusSucceeded, api.BuildStatusFailed:
			// ok
		default:
			PrintUsage(os.Stderr, "usage: gregale build list --status (queued|running|succeeded|failed)", "build")
			return 1
		}
	}
	// Strict range check (post-review fix): the server defaults limit=50
	// when 0 is passed, but the help text says "1-200" — accepting 0
	// silently would be a UX papercut. --limit 0 is a usage error so
	// callers either pick the default explicitly (omit --limit) or
	// pick a real page size.
	if *limit < 1 || *limit > 200 {
		PrintUsage(os.Stderr, "usage: gregale build list --limit N (1 <= N <= 200)", "build")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if *all {
		return cmdBuildListAll(ctx, client, *app, *status)
	}
	page, err := client.GetBuilds(ctx, *app, *status, *before, *limit)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		// Envelope (not NDJSON) so `next_before` survives; see file header.
		return jsonOut(writeJSON(page))
	}
	if len(page.Items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No builds match.")
		return 0
	}
	for _, b := range page.Items {
		renderBuildListRow(osStdout, b)
	}
	if page.NextBefore != "" {
		// Em-dash (U+2014) matches cmdDeployments' cursor hint
		// byte-for-byte — tests pin this in commands_builds_test.go.
		_, _ = fmt.Fprintf(osStdout, "... more — pass --before %s\n", page.NextBefore)
	}
	return 0
}

// cmdBuildListAll walks every page via the SDK helper and renders
// the full list. Refuses to share a single envelope with the
// one-page path (no `next_before` to surface), so JSON output is
// the bare slice — matching how deployments / apps / crons emit
// NDJSON for non-paginated lists. Mirrors cmdDeploymentsAll
// (commands_deployments.go:114-134) exactly.
func cmdBuildListAll(ctx context.Context, client *api.Client, app, status string) int {
	items, err := client.GetBuildsAll(ctx, app, status)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(items))
	}
	if len(items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No builds match.")
		return 0
	}
	for _, b := range items {
		renderBuildListRow(osStdout, b)
	}
	return 0
}
