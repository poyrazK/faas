// gregale mirror <list|create|info|update|rm|summary> --app <slug>
// (issue #72 / ADR-125 traffic mirroring PR-A2). PR-A2 ships the
// customer-facing CLI surface; the runtime mirror goroutine lands
// in PR-A3. After PR-A2 lands, a customer can `gregale mirror
// create` and see the rule via `gregale mirror list`, but no
// traffic is mirrored yet — the gateway integration is A3.
//
// Six leaves, dispatched via cmdMirror. The pattern mirrors
// commands_edge_rules.go (cmdEdgeRules + 5 leaves) exactly: each
// leaf is its own function with its own flag set, --json
// round-trips through jsonOut(writeJSON(...)) so the SDK DTOs
// reach the customer's pipeline unmodified. Human output is a
// labelled fmt.Fprintf block per the rest of the codebase.
//
// The dispatch from main() splits on the sub-command name; the
// case clauses below match the route table in cmd/apid/server.go
// (POST/GET/PATCH/DELETE/summary) so the CLI surface stays in
// lockstep with the API surface.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// mirrorHeader is the column-header line used by `gregale mirror
// list`. Kept as a package var so the format stays in lockstep
// between the header and the per-row formatter (any rename of a
// column only has to be made here).
const mirrorHeader = "%-36s %-12s %-9s %-9s %-9s"

// renderMirrorRule writes one row of the mirror-rule table. Used
// by both cmdMirrorList (the table view) and any future JSON-table
// mixin. Extracted so the formatter stays consistent across
// callers.
func renderMirrorRule(w io.Writer, r api.MirrorRuleResponse) {
	enabled := "on"
	if !r.Enabled {
		enabled = "off"
	}
	includeBody := "off"
	if r.IncludeBody {
		includeBody = "on"
	}
	_, _ = fmt.Fprintf(w, "%-36s %-12s %-9s %-9s %-9s\n",
		r.ID,
		fmt.Sprintf("%d%%", r.Percent),
		enabled,
		includeBody,
		fmt.Sprintf("%d redact", len(r.RedactHeaders)),
	)
}

