// `gregale deploys show <id>` — read-only post-stream stage summary
// (ADR-117 companion to `gregale deploy --repo OWNER/NAME`).
//
// The live deploy path emits `event: stage` SSE frames inside
// GET /v1/deployments/{id}/logs while the deploy is running. Once the
// stream closes the terminal status (live / failed / superseded) is
// the only thing left visible to the operator. This command reads
// the closed 6-stage `deployments.stage_state` jsonb column via
// GET /v1/deployments/{id}/stages and renders it as a static block
// the operator can paste into a post-mortem or hand to support.
//
// Why a NEW top-level verb (`deploys`, not `deployment <id> show`):
//
//   - The singular `deployment <id>` verb is already a flat GET with
//     flag-shaped drill-downs (--show-scan, --show-secret-scan). A
//     sub-subcommand (`deployment <id> show --stages`) would force a
//     new subcommand layer on top of the existing positional-id parse
//     and split the human-table formatter across two files.
//   - The plural `deployments` verb is the paginated list. Adding a
//     `show` subcommand there would shadow `deployments` from being
//     usable as a bare list (today's `gregale deployments` returns the
//     page; a `show` subcommand would force `deployments list`).
//   - `deploys` is the noun-form cluster verb (mirrors `apps`/
//     `inspect`); it's a fresh entry in the usage block + dispatch
//     table, no shadowing.
//
// Scope is intentionally narrow:
//
//   - ONE positional arg, the 32-hex deployment id (same shape
//     as `deployment <id>`; same `deploymentIDPattern` regex gate).
//   - TWO subcommands today (`show`, `status`). Future read-only
//     drill-downs (timeline, events) land as siblings here, not as
//     flags on `deployment <id>`.
//   - NO `--follow` flag (decision recorded in
//     `cmd/gregale/deploy_stages.go::renderDeploySummary` is a
//     static post-stream renderer). Live stream lives in
//     `gregale deploy` itself.
//
// The wire shape comes back as raw `json.RawMessage` (the SDK method
// cannot import `pkg/state` directly because `pkg/state/memstore.go`
// imports `pkg/api` — see `pkg/api/client.go` GetDeploymentStages doc).
// We unmarshal into `state.StageState` here where the import direction
// is allowed (`cmd/gregale` already imports both packages).
//
// A1 (ADR-117 v2 follow-on) adds:
//   - `--status` flag on `deploys show <id>` — fans out via
//     errgroup to fetch /v1/deployments/{id} + /stages in parallel,
//     then renders the close-6-stage block with the footer
//     ("live since …" / "failed at …" / "<status> at …").
//   - `deploys status <id>` — same fan-out, always with the
//     footer. Pinned for ticket parity with the customer-facing
//     "where is my deploy?" question.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"golang.org/x/sync/errgroup"
)

// deploysShowUsage is the canonical CLI usage string for the
// `show` subcommand. Centralized so the cmdDeploys dispatcher and
// the cli_meta.go cliCommand entry reuse the same wording.
const deploysShowUsage = "usage: gregale deploys show <id> [--status] [--json] [--url]"

// deploysStatusUsage mirrors deploysShowUsage for the `status`
// subcommand. The wording intentionally distinguishes "show" (stage
// table only) from "status" (stage table + terminal footer) so the
// operator knows which verb carries the footer without reading the
// help body.
const deploysStatusUsage = "usage: gregale deploys status <id> [--json]"

