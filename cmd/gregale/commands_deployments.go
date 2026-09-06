// Deployments list/get commands. Two top-level subcommands dispatched
// from main.go (cases `dispatchDeployments` and `dispatchDeployment`).
//
// Design notes (kept short so the handlers stay under the 50-line cap):
//
//   - `gregale deployments` lists a single page (50 rows by default) so
//     the customer sees what just shipped without thinking about
//     pagination. `--limit N` and `--before CURSOR` thread through to
//     the API; `--all` walks every page via the existing
//     Client.ListDeploymentsAll helper (pkg/api/paging.go).
//
//   - JSON output for the list path is the *envelope* ({items,next_before})
//     rather than NDJSON per record. This is a deliberate break from
//     the apps/crons/keys convention; the endpoint is paginated and
//     dropping next_before is worse for automation. The singular
//     `gregale deployment <id>` JSON path is the dotted envelope object
//     (one indented JSON object), matching AccountResponse / AppResponse.
//
//   - `gregale deployment <id>` validates the 32-hex shape locally so
//     `--json` users get `validation_failed` instead of a 404. The
//     server enforces the same shape.
//
// Field set tracks pkg/api/dto.go:86 DeploymentResponse verbatim; update
// the human table when the DTO grows (e.g. commit_sha when function
// runners land). The OpenAPI example block is currently stale against
// the DTO; spec cleanup is a separate PR.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
)

// deploymentIDPattern enforces the 32-hex deployment id shape the API
// uses everywhere (DeploymentResponse.ID, also the path segment of
// /v1/deployments/{id} and the --deployment flag on `gregale logs`).
// Local validation lets the CLI return a fast validation_failed error
// instead of a 404 round-trip — UX §3.3 "first error is the right one".
var deploymentIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$|^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// deploymentRowFmt is the human list-table column layout. Widths assume
// 32-hex deployment ids (32 chars), 32-hex app ids (32 chars), short
// status strings ("succeeded" / "failed" / "rolling_out"), and the
// app/function kind discriminator. If the DTO grows an id longer than
// 32 hex chars this layout has to shift; covered by
// TestRenderDeploymentRow pin on the column count.
const deploymentRowFmt = "%-32s %-32s %-12s %-10s %s\n"

// deploymentRowFmtWide extends the table with annotation columns
// (issue #977 / ADR-116). Layout:
//
//	id | app | status | kind | by | pr | tag | reason (40c) | created
//
// `by` is d.DeployedBy (max 16 chars rendered); `pr` is the PR
// number or "-"; `tag` is the closed-set enum (≤24 chars) or "-";
// `reason` is d.Reason truncated to 40 chars + "…" when over.
// `--wide` (`-w`) toggles this layout in cmdDeployments; the default
// table stays the old 5-column shape so existing scripts / dashboard
// scrapers don't break.
const deploymentRowFmtWide = "%-32s %-32s %-12s %-10s %-16s %-6s %-24s %-41s %s\n"

// renderDeploymentRow writes one deployment row to w. The fmt.Printf
// inside writes to os.Stdout by default; tests that need the rendered
// row in a buffer must use the osStdout package seam (see
// commands_test.go for the equivalent helper). The Fprintf return is
// intentionally discarded: writer failures (closed pipe, broken TTY)
// are unrecoverable here, matching writeStatus / output.go's convention.
func renderDeploymentRow(w io.Writer, d api.DeploymentResponse) {
	_, _ = fmt.Fprintf(w, deploymentRowFmt, d.ID, d.AppID, d.Status, d.Kind, d.CreatedAt)
}

// renderDeploymentRowWide writes one deployment row using the wide
// annotation layout. Empty annotation fields render as "-" so columns
// stay aligned (a `-` dash, not a space) — the pr_number cell is
// "-" for the common push-to-main path where the column is NULL.
func renderDeploymentRowWide(w io.Writer, d api.DeploymentResponse) {
	by := d.DeployedBy
	if by == "" {
		by = "-"
	}
	pr := "-"
	if d.PRNumber > 0 {
		pr = fmt.Sprintf("%d", d.PRNumber)
	}
	tag := d.Tag
	if tag == "" {
		tag = "-"
	}
	// Reason column also gets the dash treatment so a mixed fleet
	// (some pre-feature rows, some with annotations) lines up in
	// the terminal. truncateReason already handles ""→""; we map
	// that to "-" here so the column is visually present.
	reason := truncateReason(d.Reason, 40)
	if reason == "" {
		reason = "-"
	}
	_, _ = fmt.Fprintf(w, deploymentRowFmtWide,
		d.ID, d.AppID, d.Status, d.Kind, by, pr, tag, reason, d.CreatedAt)
}

