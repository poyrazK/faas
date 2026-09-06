// commands5.go — UX §3.1 commands that landed in issue #63:
//
//   gregale ps <app>          instances + state (humanizes parked → sleeping)
//   gregale status            personal SLO snapshot from GET /status/slo.json
//   gregale env pull|push     local .env <-> sealed secrets (key-only pull per §11/G2)
//   gregale app <slug> scale  per-app scale knobs (--ram/--max-concurrency/--idle/--min)
//   gregale app <slug> rename atomic slug swap (full-stack: server + state + CLI)
//   gregale app <slug> restart park + fresh snapshot + wake
//   gregale plan <plan>       top-level plan change (account-scoped)
//   gregale dashboard         opens the account-level dashboard in the browser
//
// main.go wires the top-level dispatch; cmdAppDispatch (here) routes
// the `gregale app` subcommand form. Reuses the authedClient / printErr
// helpers from commands.go and the dashboard URL helpers from
// commands2.go.

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/browser"
	"github.com/onebox-faas/faas/pkg/secretscan"
)

// `gregale app` subcommand names — lifted to constants so goconst stops
// flagging the repeated "scale"/"rename" string literals across the
// dispatch table (cmdAppDispatch) and the usage hints. subSecurity
// lives in commands_app_security.go (its verb constant is colocated
// with the leaf it dispatches to); subRoutes follows the same
// colocated pattern in commands_app_routes.go.
const (
	subScale   = "scale"
	subRename  = "rename"
	subRestart = "restart"
)