// cmdDeploysShow implements `gregale deploys show <id> [--status]
// [--json] [--url]`. The --url flag is the issue #976 / ADR-122 /
// SAFE-RELEASES-C.3 surface; it short-circuits to print ONLY the
// per-deployment preview URL.
//
// Wire call: GET /v1/deployments/{id}/stages (returns the raw
// deployments.stage_state jsonb verbatim; CLI is the typed-shape owner).
// When --status is set, an additional GET /v1/deployments/{id}
// round-trip fetches the deployment row's status field so the
// footer ("live since …" / "failed at …" / "<status> at …") can be
// emitted. The two round-trips happen in parallel via errgroup.
//
// Render path:
//   - --url                            → ONLY the per-deployment preview
//     URL (single line, pipe-friendly). When --url is passed,
//     nothing else is printed and the stage summary is
//     skipped — this flag exists for shell scripting
//     (`grep` / `xargs` / `$EDITOR` "open" commands). When the
//     deployment isn't preview-active, prints an empty line so
//     shell chains can branch on `wc -c`.
//   - --json (or FAAS_JSON=1)        → indented JSON of stage_state.
//   - stdout is TTY + no --json      → closed 6-row block via
//     renderDeploySummary.
//   - stdout is pipe / NO_COLOR set  → same closed 6-row block
//     (no ANSI redraw, just plain print); output.Enabled() is
//     the single source of truth.
//
// Flag-position tolerance (review finding C4): stdlib
// flag.NewFlagSet stops parsing at the first positional, so the
// natural `gregale deploys show <id> --status` would otherwise
// leave `--status` unparsed and trip the NArg()==2 usage error.
// The splitFlagArgs helper reorders the argv so flags come
// before the positional regardless of input order — the operator
// sees the same behaviour whether they write
// `gregale deploys show --status <id>` or
// `gregale deploys show <id> --status`.
func cmdDeploysShow(args []string) int {
	fs := flag.NewFlagSet("deploys show", flag.ContinueOnError)
	withStatus := fs.Bool("status", false, "include terminal-status footer (live since / failed at)")
	urlOnly := fs.Bool("url", false, "print only the per-deployment preview URL (shell-friendly)")
	reordered := splitFlagArgs(args)
	if err := fs.Parse(reordered); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, deploysShowUsage, "deploys")
		return 1
	}
	id := fs.Arg(0)
	if !deploymentIDPattern.MatchString(id) {
		PrintUsage(os.Stderr, deploysShowUsage+"   (id is 32 hex chars)", "deploys")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()

	// --url is a short-circuit branch: fetch the preview URL,
	// print ONLY the URL line, exit. Skips the stage summary
	// fetch entirely because shell consumers don't want the
	// stage payload on stderr-tty terminal-coding pipelines.
	if *urlOnly {
		u, err := client.GetDeploymentURL(ctx, id)
		if err != nil {
			return printErr("Could not fetch deployment preview URL", err)
		}
		// Print URL on stdout OR an empty line when the
		// deployment isn't preview-active / the zone is
		// disabled. Shell consumers branch on `wc -c`.
		_, _ = fmt.Fprintln(osStdout, u.URL)
		// Exit 0 either way: an empty URL line means the
		// deployment simply isn't previewable right now
		// (failed/superseded), which is a valid operator
		// answer — not a wire error. The non-zero exit was
		// rejecting deliberate probes earlier (mirror of the
		// dashboard's "preview closed" chip).
		return 0
	}

	// Fetch the stage_state (always required) and, when --status
	// is set, the deployment row (for the footer). The two
	// round-trips are parallelized via errgroup so the latency
	// budget is max(stages, deployment) not sum — both endpoints
	// hit loopback apid in production, so the fan-out cuts a
	// perceptible second off the wall clock under slack.
	raw, dep, err := fetchDeploySummaryInputs(ctx, client, id, *withStatus)
	if err != nil {
		return printErr("Could not fetch deployment summary", err)
	}
	var ss state.StageState
	if err := json.Unmarshal(raw, &ss); err != nil {
		// Should never happen — apid re-emits the raw jsonb that
		// the column CHECK constraint already validated. If it
		// does, the server and the CLI typed struct drifted apart
		// (the column gained a field the CLI doesn't know).
		// Surface as 1 + a clear error so the operator can paste
		// the raw bytes back to engineering.
		return printErr("Could not decode stage_state (CLI/server shape drift?)", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(ss))
	}

	// Derive the footer inputs. No --status → status="", terminalAt
	// is zero; renderDeploySummary handles the empty-string short
	// circuits so this still renders correctly for in-flight
	// deployments (footer prints nothing past the Total line).
	status := ""
	var terminalAt time.Time
	if *withStatus && dep != nil {
		status = dep.Status
		var createdAt time.Time
		// dep.CreatedAt is wire-formatted (RFC3339Nano); parse
		// best-effort and fall through to the zero value on a
		// parse error (caller's terminalAt.IsZero() gate skips
		// the footer). The string is server-side canonically
		// formatted so this should not happen in practice.
		if dep.CreatedAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, dep.CreatedAt); err == nil {
				createdAt = t
			}
		}
		terminalAt = deriveTerminalAt(ss, status, createdAt)
	}
	if err := renderDeploySummary(osStdout, ss, status, terminalAt); err != nil {
		// Render failures (closed-set drift, broken pipe) are
		// logged at WARN — the API call succeeded so the operator
		// can re-run with --json to get the raw bytes back. Exit
		// 0 because the wire call was authoritative.
		_, _ = fmt.Fprintf(os.Stderr, "warning: stage summary render failed: %v\n", err)
	}
	if *withStatus && dep != nil {
		renderDeployFailureIfPresent(osStdout, *dep)
	}
	return 0
}