// truncateReason returns s truncated to max runes with "…" appended
// when the input is longer. Empty string returns "" (the caller
// renders "-" elsewhere). Operates on runes (not bytes) so a multi-
// byte reason isn't sliced mid-character.
func truncateReason(s string, max int) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// cmdDeployments implements `gregale deployments [--limit N] [--before C] [--all]`
// and the `exclude` subcommand family
// (`gregale deployments exclude clear --slug=... [--project-slug=...]`).
// Mirrors cmdApps (commands.go:251) except pagination is exposed.
// Wire shape: GET /v1/deployments (paginated; cursor=before, limit=limit)
// and DELETE /v1/projects/{slug}/exclusions/{slug} (ADR-124 code-review
// fix #2 escape hatch).
func cmdDeployments(args []string) int {
	// Subcommand dispatch — must run BEFORE flag parsing or the
	// FlagSet chokes on the unrecognised "exclude" verb.
	if len(args) > 0 {
		switch args[0] {
		case "exclude":
			return cmdDeploymentsExclude(args[1:])
		}
	}
	fs := flag.NewFlagSet("deployments", flag.ContinueOnError)
	limit := fs.Int("limit", 50, "page size (1-200)")
	before := fs.String("before", "", "pagination cursor (RFC3339Nano)")
	all := fs.Bool("all", false, "walk every page (ignores --limit/--before)")
	// Issue #977 / ADR-116: --wide / -w toggles the annotation columns
	// (by / pr / tag / reason). Default stays the old 5-column shape so
	// existing scripts / dashboard scrapers don't break. Single-letter
	// `-w` because the human-viewable "wide" output is a daily-driver
	// for ops; matches the awk-flavoured shape other CLI list commands
	// use (e.g. `kubectl get pods -w` watch flag — semantically
	// different but the user mental model is "show me more").
	wide := fs.Bool("wide", false, "include annotation columns (by / pr / tag / reason)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale deployments [--limit N] [--before CURSOR] [--all] [--wide]", "deployments")
		return 1
	}
	if *limit < 0 || *limit > 200 {
		PrintUsage(os.Stderr, "usage: gregale deployments --limit N (0 < N <= 200)", "deployments")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if *all {
		return cmdDeploymentsAll(ctx, client, *wide)
	}
	page, err := client.ListDeployments(ctx, *before, *limit)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		// Envelope (not NDJSON) so `next_before` survives; see file header.
		return jsonOut(writeJSON(page))
	}
	if len(page.Items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No deployments yet.")
		_, _ = fmt.Fprintln(osStdout, "Deploy one: `gregale deploy --tarball path/to/source.tar.gz` (or `gregale deploy --image <ref>`).")
		return 0
	}
	for _, d := range page.Items {
		if *wide {
			renderDeploymentRowWide(osStdout, d)
		} else {
			renderDeploymentRow(osStdout, d)
		}
	}
	if page.NextBefore != "" {
		_, _ = fmt.Fprintf(osStdout, "... more — pass --before %s\n", page.NextBefore)
	}
	return 0
}

// cmdDeploymentsExclude dispatches the `exclude` verb family.
// Today: `clear` (ADR-124 code-review fix #2 escape hatch — drop a
// stale persisted exclusion that no longer exists in the repo).
// Future: `list` (render the persisted exclusion table for a project)
// lands here when the dashboard drill-down needs a CLI sibling.
func cmdDeploymentsExclude(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale deployments exclude clear --slug=NAME [--project-slug=SLUG]", "deployments exclude")
		return 1
	}
	switch args[0] {
	case "clear":
		return cmdDeploymentsExcludeClear(args[1:])
	default:
		PrintUsage(os.Stderr, "usage: gregale deployments exclude clear --slug=NAME [--project-slug=SLUG]", "deployments exclude")
		return 1
	}
}