// validCLISlug matches the server-side validSlug regex in cmd/apid/handlers.go.
// Duplicated here so the CLI can reject malformed slugs before paying a
// network round-trip — the server still re-validates as defence in depth.
func validCLISlug(s string) bool {
	if len(s) < 3 || len(s) > 40 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
			if i == 0 || i == len(s)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// --- ps --------------------------------------------------------------------

// cmdPS lists an app's live instances + state. The schema state
// vocabulary is parked | waking | cold_booting | running | snapshotting |
// stopped | failed (migrations/00001_init.sql:85). Parked instances
// are rendered as "sleeping" because that's how the dashboard badge
// (§6) talks about them to humans — the wire value stays unchanged.
func cmdPS(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale ps <app>", "ps")
		return 1
	}
	slug := args[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ins, err := client.ListInstances(context.Background(), slug)
	if err != nil {
		return printErr("Could not list instances", err)
	}
	if jsonOutput {
		// NDJSON: empty slice renders as zero lines (no header), which
		// `jq -c '.'` handles gracefully.
		return jsonOut(writeNDJSON(ins))
	}
	if len(ins) == 0 {
		_, _ = fmt.Fprintf(osStdout, "%s: no instances (app is parked)\n", slug)
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "%-36s %-12s %6s %-20s %-20s %-36s\n", "ID", "STATE", "RAM_MB", "STARTED", "LAST_REQUEST", "WAKE_ID")
	for _, i := range ins {
		_, _ = fmt.Fprintf(osStdout, "%-36s %-12s %6d %-20s %-20s %-36s\n",
			i.ID, humanizeInstanceState(i.State), i.RAMMB,
			i.StartedAt, i.LastRequestAt, i.WakeID)
	}
	return 0
}

// humanizeInstanceState maps the wire-level state string to a
// user-friendly rendering. The full vocabulary lives in
// pkg/state/machine.go:14-26 (parked / waking / cold_booting / running
// / snapshotting / stopped / failed) — issue #63 §1 lists the
// customer-facing subset (running | cold-booting | waking | sleeping |
// parked).
//
// Two translations:
//
//	parked → sleeping    (the dashboard badge wording; §6 uses the
//	                      euphemism so customers don't see a
//	                      stop-anxiety signal)
//	cold_booting → cold-booting  (snake → kebab so it reads as a
//	                              single hyphenated word, matching the
//	                              spec)
//
// All other states render verbatim — waking, running, snapshotting,
// stopped, failed — they read naturally and any silent rename would
// hide the wire vocabulary from operators tailing `gregale ps`.
func humanizeInstanceState(state string) string {
	switch state {
	case "parked":
		return "sleeping"
	case "cold_booting":
		return "cold-booting"
	}
	return state
}

// --- status ----------------------------------------------------------------

// cmdStatus prints the personal SLO snapshot from GET /status/slo.json.
// The endpoint is unauthenticated (spec §12 public status page), so a
// fresh CLI without a stored token still works. With a token, the
// numbers are the same fleet-wide ones; personal account SLOs land in
// a follow-up.
//
// --json (issue #63 §2) emits the raw api.StatusPage so pipelines can
// jq the SLO numbers. JSON tag set lives on the struct in
// pkg/api/dto.go — renames there propagate here automatically.
func cmdStatus(args []string) int {
	fs := flag.NewFlagSet(statusLiteral, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit raw api.StatusPage as JSON (issue #63 §2)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale status [--json]", "status")
		return 1
	}
	// Use the raw Client (not authedClient) so the public endpoint
	// works without a stored token. The Client still sends the bearer
	// header if present; apid mounts /status/slo.json on the PUBLIC
	// mux (server.go:359) before any auth middleware, so the token is
	// never inspected.
	client := NewClient(apiBase(), loadToken())
	page, err := client.GetStatusSLO(context.Background())
	if err != nil {
		return printErr("Status failed", err)
	}
	if *asJSON || jsonOutput {
		// Per-command --json (PR #66) OR top-level --json (issue #64 D1).
		// Marshal via the shared writeJSON helper so the DTO JSON tags
		// in pkg/api/dto.go are the single source of truth.
		return jsonOut(writeJSON(page))
	}
	_, _ = fmt.Fprintf(osStdout, "availability: %.2f%%\n", page.APIAvailabilityPct)
	_, _ = fmt.Fprintf(osStdout, "wake p95:     %.0f ms\n", page.WakeP95MS)
	_, _ = fmt.Fprintf(osStdout, "builds ok:    %.2f%%\n", page.BuildSuccessPct)
	_, _ = fmt.Fprintf(osStdout, "as of:        %s\n", page.AsOf.Format("2006-01-02 15:04:05 UTC"))
	_, _ = fmt.Fprintf(osStdout, "source:       %s\n", page.Source)
	return 0
}

// --- env -------------------------------------------------------------------

// cmdEnv dispatches `gregale env pull|push --app <slug>`. The pull path
// writes a KEY-only .env template (empty values) per the §11/G2
// sealed-secrets boundary — the server never returns plaintext. The
// push path re-uses the secrets API PUT with the same rotation-hint
// flow as `gregale secrets set`.
func cmdEnv(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale env <pull|push> --app <slug>", "env")
		return 1
	}
	switch args[0] {
	case "pull":
		return envPull(args[1:])
	case "push":
		return envPush(args[1:])
	case "diff":
		// ADR-117 PR-C: env-diff matrix as a text table.
		// Renders the same wire surface as GET /v1/apps/{slug}/env-diff
		// (the handler added in Commit 11). Security: secret
		// cells never reveal plaintext; env cells emit the
		// literal value (env is public). The renderer lives
		// in commands_env_diff.go.
		return envDiff(args[1:])
	}
	fmt.Fprintf(os.Stderr, "gregale env: unknown subcommand %q\n", args[0])
	return 1
}

func envPull(args []string) int {
	fs := flag.NewFlagSet("env pull", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	out := fs.String("o", ".env", "output file (default .env)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *app == "" {
		PrintUsage(os.Stderr, "usage: gregale env pull --app <slug> [-o .env]", "env")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListSecrets(context.Background(), *app)
	if err != nil {
		return printErr("List failed", err)
	}
	var b strings.Builder
	for _, s := range resp.Secrets {
		// KEY-only template: the G2 boundary (§11) means the server
		// never returns plaintext, so we intentionally write an empty
		// value. The customer fills values by hand before `env push`.
		fmt.Fprintf(&b, "%s=\n", s.Key)
	}
	if err := os.WriteFile(*out, []byte(b.String()), 0o600); err != nil {
		return printErr("Could not write .env", err)
	}
	if resp.Count == 0 {
		PrintOK(osStdout, "Wrote empty %s (%s has no secrets)", *out, *app)
		return 0
	}
	PrintOK(osStdout, "Wrote %d key(s) to %s (values intentionally blank — fill by hand)",
		resp.Count, *out)
	return 0
}

func envPush(args []string) int {
	fs := flag.NewFlagSet("env push", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	in := fs.String("f", ".env", "input file (default .env)")
	fromStdin := fs.Bool("from-stdin", false, "read KEY=VALUE pairs from stdin (one per line)")
	// --secret-scan mirrors the deploy-side flag. Default ON because the
	// failure mode (a Stripe key pasted into a `gregale env push`
	// heredoc) is the same as the deploy-side case — the value lands in
	// the platform's sealed secret store where the customer expects
	// "SECRET_KEY", not "my live Stripe key". Override with
	// --secret-scan=off for local-dev sandbox keys (e.g. sk_test_…).
	secretScan := fs.String("secret-scan", "on", "scan pairs for known credential patterns before pushing (on|off|strict|source-tree; default on)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	secretScanMode, secretScanErr := parseSecretScanFlag(*secretScan)
	if secretScanErr != nil {
		PrintFail(os.Stderr, "%s", secretScanErr)
		return 1
	}
	if *app == "" {
		PrintUsage(os.Stderr, "usage: gregale env push --app <slug> [-f .env | --from-stdin]", "env")
		return 1
	}
	if *fromStdin && *in != ".env" {
		// fs.Changed isn't available pre-Go-1.21 in some toolchains;
		// the default for -f is ".env", so anything else means the
		// customer explicitly named a file. Mutually exclusive with
		// --from-stdin so we never read both.
		PrintFail(os.Stderr, "--from-stdin and -f are mutually exclusive")
		return 1
	}
	type pair struct{ k, v string }
	var pairs []pair
	if *fromStdin {
		// Issue #63 §3: respect the --from-stdin semantics already used
		// by `gregale secrets set` (commands3.go:92). Tests pipe a string
		// into osStdin (commands5_test.go); customers pipe a heredoc
		// or process substitution. Same line cap (64 KB) — Scale's
		// SecretValueMaxBytes (32 KB) plus the key name fits, anything
		// larger truncates and the apid byte cap rejects.
		scanner := bufio.NewScanner(osStdin)
		scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			p, err := parseSecretsPair(line)
			if err != nil {
				return printErr("Bad stdin line", err)
			}
			pairs = append(pairs, pair{k: p.Key, v: p.Value})
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
			return printErr("Read stdin", err)
		}
	} else {
		f, err := openCustomerFile(*in)
		if err != nil {
			return printErr("Could not read .env", err)
		}
		defer func() { _ = f.Close() }()
		// Reuse parseSecretsPair from commands3.go (single '=' split, same
		// edge cases). Skip blanks + comments ourselves so the parser sees
		// only candidate lines.
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			p, err := parseSecretsPair(line)
			if err != nil {
				return printErr("Bad .env line", err)
			}
			pairs = append(pairs, pair{k: p.Key, v: p.Value})
		}
		if err := scanner.Err(); err != nil {
			return printErr("Read .env", err)
		}
	}
	if len(pairs) == 0 {
		PrintFail(os.Stderr, "no KEY=VALUE pairs in input")
		return 1
	}
	// Secret-scan pass: scan the parsed pairs (in-memory; no file I/O
	// because the values are already in hand) for known credential
	// patterns. Findings cause the pair to be dropped from the upload
	// and a stderr warning to be rendered. The origin string is the
	// input path or "<stdin>" so the warning says `File: <stdin>` for
	// pipe input (matches how envPush surfaces other parse errors).
	if secretScanMode.isScanEnabled() {
		origin := *in
		if *fromStdin {
			origin = "<stdin>"
		}
		scanPairs := make([]secretscan.Pair, len(pairs))
		for i, p := range pairs {
			scanPairs[i] = secretscan.Pair{Key: p.k, Value: p.v}
		}
		findings := secretscan.ScanEnvPairs(scanPairs, origin)
		// Strict mode: same shape as deploy's StrictSecretScanError so
		// the printErr dispatcher renders the unified 422 envelope.
		// ScanEnvPairs already stamps a 1-indexed Line on each
		// Finding (the pair-index in the .env file), so we forward
		// it as-is rather than rewriting Line=0 — the JSON envelope's
		// `secret_findings[].line` and the text-mode `:N` renderer
		// both depend on the contract documented in
		// pkg/secretscan/scan.go.
		if secretScanMode.isStrict() && len(findings) > 0 {
			wireFindings := make([]secretscan.Finding, 0, len(findings))
			for _, f := range findings {
				wireFindings = append(wireFindings, secretscan.Finding{
					File:     origin,
					Line:     f.Line,
					Key:      f.Key,
					Provider: f.Provider,
					Severity: f.Severity,
					Snippet:  f.Snippet,
				})
			}
			return printErr("Secret scan rejected the push",
				&StrictSecretScanError{Findings: wireFindings,
					Hint: "move detected secrets to `gregale secrets set` (see " + cliDocsURL + ")"})
		}
		if len(findings) > 0 {
			renderEnvPushScanWarnings(findings, osStderr)
			// Build a drop set keyed by (key, value) so a duplicate
			// KEY with a different VALUE pair is treated distinctly —
			// the pair table is small (handful of entries per .env
			// file) so a linear scan is fine.
			drop := make(map[int]bool, len(findings))
			for _, f := range findings {
				if f.Line >= 1 && f.Line <= len(pairs) {
					drop[f.Line-1] = true
				}
			}
			kept := pairs[:0]
			for i, p := range pairs {
				if !drop[i] {
					kept = append(kept, p)
				}
			}
			pairs = kept
		}
	}
	if len(pairs) == 0 {
		PrintFail(os.Stderr, "all KEY=VALUE pairs contained detected secrets; none were pushed. Use --secret-scan=off to override")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	// Same rotation-hint flow as secretsSet (commands3.go).
	existing := map[string]bool{}
	if list, err := client.ListSecrets(context.Background(), *app); err == nil {
		for _, s := range list.Secrets {
			existing[s.Key] = true
		}
	}
	rotated := 0
	for _, p := range pairs {
		if existing[p.k] {
			rotated++
		}
	}
	if rotated > 0 {
		_, _ = fmt.Fprintf(osStdout,
			"note: %d secret(s) already existed and are being rotated.\n"+
				"  Any parked snapshots still hold the previous plaintext until the next wake.\n"+
				"  Deploy, or call `gregale wake %s`, to force an overstamp.\n",
			rotated, *app)
	}
	for _, p := range pairs {
		if err := client.SetSecret(context.Background(), *app, p.k, p.v); err != nil {
			return printErr("Set "+p.k+" failed", err)
		}
		PrintOK(osStdout, "%s set", p.k)
	}
	return 0
}

// renderEnvPushScanWarnings is the envPush-side twin of
// renderSecretScanWarnings in commands2.go. Same two-line shape per
// finding, but the summary line ends with "pushed." rather than
// "upload." because envPush ships to the platform secret store, not a
// tarball. Stderr-only so `gregale env push --json | jq` keeps a clean
// stdout JSON envelope.
//
// Each line:
//
//	! Secret detected in .env:12 (STRIPE_SECRET_KEY → stripe_live, high)
//	  ↳ sk_liv…p7dc
//
// Summary line (only if any findings fired):
//
//	! 1 secret(s) skipped from the push. Move to: skip
func renderEnvPushScanWarnings(findings []secretscan.Finding, w io.Writer) {
	if len(findings) == 0 {
		return
	}
	for _, f := range findings {
		PrintWarn(w, "Secret detected in %s:%d (%s → %s, %s)",
			f.File, f.Line, f.Key, f.Provider, f.Severity)
		// Same convention as renderSecretScanWarnings in commands2.go:
		// the snippet is a continuation line, Fprintf errors discarded.
		_, _ = fmt.Fprintf(w, "  ↳ %s\n", f.Snippet)
	}
	PrintWarn(w, "%d secret(s) skipped from the push.", len(findings))
}

// openCustomerFile opens any customer-supplied file path with defense
// against symlink-mediated content exfiltration. Used by both:
//
//	gregale env push -f .env --app x        (cmdEnvPush)
//	gregale deploy --tarball source.tar.gz  (Client.DeployTarball)
//
// Without this guard, a customer could `ln -s /etc/passwd .env` (or
// any other readable file) and then `gregale env push -f .env --app x`.
// The scanner would feed the file's lines through parseSecretsPair;
// anything matching KEY=VALUE would be PUT to the server. The parallel
// `gregale deploy --tarball` attack is byte-exfiltration: whatever the
// symlink points at gets streamed verbatim into the multipart source
// part and ends up in the build artefact the builder microVM ingests.
//
// Two checks:
//  1. Lstat the FINAL component. If it's a symlink, the kernel would
//     follow it on Open — refuse before opening.
//  2. After Open, Lstat again on the resolved path. If a symlink was
//     swapped in between (TOCTOU race), the second Lstat catches it.
//     Also confirms the file is a regular file, not a device or FIFO.
//
// Note 1: we intentionally don't EvalSymlinks the whole path and
// compare strings. On macOS, /var is itself a symlink to /private/var
// (a system-level layout quirk), so EvalSymlinks("/var/folders/...")
// returns "/private/var/folders/..." even for plain files. Comparing
// strings there would reject every legitimate customer file on macOS
// dev boxes. Lstat-on-the-final-component catches the actual attack
// (a symlink AT the path the customer named) without false-positives.
// The same reasoning applies to the tarball call site — a customer
// may legitimately point `--tarball` at /var/folders/.../foo.tar.gz.
//
// Note 2: both call sites — cmdEnvPush (env push) and
// Client.DeployTarball (deploy --tarball) — now share this helper.
// If you add a third caller that ships customer bytes over the wire,
// route it through here too; duplicating the Lstat discipline is
// what causes TOCTOU drift.
func openCustomerFile(path string) (*os.File, error) {
	absIn, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("could not absolutize %q: %w", path, err)
	}
	// Pre-open: refuse if the final component is a symlink.
	preInfo, err := os.Lstat(absIn)
	if err != nil {
		return nil, err
	}
	if preInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing to follow symlink at %q", path)
	}
	// The Lstat guards above (pre-open + post-open) ARE the security
	// boundary that the .golangci.yml forbidigo rule exists to enforce.
	// This call site is the documented escape hatch: any other os.Open
	// in this repo is a tripwire and must route through a vetted
	// helper. The pre-open + post-open Lstat discipline below is the
	// security boundary; the bare Open on the next line is necessary.
	//nolint:forbidigo // openCustomerFile IS the security boundary — pre-open + post-open Lstat discipline above is what makes os.Open safe here.
	f, err := os.Open(absIn)
	if err != nil {
		return nil, err
	}
	// Post-open: confirm the path is still a regular file. Catches
	// TOCTOU swaps (someone `ln -sf`'ing the path between our preInfo
	// check and the Open) and refuses devices / FIFOs / directories.
	postInfo, err := os.Lstat(absIn)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if postInfo.Mode()&os.ModeSymlink != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("refusing to follow symlink at %q (raced after open)", path)
	}
	if !postInfo.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("refusing non-regular file %q (mode %s)", path, postInfo.Mode())
	}
	return f, nil
}