// cmdDeploysStatus implements `gregale deploys status <id> [--json]`.
//
// Always renders the closed-6-stage block with the terminal-status
// footer. The wire fan-out is the same as `deploys show --status`
// (errgroup over getDeployment + getDeploymentStages); the only
// surface difference is the absence of the --status flag toggle —
// the operator's intent ("show me the deploy's terminal state") is
// encoded in the verb name, not a flag.
//
// Mirrors cmdDeploysShow to within a handful of lines so the two
// subcommands share the same parser, fetch, and render path. The
// shared helper fetchDeploySummaryInputs keeps the errgroup
// wiring in one place.
func cmdDeploysStatus(args []string) int {
	fs := flag.NewFlagSet("deploys status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, deploysStatusUsage, "deploys")
		return 1
	}
	id := fs.Arg(0)
	if !deploymentIDPattern.MatchString(id) {
		PrintUsage(os.Stderr, deploysStatusUsage+"   (id is 32 hex chars)", "deploys")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	raw, dep, err := fetchDeploySummaryInputs(ctx, client, id, wantDeploySummaryFooter)
	if err != nil {
		return printErr("Could not fetch deployment status", err)
	}
	var ss state.StageState
	if err := json.Unmarshal(raw, &ss); err != nil {
		return printErr("Could not decode stage_state (CLI/server shape drift?)", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(ss))
	}
	status := dep.Status
	var createdAt time.Time
	// dep.CreatedAt is wire-formatted (RFC3339Nano); parse
	// best-effort. The string is server-side canonically
	// formatted so a parse error is unexpected — fall through
	// to the zero value on error (caller's terminalAt.IsZero()
	// gate skips the footer for the superseded branch).
	if dep.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, dep.CreatedAt); err == nil {
			createdAt = t
		}
	}
	terminalAt := deriveTerminalAt(ss, status, createdAt)
	if err := renderDeploySummary(osStdout, ss, status, terminalAt); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "warning: stage summary render failed: %v\n", err)
	}
	renderDeployFailureIfPresent(osStdout, *dep)
	return 0
}

// renderDeployFailureIfPresent appends the persisted, customer-facing failure
// explanation after the stage block. The deploy status command already has the
// deployment row, so this avoids making the operator switch to
// `gregale inspect <slug> --errors` just to learn what to fix.
func renderDeployFailureIfPresent(w io.Writer, dep api.DeploymentResponse) {
	if dep.Status != deploymentStatusFailed || !hasDeploymentFailureDetails(dep) {
		return
	}
	_, _ = fmt.Fprintln(w)
	renderDeploymentFailure(w, dep)
}