// cmdDeploymentsExcludeClear implements
// `gregale deployments exclude clear --slug=NAME [--project-slug=SLUG]`.
// ADR-124 code-review fix #2 — escape hatch for operators locked
// out by a stale persisted exclusion (workload renamed or deleted
// in the repo). Without this command, the only option was psql +
// hand-DELETE; the CLI surface is the operator-grade path. The
// server responds 404 when no row matches (already cleared), so
// the command is idempotent and safe to re-run.
//
// --project-slug defaults to the slug carried in FAAS_PROJECT_SLUG
// (the same default every other gregale subcommand uses); the
// optional flag is here for shell scripts that batch-clear across
// projects without env-mutating each iteration.
func cmdDeploymentsExcludeClear(args []string) int {
	fs := flag.NewFlagSet("deployments exclude clear", flag.ContinueOnError)
	slug := fs.String("slug", "", "lowercase workload slug to clear from deployment_scope_exclusions (required)")
	projectSlug := fs.String("project-slug", "", "project slug (defaults to FAAS_PROJECT_SLUG env)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" {
		PrintUsage(os.Stderr, "usage: gregale deployments exclude clear --slug=NAME [--project-slug=SLUG]", "deployments exclude")
		return 1
	}
	if *projectSlug == "" {
		*projectSlug = os.Getenv("FAAS_PROJECT_SLUG")
	}
	if *projectSlug == "" {
		PrintUsage(os.Stderr, "usage: gregale deployments exclude clear --slug=NAME --project-slug=SLUG (or set FAAS_PROJECT_SLUG)", "deployments exclude")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	// Normalise to lowercase — the schema's CHECK slug = lower(slug)
	// rejects mixed case, and the server's lookup is case-folded too.
	*slug = strings.ToLower(strings.TrimSpace(*slug))
	*projectSlug = strings.ToLower(strings.TrimSpace(*projectSlug))
	if err := client.DeleteDeploymentScopeExclusion(ctx, *projectSlug, *slug); err != nil {
		// 404 is fine — already cleared is the same observable end state.
		// Server returns Problem{Code: "scope_exclusion_not_found"} wrapped
		// in *APIError; branch on the code via errors.As so the same
		// surface handles both transports (HTTP 404 + any future
		// local-cache miss with the same code).
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.Problem.Code == "scope_exclusion_not_found" {
			_, _ = fmt.Fprintf(osStdout, "no persisted exclusion for %q in project %q (already clear)\n", *slug, *projectSlug)
			return 0
		}
		return printErr("Request failed", err)
	}
	_, _ = fmt.Fprintf(osStdout, "cleared persisted exclusion: project=%q slug=%q\n", *projectSlug, *slug)
	return 0
}

// cmdDeploymentsAll walks every page via the SDK helper and renders the
// full list. Refuses to share a single envelope with the one-page path
// (no `next_before` to surface), so JSON output is the bare slice —
// matching how apps/crons/keys emit NDJSON for non-paginated lists.
func cmdDeploymentsAll(ctx context.Context, client *api.Client, wide bool) int {
	items, err := client.ListDeploymentsAll(ctx)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(items))
	}
	if len(items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No deployments yet.")
		return 0
	}
	for _, d := range items {
		if wide {
			renderDeploymentRowWide(osStdout, d)
		} else {
			renderDeploymentRow(osStdout, d)
		}
	}
	return 0
}

// cmdDeployment dispatches `gregale deployment <verb> ...` to either
// the legacy singular GET (`gregale deployment <id> [--show-scan]`) or
// the Tier D mutator `gregale deployment set-min-instances <id> --min N`.
// The 3-word verb shape mirrors cmdWebhookRotateSecret (commands_webhooks.go:361).
func cmdDeployment(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale deployment <id> [--show-scan] | gregale deployment wait <id> [--timeout SECONDS] | gregale deployment set-min-instances <id> --min N", "deployment")
		return 1
	}
	switch args[0] {
	case "set-min-instances":
		return cmdDeploymentSetMinInstances(args[1:])
	case "wait":
		return cmdDeploymentWait(args[1:])
	}
	return cmdDeploymentGet(args)
}