// cmdMirrorList implements `gregale mirror list --app <slug>`.
// Mirrors the GET /v1/apps/{slug}/mirrors surface (cmd/apid/handlers_mirror.go).
func cmdMirrorList(args []string) int {
	fs := flag.NewFlagSet("mirror list", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" {
		PrintUsage(os.Stderr, "usage: gregale mirror list --app <slug>", "mirror")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetAppsSlugMirrors(context.Background(), *slug)
	if err != nil {
		return printErr("List failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Rules) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no mirror rules)")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, mirrorHeader+"\n", "ID", "PERCENT", "ENABLED", "INC.BODY", "REDACT")
	for i := range resp.Rules {
		renderMirrorRule(osStdout, resp.Rules[i])
	}
	return 0
}

// cmdMirrorCreate implements `gregale mirror create`. Required
// flags: --app --source --mirror. --percent defaults to 100
// (mirror every customer request); --redact-header can be passed
// 0..N times to populate the customer's additive redact list.
func cmdMirrorCreate(args []string) int {
	fs := flag.NewFlagSet("mirror create", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	source := fs.String("source", "", "source deployment id (required)")
	mirror := fs.String("mirror", "", "mirror deployment id (required)")
	percent := fs.Int("percent", 100, "fan-out percent in [0, 100]; 100 = every request")
	includeBody := fs.Bool("include-body", false, "include request/response bodies in the comparison ledger")
	var redactHeaders multiFlag
	fs.Var(&redactHeaders, "redact-header", "extra header name to redact (repeatable); always-stripped list applies regardless")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *source == "" || *mirror == "" {
		PrintUsage(os.Stderr, "usage: gregale mirror create --app <slug> --source <id> --mirror <id> [--percent N] [--include-body] [--redact-header Name]…", "mirror")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	headers := redactHeaders
	if headers == nil {
		headers = multiFlag{}
	}
	resp, err := client.PostAppsSlugMirrors(context.Background(), *slug, api.CreateMirrorRuleRequest{
		SourceDeploymentID: *source,
		MirrorDeploymentID: *mirror,
		Percent:            *percent,
		IncludeBody:        *includeBody,
		RedactHeaders:      headers,
	})
	if err != nil {
		return printErr("Create failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	_, _ = fmt.Fprintf(osStdout, "Created mirror rule %s (%s → %s, %d%%)\n",
		resp.ID, resp.SourceDeploymentID, resp.MirrorDeploymentID, resp.Percent)
	return 0
}

// cmdMirrorInfo implements `gregale mirror info --app <slug> --id <mirror-id>`.
// Renders the canonical MirrorRuleResponse as a labelled block;
// AlwaysStrippedHeaders is rendered so the customer can audit the
// redaction manifest in their terminal.
func cmdMirrorInfo(args []string) int {
	fs := flag.NewFlagSet("mirror info", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	id := fs.String("id", "", "mirror rule id (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *id == "" {
		PrintUsage(os.Stderr, "usage: gregale mirror info --app <slug> --id <mirror-id>", "mirror")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetAppsSlugMirrorsId(context.Background(), *slug, *id)
	if err != nil {
		return printErr("Get failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	_, _ = fmt.Fprintf(osStdout, "ID:           %s\n", resp.ID)
	_, _ = fmt.Fprintf(os.Stdout, "App:          %s\n", resp.AppID)
	_, _ = fmt.Fprintf(os.Stdout, "Source:       %s\n", resp.SourceDeploymentID)
	_, _ = fmt.Fprintf(os.Stdout, "Mirror:       %s\n", resp.MirrorDeploymentID)
	_, _ = fmt.Fprintf(os.Stdout, "Percent:      %d%%\n", resp.Percent)
	_, _ = fmt.Fprintf(os.Stdout, "Enabled:      %t\n", resp.Enabled)
	_, _ = fmt.Fprintf(os.Stdout, "Include body: %t\n", resp.IncludeBody)
	if len(resp.RedactHeaders) > 0 {
		_, _ = fmt.Fprintf(os.Stdout, "Redact hdrs:  %s\n", strings.Join(resp.RedactHeaders, ", "))
	}
	_, _ = fmt.Fprintf(os.Stdout, "Always strip: %s\n", strings.Join(resp.AlwaysStrippedHeaders, ", "))
	_, _ = fmt.Fprintf(os.Stdout, "Created:      %s\n", resp.CreatedAt.Format("2006-01-02 15:04:05 MST"))
	_, _ = fmt.Fprintf(os.Stdout, "Updated:      %s\n", resp.UpdatedAt.Format("2006-01-02 15:04:05 MST"))
	return 0
}

// cmdMirrorUpdate implements `gregale mirror update --app <slug>
// --id <mirror-id>` with the canonical patch-semantics flag set.
// fs.Visit is used to distinguish "flag not passed" from "flag
// passed with empty value" so a `--percent 0` (legal — disable
// without removing) is distinguishable from no `--percent` at
// all (keep existing value).
func cmdMirrorUpdate(args []string) int {
	fs := flag.NewFlagSet("mirror update", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	id := fs.String("id", "", "mirror rule id (required)")
	percent := fs.Int("percent", -1, "new percent in [0, 100]; -1 = unset (keep existing)")
	enable := fs.Bool("enable", false, "enable the rule")
	disable := fs.Bool("disable", false, "disable the rule")
	includeBody := fs.Bool("include-body", false, "enable body capture")
	noIncludeBody := fs.Bool("no-include-body", false, "disable body capture")
	var redactHeaders multiFlag
	fs.Var(&redactHeaders, "redact-header", "extra header name to redact (repeatable)")
	var clearRedact bool
	fs.BoolVar(&clearRedact, "clear-redact", false, "clear the customer's redact_headers list (drop to always-stripped only)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *id == "" {
		PrintUsage(os.Stderr, "usage: gregale mirror update --app <slug> --id <mirror-id> [--percent N] [--enable|--disable] [--include-body|--no-include-body] [--redact-header Name]… [--clear-redact]", "mirror")
		return 1
	}
	if *enable && *disable {
		PrintUsage(os.Stderr, "usage: gregale mirror update --app <slug> --id <mirror-id> [--enable|--disable] (mutually exclusive)", "mirror")
		return 1
	}
	if *includeBody && *noIncludeBody {
		PrintUsage(os.Stderr, "usage: gregale mirror update --app <slug> --id <mirror-id> [--include-body|--no-include-body] (mutually exclusive)", "mirror")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	req := api.UpdateMirrorRuleRequest{}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "percent":
			v := *percent
			req.Percent = &v
		case "enable":
			if *enable {
				t := true
				req.Enabled = &t
			}
		case "disable":
			if *disable {
				f := false
				req.Enabled = &f
			}
		case "include-body":
			// This is also a direct boolean setting:
			// --include-body=false disables capture.
			v := *includeBody
			req.IncludeBody = &v
		case "no-include-body":
			if *noIncludeBody {
				f := false
				req.IncludeBody = &f
			}
		case "redact-header":
			// collected below into req.RedactHeaders
		case "clear-redact":
			if clearRedact {
				empty := []string{}
				req.RedactHeaders = &empty
			}
		}
	})
	// If --redact-header was passed at least once AND --clear-redact
	// wasn't, push the collected list into the request. The
	// multiFlag slice is nil when the flag is absent. The slice
	// is materialised into []string so the API DTO type is exact.
	if fs.Lookup("redact-header").Value.String() != "" && !clearRedact {
		headers := []string(redactHeaders)
		req.RedactHeaders = &headers
	}
	if req.Percent == nil && req.Enabled == nil && req.IncludeBody == nil && req.RedactHeaders == nil {
		return printErr("No mirror changes requested", fmt.Errorf("set at least one mirror update value"))
	}
	resp, err := client.PatchAppsSlugMirrorsId(context.Background(), *slug, *id, req)
	if err != nil {
		return printErr("Update failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	_, _ = fmt.Fprintf(os.Stdout, "Updated mirror rule %s (%d%%, enabled=%t)\n",
		resp.ID, resp.Percent, resp.Enabled)
	return 0
}

// cmdMirrorRm implements `gregale mirror rm --app <slug> --id <mirror-id>`.
// 204 on success; the second delete returns 404 (silent on the
// CLI side too — the server enforces the IDOR posture).
func cmdMirrorRm(args []string) int {
	fs := flag.NewFlagSet("mirror rm", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	id := fs.String("id", "", "mirror rule id (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *id == "" {
		PrintUsage(os.Stderr, "usage: gregale mirror rm --app <slug> --id <mirror-id>", "mirror")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteAppsSlugMirrorsId(context.Background(), *slug, *id); err != nil {
		return printErr("Delete failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"deleted": true, "id": *id}))
	}
	_, _ = fmt.Fprintf(os.Stdout, "Deleted mirror rule %s\n", *id)
	return 0
}

// cmdMirrorSummary implements
// `gregale mirror summary --app <slug> --id <mirror-id> [--window 1h|24h|7d]`.
// windowStr defaults to "1h". The server validates the value and
// returns 422 invalid_mirror_window on anything else.
func cmdMirrorSummary(args []string) int {
	fs := flag.NewFlagSet("mirror summary", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	id := fs.String("id", "", "mirror rule id (required)")
	window := fs.String("window", "1h", "summary window: 1h | 24h | 7d (default 1h)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *id == "" {
		PrintUsage(os.Stderr, "usage: gregale mirror summary --app <slug> --id <mirror-id> [--window 1h|24h|7d]", "mirror")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetAppsSlugMirrorsIdSummary(context.Background(), *slug, *id, *window)
	if err != nil {
		return printErr("Summary failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	_, _ = fmt.Fprintf(os.Stdout, "Window:         %d s\n", resp.WindowSeconds)
	_, _ = fmt.Fprintf(os.Stdout, "Invocations:    %d\n", resp.TotalInvocations)
	_, _ = fmt.Fprintf(os.Stdout, "Status diff:    %d\n", resp.StatusDiffCount)
	_, _ = fmt.Fprintf(os.Stdout, "Schema diff:    %d\n", resp.SchemaDiffCount)
	_, _ = fmt.Fprintf(os.Stdout, "Body diff:      %d\n", resp.BodyDiffCount)
	_, _ = fmt.Fprintf(os.Stdout, "Mean latency Δ: %d ms\n", resp.MeanLatencyDiffMs)
	_, _ = fmt.Fprintf(os.Stdout, "p99 latency Δ:  %d ms\n", resp.P99LatencyDiffMs)
	_, _ = fmt.Fprintf(os.Stdout, "Crashes:        %d\n", resp.CrashCount)
	return 0
}

// cmdMirror dispatches the `gregale mirror` sub-command. The
// pattern matches cmdEdgeRules (commands_edge_rules.go:74) and
// cmdTraffic (commands2.go:1606) exactly: leaf-dispatch on the
// first positional arg.
func cmdMirror(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale mirror <list|create|info|update|rm|summary> --app <slug> [flags]", "mirror")
		return 1
	}
	switch args[0] {
	case "list":
		return cmdMirrorList(args[1:])
	case "create":
		return cmdMirrorCreate(args[1:])
	case "info":
		return cmdMirrorInfo(args[1:])
	case "update":
		return cmdMirrorUpdate(args[1:])
	case "rm":
		return cmdMirrorRm(args[1:])
	case "summary":
		return cmdMirrorSummary(args[1:])
	default:
		PrintUsage(os.Stderr, "usage: gregale mirror <list|create|info|update|rm|summary> --app <slug> [flags]", "mirror")
		return 1
	}
}