// --- app scale / rename (called from cmdAppDispatch) ------------------------

// cmdAppScale is the subcommand form of `gregale app <slug> scale ...`.
// Mirrors cmdApp (commands2.go:53-126) but with no --plan — plan
// changes live on `gregale plan`. Uses the same fs.Visit pattern so 0 is
// distinguishable from "unset".
func cmdAppScale(slug string, args []string) int {
	fs := flag.NewFlagSet("app scale", flag.ContinueOnError)
	ram := fs.Int("ram", 0, "update RAM (MB)")
	cpuMillicores := fs.Int("cpu-millicores", 0, "update sustained CPU allowance (250, 500, or 1000 millicores)")
	profile := fs.String("profile", "", "update named resource profile: micro|small|medium|large|xlarge")
	conc := fs.Int("max-concurrency", 0, "update max concurrent requests")
	idle := fs.Int("idle", 0, "update idle timeout (seconds)")
	min := fs.Int("min", 0, "min instances kept warm (Pro/Scale only; 0 = scale to zero)")
	rps := fs.Int("autoscale-target-rps", 0, "per-instance RPS target for reactive scale-up (Hobby+/0 = disable)")
	cpu := fs.Int("autoscale-target-cpu-pct", 0, "per-instance CPU%% target for reactive scale-up (Pro+ only; 1-100; 0 = disable)")
	// Issue #470 / PR C / ADR-074: warm-snapshot opt-in flags.
	// Mirror commands2.go:cmdApp so `gregale app <slug> scale --warm-snapshot`
	// is the canonical ES-2.4 form.
	warm := fs.Bool("warm-snapshot", false, "enable warm-snapshot tier (Pro/Scale only)")
	noWarm := fs.Bool("no-warm-snapshot", false, "disable warm-snapshot tier")
	warmMinReq := fs.Int("warm-snapshot-min-requests", 0, "warm-snapshot min-request gate (1..100; 0 = use server default)")
	warmMinMs := fs.Int("warm-snapshot-min-ms", 0, "warm-snapshot min-ms-since-ready gate (100..60000; 0 = use server default)")
	// Issue #560: per-deployment token gate. Mirror commands2.go:cmdApp
	// so the canonical `gregale app <slug> scale --require-authn` form
	// keeps parity with the top-level `gregale app <slug> --require-authn`
	// form. Pro/Scale only — the API rejects Free/Hobby with 403
	// plan_require_authn_not_allowed, which surfaces as a "Scale failed"
	// error with the API's problem code.
	requireAuthn := fs.Bool("require-authn", false, "require Authorization: Bearer <token> on every request (Pro/Scale only)")
	noRequireAuthn := fs.Bool("no-require-authn", false, "drop the token requirement; back to public-by-default")
	// ADR-124: per-app wire-protocol selector (scale path).
	// Mirrors commands2.go:cmdApp — single string flag, empty
	// value = no change. Free + grpc = 403 server-side.
	appProtocol := fs.String("app-protocol", "", "wire-protocol selector: http1|http2|grpc (omit to leave unchanged)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *warm && *noWarm {
		return printErr("Invalid flags", fmt.Errorf("--warm-snapshot and --no-warm-snapshot are mutually exclusive"))
	}
	// Issue #560: mutual exclusion check for the require-authn pair.
	// Mirrors the warm-snapshot guard above — symmetric flag pairs
	// intentionally use a usage error (not a silent last-one-wins) so
	// the customer sees the conflict instead of an unexpected PATCH.
	// The plan gate runs server-side; the CLI's job is to keep the
	// flag pair consistent.
	if *requireAuthn && *noRequireAuthn {
		return printErr("Invalid flags", fmt.Errorf("--require-authn and --no-require-authn are mutually exclusive"))
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	var req api.UpdateAppRequest
	if explicit["ram"] {
		v := *ram
		req.RAMMB = &v
	}
	if explicit["cpu-millicores"] {
		v := *cpuMillicores
		req.CPUMillicores = &v
	}
	if explicit["profile"] {
		req.ResourceProfile = profile
	}
	if explicit["max-concurrency"] {
		v := *conc
		req.MaxConcurrency = &v
	}
	if explicit["idle"] {
		v := *idle
		req.IdleTimeoutS = &v
	}
	if explicit["min"] {
		v := *min
		req.MinInstances = &v
	}
	if explicit["autoscale-target-rps"] {
		v := *rps
		req.AutoscaleTargetRPS = &v
	}
	if explicit["autoscale-target-cpu-pct"] {
		v := *cpu
		req.AutoscaleTargetCPUPct = &v
	}
	if explicit["warm-snapshot"] {
		v := true
		req.WarmSnapshotEnabled = &v
	}
	if explicit["no-warm-snapshot"] {
		v := false
		req.WarmSnapshotEnabled = &v
	}
	if explicit["warm-snapshot-min-requests"] {
		v := *warmMinReq
		req.WarmSnapshotMinRequests = &v
	}
	if explicit["warm-snapshot-min-ms"] {
		v := *warmMinMs
		req.WarmSnapshotMinMs = &v
	}
	// Issue #560: require-authn pair coalesces to a single *bool on
	// the wire so the apid side sees one canonical field. Each flag
	// of the pair sets an explicit value; the no-op guard below
	// checks `req.RequireAuthn == nil` to keep an unrelated `scale`
	// invocation (e.g. `--ram`) on the Update path.
	if explicit["require-authn"] {
		v := true
		req.RequireAuthn = &v
	}
	if explicit["no-require-authn"] {
		v := false
		req.RequireAuthn = &v
	}
	// ADR-124: per-app wire-protocol selector. Closed-set
	// validation mirrors commands2.go:cmdApp — local check
	// surfaces a usage error before the round-trip.
	if explicit["app-protocol"] {
		v := *appProtocol
		if !api.IsValidAppProtocol(v) {
			return printErr("Invalid --app-protocol",
				fmt.Errorf("must be 'http1', 'http2', or 'grpc'; got %q", v))
		}
		req.AppProtocol = &v
	}
	if req.RAMMB == nil && req.CPUMillicores == nil && req.ResourceProfile == nil && req.MaxConcurrency == nil &&
		req.IdleTimeoutS == nil && req.MinInstances == nil &&
		req.AutoscaleTargetRPS == nil && req.AutoscaleTargetCPUPct == nil &&
		req.WarmSnapshotEnabled == nil && req.WarmSnapshotMinRequests == nil && req.WarmSnapshotMinMs == nil &&
		req.RequireAuthn == nil && req.AppProtocol == nil {
		PrintUsage(os.Stderr, "usage: gregale app <slug> scale [--profile micro|small|medium|large|xlarge] [--ram N] [--cpu-millicores 250|500|1000] [--max-concurrency N] [--idle SEC] [--min N] [--autoscale-target-rps N] [--autoscale-target-cpu-pct N] [--warm-snapshot] [--no-warm-snapshot] [--warm-snapshot-min-requests N] [--warm-snapshot-min-ms N] [--require-authn] [--no-require-authn] [--app-protocol http1|http2|grpc]", "apps")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	updated, err := client.UpdateApp(context.Background(), slug, req)
	if err != nil {
		return printErr("Scale failed", err)
	}
	PrintOK(osStdout, "Updated")
	if explicit["min"] && *min > 0 {
		// Silent on Whoami failure (mid-rotation token, transient
		// API blip). The cost echo is a transparency affordance;
		// don't surface an unrelated auth issue right after a
		// successful update.
		if acct, err := client.Whoami(context.Background()); err == nil {
			printResidentCostEcho(api.Plan(acct.Plan), updated.RAMMB, *min)
		}
	}
	return 0
}

// cmdAppRename swaps an app's slug atomically. The server validates the
// new slug (same regex as CreateApp) and returns 409 CodeAppRenameFailed
// on collisions, which client.go surfaces as APIError.
func cmdAppRename(slug, newSlug string) int {
	if !validCLISlug(newSlug) {
		PrintFail(os.Stderr, "invalid slug (3-40 chars, lowercase letters/digits/hyphens, no leading/trailing hyphen)")
		return 1
	}
	if newSlug == slug {
		// Idempotent no-op so the customer can re-run safely.
		PrintOK(osStdout, "%s already has that slug", slug)
		return 0
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	updated, err := client.RenameApp(context.Background(), slug, newSlug)
	if err != nil {
		return printErr("Rename failed", err)
	}
	// The mid-string `→` here is a semantic from-to arrow (rename
	// from old slug to new slug), not the §3.2 "in-progress" symbol.
	// It stays as a literal even when stdout is not a TTY: in pipes
	// and CI logs the arrow is still load-bearing for distinguishing
	// "old" from "new". The leading glyph goes through PrintOK so the
	// §3.2 NO_COLOR rule still applies to the status-indicator half.
	PrintOK(osStdout, "Renamed %s → %s", slug, updated.Slug)
	return 0
}

// cmdAppRestart requests a fresh snapshot restart for an app. The server
// performs the park and replacement wake asynchronously and returns a wake id
// for correlation with the wake timeline.
func cmdAppRestart(slug string, args []string) int {
	if len(args) != 0 {
		PrintUsage(os.Stderr, "usage: gregale app <slug> restart", "apps")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.RestartApp(context.Background(), slug)
	if err != nil {
		return printErr("Restart failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Restart requested (wake_id=%s)", out.WakeID)
	return 0
}

// cmdAppDispatch routes `gregale app <slug> ...` to either the new
// subcommand form (scale / rename / security / routes) or the legacy
// flag-form (`gregale app <slug> --ram N`, `gregale app <slug>`).
// Pulled out of main.go so the switch stays small.
func cmdAppDispatch(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale app <slug> [scale|rename <new>|restart|security [--require-signed=true|false]|routes|streaming-cap|--ram N|--max-concurrency N|--idle SEC|--min N]", "apps")
		return 1
	}
	slug := args[0]
	if len(args) >= 2 {
		switch args[1] {
		case subScale:
			return cmdAppScale(slug, args[2:])
		case subRename:
			if len(args) != 3 {
				PrintUsage(os.Stderr, "usage: gregale app <slug> rename <new-slug>", "apps")
				return 1
			}
			return cmdAppRename(slug, args[2])
		case subRestart:
			return cmdAppRestart(slug, args[2:])
		case subSecurity:
			return cmdAppSecurity(slug, args[2:])
		case subRoutes:
			return cmdAppsRoutes(slug, args[2:])
		case subStreamingCap:
			return cmdAppsStreamingCap(slug, args[2:])
		case subStaticEgressIP:
			return cmdAppStaticEgressIP(slug, args[2:])
		}
	}
	// Backwards-compat: legacy flag-form dispatch is the existing cmdApp.
	return cmdApp(args)
}

// --- plan ------------------------------------------------------------------

// planRank assigns an ordinal for downgrade-detection. We only
// confirm on paid→downgrade transitions because going free→paid or
// hobby→pro is harmless; the Stripe webhook handles the money side
// regardless.
var planRank = map[api.Plan]int{
	api.PlanFree:  0,
	api.PlanHobby: 1,
	api.PlanPro:   2,
	api.PlanScale: 3,
}

// cmdPlan is `gregale plan <plan>`. Validates the plan name against the
// 4 known constants, then asks Whoami to check the current plan and
// prompts for y/N on paid→downgrade transitions.
func cmdPlan(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale plan <free|hobby|pro|scale>", "plan")
		return 1
	}
	target := api.Plan(args[0])
	if !target.Valid() {
		PrintFail(os.Stderr, "unknown plan %q (expected: free|hobby|pro|scale)", args[0])
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	acct, err := client.Whoami(context.Background())
	if err != nil {
		return printErr("Could not fetch account", err)
	}
	if acct.Plan != "" && planRank[api.Plan(acct.Plan)] > planRank[target] {
		fmt.Fprintf(os.Stderr,
			"Downgrade from %s to %s: existing apps may exceed the new plan's limits. "+
				"Continue? [y/N] ", acct.Plan, target)
		var ans string
		_, _ = fmt.Scanln(&ans)
		if strings.ToLower(strings.TrimSpace(ans)) != "y" {
			_, _ = fmt.Fprintln(osStdout, "aborted")
			return 1
		}
	}
	updated, err := client.ChangePlan(context.Background(), string(target))
	if err != nil {
		// 402 with a checkout / portal URL is not a failure: the provider
		// must confirm the paid upgrade, and the URL is where the customer
		// does that. Hand off instead of printing a bare error.
		var ae *APIError
		if errors.As(err, &ae) && ae.Problem.Status == http.StatusPaymentRequired &&
			(ae.Problem.CheckoutURL != "" || ae.Problem.BillingPortalURL != "") {
			return renderPlanCheckoutHandoff(ae, target)
		}
		return printErr("Plan change failed", err)
	}
	if updated.PlanChangeStatus == "pending_provider_confirmation" {
		if updated.EffectiveAt != nil {
			PrintOK(osStdout, "Plan change to %s scheduled for %s; current plan remains %s",
				updated.RequestedPlan, updated.EffectiveAt.Format(time.RFC3339), updated.Plan)
		} else {
			PrintOK(osStdout, "Plan change to %s scheduled; current plan remains %s",
				updated.RequestedPlan, updated.Plan)
		}
		return 0
	}
	PrintOK(osStdout, "Plan changed to %s", updated.Plan)
	return 0
}

// --- dashboard -------------------------------------------------------------

// cmdDashboard opens the account-level dashboard in the browser. Same
// fallback-to-URL pattern as the (now-removed) M7.5 repo-picker
// browser flow — the URL is always printed so a missing $DISPLAY
// degrades gracefully. Tests substitute browser.Default via
// withRecorder.
//
// --stateless (Move 1 PR-A) opens /dashboard/stateless instead — the
// customer-facing landing page for the stateless contract (the
// contract copy, the 8-base denylist, the 10 closed paths, and the
// account's 50 most recent advisory rows). The flag exists so a
// customer who just got an advisory row in their terminal can
// land on the explanation page without clicking through the
// account nav.
//
// Exit code on browser-open failure: 0, intentionally. The URL is
// printed to stderr so the customer can paste it into a browser
// themselves — the work the customer asked for (giving them the
// dashboard URL) is done. Mirrors the (now-removed) repo-picker
// fallback pattern and matches the §11
// "open the URL, fall back gracefully" UX convention. Exit 1 here
// would make CI scripts and `&&`-chained shell commands treat a
// missing $DISPLAY as a hard failure, which is the wrong signal.
func cmdDashboard(args []string) int {
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	stateless := fs.Bool("stateless", false, "open the stateless-advisory landing page instead of the account page")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale dashboard [--stateless]", "dashboard")
		return 1
	}
	if _, err := authedClient(); err != nil {
		return printErr("Not logged in", err)
	}
	target := dashboardAccountURL(apiBase())
	if *stateless {
		target = dashboardStatelessURL(apiBase())
	}
	_, _ = fmt.Fprintf(osStdout, "Opening %s\n", target)
	if err := browser.Open(target); err != nil {
		PrintFail(os.Stderr, "Could not open browser: %v", err)
		fmt.Fprintf(os.Stderr, "  Open this URL manually:\n  %s\n", target)
		return 0
	}
	return 0
}

// --- tail / queue tail -----------------------------------------------------

// cmdQueueDispatch routes `gregale queue <sub>` to the right handler.
// Subcommands (Tier C, issue #394):
//
//	tail         long-poll /v1/events for queue rows (existing)
//	send         enqueue one payload via POST /v1/apps/{slug}/queues/send
//	receive      drain the next row via POST .../queues/receive
//	state        depth + cap via GET .../queues/state
//	peek         inspect up to N rows without draining
//	dead-letter  rows that exhausted attempts
//	ack          release a leased row
func cmdQueueDispatch(args []string) int {
	parent, _ := lookupCliCommand("queue")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale queue <subcommand> <slug> [args]\n\n"+
			"  tail <slug>            long-poll the unified event stream (queue drain signals)\n"+
			"  send <slug> --payload J enqueue one row\n"+
			"  receive <slug>         drain the next row (blocks)\n"+
			"  state <slug>            depth + cap (no lease)\n"+
			"  peek <slug> [--limit N] inspect up to N rows without draining\n"+
			"  dead-letter <slug>     rows that exhausted attempts\n"+
			"  ack <slug> <row-id>    release a leased row\n",
			"queue")
		return 1
	}
	switch args[0] {
	case "tail":
		return cmdQueueTail(args[1:])
	case "send":
		return cmdQueueSend(args[1:])
	case "receive":
		return cmdQueueReceive(args[1:])
	case "state":
		return cmdQueueState(args[1:])
	case "peek":
		return cmdQueuePeek(args[1:])
	case "dead-letter":
		return cmdQueueDeadLetter(args[1:])
	case "ack":
		return cmdQueueAck(args[1:])
	default:
		sug, _ := suggestSubcommand(args[0], parent)
		PrintUsage(os.Stderr, "usage: gregale queue <subcommand> <slug> [args]\n\n"+
			"  tail <slug>            long-poll the unified event stream\n"+
			"  send <slug> --payload J enqueue one row\n"+
			"  receive <slug>         drain the next row\n"+
			"  state <slug>            depth + cap\n"+
			"  peek <slug> [--limit N] inspect without draining\n"+
			"  dead-letter <slug>     rows that exhausted attempts\n"+
			"  ack <slug> <row-id>    release a leased row\n",
			"queue")
		maybeSuggestSub(sug)
		return 1
	}
}

// cmdQueueSend enqueues one payload. Mirrors cmdInvoke's
// --payload semantics (inline JSON / @file / stdin).
func cmdQueueSend(args []string) int {
	fs := flag.NewFlagSet("queue send", flag.ContinueOnError)
	payload := fs.String("payload", "", "JSON payload (inline | @file | -)")
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		PrintUsage(os.Stderr, "usage: gregale queue send <slug> --payload <json|@file|->", "queue")
		return 1
	}
	slug := pos[0]
	body, err := resolveQueuePayload(*payload)
	if err != nil {
		return printErr("Invalid payload", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.QueueSend(context.Background(), slug, api.QueueSendRequest{Payload: body})
	if err != nil {
		return printErr("Queue send failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Enqueued row %s on %s.", resp.ID, slug)
	return 0
}

// cmdQueueReceive drains the next row. The server long-polls up to
// the SDK-side timeout; a 204 No Content (empty queue) returns
// `empty`. Mirrors the existing cmdQueueTail long-poll framing —
// same signal.NotifyContext wiring.
func cmdQueueReceive(args []string) int {
	fs := flag.NewFlagSet("queue receive", flag.ContinueOnError)
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		PrintUsage(os.Stderr, "usage: gregale queue receive <slug>", "queue")
		return 1
	}
	slug := pos[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	resp, err := client.QueueReceive(ctx, slug)
	if err != nil {
		// Long-poll timeout is the "empty queue" sentinel — the
		// server waited the full window and nothing arrived. SDK
		// raises ErrLongPollTimeout (pkg/api/errors.go:2036-2047)
		// which is *api.Problem{Code:"long_poll_timeout",
		// Status:504}. Treat as a successful empty result so
		// polling shell loops exit cleanly.
		var p *api.Problem
		if errors.As(err, &p) && p.Code == "long_poll_timeout" {
			if jsonOutput {
				return jsonOut(writeJSON(api.QueueReceiveResponse{}))
			}
			_, _ = fmt.Fprintln(osStdout, "(empty)")
			return 0
		}
		return printErr("Queue receive failed", err)
	}
	if resp.ID == "" {
		// Empty branch — server returned 204 / no row available.
		// --json consumers MUST receive valid JSON (jq contract);
		// human consumers get a one-line hint so they don't think
		// the queue is broken.
		if jsonOutput {
			return jsonOut(writeJSON(resp))
		}
		_, _ = fmt.Fprintln(osStdout, "(empty)")
		return 0
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Row %s leased.", resp.ID)
	if len(resp.Payload) > 0 {
		_, _ = fmt.Fprintln(osStdout, string(resp.Payload))
	}
	return 0
}

// cmdQueueState returns the depth + cap without acquiring a lease.
// Read-only; safe to call on a hot path.
func cmdQueueState(args []string) int {
	fs := flag.NewFlagSet("queue state", flag.ContinueOnError)
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		PrintUsage(os.Stderr, "usage: gregale queue state <slug>", "queue")
		return 1
	}
	slug := pos[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.QueueState(context.Background(), slug)
	if err != nil {
		return printErr("Queue state failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	fmt.Printf("app:        %s\n", resp.AppSlug)
	fmt.Printf("plan:       %s\n", resp.Plan)
	fmt.Printf("plan_cap:   %d\n", resp.PlanCap)
	fmt.Printf("depth:      %d\n", resp.Depth)
	fmt.Printf("in_flight:  %d\n", resp.InFlight)
	if resp.OldestPendingAt != nil {
		fmt.Printf("oldest_pending_at: %s\n", resp.OldestPendingAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	return 0
}

// cmdQueuePeek inspects up to N rows without acquiring a lease.
// Cursor pagination via --before mirrors cmdAuditEventsList.
func cmdQueuePeek(args []string) int {
	fs := flag.NewFlagSet("queue peek", flag.ContinueOnError)
	limit := fs.Int("limit", 50, "max rows (1..100)")
	before := fs.String("before", "", "pagination cursor (NextBefore from a prior call)")
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		PrintUsage(os.Stderr, "usage: gregale queue peek <slug> [--limit N] [--before C]", "queue")
		return 1
	}
	slug := pos[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.QueuePeek(context.Background(), slug, *limit, *before)
	if err != nil {
		return printErr("Queue peek failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Messages) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no rows peekable)")
		return 0
	}
	for _, m := range resp.Messages {
		fmt.Printf("%-32s %d attempts  %s\n", m.ID, m.Attempts, m.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	return 0
}

// cmdQueueDeadLetter lists rows that exhausted attempts. Same
// pagination shape as cmdQueuePeek (cursor via --before).
func cmdQueueDeadLetter(args []string) int {
	fs := flag.NewFlagSet("queue dead-letter", flag.ContinueOnError)
	limit := fs.Int("limit", 50, "max rows (1..100)")
	before := fs.String("before", "", "pagination cursor")
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		PrintUsage(os.Stderr, "usage: gregale queue dead-letter <slug> [--limit N] [--before C]", "queue")
		return 1
	}
	slug := pos[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.QueueDeadLetter(context.Background(), slug, *limit, *before)
	if err != nil {
		return printErr("Queue dead-letter failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Messages) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no dead-letter rows)")
		return 0
	}
	for _, m := range resp.Messages {
		fmt.Printf("%-32s %d attempts  failed %s  err=%q\n", m.ID, m.Attempts, m.FailedAt.Format("2006-01-02T15:04:05Z07:00"), m.LastError)
	}
	return 0
}

// cmdQueueAck releases a leased row. Idempotent on already-released
// ids — server returns 200. Use after cmdQueueReceive to free the
// row before the lease expires.
func cmdQueueAck(args []string) int {
	fs := flag.NewFlagSet("queue ack", flag.ContinueOnError)
	flags, pos := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 2 {
		PrintUsage(os.Stderr, "usage: gregale queue ack <slug> <row-id>", "queue")
		return 1
	}
	slug := pos[0]
	id := pos[1]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.AckQueueRow(context.Background(), slug, id); err != nil {
		return printErr("Queue ack failed", err)
	}
	PrintOK(osStdout, "Row %s acked.", id)
	return 0
}

// resolveQueuePayload is a local alias for resolvePayload — kept
// here so future queue-payload semantics (e.g. message deduplication
// keys) don't have to import the invoke leaf.
func resolveQueuePayload(s string) ([]byte, error) { return resolvePayload(s) }

// splitArgsForFlags accepts the leaf's args slice and returns a
// reordered copy with every `--flag value` (or `--flag=value`) pair
// pulled to the FRONT. Go's stdlib flag.Parse stops at the first
// non-flag token; without this helper the help text "queue send
// <slug> --payload J" silently drops the payload. The reorder is
// a one-pass scan: positional tokens land in `pos`, flag tokens
// (and their values, when separated) land in `flags`. Bool flags
// (`--async`) and bare `--` markers pass through unchanged.
//
// `--` is treated as "everything after this is positional" so the
// queue subcommand can carry messages that themselves contain
// leading dashes (rare but possible for JSON payloads starting
// with `-`).
func splitArgsForFlags(args []string) (flags, pos []string) {
	flags = make([]string, 0, len(args))
	pos = make([]string, 0, len(args))
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			return
		}
		if len(a) >= 2 && a[0] == '-' && a[1] == '-' {
			// --flag=value: one token, both halves intact.
			if eq := indexByte(a, '='); eq >= 0 {
				flags = append(flags, a)
				i++
				continue
			}
			// --flag value: peek the next token; if it's not
			// flag-shaped it belongs to this flag.
			flags = append(flags, a)
			if i+1 < len(args) && !looksLikeFlag(args[i+1]) {
				flags = append(flags, args[i+1])
				i += 2
				continue
			}
			i++
			continue
		}
		pos = append(pos, a)
		i++
	}
	return
}

// looksLikeFlag returns true when a token begins with `-` and is
// more than a single dash (avoiding negative-number false-positives
// on positional int args). Used by splitArgsForFlags to decide
// whether the token after `--flag` is the flag's value or the next
// flag.
func looksLikeFlag(s string) bool {
	return len(s) >= 2 && s[0] == '-'
}

// indexByte is a strings.IndexByte alias kept local to this file to
// avoid pulling strings into commands5.go's import block for one
// call site. (Future maintainer: if more byte-search helpers land
// here, promote to strings.IndexByte and drop this.)
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// cmdTail subscribes to /v1/events and prints one line per
// `invocation_done` frame: "<invocation_id> <app_slug> <state>". With
// `--include-stateless`, also prints one line per `stateless_advisory`
// frame: "stateless <app_id> <n> <sample_path>". Exits 130 on Ctrl-C
// so a chained shell command can detect a deliberate interrupt
// (POSIX 128 + SIGINT(2)); exits 0 on a clean SSE close.
//
// Authentication is the dashboard Bearer token; apid's
// eventsFrameForAccount filter (handlers_events.go:148-167) ensures the
// caller only sees their own account's frames. There is no escalation
// path — a customer's `gregale tail` will never see another customer's
// invocations or stateless advisories.
//
// Move 3 / M7.5 prep: the dashboard uses the same /v1/events route via
// the browser EventSource; this command is the CLI twin.
//
// Wave 0 PR-C / ADR-047: `--include-stateless` surfaces the runtime
// stateless-advisory signal the same way the dashboard's "Stateless
// advisories" tab does. Default OFF — the common `gregale tail` use case
// is watching invocations, and advisory frames are noisy (one per
// debounce window per state-shaped path).
func cmdTail(args []string) int {
	fs := flag.NewFlagSet("tail", flag.ContinueOnError)
	onlySlug := fs.String("app", "", "filter to a single app slug (optional)")
	includeStateless := fs.Bool("include-stateless", false, "also print stateless.advisory frames (default: hide)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale tail [--app <slug>] [--include-stateless]", "tail")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	body, err := client.StreamEvents(ctx)
	if err != nil {
		return printErr("Could not open events stream", err)
	}
	defer func() { _ = body.Close() }()

	dec := api.NewDecoder(body)
	dec.SetCloseFn(body.Close)
	defer func() { _ = dec.Close() }()

	if *includeStateless {
		_, _ = fmt.Fprintln(osStdout, "Tailing invocations + stateless advisories… Ctrl-C to exit.")
	} else {
		// Move 1 PR-A: stamp a discoverability hint so a customer
		// who hits the tail and gets only invocation lines knows
		// stateless advisories are a separate stream with their
		// own flag. Keeps the default invocation list un-cluttered.
		_, _ = fmt.Fprintln(osStdout, "Tailing invocations… Ctrl-C to exit.")
		_, _ = fmt.Fprintln(osStdout, "Tip: pass --include-stateless to also see stateless advisories from your app's audit row stream.")
	}
	for {
		select {
		case <-ctx.Done():
			return 130
		case e, ok := <-dec.Events():
			if !ok {
				return 0
			}
			switch e.Event {
			case "invocation_done":
				var p struct {
					InvocationID string `json:"invocation_id"`
					AppID        string `json:"app_id"`
					AppSlug      string `json:"app_slug"`
					State        string `json:"state"`
				}
				if err := json.Unmarshal([]byte(e.Data), &p); err != nil {
					// Unparseable frame — print raw so the customer
					// can see it; the next frame is independent.
					_, _ = fmt.Fprintln(osStdout, e.Data)
					continue
				}
				if *onlySlug != "" && p.AppSlug != *onlySlug && p.AppID != *onlySlug {
					continue
				}
				display := p.AppSlug
				if display == "" {
					display = p.AppID
				}
				_, _ = fmt.Fprintf(osStdout, "%s %s %s\n", p.InvocationID, display, p.State)
			case "stateless_advisory":
				if !*includeStateless {
					continue
				}
				var p struct {
					AppID      string `json:"app_id"`
					Instance   string `json:"instance"`
					N          int    `json:"n"`
					SamplePath string `json:"sample_path"`
				}
				if err := json.Unmarshal([]byte(e.Data), &p); err != nil {
					_, _ = fmt.Fprintln(osStdout, e.Data)
					continue
				}
				_, _ = fmt.Fprintf(osStdout, "stateless %s %d %s\n", p.AppID, p.N, p.SamplePath)
			}
		case err := <-dec.Errors():
			if err != nil && !errors.Is(err, io.EOF) {
				PrintWarn(os.Stderr, "stream closed: %v", err)
				return 3
			}
			return 0
		}
	}
}

// cmdQueueTail long-polls POST /v1/apps/{slug}/queues/invocations:receive
// and prints one line per dequeued row: "<id> <payload-or-pretty>". On
// the 30s server-side timeout the client sleeps 500 ms and retries; on
// Ctrl-C the context cancels and the in-flight HTTP request aborts
// within ~50 ms.
//
// This is the customer's "watch the queue drain" surface for §4.5
// webhook-style use cases: enqueue rows from any producer, tail on
// the consumer side, see payloads land in real time without polling
// the invocations read API.
//
// Move 3 / M7.5 prep: pairs with gregale queue send; together they form
// the UX surface for queues without forcing the customer to learn
// long-poll semantics.
func cmdQueueTail(args []string) int {
	if len(args) != 1 {
		PrintUsage(os.Stderr, "usage: gregale queue tail <slug>", "queue")
		return 1
	}
	slug := args[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	_, _ = fmt.Fprintf(osStdout, "Tailing queue for %s… Ctrl-C to exit.\n", slug)
	for {
		select {
		case <-ctx.Done():
			return 130
		default:
		}
		// Use a per-call deadline slightly under the server-side 30s
		// cap so a hung connection returns before Ctrl-C has to wait
		// for the OS TCP keepalive. 25s leaves headroom for the
		// apid-side pg_notify fan-in latency.
		callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		row, err := client.QueueReceive(callCtx, slug)
		cancel()
		if err != nil {
			if isLongPollTimeout(err) {
				// 30s with no rows — normal idle, retry.
				continue
			}
			if errors.Is(err, context.Canceled) {
				return 130
			}
			PrintWarn(os.Stderr, "queue receive failed: %v", err)
			return 3
		}
		payload := strings.TrimSpace(string(row.Payload))
		if payload == "" || !json.Valid(row.Payload) {
			_, _ = fmt.Fprintf(osStdout, "%s %s\n", row.ID, payload)
		} else {
			var pretty any
			if json.Unmarshal(row.Payload, &pretty) == nil {
				buf, _ := json.MarshalIndent(pretty, "", "  ")
				_, _ = fmt.Fprintf(osStdout, "%s %s\n", row.ID, string(buf))
			} else {
				_, _ = fmt.Fprintf(osStdout, "%s %s\n", row.ID, payload)
			}
		}
	}
}

// isLongPollTimeout recognises the apid ErrLongPollTimeout sentinel
// without importing the package's internal Problem type — we only
// need the *APIError.Problem.Code to discriminate.
func isLongPollTimeout(err error) bool {
	var ae *api.APIError
	if errors.As(err, &ae) {
		return ae.Problem.Code == "long_poll_timeout"
	}
	return false
}