// cmdDeploymentWait polls a deployment until it is live or terminal. It is
// intentionally a separate verb so CI callers do not have to reconstruct the
// platform's status vocabulary (or mistake an HTTP 200 for a successful
// deployment). --timeout is expressed in seconds to keep the GitHub Action
// input and CLI contract identical.
func cmdDeploymentWait(args []string) int {
	flags, pos := splitArgsForFlags(args)
	fs := flag.NewFlagSet("deployment wait", flag.ContinueOnError)
	timeoutSeconds := fs.Int("timeout", 600, "maximum seconds to wait")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 || *timeoutSeconds <= 0 || !deploymentIDPattern.MatchString(pos[0]) {
		PrintUsage(os.Stderr, "usage: gregale deployment wait <id> [--timeout SECONDS]", "deployment")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSeconds)*time.Second)
	defer cancel()

	for {
		d, getErr := client.GetDeployment(ctx, pos[0])
		if getErr != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return printErr("Deployment wait timed out", fmt.Errorf("deployment %s did not become live within %ds", pos[0], *timeoutSeconds))
			}
			return printErr("Could not fetch deployment", getErr)
		}
		switch d.Status {
		case "live":
			if jsonOutput {
				return jsonOut(writeJSON(d))
			}
			PrintOK(osStdout, "Deployment %s is live.", d.ID)
			return 0
		case "failed", "superseded", "cancelled":
			if jsonOutput {
				_ = writeJSON(d)
			}
			return printErr("Deployment did not become live", fmt.Errorf("deployment %s reached terminal status %s: %s", d.ID, d.Status, d.Error))
		}

		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return printErr("Deployment wait timed out", fmt.Errorf("deployment %s did not become live within %ds", d.ID, *timeoutSeconds))
		case <-timer.C:
		}
	}
}