func hasDeploymentFailureDetails(dep api.DeploymentResponse) bool {
	return dep.Error != "" || dep.ErrorCode != "" || dep.ErrorHint != "" ||
		dep.ErrorWhy != "" || dep.ErrorFix != "" || len(dep.ErrorRelevantLogs) > 0
}

// wantDeploySummaryFooter is the boolean toggle for the
// fetchDeploySummaryInputs helper. Hoisted to a typed const so
// the two call sites (cmdDeploysShow with --status, cmdDeploysStatus)
// read symmetrically — the helper is a small enough piece that
// a single boolean is the cleanest contract.
const wantDeploySummaryFooter = true

// splitFlagArgs reorders argv so every flag (anything starting
// with "-" or "--") comes before every positional, then any
// "=value" suffix is preserved. This is the stdlib flag.NewFlagSet
// idiom for "accept flags anywhere" — without it, fs.Parse stops
// at the first positional and trailing flags end up in fs.Args()
// silently, surfacing as a confusing usage error.
//
// Examples:
//
//	splitFlagArgs([]string{"--status", "<id>"})  → ["--status", "<id>"]
//	splitFlagArgs([]string{"<id>", "--status"})  → ["--status", "<id>"]
//	splitFlagArgs([]string{"<id>", "--json"})    → ["--json", "<id>"]
//	splitFlagArgs([]string{"--status", "<id>", "--json"}) → ["--status", "--json", "<id>"]
//
// Used only by cmdDeploysShow — other subcommands either accept
// no flags (`deploys status`) or use a different parsing shape.
// Negative numbers (e.g. `-1`) are preserved as positionals by
// the leading-minus check; `--` itself is treated as a flag (the
// stdlib also handles it as a "stop parsing" sentinel — the
// caller can extend if needed).
func splitFlagArgs(args []string) []string {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
		} else {
			positionals = append(positionals, a)
		}
	}
	return append(flags, positionals...)
}

