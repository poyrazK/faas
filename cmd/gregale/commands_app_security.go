// commands_app_security.go — Tier D audit-gap close.
// `gregale app <slug> security [--posture|--require-signed=true|false]`
// (PATCH /v1/apps/{slug}/security, issue #472 / ADR-054).
//
// Mirrors cmdAlertUpdate (commands_alerts.go:245-313) exactly: same
// pointer-everything fs.Visit detection so `--require-signed=false`
// is distinguishable from "leave alone". The leaf is wired into
// cmdAppDispatch (commands5.go:586) alongside `scale` / `rename`
// as a sibling subcommand — slug is the first positional across
// all three leaves (cmdAppScale / cmdAppRename take it as a
// separate string param; cmdAppSecurity follows the same shape
// so the dispatcher can route uniformly).
//
// Closed-set gate: `--require-signed=` accepts only the literal
// strings "true" / "false" (lowercase). Anything else is rejected
// locally before round-tripping. The handler also re-parses the
// strict literal (handlers_ext.go:1074-1093), so a fast-fail here
// matches the server-side gate and saves a round-trip.
//
// Auth: Bearer + MFA + Admin scope (server-side, ADR-054 §6). The
// customer PATCH /v1/apps/{slug} path silently drops require_signed,
// so this CLI route is the only way to flip the bit short of the
// admin dashboard.
//
// Output:
//   - --json: writeJSON(resp) — preserves the envelope so the new
//     RequireSigned value survives.
//   - human: one-line "Updated" + the new boolean. The previous
//     value is intentionally not fetched (extra GET cost); if the
//     operator needs the prior state, `gregale app <slug>` shows it.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// subSecurity is the verb name for `gregale app security ...`. Lives
// in this file (not commands2.go's const block) because it's a Tier D
// addition; co-located so the dispatcher edit (commands5.go:597)
// and the verb name live one Edit apart for reviewers.
const subSecurity = "security"

// subRoutes is the verb name for `gregale apps routes ...` /
// `gregale app <slug> routes ...` (ADR-093 Tier B item #2). Lives
// in commands_app_routes.go alongside the cmdAppsRoutes leaf, mirroring
// the subSecurity co-location pattern; the dispatcher arms import
// this const rather than re-declaring it so goconst stays quiet and
// the verb name has a single source of truth.
const subRoutes = "routes"

// requireSignedTrue / requireSignedFalse are the strict literal
// values the --require-signed flag accepts. The handler enforces the
// same closed enum (handlers_ext.go:1074-1093), so we mirror it
// locally as a fast-fail gate. Lifted to named consts so goconst
// (golangci-lint v2.4.0) stops flagging the three repeated string
// literals in the gate below.
const (
	requireSignedTrue  = "true"
	requireSignedFalse = "false"
)

// cmdAppSecurity implements `gregale app <slug> security
// [--require-signed=true|false]`. The only flag for now is
// require_signed (ADR-054 §Decision 2); future per-app security
// bits (trusted-publisher allowlist mutation, deploy-time SBOM
// enforcement) extend the AppSecurityRequest DTO + this leaf's
// flag set together. Don't grow the leaf until the DTO grows.
//
// Signature mirrors cmdAppScale / cmdAppRename: slug is the first
// positional, threaded in by cmdAppDispatch (commands5.go:607) so
// the dispatcher can route the three subcommand leaves uniformly
// (slug, args[2:]).
func cmdAppSecurity(slug string, args []string) int {
	if slug == "" {
		PrintUsage(os.Stderr, "usage: gregale app <slug> security [--posture|--require-signed=true|false]", "apps")
		return 1
	}
	// splitArgsForFlags: Go's flag.Parse halts at the first non-flag
	// positional, so `gregale app demo security --require-signed=false`
	// would silently drop --require-signed=false if we parsed args
	// directly. The reorder helper pulls the flag to the front so
	// the parser sees it. Mirrors cmdDelayedTaskAdd (commands_delayed_task.go:118).
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("app security", flag.ContinueOnError)
	requireSigned := fs.String("require-signed", "", "require signed images on deploy (true|false)")
	posture := fs.Bool("posture", false, "show the read-only security posture")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 0 {
		PrintUsage(os.Stderr, "usage: gregale app <slug> security [--posture|--require-signed=true|false]", "apps")
		return 1
	}
	if *posture && *requireSigned != "" {
		return printErr("Invalid app security flags", fmt.Errorf("--posture cannot be combined with --require-signed"))
	}
	// --require-signed is parsed as a string so the strict literal
	// "true" / "false" gate can run before strconv.ParseBool (which
	// accepts "1", "t", "True", etc. — those would silently flip
	// the server-side enum gate).
	if *requireSigned != "" && *requireSigned != requireSignedTrue && *requireSigned != requireSignedFalse {
		return printErr("Invalid --require-signed", fmt.Errorf("must be \"true\" or \"false\"; got %q", *requireSigned))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *posture {
		resp, err := client.GetAppSecurity(context.Background(), slug)
		if err != nil {
			return printErr("Security posture failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(resp))
		}
		PrintOK(osStdout, "App %s security posture: %s (score %d/100)", slug, resp.Profile, resp.Score)
		for _, finding := range resp.Findings {
			_, _ = fmt.Fprintf(osStdout, "  [%s] %s: %s\n", finding.Severity, finding.Title, finding.Remediation)
		}
		return 0
	}
	req := api.AppSecurityRequest{}
	if *requireSigned != "" {
		v, _ := strconv.ParseBool(*requireSigned)
		req.RequireSigned = &v
	}
	resp, err := client.UpdateAppSecurity(context.Background(), slug, req)
	if err != nil {
		return printErr("Update failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "App %s security updated.", slug)
	_, _ = fmt.Fprintf(osStdout, "  require_signed: %t\n", resp.RequireSigned)
	return 0
}