// cmdDeploymentGet implements
// `gregale deployment <id> [--show-scan] [--show-secret-scan]`
// (GET /v1/deployments/{id}, plus the per-deploy drill-down
// surfaces when their flags are set). Mirrors the read branch
// of cmdApp (commands2.go:69) — single positional id, JSON
// single record, human multi-line detail block.
//
// --show-scan is a flag (not a separate `gregale scan <id>`
// subcommand) because `gregale scan` is already taken by the
// Phase 3 repo-decomposition dry-run surface at
// cmd/gregale/commands_decompose.go:49. The flag-vs-subcommand
// split is the smallest-mess resolution of the name collision.
//
// --show-secret-scan mirrors --show-scan for the PR-A
// image-layer secret scan drill-down
// (`GET /v1/deployments/{id}/secret-scan`). Both flags may be
// passed together; the JSON output struct gains a
// `secret_scan` field, the text rendering prints both blocks
// in order.
func cmdDeploymentGet(args []string) int {
	fs := flag.NewFlagSet("deployment", flag.ContinueOnError)
	showScan := fs.Bool("show-scan", false, "fetch + print the per-deploy grype scan payload (GET /v1/deployments/{id}/scan)")
	showSecretScan := fs.Bool("show-secret-scan", false,
		"fetch + print the per-deploy image-layer secret-scan payload (GET /v1/deployments/{id}/secret-scan)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale deployment <id> [--show-scan] [--show-secret-scan]", "deployment")
		return 1
	}
	id := fs.Arg(0)
	if !deploymentIDPattern.MatchString(id) {
		PrintUsage(os.Stderr, "usage: gregale deployment <id> [--show-scan] [--show-secret-scan]   (id is 32 hex chars)", "deployment")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	d, err := client.GetDeployment(context.Background(), id)
	if err != nil {
		return printErr("Could not fetch deployment", err)
	}
	if jsonOutput {
		if !*showScan && !*showSecretScan {
			return jsonOut(writeJSON(d))
		}
		// Build the JSON envelope with only the surfaces the
		// caller asked for. A missing drill-down is
		// non-fatal in JSON mode (the deployment row is
		// always emitted so a script never silently loses
		// data); the fetch error is logged at WARN.
		type envelope struct {
			Deployment any `json:"deployment"`
			Scan       any `json:"scan,omitempty"`
			SecretScan any `json:"secret_scan,omitempty"`
		}
		env := envelope{Deployment: d}
		if *showScan {
			sc, scanErr := client.GetDeploymentScan(context.Background(), id)
			if scanErr != nil {
				_, _ = fmt.Fprintf(osStderr, "warning: scan unavailable: %v\n", scanErr)
			} else {
				env.Scan = sc
			}
		}
		if *showSecretScan {
			ssc, secErr := client.GetDeploymentSecretScan(context.Background(), id)
			if secErr != nil {
				_, _ = fmt.Fprintf(osStderr, "warning: secret-scan unavailable: %v\n", secErr)
			} else {
				env.SecretScan = ssc
			}
		}
		return jsonOut(writeJSON(env))
	}
	_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "id:", d.ID)
	_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "app_id:", d.AppID)
	if d.BuildID != "" {
		_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "build_id:", d.BuildID)
	}
	_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "image_digest:", d.ImageDigest)
	_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "kind:", d.Kind)
	_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "status:", d.Status)
	_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "created_at:", d.CreatedAt)
	// Issue #977 / ADR-116: annotation block. Each field is
	// conditional on non-empty so pre-feature rows render the
	// same shape as before (no `-` placeholders for legacy data).
	if d.DeployedBy != "" {
		_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "deployed_by:", d.DeployedBy)
	}
	if d.Tag != "" {
		_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "tag:", d.Tag)
	}
	if d.PRNumber > 0 {
		_, _ = fmt.Fprintf(osStdout, "%-14s #%d\n", "pr_number:", d.PRNumber)
	}
	if d.Reason != "" {
		_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "reason:", d.Reason)
	}
	if d.Error != "" {
		_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "error:", d.Error)
	}
	if d.ErrorCode != "" {
		_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "error_code:", d.ErrorCode)
	}
	renderDeploymentHostingReceipt(osStdout, d.APIHostingReceipt)
	if *showScan {
		sc, scanErr := client.GetDeploymentScan(context.Background(), id)
		if scanErr != nil {
			_, _ = fmt.Fprintf(osStdout, "%-14s (scan unavailable: %v)\n", "scan:", scanErr)
		} else {
			_, _ = fmt.Fprintf(osStdout, "\n%-14s %s\n", "scan_status:", sc.Status)
			if sc.ScannedAt != "" {
				_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "scanned_at:", sc.ScannedAt)
			}
			if sc.ScannerVersion != "" {
				_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "scanner_version:", sc.ScannerVersion)
			}
			if sc.ImageDigest != "" {
				_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "image_digest:", sc.ImageDigest)
			}
			_, _ = fmt.Fprintf(osStdout, "%-14s C=%d H=%d M=%d L=%d U=%d\n", "severity_counts:", sc.SeverityCounts.Critical, sc.SeverityCounts.High, sc.SeverityCounts.Medium, sc.SeverityCounts.Low, sc.SeverityCounts.Unknown)
			if sc.Error != "" {
				_, _ = fmt.Fprintf(osStdout, "%-14s %s\n", "scan_error:", sc.Error)
			}
			if len(sc.Vulnerabilities) > 0 {
				_, _ = fmt.Fprintf(os.Stdout, "%-14s %d\n", "vulnerabilities:", len(sc.Vulnerabilities))
				for _, v := range sc.Vulnerabilities {
					_, _ = fmt.Fprintf(os.Stdout, "  - %s [%s] %s %s (fixed in %s)\n", v.ID, v.Severity, v.Package, v.Version, v.FixedIn)
				}
			}
		}
	}
	if *showSecretScan {
		ssc, secErr := client.GetDeploymentSecretScan(context.Background(), id)
		if secErr != nil {
			_, _ = fmt.Fprintf(os.Stdout, "%-14s (secret scan unavailable: %v)\n", "secret_scan:", secErr)
		} else {
			_, _ = fmt.Fprintf(os.Stdout, "\n%-14s %s\n", "secret_scan_status:", ssc.Status)
			if ssc.ScannedAt != "" {
				_, _ = fmt.Fprintf(os.Stdout, "%-14s %s\n", "secret_scanned_at:", ssc.ScannedAt)
			}
			if ssc.ImageDigest != "" {
				_, _ = fmt.Fprintf(os.Stdout, "%-14s %s\n", "secret_scan_image_digest:", ssc.ImageDigest)
			}
			_, _ = fmt.Fprintf(os.Stdout, "%-14s %d\n", "secret_findings:", len(ssc.Findings))
			for _, f := range ssc.Findings {
				layer := f.Layer
				if layer == "" {
					// Older rows (pre-PR-A, apid source-tree
					// stamped) carry no layer label — render as
					// "source" so the operator knows which
					// pipeline produced the finding.
					layer = "source"
				}
				_, _ = fmt.Fprintf(os.Stdout, "  - [%s] %s in %s:%d (provider=%s layer=%s)\n",
					f.Severity, f.Key, f.File, f.Line, f.Provider, layer)
				if f.Snippet != "" {
					_, _ = fmt.Fprintf(os.Stdout, "      snippet: %s\n", f.Snippet)
				}
			}
		}
	}
	return 0
}