// fetchDeploySummaryInputs fans out the two GETs that the
// post-stream summary needs:
//
//   - GET /v1/deployments/{id}/stages → raw stage_state jsonb
//   - GET /v1/deployments/{id}        → deployment row (for status)
//
// The two round-trips are parallelized via errgroup so the latency
// budget is max(stages, deployment) not sum. Both endpoints hit
// loopback apid in production so the fan-out cuts a perceptible
// fraction of the wall clock under slack and is the natural choice
// when one HTTP request can already be inferred to live within the
// same handler edge.
//
// withFooter=false: only the stages call fires; dep is nil (the
// caller knows it doesn't need it). withFooter=true: both calls
// fire; the caller uses dep.Status + deriveTerminalAt to render
// the footer.
//
// IDOR posture: 404 from either endpoint surfaces as the same
// wrapped error. cmd/apid/handlers_stages.go returns 404 for both
// missing-deployment and cross-account (IDOR-safe); the CLI does
// not need to distinguish the two cases.
func fetchDeploySummaryInputs(ctx context.Context, client *api.Client, id string, withFooter bool) (json.RawMessage, *api.DeploymentResponse, error) {
	var (
		raw json.RawMessage
		dep *api.DeploymentResponse
	)
	// Type for the errgroup returned-error aggregator. Keeps the
	// two paths (with-footer / without-footer) flat in the helper.
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		r, err := client.GetDeploymentStages(ctx, id)
		if err != nil {
			return err
		}
		raw = r
		return nil
	})
	if withFooter {
		eg.Go(func() error {
			d, err := client.GetDeployment(ctx, id)
			if err != nil {
				return err
			}
			dep = &d
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, nil, err
	}
	return raw, dep, nil
}

// deriveTerminalAt picks the timestamp the footer
// ("live since …" / "failed at …" / "<status> at …") should
// anchor on. The choice is status-driven and entirely client-side
// from data already on hand (no new column, no new endpoint):
//
//   - "live"      → the StartedAt of the first history row. Every
//     stage completed for a live deployment, so the first row's
//     StartedAt is the pipeline's go-live moment.
//   - "failed"    → the EndedAt of the failed row. The customer's
//     anchor for "when did this break".
//   - superseded  → depCreatedAt. The new deployment that
//     superseded this one rolled forward at that timestamp; the
//     deployment row itself was created at the same instant
//     (the row insert and the state machine transition to
//     superseded fire in the same imaged chokepoint).
//   - default     → zero time. Caller treats this as "footer
//     omitted"; the safe default when status is unrecognised.
//
// Defensive: nil *time.Time fields in state.StageStateItem are
// surfaced as-is (zero time). The renderer's terminalAt.IsZero()
// gate must NOT print a footer for these — see
// pkg/dashboard/stages.RenderSummaryText.
//
// Review finding C1 (closed by this signature widening): the
// pre-fix version only handled live/failed and silently returned
// time.Time{} for superseded, which left the operator looking at
// a stage table with no terminal anchor even though the
// deployment's status column said "superseded".
func deriveTerminalAt(ss state.StageState, status string, depCreatedAt time.Time) time.Time {
	switch status {
	case "live":
		if len(ss.History) > 0 && ss.History[0].StartedAt != nil {
			return *ss.History[0].StartedAt
		}
	case "failed":
		for _, item := range ss.History {
			if item.Status == "failed" && item.EndedAt != nil {
				return *item.EndedAt
			}
		}
	case "superseded":
		// depCreatedAt is the deployment row's insert timestamp;
		// for a superseded row this is when the new deployment
		// that replaced it was created. The zero value is a
		// safe fallback (caller's terminalAt.IsZero() gate skips
		// the footer).
		return depCreatedAt
	}
	return time.Time{}
}

// cmdDeploys is the dispatcher for the `deploys` top-level verb.
//
// Subcommands:
//
//   - show   → cmdDeploysShow (this file). The closed 6-stage
//     post-stream summary, with --status for the footer.
//   - status → cmdDeploysStatus (this file). Same shape, always
//     with the footer.
//
// Unknown subcommands fall through to the usage branch. Future
// read-only drill-downs (timeline, events, artifacts) land here as
// sibling switch arms — NOT as flags on `gregale deployment <id>`,
// which already has --show-scan / --show-secret-scan /
// set-min-instances and would otherwise grow a third surface.
func cmdDeploys(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale deploys <subcommand> [flags]   (subcommands: show, status, cancel, reorder, clear, clear-obsolete, retry)", "deploys")
		return 1
	}
	switch args[0] {
	case "show":
		return cmdDeploysShow(args[1:])
	case statusLiteral:
		// Re-uses statusLiteral (the canonical "status" const
		// shared with the top-level `gregale status` subcommand
		// route) so the two paths stay in lock-step. Review
		// finding C2 closed the pre-fix shape (which had a
		// parallel dispatchDeploysStatus const that drifted
		// silently if anyone renamed statusLiteral).
		return cmdDeploysStatus(args[1:])
	case "cancel":
		return cmdDeploysCancel(args[1:])
	case "reorder":
		return cmdDeploysReorder(args[1:])
	case "clear":
		return cmdDeploysClear(args[1:])
	case "clear-obsolete":
		return cmdDeploysClearObsolete(args[1:])
	case "retry":
		// ADR-117 §Production-ready follow-on, C2 — per-stage
		// retry. Verb is a sibling to show/status (not a flag
		// on either) because the action mutates state.
		return cmdDeploysRetry(args[1:])
	}
	PrintUsage(os.Stderr, "usage: gregale deploys <subcommand> [flags]   (subcommands: show, status, cancel, reorder, clear, clear-obsolete, retry)", "deploys")
	return 1
}