// renderDeploymentHostingReceipt prints the durable post-readiness evidence
// when the server has one. Legacy and in-flight deployments have an empty or
// default receipt, which intentionally produces no extra output.
func renderDeploymentHostingReceipt(w io.Writer, raw json.RawMessage) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("{}")) {
		return
	}
	receipt, err := apihostingreceipt.Decode(raw)
	if err != nil {
		_, _ = fmt.Fprintf(w, "%-14s unavailable (%v)\n", "hosting_receipt:", err)
		return
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "%-14s %s\n", "hosting_status:", receipt.Smoke.Status)
	if receipt.AppURL != "" {
		_, _ = fmt.Fprintf(w, "%-14s %s\n", "hosting_app_url:", receipt.AppURL)
	}
	if receipt.Smoke.Path != "" {
		_, _ = fmt.Fprintf(w, "%-14s %s\n", "health_path:", receipt.Smoke.Path)
	}
	if receipt.Smoke.StatusCode != 0 {
		_, _ = fmt.Fprintf(w, "%-14s %d\n", "health_status:", receipt.Smoke.StatusCode)
	}
	if receipt.Smoke.LatencyMS != 0 {
		_, _ = fmt.Fprintf(w, "%-14s %dms\n", "health_latency:", receipt.Smoke.LatencyMS)
	}
	if !receipt.Smoke.VerifiedAt.IsZero() {
		_, _ = fmt.Fprintf(w, "%-14s %s\n", "verified_at:", receipt.Smoke.VerifiedAt.UTC().Format(time.RFC3339))
	}
	if receipt.Profile.Framework != "" {
		profile := receipt.Profile.Framework
		if receipt.Profile.FrameworkVer != "" {
			profile += " " + receipt.Profile.FrameworkVer
		}
		_, _ = fmt.Fprintf(w, "%-14s %s (port %d)\n", "profile:", profile, receipt.Profile.Port)
	}
	if receipt.Source.CommitSHA != "" {
		_, _ = fmt.Fprintf(w, "%-14s %s\n", "source_sha:", receipt.Source.CommitSHA)
	}
	if receipt.Source.ImageDigest != "" {
		_, _ = fmt.Fprintf(w, "%-14s %s\n", "image_digest:", receipt.Source.ImageDigest)
	}
	if receipt.Smoke.ErrorCode != "" {
		_, _ = fmt.Fprintf(w, "%-14s %s\n", "hosting_error:", receipt.Smoke.ErrorCode)
	}
	if receipt.Smoke.Error != "" {
		_, _ = fmt.Fprintf(w, "%-14s %s\n", "hosting_detail:", receipt.Smoke.Error)
	}
}

// cmdDeploymentSetMinInstances implements `gregale deployment
// set-min-instances <id> --min N` (PATCH /v1/deployments/{id}, issue #557,
// ADR-072 — per-deployment cold-wake floor override).
//
// --min N sets min_instances on the deployment. The server treats
// min_instances=0 as "inherit parent app floor" (per
// pkg/api/client.go:417-419 — the SDK emits {"min_instances":0} verbatim,
// and handlers_ext.go:1051-1093 validates against acct.Plan.MaxMinInstances).
//
// The 3-word verb shape mirrors cmdWebhookRotateSecret
// (commands_webhooks.go:361) — grep-friendly and matches the kebab-case
// surface in api/openapi.yaml.
//
// Local --min >= 0 gate runs before authedClient() so a CLI typo costs
// zero latency (mirrors validateAlertClosedSets, commands_alerts.go:172).
func cmdDeploymentSetMinInstances(args []string) int {
	// splitArgsForFlags: Go's flag.Parse halts at the first non-flag
	// positional, so `gregale deployment set-min-instances <id> --min 5`
	// would silently drop --min 5 and send min_instances:0 to the
	// server (resetting the cold-wake floor). The reorder helper
	// pulls --min to the front so the parser sees it. Mirrors
	// cmdDelayedTaskAdd (commands_delayed_task.go:118).
	flags, pos := splitArgsForFlags(args)
	fs := flag.NewFlagSet("deployment set-min-instances", flag.ContinueOnError)
	min := fs.Int("min", 0, "min_instances floor (>= 0; 0 inherits the parent app floor)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		PrintUsage(os.Stderr, "usage: gregale deployment set-min-instances <id> --min N", "deployment")
		return 1
	}
	if *min < 0 {
		return printErr("Invalid --min", fmt.Errorf("--min must be >= 0; got %d", *min))
	}
	id := pos[0]
	if !deploymentIDPattern.MatchString(id) {
		PrintUsage(os.Stderr, "usage: gregale deployment set-min-instances <id> --min N   (id is 32 hex chars)", "deployment")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	d, err := client.PatchDeployment(context.Background(), id, api.UpdateDeploymentRequest{MinInstances: min})
	if err != nil {
		return printErr("Update failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(d))
	}
	PrintOK(osStdout, "Deployment %s updated.", d.ID)
	_, _ = fmt.Fprintf(osStdout, "  min_instances: %d\n", d.MinInstances)
	return 0
}
